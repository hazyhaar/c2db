// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTopoCycleComplete(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/topo.img"
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 7)
	}

	j, err := CreateTopo(path, testImageSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateTopo: %v", err)
	}
	assertTopoODirect(t, j)

	const tsNs = uint64(1_704_067_200_123_456_789)
	id1, err := NewID(tsNs, 3, 1)
	if err != nil {
		t.Fatalf("NewID 1: %v", err)
	}
	id2, err := NewID(tsNs, 3, 2)
	if err != nil {
		t.Fatalf("NewID 2: %v", err)
	}

	var refZero [32]byte
	var refCAS [32]byte
	for i := range refCAS {
		refCAS[i] = byte(0xA0 + i)
	}

	a1 := Artifact{Kind: 1, ID: id1, Ref: refZero}
	a2 := Artifact{Kind: 2, ID: id2, Ref: refCAS}
	if err := j.Append(a1); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := j.Append(a2); err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	if err := j.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay avant Close: %v", err)
	}
	assertArtifactsEqual(t, got, []Artifact{a1, a2})

	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	j, err = OpenTopo(path, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("OpenTopo: %v", err)
	}
	assertTopoODirect(t, j)
	got, err = j.Replay()
	if err != nil {
		t.Fatalf("Replay persisté: %v", err)
	}
	assertArtifactsEqual(t, got, []Artifact{a1, a2})
	if err := j.Close(); err != nil {
		t.Fatalf("Close after persist: %v", err)
	}

	dev, err := Open(path)
	if err != nil {
		t.Fatalf("Open device for corrupt: %v", err)
	}
	blk := mmapAligned(t, LBASize)
	if err := dev.Read(0, blk); err != nil {
		t.Fatalf("Read LBA 0: %v", err)
	}
	blk[topoKindOff] ^= 0xFF
	if err := dev.Write(0, blk); err != nil {
		t.Fatalf("Write corrupted LBA 0: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush corrupt: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close device: %v", err)
	}

	j, err = OpenTopo(path, key)
	if err != nil {
		t.Fatalf("OpenTopo after corrupt: %v", err)
	}
	defer func() { _ = j.Close() }()
	if _, err := j.Replay(); err == nil {
		t.Fatalf("Replay after kind flip: expected tag error")
	} else if !errors.Is(err, errTopoBadTag) {
		t.Fatalf("Replay after kind flip: got %v, want tag mismatch", err)
	}
}

func TestTopoPersistCloseOpen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/topo.img"
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 11)
	}

	j := mustCreateTopo(t, path, key)
	const tsNs = uint64(1_704_067_200_000_000_000)
	id, err := NewID(tsNs, 9, 4)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	var ref [32]byte
	copy(ref[:], []byte("cas-hash-ref-32-bytes-exact!!!!"))
	want := Artifact{Kind: 7, ID: id, Ref: ref}
	if err := j.Append(want); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	j = mustOpenTopo(t, path, key)
	got, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	assertArtifactsEqual(t, got, []Artifact{want})

	id2, err := NewID(tsNs, 9, 5)
	if err != nil {
		t.Fatalf("NewID 2: %v", err)
	}
	extra := Artifact{Kind: 8, ID: id2}
	if err := j.Append(extra); err != nil {
		t.Fatalf("Append after Open: %v", err)
	}
	if err := j.Flush(); err != nil {
		t.Fatalf("Flush extra: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close extra: %v", err)
	}

	j = mustOpenTopo(t, path, key)
	got, err = j.Replay()
	if err != nil {
		t.Fatalf("Replay after extra: %v", err)
	}
	assertArtifactsEqual(t, got, []Artifact{want, extra})
	if err := j.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func assertTopoODirect(t *testing.T, j *Topo) {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(j.dev.fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL: %v", err)
	}
	if flags&unix.O_DIRECT == 0 {
		t.Fatalf("Topo device fd lacks O_DIRECT (flags=%#x)", flags)
	}
}

func assertArtifactsEqual(t *testing.T, got, want []Artifact) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("artifact count: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("artifact %d: got kind=%d id=%x ref=%x want kind=%d id=%x ref=%x",
				i, got[i].Kind, got[i].ID, got[i].Ref, want[i].Kind, want[i].ID, want[i].Ref)
		}
	}
}

func mustCreateTopo(t *testing.T, path string, key [32]byte) *Topo {
	t.Helper()
	j, err := CreateTopo(path, testImageSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateTopo: %v", err)
	}
	return j
}

func mustOpenTopo(t *testing.T, path string, key [32]byte) *Topo {
	t.Helper()
	j, err := OpenTopo(path, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("OpenTopo: %v", err)
	}
	return j
}
