package c2db

import (
	"bytes"
	"fmt"
	"testing"
)

// countKeyCells comptabilise les cellules brutes portant une clé donnée dans
// les pages feuilles, sans passer par Cursor.First/Next qui déduplique les
// versions (resolveKeyAt). Il lit directement leafPages + readSlotCell, comme
// rebuildAllVersionsLocked, ce qui restitue TOUTE version résiduelle.
func countKeyCells(t *testing.T, s *Shard, key []byte) int {
	t.Helper()
	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	defer func() { _ = c.Close() }()
	n := 0
	for li := 0; li < len(c.leafPages); li++ {
		for si := 0; si < c.leafSlotCount(li); si++ {
			k, _, _, _, ok := c.readSlotCell(li, si)
			if ok && bytes.Equal(k, key) {
				n++
			}
		}
	}
	return n
}

// TestReplayIdempotenceNoBloat mord sur le gonflement du rejeu (W1). Une clé
// réécrite trois fois porte trois versions dans le tas pointé. Sans la garde
// d'idempotence par identifiant de version, la réouverture réapplique les
// enregistrements antérieurs (l'égalité de valeur échoue puisque la valeur
// courante est la dernière) et duplique l'historique. Le test compare le
// nombre de cellules brutes de la clé avant et après réouverture, mesure
// directe qui ne dépend pas du volume du tas ni du seuil de rejet d'insertion.
func TestReplayIdempotenceNoBloat(t *testing.T) {
	dir := t.TempDir()
	var mk [32]byte
	for i := range mk {
		mk[i] = byte(i + 0x31)
	}
	const pages = 4096
	opts := []Option{WithHeapPages(pages), WithWALBytes(4 * 1024 * 1024)}

	s := mustOpenShard(t, dir, mk, 3, opts...)
	key := []byte("w1-replay-idempotence")
	const versions = 3
	for i := 1; i <= versions; i++ {
		if err := s.Put(key, []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("Put v%d: %v", i, err)
		}
	}
	before := countKeyCells(t, s, key)
	if before != versions {
		t.Fatalf("precondition: %d cellules brutes pour la cle, attendu %d", before, versions)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2 := mustOpenShard(t, dir, mk, 3, opts...)
	defer func() { _ = s2.Close() }()
	after := countKeyCells(t, s2, key)
	t.Logf("cellules brutes de la cle: avant reouverture=%d apres=%d", before, after)
	if after != before {
		t.Fatalf("W1: le rejeu a duplique l'historique: %d cellules apres reouverture, attendu %d "+
			"(gonflement du tas par reapplication non idempotente)", after, before)
	}
}

// TestReplayIdempotenceTransactionalNoBloat couvre la même garde sur le chemin
// transactionnel explicite (Begin/Put/Commit). Le rejeu d'un préfixe déjà
// durable doit sauter les versions dont l'identifiant est inférieur ou égal à
// celui déjà présent dans le tas, sinon l'historique est dupliqué et le repack
// de rejeu peut perdre des clés (course observée sous GOMAXPROCS=1).
func TestReplayIdempotenceTransactionalNoBloat(t *testing.T) {
	dir := t.TempDir()
	var mk [32]byte
	for i := range mk {
		mk[i] = byte(i + 0x52)
	}
	const pages = 4096
	opts := []Option{WithHeapPages(pages), WithWALBytes(4 * 1024 * 1024)}

	s := mustOpenShard(t, dir, mk, 4, opts...)
	key := []byte("w1-replay-idempotence-tx")
	const versions = 3
	for i := 1; i <= versions; i++ {
		tx, err := s.Begin()
		if err != nil {
			t.Fatalf("Begin v%d: %v", i, err)
		}
		if err := tx.Put(key, []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("Put v%d: %v", i, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit v%d: %v", i, err)
		}
	}
	before := countKeyCells(t, s, key)
	if before != versions {
		t.Fatalf("precondition: %d cellules brutes pour la cle, attendu %d", before, versions)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2 := mustOpenShard(t, dir, mk, 4, opts...)
	defer func() { _ = s2.Close() }()
	after := countKeyCells(t, s2, key)
	t.Logf("cellules brutes transactionnelles: avant reouverture=%d apres=%d", before, after)
	if after != before {
		t.Fatalf("le rejeu transactionnel a duplique l'historique: %d cellules apres reouverture, attendu %d "+
			"(reapplication non idempotente des groups RecPut)", after, before)
	}
}
