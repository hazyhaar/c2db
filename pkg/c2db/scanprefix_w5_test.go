// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"fmt"
	"testing"
)

func countCursorKeys(t *testing.T, s *Shard) int {
	t.Helper()
	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	defer func() { _ = c.Close() }()
	n := 0
	for _, _, ok := c.First(); ok; _, _, ok = c.Next() {
		n++
	}
	return n
}

// TestScanPrefixLargeResultSet mord sur le plafond fixe de 4096 correspondances
// de scanPrefixHeap (tampon 16 Kio) : l'appelant Go sous-dimensionne la sortie
// et traduit l'insuffisance du noyau en errInsert.
func TestScanPrefixLargeResultSet(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 91)
	}
	s := mustOpenShard(t, dir, key, 21)
	defer func() { _ = s.Close() }()

	const n = 6000
	pairs := make([][2][]byte, n)
	for i := 0; i < n; i++ {
		pairs[i] = [2][]byte{
			[]byte(fmt.Sprintf("docids/%06d", i)),
			[]byte(fmt.Sprintf("v%06d", i)),
		}
	}
	if err := s.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	got, err := s.ScanPrefix([]byte("docids/"))
	if err != nil {
		t.Fatalf("ScanPrefix: %v", err)
	}
	if len(got) != n {
		t.Fatalf("ScanPrefix len=%d want %d", len(got), n)
	}
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("docids/%06d", i)
		if string(got[i]) != want {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want)
		}
	}
}

// TestScanPrefixThreeLevelPage0 mord sur la collision de la sentinelle zéro
// avec la page 0 sur un nœud interne : clés longues insérées en ordre
// décroissant pour forcer un arbre à trois niveaux dont le nœud interne
// gauche désigne la page 0 comme enfant gauche.
func TestScanPrefixThreeLevelPage0(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 97)
	}
	s := mustOpenShard(t, dir, key, 22)
	defer func() { _ = s.Close() }()

	const n = 5000
	const klen = 254
	for i := n - 1; i >= 0; i-- {
		k := make([]byte, klen)
		copy(k, fmt.Sprintf("docids/long-%06d", i))
		for j := 20; j < len(k); j++ {
			k[j] = byte('a' + (i+j)%26)
		}
		if err := s.Put(k, []byte("v")); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	curN := countCursorKeys(t, s)
	got, err := s.ScanPrefix([]byte("docids/"))
	if err != nil {
		t.Fatalf("ScanPrefix: %v", err)
	}
	if curN != n || len(got) != n {
		t.Fatalf("cursor=%d ScanPrefix=%d want %d", curN, len(got), n)
	}
}

// TestScanPrefixMultipleVersions mord sur la même collision de sentinelle,
// épaissie par la multiplicité des versions d'une même clé.
func TestScanPrefixMultipleVersions(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 103)
	}
	s := mustOpenShard(t, dir, key, 23)
	defer func() { _ = s.Close() }()

	const n = 3000
	for v := 0; v < 2; v++ {
		for i := 0; i < n; i++ {
			k := []byte(fmt.Sprintf("docids/ver-%06d", i))
			if err := s.Put(k, []byte(fmt.Sprintf("payload-%d-%06d", v, i))); err != nil {
				t.Fatalf("Put v%d %d: %v", v, i, err)
			}
		}
	}
	got, err := s.ScanPrefix([]byte("docids/"))
	if err != nil {
		t.Fatalf("ScanPrefix: %v", err)
	}
	if len(got) != n {
		t.Fatalf("ScanPrefix len=%d want %d", len(got), n)
	}
}
