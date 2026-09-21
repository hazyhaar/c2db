// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"context"
	"sync"
	"testing"
	"time"
)

// writeOptionTestKey construit une clé réelle routée de façon déterministe,
// distincte des clés du banc de politique d'ouverture.
func writeOptionTestKey(seed byte) []byte {
	return []byte{'w', 'o', seed, 0x00, 'k'}
}

// writeOptionKeyForShard retourne une clé routée vers le shard demandé, sans
// donnée de complaisance : la clé est construite puis vérifiée par Route.
func writeOptionKeyForShard(id uint16) []byte {
	for i := 0; i < 1<<16; i++ {
		k := []byte{'w', 'o', byte(i), byte(i >> 8), 0x00, 'k'}
		if Route(k) == id {
			return k
		}
	}
	return nil
}

// ackSink est un ReplicationSink de test : chaque Publish attribue une
// séquence monotone, et WaitAck bloque jusqu'à ce que le banc libère
// l'acquittement. Il matérialise le quorum sans donnée de complaisance.
type ackSink struct {
	mu          sync.Mutex
	seq         uint64
	acked       uint64
	release     chan struct{}
	releaseOnce sync.Once
}

func newAckSink() *ackSink {
	return &ackSink{release: make(chan struct{})}
}

func (s *ackSink) Publish(shard uint16, recType byte, key, val []byte) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return s.seq
}

func (s *ackSink) WaitAck(ctx context.Context, seq uint64) error {
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	if seq > s.acked {
		s.acked = seq
	}
	s.mu.Unlock()
	return nil
}

func (s *ackSink) releaseAll() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func (s *ackSink) snapshot() (seq, acked uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq, s.acked
}

// TestWriteOptionWindowedOverridesOpeningImmediate prouve qu'une écriture
// demandant explicitement une fenêtre n'est PAS pointée avant l'échéance, même
// quand l'ouverture est CommitImmediate. Avant le correctif, WithDurability
// n'existe pas : le banc ne compile pas (rouge d'absence d'API). Après, la
// demande par appel gouverne le pointage.
func TestWriteOptionWindowedOverridesOpeningImmediate(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 101)
	}
	const window = 150 * time.Millisecond
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	if db.commitPolicy.Mode != CommitImmediate {
		t.Fatalf("politique d'ouverture = %v, attendu CommitImmediate", db.commitPolicy.Mode)
	}

	k := writeOptionTestKey(1)
	v := []byte("per-call-windowed-value-bit-exact")
	s, err := db.GetShard(Route(k))
	if err != nil {
		t.Fatalf("GetShard: %v", err)
	}
	if err := db.Put(k, v, WithDurability(CommitPolicy{Mode: CommitWindowed, Window: window})); err != nil {
		t.Fatalf("Put avec fenêtre par appel: %v", err)
	}

	time.Sleep(window / 5)
	s.walMu.Lock()
	early := s.unflushed > 0
	s.walMu.Unlock()
	if !early {
		t.Fatalf("écriture pointée avant l'échéance de la fenêtre par appel")
	}
	if !waitPointed(t, s, 3*time.Second) {
		t.Fatalf("écriture non pointée après la fenêtre par appel (unflushed=%d)", s.unflushed)
	}
	got, err := db.Get(k)
	if err != nil {
		t.Fatalf("Get après pointage: %v", err)
	}
	if string(got) != string(v) {
		t.Fatalf("valeur altérée: %q, attendu %q", got, v)
	}
}

// TestWriteOptionImmediateOverridesOpeningWindowed prouve qu'une écriture
// demandant CommitImmediate est pointée au retour, même si l'ouverture est
// CommitWindowed avec une fenêtre longue.
func TestWriteOptionImmediateOverridesOpeningWindowed(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 113)
	}
	db, err := OpenDB(dir, key, WithCommitPolicy(CommitPolicy{Mode: CommitWindowed, Window: time.Hour}))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	k := writeOptionTestKey(2)
	v := []byte("per-call-immediate-value-bit-exact")
	s, err := db.GetShard(Route(k))
	if err != nil {
		t.Fatalf("GetShard: %v", err)
	}
	if err := db.Put(k, v, WithDurability(CommitPolicy{Mode: CommitImmediate})); err != nil {
		t.Fatalf("Put immédiat par appel: %v", err)
	}

	s.walMu.Lock()
	pointed := s.unflushed == 0 && s.wal.pendingN == 0 && s.wal.qCount == 0
	s.walMu.Unlock()
	if !pointed {
		t.Fatalf("écriture non pointée au retour d'un CommitImmediate par appel (unflushed=%d pendingN=%d qCount=%d)",
			s.unflushed, s.wal.pendingN, s.wal.qCount)
	}
	db.committer.mu.Lock()
	_, pending := db.committer.pending[s]
	db.committer.mu.Unlock()
	if pending {
		t.Fatalf("shard laissé en attente du committer après un pointage immédiat")
	}
}

// TestWriteOptionReplicatedWaitsQuorumAck prouve qu'une écriture demandant
// CommitReplicated ne rend pas tant que le ReplicationSink n'a pas acquitté la
// séquence publiée, puis rend nil après l'acquittement.
func TestWriteOptionReplicatedWaitsQuorumAck(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 127)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	sink := newAckSink()
	db.SetReplicationSink(sink)

	k := writeOptionTestKey(3)
	v := []byte("per-call-replicated-value-bit-exact")

	done := make(chan error, 1)
	go func() {
		done <- db.Put(k, v, WithDurability(CommitPolicy{Mode: CommitReplicated}))
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		seq, _ := sink.snapshot()
		if seq > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("aucune publication vers le sink avant échéance")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("Put répliqué rendu avant acquittement de quorum: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	sink.releaseAll()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Put répliqué après acquittement: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Put répliqué bloqué après acquittement de quorum")
	}
	seq, acked := sink.snapshot()
	if acked == 0 || acked < seq {
		t.Fatalf("acquittement incohérent: acked=%d seq=%d", acked, seq)
	}
}

// TestWriteOptionAbsentKeepsOpeningPolicy prouve la rétrocompatibilité : un Put
// sans option garde la politique d'ouverture. Sous CommitImmediate, l'écriture
// est pointée dans la fenêtre par défaut.
func TestWriteOptionAbsentKeepsOpeningPolicy(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 139)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	k := writeOptionTestKey(4)
	if err := db.Put(k, []byte("legacy-value")); err != nil {
		t.Fatalf("Put sans option: %v", err)
	}
	s, err := db.GetShard(Route(k))
	if err != nil {
		t.Fatalf("GetShard: %v", err)
	}
	if !waitPointed(t, s, 2*time.Second) {
		t.Fatalf("Put sans option non pointé dans la fenêtre d'ouverture")
	}
}

var (
	_ WriteOption     = WithDurability(CommitPolicy{Mode: CommitImmediate})
	_ ReplicationSink = (*ackSink)(nil)
)
