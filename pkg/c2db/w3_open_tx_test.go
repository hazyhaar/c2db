// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestW3aOpenRespectsWriterOFDLock vérifie que l'ouverture prend le verrou
// écrivain du shard. Un verrou OFD F_WRLCK est posé sur `.lock` par une
// description de fichier distincte ; une ouverture concurrente doit échouer
// avec ErrWriterBusy au lieu d'être acceptée silencieusement.
func TestW3aOpenRespectsWriterOFDLock(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	copy(key[:], "w3a/open/ofd/lock/test")

	holder, err := unix.Open(filepath.Join(dir, ".lock"), unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatalf("open .lock: %v", err)
	}
	defer func() { _ = unix.Close(holder) }()

	fl := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
	if err := unix.FcntlFlock(uintptr(holder), unix.F_OFD_SETLK, &fl); err != nil {
		t.Skipf("verrou OFD indisponible: %v", err)
	}

	sh, err := OpenShard(dir, key, 1)
	if err == nil {
		_ = sh.Close()
		t.Fatalf("W3a: ouverture concurrente acceptee (err=nil, attendu %v)", ErrWriterBusy)
	}
	if !errors.Is(err, ErrWriterBusy) {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("W3a: err=%v, attendu %v", err, ErrWriterBusy)
	}
}

// TestW3bCheckpointAfterReplay vérifie qu'une ouverture qui rejoue le WAL pose
// un pointage et recycle le journal, tout en publiant d'abord les pages
// reconstruites vers le pager. Le scénario interrompt le shard après Put sans
// vidange du pager : data.img ne contient pas les pages insérées, seul le WAL
// les porte. Une réouverture doit les reconstruire, les publier, pointer et
// tronquer ; une troisième ouverture doit encore les servir.
func TestW3bCheckpointAfterReplay(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	copy(key[:], "w3b/checkpoint/after/replay")

	const n = 64
	pairs := make([][2][]byte, n)
	for i := 0; i < n; i++ {
		pairs[i] = [2][]byte{
			[]byte(fmt.Sprintf("w3b-key-%02d", i)),
			[]byte(fmt.Sprintf("w3b-val-%02d", i)),
		}
	}

	s := mustOpenShard(t, dir, key, 7, WithHeapPages(4096), WithWALBytes(4*1024*1024))
	for i := 0; i < n; i++ {
		if err := s.Put(pairs[i][0], pairs[i][1]); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if err := s.wal.Flush(); err != nil {
		t.Fatalf("wal flush: %v", err)
	}
	if err := s.KillWithoutFlush(); err != nil {
		t.Fatalf("KillWithoutFlush: %v", err)
	}

	s2 := mustOpenShard(t, dir, key, 7, WithHeapPages(4096), WithWALBytes(4*1024*1024))
	if !s2.wal.hasCheck {
		next := s2.wal.next
		_ = s2.Close()
		t.Fatalf("W3b: aucun pointage posé après rejeu (hasCheck=false, next=%d)", next)
	}
	if s2.wal.next > 1 {
		next := s2.wal.next
		_ = s2.Close()
		t.Fatalf("W3b: journal non recyclé après pointage (next=%d, attendu <=1)", next)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close s2: %v", err)
	}

	s3 := mustOpenShard(t, dir, key, 7, WithHeapPages(4096), WithWALBytes(4*1024*1024))
	defer func() { _ = s3.Close() }()
	for i := 0; i < n; i++ {
		got, err := s3.Get(pairs[i][0])
		if err != nil {
			t.Fatalf("W3b: clé %s perdue après pointage: %v", pairs[i][0], err)
		}
		if !bytes.Equal(got, pairs[i][1]) {
			t.Fatalf("W3b: valeur %s altérée: %q, attendu %q", pairs[i][0], got, pairs[i][1])
		}
	}
}

// TestW3bDoesNotTruncateOnDiscardedTxGroup vérifie la garde de W3b : un rejeu
// non strict qui écarte un groupe transactionnel invalide ne doit ni pointer ni
// tronquer le journal, sinon une ouverture stricte ultérieure ne pourrait plus
// constater l'infraction. Cette garde conserve la sémantique de TestQual_08.
func TestW3bDoesNotTruncateOnDiscardedTxGroup(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	copy(key[:], "w3b/discarded/tx/group")

	s := mustOpenShard(t, dir, key, 0)
	if err := s.Put([]byte("base-stable-k"), []byte("base-stable-v")); err != nil {
		t.Fatalf("Put base: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close base: %v", err)
	}

	w, err := OpenWAL(filepath.Join(dir, "wal.img"), key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	txID := [16]byte{0xCA, 0xFE, 0xBA, 0xBE, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C}
	if err := w.Append(Record{ID: txID, Type: RecPut, Payload: packTxKV([]byte("tx-prefix-key"), []byte(`{"field1":"init-val"}`))}); err != nil {
		t.Fatalf("Append m1: %v", err)
	}
	if err := w.Append(Record{ID: txID, Type: RecMut, Payload: packTxKV([]byte("tx-prefix-key"), []byte(`[{"f":"field1","op":99,"v":"1"}]`))}); err != nil {
		t.Fatalf("Append m2: %v", err)
	}
	if err := w.Append(Record{ID: txID, Type: RecTxCommit}); err != nil {
		t.Fatalf("Append commit: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush WAL: %v", err)
	}
	_ = w.Close()

	s2 := mustOpenShard(t, dir, key, 0)
	if s2.wal.hasCheck {
		_ = s2.Close()
		t.Fatalf("W3b: le groupe écarté a été rendu définitif par un pointage (hasCheck=true)")
	}
	if _, err := s2.Get([]byte("tx-prefix-key")); !errors.Is(err, ErrNotFound) {
		_ = s2.Close()
		t.Fatalf("groupe écarté non rollbacké: err=%v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close s2: %v", err)
	}

	if _, err := OpenShardStrict(dir, key, 0); err == nil {
		t.Fatalf("l'ouverture stricte aurait dû refuser le groupe transactionnel invalide")
	} else if !strings.Contains(err.Error(), "replay tx") {
		t.Fatalf("erreur stricte attendue 'replay tx', obtenue: %v", err)
	}
}
