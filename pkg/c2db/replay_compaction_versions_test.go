package c2db

import (
	"bytes"
	"testing"
)

func TestReplayCompactionPreserveVersions(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 0x5A)
	}
	s := mustOpenShard(t, dir, key, 3, WithHeapPages(4096), WithWALBytes(4*1024*1024))
	defer func() { _ = s.Close() }()

	k := []byte("w2-versions")
	if err := s.Put(k, []byte("v1")); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	id1 := s.lastID
	if err := s.Put(k, []byte("v2")); err != nil {
		t.Fatalf("Put v2: %v", err)
	}
	id2 := s.lastID
	if err := s.Put(k, []byte("v3")); err != nil {
		t.Fatalf("Put v3: %v", err)
	}
	id3 := s.lastID

	if got, err := s.GetAsOf(k, id1); err != nil || !bytes.Equal(got, []byte("v1")) {
		t.Fatalf("precondition GetAsOf(id1)=%q err=%v", got, err)
	}
	if got, err := s.GetAsOf(k, id2); err != nil || !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("precondition GetAsOf(id2)=%q err=%v", got, err)
	}

	triggered := false
	_ = s.appliquerAvecRepackRejeu(func() error {
		if !triggered {
			triggered = true
			return ErrHeapFull
		}
		return nil
	})
	if !triggered {
		t.Fatal("filet non declenche")
	}

	got1, err1 := s.GetAsOf(k, id1)
	got2, err2 := s.GetAsOf(k, id2)
	got3, err3 := s.GetAsOf(k, id3)
	t.Logf("apres compaction rejeu: id1=%q err=%v | id2=%q err=%v | id3=%q err=%v", got1, err1, got2, err2, got3, err3)
	if err1 != nil || !bytes.Equal(got1, []byte("v1")) {
		t.Fatalf("W2: version ancienne id1 perdue: got=%q err=%v", got1, err1)
	}
	if err2 != nil || !bytes.Equal(got2, []byte("v2")) {
		t.Fatalf("W2: version ancienne id2 perdue: got=%q err=%v", got2, err2)
	}
}
