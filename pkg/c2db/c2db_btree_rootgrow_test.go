// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// rgKey fabrique une clé déterministe de longueur klen, strictement croissante
// avec l'indice i sur ses quatre premiers octets. Aucun tirage pseudo-aléatoire
// n'est employé : la clé est une fonction pure de l'indice.
func rgKey(i, klen int) []byte {
	k := make([]byte, klen)
	k[0] = byte(i)
	k[1] = byte(i >> 8)
	k[2] = byte(i >> 16)
	k[3] = byte(i >> 24)
	for j := 4; j < klen; j++ {
		k[j] = byte('a' + (i+j)%26)
	}
	return k
}

func rgLE64(b []byte, off uint64) uint64 {
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(b[off+uint64(i)]) << (8 * uint(i))
	}
	return v
}

// rgDepth compte le nombre de niveaux de l'arbre en suivant l'enfant gauche
// depuis la racine : une racine feuille vaut 1, une racine interne au moins 2.
func rgDepth(heap []byte, root uint64) int {
	d := 0
	pg := root
	for d < 64 {
		if pg*pageN+22 > uint64(len(heap)) {
			return d
		}
		d++
		if heap[pg*pageN+20] != 2 {
			break
		}
		pg = rgLE64(heap, pg*pageN+8)
	}
	return d
}

// TestDbBtree_RootGrowDepth4 construit un arbre de profondeur 3 dont la racine
// est pleine, puis insère une clé sous un interne de niveau 1 saturé. Avant le
// correctif, le noyau rend ok = 0 ; après, l'insertion réussit, la racine est
// élevée et l'arbre atteint la profondeur 4, toutes les clés restant lisibles.
func TestDbBtree_RootGrowDepth4(t *testing.T) {
	const npages = uint64(256)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("leaf_init rejeté")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	const klen = 4000
	const total = 160
	val := []byte{0x42}
	id := make([]byte, 16)
	keys := make([][]byte, 0, total)
	for i := 0; i < total; i++ {
		k := rgKey(i, klen)
		id[0] = byte(i)
		id[1] = byte(i >> 8)
		got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(klen), id, val, uint64(len(val)))
		if got.Ok != 1 || got.Full != 0 {
			t.Fatalf("insertion %d refusée ok=%d full=%d used=%d root=%d", i, got.Ok, got.Full, got.Used, got.Root)
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, k)
	}
	d := rgDepth(pub, hst.Root)
	t.Logf("croissance: insertions=%d profondeur=%d root=%d used=%d", len(keys), d, hst.Root, hst.Used)
	if d < 5 {
		t.Fatalf("profondeur atteinte %d < 5 (au moins deux élévations successives attendues, root=%d used=%d)", d, hst.Root, hst.Used)
	}
	var snapMax [16]byte
	for i := range snapMax {
		snapMax[i] = 0xFF
	}
	out := make([]byte, len(val))
	for i, k := range keys {
		g := Db_bt_get_as_of_heap(pub, nbytes, npages, hst.Root, k, uint64(klen), snapMax[:], out, uint64(len(out)))
		if g.Found != 1 || g.Fallback != 0 {
			t.Fatalf("clé %d illisible après croissance: %+v", i, g)
		}
		if out[0] != 0x42 {
			t.Fatalf("clé %d valeur divergente: %q", i, out)
		}
	}
	// Clé déjà présente : la réinsertion du même couple clé/identifiant doit
	// réussir sans casser la lecture.
	got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, keys[10], uint64(klen), id, val, uint64(len(val)))
	if got.Ok != 1 || got.Full != 0 {
		t.Fatalf("réinsertion clé existante refusée ok=%d full=%d", got.Ok, got.Full)
	}
	copy(pub, dirty)
	hst = got
	// Valeur large : une cellule de 6 020 octets (clé 4 000 + valeur 2 000)
	// contraint la feuille cible à se scinder sous l'arbre profond, sans
	// invalidité de racine.
	bigVal := make([]byte, 2000)
	bigVal[0] = 0x7F
	kbig := rgKey(total+1, klen)
	id[0] = byte(total + 1)
	got = Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, kbig, uint64(klen), id, bigVal, uint64(len(bigVal)))
	if got.Ok != 1 || got.Full != 0 {
		t.Fatalf("insertion valeur large refusée ok=%d full=%d used=%d", got.Ok, got.Full, got.Used)
	}
	copy(pub, dirty)
	hst = got
	bigOut := make([]byte, len(bigVal))
	gBig := Db_bt_get_as_of_heap(pub, nbytes, npages, hst.Root, kbig, uint64(klen), snapMax[:], bigOut, uint64(len(bigOut)))
	if gBig.Found != 1 || gBig.Fallback != 0 || gBig.Len_ != uint64(len(bigVal)) || bigOut[0] != 0x7F {
		t.Fatalf("valeur large illisible: %+v", gBig)
	}
	if rgDepth(pub, hst.Root) < 4 {
		t.Fatalf("profondeur perdue après réinsertions: %d", rgDepth(pub, hst.Root))
	}
}

// TestDbBtree_RootGrowTreeFull vérifie que l'épuisement des pages pendant une
// scission interne rend full = 1, état distinct d'un refus de validation.
func TestDbBtree_RootGrowTreeFull(t *testing.T) {
	const npages = uint64(48)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("leaf_init rejeté")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	const klen = 4000
	val := []byte{0x11}
	id := make([]byte, 16)
	var last Db_bt_heap_st_t
	failed := false
	for i := 0; i < 4000; i++ {
		k := rgKey(i, klen)
		id[0] = byte(i)
		id[1] = byte(i >> 8)
		got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(klen), id, val, uint64(len(val)))
		if got.Ok != 1 {
			last = got
			failed = true
			break
		}
		copy(pub, dirty)
		hst = got
	}
	if !failed {
		t.Fatal("aucun échec sur un tas de 48 pages")
	}
	if last.Ok != 0 {
		t.Fatalf("échec attendu ok=0, got ok=%d", last.Ok)
	}
	if last.Full != 1 {
		t.Fatalf("échec d'épuisement sans full=1 (ok=%d full=%d used=%d)", last.Ok, last.Full, hst.Used)
	}
}

// TestEngineClassifyInsertFull vérifie que l'état full du noyau est traduit en
// ErrHeapFull quand le tas n'a plus de paire de pages, et en ErrTreeFull quand
// la structure seule a refusé de croître. ErrTreeFull est exporté et distinct
// de errInsert et d'ErrHeapFull.
func TestEngineClassifyInsertFull(t *testing.T) {
	if errors.Is(ErrTreeFull, errInsert) || errors.Is(ErrTreeFull, ErrHeapFull) || errors.Is(errInsert, ErrTreeFull) {
		t.Fatalf("ErrTreeFull confondue: %v", ErrTreeFull)
	}
	s := &Shard{heapPages: 100, heapUsed: 10, heapWatermark: 90}
	if got := s.classifyInsertFull(Db_bt_heap_st_t{Ok: 0, Full: 1, Used: 10}); !errors.Is(got, ErrTreeFull) {
		t.Fatalf("place disponible: got=%v want=%v", got, ErrTreeFull)
	}
	if got := s.classifyInsertFull(Db_bt_heap_st_t{Ok: 0, Full: 1, Used: 99}); !errors.Is(got, ErrHeapFull) {
		t.Fatalf("tas épuisé: got=%v want=%v", got, ErrHeapFull)
	}
	s2 := &Shard{heapPages: 100, heapUsed: 95, heapWatermark: 90}
	if got := s2.classifyInsertFull(Db_bt_heap_st_t{Ok: 0, Full: 1, Used: 95}); !errors.Is(got, ErrHeapFull) {
		t.Fatalf("filigrane atteint: got=%v want=%v", got, ErrHeapFull)
	}
}

// TestDbBtree_RootGrowVsCOracle rejoue la croissance de racine dans l'oracle
// gcc -O2 construit sur le C modifié du bureau, et compare les résumés
// bit-exacts (nombre d'insertions, racine, pages utilisées, profondeur, clés
// relues).
func TestDbBtree_RootGrowVsCOracle(t *testing.T) {
	const npages = uint64(256)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("leaf_init rejeté")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	const klen = 4000
	const total = 160
	val := []byte{0x42}
	id := make([]byte, 16)
	keys := make([][]byte, 0, total)
	var n uint64
	for i := 0; i < total; i++ {
		k := rgKey(i, klen)
		id[0] = byte(i)
		id[1] = byte(i >> 8)
		got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(klen), id, val, uint64(len(val)))
		if got.Ok != 1 {
			break
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, k)
		n++
	}
	out := make([]byte, len(val))
	var snapMax [16]byte
	for i := range snapMax {
		snapMax[i] = 0xFF
	}
	var found uint64
	for _, k := range keys {
		g := Db_bt_get_as_of_heap(pub, nbytes, npages, hst.Root, k, uint64(klen), snapMax[:], out, uint64(len(out)))
		if g.Found == 1 && g.Fallback == 0 && out[0] == 0x42 {
			found++
		}
	}
	want := fmt.Sprintf("%d %d %d %d %d\n", n, hst.Root, hst.Used, rgDepth(pub, hst.Root), found)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cSrcPath := filepath.Join(wd, "c_src", "db_btree.c")
	if _, err := os.Stat(cSrcPath); err != nil {
		t.Fatalf("source C introuvable: %v", err)
	}
	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "rootgrow_oracle")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include %q
static uint64_t rle64(const uint8_t *b, uint64_t off) {
    uint64_t i, v;
    v = 0;
    for (i = 0; i < 8; i = i + 1) {
        v = v | ((uint64_t)b[off + i] << (8 * i));
    }
    return v;
}
int main(void) {
    uint64_t npages = 256, nbytes = npages * 16384ULL;
    uint8_t *pub = (uint8_t *)malloc((size_t)nbytes);
    uint8_t *dirty = (uint8_t *)malloc((size_t)nbytes);
    uint8_t key[4000], val[1], id[16], out[8], snap[16];
    db_bt_heap_st_t hst;
    db_bt_get_t g;
    uint64_t i, j, n = 0, d = 0, pg, found = 0;
    if (pub == 0 || dirty == 0) return 1;
    memset(pub, 0, (size_t)nbytes);
    memset(dirty, 0, (size_t)nbytes);
    val[0] = 0x42;
    memset(id, 0, 16);
    memset(snap, 0xFF, 16);
    if (db_bt_leaf_init(pub, 16384).ok == 0) return 1;
    memcpy(dirty, pub, (size_t)nbytes);
    hst.root = 0; hst.used = 1; hst.nkeys = 0; hst.pages = 0; hst.ok = 1; hst.full = 0;
    for (i = 0; i < 160; i = i + 1) {
        key[0] = (uint8_t)i; key[1] = (uint8_t)(i >> 8); key[2] = (uint8_t)(i >> 16); key[3] = (uint8_t)(i >> 24);
        for (j = 4; j < 4000; j = j + 1) {
            key[j] = (uint8_t)('a' + (uint8_t)((i + j) %% 26));
        }
        id[0] = (uint8_t)i; id[1] = (uint8_t)(i >> 8);
        hst = db_bt_insert_ver_heap(pub, dirty, nbytes, npages, hst, key, 4000, id, val, 1);
        if (hst.ok != 1) break;
        memcpy(pub, dirty, (size_t)nbytes);
        n = n + 1;
    }
    d = 0; pg = hst.root;
    for (j = 0; j < 64; j = j + 1) {
        d = d + 1;
        if (pub[pg * 16384ULL + 20] != 2) break;
        pg = rle64(pub, pg * 16384ULL + 8);
    }
    for (i = 0; i < n; i = i + 1) {
        key[0] = (uint8_t)i; key[1] = (uint8_t)(i >> 8); key[2] = (uint8_t)(i >> 16); key[3] = (uint8_t)(i >> 24);
        for (j = 4; j < 4000; j = j + 1) {
            key[j] = (uint8_t)('a' + (uint8_t)((i + j) %% 26));
        }
        g = db_bt_get_as_of_heap(pub, nbytes, npages, hst.root, key, 4000, snap, out, 8);
        if (g.found == 1 && g.fallback == 0 && out[0] == 0x42) found = found + 1;
    }
    printf("%%llu %%llu %%llu %%llu %%llu\n",
        (unsigned long long)n, (unsigned long long)hst.root, (unsigned long long)hst.used,
        (unsigned long long)d, (unsigned long long)found);
    return 0;
}
`, cSrcPath)
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("écriture C: %v", err)
	}
	if o, err := exec.Command("gcc", "-O2", "-o", cBin, srcFile).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, o)
	}
	gotC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(gotC), bytes.TrimSpace([]byte(want))) {
		t.Fatalf("oracle diverge\nC  : %s\nGo : %s", gotC, want)
	}
}
