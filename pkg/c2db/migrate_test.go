// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"golang.org/x/sys/unix"
)

func TestMigrateMove(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 11)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	const from, to uint16 = 3, 7
	src, err := db.GetShard(from)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("GetShard(%d): %v", from, err)
	}
	k := []byte("migrate-key")
	v := []byte("migrate-val")
	if err := src.Put(k, v); err != nil {
		t.Fatalf("Put via GetShard(%d): %v", from, err)
	}

	if err := Migrate(db, from, to, key); err != nil {
		t.Fatalf("Migrate %d→%d: %v", from, to, err)
	}

	dst, err := db.GetShard(to)
	if err != nil {
		t.Fatalf("GetShard(%d): %v", to, err)
	}
	got, err := dst.Get(k)
	if err != nil || !bytes.Equal(got, v) {
		t.Fatalf("GetShard(%d) Get: err=%v val=%q", to, err, got)
	}

	closed, err := db.GetShard(from)
	if err != nil {
		t.Fatalf("GetShard(%d) après migration: %v", from, err)
	}
	if _, err := closed.Get(k); err == nil {
		t.Fatalf("GetShard(%d) encore lisible après migration", from)
	}

	j, err := OpenTopo(filepath.Join(dir, "topo.img"), key)
	if err != nil {
		t.Fatalf("OpenTopo: %v", err)
	}
	arts, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay topo: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close topo: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("artefacts topo=%d, want 1", len(arts))
	}
	if arts[0].Kind != byte(RecTopoTransition) {
		t.Fatalf("kind=%d, want %d", arts[0].Kind, RecTopoTransition)
	}
	var pair [4]byte
	binary.LittleEndian.PutUint16(pair[0:2], from)
	binary.LittleEndian.PutUint16(pair[2:4], to)
	wantRef := blake3archtsim.Sum256(pair[:])
	if arts[0].Ref != wantRef {
		t.Fatalf("ref=%x want %x", arts[0].Ref, wantRef)
	}
	var zeroID [16]byte
	if arts[0].ID == zeroID {
		t.Fatal("id NewID nul")
	}
}

func TestMigrateRejectSame(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	if err := Migrate(db, 3, 3, key); !errors.Is(err, errMigrateSame) {
		t.Fatalf("from==to: err=%v, want %v", err, errMigrateSame)
	}
}

func TestMigrateRejectOccupied(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 13)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	toDir := shardDir(dir, 7)
	if err := os.MkdirAll(toDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toDir, "data.img"), []byte("occupied"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	src, err := db.GetShard(3)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("GetShard(3): %v", err)
	}
	k := []byte("stay")
	if err := src.Put(k, []byte("here")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := Migrate(db, 3, 7, key); !errors.Is(err, errMigrateOccupied) {
		t.Fatalf("occupé: err=%v, want %v", err, errMigrateOccupied)
	}
	got, err := src.Get(k)
	if err != nil || !bytes.Equal(got, []byte("here")) {
		t.Fatalf("clé source après rejet: err=%v val=%q", err, got)
	}
}

func TestMigrateRejectBounds(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	if err := Migrate(db, NumShards, 1, key); err == nil {
		t.Fatal("from=1024: expected rejection")
	}
	if err := Migrate(db, 1, NumShards, key); err == nil {
		t.Fatal("to=1024: expected rejection")
	}
}
