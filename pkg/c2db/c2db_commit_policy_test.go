// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"
)

// commitTestKey construit une clé réelle routée de façon déterministe.
func commitTestKey(seed byte) []byte {
	return []byte{'c', 'p', seed, 0x00, 'k'}
}

func waitPointed(t *testing.T, s *Shard, budget time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		s.walMu.Lock()
		pointed := s.unflushed == 0 && s.wal.pendingN == 0 && s.wal.qCount == 0
		s.walMu.Unlock()
		if pointed {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// TestCommitPolicyIsolatedWriteBounded prouve que la fenêtre de perte est
// bornée. Avant le correctif, syncWAL ne synchronise qu'au compteur
// (walCoalesceN=32) : une écriture isolée reste dans les tampons du journal
// indéfiniment. Le banc écrit UNE clé sur un shard neuf, attend au-delà de la
// fenêtre, puis simule une coupure non coopérative (KillWithoutFlush) et
// rouvre la base : la clé doit être rejouée.
func TestCommitPolicyIsolatedWriteBounded(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 41)
	}
	k := commitTestKey(1)
	v := []byte("isolated-window-value-bit-exact")
	shardID := Route(k)

	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	s, err := db.GetShard(shardID)
	if err != nil {
		t.Fatalf("GetShard: %v", err)
	}
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !waitPointed(t, s, 2*time.Second) {
		t.Fatalf("écriture isolée non pointée après la fenêtre bornée (unflushed=%d)", s.unflushed)
	}

	// Coupure simulée : aucun fsync supplémentaire n'est émis au moment de la
	// coupure. Le verrou écrivain sérialise la coupure avec le committer.
	if err := s.lockWriter(); err != nil {
		t.Fatalf("lockWriter: %v", err)
	}
	_ = s.KillWithoutFlush()
	s.unlockWriter()
	// Le shard est mort (EBADF) : Close arrête le committer sans le rouvrir.
	_ = db.Close()

	db2, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB réouverture: %v", err)
	}
	defer db2.Close()
	got, err := db2.Get(k)
	if err != nil {
		t.Fatalf("clé perdue après coupure simulée: %v", err)
	}
	if !bytes.Equal(got, v) {
		t.Fatalf("valeur altérée: %q, attendu %q", got, v)
	}
}

// TestCommitPolicySyncBarrier prouve que DB.Sync pointe explicitement tous les
// shards ayant des écritures non pointées. Sous CommitReplicated, le committer
// ne pointe pas selon la fenêtre : les écritures restent non pointées jusqu'à
// la barrière explicite, qui les rend durables et relisibles bit-exactement.
func TestCommitPolicySyncBarrier(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 53)
	}
	db, err := OpenDB(dir, key, WithCommitPolicy(CommitPolicy{Mode: CommitReplicated}))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if db.committer != nil {
		t.Fatalf("CommitReplicated ne doit pas démarrer de committer de fenêtre")
	}

	type item struct {
		k, v []byte
	}
	items := make([]item, 0, 5)
	for i := 0; i < 5; i++ {
		k := commitTestKey(byte(2 + i))
		v := []byte{'v', byte(i), 0xA5, byte(i + 7)}
		if err := db.Put(k, v); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		items = append(items, item{k, v})
		shardID := Route(k)
		s, err := db.GetShard(shardID)
		if err != nil {
			t.Fatalf("GetShard %d: %v", shardID, err)
		}
		s.walMu.Lock()
		pending := s.unflushed > 0
		s.walMu.Unlock()
		if !pending {
			t.Fatalf("CommitReplicated: écriture pointée sans Sync (shard %d)", shardID)
		}
	}

	if err := db.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	for i := range items {
		s, err := db.GetShard(Route(items[i].k))
		if err != nil {
			t.Fatalf("GetShard après Sync: %v", err)
		}
		if !waitPointed(t, s, time.Second) {
			t.Fatalf("shard %d non pointé après Sync (unflushed=%d)", s.id, s.unflushed)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB réouverture: %v", err)
	}
	defer db2.Close()
	for i := range items {
		got, err := db2.Get(items[i].k)
		if err != nil {
			t.Fatalf("clé %d perdue après Sync: %v", i, err)
		}
		if !bytes.Equal(got, items[i].v) {
			t.Fatalf("clé %d altérée: %q, attendu %q", i, got, items[i].v)
		}
	}
}

// TestCommitPolicyCentralCommitterSingleGoroutine prouve qu'une seule goroutine
// suffit pour N shards : tous les shards d'une base partagent le même committer
// central, et l'ouverture d'un grand nombre de shards ne crée pas une goroutine
// par shard.
func TestCommitPolicyCentralCommitterSingleGoroutine(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 67)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	if db.committer == nil {
		t.Fatalf("CommitImmediate doit démarrer le committer central")
	}

	base := runtime.NumGoroutine()
	const nShards = 24
	for i := 0; i < nShards; i++ {
		id := uint16(i)
		s, err := db.GetShard(id)
		if err != nil {
			t.Fatalf("GetShard %d: %v", id, err)
		}
		if s.committer != db.committer {
			t.Fatalf("shard %d: committer distinct du committer central", id)
		}
		k := []byte{'g', byte(id), byte(id >> 8)}
		if err := s.Put(k, []byte("central-committer-value")); err != nil {
			t.Fatalf("Put shard %d: %v", id, err)
		}
	}
	after := runtime.NumGoroutine()
	if delta := after - base; delta > 4 {
		t.Fatalf("goroutines +%d pour %d shards : committer non central", delta, nShards)
	}
}

// TestCommitPolicyWindowedHonorsWindow vérifie qu'une fenêtre explicite est
// respectée : avant l'échéance, l'écriture isolée reste non pointée ; après,
// elle est pointée dans la borne.
func TestCommitPolicyWindowedHonorsWindow(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 79)
	}
	const window = 200 * time.Millisecond
	db, err := OpenDB(dir, key, WithCommitPolicy(CommitPolicy{Mode: CommitWindowed, Window: window}))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	if db.commitPolicy.Mode != CommitWindowed || db.commitPolicy.Window != window {
		t.Fatalf("politique stockée = %+v, attendu Windowed/%v", db.commitPolicy, window)
	}

	k := commitTestKey(9)
	v := []byte("windowed-value-bit-exact")
	s, err := db.GetShard(Route(k))
	if err != nil {
		t.Fatalf("GetShard: %v", err)
	}
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(window / 5)
	s.walMu.Lock()
	early := s.unflushed > 0
	s.walMu.Unlock()
	if !early {
		t.Fatalf("écriture pointée avant l'échéance de la fenêtre")
	}
	if !waitPointed(t, s, 3*time.Second) {
		t.Fatalf("écriture non pointée après la fenêtre (unflushed=%d)", s.unflushed)
	}
}
