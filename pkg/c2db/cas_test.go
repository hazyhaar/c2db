// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"testing"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"golang.org/x/sys/unix"
)

const casTestSize = 1 << 20

func TestCASPutGetRAW(t *testing.T) {
	path := t.TempDir() + "/c2db-cas.img"
	c := mustCreateCAS(t, path)
	assertCASODirect(t, c)

	raw := []byte("payload-cas-raw-bit-exact")
	h, err := c.Put(raw)
	if err != nil {
		t.Fatalf("Put RAW: %v", err)
	}
	want := blake3archtsim.Sum256(raw)
	if h != want {
		t.Fatalf("Put hash mismatch")
	}
	got, err := c.Get(h)
	if err != nil {
		t.Fatalf("Get RAW: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("Get RAW mismatch: got %q want %q", got, raw)
	}

	wide := bytes.Repeat([]byte{0x5A}, 5000)
	hw, err := c.Put(wide)
	if err != nil {
		t.Fatalf("Put 5000: %v", err)
	}
	gotW, err := c.Get(hw)
	if err != nil {
		t.Fatalf("Get 5000: %v", err)
	}
	if !bytes.Equal(gotW, wide) {
		t.Fatalf("Get 5000 mismatch")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCASCloseOpenGet(t *testing.T) {
	path := t.TempDir() + "/c2db-cas.img"
	c := mustCreateCAS(t, path)
	assertCASODirect(t, c)

	raw := []byte("persist-cas-bit-exact")
	h, err := c.Put(raw)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	c = mustOpenCAS(t, path)
	assertCASODirect(t, c)
	defer func() { _ = c.Close() }()
	got, err := c.Get(h)
	if err != nil {
		t.Fatalf("Get after Open: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("Get after Open mismatch: got %q want %q", got, raw)
	}
}

func TestCASDedup(t *testing.T) {
	path := t.TempDir() + "/c2db-cas.img"
	c := mustCreateCAS(t, path)
	assertCASODirect(t, c)
	defer func() { _ = c.Close() }()

	raw := []byte("dedup-cas-same-buf")
	h1, err := c.Put(raw)
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	next := c.next
	if next == 0 {
		t.Fatalf("next LBA still 0 after Put")
	}
	h2, err := c.Put(raw)
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("dedup hash mismatch")
	}
	if c.next != next {
		t.Fatalf("dedup advanced next LBA: got %d want %d", c.next, next)
	}
	got, err := c.Get(h1)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("Get after dedup: err=%v val=%q", err, got)
	}
}

func TestCASGetUnknown(t *testing.T) {
	path := t.TempDir() + "/c2db-cas.img"
	c := mustCreateCAS(t, path)
	assertCASODirect(t, c)
	defer func() { _ = c.Close() }()

	var unknown [32]byte
	for i := range unknown {
		unknown[i] = byte(i + 1)
	}
	got, err := c.Get(unknown)
	if err == nil {
		t.Fatalf("Get unknown: expected error, got %q", got)
	}
	if !errors.Is(err, errCASNotFound) {
		t.Fatalf("Get unknown: got %v, want not found", err)
	}
	if got != nil {
		t.Fatalf("Get unknown: payload %q, want nil", got)
	}
}

func mustCreateCAS(t *testing.T, path string) *CAS {
	t.Helper()
	c, err := CreateCAS(path, casTestSize)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateCAS: %v", err)
	}
	return c
}

func mustOpenCAS(t *testing.T, path string) *CAS {
	t.Helper()
	c, err := OpenCAS(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("OpenCAS: %v", err)
	}
	return c
}

func assertCASODirect(t *testing.T, c *CAS) {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(c.dev.fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL: %v", err)
	}
	if flags&unix.O_DIRECT == 0 {
		t.Fatalf("CAS device fd lacks O_DIRECT (flags=%#x)", flags)
	}
}
