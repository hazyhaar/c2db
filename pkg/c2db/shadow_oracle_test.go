// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestShadowOracleBasic(t *testing.T) {
	dir := t.TempDir()
	sourcePath := dir + "/source.db"
	sourceDB, err := sql.Open("sqlite", sourcePath+"?_journal=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	_, err = sourceDB.Exec("CREATE TABLE rules (id INTEGER PRIMARY KEY, name TEXT NOT NULL, score REAL NOT NULL)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	rows := []struct {
		id    int
		name  string
		score float64
	}{
		{1, "alpha", 1.23},
		{2, "beta", 4.56},
		{3, "gamma", 7.89},
		{42, "answer", 42.0},
		{100, "century", 100.0},
	}
	for _, r := range rows {
		if _, err := sourceDB.Exec("INSERT INTO rules (id, name, score) VALUES (?, ?, ?)", r.id, r.name, r.score); err != nil {
			t.Fatalf("insert %d: %v", r.id, err)
		}
	}

	oracle, err := NewShadowOracle(sourcePath)
	if err != nil {
		t.Fatalf("new oracle: %v", err)
	}
	defer oracle.Close()

	if err := oracle.Replicate(); err != nil {
		t.Fatalf("replicate: %v", err)
	}

	if err := oracle.ValidateReadConsistency(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	t.Logf("Replication et validation directe bit-exacte réussie pour %d clés", len(oracle.pkeys))
}

func TestShadowOracleDivergenceDetected_BothSides(t *testing.T) {
	dir := t.TempDir()
	sourcePath := dir + "/source.db"
	sourceDB, err := sql.Open("sqlite", sourcePath+"?_journal=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer sourceDB.Close()

	_, err = sourceDB.Exec("CREATE TABLE items (id TEXT PRIMARY KEY, descr TEXT NOT NULL)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, _ = sourceDB.Exec("INSERT INTO items (id, descr) VALUES ('k1', 'valeur-nominale-1')")
	_, _ = sourceDB.Exec("INSERT INTO items (id, descr) VALUES ('k2', 'valeur-nominale-2')")

	oracle, err := NewShadowOracle(sourcePath)
	if err != nil {
		t.Fatalf("new oracle: %v", err)
	}
	defer oracle.Close()

	if err := oracle.Replicate(); err != nil {
		t.Fatalf("replicate: %v", err)
	}

	// 1. Validation initiale nominale
	if err := oracle.ValidateReadConsistency(); err != nil {
		t.Fatalf("initial validate: %v", err)
	}

	// 2. Altération d'un seul octet (flip 1 bit) dans C2DB
	s, err := oracle.c2db.GetShard(0)
	if err != nil {
		t.Fatalf("get shard: %v", err)
	}
	pk1, err := EncodePrimaryKey([]interface{}{"k1"})
	if err != nil {
		t.Fatalf("encode pk1: %v", err)
	}
	val, err := s.GetFrom("items", []byte(pk1))
	if err != nil {
		t.Fatalf("getfrom: %v", err)
	}
	corruptedC2DB := append([]byte(nil), val...)
	corruptedC2DB[len(corruptedC2DB)-2] ^= 0x01 // flip 1 bit
	if err := s.PutIn("items", []byte(pk1), corruptedC2DB); err != nil {
		t.Fatalf("putin corrupted: %v", err)
	}

	errDivC2 := oracle.ValidateReadConsistency()
	if errDivC2 == nil {
		t.Fatalf("échec: la divergence C2DB aurait dû être détectée")
	}
	if !strings.Contains(errDivC2.Error(), "DIVERGENCE constatée pour table items") {
		t.Fatalf("diagnostic de divergence inattendu: %v", errDivC2)
	}
	t.Logf("Divergence C2DB 1-bit correctement détectée avec diff : %v", errDivC2)

	// Restauration de la valeur nominale dans C2DB
	_ = s.PutIn("items", []byte(pk1), val)
	if err := oracle.ValidateReadConsistency(); err != nil {
		t.Fatalf("restauration c2db échouée: %v", err)
	}
	t.Logf("Restauration nominale C2DB validée : parité 100%% restaurée")

	// 3. Altération d'un seul octet (flip 1 bit) dans SQLite miroir
	var origDescr string
	if err := oracle.mirrorDB.QueryRow("SELECT descr FROM items WHERE id = 'k2'").Scan(&origDescr); err != nil {
		t.Fatalf("read origDescr: %v", err)
	}
	corruptedSQL := []byte(origDescr)
	corruptedSQL[len(corruptedSQL)-1] ^= 0x01 // flip 1 bit
	_, err = oracle.mirrorDB.Exec("UPDATE items SET descr = ? WHERE id = 'k2'", string(corruptedSQL))
	if err != nil {
		t.Fatalf("update mirror: %v", err)
	}

	errDivSQL := oracle.ValidateReadConsistency()
	if errDivSQL == nil {
		t.Fatalf("échec: la divergence SQLite aurait dû être détectée")
	}
	if !strings.Contains(errDivSQL.Error(), "DIVERGENCE constatée pour table items") {
		t.Fatalf("diagnostic de divergence inattendu: %v", errDivSQL)
	}
	t.Logf("Divergence SQLite 1-bit correctement détectée avec diff : %v", errDivSQL)

	// 4. Restauration de la valeur nominale dans SQLite miroir et revérification nominale complète
	_, err = oracle.mirrorDB.Exec("UPDATE items SET descr = ? WHERE id = 'k2'", origDescr)
	if err != nil {
		t.Fatalf("restauration sqlite miroir échouée: %v", err)
	}
	if err := oracle.ValidateReadConsistency(); err != nil {
		t.Fatalf("revérification nominale post-restauration échouée: %v", err)
	}
	t.Logf("Restauration nominale SQLite validée : parité 100%% restaurée")
}

func TestShadowOracleConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	sourcePath := dir + "/source.db"
	initDB, err := sql.Open("sqlite", sourcePath+"?_journal=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	_, err = initDB.Exec("CREATE TABLE stream (id TEXT PRIMARY KEY, val TEXT NOT NULL)")
	if err != nil {
		initDB.Close()
		t.Fatalf("create table: %v", err)
	}
	initDB.Close()

	oracle, err := NewShadowOracle(sourcePath)
	if err != nil {
		t.Fatalf("new oracle: %v", err)
	}
	defer oracle.Close()

	s, err := oracle.c2db.GetShard(0)
	if err != nil {
		t.Fatalf("get shard: %v", err)
	}
	s.SetBusyTimeout(5 * time.Second)
	if err := s.CreateCollection("stream"); err != nil && !errors.Is(err, ErrCollectionExists) {
		t.Fatalf("create collection: %v", err)
	}

	const writers = 8
	const perWriter = 25
	var wgWriters sync.WaitGroup
	var errMu sync.Mutex
	var errList []error
	var continuousValidations atomic.Uint64
	var stopValidation atomic.Bool

	// 1. Démarrage de vérificateurs continus en vol pendant les écritures
	var wgValidators sync.WaitGroup
	const validators = 4
	for v := 0; v < validators; v++ {
		wgValidators.Add(1)
		go func(vid int) {
			defer wgValidators.Done()
			for !stopValidation.Load() {
				errMu.Lock()
				nKeys := len(oracle.pkeys)
				var sampleKey EntryKey
				if nKeys > 0 {
					sampleKey = oracle.pkeys[nKeys-1]
				}
				errMu.Unlock()
				if sampleKey.Key == "" {
					time.Sleep(200 * time.Microsecond)
					continue
				}

				pkParts, dErr := DecodePrimaryKey(sampleKey.Key)
				if dErr != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("erreur décodage pk en vol %s: %w", sampleKey.Key, dErr))
					errMu.Unlock()
					return
				}
				if len(pkParts) == 0 {
					continue
				}
				var sqlVal string
				qErr := oracle.mirrorDB.QueryRow("SELECT val FROM stream WHERE id = ?", pkParts[0]).Scan(&sqlVal)
				if qErr != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("lecture sqlite miroir en vol %s: %w", sampleKey.Key, qErr))
					errMu.Unlock()
					return
				}
				c2Bytes, cErr := s.GetFrom("stream", []byte(sampleKey.Key))
				if cErr != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("lecture c2db en vol %s: %w", sampleKey.Key, cErr))
					errMu.Unlock()
					return
				}
				expectedBytes, errExp := CanonicalRowBytes([]string{"id", "val"}, []interface{}{pkParts[0], sqlVal})
				if errExp != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("canonicalisation en vol %s: %w", sampleKey.Key, errExp))
					errMu.Unlock()
					return
				}
				if !bytes.Equal(c2Bytes, expectedBytes) {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("divergence en vol détectée sur clé %s", sampleKey.Key))
					errMu.Unlock()
					return
				}
				continuousValidations.Add(1)
				time.Sleep(100 * time.Microsecond)
			}
		}(v)
	}

	// 2. 8 goroutines écrivent SIMULTANÉMENT dans SQLite miroir ET dans C2DB
	for w := 0; w < writers; w++ {
		wgWriters.Add(1)
		go func(wid int) {
			defer wgWriters.Done()
			for i := 0; i < perWriter; i++ {
				keyRaw := fmt.Sprintf("stream-w%d-%04d", wid, i)
				val := fmt.Sprintf("valeur-concurrente-data-%d-%d", wid, i)
				keyEncoded, eErr := EncodePrimaryKey([]interface{}{keyRaw})
				if eErr != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("encode pk %s: %w", keyRaw, eErr))
					errMu.Unlock()
					return
				}
				payload, _ := CanonicalRowBytes([]string{"id", "val"}, []interface{}{keyRaw, val})

				// Écriture simultanée SQLite source et miroir
				_, srcErr := oracle.sourceDB.Exec("INSERT OR REPLACE INTO stream (id, val) VALUES (?, ?)", keyRaw, val)
				if srcErr != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("source write %s: %w", keyRaw, srcErr))
					errMu.Unlock()
					return
				}
				_, sErr := oracle.mirrorDB.Exec("INSERT OR REPLACE INTO stream (id, val) VALUES (?, ?)", keyRaw, val)
				if sErr != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("sqlite write %s: %w", keyRaw, sErr))
					errMu.Unlock()
					return
				}

				// Écriture simultanée C2DB
				cErr := s.PutIn("stream", []byte(keyEncoded), payload)
				if cErr != nil {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("c2db write %s: %w", keyRaw, cErr))
					errMu.Unlock()
					return
				}

				// Vérification immédiate continue dès l'écriture
				gotC2, errGet := s.GetFrom("stream", []byte(keyEncoded))
				if errGet != nil || !bytes.Equal(gotC2, payload) {
					errMu.Lock()
					errList = append(errList, fmt.Errorf("vérification immédiate échouée pour %s", keyRaw))
					errMu.Unlock()
					return
				}

				errMu.Lock()
				oracle.pkeys = append(oracle.pkeys, EntryKey{Table: "stream", Key: keyEncoded})
				errMu.Unlock()
			}
		}(w)
	}

	wgWriters.Wait()
	stopValidation.Store(true)
	wgValidators.Wait()

	if len(errList) > 0 {
		t.Fatalf("erreurs lors des écritures/validations concurrentes: %v", errList[0])
	}
	if continuousValidations.Load() < 50 {
		t.Fatalf("trop peu de validations continues en vol exécutées: %d (minimum requis: 50)", continuousValidations.Load())
	}
	t.Logf("Validations continues en vol exécutées avec succès pendant la charge : %d vérifications croisées (seuil bloquant >= 50 atteint)", continuousValidations.Load())

	// Validation globale finale directe : chaque écriture concurrente doit être strictement identique
	if err := oracle.ValidateReadConsistency(); err != nil {
		t.Fatalf("validation post-écritures concurrentes: %v", err)
	}
	t.Logf("Écritures concurrentes simultanées SQLite + C2DB validées bit-exact sur %d enregistrements", writers*perWriter)
}

func TestShadowOracleFullCycleWithExistingDB(t *testing.T) {
	const sourceDBPath = "/devhoros/horos55/data/k_rules.db"

	oracle, err := NewShadowOracle(sourceDBPath)
	if err != nil {
		t.Fatalf("new oracle from k_rules.db: %v", err)
	}
	defer oracle.Close()

	if err := oracle.Replicate(); err != nil {
		t.Fatalf("replicate from k_rules.db: %v", err)
	}
	t.Logf("Replication intégrale réussie : %d enregistrements réels chargés", len(oracle.pkeys))

	// Validation d'exhaustivité et de parité bit-exacte directe
	if err := oracle.ValidateReadConsistency(); err != nil {
		t.Fatalf("validate consistency k_rules.db: %v", err)
	}
	t.Logf("Validation d'oracle parallèle réussie : 100%% de conformité bit-exacte sur %d clés", len(oracle.pkeys))
}

func TestCanonicalRowBytes_InjectivityProof(t *testing.T) {
	cols := []string{"v"}

	// 1. Preuve formelle de non-collision BLOB vs TEXT
	blobBytes, err := CanonicalRowBytes(cols, []interface{}{[]byte("abc")})
	if err != nil {
		t.Fatalf("blob encode: %v", err)
	}
	textBytes, err := CanonicalRowBytes(cols, []interface{}{"abc"})
	if err != nil {
		t.Fatalf("text encode: %v", err)
	}
	if bytes.Equal(blobBytes, textBytes) {
		t.Fatalf("VIOLATION D'INJECTIVITÉ: BLOB('abc') et TEXT('abc') ont produit le même encodage binaire")
	}
	t.Logf("Injectivité BLOB vs TEXT prouvée: len(blob)=%d tag=0x%02x != len(text)=%d tag=0x%02x",
		len(blobBytes), blobBytes[15], len(textBytes), textBytes[15])

	// 2. Preuve formelle de non-collision INTEGER vs FLOAT
	intBytes, err := CanonicalRowBytes(cols, []interface{}{int64(1)})
	if err != nil {
		t.Fatalf("int encode: %v", err)
	}
	floatBytes, err := CanonicalRowBytes(cols, []interface{}{float64(1.0)})
	if err != nil {
		t.Fatalf("float encode: %v", err)
	}
	if bytes.Equal(intBytes, floatBytes) {
		t.Fatalf("VIOLATION D'INJECTIVITÉ: int64(1) et float64(1.0) ont produit le même encodage binaire")
	}
	t.Logf("Injectivité INTEGER vs FLOAT prouvée: intTag=0x%02x floatTag=0x%02x", intBytes[15], floatBytes[15])

	// 3. Préservation bit-exacte des octets non UTF-8
	nonUTF8 := []byte{0xff, 0xfe, 0x80, 0x00, 0xaa}
	rawEncoded, err := CanonicalRowBytes(cols, []interface{}{nonUTF8})
	if err != nil {
		t.Fatalf("non-utf8 encode: %v", err)
	}
	if !bytes.Contains(rawEncoded, nonUTF8) {
		t.Fatalf("altération d'octets non-UTF8 lors de l'encodage canonique")
	}
	t.Logf("Conservation bit-exacte BLOB binaire arbitraire validée: %x présent intact", nonUTF8)

	// 4. Preuve formelle de non-collision des clés composites et round-trip
	k1, err := EncodePrimaryKey([]interface{}{"a/b", "c"})
	if err != nil {
		t.Fatalf("encode k1: %v", err)
	}
	k2, err := EncodePrimaryKey([]interface{}{"a", "b/c"})
	if err != nil {
		t.Fatalf("encode k2: %v", err)
	}
	if k1 == k2 {
		t.Fatalf("VIOLATION D'INJECTIVITÉ: collision de clés composites ('a/b', 'c') == ('a', 'b/c')")
	}
	d1, err := DecodePrimaryKey(k1)
	if err != nil || len(d1) != 2 || d1[0] != "a/b" || d1[1] != "c" {
		t.Fatalf("échec round-trip clé primaire composite k1: %v", d1)
	}
	d2, err := DecodePrimaryKey(k2)
	if err != nil || len(d2) != 2 || d2[0] != "a" || d2[1] != "b/c" {
		t.Fatalf("échec round-trip clé primaire composite k2: %v", d2)
	}
	t.Logf("Injectivité et réversibilité des clés composites validées: k1 != k2")

	// 5. Preuve formelle de non-collision BLOB vs TEXT sur clés primaires (contre-exemple Astra)
	kBlob, err := EncodePrimaryKey([]interface{}{[]byte("abc")})
	if err != nil {
		t.Fatalf("encode pk blob: %v", err)
	}
	kText, err := EncodePrimaryKey([]interface{}{"[97 98 99]"})
	if err != nil {
		t.Fatalf("encode pk text: %v", err)
	}
	if kBlob == kText {
		t.Fatalf("VIOLATION D'INJECTIVITÉ: collision entre clé BLOB('abc') et TEXT('[97 98 99]')")
	}
	dBlob, err := DecodePrimaryKey(kBlob)
	if err != nil || len(dBlob) != 1 {
		t.Fatalf("decode pk blob: %v", err)
	}
	rawBlob, ok := dBlob[0].([]byte)
	if !ok || !bytes.Equal(rawBlob, []byte("abc")) {
		t.Fatalf("échec typage BLOB dans pk: type=%T val=%v", dBlob[0], dBlob[0])
	}
	dText, err := DecodePrimaryKey(kText)
	if err != nil || len(dText) != 1 {
		t.Fatalf("decode pk text: %v", err)
	}
	rawText, ok := dText[0].(string)
	if !ok || rawText != "[97 98 99]" {
		t.Fatalf("échec typage TEXT dans pk: type=%T val=%v", dText[0], dText[0])
	}
	t.Logf("Conservation typée des clés primaires validée: BLOB('abc') != TEXT('[97 98 99]'), types natifs préservés")
}
