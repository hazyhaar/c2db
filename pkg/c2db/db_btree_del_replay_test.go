// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// TestDbBtree_DelReplayLinearInN écrit N suppressions dans le journal, ferme le
// shard sans vider le magasin de pages (les suppressions ne sont pas durables),
// puis rouvre : le rejeu réapplique les suppressions sur l'état pointé des
// insertions. Le compteur d'observabilité btDelCowPages cumule les pages
// réellement copiées sur écriture par le noyau de suppression. Tant que le
// noyau suit la chaîne de feuilles, chaque suppression parcourt toutes les
// feuilles restantes et le cumul croît en N x feuilles ; après correctif, la
// descente s'arrête à la feuille cible et le cumul est linéaire en N.
func TestDbBtree_DelReplayLinearInN(t *testing.T) {
	dir := t.TempDir()
	var mk [32]byte
	for i := range mk {
		mk[i] = byte(i + 0x47)
	}
	const pages = 4096
	const nkeys = 60
	const ndel = 30
	opts := []Option{WithHeapPages(pages), WithWALBytes(8 * 1024 * 1024)}

	val := make([]byte, 2000)
	copy(val, "VALEUR-REJEU-C2DB")
	keys := make([][]byte, 0, nkeys)
	for i := 0; i < nkeys; i++ {
		keys = append(keys, []byte(fmt.Sprintf("del-replay-key-%04d", i)))
	}

	// Session 1 : insertions durables.
	s1 := mustOpenShard(t, dir, mk, 3, opts...)
	for i, k := range keys {
		if err := s1.Put(k, val); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close session 1: %v", err)
	}

	// Session 2 : suppressions non durables, pour forcer leur rejeu.
	s2 := mustOpenShard(t, dir, mk, 3, opts...)
	c, err := s2.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	leaves := len(c.leafPages)
	_ = c.Close()
	for i := 0; i < ndel; i++ {
		if err := s2.Delete(keys[i]); err != nil {
			t.Fatalf("Delete %d: %v", i, err)
		}
	}
	if s2.wal != nil {
		if err := s2.wal.Flush(); err != nil {
			t.Fatalf("flush WAL session 2: %v", err)
		}
	}
	if err := s2.KillWithoutFlush(); err != nil {
		t.Fatalf("KillWithoutFlush session 2: %v", err)
	}
	if leaves < 4 {
		t.Fatalf("arbre insuffisant pour éprouver la chaîne : %d feuilles", leaves)
	}

	// Session 3 : rejeu des suppressions sur l'état pointé des insertions.
	s3 := mustOpenShard(t, dir, mk, 3, opts...)
	defer func() { _ = s3.Close() }()
	observed := s3.btDelCowPages.Load()
	bound := uint64(ndel*3 + 16)
	t.Logf("rejeu: feuilles=%d ndel=%d pages_cow_par_suppression=%d borne=%d", leaves, ndel, observed, bound)
	if observed > bound {
		t.Fatalf("rejeu des suppressions non linéaire : %d pages copiées pour %d suppressions (borne %d, feuilles %d) : la chaîne est suivie", observed, ndel, bound, leaves)
	}
	if observed == 0 {
		t.Fatalf("aucune suppression rejouée : le banc ne mesure rien")
	}

	// Oracle de correction : les clés supprimées sont absentes, les autres
	// restent présentes et lisibles.
	for i := 0; i < ndel; i++ {
		if _, err := s3.Get(keys[i]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("clé %d présente après rejeu : err=%v", i, err)
		}
	}
	for i := ndel; i < nkeys; i++ {
		got, err := s3.Get(keys[i])
		if err != nil {
			t.Fatalf("clé %d perdue après rejeu : err=%v", i, err)
		}
		if !bytes.Equal(got, val) {
			t.Fatalf("clé %d valeur divergente après rejeu", i)
		}
	}

	// Cohérence temporelle : au snapshot maximal, GetAsOf rend le même verdict
	// que Get, pour les clés supprimées comme pour les clés conservées.
	var maxSnap [16]byte
	for i := range maxSnap {
		maxSnap[i] = 0xFF
	}
	for i := 0; i < ndel; i++ {
		if _, err := s3.GetAsOf(keys[i], maxSnap); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetAsOf clé %d ressuscite la clé supprimée : err=%v", i, err)
		}
	}
	for i := ndel; i < nkeys; i++ {
		got, err := s3.GetAsOf(keys[i], maxSnap)
		if err != nil || !bytes.Equal(got, val) {
			t.Fatalf("GetAsOf clé %d incohérent : err=%v", i, err)
		}
	}
}
