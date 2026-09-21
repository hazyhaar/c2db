// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"golang.org/x/sys/unix"
)

const (
	latSeedN           = 512
	latWriters         = 2
	latReaders         = 8
	latReadBatch       = 32
	latBatchesPerR     = 128
	latSamplesPerR     = latBatchesPerR * latReadBatch
	latWarmupPerR      = 256
	latWriteBatch      = 8
	latJSON            = "/devhoros/c2simd/c2pkg/c2db/bench_latency_profile.json"
	latC2ReadCeilingUs = 100.0
)

type latencyPercentiles struct {
	Count  int     `json:"count"`
	MinNs  int64   `json:"min_ns"`
	P50Ns  int64   `json:"p50_ns"`
	P95Ns  int64   `json:"p95_ns"`
	P99Ns  int64   `json:"p99_ns"`
	P999Ns int64   `json:"p99_9_ns"`
	MaxNs  int64   `json:"max_ns"`
	MeanNs float64 `json:"mean_ns"`
	Jitter float64 `json:"jitter_p99_p50"`
}

type latencyProfileRow struct {
	Engine                     string             `json:"engine"`
	Op                         string             `json:"op"`
	Writers                    int                `json:"writers"`
	Readers                    int                `json:"readers"`
	Samples                    int                `json:"samples"`
	WriteOps                   uint64             `json:"write_ops"`
	WriteErrors                uint64             `json:"write_errors"`
	WritesTemporalDistribution []uint64           `json:"writes_temporal_distribution"`
	ReadOps                    uint64             `json:"read_ops"`
	DurationS                  float64            `json:"duration_s"`
	Note                       string             `json:"note,omitempty"`
	IndividualLat              latencyPercentiles `json:"individual_latency_ns"`
	BatchAmortizedLat          latencyPercentiles `json:"batch_amortized_latency_ns"`
	BatchSize                  int                `json:"batch_size"`
}

type campaignResult struct {
	PassIndex int               `json:"pass_index"`
	C2Row     latencyProfileRow `json:"c2db"`
	SQLiteRow latencyProfileRow `json:"sqlite"`
}

type latencyProfileReport struct {
	RaceEnabled             bool                `json:"race_enabled"`
	ClockCallOverheadNs     float64             `json:"clock_call_overhead_ns"`
	MeasurementMethod       string              `json:"measurement_method"`
	CampaignsCount          int                 `json:"campaigns_count"`
	SelectedMedianIndex     int                 `json:"selected_median_index"`
	SelectedMedianCriterion string              `json:"selected_median_criterion"`
	MedianCampaign          []latencyProfileRow `json:"median_campaign"`
	AllCampaigns            []campaignResult    `json:"all_campaigns"`
}

func calibrateClockOverheadNs() float64 {
	const N = 50000
	t0 := time.Now()
	for i := 0; i < N; i++ {
		_ = time.Now().UnixNano()
	}
	return float64(time.Since(t0).Nanoseconds()) / float64(N)
}

func latencyPct(samples []int64) latencyPercentiles {
	n := len(samples)
	if n == 0 {
		return latencyPercentiles{}
	}
	sorted := make([]int64, n)
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := func(p float64) int {
		i := int(p * float64(n-1))
		if i < 0 {
			return 0
		}
		if i >= n {
			return n - 1
		}
		return i
	}

	var sum float64
	for _, v := range sorted {
		sum += float64(v)
	}
	mean := sum / float64(n)
	p50 := sorted[idx(0.50)]
	p99 := sorted[idx(0.99)]
	jitter := 0.0
	if p50 > 0 {
		jitter = float64(p99) / float64(p50)
	}
	return latencyPercentiles{
		Count:  n,
		MinNs:  sorted[0],
		P50Ns:  p50,
		P95Ns:  sorted[idx(0.95)],
		P99Ns:  p99,
		P999Ns: sorted[idx(0.999)],
		MaxNs:  sorted[n-1],
		MeanNs: mean,
		Jitter: jitter,
	}
}

func flattenLatencies(perReader [][]int64, nPer int) []int64 {
	out := make([]int64, 0, len(perReader)*nPer)
	for _, row := range perReader {
		out = append(out, row[:nPer]...)
	}
	return out
}

func logLatencyTable(t *testing.T, rows []latencyProfileRow) {
	t.Helper()
	t.Logf("%-8s %-18s %4s %4s %9s %9s %8s %12s %12s %12s %12s",
		"engine", "op", "W", "R", "write_ops", "write_err", "n", "indiv_p50_us", "indiv_p99_us", "batch_p50_us", "batch_p99_us")
	for _, r := range rows {
		t.Logf("%-8s %-18s %4d %4d %9d %9d %8d %12.3f %12.3f %12.3f %12.3f",
			r.Engine, r.Op, r.Writers, r.Readers, r.WriteOps, r.WriteErrors, r.IndividualLat.Count,
			float64(r.IndividualLat.P50Ns)/1e3,
			float64(r.IndividualLat.P99Ns)/1e3,
			float64(r.BatchAmortizedLat.P50Ns)/1e3,
			float64(r.BatchAmortizedLat.P99Ns)/1e3)
	}
}

func seedC2(t *testing.T, s *c2db.Shard, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Put(keys[i], vals[i]); err != nil {
			t.Fatalf("c2db seed Put %d: %v", i, err)
		}
	}
}

func seedSQLite(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("sqlite seed begin: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, keys[i], vals[i]); err != nil {
			_ = tx.Rollback()
			t.Fatalf("sqlite seed %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("sqlite seed commit: %v", err)
	}
}

func openSQLiteLat(path string) (*sql.DB, error) {
	dsn := path + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (k BLOB PRIMARY KEY, v BLOB)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func awaitWriteFloor(writeOps *atomic.Uint64, floor uint64, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if writeOps.Load() >= floor {
			return true
		}
		time.Sleep(200 * time.Microsecond)
	}
	return writeOps.Load() >= floor
}

func runC2LatencyProfile(t *testing.T, s *c2db.Shard, seedN, writers, readers, samplesPerR int) latencyProfileRow {
	t.Helper()

	perReaderIndiv := make([][]int64, readers)
	perReaderBatch := make([][]int64, readers)
	for r := 0; r < readers; r++ {
		perReaderIndiv[r] = make([]int64, samplesPerR)
		perReaderBatch[r] = make([]int64, latBatchesPerR)
	}

	var (
		stop          atomic.Bool
		writeOps      atomic.Uint64
		writeErrCount atomic.Uint64
		readOps       atomic.Uint64
		readerErr     atomic.Value
		wg            sync.WaitGroup
		writeTimesMu  sync.Mutex
		writeTimes    []time.Time
	)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			var seq uint64
			for !stop.Load() {
				tx, err := s.Begin()
				if err != nil {
					writeErrCount.Add(1)
					time.Sleep(100 * time.Microsecond)
					continue
				}
				ok := true
				for b := 0; b < latWriteBatch; b++ {
					idx := (wid*latWriteBatch + int(seq) + b) % seedN
					if err := tx.Put(keys[idx], vals[idx]); err != nil {
						writeErrCount.Add(1)
						_ = tx.Rollback()
						ok = false
						break
					}
				}
				if !ok {
					time.Sleep(100 * time.Microsecond)
					continue
				}
				if err := tx.Commit(); err != nil {
					writeErrCount.Add(1)
					time.Sleep(100 * time.Microsecond)
					continue
				}
				now := time.Now()
				writeOps.Add(uint64(latWriteBatch))
				seq += uint64(latWriteBatch)
				writeTimesMu.Lock()
				writeTimes = append(writeTimes, now)
				writeTimesMu.Unlock()
			}
		}(w)
	}

	if !awaitWriteFloor(&writeOps, 32, 5*time.Second) {
		stop.Store(true)
		wg.Wait()
		t.Fatalf("c2db: saturation écriture non atteinte avant mesure (write_ops=%d)", writeOps.Load())
	}

	for r := 0; r < readers; r++ {
		for i := 0; i < latWarmupPerR; i++ {
			_, _ = s.Get(keys[i%seedN])
		}
	}

	writesAtStart := writeOps.Load()
	t0 := time.Now()
	var rWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		rWG.Add(1)
		go func(rid int) {
			defer rWG.Done()
			indivBuf := perReaderIndiv[rid]
			batchBuf := perReaderBatch[rid]
			base := rid * 17
			sampleIdx := 0
			for b := 0; b < latBatchesPerR; b++ {
				tBatchStart := time.Now().UnixNano()
				for j := 0; j < latReadBatch; j++ {
					currIdx := (base + b*latReadBatch + j) % seedN
					k := keys[currIdx]
					expected := vals[currIdx]
					tStart := time.Now().UnixNano()
					got, err := s.Get(k)
					tEnd := time.Now().UnixNano()
					if err != nil {
						readerErr.CompareAndSwap(nil, fmt.Errorf("c2db Get(%s) rid=%d b=%d j=%d: %w", string(k), rid, b, j, err))
						return
					}
					if !bytes.Equal(got, expected) {
						readerErr.CompareAndSwap(nil, fmt.Errorf("c2db bit-exact mismatch rid=%d b=%d j=%d on key %s", rid, b, j, string(k)))
						return
					}
					indivBuf[sampleIdx] = tEnd - tStart
					sampleIdx++
				}
				tBatchEnd := time.Now().UnixNano()
				batchBuf[b] = (tBatchEnd - tBatchStart) / int64(latReadBatch)
				readOps.Add(uint64(latReadBatch))
			}
		}(r)
	}
	rWG.Wait()
	tWindowEnd := time.Now()
	elapsed := tWindowEnd.Sub(t0)
	writesDuring := writeOps.Load() - writesAtStart

	stop.Store(true)
	wg.Wait()

	if v := readerErr.Load(); v != nil {
		t.Fatalf("échec d'un lecteur c2db: %v", v)
	}

	// Caractérisation de la répartition temporelle des écritures en 4 tranches égales
	const numSlices = 4
	dist := make([]uint64, numSlices)
	sliceDur := elapsed / numSlices
	if sliceDur > 0 {
		writeTimesMu.Lock()
		for _, wt := range writeTimes {
			if (wt.Equal(t0) || wt.After(t0)) && (wt.Equal(tWindowEnd) || wt.Before(tWindowEnd)) {
				idx := int(wt.Sub(t0) / sliceDur)
				if idx >= numSlices {
					idx = numSlices - 1
				}
				if idx < 0 {
					idx = 0
				}
				dist[idx] += uint64(latWriteBatch)
			}
		}
		writeTimesMu.Unlock()
	}

	allIndiv := flattenLatencies(perReaderIndiv, samplesPerR)
	indivLat := latencyPct(allIndiv)
	allBatch := flattenLatencies(perReaderBatch, latBatchesPerR)
	batchLat := latencyPct(allBatch)

	return latencyProfileRow{
		Engine:                     "c2db",
		Op:                         "get_sous_write",
		Writers:                    writers,
		Readers:                    readers,
		Samples:                    len(allIndiv),
		WriteOps:                   writesDuring,
		WriteErrors:                writeErrCount.Load(),
		WritesTemporalDistribution: dist,
		ReadOps:                    readOps.Load(),
		DurationS:                  elapsed.Seconds(),
		Note:                       "mvcc_pinLive_Get_sous_tx_Put",
		IndividualLat:              indivLat,
		BatchAmortizedLat:          batchLat,
		BatchSize:                  latReadBatch,
	}
}

func runSQLiteLatencyProfile(t *testing.T, db *sql.DB, seedN, writers, readers, samplesPerR int) latencyProfileRow {
	t.Helper()

	db.SetMaxOpenConns(writers + readers + 2)
	db.SetMaxIdleConns(writers + readers + 2)

	perReaderIndiv := make([][]int64, readers)
	perReaderBatch := make([][]int64, readers)
	for r := 0; r < readers; r++ {
		perReaderIndiv[r] = make([]int64, samplesPerR)
		perReaderBatch[r] = make([]int64, latBatchesPerR)
	}

	var (
		stop          atomic.Bool
		writeOps      atomic.Uint64
		writeErrCount atomic.Uint64
		readOps       atomic.Uint64
		readerErr     atomic.Value
		wg            sync.WaitGroup
		writeTimesMu  sync.Mutex
		writeTimes    []time.Time
	)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			var seq uint64
			for !stop.Load() {
				tx, err := db.Begin()
				if err != nil {
					writeErrCount.Add(1)
					time.Sleep(100 * time.Microsecond)
					continue
				}
				ok := true
				for b := 0; b < latWriteBatch; b++ {
					idx := (wid*latWriteBatch + int(seq) + b) % seedN
					if _, err := tx.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, keys[idx], vals[idx]); err != nil {
						writeErrCount.Add(1)
						_ = tx.Rollback()
						ok = false
						break
					}
				}
				if !ok {
					time.Sleep(100 * time.Microsecond)
					continue
				}
				if err := tx.Commit(); err != nil {
					writeErrCount.Add(1)
					time.Sleep(100 * time.Microsecond)
					continue
				}
				now := time.Now()
				writeOps.Add(uint64(latWriteBatch))
				seq += uint64(latWriteBatch)
				writeTimesMu.Lock()
				writeTimes = append(writeTimes, now)
				writeTimesMu.Unlock()
			}
		}(w)
	}

	if !awaitWriteFloor(&writeOps, 32, 5*time.Second) {
		stop.Store(true)
		wg.Wait()
		t.Fatalf("sqlite: saturation écriture non atteinte avant mesure (write_ops=%d)", writeOps.Load())
	}

	stmts := make([]*sql.Stmt, readers)
	for r := 0; r < readers; r++ {
		st, err := db.Prepare(`SELECT v FROM kv WHERE k=?`)
		if err != nil {
			t.Fatalf("sqlite prepare: %v", err)
		}
		stmts[r] = st
		for i := 0; i < latWarmupPerR; i++ {
			var v []byte
			_ = st.QueryRow(keys[i%seedN]).Scan(&v)
		}
	}
	defer func() {
		for _, st := range stmts {
			_ = st.Close()
		}
	}()

	writesAtStart := writeOps.Load()
	t0 := time.Now()
	var rWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		rWG.Add(1)
		go func(rid int) {
			defer rWG.Done()
			st := stmts[rid]
			indivBuf := perReaderIndiv[rid]
			batchBuf := perReaderBatch[rid]
			base := rid * 17
			sampleIdx := 0
			var v []byte
			for b := 0; b < latBatchesPerR; b++ {
				tBatchStart := time.Now().UnixNano()
				for j := 0; j < latReadBatch; j++ {
					currIdx := (base + b*latReadBatch + j) % seedN
					k := keys[currIdx]
					expected := vals[currIdx]
					tStart := time.Now().UnixNano()
					err := st.QueryRow(k).Scan(&v)
					tEnd := time.Now().UnixNano()
					if err != nil {
						readerErr.CompareAndSwap(nil, fmt.Errorf("sqlite Scan(%s) rid=%d b=%d j=%d: %w", string(k), rid, b, j, err))
						return
					}
					if !bytes.Equal(v, expected) {
						readerErr.CompareAndSwap(nil, fmt.Errorf("sqlite bit-exact mismatch rid=%d b=%d j=%d on key %s", rid, b, j, string(k)))
						return
					}
					indivBuf[sampleIdx] = tEnd - tStart
					sampleIdx++
				}
				tBatchEnd := time.Now().UnixNano()
				batchBuf[b] = (tBatchEnd - tBatchStart) / int64(latReadBatch)
				readOps.Add(uint64(latReadBatch))
			}
		}(r)
	}
	rWG.Wait()
	tWindowEnd := time.Now()
	elapsed := tWindowEnd.Sub(t0)
	writesDuring := writeOps.Load() - writesAtStart

	stop.Store(true)
	wg.Wait()

	if v := readerErr.Load(); v != nil {
		t.Fatalf("échec d'un lecteur sqlite: %v", v)
	}

	// Caractérisation de la répartition temporelle des écritures en 4 tranches égales
	const numSlices = 4
	dist := make([]uint64, numSlices)
	sliceDur := elapsed / numSlices
	if sliceDur > 0 {
		writeTimesMu.Lock()
		for _, wt := range writeTimes {
			if (wt.Equal(t0) || wt.After(t0)) && (wt.Equal(tWindowEnd) || wt.Before(tWindowEnd)) {
				idx := int(wt.Sub(t0) / sliceDur)
				if idx >= numSlices {
					idx = numSlices - 1
				}
				if idx < 0 {
					idx = 0
				}
				dist[idx] += uint64(latWriteBatch)
			}
		}
		writeTimesMu.Unlock()
	}

	allIndiv := flattenLatencies(perReaderIndiv, samplesPerR)
	indivLat := latencyPct(allIndiv)
	allBatch := flattenLatencies(perReaderBatch, latBatchesPerR)
	batchLat := latencyPct(allBatch)

	return latencyProfileRow{
		Engine:                     "sqlite",
		Op:                         "get_sous_write",
		Writers:                    writers,
		Readers:                    readers,
		Samples:                    len(allIndiv),
		WriteOps:                   writesDuring,
		WriteErrors:                writeErrCount.Load(),
		WritesTemporalDistribution: dist,
		ReadOps:                    readOps.Load(),
		DurationS:                  elapsed.Seconds(),
		Note:                       "wal_sync_full_select_pk_sous_tx_insert",
		IndividualLat:              indivLat,
		BatchAmortizedLat:          batchLat,
		BatchSize:                  latReadBatch,
	}
}

func TestLatencyProfile_ReadUnderSaturatingWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du profil de latence sous -short")
	}

	clockOverheadNs := calibrateClockOverheadNs()
	t.Logf("Calibration horloge système (vDSO) : coût moyen d'un appel time.Now().UnixNano() = %.2f ns", clockOverheadNs)

	seedN := latSeedN
	if seedN > len(keys) {
		seedN = len(keys)
	}

	dir := t.TempDir()
	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 41)
	}

	c2dir := filepath.Join(dir, "c2")
	if err := os.MkdirAll(c2dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := c2db.OpenShard(c2dir, mac, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()
	s.SetBusyTimeout(5 * time.Second)
	seedC2(t, s, seedN)

	sq, err := openSQLiteLat(filepath.Join(dir, "lat.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer sq.Close()
	seedSQLite(t, sq, seedN)

	const numPasses = 3
	var campaigns [numPasses]campaignResult

	for pass := 0; pass < numPasses; pass++ {
		c2row := runC2LatencyProfile(t, s, seedN, latWriters, latReaders, latSamplesPerR)
		sqrow := runSQLiteLatencyProfile(t, sq, seedN, latWriters, latReaders, latSamplesPerR)

		if c2row.WriteOps < 32 {
			t.Fatalf("c2db pass %d: saturation écriture insuffisante pendant la mesure (write_ops=%d < 32)", pass, c2row.WriteOps)
		}
		if sqrow.WriteOps < 32 {
			t.Fatalf("sqlite pass %d: saturation écriture insuffisante pendant la mesure (write_ops=%d < 32)", pass, sqrow.WriteOps)
		}
		if c2row.WriteErrors != 0 {
			t.Fatalf("c2db pass %d: erreurs d'écriture pendant la mesure (write_errors=%d)", pass, c2row.WriteErrors)
		}
		if sqrow.WriteErrors != 0 {
			t.Fatalf("sqlite pass %d: erreurs d'écriture pendant la mesure (write_errors=%d)", pass, sqrow.WriteErrors)
		}
		expectedReads := uint64(latReaders * latSamplesPerR)
		if c2row.ReadOps != expectedReads {
			t.Fatalf("c2db pass %d: lectures incomplètes (%d / %d)", pass, c2row.ReadOps, expectedReads)
		}
		if sqrow.ReadOps != expectedReads {
			t.Fatalf("sqlite pass %d: lectures incomplètes (%d / %d)", pass, sqrow.ReadOps, expectedReads)
		}

		// Assertion formelle bloquante : chaque quartile temporel doit contenir des écritures (absence de pause d'écriture)
		for q, count := range c2row.WritesTemporalDistribution {
			if count == 0 {
				t.Fatalf("c2db pass %d: quartile temporel %d sans aucune écriture validée (distribution=%v)", pass, q, c2row.WritesTemporalDistribution)
			}
		}
		for q, count := range sqrow.WritesTemporalDistribution {
			if count == 0 {
				t.Fatalf("sqlite pass %d: quartile temporel %d sans aucune écriture validée (distribution=%v)", pass, q, sqrow.WritesTemporalDistribution)
			}
		}

		// Invariants bloquants contractuels sur la LATENCE INDIVIDUELLE (Plan Directeur : p99 < 100 µs et p50 < 100 µs)
		p99IndivUs := float64(c2row.IndividualLat.P99Ns) / 1e3
		if c2row.IndividualLat.P99Ns >= 100_000 {
			t.Fatalf("VIOLATION DE CONTRAT BLOQUANTE: c2db pass %d p99 individuel = %.3f µs dépasse ou égale le plafond contractuel de 100 µs (max permis: < 100 000 ns, observé: %d ns)",
				pass, p99IndivUs, c2row.IndividualLat.P99Ns)
		}
		p50IndivUs := float64(c2row.IndividualLat.P50Ns) / 1e3
		if c2row.IndividualLat.P50Ns >= 100_000 {
			t.Fatalf("VIOLATION DE CONTRAT BLOQUANTE: c2db pass %d p50 individuel = %.3f µs dépasse ou égale le plafond contractuel de 100 µs (max permis: < 100 000 ns, observé: %d ns)",
				pass, p50IndivUs, c2row.IndividualLat.P50Ns)
		}

		campaigns[pass] = campaignResult{PassIndex: pass + 1, C2Row: c2row, SQLiteRow: sqrow}
		t.Logf("Campagne métrologique %d/%d validée : c2db indiv_p50=%.2fµs indiv_p99=%.2fµs batch_p99=%.2fµs (w=%d dist=%v), sqlite indiv_p50=%.2fµs indiv_p99=%.2fµs (w=%d dist=%v)",
			pass+1, numPasses,
			float64(c2row.IndividualLat.P50Ns)/1e3, float64(c2row.IndividualLat.P99Ns)/1e3, float64(c2row.BatchAmortizedLat.P99Ns)/1e3, c2row.WriteOps, c2row.WritesTemporalDistribution,
			float64(sqrow.IndividualLat.P50Ns)/1e3, float64(sqrow.IndividualLat.P99Ns)/1e3, sqrow.WriteOps, sqrow.WritesTemporalDistribution)
	}

	// Tri des 3 campagnes pour retenir la médiane (par latence individuelle p99 de c2db, exigence stricte du Plan Directeur)
	sortedIndices := []int{0, 1, 2}
	sort.Slice(sortedIndices, func(i, j int) bool {
		return campaigns[sortedIndices[i]].C2Row.IndividualLat.P99Ns < campaigns[sortedIndices[j]].C2Row.IndividualLat.P99Ns
	})
	medianIdx := sortedIndices[numPasses/2]
	medianCampaign := campaigns[medianIdx]
	medianRows := []latencyProfileRow{medianCampaign.C2Row, medianCampaign.SQLiteRow}

	logLatencyTable(t, medianRows)
	for _, r := range medianRows {
		t.Logf("%s write_ops=%d write_errors=%d read_ops=%d duration=%.3fs indiv_min=%dns indiv_mean=%.0fns indiv_p99=%.0fns batch_p99=%.0fns dist=%v note=%s",
			r.Engine, r.WriteOps, r.WriteErrors, r.ReadOps, r.DurationS,
			r.IndividualLat.MinNs, r.IndividualLat.MeanNs, float64(r.IndividualLat.P99Ns), float64(r.BatchAmortizedLat.P99Ns),
			r.WritesTemporalDistribution, r.Note)
	}

	report := latencyProfileReport{
		RaceEnabled:             raceDetectorActive,
		ClockCallOverheadNs:     clockOverheadNs,
		MeasurementMethod:       "dual_individual_and_batch_amortized: 32768 lectures individuelles instrumentées individuellement et 1024 lots de 32 lectures",
		CampaignsCount:          numPasses,
		SelectedMedianIndex:     medianIdx + 1,
		SelectedMedianCriterion: "c2db.individual_latency_ns.p99_ns",
		MedianCampaign:          medianRows,
		AllCampaigns:            campaigns[:],
	}

	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(latJSON, raw, 0o644); err != nil {
		t.Logf("ecriture %s: %v", latJSON, err)
	} else {
		t.Logf("rapport complet de latence ecrit dans %s (race_enabled=%v, indiv_p99=%.2fµs, batch_p99=%.2fµs, 3 campagnes)",
			latJSON, raceDetectorActive, float64(medianCampaign.C2Row.IndividualLat.P99Ns)/1e3, float64(medianCampaign.C2Row.BatchAmortizedLat.P99Ns)/1e3)
	}
}

func TestLatencyProfile_PercentileExactness(t *testing.T) {
	in := make([]int64, 1000)
	for i := range in {
		in[i] = int64(i + 1)
	}
	lat := latencyPct(in)
	if lat.Count != 1000 {
		t.Fatalf("count=%d", lat.Count)
	}
	if lat.MinNs != 1 || lat.MaxNs != 1000 {
		t.Fatalf("min/max=%d/%d", lat.MinNs, lat.MaxNs)
	}
	if lat.P50Ns != 500 && lat.P50Ns != 501 {
		t.Fatalf("p50=%d want ~500", lat.P50Ns)
	}
	if lat.P95Ns < 950 || lat.P95Ns > 951 {
		t.Fatalf("p95=%d want ~950", lat.P95Ns)
	}
	if lat.P99Ns < 990 || lat.P99Ns > 991 {
		t.Fatalf("p99=%d want ~990", lat.P99Ns)
	}
	if lat.P999Ns < 999 || lat.P999Ns > 1000 {
		t.Fatalf("p99.9=%d want ~999", lat.P999Ns)
	}
}
