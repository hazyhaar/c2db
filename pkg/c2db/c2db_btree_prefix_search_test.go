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

func buildTestLeafPage(keys []string) []byte {
	page := make([]byte, 16384)
	page[20] = 1 // TYPE_LEAF
	binary.LittleEndian.PutUint16(page[22:24], uint16(len(keys)))

	cellOff := uint16(16384)
	for i, k := range keys {
		klen := uint16(len(k))
		vlen := uint16(8) // dummy val
		cellLen := 4 + klen + vlen
		cellOff -= cellLen

		// Écriture du slot pointant vers cellOff
		slotAddr := 64 + i*2
		binary.LittleEndian.PutUint16(page[slotAddr:slotAddr+2], cellOff)

		// Écriture de la cellule
		binary.LittleEndian.PutUint16(page[cellOff:cellOff+2], klen)
		binary.LittleEndian.PutUint16(page[cellOff+2:cellOff+4], vlen)
		copy(page[cellOff+4:cellOff+4+klen], []byte(k))
	}
	return page
}

func TestC2dbBtreePrefixSearch_Functional(t *testing.T) {
	keys := []string{"apple", "banana1", "banana2", "banana3", "cherry", "date"}
	page := buildTestLeafPage(keys)

	// Cas 1 : Préfixe multi-matches "banana"
	res := C2db_btree_prefix_search(page, 16384, []byte("banana"), 6)
	if res.Found != 1 || res.Slot_idx != 1 || res.Count != 3 {
		t.Fatalf("Recherche 'banana' invalide: found=%d idx=%d count=%d (attendu 1, 1, 3)", res.Found, res.Slot_idx, res.Count)
	}

	// Cas 2 : Match unique début "apple"
	res = C2db_btree_prefix_search(page, 16384, []byte("apple"), 5)
	if res.Found != 1 || res.Slot_idx != 0 || res.Count != 1 {
		t.Fatalf("Recherche 'apple' invalide: found=%d idx=%d count=%d (attendu 1, 0, 1)", res.Found, res.Slot_idx, res.Count)
	}

	// Cas 3 : Match unique fin "date"
	res = C2db_btree_prefix_search(page, 16384, []byte("date"), 4)
	if res.Found != 1 || res.Slot_idx != 5 || res.Count != 1 {
		t.Fatalf("Recherche 'date' invalide: found=%d idx=%d count=%d (attendu 1, 5, 1)", res.Found, res.Slot_idx, res.Count)
	}

	// Cas 4 : Inexistant au milieu "blueberry"
	res = C2db_btree_prefix_search(page, 16384, []byte("blueberry"), 9)
	if res.Found != 0 || res.Count != 0 {
		t.Fatalf("Recherche 'blueberry' trouvée à tort: found=%d count=%d", res.Found, res.Count)
	}

	// Cas 5 : Inexistant après la fin "zebra"
	res = C2db_btree_prefix_search(page, 16384, []byte("zebra"), 5)
	if res.Found != 0 || res.Count != 0 {
		t.Fatalf("Recherche 'zebra' trouvée à tort: found=%d count=%d", res.Found, res.Count)
	}

	// Cas 6 : Préfixe court 1 lettre 'b' -> les 3 bananes
	res = C2db_btree_prefix_search(page, 16384, []byte("b"), 1)
	if res.Found != 1 || res.Slot_idx != 1 || res.Count != 3 {
		t.Fatalf("Recherche 'b' invalide: found=%d idx=%d count=%d (attendu 1, 1, 3)", res.Found, res.Slot_idx, res.Count)
	}
}

func TestC2dbBtreePrefixSearch_ZeroAlloc(t *testing.T) {
	keys := []string{"alpha", "beta1", "beta2", "gamma"}
	page := buildTestLeafPage(keys)
	pref := []byte("beta")

	allocs := testing.AllocsPerRun(100, func() {
		_ = C2db_btree_prefix_search(page, 16384, pref, uint64(len(pref)))
	})
	if allocs != 0 {
		t.Fatalf("C2db_btree_prefix_search allocs/op = %.2f, attendu 0", allocs)
	}
}

func TestC2dbBtreePrefixSearch_VsCOracle(t *testing.T) {
	keys := []string{"apple", "banana1", "banana2", "banana3", "cherry", "date"}
	page := buildTestLeafPage(keys)

	res1 := C2db_btree_prefix_search(page, 16384, []byte("banana"), 6)
	res2 := C2db_btree_prefix_search(page, 16384, []byte("fig"), 3)
	res3 := C2db_btree_prefix_search(page, 16383, []byte("banana"), 6) // hostile len
	res4 := C2db_btree_prefix_search(nil, 16384, []byte("banana"), 6)  // hostile nil page

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "pref_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/c2db_btree_prefix_search.c"

int main(void) {
    uint8_t page[16384];
    memset(page, 0, 16384);
    page[20] = 1; // TYPE_LEAF
    
    // nslots = 6
    page[22] = 6;
    page[23] = 0;

    const char *keys[] = {"apple", "banana1", "banana2", "banana3", "cherry", "date"};
    uint16_t cell_off = 16384;
    for (int i = 0; i < 6; i++) {
        uint16_t klen = (uint16_t)strlen(keys[i]);
        uint16_t vlen = 8;
        uint16_t clen = 4 + klen + vlen;
        cell_off -= clen;

        uint16_t slot_addr = 64 + i * 2;
        page[slot_addr] = (uint8_t)(cell_off & 0xFF);
        page[slot_addr + 1] = (uint8_t)((cell_off >> 8) & 0xFF);

        page[cell_off] = (uint8_t)(klen & 0xFF);
        page[cell_off + 1] = (uint8_t)((klen >> 8) & 0xFF);
        page[cell_off + 2] = (uint8_t)(vlen & 0xFF);
        page[cell_off + 3] = (uint8_t)((vlen >> 8) & 0xFF);
        memcpy(page + cell_off + 4, keys[i], klen);
    }

    c2db_prefix_result_t r1 = c2db_btree_prefix_search(page, 16384, (const uint8_t*)"banana", 6);
    c2db_prefix_result_t r2 = c2db_btree_prefix_search(page, 16384, (const uint8_t*)"fig", 3);
    c2db_prefix_result_t r3 = c2db_btree_prefix_search(page, 16383, (const uint8_t*)"banana", 6);
    c2db_prefix_result_t r4 = c2db_btree_prefix_search(NULL, 16384, (const uint8_t*)"banana", 6);

    printf("%lu %lu %u | %lu %lu %u | %lu %lu %u | %lu %lu %u\n",
        r1.slot_idx, r1.count, r1.found,
        r2.slot_idx, r2.count, r2.found,
        r3.slot_idx, r3.count, r3.found,
        r4.slot_idx, r4.count, r4.found);
    return 0;
}
`
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("Échec écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Échec compilation gcc -O2: %v, out=%s", err, out)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("Échec exécution binaire C oracle: %v", err)
	}

	expectedOutput := fmt.Sprintf("%d %d %d | %d %d %d | %d %d %d | %d %d %d\n",
		res1.Slot_idx, res1.Count, res1.Found,
		res2.Slot_idx, res2.Count, res2.Found,
		res3.Slot_idx, res3.Count, res3.Found,
		res4.Slot_idx, res4.Count, res4.Found)

	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(expectedOutput))) {
		t.Fatalf("PARITÉ ROMPUE C2DB BTREE PREFIX SEARCH VS GCC -O2 : Go=%q, C=%q", expectedOutput, string(outC))
	}
	t.Logf("Oracle gcc -O2 parité bit-exacte prefix_search validée : %s", bytes.TrimSpace(outC))
}
