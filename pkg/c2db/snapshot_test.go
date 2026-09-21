// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRoundtrip(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 3)
	}
	const shardID uint16 = 7

	s := mustOpenShard(t, dir, key, shardID)
	defer func() { _ = s.Close() }()

	k := []byte("k")
	v := []byte("v")
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}

	id, err := s.Freeze()
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	recs, err := s.wal.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) == 0 {
		t.Fatalf("WAL vide après Put")
	}
	if id != recs[len(recs)-1].ID {
		t.Fatalf("Freeze id distinct du dernier record")
	}

	dst := filepath.Join(dir, "snap.c2sn")
	if err := Export(s, dst, key); err != nil {
		t.Fatalf("Export: %v", err)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(raw) < SnapHdrSize {
		t.Fatalf("pack trop court: %d", len(raw))
	}
	if magic := binary.LittleEndian.Uint32(raw[snapMagicOff:]); magic != SnapMagic {
		t.Fatalf("magic=%#x, want %#x (C2SN)", magic, SnapMagic)
	}
	if magic := binary.LittleEndian.Uint32(raw[snapMagicOff:]); magic != 0x4332534E {
		t.Fatalf("magic=%#x, want 0x4332534E", magic)
	}
	if err := VerifySnapshot(dst, key); err != nil {
		t.Fatalf("VerifySnapshot: %v", err)
	}

	destPub := make([]byte, shardBytes)
	if err := Apply(dst, destPub, key); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(destPub, s.pub) {
		t.Fatalf("tas reconstitué distinct de pub")
	}

	var maxSnap [16]byte
	for i := range maxSnap {
		maxSnap[i] = 0xFF
	}
	out := make([]byte, 8)
	g := Db_bt_get_as_of_heap(destPub, shardBytes, heapPages, s.heapRoot, k, uint64(len(k)), maxSnap[:], out, uint64(len(out)))
	decoded, err := decodeValue(destPub, heapPages, out[:g.Len_])
	if g.Found != 1 || err != nil || !bytes.Equal(decoded, v) {
		t.Fatalf("GetAsOf destPub: %+v decoded=%q err=%v", g, decoded, err)
	}
	got, err := s.GetAsOf(k, maxSnap)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("GetAsOf shard: err=%v val=%q", err, got)
	}
}

func TestSnapshotTamper(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 5)
	}
	s := mustOpenShard(t, dir, key, 3)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	dst := filepath.Join(dir, "snap.c2sn")
	if err := Export(s, dst, key); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := VerifySnapshot(dst, key); err != nil {
		t.Fatalf("VerifySnapshot intact: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) <= snapIDOff {
		t.Fatalf("pack trop court: %d", len(data))
	}
	data[snapIDOff] ^= 0xFF
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("WriteFile tamper: %v", err)
	}
	if err := VerifySnapshot(dst, key); err == nil {
		t.Fatalf("VerifySnapshot après falsification: expected error")
	}
}

func TestSnapshotEncRoundtrip(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 7)
	}
	var encKey [32]byte
	for i := range encKey {
		encKey[i] = byte(i + 11)
	}
	s := mustOpenShard(t, dir, key, 9)
	defer func() { _ = s.Close() }()
	k := []byte("k")
	v := []byte("v-enc")
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}
	dst := filepath.Join(dir, "snap-enc.c2sn")
	if err := ExportEnc(s, dst, key, encKey); err != nil {
		t.Fatalf("ExportEnc: %v", err)
	}
	if err := VerifySnapshot(dst, key); err != nil {
		t.Fatalf("VerifySnapshot sans encKey: %v", err)
	}
	destPub := make([]byte, shardBytes)
	if err := Apply(dst, destPub, key); err != errSnapNeedEnc {
		t.Fatalf("Apply sans encKey: %v", err)
	}
	if err := ApplyEnc(dst, destPub, key, encKey); err != nil {
		t.Fatalf("ApplyEnc: %v", err)
	}
	if !bytes.Equal(destPub, s.pub) {
		t.Fatalf("tas reconstitué distinct de pub")
	}
}

func TestSnapshotEncTamper(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 13)
	}
	var encKey [32]byte
	for i := range encKey {
		encKey[i] = byte(i + 17)
	}
	s := mustOpenShard(t, dir, key, 4)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	dst := filepath.Join(dir, "snap-enc.c2sn")
	if err := ExportEnc(s, dst, key, encKey); err != nil {
		t.Fatalf("ExportEnc: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) <= SnapHdrSize {
		t.Fatalf("pack trop court: %d", len(data))
	}
	data[SnapHdrSize] ^= 0xFF
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("WriteFile tamper: %v", err)
	}
	if err := VerifySnapshot(dst, key); err == nil {
		t.Fatalf("VerifySnapshot après falsification ciphertext: expected error")
	}
}

func TestSnapshotHeapRoundtrip(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 23)
	}
	const shardID uint16 = 5

	s := mustOpenShard(t, dir, key, shardID)
	defer func() { _ = s.Close() }()

	val := make([]byte, 1200)
	for i := range val {
		val[i] = byte(i * 7)
	}
	n := 0
	for i := 0; i < 64; i++ {
		k := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', 's', 'n', 'a', 'p'}
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		n++
		if s.heapUsed >= 3 {
			break
		}
	}
	if s.heapUsed < 3 {
		t.Fatalf("feuille non saturée: used=%d n=%d", s.heapUsed, n)
	}

	dst := filepath.Join(dir, "snap-heap.c2sn")
	if err := Export(s, dst, key); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := VerifySnapshot(dst, key); err != nil {
		t.Fatalf("VerifySnapshot: %v", err)
	}

	destPub := make([]byte, shardBytes)
	if err := Apply(dst, destPub, key); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !bytes.Equal(destPub, s.pub) {
		t.Fatalf("tas reconstitué distinct de pub")
	}

	root := binary.LittleEndian.Uint64(destPub[heapRootOff : heapRootOff+8])
	used := binary.LittleEndian.Uint64(destPub[heapUsedOff : heapUsedOff+8])
	if root != s.heapRoot || used != s.heapUsed {
		t.Fatalf("root/used reconstitué (%d, %d) != shard (%d, %d)", root, used, s.heapRoot, s.heapUsed)
	}

	var maxSnap [16]byte
	for i := range maxSnap {
		maxSnap[i] = 0xFF
	}

	for i := 0; i < n; i++ {
		k := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', 's', 'n', 'a', 'p'}
		got, err := getAsOfHeap(destPub, root, k, maxSnap)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("GetAsOf destPub %d: err=%v val=%q", i, err, got)
		}
		gotShard, err := s.Get(k)
		if err != nil || !bytes.Equal(gotShard, val) {
			t.Fatalf("Get shard %d: err=%v val=%q", i, err, gotShard)
		}
	}

	dir2 := t.TempDir()
	s2 := mustOpenShard(t, dir2, key, shardID)
	defer func() { _ = s2.Close() }()

	if err := Apply(dst, s2.pub, key); err != nil {
		t.Fatalf("Apply dans s2.pub: %v", err)
	}
	copy(s2.dirty, s2.pub)
	s2.heapRoot = binary.LittleEndian.Uint64(s2.pub[heapRootOff : heapRootOff+8])
	s2.heapUsed = binary.LittleEndian.Uint64(s2.pub[heapUsedOff : heapUsedOff+8])

	for i := 0; i < n; i++ {
		k := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', 's', 'n', 'a', 'p'}
		got, err := s2.Get(k)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("s2.Get %d: err=%v val=%q", i, err, got)
		}
	}
}

// TestSnapshot_LargeShard_Truncation prouve que exportSnap/applySnap couvrent la taille
// pleine du shard (len(s.pub)), pas seulement shardBytes (64 MiB). Rouge d'abord : le
// source actuel tronque à 64 MiB, les clés au-delà sont perdues au roundtrip.
func TestSnapshot_LargeShard_Truncation(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{42, 43, 44}
	s := mustOpenShard(t, dir, key, 0, WithHeapPages(8192)) // 128 Mo
	defer func() { _ = s.Close() }()

	// 33 payloads de 2 Mo : ~4158 pages heap, au-delà des 4096 pages (64 MiB).
	payloads := make(map[string][]byte, 33)
	for i := 0; i < 33; i++ {
		data := make([]byte, 2*1024*1024)
		for j := range data {
			data[j] = byte(i + j)
		}
		k := fmt.Sprintf("trunc_%02d", i)
		payloads[k] = data
		if err := s.Put([]byte(k), data); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}

	dstPath := filepath.Join(dir, "snap_large.bin")
	if err := Export(s, dstPath, key); err != nil {
		t.Fatalf("Export: %v", err)
	}

	dir2 := t.TempDir()
	s2 := mustOpenShard(t, dir2, key, 0, WithHeapPages(8192))
	defer func() { _ = s2.Close() }()

	if err := Apply(dstPath, s2.pub, key); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	copy(s2.dirty, s2.pub)
	s2.heapRoot = binary.LittleEndian.Uint64(s2.pub[heapRootOff : heapRootOff+8])
	s2.heapUsed = binary.LittleEndian.Uint64(s2.pub[heapUsedOff : heapUsedOff+8])

	for k, want := range payloads {
		got, err := s2.Get([]byte(k))
		if err != nil {
			t.Fatalf("s2.Get %s: %v (troncature snapshot au-delà de 64 MiB)", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("s2.Get %s mismatch: len got=%d want=%d", k, len(got), len(want))
		}
	}
}
