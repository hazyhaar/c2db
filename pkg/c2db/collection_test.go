// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"testing"
)

func TestCollDisjointScans(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 11)
	}
	const shardID uint16 = 3

	s := mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	if err := s.CreateCollection("users"); err != nil {
		t.Fatalf("CreateCollection users: %v", err)
	}
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("CreateCollection orders: %v", err)
	}
	if err := s.CreateCollection("users"); !errors.Is(err, ErrCollectionExists) {
		t.Fatalf("CreateCollection users twice: %v", err)
	}

	if err := s.PutIn("users", []byte("u1"), []byte("alice")); err != nil {
		t.Fatalf("PutIn users u1: %v", err)
	}
	if err := s.PutIn("users", []byte("u2"), []byte("bob")); err != nil {
		t.Fatalf("PutIn users u2: %v", err)
	}

	if err := s.PutIn("orders", []byte("o1"), []byte("ord_100")); err != nil {
		t.Fatalf("PutIn orders o1: %v", err)
	}
	if err := s.PutIn("orders", []byte("o2"), []byte("ord_200")); err != nil {
		t.Fatalf("PutIn orders o2: %v", err)
	}

	scanUsers, err := s.Scan("users")
	if err != nil {
		t.Fatalf("Scan users: %v", err)
	}
	if len(scanUsers) != 2 {
		t.Fatalf("Scan users len=%d, want 2", len(scanUsers))
	}
	wantU1 := collectionKey("users", []byte("u1"))
	wantU2 := collectionKey("users", []byte("u2"))
	if !bytes.Equal(scanUsers[0], wantU1) || !bytes.Equal(scanUsers[1], wantU2) {
		t.Fatalf("Scan users got %q and %q, want %q and %q", scanUsers[0], scanUsers[1], wantU1, wantU2)
	}

	scanOrders, err := s.Scan("orders")
	if err != nil {
		t.Fatalf("Scan orders: %v", err)
	}
	if len(scanOrders) != 2 {
		t.Fatalf("Scan orders len=%d, want 2", len(scanOrders))
	}
	wantO1 := collectionKey("orders", []byte("o1"))
	wantO2 := collectionKey("orders", []byte("o2"))
	if !bytes.Equal(scanOrders[0], wantO1) || !bytes.Equal(scanOrders[1], wantO2) {
		t.Fatalf("Scan orders got %q and %q, want %q and %q", scanOrders[0], scanOrders[1], wantO1, wantO2)
	}

	// Verification of disjointness between the two collections
	for _, uKey := range scanUsers {
		for _, oKey := range scanOrders {
			if bytes.Equal(uKey, oKey) {
				t.Fatalf("Disjointness violated: key %q found in both collections", uKey)
			}
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCollPrefixCollisionSafety(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 19)
	}
	const shardID uint16 = 4

	s := mustOpenShard(t, dir, key, shardID)
	if err := s.CreateCollection("coll"); err != nil {
		t.Fatalf("CreateCollection coll: %v", err)
	}
	if err := s.CreateCollection("collection"); err != nil {
		t.Fatalf("CreateCollection collection: %v", err)
	}

	if err := s.PutIn("coll", []byte("k1"), []byte("val_short")); err != nil {
		t.Fatalf("PutIn coll k1: %v", err)
	}
	if err := s.PutIn("collection", []byte("k1"), []byte("val_long")); err != nil {
		t.Fatalf("PutIn collection k1: %v", err)
	}

	scanShort, err := s.Scan("coll")
	if err != nil {
		t.Fatalf("Scan coll: %v", err)
	}
	if len(scanShort) != 1 {
		t.Fatalf("Scan coll len=%d, want 1", len(scanShort))
	}
	if !bytes.Equal(scanShort[0], collectionKey("coll", []byte("k1"))) {
		t.Fatalf("Scan coll got %q", scanShort[0])
	}

	scanLong, err := s.Scan("collection")
	if err != nil {
		t.Fatalf("Scan collection: %v", err)
	}
	if len(scanLong) != 1 {
		t.Fatalf("Scan collection len=%d, want 1", len(scanLong))
	}
	if !bytes.Equal(scanLong[0], collectionKey("collection", []byte("k1"))) {
		t.Fatalf("Scan collection got %q", scanLong[0])
	}

	// Disjoint check
	if bytes.Equal(scanShort[0], scanLong[0]) {
		t.Fatalf("Collision detected between coll and collection")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCollLifecycleAndPersistence(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 29)
	}
	const shardID uint16 = 5

	s := mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	if err := s.PutIn("items", []byte("alpha"), []byte("val_alpha")); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("PutIn sans Create: %v", err)
	}
	if err := s.CreateCollection("items"); err != nil {
		t.Fatalf("CreateCollection items: %v", err)
	}
	names, err := s.ListCollections()
	if err != nil || len(names) != 1 || names[0] != "items" {
		t.Fatalf("ListCollections: err=%v %q", err, names)
	}

	if err := s.PutIn("items", []byte("alpha"), []byte("val_alpha")); err != nil {
		t.Fatalf("PutIn items alpha: %v", err)
	}
	if err := s.PutIn("items", []byte("beta"), []byte("val_beta")); err != nil {
		t.Fatalf("PutIn items beta: %v", err)
	}

	gotAlpha, err := s.GetIn("items", []byte("alpha"))
	if err != nil || !bytes.Equal(gotAlpha, []byte("val_alpha")) {
		t.Fatalf("GetIn items alpha: err=%v val=%q", err, gotAlpha)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen shard and verify Read-After-Write persistence
	s = mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	names, err = s.ListCollections()
	if err != nil || len(names) != 1 || names[0] != "items" {
		t.Fatalf("ListCollections persisté: err=%v %q", err, names)
	}

	gotAlpha, err = s.GetIn("items", []byte("alpha"))
	if err != nil || !bytes.Equal(gotAlpha, []byte("val_alpha")) {
		t.Fatalf("GetIn persisted alpha: err=%v val=%q", err, gotAlpha)
	}
	gotBeta, err := s.GetIn("items", []byte("beta"))
	if err != nil || !bytes.Equal(gotBeta, []byte("val_beta")) {
		t.Fatalf("GetIn persisted beta: err=%v val=%q", err, gotBeta)
	}

	scan, err := s.Scan("items")
	if err != nil || len(scan) != 2 {
		t.Fatalf("Scan persisted items: err=%v len=%d", err, len(scan))
	}

	// Strip collection prefix helper test
	uKey, ok := StripCollectionPrefix("items", scan[0])
	if !ok || !bytes.Equal(uKey, []byte("alpha")) {
		t.Fatalf("StripCollectionPrefix alpha got ok=%v key=%q", ok, uKey)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCollValidationAndRejection(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 31)
	}
	const shardID uint16 = 6

	s := mustOpenShard(t, dir, key, shardID)

	// Empty collection name
	if err := s.CreateCollection(""); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("CreateCollection empty name expected ErrInvalidCollectionName, got %v", err)
	}
	if err := s.DropCollection(""); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("DropCollection empty name expected ErrInvalidCollectionName, got %v", err)
	}
	if err := s.PutIn("", []byte("k"), []byte("v")); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("PutIn empty collection expected ErrInvalidCollectionName, got %v", err)
	}
	if _, err := s.GetIn("", []byte("k")); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("GetIn empty collection expected ErrInvalidCollectionName, got %v", err)
	}
	if _, err := s.Scan(""); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("Scan empty collection expected ErrInvalidCollectionName, got %v", err)
	}

	// Name containing separator byte
	badName := "bad\x1fname"
	if err := s.CreateCollection(badName); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("CreateCollection with separator expected ErrInvalidCollectionName, got %v", err)
	}
	if err := s.DropCollection(badName); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("DropCollection with separator expected ErrInvalidCollectionName, got %v", err)
	}
	if err := s.PutIn(badName, []byte("k"), []byte("v")); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("PutIn with separator expected ErrInvalidCollectionName, got %v", err)
	}
	if _, err := s.GetIn(badName, []byte("k")); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("GetIn with separator expected ErrInvalidCollectionName, got %v", err)
	}
	if _, err := s.Scan(badName); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("Scan with separator expected ErrInvalidCollectionName, got %v", err)
	}

	// Empty user key
	if err := s.PutIn("coll", nil, []byte("v")); !errors.Is(err, ErrEmptyCollectionKey) {
		t.Fatalf("PutIn nil key expected ErrEmptyCollectionKey, got %v", err)
	}
	if err := s.PutIn("coll", []byte{}, []byte("v")); !errors.Is(err, ErrEmptyCollectionKey) {
		t.Fatalf("PutIn empty slice key expected ErrEmptyCollectionKey, got %v", err)
	}
	if _, err := s.GetIn("coll", nil); !errors.Is(err, ErrEmptyCollectionKey) {
		t.Fatalf("GetIn nil key expected ErrEmptyCollectionKey, got %v", err)
	}

	if err := s.CreateCollection("coll"); err != nil {
		t.Fatalf("CreateCollection coll: %v", err)
	}
	empty, err := s.Scan("coll")
	if err != nil || len(empty) != 0 {
		t.Fatalf("Scan collection vide: err=%v len=%d", err, len(empty))
	}
	if err := s.PutIn("coll", []byte("valid"), []byte("v")); err != nil {
		t.Fatalf("PutIn valid after rejections: %v", err)
	}
	got, err := s.GetIn("coll", []byte("valid"))
	if err != nil || !bytes.Equal(got, []byte("v")) {
		t.Fatalf("GetIn valid after rejections: err=%v val=%q", err, got)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Operation on closed shard
	if err := s.CreateCollection("coll"); err == nil {
		t.Fatalf("CreateCollection on closed shard expected error")
	}
	if err := s.DropCollection("coll"); err == nil {
		t.Fatalf("DropCollection on closed shard expected error")
	}
	if err := s.PutIn("coll", []byte("k"), []byte("v")); err == nil {
		t.Fatalf("PutIn on closed shard expected error")
	}
	if _, err := s.GetIn("coll", []byte("k")); err == nil {
		t.Fatalf("GetIn on closed shard expected error")
	}
	if _, err := s.Scan("coll"); err == nil {
		t.Fatalf("Scan on closed shard expected error")
	}
}

func TestCollDrop(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 41)
	}
	const shardID uint16 = 7

	s := mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	// Drop non-existent collection returns ErrNoCollection
	if err := s.DropCollection("missing"); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("DropCollection missing expected ErrNoCollection, got %v", err)
	}

	if err := s.CreateCollection("users"); err != nil {
		t.Fatalf("CreateCollection users: %v", err)
	}
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("CreateCollection orders: %v", err)
	}
	if err := s.CreateCollection("settings"); err != nil {
		t.Fatalf("CreateCollection settings: %v", err)
	}

	if err := s.PutIn("users", []byte("u1"), []byte("alice")); err != nil {
		t.Fatalf("PutIn users u1: %v", err)
	}
	if err := s.PutIn("users", []byte("u2"), []byte("bob")); err != nil {
		t.Fatalf("PutIn users u2: %v", err)
	}
	if err := s.PutIn("orders", []byte("o100"), []byte("item1")); err != nil {
		t.Fatalf("PutIn orders o100: %v", err)
	}
	if err := s.PutIn("orders", []byte("o101"), []byte("item2")); err != nil {
		t.Fatalf("PutIn orders o101: %v", err)
	}
	if err := s.PutIn("settings", []byte("theme"), []byte("dark")); err != nil {
		t.Fatalf("PutIn settings theme: %v", err)
	}

	// Drop orders collection
	if err := s.DropCollection("orders"); err != nil {
		t.Fatalf("DropCollection orders: %v", err)
	}

	// Dropping again immediately returns ErrNoCollection
	if err := s.DropCollection("orders"); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("DropCollection orders twice expected ErrNoCollection, got %v", err)
	}

	// ListCollections should only contain settings and users
	names, err := s.ListCollections()
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("ListCollections len=%d, want 2, got %v", len(names), names)
	}
	for _, n := range names {
		if n == "orders" {
			t.Fatalf("ListCollections still contains dropped collection %q", n)
		}
	}

	// GetIn and Scan on dropped collection return ErrNoCollection
	if _, err := s.GetIn("orders", []byte("o100")); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("GetIn orders expected ErrNoCollection, got %v", err)
	}
	if _, err := s.Scan("orders"); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("Scan orders expected ErrNoCollection, got %v", err)
	}

	// Other collections remain intact
	gotU1, err := s.GetIn("users", []byte("u1"))
	if err != nil || !bytes.Equal(gotU1, []byte("alice")) {
		t.Fatalf("GetIn users u1: err=%v val=%q", err, gotU1)
	}
	gotU2, err := s.GetIn("users", []byte("u2"))
	if err != nil || !bytes.Equal(gotU2, []byte("bob")) {
		t.Fatalf("GetIn users u2: err=%v val=%q", err, gotU2)
	}
	gotTheme, err := s.GetIn("settings", []byte("theme"))
	if err != nil || !bytes.Equal(gotTheme, []byte("dark")) {
		t.Fatalf("GetIn settings theme: err=%v val=%q", err, gotTheme)
	}

	// Recreate orders and ensure clean state
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("Recreate orders: %v", err)
	}
	if err := s.PutIn("orders", []byte("o102"), []byte("item3")); err != nil {
		t.Fatalf("PutIn recreated orders o102: %v", err)
	}
	gotO102, err := s.GetIn("orders", []byte("o102"))
	if err != nil || !bytes.Equal(gotO102, []byte("item3")) {
		t.Fatalf("GetIn recreated orders o102: err=%v val=%q", err, gotO102)
	}
	// Old dropped keys must not resurface
	if _, err := s.GetIn("orders", []byte("o100")); !errors.Is(err, errNotFound) {
		t.Fatalf("GetIn old key o100 expected errNotFound, got %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCollDropPersist(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 51)
	}
	const shardID uint16 = 8

	s := mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	if err := s.CreateCollection("alpha"); err != nil {
		t.Fatalf("CreateCollection alpha: %v", err)
	}
	if err := s.CreateCollection("beta"); err != nil {
		t.Fatalf("CreateCollection beta: %v", err)
	}

	if err := s.PutIn("alpha", []byte("a1"), []byte("val_a1")); err != nil {
		t.Fatalf("PutIn alpha a1: %v", err)
	}
	if err := s.PutIn("alpha", []byte("a2"), []byte("val_a2")); err != nil {
		t.Fatalf("PutIn alpha a2: %v", err)
	}
	if err := s.PutIn("beta", []byte("b1"), []byte("val_b1")); err != nil {
		t.Fatalf("PutIn beta b1: %v", err)
	}

	// Drop alpha
	if err := s.DropCollection("alpha"); err != nil {
		t.Fatalf("DropCollection alpha: %v", err)
	}

	// Verify before closing
	if _, err := s.GetIn("alpha", []byte("a1")); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("GetIn alpha a1 before close expected ErrNoCollection, got %v", err)
	}

	// Close cleanly
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen shard and verify persistence of drop
	s = mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	names, err := s.ListCollections()
	if err != nil || len(names) != 1 || names[0] != "beta" {
		t.Fatalf("ListCollections after reopen: err=%v %q, want [beta]", err, names)
	}

	if _, err := s.GetIn("alpha", []byte("a1")); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("GetIn alpha a1 after reopen expected ErrNoCollection, got %v", err)
	}
	if _, err := s.GetIn("alpha", []byte("a2")); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("GetIn alpha a2 after reopen expected ErrNoCollection, got %v", err)
	}
	if _, err := s.Scan("alpha"); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("Scan alpha after reopen expected ErrNoCollection, got %v", err)
	}

	gotB1, err := s.GetIn("beta", []byte("b1"))
	if err != nil || !bytes.Equal(gotB1, []byte("val_b1")) {
		t.Fatalf("GetIn beta b1 after reopen: err=%v val=%q", err, gotB1)
	}

	// Crash / WAL redo recovery test on DropCollection
	if err := s.CreateCollection("gamma"); err != nil {
		t.Fatalf("CreateCollection gamma: %v", err)
	}
	if err := s.PutIn("gamma", []byte("g1"), []byte("val_g1")); err != nil {
		t.Fatalf("PutIn gamma g1: %v", err)
	}
	if err := s.PutIn("gamma", []byte("g2"), []byte("val_g2")); err != nil {
		t.Fatalf("PutIn gamma g2: %v", err)
	}
	if err := s.DropCollection("gamma"); err != nil {
		t.Fatalf("DropCollection gamma: %v", err)
	}

	// Simulate crash without pager flush
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	// Reopen after crash to verify WAL redo applied DropCollection
	s = mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	names, err = s.ListCollections()
	if err != nil || len(names) != 1 || names[0] != "beta" {
		t.Fatalf("ListCollections after crash recovery: err=%v %q, want [beta]", err, names)
	}

	if _, err := s.GetIn("gamma", []byte("g1")); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("GetIn gamma g1 after crash recovery expected ErrNoCollection, got %v", err)
	}
	if _, err := s.Scan("gamma"); !errors.Is(err, ErrNoCollection) {
		t.Fatalf("Scan gamma after crash recovery expected ErrNoCollection, got %v", err)
	}

	// Recreate gamma to verify full lifecycle recovery
	if err := s.CreateCollection("gamma"); err != nil {
		t.Fatalf("CreateCollection gamma after recovery: %v", err)
	}
	if err := s.PutIn("gamma", []byte("g3"), []byte("val_g3")); err != nil {
		t.Fatalf("PutIn gamma g3: %v", err)
	}
	gotG3, err := s.GetIn("gamma", []byte("g3"))
	if err != nil || !bytes.Equal(gotG3, []byte("val_g3")) {
		t.Fatalf("GetIn gamma g3: err=%v val=%q", err, gotG3)
	}
	if _, err := s.GetIn("gamma", []byte("g1")); !errors.Is(err, errNotFound) {
		t.Fatalf("GetIn old gamma g1 expected errNotFound, got %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close final: %v", err)
	}
}
