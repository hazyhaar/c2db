// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func readOnlyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenShard(dir, [32]byte{}, 0, WithWALBytes(32*LBASize))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s.data != nil {
			_ = s.KillWithoutFlush()
		}
	})
	if err := s.Put([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

type readOnlyFileState struct {
	Sum     [32]byte
	Size    int64
	ModTime time.Time
	Mode    os.FileMode
}

func readOnlySnapshot(t *testing.T, dir string) map[string]readOnlyFileState {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := make(map[string]readOnlyFileState, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("snapshot: %v, %v", copyErr, closeErr)
		}
		state[entry.Name()] = readOnlyFileState{
			Sum: [32]byte(h.Sum(nil)), Size: info.Size(), ModTime: info.ModTime(), Mode: info.Mode(),
		}
	}
	return state
}

func TestReadOnlyOpen(t *testing.T) {
	dir := readOnlyFixture(t)
	for _, name := range []string{".lock", "data.img", "wal.img", "tags.img"} {
		if err := os.Chmod(filepath.Join(dir, name), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	before := readOnlySnapshot(t, dir)
	s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s.data != nil {
			_ = s.Close()
		}
	})
	for _, fd := range []int{s.lockFd, s.data.fd, s.wal.dev.fd, s.pager.tags.fd} {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
			t.Fatalf("fd %d flags=%x err=%v", fd, flags, err)
		}
		flags, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("fd %d missing CLOEXEC: %v", fd, err)
		}
	}
	if got, err := s.Get([]byte("key")); err != nil || string(got) != "value" {
		t.Fatalf("Get=%q err=%v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only open/close changed files")
	}
}

func TestReadOnlyMutationsFailClosed(t *testing.T) {
	dir := readOnlyFixture(t)
	before := readOnlySnapshot(t, dir)
	s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	called := false
	cases := []struct {
		name string
		call func() error
	}{
		{"Put", func() error { return s.Put([]byte("key"), []byte("changed")) }},
		{"PutInvalid", func() error { return s.Put(nil, nil) }},
		{"PutBatch", func() error { return s.PutBatch([][2][]byte{{[]byte("key"), []byte("changed")}}) }},
		{"PutBatchEmpty", func() error { return s.PutBatch(nil) }},
		{"Delete", func() error { return s.Delete([]byte("key")) }},
		{"DeleteBatch", func() error { return s.DeleteBatch([][]byte{[]byte("key")}) }},
		{"DeleteBatchEmpty", func() error { return s.DeleteBatch(nil) }},
		{"Mut", func() error { return s.Mut([]byte("key"), nil) }},
		{"Update", func() error { return s.Update(func(*Shard) error { called = true; return nil }) }},
		{"Compact", s.Compact},
		{"Checkpoint", s.Checkpoint},
		{"RepackHeap", s.RepackHeap},
	}
	// Les gardes doivent précéder même un verrou déjà détenu et un état empoisonné.
	s.writeMu.Lock()
	s.poison(ErrDevicePoisoned)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != ErrReadOnly {
				t.Fatalf("got %v, want ErrReadOnly", err)
			}
		})
	}
	s.poisonErr.Store(nil)
	s.writeMu.Unlock()
	if called {
		t.Fatal("Update invoked its callback")
	}
	if got, err := s.Get([]byte("key")); err != nil || string(got) != "value" {
		t.Fatalf("Get=%q err=%v", got, err)
	}
	if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected mutations changed files")
	}
}

func TestReadOnlyConcurrentReaders(t *testing.T) {
	dir := readOnlyFixture(t)
	fd, err := unix.Open(filepath.Join(dir, ".lock"), unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	// Un lecteur externe reste verrouillé pendant toutes les ouvertures : une
	// régression vers F_WRLCK échoue même si l'ordonnanceur les sérialise.
	fl := unix.Flock_t{Type: unix.F_RDLCK, Whence: int16(unix.SEEK_SET), Len: 1}
	if err := unix.FcntlFlock(uintptr(fd), unix.F_OFD_SETLK, &fl); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 10)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			<-start
			s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
			if err != nil {
				results <- err
				return
			}
			for range 20 {
				got, getErr := s.Get([]byte("key"))
				if getErr != nil || string(got) != "value" {
					err = fmt.Errorf("Get=%q err=%v", got, getErr)
					break
				}
			}
			if closeErr := s.Close(); err == nil {
				err = closeErr
			}
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent reader: %v", err)
		}
	}
	fl.Type = unix.F_WRLCK
	if err := unix.FcntlFlock(uintptr(fd), unix.F_OFD_SETLK, &fl); err != nil {
		t.Fatalf("reader lock leaked: %v", err)
	}
	if s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true)); err != ErrWriterBusy {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("exclusive writer: got %v, want ErrWriterBusy", err)
	}
}

func TestReadOnlyCloseWithoutFlush(t *testing.T) {
	for _, method := range []string{"Close", "CloseWithoutFlush"} {
		t.Run(method, func(t *testing.T) {
			dir := readOnlyFixture(t)
			before := readOnlySnapshot(t, dir)
			s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
			if err != nil {
				t.Fatal(err)
			}
			data, walDev, tags := s.data, s.wal.dev, s.pager.tags
			pager, journal, lockFD := s.pager, s.wal, s.lockFd
			// Injecter des tampons sales rend toute tentative de vidange observable,
			// y compris WAL.Close qui ignore certaines erreurs d'écriture.
			if err := pager.PutPage(0, s.pub[:pageN]); err != nil {
				t.Fatal(err)
			}
			journal.pending = []Record{{Type: RecPut, Payload: packKV([]byte("key"), []byte("changed"))}}
			journal.pendingN = packedRecSize(journal.pending[0])
			journal.qCount = 1
			if method == "Close" {
				err = s.Close()
			} else {
				err = s.CloseWithoutFlush()
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, dev := range []*Device{data, walDev, tags} {
				if dev.fd != -1 || dev.PwriteN() != 0 || dev.IsPoisoned() {
					t.Fatalf("close wrote or leaked device: fd=%d writes=%d poisoned=%v", dev.fd, dev.PwriteN(), dev.IsPoisoned())
				}
			}
			if _, err := unix.FcntlInt(uintptr(lockFD), unix.F_GETFD, 0); err != unix.EBADF {
				t.Fatalf("lock descriptor not closed: %v", err)
			}
			if s.pub != nil || s.dirty != nil || s.live.Load() != nil || pager.arena != nil || pager.tagBlk != nil || journal.block != nil || journal.qBuf != nil || journal.probeBuf != nil {
				t.Fatal("close retained mappings")
			}
			if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatal("close changed files")
			}
		})
	}
}

func TestReadOnlyReplayInMemory(t *testing.T) {
	dir := readOnlyFixture(t)
	w, err := OpenShard(dir, [32]byte{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Put([]byte("replayed"), []byte("durable WAL")); err != nil {
		t.Fatal(err)
	}
	if err := w.Delete([]byte("key")); err != nil {
		t.Fatal(err)
	}
	if err := w.wal.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := w.KillWithoutFlush(); err != nil {
		t.Fatal(err)
	}
	before := readOnlySnapshot(t, dir)
	for range 2 {
		s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get([]byte("replayed")); err != nil || string(got) != "durable WAL" {
			t.Errorf("replayed Get=%q err=%v", got, err)
		}
		if _, err := s.Get([]byte("key")); err != ErrNotFound {
			t.Errorf("replayed Delete: %v", err)
		}
		if s.pager.NDirty() != 0 || s.walCheckpoints != 0 {
			t.Error("replay published or checkpointed")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("replay changed files")
	}
}

func TestReadOnlyMissingFiles(t *testing.T) {
	for _, name := range []string{".lock", "data.img", "wal.img", "tags.img"} {
		t.Run(name, func(t *testing.T) {
			dir := readOnlyFixture(t)
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			before := readOnlySnapshot(t, dir)
			if s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true)); err == nil {
				_ = s.Close()
				t.Fatal("missing file accepted")
			}
			if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatal("failed open changed files")
			}
		})
	}
}

func TestReadOnlyUninitializedHeap(t *testing.T) {
	dir := readOnlyFixture(t)
	// Une racine perdue et un journal pointé permettent de vérifier le chemin
	// vide sans que l'ouverture ne réinitialise ni ne scelle la page.
	f, err := os.OpenFile(filepath.Join(dir, "data.img"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteAt(make([]byte, pageN), 0)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("clear root: %v, %v", writeErr, closeErr)
	}
	before := readOnlySnapshot(t, dir)
	s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true))
	if err != nil {
		t.Fatal(err)
	}
	if s.pub[20] != 0 || s.heapUsed != 0 {
		t.Error("uninitialized heap was initialized")
	}
	if _, err := s.Get([]byte("key")); err != ErrNotFound {
		t.Errorf("empty Get: %v", err)
	}
	if keys, err := s.ScanPrefix(nil); err != nil || len(keys) != 0 {
		t.Errorf("empty ScanPrefix: keys=%v err=%v", keys, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("empty open changed files")
	}
}

func TestReadOnlyRecoveryRequired(t *testing.T) {
	dir := readOnlyFixture(t)
	w, err := OpenShard(dir, [32]byte{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Put([]byte("recovery"), []byte("pending")); err != nil {
		t.Fatal(err)
	}
	if err := w.wal.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := w.KillWithoutFlush(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "data.img"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteAt(make([]byte, pageN), 0)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("clear root: %v, %v", writeErr, closeErr)
	}
	before := readOnlySnapshot(t, dir)
	if s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true)); err != ErrRecoveryRequired {
		if s != nil {
			_ = s.Close()
		}
		t.Fatalf("got %v, want ErrRecoveryRequired", err)
	}
	if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("failed recovery changed files")
	}
	s := &Shard{readOnly: true}
	if err := s.rebuildAllVersionsLocked(); err != ErrRecoveryRequired {
		t.Fatalf("rebuild guard: %v", err)
	}
}

func TestReadOnlyWritableOptions(t *testing.T) {
	dir := readOnlyFixture(t)
	before := readOnlySnapshot(t, dir)
	for _, opt := range []Option{WithTxLog(filepath.Join(dir, "events")), WithHistoryRetention(RetentionArchiveSuperseded)} {
		if s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true), opt); err != ErrReadOnly {
			if s != nil {
				_ = s.Close()
			}
			t.Fatalf("writable option: %v", err)
		}
	}
	if after := readOnlySnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("writable options changed files")
	}
	s, err := OpenShard(dir, [32]byte{}, 0, WithReadOnly(true), WithReadOnly(false))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Put([]byte("key"), []byte("writable")); err != nil {
		t.Fatalf("WithReadOnly(false): %v", err)
	}
}
