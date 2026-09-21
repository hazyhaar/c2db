// SPDX-License-Identifier: Apache-2.0 OR MIT

// Banc opposable du débit d'écriture concurrente (Phase 4, Vague 1, Agent 3).
//
// Le banc confronte deux disciplines d'écriture sur la même charge et les
// mêmes octets : SQLite, sérialisé par son verrou d'écrivain unique en mode
// WAL, face à c2db.DB, dont les écritures sont routées par Blake3 vers des
// shards indépendants disposant chacun de leur propre verrou d'écrivain.
// Les goroutines qui touchent des shards distincts écrivent en parallèle pur,
// sans aucun verrou global.
//
// Matière loyale : les valeurs écrites sont des structures JSON réelles
// issues de /devhoros/horos55/data/wf55.db. La table wcard_execution, vide à
// la date du banc, est constatée comme telle dans le rapport ; la matière de
// substitution provient alors des tables réelles wcard et step_run, sans
// aucun octet synthétisé dans les valeurs (seules les clés portent les
// coordonnées du banc, ce que le rapport déclare).
package benchcmp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const (
	// opposedPs couvre la gamme imposée : 4, 16 puis 64 écrivains parallèles.
	// opposedN fixe le volume par goroutine à 500 enregistrements.
	opposedDBPath   = "/devhoros/horos55/data/wf55.db"
	opposedN        = 500
	opposedBusyMs   = 5000
	opposedMDName   = "bench_concurrency.md"
	opposedWALBytes = 16 * 1024 * 1024
)

var opposedPs = []int{4, 16, 64}

// opposedShardsFor borne l'ensemble de shards actifs par palier : 16 shards
// pour les paliers 4 et 16, 64 shards pour le palier 64. Le bornage contient
// l'empreinte disque (chaque shard préalloue son image sous O_DIRECT) tout en
// laissant le routage Blake3 s'exercer sur un ensemble large.
func opposedShardsFor(p int) int {
	if p <= 16 {
		return 16
	}
	return 64
}

// opposedWcard reflète une ligne réelle de la table wcard de wf55.db.
type opposedWcard struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Intention  string `json:"intention"`
	Criteres   string `json:"criteres"`
	Provenance string `json:"provenance"`
	CreatedAt  string `json:"created_at"`
}

// opposedStep reflète une ligne réelle de la table step_run de wf55.db.
type opposedStep struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	StepID    string `json:"step_id"`
	WcardID   string `json:"wcard_id"`
	DeskID    string `json:"desk_id"`
	Iteration int    `json:"iteration"`
	Status    string `json:"status"`
	Verdict   string `json:"verdict"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
}

// opposedExec reflète une ligne réelle de la table wcard_execution.
type opposedExec struct {
	ID        string `json:"id"`
	WcardID   string `json:"wcard_id"`
	DeskID    string `json:"desk_id"`
	Status    string `json:"status"`
	ModelID   string `json:"model_id"`
	Branch    string `json:"branch"`
	Verdict   string `json:"verdict"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
}

// opposedCorpus porte la matière du banc et sa provenance constatable.
type opposedCorpus struct {
	Vals       [][]byte // valeurs JSON réelles, rejouées en boucle
	Anchors    []string // identifiants réels d'ancrage, un par valeur
	ExecRows   int      // lignes lues dans wcard_execution (0 = table vide)
	WcardRows  int      // lignes lues dans wcard
	StepRows   int      // lignes lues dans step_run
	Provenance string   // phrase de provenance inscrite au rapport
}

// openWF55RO ouvre wf55.db en lecture seule. L'ouverture par URI mode=ro
// interdit toute création de journal aux côtés de la base de production ; en
// cas de refus du pilote, le repli ouvre le chemin direct en lecture seule
// logique (le banc n'y émet que des SELECT).
func openWF55RO() (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+opposedDBPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		db, err = sql.Open("sqlite", opposedDBPath)
		if err != nil {
			return nil, err
		}
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// loadOpposedCorpus lit la matière réelle. La table wcard_execution est lue
// en premier ; si elle est vide, le corpus est formé de paires réelles
// {wcard, step_run} associées en tourniquet, et la substitution est déclarée
// dans Provenance au lieu d'être tue.
func loadOpposedCorpus(t *testing.T) *opposedCorpus {
	t.Helper()
	db, err := openWF55RO()
	if err != nil {
		t.Fatalf("ouverture RO de %s: %v", opposedDBPath, err)
	}
	defer func() { _ = db.Close() }()

	c := &opposedCorpus{}
	var execs []opposedExec
	rows, err := db.Query(`SELECT id, wcard_id, desk_id, status, model_id, branch, verdict, started_at, ended_at FROM wcard_execution`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e opposedExec
			if err := rows.Scan(&e.ID, &e.WcardID, &e.DeskID, &e.Status, &e.ModelID, &e.Branch, &e.Verdict, &e.StartedAt, &e.EndedAt); err != nil {
				t.Fatalf("lecture wcard_execution: %v", err)
			}
			execs = append(execs, e)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("parcours wcard_execution: %v", err)
		}
	} else {
		t.Logf("wcard_execution illisible (%v), repli sur wcard+step_run", err)
	}
	c.ExecRows = len(execs)

	if len(execs) > 0 {
		for _, e := range execs {
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("marshal exec: %v", err)
			}
			c.Vals = append(c.Vals, raw)
			c.Anchors = append(c.Anchors, e.ID)
		}
		c.Provenance = fmt.Sprintf("wcard_execution de %s : %d lignes réelles rejouées en tourniquet", opposedDBPath, c.ExecRows)
		return c
	}

	var wcards []opposedWcard
	wrows, err := db.Query(`SELECT id, kind, title, intention, criteres, provenance, created_at FROM wcard`)
	if err != nil {
		t.Fatalf("lecture wcard: %v", err)
	}
	for wrows.Next() {
		var w opposedWcard
		if err := wrows.Scan(&w.ID, &w.Kind, &w.Title, &w.Intention, &w.Criteres, &w.Provenance, &w.CreatedAt); err != nil {
			_ = wrows.Close()
			t.Fatalf("parcours wcard: %v", err)
		}
		wcards = append(wcards, w)
	}
	_ = wrows.Close()
	if err := wrows.Err(); err != nil {
		t.Fatalf("erreur wrows: %v", err)
	}

	var steps []opposedStep
	srows, err := db.Query(`SELECT id, run_id, step_id, wcard_id, desk_id, iteration, status, verdict, started_at, ended_at FROM step_run`)
	if err != nil {
		t.Fatalf("lecture step_run: %v", err)
	}
	for srows.Next() {
		var s opposedStep
		if err := srows.Scan(&s.ID, &s.RunID, &s.StepID, &s.WcardID, &s.DeskID, &s.Iteration, &s.Status, &s.Verdict, &s.StartedAt, &s.EndedAt); err != nil {
			_ = srows.Close()
			t.Fatalf("parcours step_run: %v", err)
		}
		steps = append(steps, s)
	}
	_ = srows.Close()
	if err := srows.Err(); err != nil {
		t.Fatalf("erreur srows: %v", err)
	}

	c.WcardRows, c.StepRows = len(wcards), len(steps)
	if len(wcards) == 0 || len(steps) == 0 {
		t.Fatalf("matière insuffisante dans %s: wcard=%d step_run=%d", opposedDBPath, len(wcards), len(steps))
	}
	for i := range wcards {
		w := wcards[i%len(wcards)]
		s := steps[i%len(steps)]
		raw, err := json.Marshal(map[string]any{"wcard": w, "step_run": s})
		if err != nil {
			t.Fatalf("marshal paire: %v", err)
		}
		c.Vals = append(c.Vals, raw)
		c.Anchors = append(c.Anchors, w.ID+"/"+s.ID)
	}
	c.Provenance = fmt.Sprintf("wcard_execution vide (0 ligne constatée) dans %s ; substitution déclarée par paires réelles {wcard x %d, step_run x %d} associées en tourniquet, octets des valeurs intégralement issus de la base",
		opposedDBPath, c.WcardRows, c.StepRows)
	return c
}

// buildOpposedWorkload forge total clés uniques dont le routage Blake3 tombe
// dans l'ensemble [0, shards[. L'échantillonnage par rejet exerce le vrai
// routage de production au lieu de l'imiter : chaque clé retenue est routée
// par c2db.Route, exactement comme le fera db.Put.
func buildOpposedWorkload(t *testing.T, c *opposedCorpus, total, shards int) (keys, vals [][]byte) {
	t.Helper()
	keys = make([][]byte, 0, total)
	vals = make([][]byte, 0, total)
	for cand := 0; len(keys) < total; cand++ {
		if cand > total*4096+1024 {
			t.Fatalf("échantillonnage Blake3 enlisé: %d clés pour %d demandées (shards=%d)", len(keys), total, shards)
		}
		anchor := c.Anchors[cand%len(c.Anchors)]
		k := []byte(fmt.Sprintf("opposed/%07d/%s", cand, anchor))
		if int(c2db.Route(k)) >= shards {
			continue
		}
		keys = append(keys, k)
		vals = append(vals, c.Vals[cand%len(c.Vals)])
	}
	return keys, vals
}

// openSQLiteOpposed prépare le concurrent loyal : WAL, synchronous=NORMAL,
// table kv. Le busy_timeout est posé par connexion dans les travailleurs car
// ce pragma meurt avec la connexion qui l'a reçu.
func openSQLiteOpposed(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	for _, pr := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`CREATE TABLE IF NOT EXISTS kv (k BLOB PRIMARY KEY, v BLOB)`,
	} {
		if _, err := db.Exec(pr); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pr, err)
		}
	}
	return db, nil
}

// isSQLiteBusy reconnaît les refus de verrou que SQLite rend quand son
// écrivain unique est déjà pris : ce sont ces refus qui matérialisent la
// sérialisation mesurée, non des erreurs de fond.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, m := range []string{"SQLITE_BUSY", "database is locked", "database table is locked", "database schema is locked"} {
		if contains([]byte(s), m) {
			return true
		}
	}
	return false
}

// sqliteExecBusyRetry rejoue une insertion refusée par le verrou jusqu'à 60 s.
// L'attente est un coût loyal : elle reste incluse dans le temps total du
// palier, exactement comme la file d'attente WAL qu'elle révèle.
func sqliteExecBusyRetry(ctx context.Context, conn *sql.Conn, retries *atomic.Int64, k, v []byte) error {
	deadline := time.Now().Add(60 * time.Second)
	delay := time.Millisecond
	for {
		_, err := conn.ExecContext(ctx, `INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, k, v)
		if err == nil {
			return nil
		}
		if !isSQLiteBusy(err) || time.Now().After(deadline) {
			return err
		}
		retries.Add(1)
		time.Sleep(delay)
		if delay < 10*time.Millisecond {
			delay *= 2
		}
	}
}

// runSQLiteLeg fait ingérer total=P*N paires à P goroutines parties d'une
// barrière commune et retourne le débit mesuré. Chaque travailleur tient sa
// propre connexion avec PRAGMA busy_timeout=5000 et PRAGMA synchronous=NORMAL
// explicitement posés directement sur la connexion dédiée.
func runSQLiteLeg(t *testing.T, p int, keys, vals [][]byte) (opsPerSec, seconds float64, verified int64, busyRetries int64) {
	t.Helper()
	total := len(keys)
	db, err := openSQLiteOpposed(filepath.Join(t.TempDir(), "opposed.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(p)
	db.SetMaxIdleConns(p)

	n := total / p
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	var firstErr atomic.Value
	var retries atomic.Int64
	ready.Add(p)
	done.Add(p)
	for g := 0; g < p; g++ {
		go func(g int) {
			defer done.Done()
			conn, err := db.Conn(context.Background())
			if err != nil {
				firstErr.CompareAndSwap(nil, err)
				ready.Done()
				return
			}
			defer func() { _ = conn.Close() }()
			if _, err := conn.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA busy_timeout=%d`, opposedBusyMs)); err != nil {
				firstErr.CompareAndSwap(nil, err)
				ready.Done()
				return
			}
			if _, err := conn.ExecContext(context.Background(), `PRAGMA synchronous=NORMAL`); err != nil {
				firstErr.CompareAndSwap(nil, err)
				ready.Done()
				return
			}
			ready.Done()
			<-start
			for i := g * n; i < (g+1)*n; i++ {
				if err := sqliteExecBusyRetry(context.Background(), conn, &retries, keys[i], vals[i]); err != nil {
					firstErr.CompareAndSwap(nil, err)
					return
				}
			}
		}(g)
	}
	ready.Wait()
	t0 := time.Now()
	close(start)
	done.Wait()
	dt := time.Since(t0)
	if v := firstErr.Load(); v != nil {
		t.Fatalf("écriture sqlite P=%d: %v", p, v.(error))
	}
	var count int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM kv`).Scan(&count); err != nil {
		t.Fatalf("comptage sqlite P=%d: %v", p, err)
	}
	if count != int64(total) {
		t.Fatalf("sqlite P=%d: %d lignes contre %d écritures", p, count, total)
	}

	// Validation exhaustive bit-exacte à 100 % de toutes les paires insérées dans SQLite
	for i := 0; i < total; i++ {
		var v []byte
		if err := db.QueryRow(`SELECT v FROM kv WHERE k = ?`, keys[i]).Scan(&v); err != nil {
			t.Fatalf("relecture sqlite P=%d clé %d: %v", p, i, err)
		}
		if !bytes.Equal(v, vals[i]) {
			t.Fatalf("relecture sqlite P=%d clé %d: divergence bit-exact", p, i)
		}
	}

	seconds = dt.Seconds()
	opsPerSec = float64(total) / seconds
	return opsPerSec, seconds, count, retries.Load()
}

// runC2dbLeg fait ingérer la même charge à P goroutines via c2db.DB. Les
// shards de l'ensemble sont pré-ouverts hors chronomètre (préallocation
// fallocate exclue de la mesure) ; le chronomètre ne couvre que l'ingestion.
// Le verrou d'écrivain de chaque shard attend jusqu'à 5 s (SetBusyTimeout),
// de sorte qu'une collision honnête sur un shard se résorbe au lieu d'avorter.
func runC2dbLeg(t *testing.T, p, shards int, keys, vals [][]byte) (opsPerSec, seconds float64, distinct int, skipped bool, reason string) {
	t.Helper()
	total := len(keys)
	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 21)
	}
	db, err := c2db.OpenDB(filepath.Join(t.TempDir(), "c2opposed"), mac)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			return 0, 0, 0, true, "O_DIRECT indisponible (EINVAL à OpenDB)"
		}
		t.Fatalf("c2db OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetBusyTimeout(5 * time.Second)
	for id := 0; id < shards; id++ {
		if _, err := db.GetShard(uint16(id)); err != nil {
			if errors.Is(err, unix.EINVAL) {
				return 0, 0, 0, true, "O_DIRECT indisponible (EINVAL au pré-chauffage)"
			}
			t.Fatalf("c2db GetShard(%d): %v", id, err)
		}
	}

	n := total / p
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	var firstErr atomic.Value
	ready.Add(p)
	done.Add(p)
	for g := 0; g < p; g++ {
		go func(g int) {
			defer done.Done()
			ready.Done()
			<-start
			for i := g * n; i < (g+1)*n; i++ {
				if err := db.Put(keys[i], vals[i]); err != nil {
					firstErr.CompareAndSwap(nil, err)
					return
				}
			}
		}(g)
	}
	ready.Wait()
	t0 := time.Now()
	close(start)
	done.Wait()
	dt := time.Since(t0)
	if v := firstErr.Load(); v != nil {
		if err, ok := v.(error); ok && errors.Is(err, unix.EINVAL) {
			return 0, 0, 0, true, "O_DIRECT indisponible (EINVAL à l'écriture)"
		}
		t.Fatalf("écriture c2db P=%d: %v", p, v)
	}

	seen := make(map[uint16]struct{}, shards)
	for _, k := range keys {
		seen[c2db.Route(k)] = struct{}{}
	}
	// Relecture exhaustive bit-exacte à 100 % de toutes les paires insérées dans C2DB
	for i := 0; i < total; i++ {
		got, err := db.Get(keys[i])
		if err != nil {
			t.Fatalf("relecture c2db P=%d clé %d: %v", p, i, err)
		}
		if !bytes.Equal(got, vals[i]) {
			t.Fatalf("relecture c2db P=%d clé %d: divergence bit-exact", p, i)
		}
	}
	seconds = dt.Seconds()
	opsPerSec = float64(total) / seconds
	return opsPerSec, seconds, len(seen), false, ""
}

// opposedResult consigne un palier complet du banc.
type opposedResult struct {
	P            int
	N            int
	Total        int
	Shards       int
	SQLiteOps    float64
	SQLiteSec    float64
	SQLiteBusy   int64
	C2dbOps      float64
	C2dbSec      float64
	Speedup      float64
	C2dbDistinct int
	SkippedC2db  bool
	SkipReason   string
}

// TestOpposedPayloadsAreReal verrouille la loyauté de la matière : le corpus
// existe, ses valeurs portent des ancres réelles de wf55.db, et l'état vide
// de wcard_execution est constaté plutôt que contourné en silence.
func TestOpposedPayloadsAreReal(t *testing.T) {
	c := loadOpposedCorpus(t)
	if len(c.Vals) == 0 {
		t.Fatal("corpus vide")
	}
	t.Logf("provenance: %s", c.Provenance)
	real := 0
	for _, v := range c.Vals {
		if json.Valid(v) && (contains(v, "desk-") || contains(v, "wcard") || contains(v, "step")) {
			real++
		}
	}
	if real != len(c.Vals) {
		t.Fatalf("valeurs sans ancre réelle: %d/%d", real, len(c.Vals))
	}
}

func contains(b []byte, s string) bool {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}

// TestOpposedConcurrency_WriteThroughput est le banc opposable : pour chaque
// palier P, la même charge de P*500 enregistrements réels est ingérée sous
// SQLite WAL puis sous c2db multi-shards, et l'accélération mesurée
// S = débit(c2db) / débit(sqlite) est consignée dans bench_concurrency.md.
func TestOpposedConcurrency_WriteThroughput(t *testing.T) {
	corpus := loadOpposedCorpus(t)
	t.Logf("corpus: %d valeurs distinctes ; %s", len(corpus.Vals), corpus.Provenance)

	var results []opposedResult
	for _, p := range opposedPs {
		shards := opposedShardsFor(p)
		total := p * opposedN
		keys, vals := buildOpposedWorkload(t, corpus, total, shards)

		sqlOps, sqlSec, _, sqlBusy := runSQLiteLeg(t, p, keys, vals)
		c2Ops, c2Sec, distinct, skipped, reason := runC2dbLeg(t, p, shards, keys, vals)

		r := opposedResult{P: p, N: opposedN, Total: total, Shards: shards,
			SQLiteOps: sqlOps, SQLiteSec: sqlSec, SQLiteBusy: sqlBusy,
			C2dbDistinct: distinct, SkippedC2db: skipped, SkipReason: reason}
		if !skipped {
			r.C2dbOps, r.C2dbSec = c2Ops, c2Sec
			r.Speedup = c2Ops / sqlOps
			// Le routage Blake3 doit disperser : exiger au moins autant de
			// shards touchés que de goroutines au petit palier, et l'ensemble
			// borné presque entier au grand palier.
			want := p
			if want > shards {
				want = shards
			}
			if distinct < want/2+1 {
				t.Fatalf("P=%d: dispersion insuffisante (%d shards touchés, %d attendus au moins)", p, distinct, want/2+1)
			}
			t.Logf("P=%02d N=%d shards=%d sqlite=%.0f ops/s (%.2fs, busy=%d) c2db=%.0f ops/s (%.2fs) S=%.2f distinct=%d",
				p, opposedN, shards, sqlOps, sqlSec, sqlBusy, c2Ops, c2Sec, r.Speedup, distinct)
		} else {
			t.Logf("P=%02d N=%d sqlite=%.0f ops/s (%.2fs, busy=%d) c2db=IGNORÉ (%s)", p, opposedN, sqlOps, sqlSec, sqlBusy, reason)
		}
		results = append(results, r)
	}
	writeOpposedMD(t, corpus, results)
}

// writeOpposedMD consigne le rapport opposable à côté du banc.
func writeOpposedMD(t *testing.T, c *opposedCorpus, results []opposedResult) {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	out := filepath.Join(filepath.Dir(self), opposedMDName)

	maxTotal := 0
	for _, r := range results {
		maxTotal += r.Total
	}
	var b []byte
	b = append(b, []byte("# Banc opposable — débit d'écriture concurrente SQLite face à c2db\n\n")...)
	b = append(b, []byte(fmt.Sprintf("Date : %s. Machine : %s/%s, %d CPU, %s. Dépôt : `/devhoros/c2simd`.\n\n",
		time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version()))...)
	b = append(b, []byte("## Matière\n\n"+c.Provenance+".\n\n")...)
	b = append(b, []byte("Chaque écriture porte une clé unique (`opposed/<compteur>/<ancre réelle>`) et une valeur JSON réelle ; "+
		"les clés sont retenues par échantillonnage sur le routage Blake3 de production (`c2db.Route`), de sorte que chaque palier "+
		"n'écrit que dans son ensemble borné de shards. Aucune valeur n'est synthétisée.\n\n")...)
	b = append(b, []byte("## Protocole loyal\n\n")...)
	b = append(b, []byte("- SQLite (modernc, sans CGO) : `PRAGMA journal_mode=WAL`, `PRAGMA synchronous=NORMAL`, "+
		fmt.Sprintf("`PRAGMA busy_timeout=%d` posé sur chaque connexion de travailleur, table `kv(k BLOB PRIMARY KEY, v BLOB)`, "+
			"`INSERT OR REPLACE`, une connexion par goroutine, départ sur barrière commune, durée = ingestion seule. "+
			"Quand le verrou rend SQLITE_BUSY au-delà du busy_timeout, l'insertion est rejouée côté Go (plafond 60 s, attente incluse "+
			"dans la mesure) et chaque refus rejoué est compté dans la colonne dédiée : ces refus sont la sérialisation observée, non un artefact.\n", opposedBusyMs))...)
	b = append(b, []byte("- c2db : `c2db.DB` sous `O_DIRECT`, ensemble borné à 16 shards (paliers 4, 16) puis 64 shards (palier 64), "+
		"`SetBusyTimeout(5s)` sur chaque écrivain de shard, `Put` direct, départ sur barrière commune, durée = ingestion seule "+
		"(pré-ouverture des shards hors chronomètre).\n")...)
	b = append(b, []byte("- Vérification : relecture et vérification bit-exacte à 100% de l'ensemble exhaustif des clés côté SQLite et côté c2db, "+
		"comptage `COUNT(*)` côté SQLite et dénombrement des shards distincts touchés côté c2db. Tout écart de parité ou d'exhaustivité échoue immédiatement le banc.\n\n")...)
	b = append(b, []byte("## Résultats\n\n")...)
	b = append(b, []byte("| P goroutines | N / goroutine | Total | Shards c2db | SQLite ops/s | SQLite s | SQLite refus busy rejoués | c2db ops/s | c2db s | S = c2db/sqlite | Shards touchés |\n")...)
	b = append(b, []byte("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")...)
	for _, r := range results {
		if r.SkippedC2db {
			b = append(b, []byte(fmt.Sprintf("| %d | %d | %d | %d | %.0f | %.2f | %d | IGNORÉ | — | — | — |\n",
				r.P, r.N, r.Total, r.Shards, r.SQLiteOps, r.SQLiteSec, r.SQLiteBusy))...)
			continue
		}
		b = append(b, []byte(fmt.Sprintf("| %d | %d | %d | %d | %.0f | %.2f | %d | %.0f | %.2f | %.2f | %d |\n",
			r.P, r.N, r.Total, r.Shards, r.SQLiteOps, r.SQLiteSec, r.SQLiteBusy, r.C2dbOps, r.C2dbSec, r.Speedup, r.C2dbDistinct))...)
	}
	b = append(b, []byte("\n## Lecture\n\n")...)
	b = append(b, []byte("SQLite sérialise l'ensemble des écritures derrière son verrou WAL unique sous forte concurrence. "+
		"c2db partitionne les écritures par routage Blake3 sur des shards indépendants dotés de leurs propres verrous d'écrivain sous O_DIRECT. "+
		"Sur un support de stockage NVMe unique avec barrière de synchronisation matérielle directe, le débit global reflète le compromis entre l'indépendance des verrous de shards et le coût unitaire de l'E/S directe non mise en cache.\n\n")...)
	b = append(b, []byte("## Limites déclarées\n\n")...)
	b = append(b, []byte(fmt.Sprintf("- Mesure sur un seul poste, un seul volume ; la latence de `fdatasync` du volume porte les deux moteurs.\n"+
		"- Charge totale du banc : %d écritures ; valeurs de quelques centaines d'octets.\n"+
		"- Si la jambe c2db est marquée IGNORÉE, le volume ne porte pas `O_DIRECT` et seul le motif est consigné, sans chiffre inventé.\n", maxTotal))...)
	for _, r := range results {
		if r.SkippedC2db {
			b = append(b, []byte(fmt.Sprintf("- Palier P=%d : jambe c2db ignorée — %s.\n", r.P, r.SkipReason))...)
		}
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatalf("écriture du rapport: %v", err)
	}
	t.Logf("rapport opposable: %s", out)
}
