// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func txnTestKey(seed byte) [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func txnEventsOfKind(evs []TxnEvent, kind EventKind) []TxnEvent {
	var out []TxnEvent
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func txnHasKey(evs []TxnEvent, key []byte) bool {
	for i := range evs {
		if bytes.Equal(evs[i].Key, key) {
			return true
		}
	}
	return false
}

// TestEngineTxnLogEmitMutation vérifie qu'une mutation publiée (Put, PutBatch,
// Delete, Mut) produit un événement Mutation portant la clé et l'identifiant de
// version, relu depuis le journal par ReadTxLogDir.
func TestEngineTxnLogEmitMutation(t *testing.T) {
	dir := t.TempDir()
	txnDir := filepath.Join(dir, "events")
	s := mustOpenShard(t, dir, txnTestKey(141), 11, WithTxLog(txnDir))

	k1 := []byte("txn-mut-put")
	if err := s.Put(k1, []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	id1 := s.lastID

	k2a := []byte("txn-mut-batch-a")
	k2b := []byte("txn-mut-batch-b")
	if err := s.PutBatch([][2][]byte{{k2a, []byte("va")}, {k2b, []byte("vb")}}); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	batchLast := s.lastID

	k3 := []byte("txn-mut-del")
	if err := s.Put(k3, []byte("v3")); err != nil {
		t.Fatalf("Put del: %v", err)
	}
	if err := s.Delete(k3); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	delID := s.lastID

	k4 := []byte("txn-mut-mut")
	if err := s.Put(k4, []byte(`{}`)); err != nil {
		t.Fatalf("Put mut: %v", err)
	}
	if err := s.Mut(k4, []qlMutOp{{Op: opSetField, F: "a", V: json.RawMessage("1")}}); err != nil {
		t.Fatalf("Mut: %v", err)
	}
	mutID := s.lastID

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	evs, err := ReadTxLogDir(txnDir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	muts := txnEventsOfKind(evs, EventMutation)
	if len(muts) != 7 {
		t.Fatalf("mutations=%d, attendu 7", len(muts))
	}
	for _, k := range [][]byte{k1, k2a, k2b, k3, k4} {
		if !txnHasKey(muts, k) {
			t.Fatalf("clé %q absente des événements Mutation", k)
		}
	}
	if !txnHasID(muts, k1, id1) {
		t.Fatalf("Put: identifiant de version %x non consigné", id1)
	}
	if !txnHasID(muts, k2b, batchLast) {
		t.Fatalf("PutBatch: identifiant de dernière version %x non consigné", batchLast)
	}
	if !txnHasID(muts, k3, delID) {
		t.Fatalf("Delete: identifiant de version %x non consigné", delID)
	}
	if !txnHasID(muts, k4, mutID) {
		t.Fatalf("Mut: identifiant de version %x non consigné", mutID)
	}
}

func txnHasID(evs []TxnEvent, key []byte, id [16]byte) bool {
	for i := range evs {
		if bytes.Equal(evs[i].Key, key) && evs[i].VersionID == id {
			return true
		}
	}
	return false
}

// TestEngineTxnLogCompactionPrune vérifie que la compaction sous
// RetentionPruneSuperseded émet deux Compaction (avant/après) portant la
// rétention et la frontière, plus un Prune du nombre de versions abandonnées.
func TestEngineTxnLogCompactionPrune(t *testing.T) {
	dir := t.TempDir()
	txnDir := filepath.Join(dir, "events")
	s := mustOpenShard(t, dir, txnTestKey(151), 11,
		WithHistoryRetention(RetentionPruneSuperseded), WithTxLog(txnDir))

	k := []byte("txn-prune")
	if err := s.Put(k, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	ok, err := s.tryAutoCompact()
	if err != nil {
		t.Fatalf("tryAutoCompact: %v", err)
	}
	if !ok {
		t.Fatal("tryAutoCompact n'a pas compacté")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	evs, err := ReadTxLogDir(txnDir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	comps := txnEventsOfKind(evs, EventCompaction)
	if len(comps) != 2 {
		t.Fatalf("Compaction=%d, attendu 2 (avant/après)", len(comps))
	}
	var phases [2]bool
	for i := range comps {
		if len(comps[i].Payload) < 2 {
			t.Fatalf("Compaction %d: charge utile trop courte", i)
		}
		if comps[i].Payload[0] != byte(RetentionPruneSuperseded) {
			t.Fatalf("Compaction %d: rétention=%d, attendu %d", i, comps[i].Payload[0], RetentionPruneSuperseded)
		}
		if comps[i].Payload[1] > 1 {
			t.Fatalf("Compaction %d: phase invalide %d", i, comps[i].Payload[1])
		}
		phases[comps[i].Payload[1]] = true
	}
	if !phases[0] || !phases[1] {
		t.Fatalf("phases avant/après incomplètes: %v", phases)
	}

	prunes := txnEventsOfKind(evs, EventPrune)
	if len(prunes) != 1 {
		t.Fatalf("Prune=%d, attendu 1", len(prunes))
	}
	if len(prunes[0].Payload) != 8 {
		t.Fatalf("Prune: charge utile de %d octets, attendu 8", len(prunes[0].Payload))
	}
	if n := binary.LittleEndian.Uint64(prunes[0].Payload); n != 1 {
		t.Fatalf("Prune: versions supprimées=%d, attendu 1", n)
	}
}

// TestEngineTxnLogKeepAllNoPrune vérifie que la compaction sous
// RetentionKeepAll émet bien Compaction mais aucun Prune.
func TestEngineTxnLogKeepAllNoPrune(t *testing.T) {
	dir := t.TempDir()
	txnDir := filepath.Join(dir, "events")
	s := mustOpenShard(t, dir, txnTestKey(161), 11,
		WithHistoryRetention(RetentionKeepAll), WithTxLog(txnDir))

	k := []byte("txn-keep")
	if err := s.Put(k, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	ok, err := s.tryAutoCompact()
	if err != nil {
		t.Fatalf("tryAutoCompact: %v", err)
	}
	if !ok {
		t.Fatal("tryAutoCompact n'a pas compacté")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	evs, err := ReadTxLogDir(txnDir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	if got := len(txnEventsOfKind(evs, EventCompaction)); got != 2 {
		t.Fatalf("Compaction=%d, attendu 2", got)
	}
	if got := len(txnEventsOfKind(evs, EventPrune)); got != 0 {
		t.Fatalf("Prune=%d sous KeepAll, attendu 0", got)
	}
}

// TestEngineTxnLogRefusalKeepAll sature le tas sous RetentionKeepAll, où
// l'historique intégral doit survivre. L'écriture qui ne peut plus être
// admise échoue fermé par ErrHeapFull et consigne un Refusal, au lieu de
// perdre l'historique.
func TestEngineTxnLogRefusalKeepAll(t *testing.T) {
	dir := t.TempDir()
	txnDir := filepath.Join(dir, "events")
	s := mustOpenShard(t, dir, txnTestKey(171), 11,
		WithHistoryRetention(RetentionKeepAll), WithTxLog(txnDir))

	val := make([]byte, 2000)
	for i := range val {
		val[i] = byte(i * 11)
	}
	const batch = 1000
	var lastErr error
	for round := 0; round < 200; round++ {
		pairs := make([][2][]byte, batch)
		for j := range pairs {
			k := make([]byte, 8)
			binary.LittleEndian.PutUint32(k[0:4], uint32(round))
			binary.LittleEndian.PutUint32(k[4:8], uint32(j))
			pairs[j] = [2][]byte{k, val}
		}
		if lastErr = s.PutBatch(pairs); lastErr != nil {
			break
		}
	}
	if !errors.Is(lastErr, ErrHeapFull) {
		t.Fatalf("saturation: err=%v, attendu %v (heapUsed=%d watermark=%d)", lastErr, ErrHeapFull, s.heapUsed, s.heapWatermark)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	evs, err := ReadTxLogDir(txnDir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	if got := len(txnEventsOfKind(evs, EventRefusal)); got < 1 {
		t.Fatalf("Refusal=%d, attendu au moins 1", got)
	}
}

// TestDBSetEventSink vérifie qu'un puits posé sur la base s'applique aux
// shards ouverts ensuite, sans que la base ne ferme un puits qu'elle ne
// détient pas.
func TestDBSetEventSink(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	txnDir := filepath.Join(root, "events")
	db, err := OpenDB(dbDir, txnTestKey(181))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	l, err := OpenTxLog(txnDir, 0)
	if err != nil {
		t.Fatalf("OpenTxLog: %v", err)
	}
	db.SetEventSink(l)

	key := []byte("db-sink-key")
	if err := db.Put(key, []byte("value")); err != nil {
		t.Fatalf("DB.Put: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("DB.Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("TxLog.Close: %v", err)
	}

	evs, err := ReadTxLogDir(txnDir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	if !txnHasKey(txnEventsOfKind(evs, EventMutation), key) {
		t.Fatal("mutation de la base absente du journal partagé")
	}
}
