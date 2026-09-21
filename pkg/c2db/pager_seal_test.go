// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCI55_PageSealReject(t *testing.T) {
	path := makePreallocatedImage(t, int64(4*pageN))
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()
	tagsPath := makePreallocatedImage(t, int64(uint64(tagsFileLBAs)*LBASize))
	tags, err := Open(tagsPath)
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	defer func() { _ = tags.Close() }()
	p, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	var key [32]byte
	key[0] = 7
	if err := p.SetSeal(key, tags, 0); err != nil {
		t.Fatalf("SetSeal: %v", err)
	}
	page := mmapAligned(t, int(pageN))
	for i := range page {
		page[i] = 0xA5
	}
	if err := p.PutPage(0, page); err != nil {
		t.Fatalf("PutPage: %v", err)
	}
	if _, err := p.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}
	bad := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if err := p.writeTag(0, bad); err != nil {
		t.Fatalf("writeTag: %v", err)
	}
	for i := range p.slots {
		p.slots[i].valid = false
	}
	if _, err := p.GetPage(0); !errors.Is(err, errPageSeal) {
		t.Fatalf("GetPage sceau corrompu: %v want errPageSeal", err)
	}
}

func TestCI55_PageSealZeroTagPolicy(t *testing.T) {
	path := makePreallocatedImage(t, int64(4*pageN))
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()
	tagsPath := makePreallocatedImage(t, int64(uint64(tagsFileLBAs)*LBASize))
	tags, err := Open(tagsPath)
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	defer func() { _ = tags.Close() }()
	p, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	var key [32]byte
	key[0] = 42
	if err := p.SetSeal(key, tags, 1); err != nil {
		t.Fatalf("SetSeal: %v", err)
	}

	page := mmapAligned(t, int(pageN))

	// Cas 1 : page 0 avec tag nul -> rejeté obligatoirement
	if err := p.writeTag(0, [16]byte{}); err != nil {
		t.Fatalf("writeTag: %v", err)
	}
	if err := p.verifyPage(0, page); !errors.Is(err, errPageSeal) {
		t.Fatalf("page 0 avec tag nul doit être rejetée: got %v, want errPageSeal", err)
	}

	// Cas 2 : page non allouée (index 1, type=0, nslots=0) avec tag nul -> accepté
	if err := p.writeTag(1, [16]byte{}); err != nil {
		t.Fatalf("writeTag: %v", err)
	}
	if err := p.verifyPage(1*pageLBAs, page); err != nil {
		t.Fatalf("page non allouée avec tag nul doit être acceptée: %v", err)
	}

	// Cas 3 : page pseudo-allouée (index 1, type=1) avec tag nul -> rejeté
	page[BT_TypeOffset] = 1
	if err := p.verifyPage(1*pageLBAs, page); !errors.Is(err, errPageSeal) {
		t.Fatalf("page allouée avec tag nul doit être rejetée: got %v, want errPageSeal", err)
	}
}
