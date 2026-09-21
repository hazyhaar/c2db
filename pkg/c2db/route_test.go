// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNumShardsConst(t *testing.T) {
	if NumShards != 1024 {
		t.Fatalf("NumShards=%d, want 1024", NumShards)
	}
}

func TestRouteStable(t *testing.T) {
	k := []byte("c2db-route-stable")
	a := Route(k)
	if b := Route(k); a != b {
		t.Fatalf("Route instable: %d puis %d", a, b)
	}
	if a >= NumShards {
		t.Fatalf("Route=%d >= NumShards=%d", a, NumShards)
	}
	nonzero := false
	for i := 0; i < 256; i++ {
		s := Route([]byte{byte(i)})
		if s >= NumShards {
			t.Fatalf("clé %d: shard %d >= %d", i, s, NumShards)
		}
		if s != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		t.Fatal("256 clés toutes routées vers le fragment 0")
	}
}

func TestRouteCoverage(t *testing.T) {
	seen := make(map[uint16]struct{}, 64)
	var k [2]byte
	for i := 0; i < 4096; i++ {
		k[0] = byte(i >> 8)
		k[1] = byte(i)
		s := Route(k[:])
		if s >= NumShards {
			t.Fatalf("clé %d: shard %d >= %d", i, s, NumShards)
		}
		seen[s] = struct{}{}
	}
	if len(seen) < 16 {
		t.Fatalf("4096 clés n'ont touché que %d fragments, seuil 16", len(seen))
	}
}

func TestDBTwoShards(t *testing.T) {
	var a, b []byte
	var sa, sb uint16
	for i := 0; i < 1<<16; i++ {
		k := []byte{byte(i >> 8), byte(i)}
		s := Route(k)
		if a == nil {
			a = append([]byte(nil), k...)
			sa = s
			continue
		}
		if s != sa {
			b = append([]byte(nil), k...)
			sb = s
			break
		}
	}
	if b == nil {
		t.Fatal("aucune paire de clés sur deux fragments distincts")
	}
	if sa == sb {
		t.Fatalf("sa=%d sb=%d", sa, sb)
	}

	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 5)
	}
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := db.Put(a, []byte("va")); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Put a: %v", err)
	}
	if err := db.Put(b, []byte("vb")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if n := len(db.shards); n != 2 {
		t.Fatalf("fragments ouverts=%d, want 2", n)
	}
	gotA, err := db.Get(a)
	if err != nil || !bytes.Equal(gotA, []byte("va")) {
		t.Fatalf("Get a: err=%v val=%q", err, gotA)
	}
	gotB, err := db.Get(b)
	if err != nil || !bytes.Equal(gotB, []byte("vb")) {
		t.Fatalf("Get b: err=%v val=%q", err, gotB)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err = OpenDB(dir, key)
	if err != nil {
		t.Fatalf("ré-OpenDB: %v", err)
	}
	gotA, err = db.Get(a)
	if err != nil || !bytes.Equal(gotA, []byte("va")) {
		t.Fatalf("Get a persisté: err=%v val=%q", err, gotA)
	}
	gotB, err = db.Get(b)
	if err != nil || !bytes.Equal(gotB, []byte("vb")) {
		t.Fatalf("Get b persisté: err=%v val=%q", err, gotB)
	}
	if n := len(db.shards); n != 2 {
		t.Fatalf("ré-Open fragments ouverts=%d, want 2", n)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}

func TestDBScanPrefixMerge(t *testing.T) {
	var a, b []byte
	var sa, sb uint16
	for i := 0; i < 1<<16; i++ {
		k := []byte{byte(i >> 8), byte(i)}
		s := Route(k)
		if a == nil {
			a = append([]byte(nil), k...)
			sa = s
			continue
		}
		if s != sa {
			b = append([]byte(nil), k...)
			sb = s
			break
		}
	}
	if b == nil || sa == sb {
		t.Fatal("besoin de deux fragments")
	}
	dir := t.TempDir()
	var key [32]byte
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Put(a, []byte("va")); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté")
		}
		t.Fatalf("Put a: %v", err)
	}
	if err := db.Put(b, []byte("vb")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if _, err := db.ScanPrefix(nil, 0); !errors.Is(err, errQLLimitAbsente) {
		t.Fatalf("limit 0: %v", err)
	}
	keys, err := db.ScanPrefix(nil, 8)
	if err != nil {
		t.Fatalf("ScanPrefix: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("n=%d want 2", len(keys))
	}
	if bytes.Compare(keys[0], keys[1]) >= 0 {
		t.Fatalf("non trié: %x %x", keys[0], keys[1])
	}
	lim, err := db.ScanPrefix(nil, 1)
	if err != nil || len(lim) != 1 {
		t.Fatalf("limit 1: n=%d err=%v", len(lim), err)
	}
}

func TestOpenDBLazyShards(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if len(db.shards) != 0 {
		t.Fatalf("tiroirs ouverts à vide=%d", len(db.shards))
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	nDir := 0
	for _, e := range ents {
		if e.IsDir() {
			nDir++
		}
	}
	if nDir != 0 {
		t.Fatalf("sous-dossiers à l'ouverture=%d want 0", nDir)
	}
	k := []byte("lazy-one")
	if err := db.Put(k, []byte("v")); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("Put: %v", err)
	}
	if len(db.shards) != 1 {
		t.Fatalf("après un Put: %d tiroirs", len(db.shards))
	}
	ents, err = os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir2: %v", err)
	}
	nDir = 0
	for _, e := range ents {
		if e.IsDir() {
			nDir++
		}
	}
	if nDir != 1 {
		t.Fatalf("dossiers après Put=%d want 1 (pas 1024)", nDir)
	}
	id := Route(k)
	st, err := os.Stat(filepath.Join(dir, filepath.Base(shardDir(dir, id)), "data.img"))
	if err != nil {
		t.Fatalf("data.img: %v", err)
	}
	if uint64(st.Size()) > maxShardFile {
		t.Fatalf("taille data=%d > maxShardFile", st.Size())
	}
	if uint64(st.Size()) == 1<<30 {
		t.Fatal("mmap/fichier 1 Gio pour un tiroir")
	}
}
