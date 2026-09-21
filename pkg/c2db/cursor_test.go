// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"golang.org/x/sys/unix"
)

func mustOpenShardTB(tb testing.TB, dir string, key [32]byte, id uint16) *Shard {
	tb.Helper()
	s, err := OpenShard(dir, key, id)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			tb.Skip("O_DIRECT non supporté par le système de fichiers")
		}
		tb.Fatalf("OpenShard: %v", err)
	}
	return s
}

func TestCursor_Empty(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	s := mustOpenShardTB(t, dir, key, 1)
	defer s.Close()

	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("s.Cursor: %v", err)
	}
	defer c.Close()

	if k, v, ok := c.First(); ok || k != nil || v != nil {
		t.Fatalf("First on empty: got ok=%v k=%v v=%v", ok, k, v)
	}
	if k, v, ok := c.Last(); ok || k != nil || v != nil {
		t.Fatalf("Last on empty: got ok=%v k=%v v=%v", ok, k, v)
	}
	if k, v, ok := c.Seek([]byte("test")); ok || k != nil || v != nil {
		t.Fatalf("Seek on empty: got ok=%v k=%v v=%v", ok, k, v)
	}
	if k, v, ok := c.Next(); ok || k != nil || v != nil {
		t.Fatalf("Next on empty: got ok=%v k=%v v=%v", ok, k, v)
	}
	if k, v, ok := c.Prev(); ok || k != nil || v != nil {
		t.Fatalf("Prev on empty: got ok=%v k=%v v=%v", ok, k, v)
	}
	if c.Valid() {
		t.Fatalf("Valid on empty cursor returned true")
	}
}

func TestCursor_BasicForwardBackward(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 2)
	}
	s := mustOpenShardTB(t, dir, key, 2)
	defer s.Close()

	const n = 100
	expected := make(map[string]string)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key_%04d", i)
		v := fmt.Sprintf("val_%04d", i)
		expected[k] = v
		if err := s.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}

	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("s.Cursor: %v", err)
	}
	defer c.Close()

	// 1. Parcours avant (First -> Next)
	countFwd := 0
	for k, v, ok := c.First(); ok; k, v, ok = c.Next() {
		sk := string(k)
		sv := string(v)
		wantV, exists := expected[sk]
		if !exists {
			t.Fatalf("Fwd: unexpected key %q", sk)
		}
		if sv != wantV {
			t.Fatalf("Fwd key %q: got %q, want %q", sk, sv, wantV)
		}
		expectedKey := fmt.Sprintf("key_%04d", countFwd)
		if sk != expectedKey {
			t.Fatalf("Fwd order mismatch at %d: got %q, want %q", countFwd, sk, expectedKey)
		}
		countFwd++
	}
	if countFwd != n {
		t.Fatalf("Fwd: count=%d, want %d", countFwd, n)
	}

	// 2. Parcours arrière (Last -> Prev)
	countBwd := 0
	for k, v, ok := c.Last(); ok; k, v, ok = c.Prev() {
		sk := string(k)
		sv := string(v)
		wantV, exists := expected[sk]
		if !exists {
			t.Fatalf("Bwd: unexpected key %q", sk)
		}
		if sv != wantV {
			t.Fatalf("Bwd key %q: got %q, want %q", sk, sv, wantV)
		}
		expectedKey := fmt.Sprintf("key_%04d", n-1-countBwd)
		if sk != expectedKey {
			t.Fatalf("Bwd order mismatch at %d: got %q, want %q", countBwd, sk, expectedKey)
		}
		countBwd++
	}
	if countBwd != n {
		t.Fatalf("Bwd: count=%d, want %d", countBwd, n)
	}

	// 3. Va-et-vient (Next / Prev alternés)
	k, _, ok := c.First()
	if !ok || string(k) != "key_0000" {
		t.Fatalf("Va-et-vient init First failed: %q", string(k))
	}
	k, _, ok = c.Next()
	if !ok || string(k) != "key_0001" {
		t.Fatalf("Next -> key_0001 failed: %q", string(k))
	}
	k, _, ok = c.Next()
	if !ok || string(k) != "key_0002" {
		t.Fatalf("Next -> key_0002 failed: %q", string(k))
	}
	k, _, ok = c.Prev()
	if !ok || string(k) != "key_0001" {
		t.Fatalf("Prev -> key_0001 failed: %q", string(k))
	}
	k, _, ok = c.Prev()
	if !ok || string(k) != "key_0000" {
		t.Fatalf("Prev -> key_0000 failed: %q", string(k))
	}
	_, _, ok = c.Prev()
	if ok {
		t.Fatalf("Prev past start should return ok=false")
	}
}

func TestCursor_MultiPageSplit(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 3)
	}
	s := mustOpenShardTB(t, dir, key, 3)
	defer s.Close()

	// 500 clés avec valeurs de 400 octets -> force l'éclatement sur de nombreuses pages
	const total = 500
	payload := make([]byte, 400)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	for i := 0; i < total; i++ {
		k := []byte(fmt.Sprintf("split_item_%05d", i))
		if err := s.Put(k, payload); err != nil {
			t.Fatalf("Put %s: %v", string(k), err)
		}
	}

	if s.heapUsed < 5 {
		t.Fatalf("heapUsed=%d, attendu >= 5 pour valider le multi-pages", s.heapUsed)
	}

	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("s.Cursor: %v", err)
	}
	defer c.Close()

	// Vérification du nombre de feuilles
	if len(c.leafPages) < 4 {
		t.Fatalf("leafPages count=%d, attendu >= 4", len(c.leafPages))
	}

	// Parcours complet en avant
	seen := 0
	for k, v, ok := c.First(); ok; k, v, ok = c.Next() {
		wantK := fmt.Sprintf("split_item_%05d", seen)
		if string(k) != wantK {
			t.Fatalf("Fwd key mismatch at index %d: got %q, want %q", seen, string(k), wantK)
		}
		if !bytes.Equal(v, payload) {
			t.Fatalf("Fwd payload mismatch at %d", seen)
		}
		seen++
	}
	if seen != total {
		t.Fatalf("Fwd: traversed %d items, want %d", seen, total)
	}

	// Parcours complet en arrière
	seenBwd := 0
	for k, v, ok := c.Last(); ok; k, v, ok = c.Prev() {
		wantK := fmt.Sprintf("split_item_%05d", total-1-seenBwd)
		if string(k) != wantK {
			t.Fatalf("Bwd key mismatch at index %d: got %q, want %q", seenBwd, string(k), wantK)
		}
		if !bytes.Equal(v, payload) {
			t.Fatalf("Bwd payload mismatch at %d", seenBwd)
		}
		seenBwd++
	}
	if seenBwd != total {
		t.Fatalf("Bwd: traversed %d items, want %d", seenBwd, total)
	}
}

func TestCursor_SeekExactAndBetween(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 4)
	}
	s := mustOpenShardTB(t, dir, key, 4)
	defer s.Close()

	// Insérer les clés paires uniquement : 00, 02, 04, ..., 98
	for i := 0; i < 100; i += 2 {
		k := []byte(fmt.Sprintf("k_%02d", i))
		v := []byte(fmt.Sprintf("v_%02d", i))
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %s: %v", string(k), err)
		}
	}

	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("s.Cursor: %v", err)
	}
	defer c.Close()

	// 1. Seek exact sur clé existante
	k, v, ok := c.Seek([]byte("k_40"))
	if !ok || string(k) != "k_40" || string(v) != "v_40" {
		t.Fatalf("Seek exact k_40: got ok=%v k=%q v=%q", ok, string(k), string(v))
	}

	// 2. Seek intermédiaire (clé impaire inexistante k_41 -> doit atterrir sur k_42)
	k, v, ok = c.Seek([]byte("k_41"))
	if !ok || string(k) != "k_42" || string(v) != "v_42" {
		t.Fatalf("Seek between k_41: got ok=%v k=%q v=%q", ok, string(k), string(v))
	}

	// 3. Seek inférieur à toutes les clés
	k, v, ok = c.Seek([]byte("a_before"))
	if !ok || string(k) != "k_00" || string(v) != "v_00" {
		t.Fatalf("Seek before: got ok=%v k=%q v=%q", ok, string(k), string(v))
	}

	// 4. Seek supérieur à toutes les clés
	k, v, ok = c.Seek([]byte("z_after"))
	if ok || k != nil || v != nil {
		t.Fatalf("Seek after: expected ok=false, got ok=%v k=%q", ok, string(k))
	}
}

func TestCursor_MVCC_TombstonesAndVersions(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 5)
	}
	s := mustOpenShardTB(t, dir, key, 5)
	defer s.Close()

	// 1. Clé mise à jour plusieurs fois
	if err := s.Put([]byte("item_a"), []byte("val_a_v1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("item_a"), []byte("val_a_v2")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("item_a"), []byte("val_a_v3")); err != nil {
		t.Fatal(err)
	}

	// 2. Clé insérée puis supprimée
	if err := s.Put([]byte("item_b"), []byte("val_b_v1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete([]byte("item_b")); err != nil {
		t.Fatal(err)
	}

	// 3. Clé simple active
	if err := s.Put([]byte("item_c"), []byte("val_c_v1")); err != nil {
		t.Fatal(err)
	}

	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("s.Cursor: %v", err)
	}
	defer c.Close()

	// Itération complète : on ne doit voir QUE item_a (v3) et item_c (v1) ! item_b doit être invisible.
	var keys []string
	var vals []string
	for k, v, ok := c.First(); ok; k, v, ok = c.Next() {
		keys = append(keys, string(k))
		vals = append(vals, string(v))
	}

	if len(keys) != 2 {
		t.Fatalf("Expected exactly 2 active keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "item_a" || vals[0] != "val_a_v3" {
		t.Fatalf("item_a mismatch: k=%q v=%q", keys[0], vals[0])
	}
	if keys[1] != "item_c" || vals[1] != "val_c_v1" {
		t.Fatalf("item_c mismatch: k=%q v=%q", keys[1], vals[1])
	}

	// Test Seek sur le tombstone item_b -> doit sauter à item_c
	sk, sv, ok := c.Seek([]byte("item_b"))
	if !ok || string(sk) != "item_c" || string(sv) != "val_c_v1" {
		t.Fatalf("Seek tombstone item_b: got ok=%v k=%q v=%q", ok, string(sk), string(sv))
	}
}

func TestCursor_Range(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 6)
	}
	s := mustOpenShardTB(t, dir, key, 6)
	defer s.Close()

	for i := 0; i < 50; i++ {
		k := fmt.Sprintf("range_%03d", i)
		v := fmt.Sprintf("v_%03d", i)
		if err := s.Put([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Plage bornée [range_010, range_025[
	var collected []string
	err := s.Range([]byte("range_010"), []byte("range_025"), func(k, v []byte) bool {
		collected = append(collected, string(k))
		return true
	})
	if err != nil {
		t.Fatalf("s.Range: %v", err)
	}
	if len(collected) != 15 {
		t.Fatalf("Range [10, 25[: expected 15 items, got %d", len(collected))
	}
	if collected[0] != "range_010" || collected[len(collected)-1] != "range_024" {
		t.Fatalf("Range bounds mismatch: first=%q last=%q", collected[0], collected[len(collected)-1])
	}

	// 2. Plage avec arrêt anticipé à 5 éléments
	stopCount := 0
	err = s.Range([]byte("range_000"), nil, func(k, v []byte) bool {
		stopCount++
		return stopCount < 5
	})
	if err != nil {
		t.Fatalf("s.Range early stop: %v", err)
	}
	if stopCount != 5 {
		t.Fatalf("Early stop: expected 5 iterations, got %d", stopCount)
	}
}

func TestCursor_ZeroAlloc(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 7)
	}
	s := mustOpenShardTB(t, dir, key, 7)
	defer s.Close()

	for i := 0; i < 200; i++ {
		k := fmt.Sprintf("alloc_k_%04d", i)
		v := fmt.Sprintf("alloc_v_%04d", i)
		if err := s.Put([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}

	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("s.Cursor: %v", err)
	}
	defer c.Close()

	// Mesure des allocations sur Next()
	c.First()
	allocsNext := testing.AllocsPerRun(100, func() {
		_, _, ok := c.Next()
		if !ok {
			c.First()
		}
	})
	if allocsNext != 0 {
		t.Fatalf("c.Next() allocs = %f, want 0.0", allocsNext)
	}

	// Mesure des allocations sur Prev()
	c.Last()
	allocsPrev := testing.AllocsPerRun(100, func() {
		_, _, ok := c.Prev()
		if !ok {
			c.Last()
		}
	})
	if allocsPrev != 0 {
		t.Fatalf("c.Prev() allocs = %f, want 0.0", allocsPrev)
	}
}

func BenchmarkCursor_StreamingScan(b *testing.B) {
	dir := b.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 8)
	}
	s := mustOpenShardTB(b, dir, key, 8)
	defer s.Close()

	const items = 5000
	for i := 0; i < items; i++ {
		k := fmt.Sprintf("bench_%06d", i)
		v := fmt.Sprintf("val_%06d", i)
		if err := s.Put([]byte(k), []byte(v)); err != nil {
			b.Fatal(err)
		}
	}

	c, err := s.Cursor()
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, ok := c.Next()
		if !ok {
			c.First()
		}
	}
}
