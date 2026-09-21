// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPagerCacheSize(t *testing.T) {
	if PagerSlots != 256 {
		t.Fatalf("PagerSlots=%d, attendu 256", PagerSlots)
	}
	if PagerBytes != 4*1024*1024 {
		t.Fatalf("PagerBytes=%d, attendu 4 MiB", PagerBytes)
	}
	if PagerBytes != PagerSlots*int(pageN) {
		t.Fatalf("PagerBytes=%d, PagerSlots*%d=%d", PagerBytes, pageN, PagerSlots*int(pageN))
	}

	path := makePreallocatedImage(t, int64(PagerBytes))
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()

	p, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	assertPagerODirect(t, p)
	if len(p.arena) != PagerBytes {
		t.Fatalf("arena=%d, attendu %d", len(p.arena), PagerBytes)
	}

	page := mmapAligned(t, int(pageN))
	for i := 0; i < PagerSlots; i++ {
		for j := range page {
			page[j] = byte(i + 1)
		}
		lba := uint64(i) * pageLBAs
		if err := p.PutPage(lba, page); err != nil {
			t.Fatalf("PutPage LBA %d: %v", lba, err)
		}
	}
	if p.NDirty() != PagerSlots {
		t.Fatalf("NDirty=%d, attendu %d", p.NDirty(), PagerSlots)
	}
	n, err := p.FlushDirty()
	if err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}
	if n != PagerSlots {
		t.Fatalf("FlushDirty n=%d, attendu %d", n, PagerSlots)
	}
	if p.NDirty() != 0 {
		t.Fatalf("NDirty après Flush=%d", p.NDirty())
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p2, err := NewPager(dev)
	if err != nil {
		t.Fatalf("re-NewPager: %v", err)
	}
	defer func() { _ = p2.CloseWithoutFlush() }()
	for i := 0; i < PagerSlots; i++ {
		lba := uint64(i) * pageLBAs
		got, err := p2.GetPage(lba)
		if err != nil {
			t.Fatalf("GetPage LBA %d: %v", lba, err)
		}
		for j := range got {
			if got[j] != byte(i+1) {
				t.Fatalf("LBA %d octet %d=%#x, attendu %#x", lba, j, got[j], byte(i+1))
			}
		}
	}
}

func TestPagerDirtyFlushOrder(t *testing.T) {
	path := makePreallocatedImage(t, int64(PagerBytes))
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()

	old := mmapAligned(t, int(pageN))
	for i := range old {
		old[i] = 0xA5
	}
	if err := dev.Write(0, old); err != nil {
		t.Fatalf("Write initial: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush initial: %v", err)
	}

	p, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer func() { _ = p.CloseWithoutFlush() }()
	assertPagerODirect(t, p)

	fresh := mmapAligned(t, int(pageN))
	for i := range fresh {
		fresh[i] = 0x5A
	}
	if err := p.PutPage(0, fresh); err != nil {
		t.Fatalf("PutPage: %v", err)
	}
	if p.NDirty() != 1 {
		t.Fatalf("NDirty=%d, attendu 1", p.NDirty())
	}

	got := mmapAligned(t, int(pageN))
	if err := dev.Read(0, got); err != nil {
		t.Fatalf("Read avant FlushDirty: %v", err)
	}
	if !bytes.Equal(got, old) {
		t.Fatalf("Write data avant FlushDirty")
	}

	n, err := p.FlushDirty()
	if err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}
	if n != 1 {
		t.Fatalf("FlushDirty n=%d, attendu 1", n)
	}
	if p.NDirty() != 0 {
		t.Fatalf("NDirty après Flush=%d", p.NDirty())
	}
	if err := dev.Read(0, got); err != nil {
		t.Fatalf("Read après FlushDirty: %v", err)
	}
	if !bytes.Equal(got, fresh) {
		t.Fatalf("page après FlushDirty divergente")
	}

	n, err = p.FlushDirty()
	if err != nil {
		t.Fatalf("FlushDirty idempotent: %v", err)
	}
	if n != 0 {
		t.Fatalf("FlushDirty sans sale n=%d", n)
	}
}

func TestPagerCloseWithoutFlush(t *testing.T) {
	path := makePreallocatedImage(t, int64(PagerBytes))
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}

	old := mmapAligned(t, int(pageN))
	for i := range old {
		old[i] = 0x11
	}
	if err := dev.Write(0, old); err != nil {
		t.Fatalf("Write initial: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush initial: %v", err)
	}

	p, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	fresh := mmapAligned(t, int(pageN))
	for i := range fresh {
		fresh[i] = 0x22
	}
	if err := p.PutPage(0, fresh); err != nil {
		t.Fatalf("PutPage: %v", err)
	}
	if err := p.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close device: %v", err)
	}

	dev, err = Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("re-Open: %v", err)
	}
	defer func() { _ = dev.Close() }()
	got := mmapAligned(t, int(pageN))
	if err := dev.Read(0, got); err != nil {
		t.Fatalf("Read après crash: %v", err)
	}
	if !bytes.Equal(got, old) {
		t.Fatalf("CloseWithoutFlush a écrit la page sale")
	}
}

func assertPagerODirect(t *testing.T, p *Pager) {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(p.dev.fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL: %v", err)
	}
	if flags&unix.O_DIRECT == 0 {
		t.Fatalf("pager device fd lacks O_DIRECT (flags=%#x)", flags)
	}
}

func TestPagerPreadSavings(t *testing.T) {
	path := makePreallocatedImage(t, int64(PagerBytes))
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()

	p, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	assertPagerODirect(t, p)

	page := mmapAligned(t, int(pageN))
	for i := range page {
		page[i] = byte(0x3C ^ i)
	}

	// 1 Put + Flush (pages rendues durables sur disque)
	if err := p.PutPage(0, page); err != nil {
		t.Fatalf("PutPage: %v", err)
	}
	n, err := p.FlushDirty()
	if err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}
	if n != 1 {
		t.Fatalf("FlushDirty n=%d, attendu 1", n)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Réouverture d'un Pager à froid (cache vide) sur le même périphérique
	p2, err := NewPager(dev)
	if err != nil {
		t.Fatalf("re-NewPager: %v", err)
	}
	defer func() { _ = p2.CloseWithoutFlush() }()

	preadBefore := dev.preadN.Load()

	// 1er Get (warmup / cache miss) : lecture réelle sur disque
	got1, err := p2.GetPage(0)
	if err != nil {
		t.Fatalf("GetPage warmup: %v", err)
	}
	if !bytes.Equal(got1, page) {
		t.Fatalf("GetPage warmup divergent")
	}

	preadWarmup := dev.preadN.Load()
	warmupPreads := preadWarmup - preadBefore
	if warmupPreads == 0 {
		t.Fatalf("warmup GetPage attendait au moins 1 pread, obtenu %d", warmupPreads)
	}

	// 49 Gets suivants de la même page (total 50 Gets) : servis depuis le cache Pager
	for i := 2; i <= 50; i++ {
		got, err := p2.GetPage(0)
		if err != nil {
			t.Fatalf("GetPage #%d: %v", i, err)
		}
		if !bytes.Equal(got, page) {
			t.Fatalf("GetPage #%d divergent", i)
		}
	}

	preadAfter50 := dev.preadN.Load()
	additionalPreads := preadAfter50 - preadWarmup
	if additionalPreads != 0 {
		t.Fatalf("49 Gets suivants: %d pread supplémentaires constatés (attendu 0)", additionalPreads)
	}

	t.Logf("TestPagerPreadSavings: warmup_pread=%d, additional_49_gets_pread=%d (total_pread=%d)", warmupPreads, additionalPreads, preadAfter50-preadBefore)
}

func TestPagerClockBeyondCache(t *testing.T) {
	nPages := PagerSlots * 2
	path := makePreallocatedImage(t, int64(PagerBytes))
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()
	if err := dev.Grow(uint64(nPages) * pageN); err != nil {
		t.Fatalf("Grow: %v", err)
	}
	p, err := NewPager(dev)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer func() { _ = p.Close() }()
	page := mmapAligned(t, int(pageN))
	for i := 0; i < nPages; i++ {
		for j := range page {
			page[j] = byte(i + 1)
		}
		lba := uint64(i) * pageLBAs
		if err := p.PutPage(lba, page); err != nil {
			t.Fatalf("PutPage LBA %d: %v", lba, err)
		}
	}
	if _, err := p.FlushDirty(); err != nil {
		t.Fatalf("FlushDirty: %v", err)
	}
	last := uint64(nPages-1) * pageLBAs
	got, err := p.GetPage(last)
	if err != nil {
		t.Fatalf("GetPage last: %v", err)
	}
	for j := range got {
		if got[j] != byte(nPages) {
			t.Fatalf("last page octet %d=%#x want %#x", j, got[j], byte(nPages))
		}
	}
	first, err := p.GetPage(0)
	if err != nil {
		t.Fatalf("GetPage 0 after eviction: %v", err)
	}
	for j := range first {
		if first[j] != 1 {
			t.Fatalf("page 0 after clock octet %d=%#x want 1", j, first[j])
		}
	}
	oob := uint64(nPages) * pageLBAs
	if _, err := p.GetPage(oob); err == nil {
		t.Fatal("GetPage hors fichier: expected error")
	}
}
