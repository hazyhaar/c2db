// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestShardCycleComplete(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 3)
	}
	const shardID uint16 = 7

	s := mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	if err := s.Put([]byte("a"), []byte("va")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := s.Put([]byte("b"), []byte("vb")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	gotA, err := s.Get([]byte("a"))
	if err != nil || !bytes.Equal(gotA, []byte("va")) {
		t.Fatalf("Get a: err=%v val=%q", err, gotA)
	}
	gotB, err := s.Get([]byte("b"))
	if err != nil || !bytes.Equal(gotB, []byte("vb")) {
		t.Fatalf("Get b: err=%v val=%q", err, gotB)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s = mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)
	gotA, err = s.Get([]byte("a"))
	if err != nil || !bytes.Equal(gotA, []byte("va")) {
		t.Fatalf("Get a persisté: err=%v val=%q", err, gotA)
	}
	gotB, err = s.Get([]byte("b"))
	if err != nil || !bytes.Equal(gotB, []byte("vb")) {
		t.Fatalf("Get b persisté: err=%v val=%q", err, gotB)
	}

	id, err := NewID(uint64(time.Now().UnixNano()), s.id, s.counter)
	if err != nil {
		t.Fatalf("NewID c: %v", err)
	}
	s.counter++
	if err := s.wal.Append(Record{ID: id, Type: RecPut, Payload: packKV([]byte("c"), []byte("vc"))}); err != nil {
		t.Fatalf("Append c: %v", err)
	}
	if err := s.wal.Flush(); err != nil {
		t.Fatalf("WAL.Flush c: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close sans Write data: %v", err)
	}

	s = mustOpenShard(t, dir, key, shardID)
	gotC, err := s.Get([]byte("c"))
	if err != nil || !bytes.Equal(gotC, []byte("vc")) {
		t.Fatalf("redo WAL c: err=%v val=%q", err, gotC)
	}
	gotA, err = s.Get([]byte("a"))
	if err != nil || !bytes.Equal(gotA, []byte("va")) {
		t.Fatalf("Get a après redo: err=%v val=%q", err, gotA)
	}
	gotB, err = s.Get([]byte("b"))
	if err != nil || !bytes.Equal(gotB, []byte("vb")) {
		t.Fatalf("Get b après redo: err=%v val=%q", err, gotB)
	}

	if err := s.Put([]byte{}, []byte("x")); err == nil {
		t.Fatalf("Put clé vide: expected error")
	}
	if err := s.Put(nil, []byte("x")); err == nil {
		t.Fatalf("Put clé nil: expected error")
	}
	if err := s.Put([]byte("d"), []byte("vd")); err != nil {
		t.Fatalf("reprise après rejet: %v", err)
	}
	gotD, err := s.Get([]byte("d"))
	if err != nil || !bytes.Equal(gotD, []byte("vd")) {
		t.Fatalf("Get d reprise: err=%v val=%q", err, gotD)
	}
	gotC, err = s.Get([]byte("c"))
	if err != nil || !bytes.Equal(gotC, []byte("vc")) {
		t.Fatalf("Get c après reprise: err=%v val=%q", err, gotC)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func TestEngineMVCCTwoVersions(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 7)
	}
	const shardID uint16 = 5

	s := mustOpenShard(t, dir, key, shardID)
	k := []byte("k")
	if err := s.Put(k, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("Get: err=%v val=%q want v2", err, got)
	}
	recs, err := s.wal.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) < 2 {
		t.Fatalf("records: %d want ≥2", len(recs))
	}
	got1, err := s.GetAsOf(k, recs[0].ID)
	if err != nil || !bytes.Equal(got1, []byte("v1")) {
		t.Fatalf("GetAsOf v1: err=%v val=%q", err, got1)
	}
	got2, err := s.GetAsOf(k, recs[1].ID)
	if err != nil || !bytes.Equal(got2, []byte("v2")) {
		t.Fatalf("GetAsOf v2: err=%v val=%q", err, got2)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineGroupCommit(t *testing.T) {
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
	// Un lot n'émet qu'une barrière : la fdatasync du journal. Les pages du tas
	// sont matérialisées sans barrière, celle-ci étant différée au pointage.
	if dataDelta != 0 {
		t.Fatalf("dataFlushes=%d want 0 (barrière data différée au pointage)", dataDelta)
	}
	if walDelta+dataDelta != 1 {
		t.Fatalf("fdatasync=%d want 1 (une seule barrière par lot)", walDelta+dataDelta)
	}

	kBlocks := s.wal.next - blocks0
	if kBlocks != 1 {
		t.Fatalf("K blocs WAL=%d want 1 (lot empaqueté)", kBlocks)
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

func TestPagerWALBeforeData(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 13)
	}
	const shardID uint16 = 2
	k := []byte("pager-wal-k")
	v := []byte("pager-wal-v-bit-exact")

	s := mustOpenShard(t, dir, key, shardID)
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if s.pager.NDirty() == 0 {
		t.Fatalf("NDirty après Put=0, attendu > 0")
	}
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get chaud: err=%v val=%q", err, got)
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	assertDataPageLacksKey(t, filepath.Join(dir, "data.img"), k)

	s = mustOpenShard(t, dir, key, shardID)
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("redo WAL après crash data: err=%v val=%q", err, got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWALCrashWriteFlushBuffer(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 17)
	}
	const shardID uint16 = 4
	const durableN = 3

	s := mustOpenShard(t, dir, key, shardID)
	durable := make([][2][]byte, durableN)
	for i := range durable {
		durable[i] = [2][]byte{
			[]byte("dur-k-" + itoa(i)),
			[]byte("dur-v-bit-exact-" + itoa(i)),
		}
		if err := s.Put(durable[i][0], durable[i][1]); err != nil {
			t.Fatalf("Put durable %d: %v", i, err)
		}
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	dataPath := filepath.Join(dir, "data.img")
	for i := range durable {
		assertDataPageLacksKey(t, dataPath, durable[i][0])
	}

	s = mustOpenShard(t, dir, key, shardID)
	for i := range durable {
		got, err := s.Get(durable[i][0])
		if err != nil || !bytes.Equal(got, durable[i][1]) {
			t.Fatalf("redo %d: err=%v val=%q", i, err, got)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWALCrashWriteFlushBufferFuzz(t *testing.T) {
	const seed = int64(0xC2DBF108)
	pass1 := runPagerCrashFuzzPass(t, seed)
	pass2 := runPagerCrashFuzzPass(t, seed)
	if !bytes.Equal(pass1, pass2) {
		t.Fatalf("fuzz déterminisme: deux passes graine %d divergentes", seed)
	}
}

func runPagerCrashFuzzPass(t *testing.T, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var out []byte
	for i := 0; i < 8; i++ {
		n := 1 + rng.Intn(4)
		dir := t.TempDir()
		var key [32]byte
		rng.Read(key[:])
		s := mustOpenShard(t, dir, key, 1)
		pairs := make([][2][]byte, n)
		for j := 0; j < n; j++ {
			k := make([]byte, 4+rng.Intn(8))
			v := make([]byte, 8+rng.Intn(16))
			rng.Read(k)
			rng.Read(v)
			k[0] = byte(j + 1)
			pairs[j] = [2][]byte{k, v}
			if err := s.Put(k, v); err != nil {
				t.Fatalf("fuzz Put %d: %v", j, err)
			}
		}
		if err := s.CloseWithoutFlush(); err != nil {
			t.Fatalf("fuzz CloseWithoutFlush: %v", err)
		}
		s = mustOpenShard(t, dir, key, 1)
		for j := range pairs {
			got, err := s.Get(pairs[j][0])
			if err != nil || !bytes.Equal(got, pairs[j][1]) {
				t.Fatalf("fuzz redo %d: err=%v", j, err)
			}
			out = append(out, got...)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("fuzz Close: %v", err)
		}
		out = append(out, byte(n))
	}
	return out
}

// Tampon pager (retardé) : Put fait WAL.Flush puis laisse les pages sales ;
// CloseWithoutFlush les abandonne. Tampon off : PutBatch et Close font
// Write+Flush immédiat du tas. Pas de second chemin Shard sans pager.

func TestEngineHeapCrashUnflushed(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 29)
	}
	const shardID uint16 = 11
	k := []byte("heap-unflushed-k")
	v := []byte("heap-unflushed-v-bit-exact")

	s := mustOpenShard(t, dir, key, shardID)
	if uint64(len(s.pub)) != shardBytes {
		t.Fatalf("tas=%d, attendu %d", len(s.pub), shardBytes)
	}
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if s.pager.NDirty() == 0 {
		t.Fatalf("NDirty après Put=%d, attendu > 0", s.pager.NDirty())
	}
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get chaud: err=%v val=%q", err, got)
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	dataPath := filepath.Join(dir, "data.img")
	assertDataPageLacksKey(t, dataPath, k)

	s = mustOpenShard(t, dir, key, shardID)
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("redo WAL après crash pager: err=%v val=%q", err, got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineHeapCrashTamperPage(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 31)
	}
	const shardID uint16 = 12
	k := []byte("heap-tamper-k")
	v := []byte("heap-tamper-v-wal-payload")

	s := mustOpenShard(t, dir, key, shardID)
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if s.pager.NDirty() == 0 {
		t.Fatalf("pages sales absentes avant crash")
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	dataPath := filepath.Join(dir, "data.img")
	assertDataPageLacksKey(t, dataPath, k)
	xfer, checkCanaries := mmapXferWithCanaries(t)
	flipWALBlock(t, dataPath, 0, 100, xfer)
	checkCanaries()

	s = mustOpenShard(t, dir, key, shardID)
	got, err := s.Get(k)
	if err != nil {
		t.Fatalf("Get après tamper: %v", err)
	}
	if !bytes.Equal(got, v) {
		t.Fatalf("payload mélangé après tamper: got %q want %q", got, v)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	checkCanaries()
}

func TestEngineHeapCrashBufferOff(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 37)
	}
	const shardID uint16 = 13
	k := []byte("heap-buffer-off-k")
	v := []byte("heap-buffer-off-v-bit-exact")

	s := mustOpenShard(t, dir, key, shardID)
	if err := s.PutBatch([][2][]byte{{k, v}}); err != nil {
		t.Fatalf("PutBatch tampon off: %v", err)
	}
	if s.pager.NDirty() != 0 {
		t.Fatalf("NDirty après PutBatch=%d, attendu 0", s.pager.NDirty())
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	assertDataPageHasKey(t, filepath.Join(dir, "data.img"), k)

	s = mustOpenShard(t, dir, key, shardID)
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get tampon off: err=%v val=%q", err, got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineHeapCrashFuzz(t *testing.T) {
	const seed = int64(0xC2DBF10A)
	pass1 := runHeapCrashFuzzPass(t, seed)
	pass2 := runHeapCrashFuzzPass(t, seed)
	if !bytes.Equal(pass1, pass2) {
		t.Fatalf("fuzz déterminisme: deux passes graine %d divergentes", seed)
	}
}

func runHeapCrashFuzzPass(t *testing.T, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var out []byte
	for i := 0; i < 8; i++ {
		durableN := 1 + rng.Intn(4)
		tamper := rng.Intn(2) == 1
		flipOff := 64 + rng.Intn(LBASize-64)
		dir := t.TempDir()
		var key [32]byte
		rng.Read(key[:])
		s := mustOpenShard(t, dir, key, 1)
		durable := make([][2][]byte, durableN)
		for j := 0; j < durableN; j++ {
			k := make([]byte, 4+rng.Intn(8))
			v := make([]byte, 8+rng.Intn(16))
			rng.Read(k)
			rng.Read(v)
			k[0] = byte(j + 1)
			durable[j] = [2][]byte{k, v}
			if err := s.Put(k, v); err != nil {
				t.Fatalf("fuzz Put durable %d: %v", j, err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatalf("fuzz Close durable: %v", err)
		}

		s = mustOpenShard(t, dir, key, 1)
		extraK := make([]byte, 4+rng.Intn(8))
		extraV := make([]byte, 8+rng.Intn(16))
		rng.Read(extraK)
		rng.Read(extraV)
		extraK[0] = 0xE0
		if err := s.Put(extraK, extraV); err != nil {
			t.Fatalf("fuzz Put N+1: %v", err)
		}
		if err := s.CloseWithoutFlush(); err != nil {
			t.Fatalf("fuzz CloseWithoutFlush: %v", err)
		}

		dataPath := filepath.Join(dir, "data.img")
		assertDataPageLacksKey(t, dataPath, extraK)
		xfer, checkCanaries := mmapXferWithCanaries(t)
		if tamper {
			flipWALBlock(t, dataPath, 0, flipOff, xfer)
		}
		checkCanaries()

		s = mustOpenShard(t, dir, key, 1)
		for j := range durable {
			got, err := s.Get(durable[j][0])
			if err != nil || !bytes.Equal(got, durable[j][1]) {
				t.Fatalf("fuzz N %d: err=%v val=%q", j, err, got)
			}
			out = append(out, got...)
		}
		got, err := s.Get(extraK)
		if err != nil || !bytes.Equal(got, extraV) {
			t.Fatalf("fuzz N+1 redo: err=%v val=%q", err, got)
		}
		out = append(out, got...)
		if err := s.Close(); err != nil {
			t.Fatalf("fuzz Close: %v", err)
		}
		out = append(out, byte(durableN), byte(flipOff), byte(boolToByte(tamper)))
		checkCanaries()
	}
	return out
}

func boolToByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func assertDataPageHasKey(t *testing.T, path string, key []byte) {
	t.Helper()
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open data: %v", err)
	}
	defer func() { _ = dev.Close() }()
	heap := mmapAligned(t, int(shardBytes))
	if err := dev.Read(0, heap); err != nil {
		t.Fatalf("Read tas: %v", err)
	}
	if !bytes.Contains(heap, key) {
		t.Fatalf("tas data absente %q après Write+Flush", key)
	}
}

func assertDataPageLacksKey(t *testing.T, path string, key []byte) {
	t.Helper()
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open data: %v", err)
	}
	defer func() { _ = dev.Close() }()
	page := mmapAligned(t, int(pageN))
	if err := dev.Read(0, page); err != nil {
		t.Fatalf("Read data LBA 0: %v", err)
	}
	g := Db_bt_get(page, pageN, key, uint64(len(key)), nil, 0)
	if g.Found != 0 {
		t.Fatalf("page data contient %q avant redo", key)
	}
}

func mustOpenShard(t *testing.T, dir string, key [32]byte, id uint16, opts ...Option) *Shard {
	t.Helper()
	s, err := OpenShard(dir, key, id, opts...)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	return s
}

func assertShardODirect(t *testing.T, s *Shard) {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(s.data.fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL data: %v", err)
	}
	if flags&unix.O_DIRECT == 0 {
		t.Fatalf("data fd lacks O_DIRECT (flags=%#x)", flags)
	}
	flags, err = unix.FcntlInt(uintptr(s.wal.dev.fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL wal: %v", err)
	}
	if flags&unix.O_DIRECT == 0 {
		t.Fatalf("wal fd lacks O_DIRECT (flags=%#x)", flags)
	}
}

func TestEngineHeapOverflow(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 19)
	}
	const shardID uint16 = 6

	s := mustOpenShard(t, dir, key, shardID)
	val := make([]byte, 1200)
	for i := range val {
		val[i] = byte(i * 5)
	}
	n := 0
	for i := 0; i < 64; i++ {
		k := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', '-', '-'}
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
	rootOff := s.heapRoot * pageN
	if rootOff+20 >= uint64(len(s.pub)) {
		t.Fatalf("root hors tas: %+v", s.heapRoot)
	}
	if s.pub[rootOff+20] != 2 {
		t.Fatalf("root type=%d want TYPE_INTERNAL=2 root=%d used=%d", s.pub[rootOff+20], s.heapRoot, s.heapUsed)
	}
	for i := 0; i < n; i++ {
		k := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', '-', '-'}
		got, err := s.Get(k)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("Get %d: err=%v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s = mustOpenShard(t, dir, key, shardID)
	if s.heapUsed < 3 {
		t.Fatalf("used persisté=%d", s.heapUsed)
	}
	rootOff = s.heapRoot * pageN
	if s.pub[rootOff+20] != 2 {
		t.Fatalf("root persisté type=%d root=%d", s.pub[rootOff+20], s.heapRoot)
	}
	for i := 0; i < n; i++ {
		k := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', '-', '-'}
		got, err := s.Get(k)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("Get persisté %d: err=%v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func TestEngineHeapFull(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 31)
	}
	const shardID uint16 = 9

	s := mustOpenShard(t, dir, key, shardID)
	val := make([]byte, 1200)
	for i := range val {
		val[i] = byte(i * 7)
	}

	var insertedKeys [][]byte
	var heapFullHit bool
	for i := 0; i < 100; i++ {
		k := []byte{byte(i), byte(i >> 8), 'f', 'u', 'l', 'l', '-', byte(i >> 16)}
		err := s.Put(k, val)
		if err != nil {
			if !errors.Is(err, ErrHeapFull) {
				t.Fatalf("Put %d: erreur inattendue got=%v want=%v", i, err, ErrHeapFull)
			}
			heapFullHit = true
			break
		}
		insertedKeys = append(insertedKeys, k)
	}

	if !heapFullHit && len(insertedKeys) < 100 {
		t.Fatalf("trop peu d'insertions: %d heapUsed=%d", len(insertedKeys), s.heapUsed)
	}
	if len(insertedKeys) == 0 {
		t.Fatalf("aucune clé insérée avant saturation du tas")
	}

	for i, k := range insertedKeys {
		got, err := s.Get(k)
		if err != nil {
			t.Fatalf("Get clé %d: %v", i, err)
		}
		if !bytes.Equal(got, val) {
			t.Fatalf("Get clé %d: contenu altéré", i)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s = mustOpenShard(t, dir, key, shardID)
	for i, k := range insertedKeys {
		got, err := s.Get(k)
		if err != nil {
			t.Fatalf("Get réouvert clé %d: %v", i, err)
		}
		if !bytes.Equal(got, val) {
			t.Fatalf("Get réouvert clé %d: contenu altéré", i)
		}
	}

	if heapFullHit {
		newKey := []byte{0xFF, 0xEE, 'e', 'x', 't', 'r', 'a', 0x01}
		if err := s.Put(newKey, val); !errors.Is(err, ErrHeapFull) {
			t.Fatalf("Put post-saturation: got=%v want=%v", err, ErrHeapFull)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func TestEngineAutoCompactOnHeapFull(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 91)
	}
	s := mustOpenShard(t, dir, key, 11, WithHistoryRetention(RetentionPruneSuperseded))
	defer func() { _ = s.Close() }()

	const nKeys = 32
	keys := make([][]byte, nKeys)
	val := make([]byte, 1200)
	for i := range val {
		val[i] = byte(i)
	}
	for i := 0; i < nKeys; i++ {
		k := []byte{byte(i), 'a', 'u', 't', 'o', 'c', 'm', 'p'}
		keys[i] = k
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put initial %d: %v", i, err)
		}
	}

	var compacted bool
	peak := s.heapUsed
	for round := 0; round < 2000 && !compacted; round++ {
		for i := range val {
			val[i] = byte(round + i)
		}
		usedBefore := s.heapUsed
		for i := 0; i < nKeys; i++ {
			if err := s.Put(keys[i], val); err != nil {
				t.Fatalf("Put overwrite round=%d i=%d heapUsed=%d: %v", round, i, s.heapUsed, err)
			}
			if s.heapUsed < usedBefore {
				compacted = true
			}
			if s.heapUsed > peak {
				peak = s.heapUsed
			}
		}
	}
	if !compacted {
		t.Fatalf("auto-compaction absente après 2000 rounds heapUsed=%d peak=%d watermark=%d", s.heapUsed, peak, s.heapWatermark)
	}
	for i, k := range keys {
		got, err := s.Get(k)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("Get post-compact %d: err=%v", i, err)
		}
	}
}

func TestEngineScanAfterOverflow(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 47)
	}
	s := mustOpenShard(t, dir, key, 8)
	val := make([]byte, 1200)
	n := 0
	for i := 0; i < 256; i++ {
		k := []byte{byte(i), byte(i >> 8), 's', 'c', 'a', 'n', '-', 'x'}
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put %d used=%d: %v", i, s.heapUsed, err)
		}
		n++
		if s.heapUsed >= 5 {
			break
		}
	}
	if s.heapUsed < 5 {
		t.Fatalf("used=%d n=%d, besoin >= 5 (page base > 64Kio)", s.heapUsed, n)
	}
	got, err := s.ScanPrefix(nil)
	if err != nil {
		t.Fatalf("ScanPrefix: %v used=%d n=%d", err, s.heapUsed, n)
	}
	if len(got) != n {
		t.Fatalf("ScanPrefix len=%d want %d used=%d", len(got), n, s.heapUsed)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEnginePutBatch1000(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 31)
	}
	const shardID uint16 = 10
	const n = 1000

	s := mustOpenShard(t, dir, key, shardID)
	wal0 := s.walFlushes
	data0 := s.dataFlushes

	pairs := make([][2][]byte, n)
	for i := 0; i < n; i++ {
		k := []byte{
			byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24),
			'k', 'e', 'y', 8,
		}
		v := make([]byte, 16)
		for j := range v {
			v[j] = byte(i*3 + j)
		}
		pairs[i] = [2][]byte{k, v}
	}
	if err := s.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	for _, i := range []int{0, 500, 999} {
		got, err := s.Get(pairs[i][0])
		if err != nil || !bytes.Equal(got, pairs[i][1]) {
			t.Fatalf("Get %d: err=%v val=%q want %q", i, err, got, pairs[i][1])
		}
	}
	if d := s.walFlushes - wal0; d != 1 {
		t.Fatalf("walFlushes=%d want 1", d)
	}
	if d := s.dataFlushes - data0; d != 0 {
		t.Fatalf("dataFlushes=%d want 0 (barrière data différée au pointage)", d)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestShardScanPrefix(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 23)
	}
	const shardID uint16 = 8

	s := mustOpenShard(t, dir, key, shardID)
	if err := s.Put([]byte("aa"), []byte("1")); err != nil {
		t.Fatalf("Put aa: %v", err)
	}
	if err := s.Put([]byte("ab"), []byte("2")); err != nil {
		t.Fatalf("Put ab: %v", err)
	}
	if err := s.Put([]byte("ba"), []byte("3")); err != nil {
		t.Fatalf("Put ba: %v", err)
	}
	got, err := s.ScanPrefix([]byte("a"))
	if err != nil {
		t.Fatalf("ScanPrefix: %v", err)
	}
	if len(got) != 2 || string(got[0]) != "aa" || string(got[1]) != "ab" {
		t.Fatalf("ScanPrefix a: %q", got)
	}
	gotB, err := s.ScanPrefix([]byte("b"))
	if err != nil || len(gotB) != 1 || string(gotB[0]) != "ba" {
		t.Fatalf("ScanPrefix b: err=%v %q", err, gotB)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineViewHeapIsolation(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 41)
	}
	const shardID uint16 = 9

	s := mustOpenShard(t, dir, key, shardID)
	k := []byte("iso-key")
	v1 := []byte("v1-isolated-view")
	v2 := []byte("v2-mutated-shard")

	if err := s.Put(k, v1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}

	view, err := s.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	defer view.Close()

	if uint64(len(view.heap)) != shardBytes {
		t.Fatalf("view.heap length = %d, want %d", len(view.heap), shardBytes)
	}

	if err := s.Put(k, v2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	gotShard, err := s.Get(k)
	if err != nil || !bytes.Equal(gotShard, v2) {
		t.Fatalf("Shard.Get: err=%v val=%q want %q", err, gotShard, v2)
	}

	gotView, err := view.Get(k)
	if err != nil || !bytes.Equal(gotView, v1) {
		t.Fatalf("View.Get: err=%v val=%q want %q", err, gotView, v1)
	}

	kAfter := []byte("iso-key-after")
	vAfter := []byte("v-after-view")
	if err := s.Put(kAfter, vAfter); err != nil {
		t.Fatalf("Put after View: %v", err)
	}

	gotAfterShard, err := s.Get(kAfter)
	if err != nil || !bytes.Equal(gotAfterShard, vAfter) {
		t.Fatalf("Shard.Get kAfter: err=%v val=%q want %q", err, gotAfterShard, vAfter)
	}

	if _, err := view.Get(kAfter); err == nil {
		t.Fatalf("View.Get kAfter: expected not found error, got nil")
	}

	scanView, err := view.ScanPrefix([]byte("iso-key"))
	if err != nil {
		t.Fatalf("view.ScanPrefix: %v", err)
	}
	if len(scanView) != 1 || !bytes.Equal(scanView[0], k) {
		t.Fatalf("view.ScanPrefix: got %q want [%q]", scanView, k)
	}

	scanShard, err := s.ScanPrefix([]byte("iso-key"))
	if err != nil {
		t.Fatalf("s.ScanPrefix: %v", err)
	}
	if len(scanShard) != 2 {
		t.Fatalf("s.ScanPrefix: got %q want 2 keys", scanShard)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineDelete(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 53)
	}
	const shardID uint16 = 14

	s := mustOpenShard(t, dir, key, shardID)
	k := []byte("del-key")
	v := []byte("del-val-bit-exact")
	otherK := []byte("other-key")
	otherV := []byte("other-val")

	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put k: %v", err)
	}
	if err := s.Put(otherK, otherV); err != nil {
		t.Fatalf("Put other: %v", err)
	}

	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get avant Delete: err=%v val=%q", err, got)
	}

	if err := s.Delete(k); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := s.Get(k); err == nil {
		t.Fatalf("Get après Delete: attendu errNotFound, obtenu nil")
	}

	gotOther, err := s.Get(otherK)
	if err != nil || !bytes.Equal(gotOther, otherV) {
		t.Fatalf("Get other après Delete: err=%v val=%q", err, gotOther)
	}

	// Suppression d'une clé absente = no-op
	if err := s.Delete([]byte("absent-key")); err != nil {
		t.Fatalf("Delete absent: %v", err)
	}

	// Suppression d'une clé vide = rejet
	if err := s.Delete(nil); err == nil {
		t.Fatalf("Delete nil attendu rejet")
	}
	if err := s.Delete([]byte{}); err == nil {
		t.Fatalf("Delete vide attendu rejet")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s = mustOpenShard(t, dir, key, shardID)
	if _, err := s.Get(k); err == nil {
		t.Fatalf("Get après réouverture: attendu errNotFound, obtenu nil")
	}
	gotOther, err = s.Get(otherK)
	if err != nil || !bytes.Equal(gotOther, otherV) {
		t.Fatalf("Get other après réouverture: err=%v val=%q", err, gotOther)
	}

	// Réinsertion après suppression
	v2 := []byte("del-val-v2")
	if err := s.Put(k, v2); err != nil {
		t.Fatalf("Put après Delete: %v", err)
	}
	got, err = s.Get(k)
	if err != nil || !bytes.Equal(got, v2) {
		t.Fatalf("Get après réinsertion: err=%v val=%q", err, got)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func TestEngineDeleteRedo(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 59)
	}
	const shardID uint16 = 15

	s := mustOpenShard(t, dir, key, shardID)
	k := []byte("redo-del-k")
	v := []byte("redo-del-v")
	keepK := []byte("redo-keep-k")
	keepV := []byte("redo-keep-v")

	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put k: %v", err)
	}
	if err := s.Put(keepK, keepV); err != nil {
		t.Fatalf("Put keep: %v", err)
	}
	if err := s.Delete(k); err != nil {
		t.Fatalf("Delete k: %v", err)
	}
	if _, err := s.Get(k); err == nil {
		t.Fatalf("Get chaud k: attendu errNotFound")
	}
	gotKeep, err := s.Get(keepK)
	if err != nil || !bytes.Equal(gotKeep, keepV) {
		t.Fatalf("Get chaud keep: err=%v val=%q", err, gotKeep)
	}

	// Crash sans vidage pager / data.img
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	// Réouverture : le replay du WAL applique le RecDel
	s = mustOpenShard(t, dir, key, shardID)
	if _, err := s.Get(k); err == nil {
		t.Fatalf("redo WAL après crash : clé k toujours présente !")
	}
	gotKeep, err = s.Get(keepK)
	if err != nil || !bytes.Equal(gotKeep, keepV) {
		t.Fatalf("redo WAL après crash : clé keep corrompue : err=%v val=%q", err, gotKeep)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func TestEnginePutNDirty(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 42)
	}
	const shardID uint16 = 3

	s := mustOpenShard(t, dir, key, shardID)
	defer s.Close()

	ndInit := s.pager.NDirty()
	if ndInit != 0 {
		t.Fatalf("NDirty after open: %d want 0", ndInit)
	}

	k := []byte("dirty-check-key")
	v := []byte("dirty-check-value")
	if err := s.Put(k, v); err != nil {
		t.Fatalf("Put: %v", err)
	}

	nd := s.pager.NDirty()
	if nd == 0 {
		t.Fatalf("NDirty after one Put: 0 want > 0")
	}
	if nd >= int(heapPages) {
		t.Fatalf("NDirty after one Put: %d want << %d", nd, heapPages)
	}

	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("Get: err=%v val=%q want %q", err, got, v)
	}
}

func TestEngineWALNewerVersionNotSkipped(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 11)
	if err := s.Put([]byte("k"), []byte("v1")); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close1: %v", err)
	}
	s = mustOpenShard(t, dir, key, 11)
	if err := s.Put([]byte("k"), []byte("v2")); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("crash: %v", err)
	}
	s = mustOpenShard(t, dir, key, 11)
	got, err := s.Get([]byte("k"))
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("version récente perdue: err=%v val=%q", err, got)
	}
	_ = s.Close()
}

func TestEngineDeleteReopenCycle(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 12)
	k := []byte("del-k")
	if err := s.Put(k, []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.Delete(k); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := s.Get(k); !errors.Is(err, errNotFound) {
		t.Fatalf("get après del: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s = mustOpenShard(t, dir, key, 12)
	if _, err := s.Get(k); !errors.Is(err, errNotFound) {
		t.Fatalf("get réouvert: %v", err)
	}
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	got, err := s.Get(k)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("get après re-put: err=%v val=%q", err, got)
	}
	_ = s.Close()
}

func TestEngineElevateRootPut(t *testing.T) {
	dir := t.TempDir()
	var mac [32]byte
	s := mustOpenShard(t, dir, mac, 13)
	val := make([]byte, 100)
	var first uint64
	n := 0
	for i := 0; i < 128; i++ {
		k := make([]byte, 2000)
		k[0] = byte(i)
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put %d: %v used=%d", i, err, s.heapUsed)
		}
		n++
		if s.pub[s.heapRoot*pageN+20] == 2 && first == 0 {
			first = s.heapRoot
		}
		if first != 0 && s.heapRoot != first {
			break
		}
	}
	if first == 0 || s.heapRoot == first {
		t.Fatalf("pas d'élévation moteur root=%d first=%d n=%d used=%d", s.heapRoot, first, n, s.heapUsed)
	}
	for i := 0; i < n; i++ {
		k := make([]byte, 2000)
		k[0] = byte(i)
		got, err := s.Get(k)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("Get %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s = mustOpenShard(t, dir, mac, 13)
	for i := 0; i < n; i++ {
		k := make([]byte, 2000)
		k[0] = byte(i)
		got, err := s.Get(k)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("Get réouvert %d: %v", i, err)
		}
	}
	_ = s.Close()
}

func TestPutHeapSkipsCleanPages(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 7)
	val := bytes.Repeat([]byte{'x'}, 400)
	for i := 0; s.heapUsed < 20; i++ {
		k := []byte{byte(i >> 8), byte(i)}
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		if i > 4000 {
			t.Fatalf("tas used=%d n'atteint pas 20", s.heapUsed)
		}
	}
	if err := s.flushPages(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	n0 := s.data.PwriteN()
	if err := s.Put([]byte{99}, []byte("z")); err != nil {
		t.Fatalf("Put extra: %v", err)
	}
	if err := s.flushPages(); err != nil {
		t.Fatalf("flush2: %v", err)
	}
	delta := s.data.PwriteN() - n0
	if delta == 0 {
		t.Fatal("aucune écriture data")
	}
	if delta > 64 {
		t.Fatalf("pwrite data delta=%d want ≤64 LBA (pages sales seulement)", delta)
	}
	got, err := s.Get([]byte{99})
	if err != nil || string(got) != "z" {
		t.Fatalf("Get extra: err=%v val=%q", err, got)
	}
	_ = s.Close()
}

func TestShardRepackHeapRecycle(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 55)
	}
	s := mustOpenShard(t, dir, key, 3)
	defer s.Close()

	val := bytes.Repeat([]byte{'c'}, 300)
	// Insérer 200 clés (60 Ko) pour forcer plusieurs niveaux de split B-Tree
	for i := 0; i < 200; i++ {
		k := []byte(fmt.Sprintf("k-%04d", i))
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	usedBefore := s.heapUsed
	if usedBefore <= 1 {
		t.Fatalf("heapUsed before repack=%d want > 1", usedBefore)
	}

	// Supprimer 150 clés pour rendre des branches et anciennes versions orphelines
	for i := 0; i < 150; i++ {
		k := []byte(fmt.Sprintf("k-%04d", i))
		if err := s.Delete(k); err != nil {
			t.Fatalf("Delete %d: %v", i, err)
		}
	}

	usedAfterDelete := s.heapUsed

	// Lancement de RepackHeap pour recycler les pages orphelines
	if err := s.RepackHeap(); err != nil {
		t.Fatalf("RepackHeap: %v", err)
	}

	usedAfterRepack := s.heapUsed
	t.Logf("Tas CoW: before=%d, afterDelete=%d, afterRepack=%d", usedBefore, usedAfterDelete, usedAfterRepack)

	// Vérification de l'intégrité bit-exacte des clés survivantes (150 à 199)
	for i := 150; i < 200; i++ {
		k := []byte(fmt.Sprintf("k-%04d", i))
		got, err := s.Get(k)
		if err != nil {
			t.Fatalf("Get clé %d après repack: %v", i, err)
		}
		if !bytes.Equal(got, val) {
			t.Fatalf("Get clé %d après repack altéré", i)
		}
	}

	// Vérification que les clés supprimées (0 à 149) sont bien absentes
	for i := 0; i < 150; i++ {
		k := []byte(fmt.Sprintf("k-%04d", i))
		_, err := s.Get(k)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Clé supprimée %d encore présente après repack", i)
		}
	}

	// Réinsertion de nouvelles clés pour prouver la santé du tas recyclé
	for i := 200; i < 230; i++ {
		k := []byte(fmt.Sprintf("k-%04d", i))
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put new key %d après repack: %v", i, err)
		}
	}
}

func TestPayloadTooLargeRejection(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{1, 2, 3}
	s := mustOpenShard(t, dir, key, 0)
	defer s.Close()

	// 1. Clé dépassant 65535 octets : rejet obligatoire
	hugeKey := make([]byte, 65536)
	if err := s.Put(hugeKey, []byte("val")); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("Put huge key: got=%v want=%v", err, ErrPayloadTooLarge)
	}

	// 2. Valeur dépassant 65535 octets : supportée en pages d'overflow
	hugeVal := make([]byte, 70000)
	for i := range hugeVal {
		hugeVal[i] = byte(i*13 + 7)
	}
	if err := s.Put([]byte("key_overflow"), hugeVal); err != nil {
		t.Fatalf("Put huge val (overflow): unexpected error %v", err)
	}
	got, err := s.Get([]byte("key_overflow"))
	if err != nil {
		t.Fatalf("Get huge val: %v", err)
	}
	if !bytes.Equal(got, hugeVal) {
		t.Fatalf("Get huge val: content mismatch (len got=%d want=%d)", len(got), len(hugeVal))
	}

	// 3. PutBatch contenant un enregistrement en overflow
	pairs := [][2][]byte{
		{[]byte("k1"), []byte("v1")},
		{[]byte("k2"), hugeVal},
	}
	if err := s.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch with overflow: unexpected error %v", err)
	}
	got2, err := s.Get([]byte("k2"))
	if err != nil {
		t.Fatalf("Get k2 from batch: %v", err)
	}
	if !bytes.Equal(got2, hugeVal) {
		t.Fatalf("Get k2: content mismatch (len got=%d want=%d)", len(got2), len(hugeVal))
	}
}

func TestRepackHeapMultiLevelBTreeCursor(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{4, 5, 6}
	s := mustOpenShard(t, dir, key, 0)
	defer s.Close()

	val := make([]byte, 300)
	for i := range val {
		val[i] = byte(i)
	}

	total := 400
	for i := 0; i < total; i++ {
		k := []byte(fmt.Sprintf("item-%05d", i))
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	// Suppression de 3 éléments sur 4 pour créer des trous
	surviving := make(map[string]bool)
	for i := 0; i < total; i++ {
		k := []byte(fmt.Sprintf("item-%05d", i))
		if i%4 != 0 {
			if err := s.Delete(k); err != nil {
				t.Fatalf("Delete %d: %v", i, err)
			}
		} else {
			surviving[string(k)] = true
		}
	}

	if err := s.RepackHeap(); err != nil {
		t.Fatalf("RepackHeap multi-level: %v", err)
	}

	// Parcours complet par curseur zéro-allocation
	cur, err := s.Cursor()
	if err != nil {
		t.Fatalf("s.Cursor: %v", err)
	}
	defer cur.Close()

	var iteratedKeys []string
	for k, v, ok := cur.First(); ok; k, v, ok = cur.Next() {
		iteratedKeys = append(iteratedKeys, string(k))
		if !bytes.Equal(v, val) {
			t.Fatalf("Curseur valeur altérée pour %s", string(k))
		}
	}

	if len(iteratedKeys) != len(surviving) {
		t.Fatalf("Curseur count post-repack: got=%d want=%d", len(iteratedKeys), len(surviving))
	}
	for i, k := range iteratedKeys {
		expected := fmt.Sprintf("item-%05d", i*4)
		if k != expected {
			t.Fatalf("Curseur ordre post-repack [%d]: got=%s want=%s", i, k, expected)
		}
	}
}

func TestDynamicShardCapacity_256MB(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{7, 8, 9}

	// 1. Ouverture explicite avec 16384 pages (256 Mo)
	s, err := OpenShard(dir, key, 0, WithHeapPages(16384))
	if err != nil {
		t.Fatalf("OpenShard 256MB: %v", err)
	}

	if s.HeapPages() != 16384 {
		t.Fatalf("HeapPages: got %d want 16384", s.HeapPages())
	}
	wantBytes := uint64(16384 * 16384)
	if s.ShardBytes() != wantBytes {
		t.Fatalf("ShardBytes: got %d want %d", s.ShardBytes(), wantBytes)
	}

	// 2. Écriture de quelques enregistrements
	k1 := []byte("key-256mb-alpha")
	v1 := []byte("payload-256mb-alpha-content")
	if err := s.Put(k1, v1); err != nil {
		t.Fatalf("Put k1: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close 256MB shard: %v", err)
	}

	// 3. Réouverture sans option explicite : doit détecter automatiquement 16384 pages (256 Mo)
	s2, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard auto-detect 256MB: %v", err)
	}
	defer s2.Close()

	if s2.HeapPages() != 16384 {
		t.Fatalf("Reopened HeapPages: got %d want 16384", s2.HeapPages())
	}
	if s2.ShardBytes() != wantBytes {
		t.Fatalf("Reopened ShardBytes: got %d want %d", s2.ShardBytes(), wantBytes)
	}

	gotV1, err := s2.Get(k1)
	if err != nil {
		t.Fatalf("Get k1 post-reopen: %v", err)
	}
	if !bytes.Equal(gotV1, v1) {
		t.Fatalf("Value mismatch post-reopen: got %q want %q", gotV1, v1)
	}
}

func TestDynamicShardCapacity_InvalidPages(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{10, 11, 12}

	// < 4096 pages
	if _, err := OpenShard(dir, key, 0, WithHeapPages(2048)); !errors.Is(err, ErrInvalidHeapPages) {
		t.Fatalf("WithHeapPages(2048): got %v want %v", err, ErrInvalidHeapPages)
	}

	// > maxHeapPages (67 108 864 pages = 1 To)
	if _, err := OpenShard(dir, key, 0, WithHeapPages(70000000)); !errors.Is(err, ErrInvalidHeapPages) {
		t.Fatalf("WithHeapPages(70000000): got %v want %v", err, ErrInvalidHeapPages)
	}

	// non multiple de 64
	if _, err := OpenShard(dir, key, 0, WithHeapPages(4097)); !errors.Is(err, ErrInvalidHeapPages) {
		t.Fatalf("WithHeapPages(4097): got %v want %v", err, ErrInvalidHeapPages)
	}
}

func TestDynamicShardCapacity_MismatchDetection(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{13, 14, 15}

	// Création d'un shard par défaut 64 Mo (4096 pages)
	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Tentative conflictuelle de réouverture explicite à 256 Mo
	if _, err := OpenShard(dir, key, 0, WithHeapPages(16384)); !errors.Is(err, ErrShardCapacityMismatch) {
		t.Fatalf("OpenShard mismatch: got %v want %v", err, ErrShardCapacityMismatch)
	}

	// Cas inverse : création à 256 Mo puis tentative explicite à 4096
	dir2 := t.TempDir()
	s2, err := OpenShard(dir2, key, 0, WithHeapPages(16384))
	if err != nil {
		t.Fatalf("OpenShard dir2: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close dir2: %v", err)
	}

	// Réouverture explicite à WithHeapPages(4096) sur fichier à 16384 pages : doit retourner mismatch
	if _, err := OpenShard(dir2, key, 0, WithHeapPages(4096)); !errors.Is(err, ErrShardCapacityMismatch) {
		t.Fatalf("OpenShard dir2 explicit 4096 mismatch: got %v want %v", err, ErrShardCapacityMismatch)
	}

	// Réouverture sans option : doit réussir et adopter 16384 pages
	s2Adopt, err := OpenShard(dir2, key, 0)
	if err != nil {
		t.Fatalf("OpenShard dir2 adoption: %v", err)
	}
	if s2Adopt.heapPages != 16384 {
		t.Fatalf("heapPages adopté = %d want 16384", s2Adopt.heapPages)
	}
	_ = s2Adopt.Close()
}

func TestAsOf_SurvivesLeafCompactionFallback(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{7, 8, 9}

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// 1. Écriture de la version v1 de la clé kTarget et capture d'une vue as-of
	kTarget := []byte("target-key")
	v1 := []byte("value-version-1-historical")
	if err := s.Put(kTarget, v1); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	viewV1, err := s.View()
	if err != nil {
		t.Fatalf("View at v1: %v", err)
	}
	defer viewV1.Close()

	// 2. Écriture de multiples versions et clés voisines pour remplir et fragmenter la feuille
	for i := 0; i < 40; i++ {
		k := []byte(fmt.Sprintf("neighbour-%04d", i))
		v := bytes.Repeat([]byte{byte(i + 1)}, 300)
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put neighbour %d: %v", i, err)
		}
	}

	// Mettre à jour kTarget pour créer une version v2
	v2 := []byte("value-version-2-updated")
	if err := s.Put(kTarget, v2); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	// 3. Déclencher des suppressions pour introduire des tombstones
	for i := 0; i < 20; i++ {
		k := []byte(fmt.Sprintf("neighbour-%04d", i*2))
		if err := s.Delete(k); err != nil {
			t.Fatalf("Delete %d: %v", i, err)
		}
	}

	// 4. Forcer un compactage de feuille lors d'une nouvelle insertion
	vBig := bytes.Repeat([]byte("fallback-trigger"), 50)
	if err := s.Put([]byte("neighbour-0001"), vBig); err != nil {
		t.Fatalf("Put after tombstones: %v", err)
	}

	// 5. Prouver que la vue As-Of capturée à v1 retourne fidèlement v1 (non effacée par le compactage)
	gotV1, err := viewV1.Get(kTarget)
	if err != nil {
		t.Fatalf("Get kTarget from viewV1: %v", err)
	}
	if !bytes.Equal(gotV1, v1) {
		t.Fatalf("Version historique v1 altérée par le compactage ! got %q want %q", gotV1, v1)
	}

	// 6. Vérifier que la lecture courante retourne bien v2
	gotCurrent, err := s.Get(kTarget)
	if err != nil {
		t.Fatalf("Get current: %v", err)
	}
	if !bytes.Equal(gotCurrent, v2) {
		t.Fatalf("Version courante v2 altérée ! got %q want %q", gotCurrent, v2)
	}
}

func TestDB_PutBatch_ScatterGatherMultiShards(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 61)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	const n = 500
	pairs := make([][2][]byte, n)
	seen := make(map[uint16]struct{})
	for i := 0; i < n; i++ {
		k := []byte{
			byte(i >> 8), byte(i), 's', 'g',
			byte(i >> 16), byte(i >> 24),
		}
		v := []byte(fmt.Sprintf("sg-val-%04d", i))
		pairs[i] = [2][]byte{k, v}
		seen[Route(k)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("500 clés n'ont touché que %d shard(s)", len(seen))
	}

	if err := db.PutBatch(pairs); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("PutBatch: %v", err)
	}
	if nShards := len(db.shards); nShards < 2 {
		t.Fatalf("shards ouverts=%d, scatter-gather exige >= 2", nShards)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := db.Get(pairs[i][0])
			if err != nil {
				errCh <- fmt.Errorf("Get %d: %w", i, err)
				return
			}
			if !bytes.Equal(got, pairs[i][1]) {
				errCh <- fmt.Errorf("Get %d: val=%q want %q", i, got, pairs[i][1])
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestDB_PutBatch_ConcurrentParallel(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 67)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	db.SetBusyTimeout(5 * time.Second)

	const goroutines = 8
	const perG = 32
	overlap := make([][2][]byte, 8)
	for i := range overlap {
		overlap[i] = [2][]byte{
			[]byte(fmt.Sprintf("overlap-%02d", i)),
			[]byte(fmt.Sprintf("oval-%02d", i)),
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			pairs := make([][2][]byte, perG+len(overlap))
			for i := 0; i < perG; i++ {
				pairs[i] = [2][]byte{
					[]byte(fmt.Sprintf("g%02d-k%03d", gid, i)),
					[]byte(fmt.Sprintf("g%02d-v%03d", gid, i)),
				}
			}
			copy(pairs[perG:], overlap)
			if err := db.PutBatch(pairs); err != nil {
				if errors.Is(err, unix.EINVAL) {
					errCh <- err
					return
				}
				errCh <- fmt.Errorf("gid %d: %w", gid, err)
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("PutBatch concurrent: %v", err)
	}

	for g := 0; g < goroutines; g++ {
		for i := 0; i < perG; i++ {
			k := []byte(fmt.Sprintf("g%02d-k%03d", g, i))
			want := []byte(fmt.Sprintf("g%02d-v%03d", g, i))
			got, err := db.Get(k)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("Get disjoint %s: err=%v val=%q want %q", k, err, got, want)
			}
		}
	}
	for i := range overlap {
		got, err := db.Get(overlap[i][0])
		if err != nil || !bytes.Equal(got, overlap[i][1]) {
			t.Fatalf("Get overlap %s: err=%v val=%q want %q", overlap[i][0], err, got, overlap[i][1])
		}
	}
}

func TestDB_PutBatch_ReplicationHookCalled(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 73)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	type hookCall struct {
		shard   uint16
		recType byte
		key     []byte
		val     []byte
	}
	var (
		mu    sync.Mutex
		calls []hookCall
	)
	db.SetReplicationHook(func(shard uint16, recType byte, k, v []byte) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, hookCall{
			shard:   shard,
			recType: recType,
			key:     append([]byte(nil), k...),
			val:     append([]byte(nil), v...),
		})
	})

	assertHooked := func(t *testing.T, pairs [][2][]byte) {
		t.Helper()
		mu.Lock()
		got := append([]hookCall(nil), calls...)
		mu.Unlock()
		if len(got) != len(pairs) {
			t.Fatalf("crochet: %d appels, attendu %d", len(got), len(pairs))
		}
		seen := make(map[string]int, len(pairs))
		for _, c := range got {
			if c.recType != byte(RecPut) {
				t.Fatalf("recType=%d, attendu RecPut=%d", c.recType, RecPut)
			}
			seen[string(c.key)]++
		}
		for i := range pairs {
			wantKey := string(pairs[i][0])
			if seen[wantKey] != 1 {
				t.Fatalf("clé %q : %d notifications, attendu 1", pairs[i][0], seen[wantKey])
			}
			found := false
			for _, c := range got {
				if bytes.Equal(c.key, pairs[i][0]) {
					if !bytes.Equal(c.val, pairs[i][1]) {
						t.Fatalf("clé %q : val crochet=%q want %q", pairs[i][0], c.val, pairs[i][1])
					}
					if c.shard != Route(pairs[i][0]) {
						t.Fatalf("clé %q : shard crochet=%d want %d", pairs[i][0], c.shard, Route(pairs[i][0]))
					}
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("clé %q absente du crochet", pairs[i][0])
			}
		}
	}

	monoShard := uint16(0)
	monoPairs := make([][2][]byte, 0, 4)
	for i := 0; i < 1<<16 && len(monoPairs) < 4; i++ {
		k := []byte{byte(i >> 8), byte(i), 'm', 'o', 'n', 'o'}
		id := Route(k)
		if len(monoPairs) == 0 {
			monoShard = id
			monoPairs = append(monoPairs, [2][]byte{k, []byte(fmt.Sprintf("mv-%d", i))})
			continue
		}
		if id == monoShard {
			monoPairs = append(monoPairs, [2][]byte{append([]byte(nil), k...), []byte(fmt.Sprintf("mv-%d", i))})
		}
	}
	if len(monoPairs) < 4 {
		t.Fatal("impossible de rassembler 4 clés sur un même fragment")
	}

	mu.Lock()
	calls = nil
	mu.Unlock()
	if err := db.PutBatch(monoPairs); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("PutBatch mono-shard: %v", err)
	}
	assertHooked(t, monoPairs)

	multiPairs := make([][2][]byte, 0, 8)
	seenShards := make(map[uint16]struct{})
	for i := 0; i < 1<<16 && len(multiPairs) < 8; i++ {
		k := []byte{byte(i >> 8), byte(i), 'm', 'u', 'l', 't'}
		id := Route(k)
		if _, ok := seenShards[id]; ok {
			continue
		}
		seenShards[id] = struct{}{}
		multiPairs = append(multiPairs, [2][]byte{append([]byte(nil), k...), []byte(fmt.Sprintf("xv-%d", i))})
	}
	if len(seenShards) < 2 {
		t.Fatal("impossible de rassembler des clés sur au moins deux fragments")
	}

	mu.Lock()
	calls = nil
	mu.Unlock()
	if err := db.PutBatch(multiPairs); err != nil {
		t.Fatalf("PutBatch multi-shards: %v", err)
	}
	assertHooked(t, multiPairs)
}

func TestRoute_MalformedVFSKeysNoPanic(t *testing.T) {
	keys := [][]byte{
		[]byte("vfs:9223372036854775807:"),
		[]byte("vfs:9223372036854775808:"),
		[]byte("vfs:"),
		[]byte("vfs:1"),
		[]byte("vfs:1:"),
		[]byte("vfs:abc:foo"),
		[]byte("vfs:5:ab:1:x"),
		[]byte("vfs:1:t:9223372036854775807:"),
		[]byte("vfs:1:t:1"),
		[]byte("vfs:0::0:"),
		[]byte("vfs"),
		nil,
		[]byte("vfs:999999999999999999999:x"),
		[]byte("vfs:1:t:5:x"),
	}
	for i, k := range keys {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("clé %d %q: panic %v", i, k, rec)
				}
			}()
			_ = Route(k)
			if bytes.HasPrefix(k, []byte("vfs:")) {
				_ = vfsRoutingPrefix(k)
			}
		}()
	}
}

func TestEngine_AutoCompactPreservesVersionIDs(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 79)
	}
	s := mustOpenShard(t, dir, key, 11)

	k1 := []byte("ver-key")
	if err := s.Put(k1, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k1, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	id2 := s.lastID
	k2 := []byte("other")
	if err := s.Put(k2, []byte("x")); err != nil {
		t.Fatalf("Put other: %v", err)
	}
	id3 := s.lastID

	got, err := s.GetAsOf(k1, id2)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("GetAsOf k1@id2 avant rebuild: err=%v val=%q", err, got)
	}
	got, err = s.GetAsOf(k2, id3)
	if err != nil || !bytes.Equal(got, []byte("x")) {
		t.Fatalf("GetAsOf k2@id3 avant rebuild: err=%v val=%q", err, got)
	}

	if err := s.lockWriter(); err != nil {
		t.Fatalf("lockWriter: %v", err)
	}
	if err := s.rebuildLatestLocked(); err != nil {
		s.unlockWriter()
		t.Fatalf("rebuildLatestLocked: %v", err)
	}
	s.unlockWriter()

	if s.lastID != id3 {
		t.Fatalf("lastID altéré par rebuild: got %x want %x", s.lastID, id3)
	}
	if bytes.Equal(id1[:], id2[:]) || bytes.Equal(id2[:], id3[:]) {
		t.Fatal("identifiants de version non distincts avant rebuild")
	}

	got, err = s.GetAsOf(k1, id2)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("GetAsOf k1@id2 après rebuild: err=%v val=%q (id de version perdu)", err, got)
	}
	got, err = s.GetAsOf(k2, id3)
	if err != nil || !bytes.Equal(got, []byte("x")) {
		t.Fatalf("GetAsOf k2@id3 après rebuild: err=%v val=%q (id de version perdu)", err, got)
	}
	got, err = s.Get(k1)
	if err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("Get k1 après rebuild: err=%v val=%q", err, got)
	}
	got, err = s.Get(k2)
	if err != nil || !bytes.Equal(got, []byte("x")) {
		t.Fatalf("Get k2 après rebuild: err=%v val=%q", err, got)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
