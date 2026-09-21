package c2db

import (
	"bytes"
	"math/rand"
	"path/filepath"
	"testing"
)

// TestW4TxGroupRetryAfterRepack prouve qu'un groupe transactionnel dont une
// mutation manque d'espace au rejeu est rejoué de façon indivisible : le filet
// W4 effectue un rollback complet, un repack préventif à la frontière du groupe,
// puis un second essai. La clé du groupe doit être présente et la clé de base
// antérieure intacte. Ce test échouait avant le correctif (groupe abandonné).
func TestW4TxGroupRetryAfterRepack(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// 1. Base stable, tas porté au seuil de capacité, puis scellé.
	s, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	if err := s.Put([]byte("w4-base-k"), []byte("w4-base-v")); err != nil {
		t.Fatalf("Put base: %v", err)
	}
	s.heapUsed = s.heapWatermark
	if err := s.publish(); err != nil {
		t.Fatalf("publish seuil: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close base: %v", err)
	}

	// 2. Groupe transactionnel scellé directement dans le journal : une
	// mutation RecPut sur une clé neuve, puis son RecTxCommit.
	walPath := filepath.Join(dir, "wal.img")
	w, err := OpenWAL(walPath, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	txID := [16]byte{0x5A, 0x04, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D}
	p := packTxKV([]byte("w4-tx-k"), []byte("w4-tx-v"))
	if err := w.Append(Record{ID: txID, Type: RecPut, Payload: p}); err != nil {
		t.Fatalf("Append tx put: %v", err)
	}
	if err := w.Append(Record{ID: txID, Type: RecTxCommit}); err != nil {
		t.Fatalf("Append tx commit: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush WAL: %v", err)
	}
	_ = w.Close()

	// 3. Réouverture : la première tentative est anticipée par le seuil
	// (applyInsertVer rend ErrHeapFull) ; le filet W4 repack puis rejoue.
	s2, err := OpenShard(dir, key, 0, WithHeapPages(4096))
	if err != nil {
		t.Fatalf("OpenShard rejeu: %v", err)
	}
	defer func() { _ = s2.Close() }()

	got, err := s2.Get([]byte("w4-tx-k"))
	if err != nil || !bytes.Equal(got, []byte("w4-tx-v")) {
		t.Fatalf("W4: groupe transactionnel abandonne malgre le repack: got=%q err=%v", got, err)
	}
	gotBase, err := s2.Get([]byte("w4-base-k"))
	if err != nil || !bytes.Equal(gotBase, []byte("w4-base-v")) {
		t.Fatalf("W4: cle de base alteree par le repack: got=%q err=%v", gotBase, err)
	}
}
