// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"golang.org/x/sys/unix"
)

const (
	tortureN     = 512
	tortureBatch = 32
	tortureJSON  = "/devhoros/c2simd/c2pkg/c2db/bench_stratified.json"
)

type stratumRow struct {
	Engine      string      `json:"engine"`
	Stratum     string      `json:"stratum"`
	Op          string      `json:"op"`
	N           int         `json:"n"`
	NsPerOp     float64     `json:"ns_per_op"`
	AllocsPerOp int64       `json:"allocs_per_op"`
	Kernel      KernelProbe `json:"kernel"`
	L1          l1Counters  `json:"l1"`
	Note        string      `json:"note,omitempty"`
}

func measure(n int, fn func()) (ns float64, k KernelProbe, l1 l1Counters) {
	p := openPerf()
	defer p.close()
	before := snapProbe()
	p.start()
	t0 := time.Now()
	fn()
	elapsed := time.Since(t0)
	l1 = p.snap()
	k = snapProbe().Sub(before)
	if n > 0 {
		ns = float64(elapsed.Nanoseconds()) / float64(n)
	}
	return ns, k, l1
}

func TestTortureStratifiedC2dbSQLite(t *testing.T) {
	dir := t.TempDir()
	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 7)
	}
	c2dir := filepath.Join(dir, "c2")
	if err := os.MkdirAll(c2dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := c2db.OpenShard(c2dir, mac, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()
	sq, err := openSQLite(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer sq.Close()

	n := tortureN
	if n > len(keys) {
		n = len(keys)
	}
	var rows []stratumRow

	ns, k, l1 := measure(n, func() {
		for i := 0; i < n; i++ {
			if err := s.Put(keys[i], vals[i]); err != nil {
				t.Errorf("c2db Put: %v", err)
				return
			}
		}
	})
	rows = append(rows, stratumRow{Engine: "c2db", Stratum: "L3", Op: "put", N: n, NsPerOp: ns, Kernel: k, L1: l1, Note: "put_isole_fsync_journal"})

	ns, k, l1 = measure(n, func() {
		for i := 0; i < n; i++ {
			if _, err := sq.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, keys[i], vals[i]); err != nil {
				t.Errorf("sqlite insert: %v", err)
				return
			}
		}
	})
	rows = append(rows, stratumRow{Engine: "sqlite", Stratum: "L4", Op: "put", N: n, NsPerOp: ns, Kernel: k, L1: l1, Note: "wal_synchronous_full"})

	ns, k, l1 = measure(n, func() {
		for i := 0; i < n; i++ {
			if _, err := s.Get(keys[i]); err != nil {
				t.Errorf("c2db Get: %v", err)
				return
			}
		}
	})
	rows = append(rows, stratumRow{Engine: "c2db", Stratum: "L3", Op: "get", N: n, NsPerOp: ns, Kernel: k, L1: l1, Note: "get_pin_live"})

	ns, k, l1 = measure(n, func() {
		for i := 0; i < n; i++ {
			var v []byte
			if err := sq.QueryRow(`SELECT v FROM kv WHERE k=?`, keys[i]).Scan(&v); err != nil {
				t.Errorf("sqlite get: %v", err)
				return
			}
		}
	})
	rows = append(rows, stratumRow{Engine: "sqlite", Stratum: "L4", Op: "get", N: n, NsPerOp: ns, Kernel: k, L1: l1})

	pairs := make([][2][]byte, tortureBatch)
	for i := 0; i < tortureBatch; i++ {
		pairs[i] = [2][]byte{keys[n+i], vals[n+i]}
	}
	ns, k, l1 = measure(tortureBatch, func() {
		if err := s.PutBatch(pairs); err != nil {
			t.Errorf("PutBatch: %v", err)
		}
	})
	rows = append(rows, stratumRow{Engine: "c2db", Stratum: "L3", Op: "put_batch", N: tortureBatch, NsPerOp: ns, Kernel: k, L1: l1, Note: "un_fdatasync_lot"})

	ns, k, l1 = measure(tortureBatch, func() {
		tx, err := sq.Begin()
		if err != nil {
			t.Errorf("begin: %v", err)
			return
		}
		for i := 0; i < tortureBatch; i++ {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, keys[i], vals[i]); err != nil {
				t.Errorf("tx insert: %v", err)
				_ = tx.Rollback()
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Errorf("commit: %v", err)
		}
	})
	rows = append(rows, stratumRow{Engine: "sqlite", Stratum: "L4", Op: "put_batch", N: tortureBatch, NsPerOp: ns, Kernel: k, L1: l1, Note: "transaction"})

	const readers = 4
	const writes = 128
	ns, k, l1 = measure(writes, func() {
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for r := 0; r < readers; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_, _ = s.Get(keys[0])
					}
				}
			}()
		}
		base := n + tortureBatch
		for i := 0; i < writes; i++ {
			idx := base + i
			if idx >= len(keys) {
				idx = i % n
			}
			if err := s.Put(keys[idx], vals[idx]); err != nil {
				t.Errorf("put sous lecture: %v", err)
				break
			}
		}
		close(stop)
		wg.Wait()
	})
	rows = append(rows, stratumRow{Engine: "c2db", Stratum: "L5", Op: "put_sous_get", N: writes, NsPerOp: ns, Kernel: k, L1: l1, Note: "1_writer_4_readers"})

	if rows[0].Kernel.WriteBytes == 0 && rows[0].Kernel.SyscWrite == 0 {
		t.Fatalf("c2db Put sans I/O noyau: %+v", rows[0].Kernel)
	}

	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tortureJSON, raw, 0o644); err != nil {
		t.Logf("ecriture %s: %v", tortureJSON, err)
	}
	for _, r := range rows {
		t.Logf("%s %s %s n=%d ns/op=%.0f ipc=%.2f sysc_w=%d write_b=%d l1d_miss=%d br_miss=%d %s",
			r.Engine, r.Stratum, r.Op, r.N, r.NsPerOp, r.L1.IPC, r.Kernel.SyscWrite, r.Kernel.WriteBytes, r.L1.L1DMiss, r.L1.BrMiss, r.Note)
	}
}
