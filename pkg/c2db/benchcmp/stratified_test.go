// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const probeN = 200

func TestAdvancedUnixProbesShardPut(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 3)
	}
	s, err := c2db.OpenShard(dir, key, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()
	before := snapProbe()
	for i := 0; i < probeN; i++ {
		k := keys[i%len(keys)]
		v := vals[i%len(vals)]
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	d := snapProbe().Sub(before)
	if d.WriteBytes == 0 && d.SyscWrite == 0 {
		t.Fatalf("aucune écriture noyau constatée après %d Put: %+v", probeN, d)
	}
	t.Logf("strate_shard_put n=%d user_ns=%d sys_ns=%d sysc_w=%d write_bytes=%d nivcsw=%d maxrss_kb=%d",
		probeN, d.UserNs, d.SysNs, d.SyscWrite, d.WriteBytes, d.Nivcsw, d.MaxRSSKB)
	raw, _ := json.Marshal(d)
	t.Logf("probe_json %s", raw)
}

func TestAdvancedSQLiteParityReopen(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 9)
	}
	c2dir := filepath.Join(dir, "c2")
	if err := os.MkdirAll(c2dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := c2db.OpenShard(c2dir, key, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	db, err := openSQLite(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	const n = 64
	for i := 0; i < n; i++ {
		if err := s.Put(keys[i], vals[i]); err != nil {
			t.Fatalf("c2db Put: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO kv(k,v) VALUES(?,?)`, keys[i], vals[i]); err != nil {
			t.Fatalf("sqlite insert: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("c2db Close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("sqlite Close: %v", err)
	}
	s, err = c2db.OpenShard(c2dir, key, 1)
	if err != nil {
		t.Fatalf("reopen c2db: %v", err)
	}
	defer s.Close()
	db, err = openSQLite(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer db.Close()
	for i := 0; i < n; i++ {
		got, err := s.Get(keys[i])
		if err != nil {
			t.Fatalf("c2db Get %d: %v", i, err)
		}
		if string(got) != string(vals[i]) {
			t.Fatalf("c2db Get %d mismatch", i)
		}
		var v []byte
		if err := db.QueryRow(`SELECT v FROM kv WHERE k=?`, keys[i]).Scan(&v); err != nil {
			t.Fatalf("sqlite Get %d: %v", i, err)
		}
		if string(v) != string(vals[i]) {
			t.Fatalf("sqlite Get %d mismatch", i)
		}
	}
}

func BenchmarkL0_DevicePwriteSync(b *testing.B) {
	path := filepath.Join(b.TempDir(), "dev.img")
	dev, err := c2db.Create(path, 64*c2db.LBASize)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			b.Skip("O_DIRECT")
		}
		b.Fatal(err)
	}
	defer dev.Close()
	aligned, err := unix.Mmap(-1, 0, c2db.LBASize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Munmap(aligned)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		aligned[0] = byte(i)
		if err := dev.Write(uint64(i%16), aligned); err != nil {
			b.Fatal(err)
		}
		if err := dev.Flush(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkL1_WALAppendNoFlush(b *testing.B) {
	path := filepath.Join(b.TempDir(), "wal.img")
	var key [32]byte
	w, err := c2db.CreateWAL(path, 256<<20, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			b.Skip("O_DIRECT")
		}
		b.Fatal(err)
	}
	defer w.Close()
	id := [16]byte{2}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id[15] = byte(i)
		id[14] = byte(i >> 8)
		rec := c2db.Record{ID: id, Type: c2db.RecPut, Payload: vals[i%len(vals)]}
		if err := w.Append(rec); err != nil {
			b.StopTimer()
			_ = w.Close()
			_ = os.Remove(path)
			w, err = c2db.CreateWAL(path, 256<<20, key)
			if err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			if err := w.Append(rec); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkL1_WALAppendFlush(b *testing.B) {
	path := filepath.Join(b.TempDir(), "wal.img")
	var key [32]byte
	w, err := c2db.CreateWAL(path, 64<<20, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			b.Skip("O_DIRECT")
		}
		b.Fatal(err)
	}
	defer w.Close()
	id := [16]byte{1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id[15] = byte(i)
		rec := c2db.Record{ID: id, Type: c2db.RecPut, Payload: vals[i%len(vals)]}
		if err := w.Append(rec); err != nil {
			b.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkL3_ShardPut(b *testing.B) {
	var key [32]byte
	h := &c2hold{key: key}
	h.reopen(b)
	defer h.close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.put(b, keys[i%nKeys], vals[i%nKeys])
	}
	rec("c2db_l3", "put", capture(b))
}

func BenchmarkL4_SQLiteInsertSync(b *testing.B) {
	db, err := openSQLite(filepath.Join(b.TempDir(), "b.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, keys[i%nKeys], vals[i%nKeys]); err != nil {
			b.Fatal(err)
		}
	}
	rec("sqlite", "put", capture(b))
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=FULL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (k BLOB PRIMARY KEY, v BLOB)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
