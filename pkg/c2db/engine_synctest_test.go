// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"golang.org/x/sys/unix"
)

func synctestKV(i int) (key, val []byte) {
	return []byte{'k', byte(i)}, []byte{'v', byte(i), byte(i ^ 0x5a), byte(i + 3)}
}

func mustOpenShardForSynctest(t *testing.T, dir string, key [32]byte, id uint16) *Shard {
	t.Helper()
	w, err := CreateWAL(filepath.Join(dir, "wal.img"), 128*1024, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("CreateWAL Close: %v", err)
	}
	return mustOpenShard(t, dir, key, id)
}

func TestEngineSynctestAfterPuts(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 11)
	}
	const shardID uint16 = 11
	const nPut = 20
	const nReaders = 4

	s := mustOpenShardForSynctest(t, dir, key, shardID)
	defer s.Close()

	synctest.Test(t, func(t *testing.T) {
		go func() {
			for i := 0; i < nPut; i++ {
				k, v := synctestKV(i)
				if err := s.Put(k, v); err != nil {
					t.Errorf("Put %d: %v", i, err)
				}
			}
		}()
		synctest.Wait()

		for r := 0; r < nReaders; r++ {
			go func() {
				for i := 0; i < nPut; i++ {
					k, want := synctestKV(i)
					got, err := s.Get(k)
					if err != nil || !bytes.Equal(got, want) {
						t.Errorf("lecteur Get %d: err=%v val=%q want %q", i, err, got, want)
					}
				}
			}()
		}
		synctest.Wait()

		for i := 0; i < nPut; i++ {
			k, want := synctestKV(i)
			got, err := s.Get(k)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("après Wait Get %d: err=%v val=%q want %q", i, err, got, want)
			}
		}
	})
}

func TestEnginePutBusy(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShardForSynctest(t, dir, key, 18)
	defer s.Close()
	k := []byte("k")
	synctest.Test(t, func(t *testing.T) {
		hold := make(chan struct{})
		release := make(chan struct{})
		var busy error
		go func() {
			err := s.Update(func(sh *Shard) error {
				close(hold)
				<-release
				return sh.Put(k, []byte("v1"))
			})
			if err != nil {
				t.Errorf("Update: %v", err)
			}
		}()
		go func() {
			<-hold
			busy = s.Put(k, []byte("v2"))
			close(release)
		}()
		synctest.Wait()
		if !errors.Is(busy, ErrWriterBusy) {
			t.Fatalf("Put concurrent: %v want ErrWriterBusy", busy)
		}
	})
}

func TestEngineWriterBusy(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 17)
	}
	const shardID uint16 = 17

	s := mustOpenShardForSynctest(t, dir, key, shardID)
	defer s.Close()

	k := []byte("k")
	v1 := []byte("v1")

	synctest.Test(t, func(t *testing.T) {
		hold := make(chan struct{})
		release := make(chan struct{})
		var busy error
		go func() {
			err := s.Update(func(sh *Shard) error {
				close(hold)
				<-release
				return sh.Put(k, v1)
			})
			if err != nil {
				t.Errorf("Update Put: %v", err)
			}
		}()
		go func() {
			<-hold
			busy = s.Update(func(sh *Shard) error {
				return sh.Put(k, []byte("v2"))
			})
			close(release)
		}()
		synctest.Wait()
		if !errors.Is(busy, ErrWriterBusy) {
			t.Fatalf("Update concurrent: %v want ErrWriterBusy", busy)
		}
		got, err := s.Get(k)
		if err != nil || !bytes.Equal(got, v1) {
			t.Fatalf("Get k: err=%v val=%q want v1", err, got)
		}
	})
}

func TestEngineSynctestInterleave(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 13)
	}
	const shardID uint16 = 13
	const nPut = 20

	s := mustOpenShardForSynctest(t, dir, key, shardID)
	defer s.Close()

	synctest.Test(t, func(t *testing.T) {
		for i := 0; i < nPut; i++ {
			k, v := synctestKV(i)
			go func(k, v []byte) {
				if err := s.Put(k, v); err != nil {
					t.Errorf("Put %q: %v", k, err)
				}
			}(k, v)
			synctest.Wait()
			go func(k, v []byte) {
				got, err := s.Get(k)
				if err != nil || !bytes.Equal(got, v) {
					t.Errorf("Get %q: err=%v val=%q want %q", k, err, got, v)
				}
			}(k, v)
			synctest.Wait()
		}
	})
}

func TestEngineViewDuringUpdate(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 19)
	}
	const shardID uint16 = 19

	k := []byte("k_view_update")
	v1 := []byte("v1_initial")
	v2 := []byte("v2_updated")

	synctest.Test(t, func(t *testing.T) {
		s := mustOpenShardForSynctest(t, dir, key, shardID)
		defer s.Close()

		if err := s.Put(k, v1); err != nil {
			t.Fatalf("Put k=v1: %v", err)
		}

		v, err := s.View()
		if err != nil {
			t.Fatalf("View: %v", err)
		}
		defer v.Close()

		hold := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})

		go func() {
			err := s.Update(func(sh *Shard) error {
				close(hold)
				<-release
				return sh.Put(k, v2)
			})
			if err != nil {
				t.Errorf("Update Put: %v", err)
			}
			close(done)
		}()

		var reads atomic.Int64
		go func() {
			<-hold
			var once bool
			for {
				gotV, err := v.Get(k)
				if err != nil || !bytes.Equal(gotV, v1) {
					t.Errorf("View.Get during update: err=%v val=%q want %q", err, gotV, v1)
					return
				}
				reads.Add(1)

				if !once {
					once = true
					close(release)
				}

				select {
				case <-done:
					return
				default:
				}
			}
		}()

		synctest.Wait()

		if reads.Load() == 0 {
			t.Fatalf("aucun Get concurrent exécuté sur View pendant Update")
		}

		gotV, err := v.Get(k)
		if err != nil || !bytes.Equal(gotV, v1) {
			t.Fatalf("final View.Get: err=%v val=%q want %q", err, gotV, v1)
		}
		gotS, err := s.Get(k)
		if err != nil || !bytes.Equal(gotS, v2) {
			t.Fatalf("final Shard.Get: err=%v val=%q want %q", err, gotS, v2)
		}
	})
}
