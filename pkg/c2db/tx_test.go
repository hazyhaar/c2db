// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestTxCommitRollbackCycle(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 77)
	}
	s := mustOpenShard(t, dir, key, 4)
	defer s.Close()

	// 1. Transaction avec Commit nominal
	tx1, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx1: %v", err)
	}
	if tx1.ID() == [16]byte{} {
		t.Fatalf("tx1 ID is zero")
	}
	if err := tx1.Put([]byte("tx-k1"), []byte("tx-v1")); err != nil {
		t.Fatalf("tx1 Put: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 Commit: %v", err)
	}

	// Double commit / opération post-commit doit échouer avec ErrTxClosed
	if err := tx1.Commit(); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("tx1 double commit: got %v want ErrTxClosed", err)
	}
	if err := tx1.Put([]byte("tx-k1"), []byte("bad")); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("tx1 put after commit: got %v want ErrTxClosed", err)
	}

	got, err := s.Get([]byte("tx-k1"))
	if err != nil || !bytes.Equal(got, []byte("tx-v1")) {
		t.Fatalf("Get k1: err=%v val=%q want tx-v1", err, got)
	}

	// 2. Transaction avec Rollback
	tx2, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx2: %v", err)
	}
	if err := tx2.Put([]byte("tx-k2"), []byte("tx-v2-aborted")); err != nil {
		t.Fatalf("tx2 Put: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("tx2 Rollback: %v", err)
	}

	// Vérification de l'absence de tx-k2
	if _, err := s.Get([]byte("tx-k2")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get k2 after rollback: attendu ErrNotFound, got %v", err)
	}

	// 3. Concurrence : un second écrivain pendant une transaction active est rejeté avec ErrWriterBusy
	tx3, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin tx3: %v", err)
	}

	_, errBusy := s.Begin()
	if !errors.Is(errBusy, ErrWriterBusy) {
		t.Fatalf("Begin concurrent: got %v want ErrWriterBusy", errBusy)
	}

	busyCh := make(chan error, 1)
	go func() {
		busyCh <- s.Put([]byte("k-concurrent"), []byte("val"))
	}()
	errPutBusy := <-busyCh
	if !errors.Is(errPutBusy, ErrWriterBusy) {
		t.Fatalf("Put concurrent: got %v want ErrWriterBusy", errPutBusy)
	}

	if err := tx3.Commit(); err != nil {
		t.Fatalf("tx3 Commit: %v", err)
	}
}

func TestTxHighFrequencyBatches(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 99)
	}
	s := mustOpenShard(t, dir, key, 5)
	defer s.Close()

	const numTxs = 50
	const opsPerTx = 20

	for i := 0; i < numTxs; i++ {
		tx, err := s.Begin()
		if err != nil {
			t.Fatalf("Begin %d: %v", i, err)
		}
		for j := 0; j < opsPerTx; j++ {
			k := []byte(fmt.Sprintf("hf-%03d-%03d", i, j))
			v := []byte(fmt.Sprintf("val-%03d-%03d", i, j))
			if err := tx.Put(k, v); err != nil {
				t.Fatalf("tx %d Put %d: %v", i, j, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("tx %d Commit: %v", i, err)
		}
	}

	// Relecture aléatoire et vérification
	for i := 0; i < numTxs; i += 5 {
		for j := 0; j < opsPerTx; j += 5 {
			k := []byte(fmt.Sprintf("hf-%03d-%03d", i, j))
			exp := []byte(fmt.Sprintf("val-%03d-%03d", i, j))
			got, err := s.Get(k)
			if err != nil {
				t.Fatalf("Get %s: %v", k, err)
			}
			if !bytes.Equal(got, exp) {
				t.Fatalf("Val mismatch %s: got %q want %q", k, got, exp)
			}
		}
	}
}

func TestTxConcurrentContention(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 123)
	}
	s := mustOpenShard(t, dir, key, 6)
	defer s.Close()

	const goroutines = 16
	const rounds = 20

	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				tx, err := s.Begin()
				if err != nil {
					if errors.Is(err, ErrWriterBusy) {
						continue
					}
					t.Errorf("Unexpected error: %v", err)
					return
				}
				k := []byte(fmt.Sprintf("c-%02d-%02d", gid, r))
				v := []byte(fmt.Sprintf("data-%02d-%02d", gid, r))
				_ = tx.Put(k, v)
				_ = tx.Commit()
			}
		}(g)
	}

	wg.Wait()
}
