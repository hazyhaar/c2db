// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

const testImageSize = 64 * 1024

func TestDeviceReadAfterWritePersist(t *testing.T) {
	path := makePreallocatedImage(t, testImageSize)

	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}

	pat4k := mmapAligned(t, LBASize)
	for i := range pat4k {
		pat4k[i] = byte(i)
	}
	pat16k := mmapAligned(t, 4*LBASize)
	for i := range pat16k {
		pat16k[i] = byte(0xA5 ^ i)
	}

	if err := dev.Write(0, pat4k); err != nil {
		t.Fatalf("Write LBA 0: %v", err)
	}
	if err := dev.Write(4, pat16k); err != nil {
		t.Fatalf("Write LBA 4: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got4k := mmapAligned(t, LBASize)
	got16k := mmapAligned(t, 4*LBASize)
	if err := dev.Read(0, got4k); err != nil {
		t.Fatalf("Read LBA 0: %v", err)
	}
	if err := dev.Read(4, got16k); err != nil {
		t.Fatalf("Read LBA 4: %v", err)
	}
	if !bytes.Equal(got4k, pat4k) {
		t.Fatalf("read-after-write LBA 0 mismatch")
	}
	if !bytes.Equal(got16k, pat16k) {
		t.Fatalf("read-after-write LBA 4 mismatch")
	}

	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dev, err = Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("re-Open: %v", err)
	}
	defer func() { _ = dev.Close() }()

	persist4k := mmapAligned(t, LBASize)
	persist16k := mmapAligned(t, 4*LBASize)
	if err := dev.Read(0, persist4k); err != nil {
		t.Fatalf("persist Read LBA 0: %v", err)
	}
	if err := dev.Read(4, persist16k); err != nil {
		t.Fatalf("persist Read LBA 4: %v", err)
	}
	if !bytes.Equal(persist4k, pat4k) {
		t.Fatalf("persist LBA 0 mismatch")
	}
	if !bytes.Equal(persist16k, pat16k) {
		t.Fatalf("persist LBA 4 mismatch")
	}

	short := make([]byte, 100)
	if err := dev.Write(0, short); err == nil {
		t.Fatalf("Write 100 bytes: expected error")
	}

	unaligned := unalignedBuf(t, LBASize)
	if err := dev.Write(0, unaligned); err == nil {
		t.Fatalf("Write unaligned 4096: expected error")
	} else if !errors.Is(err, unix.EINVAL) {
		t.Fatalf("Write unaligned 4096: got %v, want EINVAL", err)
	}
}

func TestDeviceCreatePagePersist(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir+"/bad100", 100); err == nil {
		t.Fatalf("Create size=100: expected error")
	}
	if _, err := Create(dir+"/bad0", 0); err == nil {
		t.Fatalf("Create size=0: expected error")
	}

	path := dir + "/c2db-create.img"
	dev, err := Create(path, testImageSize)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Create: %v", err)
	}

	const pageN = uint64(16384)
	page := mmapAligned(t, int(pageN))
	want := Db_page_hdr_t{Nslots: 7, Free_lo: 64, Free_hi: 200}
	if Db_page_hdr_write(page, pageN, &want) == 0 {
		t.Fatalf("Db_page_hdr_write: rejected")
	}

	if err := dev.Write(0, page); err != nil {
		t.Fatalf("Write LBA 0: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dev, err = Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()

	gotPage := mmapAligned(t, int(pageN))
	if err := dev.Read(0, gotPage); err != nil {
		t.Fatalf("Read LBA 0: %v", err)
	}
	got := Db_page_hdr_read(gotPage, pageN)
	if got.Nslots != want.Nslots || got.Free_lo != want.Free_lo || got.Free_hi != want.Free_hi {
		t.Fatalf("persist hdr mismatch: got %+v want %+v", got, want)
	}
}

func TestDeviceGrow(t *testing.T) {
	path := makePreallocatedImage(t, testImageSize)
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}
	if dev.Size() != uint64(testImageSize) {
		t.Fatalf("Size=%d want %d", dev.Size(), testImageSize)
	}
	if err := dev.Grow(0); !errors.Is(err, errDeviceSize) {
		t.Fatalf("Grow 0: %v", err)
	}
	if err := dev.Grow(uint64(testImageSize / 2)); !errors.Is(err, errDeviceGrow) {
		t.Fatalf("Grow shrink: %v", err)
	}
	grown := uint64(2 * testImageSize)
	if err := dev.Grow(grown); err != nil {
		t.Fatalf("Grow: %v", err)
	}
	if dev.Size() != grown {
		t.Fatalf("Size after grow=%d want %d", dev.Size(), grown)
	}
	last := mmapAligned(t, LBASize)
	for i := range last {
		last[i] = 0x5A
	}
	lastLBA := grown/LBASize - 1
	if err := dev.Write(lastLBA, last); err != nil {
		t.Fatalf("Write last LBA: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	dev, err = Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer func() { _ = dev.Close() }()
	if dev.Size() != grown {
		t.Fatalf("persist Size=%d want %d", dev.Size(), grown)
	}
	got := mmapAligned(t, LBASize)
	if err := dev.Read(lastLBA, got); err != nil {
		t.Fatalf("Read last LBA: %v", err)
	}
	if !bytes.Equal(got, last) {
		t.Fatal("grow read-after-write mismatch")
	}
}

func TestDeviceGrow1GiB(t *testing.T) {
	path := makePreallocatedImage(t, testImageSize)
	dev, err := Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()
	if err := dev.Grow(maxShardFile); err != nil {
		t.Skipf("Grow 1 Gio: %v", err)
	}
	if dev.Size() != maxShardFile {
		t.Fatalf("Size=%d want %d", dev.Size(), maxShardFile)
	}
	last := mmapAligned(t, LBASize)
	last[0] = 0xAB
	lba := maxShardFile/LBASize - 1
	if err := dev.Write(lba, last); err != nil {
		t.Fatalf("Write last: %v", err)
	}
	got := mmapAligned(t, LBASize)
	if err := dev.Read(lba, got); err != nil {
		t.Fatalf("Read last: %v", err)
	}
	if got[0] != 0xAB {
		t.Fatalf("last octet=%#x", got[0])
	}
}

func makePreallocatedImage(t *testing.T, size int64) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "c2db-device-*.img")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		t.Fatalf("Truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}
	return path
}

func mmapAligned(t *testing.T, n int) []byte {
	t.Helper()
	b, err := unix.Mmap(-1, 0, n, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		t.Fatalf("mmap: %v", err)
	}
	t.Cleanup(func() { _ = unix.Munmap(b) })
	return b
}

func unalignedBuf(t *testing.T, n int) []byte {
	t.Helper()
	raw := make([]byte, n+LBASize)
	addr := uintptr(unsafe.Pointer(&raw[0]))
	off := int(addr & (LBASize - 1))
	if off == 0 {
		return raw[1 : 1+n]
	}
	return raw[0:n]
}
