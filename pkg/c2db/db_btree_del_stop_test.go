// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"fmt"
	"math/bits"
	"testing"
)

// TestDbBtree_DelStopsAtTargetLeaf mesure, via le masque de pages copiées sur
// écriture (st.Pages) renvoyé par le noyau, combien de pages une suppression
// parcourt. Sur un arbre à plusieurs feuilles, la descente doit atteindre la
// feuille cible et s'y arrêter : le masque ne doit porter que la racine interne
// et cette feuille. Tant que le noyau suit le pointeur `next`, il copie chaque
// feuille de la chaîne et le masque s'élargit.
func TestDbBtree_DelStopsAtTargetLeaf(t *testing.T) {
	const npages = uint64(64)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("leaf init rejeté")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}

	val := make([]byte, 4000)
	copy(val, "VALEUR-C2DB")
	const nkeys = 40
	keys := make([][]byte, 0, nkeys)
	for i := 0; i < nkeys; i++ {
		k := []byte(fmt.Sprintf("k%04d", i))
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), val, uint64(len(val)))
		if got.Ok != 1 {
			t.Fatalf("insert %d rejeté: %+v (used=%d)", i, got, got.Used)
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, k)
	}

	if pub[hst.Root*pageN+20] != 2 {
		t.Fatalf("racine non interne après %d insertions (root=%d used=%d)", nkeys, hst.Root, hst.Used)
	}
	leaves := uint64(0)
	for pg := uint64(0); pg < npages; pg++ {
		if pub[pg*pageN+20] == 1 {
			leaves++
		}
	}
	if leaves < 4 {
		t.Fatalf("arbre insuffisant pour distinguer la chaîne : %d feuilles", leaves)
	}
	t.Logf("arbre: racine interne=%d, used=%d, feuilles=%d", hst.Root, hst.Used, leaves)

	del := Db_bt_del_heap(pub, dirty, nbytes, npages, &hst, keys[0], uint64(len(keys[0])))
	if del.Ok != 1 {
		t.Fatalf("suppression rejetée: %+v", del)
	}
	visited := uint64(bits.OnesCount64(del.Pages))
	t.Logf("pages copiées par la suppression: %d (masque=%#x, feuilles=%d)", visited, del.Pages, leaves)

	if visited > 2 {
		t.Fatalf("suppression a parcouru %d pages pour un chemin de profondeur 2 (masque=%#x, feuilles=%d) : la chaîne de feuilles est suivie", visited, del.Pages, leaves)
	}

	copy(pub, dirty)
	hst = del
	out := make([]byte, 4096)
	if g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, keys[0], uint64(len(keys[0])), out, uint64(len(out))); g.Found != 0 {
		t.Fatalf("clé supprimée encore présente: %+v", g)
	}
	for i := 1; i < nkeys; i++ {
		g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, keys[i], uint64(len(keys[i])), out, uint64(len(out)))
		if g.Found != 1 || g.Fallback != 0 || !bytes.Equal(out[:11], val[:11]) {
			t.Fatalf("clé %d perdue après suppression: %+v", i, g)
		}
	}
}
