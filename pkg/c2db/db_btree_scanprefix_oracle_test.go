// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDbBtree_ScanPrefixHeapVsCOracle prouve la parité bit-exacte de
// Db_bt_scan_prefix_heap contre l'oracle gcc -O2 sur un tas à trois niveaux
// dont l'enfant gauche du nœud interne de niveau 1 est la page 0. Le vecteur
// couvre le grand tampon (compte exact), le tampon de 16 Kio (capacité
// insuffisante signalée) et la somme des décalages produits.
func TestDbBtree_ScanPrefixHeapVsCOracle(t *testing.T) {
	const pageN = uint64(16384)
	const npages = uint64(512)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)

	st := Db_bt_leaf_init(pub[:pageN], pageN)
	if st.Ok != 1 {
		t.Fatalf("leaf init rejeté")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}

	const nkeys = 5000
	const klen = 254
	for i := nkeys - 1; i >= 0; i-- {
		k := make([]byte, klen)
		copy(k, fmt.Sprintf("docids/long-%06d", i))
		for j := 20; j < len(k); j++ {
			k[j] = byte('a' + (i+j)%26)
		}
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), []byte("v"), 1)
		if got.Ok != 1 {
			t.Fatalf("insert %d rejeté: used=%d nkeys=%d", i, got.Used, got.Nkeys)
		}
		copy(pub, dirty)
		hst = got
	}

	// Contrôle de structure : racine interne, enfant gauche interne, enfant
	// gauche de ce dernier = page 0. Sans cette forme, le vecteur ne mord pas.
	rootBase := hst.Root * pageN
	if pub[rootBase+20] != 2 {
		t.Fatalf("racine page %d type=%d, attendu interne", hst.Root, pub[rootBase+20])
	}
	mid := binary.LittleEndian.Uint64(pub[rootBase+8:])
	if mid >= npages || pub[mid*pageN+20] != 2 {
		t.Fatalf("enfant gauche %d n'est pas un nœud interne", mid)
	}
	if left := binary.LittleEndian.Uint64(pub[mid*pageN+8:]); left != 0 {
		t.Fatalf("enfant gauche du niveau 1 = %d, attendu page 0", left)
	}

	prefix := []byte("docids/")
	big := make([]byte, nbytes*4)
	rBig := Db_bt_scan_prefix_heap(pub, nbytes, npages, hst.Root, prefix, uint64(len(prefix)), big, uint64(len(big)))
	if rBig.Ok != 1 || rBig.Nfound != nkeys {
		t.Fatalf("Go grand tampon ok=%d nfound=%d want 1/%d", rBig.Ok, rBig.Nfound, nkeys)
	}
	var sum uint64
	for i := uint64(0); i < rBig.Nfound; i++ {
		sum += uint64(binary.LittleEndian.Uint32(big[i*4:]))
	}
	small := make([]byte, 16384)
	rSmall := Db_bt_scan_prefix_heap(pub, nbytes, npages, hst.Root, prefix, uint64(len(prefix)), small, uint64(len(small)))
	if rSmall.Ok != 0 {
		t.Fatalf("Go petit tampon ok=%d, attendu 0 (capacité insuffisante)", rSmall.Ok)
	}

	tmpDir := t.TempDir()
	heapFile := filepath.Join(tmpDir, "heap.bin")
	if err := os.WriteFile(heapFile, pub, 0644); err != nil {
		t.Fatalf("écriture tas: %v", err)
	}
	cBin := filepath.Join(tmpDir, "db_btree_scanprefix_oracle")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include "%s/db_btree.c"

int main(int argc, char **argv) {
    FILE *f = fopen(argv[1], "rb");
    if (f == NULL) return 2;
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    uint8_t *heap = (uint8_t *)malloc((size_t)n);
    if (heap == NULL) return 3;
    if (fread(heap, 1, (size_t)n, f) != (size_t)n) return 4;
    fclose(f);
    uint64_t nbytes = (uint64_t)n;
    uint64_t npages = nbytes / (uint64_t)16384;
    uint64_t root = (uint64_t)strtoull(argv[2], NULL, 10);
    uint8_t prefix[7];
    prefix[0]='d'; prefix[1]='o'; prefix[2]='c'; prefix[3]='i';
    prefix[4]='d'; prefix[5]='s'; prefix[6]='/';
    uint8_t *big = (uint8_t *)malloc((size_t)(n * 4));
    uint8_t *small = (uint8_t *)malloc(16384);
    db_bt_scan_t rbig = db_bt_scan_prefix_heap(heap, nbytes, npages, root, prefix, 7, big, nbytes * 4);
    db_bt_scan_t rsmall = db_bt_scan_prefix_heap(heap, nbytes, npages, root, prefix, 7, small, 16384);
    uint64_t sum = 0;
    uint64_t i;
    for (i = 0; i < rbig.nfound; i++) {
        uint32_t off;
        __builtin_memcpy(&off, big + i * 4, 4);
        sum += (uint64_t)off;
    }
    printf("%%u %%llu %%u %%llu %%llu\n",
        rbig.ok, (unsigned long long)rbig.nfound,
        rsmall.ok, (unsigned long long)rsmall.nfound,
        (unsigned long long)sum);
    return 0;
}
`, "/devhoros/c2simd/c2pkg/c2db/c_src")

	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("Échec écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Échec compilation gcc: %v, out=%s", err, o)
	}
	outC, err := exec.Command(cBin, heapFile, fmt.Sprintf("%d", hst.Root)).Output()
	if err != nil {
		t.Fatalf("Échec exécution binaire C oracle: %v", err)
	}

	want := fmt.Sprintf("%d %d %d %d %d\n", rBig.Ok, rBig.Nfound, rSmall.Ok, rSmall.Nfound, sum)
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(want))) {
		t.Fatalf("PARITÉ ROMPUE C2DB BTREE SCAN PREFIX VS GCC -O2 : Go=%q, C=%q", want, string(outC))
	}
	t.Logf("Vecteur scan_prefix_heap : parité bit-exacte gcc -O2 : %s", bytes.TrimSpace(outC))
}
