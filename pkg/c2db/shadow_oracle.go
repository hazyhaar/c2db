// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// EntryKey identifie formellement un enregistrement par sa collection/table et sa clé primaire.
type EntryKey struct {
	Table string
	Key   string
}

type tableColInfo struct {
	cid       int
	name      string
	colType   string
	notnull   bool
	dfltValue sql.NullString
	pk        int // > 0 si fait partie de la clé primaire
}

// ShadowOracle orchestre le shadow testing miroir entre SQLite et c2db.
// Il garantit l'invariance bit-exacte, la conservation des schémas stricts,
// l'absence d'ambiguïté des types et l'assertion directe des résultats relus.
type ShadowOracle struct {
	sourceDB   *sql.DB
	mirrorDB   *sql.DB
	mirrorPath string
	c2db       *DB
	tables     []string
	tableCols  map[string][]tableColInfo
	tablePKs   map[string][]string
	pkeys      []EntryKey
}

// NewShadowOracle initialise une session de Shadow Testing à partir d'une base SQLite réelle.
func NewShadowOracle(dbPath string) (*ShadowOracle, error) {
	dsn := dbPath
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)"
	} else if !strings.Contains(dsn, "busy_timeout") {
		dsn += "&_pragma=busy_timeout(10000)"
	}
	sourceDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open source DB: %w", err)
	}

	// Découverte des tables utilisateur en ignorant les tables internes et virtuelles FTS
	rows, err := sourceDB.Query("SELECT name, sql FROM sqlite_master WHERE type='table'")
	if err != nil {
		sourceDB.Close()
		return nil, fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	tableDDL := make(map[string]string)
	for rows.Next() {
		var name string
		var ddl sql.NullString
		if err := rows.Scan(&name, &ddl); err != nil {
			continue
		}
		if name == "" || strings.HasPrefix(name, "sqlite_") || strings.Contains(name, "_fts_") || strings.HasSuffix(name, "_fts") {
			continue
		}
		tables = append(tables, name)
		if ddl.Valid {
			tableDDL[name] = ddl.String
		}
	}

	if len(tables) == 0 {
		sourceDB.Close()
		return nil, fmt.Errorf("no user tables found in source DB")
	}

	sort.Strings(tables)

	// Inspection détaillée des schémas de chaque table
	tableCols := make(map[string][]tableColInfo)
	tablePKs := make(map[string][]string)
	for _, t := range tables {
		pRows, err := sourceDB.Query(fmt.Sprintf("PRAGMA table_info(\"%s\")", t))
		if err != nil {
			sourceDB.Close()
			return nil, fmt.Errorf("pragma table_info %s: %w", t, err)
		}
		var cols []tableColInfo
		var pks []tableColInfo
		for pRows.Next() {
			var c tableColInfo
			if err := pRows.Scan(&c.cid, &c.name, &c.colType, &c.notnull, &c.dfltValue, &c.pk); err != nil {
				pRows.Close()
				sourceDB.Close()
				return nil, fmt.Errorf("scan colinfo %s: %w", t, err)
			}
			cols = append(cols, c)
			if c.pk > 0 {
				pks = append(pks, c)
			}
		}
		pRows.Close()
		tableCols[t] = cols

		sort.Slice(pks, func(i, j int) bool {
			return pks[i].pk < pks[j].pk
		})
		var pkNames []string
		for _, p := range pks {
			pkNames = append(pkNames, p.name)
		}
		tablePKs[t] = pkNames
	}

	// Répertoire miroir temporaire
	mirrorDir, err := os.MkdirTemp("", "shadow-oracle-*")
	if err != nil {
		sourceDB.Close()
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	mirrorPath := filepath.Join(mirrorDir, "mirror.db")

	mirrorDB, err := sql.Open("sqlite", mirrorPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		os.RemoveAll(mirrorDir)
		sourceDB.Close()
		return nil, fmt.Errorf("open mirror DB: %w", err)
	}

	// Recréation exacte des tables dans mirrorDB à partir de leurs DDL sources stricts
	for _, t := range tables {
		ddl := tableDDL[t]
		if ddl == "" {
			ddl = fmt.Sprintf("CREATE TABLE \"%s\" (id TEXT PRIMARY KEY, data BLOB)", t)
		}
		if _, err := mirrorDB.Exec(ddl); err != nil {
			mirrorDB.Close()
			sourceDB.Close()
			os.RemoveAll(mirrorDir)
			return nil, fmt.Errorf("create mirror table %s: %w (ddl: %s)", t, err, ddl)
		}
	}

	// Initialisation de c2db avec 16 shards
	c2dbDir := filepath.Join(mirrorDir, "c2db")
	if err := os.MkdirAll(c2dbDir, 0o700); err != nil {
		mirrorDB.Close()
		sourceDB.Close()
		os.RemoveAll(mirrorDir)
		return nil, fmt.Errorf("create c2db dir: %w", err)
	}
	for i := uint16(0); i < 16; i++ {
		shardDir := filepath.Join(c2dbDir, fmt.Sprintf("%04x", i))
		if err := os.MkdirAll(shardDir, 0o700); err != nil {
			mirrorDB.Close()
			sourceDB.Close()
			os.RemoveAll(mirrorDir)
			return nil, fmt.Errorf("create shard dir %d: %w", i, err)
		}
	}

	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 1)
	}
	c2dbInstance, err := OpenDB(c2dbDir, mac)
	if err != nil {
		mirrorDB.Close()
		sourceDB.Close()
		os.RemoveAll(mirrorDir)
		return nil, fmt.Errorf("open c2db: %w", err)
	}
	c2dbInstance.SetBusyTimeout(5 * time.Second)

	return &ShadowOracle{
		sourceDB:   sourceDB,
		mirrorDB:   mirrorDB,
		mirrorPath: mirrorPath,
		c2db:       c2dbInstance,
		tables:     tables,
		tableCols:  tableCols,
		tablePKs:   tablePKs,
		pkeys:      nil,
	}, nil
}

// Tags de types SQL canoniques (1 octet) pour encodage TLV strictement injectif.
const (
	tagNull    byte = 0x00
	tagInteger byte = 0x01
	tagFloat   byte = 0x02
	tagText    byte = 0x03
	tagBlob    byte = 0x04
)

type colValPair struct {
	name string
	val  interface{}
}

// CanonicalRowBytes sérialise de façon déterministe et strictement injective une ligne SQL
// en format binaire canonique TLV (Type-Length-Value).
// Zéro collision possible entre BLOB et TEXT, ni entre entier et flottant.
func CanonicalRowBytes(cols []string, vals []interface{}) ([]byte, error) {
	if len(cols) != len(vals) {
		return nil, fmt.Errorf("len(cols)=%d != len(vals)=%d", len(cols), len(vals))
	}
	pairs := make([]colValPair, len(cols))
	for i := range cols {
		pairs[i] = colValPair{name: cols[i], val: vals[i]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].name < pairs[j].name
	})

	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(pairs)))
	buf.Write(lenBuf[:])

	for _, p := range pairs {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(p.name)))
		buf.Write(lenBuf[:])
		buf.WriteString(p.name)

		switch v := p.val.(type) {
		case nil:
			buf.WriteByte(tagNull)
		case int64:
			buf.WriteByte(tagInteger)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			buf.Write(b[:])
		case int:
			buf.WriteByte(tagInteger)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			buf.Write(b[:])
		case float64:
			buf.WriteByte(tagFloat)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
			buf.Write(b[:])
		case string:
			buf.WriteByte(tagText)
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(v)))
			buf.Write(lenBuf[:])
			buf.WriteString(v)
		case []byte:
			buf.WriteByte(tagBlob)
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(v)))
			buf.Write(lenBuf[:])
			buf.Write(v)
		default:
			str := fmt.Sprintf("%v", v)
			buf.WriteByte(tagText)
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(str)))
			buf.Write(lenBuf[:])
			buf.WriteString(str)
		}
	}
	return buf.Bytes(), nil
}

// EncodePrimaryKey sérialise les composants d'une clé primaire en format binaire TLV
// strictement typé et injectif, préservant les types réels (BLOB, TEXT, INTEGER, FLOAT)
// et éliminant toute collision de délimiteur ou de conversion de chaîne.
func EncodePrimaryKey(parts []interface{}) (string, error) {
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(parts)))
	buf.Write(lenBuf[:])

	for _, p := range parts {
		switch v := p.(type) {
		case nil:
			buf.WriteByte(tagNull)
		case int64:
			buf.WriteByte(tagInteger)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			buf.Write(b[:])
		case int:
			buf.WriteByte(tagInteger)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			buf.Write(b[:])
		case float64:
			buf.WriteByte(tagFloat)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
			buf.Write(b[:])
		case string:
			buf.WriteByte(tagText)
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(v)))
			buf.Write(lenBuf[:])
			buf.WriteString(v)
		case []byte:
			buf.WriteByte(tagBlob)
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(v)))
			buf.Write(lenBuf[:])
			buf.Write(v)
		default:
			return "", fmt.Errorf("type de clé primaire non supporté: %T", v)
		}
	}
	return buf.String(), nil
}

// DecodePrimaryKey désérialise les composants de la clé primaire avec leurs types Go natifs stricts.
func DecodePrimaryKey(s string) ([]interface{}, error) {
	b := []byte(s)
	if len(b) < 4 {
		return nil, fmt.Errorf("pk trop courte: len=%d", len(b))
	}
	numParts := int(binary.BigEndian.Uint32(b[:4]))
	b = b[4:]
	parts := make([]interface{}, 0, numParts)
	for i := 0; i < numParts; i++ {
		if len(b) < 1 {
			return nil, fmt.Errorf("part %d tag manquant", i)
		}
		tag := b[0]
		b = b[1:]
		switch tag {
		case tagNull:
			parts = append(parts, nil)
		case tagInteger:
			if len(b) < 8 {
				return nil, fmt.Errorf("part %d integer tronqué", i)
			}
			val := int64(binary.BigEndian.Uint64(b[:8]))
			b = b[8:]
			parts = append(parts, val)
		case tagFloat:
			if len(b) < 8 {
				return nil, fmt.Errorf("part %d float tronqué", i)
			}
			bits := binary.BigEndian.Uint64(b[:8])
			b = b[8:]
			parts = append(parts, math.Float64frombits(bits))
		case tagText:
			if len(b) < 4 {
				return nil, fmt.Errorf("part %d text header tronqué", i)
			}
			tLen := int(binary.BigEndian.Uint32(b[:4]))
			b = b[4:]
			if len(b) < tLen {
				return nil, fmt.Errorf("part %d text body tronqué: len=%d < %d", i, len(b), tLen)
			}
			parts = append(parts, string(b[:tLen]))
			b = b[tLen:]
		case tagBlob:
			if len(b) < 4 {
				return nil, fmt.Errorf("part %d blob header tronqué", i)
			}
			bLen := int(binary.BigEndian.Uint32(b[:4]))
			b = b[4:]
			if len(b) < bLen {
				return nil, fmt.Errorf("part %d blob body tronqué: len=%d < %d", i, len(b), bLen)
			}
			blobCopy := make([]byte, bLen)
			copy(blobCopy, b[:bLen])
			parts = append(parts, blobCopy)
			b = b[bLen:]
		default:
			return nil, fmt.Errorf("part %d tag inconnu: 0x%02x", i, tag)
		}
	}
	return parts, nil
}

// Replicate ingère exhaustivement toutes les lignes réelles des tables cibles
// et les écrit simultanément dans SQLite miroir et dans c2db.
func (so *ShadowOracle) Replicate() error {
	so.pkeys = nil
	for _, table := range so.tables {
		if err := so.replicateTable(table); err != nil {
			return fmt.Errorf("replicate table %s: %w", table, err)
		}
	}
	return nil
}

func (so *ShadowOracle) replicateTable(table string) error {
	cols := so.tableCols[table]
	pkNames := so.tablePKs[table]
	if len(cols) == 0 {
		return fmt.Errorf("aucune colonne trouvée pour %s", table)
	}

	colNames := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = "\"" + c.name + "\""
		placeholders[i] = "?"
	}

	// Création de la collection c2db
	shardID := uint16(0)
	s, err := so.c2db.GetShard(shardID)
	if err != nil {
		return fmt.Errorf("get shard 0: %w", err)
	}
	if err := s.CreateCollection(table); err != nil && !errors.Is(err, ErrCollectionExists) {
		return fmt.Errorf("create collection %s: %w", table, err)
	}

	// Lecture de toutes les lignes source
	querySQL := fmt.Sprintf("SELECT %s FROM \"%s\"", strings.Join(colNames, ", "), table)
	rows, err := so.sourceDB.Query(querySQL)
	if err != nil {
		return fmt.Errorf("query source %s: %w", table, err)
	}
	defer rows.Close()

	insertSQL := fmt.Sprintf("INSERT OR REPLACE INTO \"%s\" (%s) VALUES (%s)",
		table, strings.Join(colNames, ", "), strings.Join(placeholders, ", "))
	stmt, err := so.mirrorDB.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare mirror insert %s: %w", table, err)
	}
	defer stmt.Close()

	for rows.Next() {
		rawVals := make([]interface{}, len(cols))
		scanTargets := make([]interface{}, len(cols))
		for i := range rawVals {
			scanTargets[i] = &rawVals[i]
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return fmt.Errorf("scan row %s: %w", table, err)
		}

		// Résolution de la clé primaire canonique typée (préservation exacte des types SQL)
		var pkParts []interface{}
		if len(pkNames) > 0 {
			for _, pkCol := range pkNames {
				for i, c := range cols {
					if c.name == pkCol {
						pkParts = append(pkParts, rawVals[i])
						break
					}
				}
			}
		} else {
			// Repli sur première colonne
			pkParts = append(pkParts, rawVals[0])
		}
		primaryKey, err := EncodePrimaryKey(pkParts)
		if err != nil || primaryKey == "" {
			return fmt.Errorf("clé primaire vide ou invalide sur table %s: %w", table, err)
		}

		// 1. Insertion miroir dans SQLite
		if _, err := stmt.Exec(rawVals...); err != nil {
			return fmt.Errorf("mirror insert %s pk=%s: %w", table, primaryKey, err)
		}

		// 2. Sérialisation canonique et insertion dans c2db
		plainColNames := make([]string, len(cols))
		for i, c := range cols {
			plainColNames[i] = c.name
		}
		canonicalBytes, err := CanonicalRowBytes(plainColNames, rawVals)
		if err != nil {
			return fmt.Errorf("canonical row bytes %s pk=%s: %w", table, primaryKey, err)
		}

		if err := s.PutIn(table, []byte(primaryKey), canonicalBytes); err != nil {
			return fmt.Errorf("c2db PutIn %s pk=%s: %w", table, primaryKey, err)
		}

		so.pkeys = append(so.pkeys, EntryKey{Table: table, Key: primaryKey})
	}

	return rows.Err()
}

// ValidateReadConsistency relit chaque ligne depuis SQLite miroir et depuis c2db,
// et asserte l'égalité bit-à-bit stricte directe entre les deux moteurs.
// Zéro divergence tolérée.
func (so *ShadowOracle) ValidateReadConsistency() error {
	shardID := uint16(0)
	s, err := so.c2db.GetShard(shardID)
	if err != nil {
		return fmt.Errorf("get shard 0: %w", err)
	}

	// 1. Contrôle d'exhaustivité triple : source vs miroir vs c2db
	tableCounts := make(map[string]int)
	for _, entry := range so.pkeys {
		tableCounts[entry.Table]++
	}

	for _, table := range so.tables {
		expectedCount := tableCounts[table]
		var sourceCount, mirrorCount int
		if err := so.sourceDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM \"%s\"", table)).Scan(&sourceCount); err != nil {
			return fmt.Errorf("source count table %s: %w", table, err)
		}
		if err := so.mirrorDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM \"%s\"", table)).Scan(&mirrorCount); err != nil {
			return fmt.Errorf("mirror count table %s: %w", table, err)
		}
		if sourceCount != mirrorCount {
			return fmt.Errorf("exhaustivité source vs miroir rompue sur table %s: source=%d mirror=%d", table, sourceCount, mirrorCount)
		}
		if mirrorCount != expectedCount {
			return fmt.Errorf("exhaustivité pkeys vs miroir rompue sur table %s: mirror=%d attendu=%d", table, mirrorCount, expectedCount)
		}

		c2Keys, err := s.Scan(table)
		if err != nil {
			return fmt.Errorf("c2db scan table %s: %w", table, err)
		}
		if len(c2Keys) != expectedCount {
			return fmt.Errorf("exhaustivité rompue sur table %s: c2db scan=%d attendu=%d", table, len(c2Keys), expectedCount)
		}
	}

	// 2. Relecture et comparaison directe triple bit-exacte (source == miroir == c2db)
	for _, entry := range so.pkeys {
		cols := so.tableCols[entry.Table]
		pkNames := so.tablePKs[entry.Table]

		colNames := make([]string, len(cols))
		plainColNames := make([]string, len(cols))
		for i, c := range cols {
			colNames[i] = "\"" + c.name + "\""
			plainColNames[i] = c.name
		}

		pkParts, err := DecodePrimaryKey(entry.Key)
		if err != nil {
			return fmt.Errorf("decode pk table %s: %w", entry.Table, err)
		}

		var whereClause string
		var whereArgs []interface{}
		if len(pkNames) > 0 && len(pkNames) == len(pkParts) {
			var clauses []string
			for i, pkCol := range pkNames {
				clauses = append(clauses, fmt.Sprintf("\"%s\" = ?", pkCol))
				whereArgs = append(whereArgs, pkParts[i])
			}
			whereClause = strings.Join(clauses, " AND ")
		} else {
			whereClause = fmt.Sprintf("\"%s\" = ?", cols[0].name)
			whereArgs = append(whereArgs, pkParts[0])
		}

		querySQL := fmt.Sprintf("SELECT %s FROM \"%s\" WHERE %s",
			strings.Join(colNames, ", "), entry.Table, whereClause)

		// Relecture fraîche depuis SQLite source
		srcRow := so.sourceDB.QueryRow(querySQL, whereArgs...)
		srcVals := make([]interface{}, len(cols))
		srcScanTargets := make([]interface{}, len(cols))
		for i := range srcVals {
			srcScanTargets[i] = &srcVals[i]
		}
		if err := srcRow.Scan(srcScanTargets...); err != nil {
			return fmt.Errorf("relecture source SQLite table %s key %s: %w", entry.Table, entry.Key, err)
		}
		srcBytes, err := CanonicalRowBytes(plainColNames, srcVals)
		if err != nil {
			return fmt.Errorf("canonicalisation source SQLite table %s key %s: %w", entry.Table, entry.Key, err)
		}

		// Relecture fraîche depuis SQLite miroir
		row := so.mirrorDB.QueryRow(querySQL, whereArgs...)
		mirrorVals := make([]interface{}, len(cols))
		scanTargets := make([]interface{}, len(cols))
		for i := range mirrorVals {
			scanTargets[i] = &mirrorVals[i]
		}
		if err := row.Scan(scanTargets...); err != nil {
			return fmt.Errorf("relecture mirror SQLite table %s key %s: %w", entry.Table, entry.Key, err)
		}
		mirrorBytes, err := CanonicalRowBytes(plainColNames, mirrorVals)
		if err != nil {
			return fmt.Errorf("canonicalisation mirror SQLite table %s key %s: %w", entry.Table, entry.Key, err)
		}

		// Contrôle de parité bit-exacte source vs miroir
		if !bytes.Equal(srcBytes, mirrorBytes) {
			diff := computeDiff(srcBytes, mirrorBytes)
			return fmt.Errorf("DIVERGENCE constatée pour table %s key %s:\nsource=%s\nmiroir=%s\ndiff  =%s",
				entry.Table, entry.Key, hex.EncodeToString(srcBytes), hex.EncodeToString(mirrorBytes), diff)
		}

		// Relecture fraîche depuis c2db
		c2dbBytes, err := s.GetFrom(entry.Table, []byte(entry.Key))
		if err != nil {
			return fmt.Errorf("relecture c2db table %s key %s: %w", entry.Table, entry.Key, err)
		}

		// ASSERTION DIRECTE : comparaison bit-à-bit c2db vs SQLite miroir (et source)
		if !bytes.Equal(c2dbBytes, mirrorBytes) {
			diff := computeDiff(c2dbBytes, mirrorBytes)
			return fmt.Errorf("DIVERGENCE constatée pour table %s key %s:\nc2db  =%s\nsqlite=%s\ndiff  =%s",
				entry.Table, entry.Key, hex.EncodeToString(c2dbBytes), hex.EncodeToString(mirrorBytes), diff)
		}
	}

	return nil
}

// Close ferme proprement les ressources SQLite et c2db.
func (so *ShadowOracle) Close() error {
	var errs []string
	if err := so.sourceDB.Close(); err != nil {
		errs = append(errs, fmt.Sprintf("close source: %v", err))
	}
	if err := so.mirrorDB.Close(); err != nil {
		errs = append(errs, fmt.Sprintf("close mirror: %v", err))
	}
	if err := so.c2db.Close(); err != nil {
		errs = append(errs, fmt.Sprintf("close c2db: %v", err))
	}
	if err := os.RemoveAll(so.mirrorPath); err != nil {
		errs = append(errs, fmt.Sprintf("remove mirror dir: %v", err))
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func computeDiff(a, b []byte) string {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		if a[i] != b[i] {
			return fmt.Sprintf("octet %d: c2db=0x%02x sqlite=0x%02x", i, a[i], b[i])
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("longueur différente: c2db=%d octets, sqlite=%d octets", len(a), len(b))
	}
	return "identiques"
}
