// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/hazyhaar/c2db/pkg/c2db"
	"go.etcd.io/bbolt"
	"golang.org/x/sys/unix"
)

const (
	nKeys            = 1000
	keySize          = 16
	c2dbHeapPages    = 16
	c2dbGroupN       = 1000
	c2dbRecycleEvery = 12
	historyPath      = "/devhoros/c2simd/c2pkg/c2db/bench_history.json"
)

var boltBucket = []byte("kv")

type engineMetrics struct {
	PutNsOp         float64 `json:"put_ns_op,omitempty"`
	GetNsOp         float64 `json:"get_ns_op,omitempty"`
	PutAllocsOp     *int64  `json:"put_allocs_op,omitempty"`
	GetAllocsOp     *int64  `json:"get_allocs_op,omitempty"`
	GroupCommitNsOp float64 `json:"group_commit_ns_op,omitempty"`
	ServicePath     string  `json:"service_path,omitempty"`
}

type history struct {
	When      string                    `json:"when"`
	Go        string                    `json:"go"`
	N         int                       `json:"n"`
	HeapPages any                       `json:"heap_pages"`
	C2dbSkip  string                    `json:"c2db_skip,omitempty"`
	Engines   map[string]*engineMetrics `json:"engines"`
}

var (
	histMu     sync.Mutex
	histEng    = map[string]*engineMetrics{}
	c2dbSkip   atomic.Value
	keys, vals [][]byte
)

func TestMain(m *testing.M) {
	var err error
	var prov string
	keys, vals, prov, err = loadRealBenchmarkKVInternal(nKeys)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: impossible de charger le corpus réel pour les bancs: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[benchcmp] Corpus réel initialisé: %s\n", prov)
	c2dbSkip.Store("")
	code := m.Run()
	writeHistory()
	os.Exit(code)
}

func rec(engine, field string, nsOp float64) {
	histMu.Lock()
	defer histMu.Unlock()
	m := histEng[engine]
	if m == nil {
		m = &engineMetrics{}
		histEng[engine] = m
	}
	switch field {
	case "put":
		m.PutNsOp = nsOp
	case "get":
		m.GetNsOp = nsOp
	case "group":
		m.GroupCommitNsOp = nsOp
	}
	if engine == "c2db" {
		m.ServicePath = "heap_16"
	}
}

func capture(b *testing.B) float64 {
	b.Helper()
	b.StopTimer()
	if b.N == 0 {
		return 0
	}
	return float64(b.Elapsed().Nanoseconds()) / float64(b.N)
}

func writeHistory() {
	histMu.Lock()
	n := len(histEng)
	histMu.Unlock()
	if n == 0 {
		return
	}
	h := history{
		When:      time.Now().Format(time.RFC3339),
		Go:        "1.27",
		N:         nKeys,
		HeapPages: c2dbHeapPages,
		Engines:   map[string]*engineMetrics{},
	}
	if s, _ := c2dbSkip.Load().(string); s != "" {
		h.C2dbSkip = s
	}
	histMu.Lock()
	for k, v := range histEng {
		cp := *v
		h.Engines[k] = &cp
	}
	histMu.Unlock()
	if h.C2dbSkip != "" {
		delete(h.Engines, "c2db")
	}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return
	}
	raw = append(raw, '\n')
	_ = os.WriteFile(historyPath, raw, 0o644)
}

func openC2(tb testing.TB, dir string, key [32]byte) *c2db.Shard {
	tb.Helper()
	s, err := c2db.OpenShard(dir, key, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			c2dbSkip.Store("O_DIRECT")
			tb.Skip("O_DIRECT")
		}
		tb.Fatalf("OpenShard: %v", err)
	}
	return s
}

type c2hold struct {
	s    *c2db.Shard
	key  [32]byte
	puts int
	dir  string
}

func (h *c2hold) reopen(b *testing.B) {
	b.Helper()
	b.StopTimer()
	if h.s != nil {
		_ = h.s.Close()
		h.s = nil
	}
	if h.dir != "" {
		_ = os.RemoveAll(h.dir)
	}
	dir, err := os.MkdirTemp("", "c2hold-*")
	if err != nil {
		b.Fatalf("MkdirTemp: %v", err)
	}
	h.dir = dir
	h.s = openC2(b, h.dir, h.key)
	h.puts = 0
	b.StartTimer()
}

func (h *c2hold) close() {
	if h.s != nil {
		_ = h.s.Close()
		h.s = nil
	}
	if h.dir != "" {
		_ = os.RemoveAll(h.dir)
		h.dir = ""
	}
}

func (h *c2hold) put(b *testing.B, k, v []byte) {
	b.Helper()
	if h.puts >= c2dbRecycleEvery {
		h.reopen(b)
	}
	if err := h.s.Put(k, v); err != nil {
		h.reopen(b)
		if err2 := h.s.Put(k, v); err2 != nil {
			b.Fatalf("Put: %v (prev %v)", err2, err)
		}
	}
	h.puts++
}

func BenchmarkHotWrite_c2db(b *testing.B) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 3)
	}
	h := &c2hold{key: key}
	h.reopen(b)
	defer h.close()
	if err := h.s.Put(keys[0], vals[0]); err != nil {
		b.Fatalf("seed: %v", err)
	}
	h.puts = 1
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.put(b, keys[0], vals[0])
	}
	rec("c2db", "put", capture(b))
}

func BenchmarkSnapRead_c2db(b *testing.B) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 5)
	}
	s := openC2(b, b.TempDir(), key)
	defer func() { _ = s.Close() }()
	if err := s.Put(keys[0], vals[0]); err != nil {
		b.Fatalf("seed: %v", err)
	}
	snap, err := s.Freeze()
	if err != nil {
		b.Fatalf("Freeze: %v", err)
	}
	got, err := s.GetAsOf(keys[0], snap)
	if err != nil || !bytes.Equal(got, vals[0]) {
		b.Fatalf("GetAsOf seed: err=%v got=%v want=%v", err, got, vals[0])
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err = s.GetAsOf(keys[0], snap)
		if err != nil {
			b.Fatalf("GetAsOf: %v", err)
		}
		_ = got
	}
	rec("c2db", "get", capture(b))
}

func BenchmarkGroupCommit_c2db(b *testing.B) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 7)
	}
	pairs := make([][2][]byte, c2dbGroupN)
	for i := 0; i < c2dbGroupN; i++ {
		pairs[i] = [2][]byte{keys[i], vals[i]}
	}
	dir, err := os.MkdirTemp("", "c2group-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		_ = os.RemoveAll(dir)
		_ = os.MkdirAll(dir, 0o755)
		s := openC2(b, dir, key)
		b.StartTimer()
		if err := s.PutBatch(pairs); err != nil {
			_ = s.Close()
			b.Fatalf("PutBatch: %v", err)
		}
		b.StopTimer()
		_ = s.Close()
		b.StartTimer()
	}
	rec("c2db", "group", capture(b))
}

func openBolt(tb testing.TB) *bbolt.DB {
	tb.Helper()
	db, err := bbolt.Open(filepath.Join(tb.TempDir(), "bolt.db"), 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		tb.Fatalf("bbolt.Open: %v", err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(boltBucket)
		return err
	}); err != nil {
		_ = db.Close()
		tb.Fatalf("CreateBucket: %v", err)
	}
	return db
}

func boltPut(db *bbolt.DB, k, v []byte) error {
	return db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(boltBucket).Put(k, v)
	})
}

func boltFill(tb testing.TB, db *bbolt.DB) {
	tb.Helper()
	if err := db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket(boltBucket)
		for i := 0; i < nKeys; i++ {
			if err := bkt.Put(keys[i], vals[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		tb.Fatalf("bolt fill: %v", err)
	}
}

func BenchmarkHotWrite_bbolt(b *testing.B) {
	db := openBolt(b)
	defer func() { _ = db.Close() }()
	boltFill(b, db)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := i % nKeys
		if err := boltPut(db, keys[j], vals[j]); err != nil {
			b.Fatalf("Put: %v", err)
		}
	}
	rec("bbolt", "put", capture(b))
}

func BenchmarkSnapRead_bbolt(b *testing.B) {
	db := openBolt(b)
	defer func() { _ = db.Close() }()
	boltFill(b, db)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := i % nKeys
		err := db.View(func(tx *bbolt.Tx) error {
			got := tx.Bucket(boltBucket).Get(keys[j])
			if got == nil {
				return errors.New("missing")
			}
			return nil
		})
		if err != nil {
			b.Fatalf("Get: %v", err)
		}
	}
	rec("bbolt", "get", capture(b))
}

func BenchmarkGroupCommit_bbolt(b *testing.B) {
	db := openBolt(b)
	defer func() { _ = db.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := db.Update(func(tx *bbolt.Tx) error {
			bkt := tx.Bucket(boltBucket)
			for j := 0; j < nKeys; j++ {
				if err := bkt.Put(keys[j], vals[j]); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatalf("group commit: %v", err)
		}
	}
	rec("bbolt", "group", capture(b))
}

func openPebble(tb testing.TB) *pebble.DB {
	tb.Helper()
	db, err := pebble.Open(tb.TempDir(), &pebble.Options{})
	if err != nil {
		tb.Fatalf("pebble.Open: %v", err)
	}
	return db
}

func pebbleFill(tb testing.TB, db *pebble.DB) {
	tb.Helper()
	for i := 0; i < nKeys; i++ {
		if err := db.Set(keys[i], vals[i], pebble.Sync); err != nil {
			tb.Fatalf("pebble fill: %v", err)
		}
	}
}

func BenchmarkHotWrite_pebble(b *testing.B) {
	db := openPebble(b)
	defer func() { _ = db.Close() }()
	pebbleFill(b, db)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := i % nKeys
		if err := db.Set(keys[j], vals[j], pebble.Sync); err != nil {
			b.Fatalf("Set: %v", err)
		}
	}
	rec("pebble", "put", capture(b))
}

func BenchmarkSnapRead_pebble(b *testing.B) {
	db := openPebble(b)
	defer func() { _ = db.Close() }()
	pebbleFill(b, db)
	snap := db.NewSnapshot()
	defer snap.Close()
	got, closer, err := snap.Get(keys[0])
	if err != nil || !bytes.Equal(got, vals[0]) {
		if closer != nil {
			_ = closer.Close()
		}
		b.Fatalf("snap Get seed: err=%v got=%v want=%v", err, got, vals[0])
	}
	_ = closer.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := i % nKeys
		got, closer, err = snap.Get(keys[j])
		if err != nil {
			b.Fatalf("Get: %v", err)
		}
		_ = got
		_ = closer.Close()
	}
	rec("pebble", "get", capture(b))
}

func BenchmarkGroupCommit_pebble(b *testing.B) {
	db := openPebble(b)
	defer func() { _ = db.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < nKeys; j++ {
			if err := db.Set(keys[j], vals[j], pebble.NoSync); err != nil {
				b.Fatalf("Set: %v", err)
			}
		}
		if err := db.LogData(nil, pebble.Sync); err != nil {
			b.Fatalf("Sync: %v", err)
		}
	}
	rec("pebble", "group", capture(b))
}
