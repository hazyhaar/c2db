// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteVerifyFixtures(t *testing.T) {
	fixtureDir := filepath.Join("testdata")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	archPath := filepath.Join(fixtureDir, "verify_ok.arch")

	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}

	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "wal.img")

	w, err := CreateWAL(walPath, testImageSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	rec := Record{
		Type:    RecPut,
		Payload: []byte("c2db-verify-fixture-data"),
	}
	copy(rec.ID[:], []byte("fixture-rec-0001"))
	if err := w.Append(rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := w.CompactTo(archPath); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if err := VerifyArchive(archPath, key); err != nil {
		t.Fatalf("VerifyArchive: %v", err)
	}
}
