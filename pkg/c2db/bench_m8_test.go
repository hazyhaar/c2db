// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestM8_snap_read(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 11)
	}
	const shardID uint16 = 9
	k := []byte("snap-k")
	v := []byte("snap-v-bit-exact")

	s := mustOpenShard(t, dir, key, shardID)
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get chaud: err=%v val=%q", err, got)
	}
	crcWarm := Db_page_crc32c(s.pub, pageN)
	if crcWarm == 0 {
		t.Fatalf("CRC32-C mémoire nul après Put")
	}
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get page chaude: err=%v val=%q", err, got)
	}
	if crc2 := Db_page_crc32c(s.pub, pageN); crc2 != crcWarm {
		t.Fatalf("CRC32-C page chaude: %#x want %#x", crc2, crcWarm)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s = mustOpenShard(t, dir, key, shardID)
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get après reopen: err=%v val=%q", err, got)
	}
	crcSnap := Db_page_crc32c(s.pub, pageN)
	if crcSnap == 0 {
		t.Fatalf("CRC32-C après reopen nul")
	}
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get relecture chaude après reopen: err=%v val=%q", err, got)
	}
	if crc2 := Db_page_crc32c(s.pub, pageN); crc2 != crcSnap {
		t.Fatalf("CRC32-C page chaude après reopen: %#x want %#x", crc2, crcSnap)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func TestM8_hot_write(t *testing.T) {
	const n = uint64(16384)
	pub := make([]byte, n)
	dirty := make([]byte, n)
	for i := range pub {
		pub[i] = byte(i*17 + 3)
	}
	st := Db_bt_leaf_init(pub, n)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	snap := append([]byte(nil), pub...)

	k := []byte("alpha")
	v := []byte("bravo")
	got := Db_bt_insert(pub, dirty, n, &st, k, uint64(len(k)), v, uint64(len(v)))
	if got.Ok != 1 || got.Copied != 1 || got.Nkeys != 1 {
		t.Fatalf("insert CoW: %+v", got)
	}
	if !bytes.Equal(pub, snap) {
		t.Fatalf("pub muté par insert CoW")
	}

	out := make([]byte, 16)
	gDirty := Db_bt_get(dirty, n, k, uint64(len(k)), out, uint64(len(out)))
	if gDirty.Found != 1 || gDirty.Len_ != uint64(len(v)) {
		t.Fatalf("get dirty: %+v", gDirty)
	}
	if !bytes.Equal(out[:len(v)], v) {
		t.Fatalf("valeur dirty=%q", out[:len(v)])
	}
	gPub := Db_bt_get(pub, n, k, uint64(len(k)), out, uint64(len(out)))
	if gPub.Found != 0 {
		t.Fatalf("get pub a trouvé la clé avant publication")
	}
}

func TestM8_group_commit(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 5)
	}
	const shardID uint16 = 3
	const n = 8

	s := mustOpenShard(t, dir, key, shardID)
	wal0 := s.walFlushes
	data0 := s.dataFlushes
	blocks0 := s.wal.next

	pairs := make([][2][]byte, n)
	for i := 0; i < n; i++ {
		pairs[i] = [2][]byte{
			[]byte{'k', byte('0' + i)},
			[]byte{'v', byte('0' + i)},
		}
	}
	if err := s.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	for i := 0; i < n; i++ {
		got, err := s.Get(pairs[i][0])
		if err != nil || !bytes.Equal(got, pairs[i][1]) {
			t.Fatalf("Get %q: err=%v val=%q", pairs[i][0], err, got)
		}
	}

	walDelta := s.walFlushes - wal0
	dataDelta := s.dataFlushes - data0
	if walDelta != 1 {
		t.Fatalf("walFlushes=%d want 1", walDelta)
	}
	if dataDelta != 0 {
		t.Fatalf("dataFlushes=%d want 0 (barrière data différée au pointage)", dataDelta)
	}
	kBlocks := s.wal.next - blocks0
	if kBlocks == 0 {
		t.Fatalf("K blocs WAL: 0")
	}
	recs, err := s.wal.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) != n {
		t.Fatalf("N records: got %d want %d (K blocs=%d)", len(recs), n, kBlocks)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestM8_crash_write_flush(t *testing.T) {
	const durableN = 3
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)

	durable := make([]Record, durableN)
	for i := range durable {
		durable[i] = walCrashRecord(i, []byte("durable-payload-bit-exact-"+itoa(i)))
		if err := w.Append(durable[i]); err != nil {
			t.Fatalf("Append durable %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush durable: %v", err)
	}
	extra := walCrashRecord(durableN, []byte("unflushed-nplus1-bit-exact-payload"))
	if err := w.Append(extra); err != nil {
		t.Fatalf("Append N+1: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close without Flush: %v", err)
	}

	w = mustOpenWAL(t, path, key)
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay after crash write/flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close after replay: %v", err)
	}
	assertReplayPrefixOrNext(t, recs, durable, extra)

	xfer, checkCanaries := mmapXferWithCanaries(t)
	flipWALBlock(t, path, uint64(durableN), walPayloadOff, xfer)
	checkCanaries()

	w = mustOpenWAL(t, path, key)
	recs, replayErr := w.Replay()
	if err := w.Close(); err != nil {
		t.Fatalf("Close after corrupt replay: %v", err)
	}
	if replayErr == nil {
		t.Fatalf("Replay after N+1 flip: expected errWALBadTag, got %d records", len(recs))
	}
	if !errors.Is(replayErr, errWALBadTag) {
		t.Fatalf("Replay after N+1 flip: got %v, want errWALBadTag", replayErr)
	}
	if recs != nil {
		t.Fatalf("Replay after N+1 flip mixed records with N: got %d", len(recs))
	}
}

func TestM8_shards_empty(t *testing.T) {
	if NumShards != 1024 {
		t.Fatalf("NumShards=%d, want 1024", NumShards)
	}
	if maxShard != 1024 {
		t.Fatalf("maxShard=%d, want 1024", maxShard)
	}
	seen := make(map[uint16]struct{}, 64)
	var k [2]byte
	for i := 0; i < 4096; i++ {
		k[0] = byte(i >> 8)
		k[1] = byte(i)
		s := Route(k[:])
		if s >= NumShards {
			t.Fatalf("clé %d: shard %d >= %d", i, s, NumShards)
		}
		seen[s] = struct{}{}
	}
	if len(seen) < 16 {
		t.Fatalf("4096 clés n'ont touché que %d fragments, seuil 16", len(seen))
	}

	var a, b []byte
	var sa, sb uint16
	for i := 0; i < 1<<16; i++ {
		kk := []byte{byte(i >> 8), byte(i)}
		s := Route(kk)
		if a == nil {
			a = append([]byte(nil), kk...)
			sa = s
			continue
		}
		if s != sa {
			b = append([]byte(nil), kk...)
			sb = s
			break
		}
	}
	if b == nil || sa == sb {
		t.Fatalf("aucune paire de clés sur deux fragments distincts")
	}

	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 5)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := db.Put(a, []byte("va")); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Put a: %v", err)
	}
	if err := db.Put(b, []byte("vb")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if n := len(db.shards); n != 2 {
		t.Fatalf("fragments ouverts=%d, want 2", n)
	}
	gotA, err := db.Get(a)
	if err != nil || !bytes.Equal(gotA, []byte("va")) {
		t.Fatalf("Get a: err=%v val=%q", err, gotA)
	}
	gotB, err := db.Get(b)
	if err != nil || !bytes.Equal(gotB, []byte("vb")) {
		t.Fatalf("Get b: err=%v val=%q", err, gotB)
	}
	if _, err := db.GetShard(sa); err != nil {
		t.Fatalf("GetShard a: %v", err)
	}
	if _, err := db.GetShard(sb); err != nil {
		t.Fatalf("GetShard b: %v", err)
	}
	assertPreallocatedShard(t, dir, sa)
	assertPreallocatedShard(t, dir, sb)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestM8_gc_prefix(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}

	key := []byte("prefix_test_key")
	id1 := mkID(1)
	id2 := mkID(2)
	id3 := mkID(3)

	// Insertion de 3 versions successives
	r1 := Db_bt_insert_ver(pub, dirty, pageN, key, uint64(len(key)), id1, []byte("val_v1"), 6)
	if r1.Ok != 1 {
		t.Fatalf("insert v1: %+v", r1)
	}
	copy(pub, dirty)

	r2 := Db_bt_insert_ver(pub, dirty, pageN, key, uint64(len(key)), id2, []byte("val_v2"), 6)
	if r2.Ok != 1 {
		t.Fatalf("insert v2: %+v", r2)
	}
	copy(pub, dirty)

	r3 := Db_bt_insert_ver(pub, dirty, pageN, key, uint64(len(key)), id3, []byte("val_v3"), 6)
	if r3.Ok != 1 {
		t.Fatalf("insert v3: %+v", r3)
	}
	copy(pub, dirty)

	// Purge GC des versions antérieures à id3
	gc := Db_bt_gc_before(pub, dirty, pageN, id3)
	if gc.Ok != 1 {
		t.Fatalf("gc_before id3: %+v", gc)
	}

	out := make([]byte, 16)
	// v1 et v2 doivent avoir été collectées
	if g1 := Db_bt_get_as_of(dirty, pageN, key, uint64(len(key)), id1, out, uint64(len(out))); g1.Found != 0 {
		t.Fatalf("v1 trouvée après GC alors qu'elle doit être purgée: %+v", g1)
	}
	if g2 := Db_bt_get_as_of(dirty, pageN, key, uint64(len(key)), id2, out, uint64(len(out))); g2.Found != 0 {
		t.Fatalf("v2 trouvée après GC alors qu'elle doit être purgée: %+v", g2)
	}

	// v3 doit être présente et intacte
	g3 := Db_bt_get_as_of(dirty, pageN, key, uint64(len(key)), id3, out, uint64(len(out)))
	if g3.Found != 1 || g3.Len_ != 6 || string(out[:6]) != "val_v3" {
		t.Fatalf("v3 invalide après GC: %+v out=%q", g3, out[:g3.Len_])
	}
}

func assertPreallocatedShard(t *testing.T, dir string, id uint16) {
	t.Helper()
	subdir := filepath.Join(dir, fmt.Sprintf("%04x", id))
	files := []struct {
		name string
		size int64
	}{
		{"data.img", int64(shardBytes)},
		{"wal.img", int64(walBytes)},
	}
	for _, f := range files {
		p := filepath.Join(subdir, f.name)
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if st.Size() != f.size {
			t.Fatalf("%s size=%d want %d", p, st.Size(), f.size)
		}
	}
}
