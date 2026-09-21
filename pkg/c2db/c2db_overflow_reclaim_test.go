// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"testing"
)

// ovfValue fabrique une charge utile strictement supérieure au seuil inline
// (InlineCutoff = 2048), de façon déterministe et sans tirage pseudo-aléatoire.
func ovfValue(n int, tag byte) []byte {
	v := make([]byte, n)
	for i := range v {
		v[i] = byte('A' + (i+int(tag))%26)
	}
	v[0] = tag
	return v
}

// orphanOverflowPages relève, par reachability depuis la racine, les pages de
// type débordement (3) non atteintes. Le parcours reproduit markActivePages :
// une page de type 3 hors masque est une chaîne orpheline.
func orphanOverflowPages(s *Shard) []uint64 {
	mask := make([]uint64, len(s.dirtyMask))
	s.markActivePages(mask)
	src := s.pub
	if uint64(len(src)) < s.shardBytes {
		src = s.dirty
	}
	var orphans []uint64
	for pg := uint64(1); pg < s.heapUsed; pg++ {
		off := pg * pageN
		if off+pageN > uint64(len(src)) {
			break
		}
		if src[off+BT_TypeOffset] != TypeOverflow {
			continue
		}
		w := pg / 64
		b := pg % 64
		if w >= uint64(len(mask)) || mask[w]&(uint64(1)<<b) == 0 {
			orphans = append(orphans, pg)
		}
	}
	return orphans
}

// countTypeOverflow compte les pages de type débordement allouées dans [1, heapUsed).
func countTypeOverflow(s *Shard) int {
	src := s.pub
	if uint64(len(src)) < s.shardBytes {
		src = s.dirty
	}
	n := 0
	for pg := uint64(1); pg < s.heapUsed; pg++ {
		off := pg * pageN
		if off+pageN > uint64(len(src)) {
			break
		}
		if src[off+BT_TypeOffset] == TypeOverflow {
			n++
		}
	}
	return n
}

// TestOverflowChainReclaimedOnDeleteQueue prouve qu'une valeur à débordement
// supprimée ne laisse ni page de type 3 orpheline ni occupation résiduelle :
// la chaîne occupe la queue du tas, elle est donc réclamée immédiatement
// (heapUsed revient à 1) et la reachability depuis la racine est close.
func TestOverflowChainReclaimedOnDeleteQueue(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{0x51, 0x52, 0x53}
	s := mustOpenShard(t, dir, key, 0)
	defer s.Close()

	const valLen = 40000 // ceil(40000/16320) = 3 pages de débordement
	val := ovfValue(valLen, 'Q')
	k := []byte("ovf-queue-key")
	if err := s.Put(k, val); err != nil {
		t.Fatalf("Put: %v", err)
	}
	usedAfterPut := s.heapUsed
	chainPages := countTypeOverflow(s)
	if chainPages == 0 {
		t.Fatalf("aucune page de débordement après Put (heapUsed=%d)", usedAfterPut)
	}
	t.Logf("après Put: heapUsed=%d pagesOverflow=%d", usedAfterPut, chainPages)

	if err := s.Delete(k); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	usedAfterDelete := s.heapUsed
	orphans := orphanOverflowPages(s)
	t.Logf("après Delete: heapUsed=%d (avant=%d) orphelines=%v", usedAfterDelete, usedAfterPut, orphans)

	if len(orphans) != 0 {
		t.Fatalf("pages de type 3 orphelines après suppression: %v", orphans)
	}
	if usedAfterDelete >= usedAfterPut {
		t.Fatalf("chaîne de queue non réclamée: heapUsed avant=%d après=%d", usedAfterPut, usedAfterDelete)
	}
	if _, err := s.Get(k); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clé supprimée encore lisible: err=%v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2 := mustOpenShard(t, dir, key, 0)
	defer s2.Close()
	if s2.heapUsed != usedAfterDelete {
		t.Fatalf("heapUsed non persisté: got=%d want=%d", s2.heapUsed, usedAfterDelete)
	}
	if _, err := s2.Get(k); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clé ressuscitée à la réouverture: err=%v", err)
	}
	if o := orphanOverflowPages(s2); len(o) != 0 {
		t.Fatalf("orphelines après réouverture: %v", o)
	}
}

// TestOverflowChainReclaimedAcrossReplay vérifie que la libération d'une chaîne
// de débordement survit au rejeu : la chaîne est pointée dans le fichier, la
// suppression reste dans le journal, et la réouverture rejoue la suppression
// sans laisser de page de type 3 orpheline.
func TestOverflowChainReclaimedAcrossReplay(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{0x71, 0x72, 0x73}
	s := mustOpenShard(t, dir, key, 0)

	val := ovfValue(50000, 'R')
	k := []byte("ovf-replay-key")
	if err := s.Put(k, val); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := s.Delete(k); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	usedAfterDelete := s.heapUsed
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2 := mustOpenShard(t, dir, key, 0)
	defer s2.Close()
	if _, err := s2.Get(k); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clé ressuscitée après rejeu: err=%v", err)
	}
	if o := orphanOverflowPages(s2); len(o) != 0 {
		t.Fatalf("orphelines après rejeu: %v", o)
	}
	if s2.heapUsed > usedAfterDelete {
		t.Fatalf("heapUsed après rejeu=%d > après suppression=%d", s2.heapUsed, usedAfterDelete)
	}
}

// TestOverflowChainConsignedWhenNotQueue prouve que la chaîne d'une valeur
// supprimée qui n'occupe pas la queue du tas (une autre valeur vit au-dessus)
// est consignée morte : les pages deviennent de type 0, aucune page de type 3
// orpheline ne subsiste, et le repack ultérieur récupère le tout sans perte.
func TestOverflowChainConsignedWhenNotQueue(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{0x61, 0x62, 0x63}
	s := mustOpenShard(t, dir, key, 0)
	defer s.Close()

	valA := ovfValue(40000, 'A')
	valB := ovfValue(40000, 'B')
	kA := []byte("ovf-key-a")
	kB := []byte("ovf-key-b")
	if err := s.Put(kA, valA); err != nil {
		t.Fatalf("Put A: %v", err)
	}
	if err := s.Put(kB, valB); err != nil {
		t.Fatalf("Put B: %v", err)
	}
	usedAfterPuts := s.heapUsed
	if got := countTypeOverflow(s); got < 6 {
		t.Fatalf("pages de débordement insuffisantes: %d", got)
	}

	if err := s.Delete(kA); err != nil {
		t.Fatalf("Delete A: %v", err)
	}
	if len(orphanOverflowPages(s)) != 0 {
		t.Fatalf("chaîne A non consignée: orphelines=%v", orphanOverflowPages(s))
	}
	if s.heapUsed != usedAfterPuts {
		t.Fatalf("la chaîne non queue ne doit pas déplacer heapUsed: avant=%d après=%d", usedAfterPuts, s.heapUsed)
	}
	gotB, err := s.Get(kB)
	if err != nil || !bytes.Equal(gotB, valB) {
		t.Fatalf("valeur B altérée après suppression de A: err=%v len=%d", err, len(gotB))
	}
	if _, err := s.Get(kA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("A encore lisible: err=%v", err)
	}

	if err := s.Delete(kB); err != nil {
		t.Fatalf("Delete B: %v", err)
	}
	if len(orphanOverflowPages(s)) != 0 {
		t.Fatalf("orphelines après suppression de B: %v", orphanOverflowPages(s))
	}
	if s.heapUsed != 1 {
		t.Fatalf("tas non vidé après suppression des deux valeurs: heapUsed=%d", s.heapUsed)
	}

	if err := s.RepackHeap(); err != nil {
		t.Fatalf("RepackHeap: %v", err)
	}
	if s.heapUsed != 1 {
		t.Fatalf("heapUsed après repack=%d", s.heapUsed)
	}
	for _, k := range [][]byte{kA, kB} {
		if _, err := s.Get(k); !errors.Is(err, ErrNotFound) {
			t.Fatalf("clé %q ressuscitée après repack: %v", k, err)
		}
	}
}
