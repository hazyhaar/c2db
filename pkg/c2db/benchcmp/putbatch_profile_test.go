// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2db"
)

// BenchmarkC2PutBatch32 mesure le lot de 32 paires réelles sur un shard chaud.
// Le lot et les unitaires portent sur le MÊME corpus réel (sources.db, k_rules.db).
func BenchmarkC2PutBatch32(b *testing.B) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 21)
	}
	dir := b.TempDir()
	s := openC2(b, dir, key)
	defer func() { _ = s.Close() }()

	const batch = 32
	pairs := make([][2][]byte, batch)
	for j := 0; j < batch; j++ {
		pairs[j] = [2][]byte{keys[j], vals[j]}
	}
	if err := s.PutBatch(pairs); err != nil {
		b.Fatalf("seed PutBatch: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	base := batch
	for i := 0; i < b.N; i++ {
		if base+batch > nKeys {
			base = 0
		}
		for j := 0; j < batch; j++ {
			pairs[j] = [2][]byte{keys[base+j], vals[base+j]}
		}
		if err := s.PutBatch(pairs); err != nil {
			b.Fatalf("PutBatch: %v", err)
		}
		base += batch
	}
}

// BenchmarkC2PutSeq32 mesure 32 Put unitaires réels sur le même shard chaud.
func BenchmarkC2PutSeq32(b *testing.B) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 22)
	}
	dir := b.TempDir()
	s := openC2(b, dir, key)
	defer func() { _ = s.Close() }()

	const batch = 32
	if err := s.Put(keys[0], vals[0]); err != nil {
		b.Fatalf("seed Put: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	base := 1
	for i := 0; i < b.N; i++ {
		if base+batch > nKeys {
			base = 0
		}
		for j := 0; j < batch; j++ {
			if err := s.Put(keys[base+j], vals[base+j]); err != nil {
				b.Fatalf("Put: %v", err)
			}
		}
		base += batch
	}
}

// TestPutBatchSyncCounter mesure le nombre de vidages noyau d'un lot et d'une
// séquence unitaire équivalente (même corpus réel), sans chronométrage.
func TestPutBatchSyncCounter(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 23)
	}
	const batch = 32

	open := func() (*c2db.Shard, func()) {
		dir := t.TempDir()
		s := openC2(t, dir, key)
		return s, func() { _ = s.Close() }
	}

	s1, c1 := open()
	before := snapProbe()
	pairs := make([][2][]byte, batch)
	for j := 0; j < batch; j++ {
		pairs[j] = [2][]byte{keys[j], vals[j]}
	}
	if err := s1.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	d1 := snapProbe().Sub(before)
	c1()
	t.Logf("PutBatch(32): sysc_write=%d write_bytes=%d nvcsw=%d", d1.SyscWrite, d1.WriteBytes, d1.Nvcsw)

	s2, c2 := open()
	before = snapProbe()
	for j := 0; j < batch; j++ {
		if err := s2.Put(keys[j], vals[j]); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	d2 := snapProbe().Sub(before)
	c2()
	t.Logf("Put x32: sysc_write=%d write_bytes=%d nvcsw=%d", d2.SyscWrite, d2.WriteBytes, d2.Nvcsw)
}

// TestTortureRepro reproduit la séquence du banc stratifié : 512 Put réels,
// puis un lot réel de 32 paires, puis la transaction SQLite équivalente.
// Il rapporte le total du lot et la somme de 32 Put unitaires.
func TestTortureRepro(t *testing.T) {
	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 31)
	}
	dir := t.TempDir()
	s := openC2(t, dir, mac)
	defer func() { _ = s.Close() }()
	sq, err := openSQLite(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer sq.Close()

	const n = 512
	const batch = 32
	for i := 0; i < n; i++ {
		if err := s.Put(keys[i], vals[i]); err != nil {
			t.Fatalf("seed Put: %v", err)
		}
	}

	pairs := make([][2][]byte, batch)
	for j := 0; j < batch; j++ {
		pairs[j] = [2][]byte{keys[n+j], vals[n+j]}
	}

	batchNs, _, _ := measure(batch, func() {
		if err := s.PutBatch(pairs); err != nil {
			t.Errorf("PutBatch: %v", err)
		}
	})

	unitNs, _, _ := measure(batch, func() {
		for j := 0; j < batch; j++ {
			if err := s.Put(keys[n+batch+j], vals[n+batch+j]); err != nil {
				t.Errorf("Put: %v", err)
			}
		}
	})

	txNs, _, _ := measure(batch, func() {
		tx, err := sq.Begin()
		if err != nil {
			t.Errorf("begin: %v", err)
			return
		}
		for j := 0; j < batch; j++ {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, keys[j], vals[j]); err != nil {
				t.Errorf("tx insert: %v", err)
				_ = tx.Rollback()
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("commit: %v", err)
		}
	})

	t.Logf("c2db PutBatch(32): ns/paire=%.0f total=%.0f ns", batchNs, batchNs*batch)
	t.Logf("c2db Put x32:      ns/paire=%.0f total=%.0f ns", unitNs, unitNs*batch)
	t.Logf("sqlite tx x32:     ns/paire=%.0f total=%.0f ns", txNs, txNs*batch)
	if batchNs*batch > unitNs*batch {
		t.Errorf("lot plus lent que la somme des unitaires: lot=%.0f ns > unit=%.0f ns", batchNs*batch, unitNs*batch)
	}
}

// TestWALCheckpointCadence mesure le coût unitaire et la fréquence des
// pointages de journal sous une charge de lots réels, sur un journal
// volontairement réduit pour franchir plusieurs fois le seuil de recyclage.
func TestWALCheckpointCadence(t *testing.T) {
	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 51)
	}
	dir := t.TempDir()
	// Journal réduit à 2 Mio : le seuil de recyclage (moitié) tombe après
	// ~1 Mio d'avance WAL, ce qui force plusieurs pointages sous la charge.
	s, err := c2db.OpenShard(dir, mac, 1, c2db.WithWALBytes(2<<20))
	if err != nil {
		t.Skipf("OpenShard WAL réduit: %v", err)
	}

	const batch = 32
	const rounds = 4000
	pairs := make([][2][]byte, batch)
	base := 0
	t0 := time.Now()
	for r := 0; r < rounds; r++ {
		if base+batch > nKeys {
			base = 0
		}
		for j := 0; j < batch; j++ {
			pairs[j] = [2][]byte{keys[base+j], vals[base+j]}
		}
		if err := s.PutBatch(pairs); err != nil {
			_ = s.Close()
			t.Fatalf("PutBatch round %d: %v", r, err)
		}
		base += batch
	}
	elapsed := time.Since(t0)

	count, totalNs, maxNs := s.WALCheckpointStats()
	var perCheckpointNs float64
	if count > 0 {
		perCheckpointNs = float64(totalNs) / float64(count)
	}
	var batchesPerCheckpoint float64
	if count > 0 {
		batchesPerCheckpoint = float64(rounds) / float64(count)
	}

	// Coût d'un pointage explicite sur un shard au repos, pour référence.
	c0, _, _ := s.WALCheckpointStats()
	tCheck := time.Now()
	if err := s.Checkpoint(); err != nil {
		_ = s.Close()
		t.Fatalf("Checkpoint explicite: %v", err)
	}
	explicitNs := time.Since(tCheck).Nanoseconds()
	c1, _, _ := s.WALCheckpointStats()
	if c1 != c0+1 {
		_ = s.Close()
		t.Fatalf("compteur de pointage non incrémenté: %d -> %d", c0, c1)
	}

	t.Logf("cadence WAL: %d lots en %s, %d pointages, %.1f lots/pointage, %.1f µs/pointage (max %.1f µs), pointage explicite %.1f µs",
		rounds, elapsed.Round(time.Millisecond), count, batchesPerCheckpoint, perCheckpointNs/1e3, float64(maxNs)/1e3, float64(explicitNs)/1e3)

	if count == 0 {
		_ = s.Close()
		t.Fatalf("aucun pointage observé sous %d lots: le seuil de recyclage n'a pas été franchi", rounds)
	}
	if perCheckpointNs <= 0 {
		_ = s.Close()
		t.Fatalf("coût de pointage non mesuré")
	}

	// Non-régression de la reprise : la charge est relue après fermeture.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := c2db.OpenShard(dir, mac, 1)
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	defer func() { _ = s2.Close() }()
	for j := 0; j < batch; j++ {
		if _, err := s2.Get(keys[j]); err != nil {
			t.Fatalf("relecture après reprise clé %q: %v", keys[j], err)
		}
	}
}
