// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestC2dbZipper3Way_Functional(t *testing.T) {
	// Base : clés 1, 2, 3, 4, 5
	base := []C2db_zip_entry_t{
		{Key: 1, Ver: 10, Val_crc: 0x1111, Deleted: 0},
		{Key: 2, Ver: 10, Val_crc: 0x2222, Deleted: 0},
		{Key: 3, Ver: 10, Val_crc: 0x3333, Deleted: 0},
		{Key: 4, Ver: 10, Val_crc: 0x4444, Deleted: 0},
		{Key: 5, Ver: 10, Val_crc: 0x5555, Deleted: 0},
	}

	// Ours :
	// 1 modifié (Val_crc 0x1111 -> 0xAAAA)
	// 2 inchangé
	// 3 modifié en conflit (Val_crc 0x3333 -> 0xBBBB, Ver 20)
	// 4 supprimé (absent de ours)
	// 5 modifié (Val_crc 0x5555 -> 0xCCCC, Ver 25)
	// 10 ajouté (nouveau)
	ours := []C2db_zip_entry_t{
		{Key: 1, Ver: 15, Val_crc: 0xAAAA, Deleted: 0},
		{Key: 2, Ver: 10, Val_crc: 0x2222, Deleted: 0},
		{Key: 3, Ver: 20, Val_crc: 0xBBBB, Deleted: 0},
		{Key: 5, Ver: 25, Val_crc: 0xCCCC, Deleted: 0},
		{Key: 10, Ver: 30, Val_crc: 0x1010, Deleted: 0},
	}

	// Theirs :
	// 1 inchangé
	// 2 modifié (Val_crc 0x2222 -> 0x2B2B)
	// 3 modifié en conflit (Val_crc 0x3333 -> 0xDDDD, Ver 22) -> Theirs ver 22 > Ours ver 20
	// 4 supprimé (inchangé dans base, absent ici aussi)
	// 5 supprimé (absent de theirs alors que Ours l'a modifié -> conflit modif vs delete !)
	// 20 ajouté (nouveau)
	theirs := []C2db_zip_entry_t{
		{Key: 1, Ver: 10, Val_crc: 0x1111, Deleted: 0},
		{Key: 2, Ver: 18, Val_crc: 0x2B2B, Deleted: 0},
		{Key: 3, Ver: 22, Val_crc: 0xDDDD, Deleted: 0},
		{Key: 20, Ver: 30, Val_crc: 0x2020, Deleted: 0},
	}

	out := make([]C2db_zip_entry_t, 16)
	res := C2db_zipper_3way(base, uint64(len(base)), ours, uint64(len(ours)), theirs, uint64(len(theirs)), out, uint64(len(out)))

	if res.Ok != 1 {
		t.Fatalf("C2db_zipper_3way Ok = %d, attendu 1", res.Ok)
	}
	// Conflits attendus : 2 (clé 3 conflit de mutation, clé 5 conflit modif vs delete)
	if res.Conflicts_count != 2 {
		t.Fatalf("Conflicts_count = %d, attendu 2", res.Conflicts_count)
	}

	// Nombre fusionné :
	// Clé 1 (Ours: 0xAAAA)
	// Clé 2 (Theirs: 0x2B2B)
	// Clé 3 (Theirs gagne: Ver 22 > 20, 0xDDDD)
	// Clé 4 (supprimé des 2 côtés -> pas émis)
	// Clé 5 (Ours modif l'emporte sur suppression: 0xCCCC)
	// Clé 10 (Ours)
	// Clé 20 (Theirs)
	// Total = 6 entrées
	if res.Merged_count != 6 {
		t.Fatalf("Merged_count = %d, attendu 6", res.Merged_count)
	}

	// Vérification de la clé 3 (gagnée par Theirs avec Ver 22)
	if out[2].Key != 3 || out[2].Ver != 22 || out[2].Val_crc != 0xDDDD {
		t.Fatalf("Résolution clé 3 erronée: key=%d ver=%d crc=%#x", out[2].Key, out[2].Ver, out[2].Val_crc)
	}
	// Vérification de la clé 5 (Ours l'emporte sur delete)
	if out[3].Key != 5 || out[3].Val_crc != 0xCCCC {
		t.Fatalf("Résolution clé 5 erronée: key=%d crc=%#x", out[3].Key, out[3].Val_crc)
	}
}

func TestC2dbZipper3Way_ZeroAlloc(t *testing.T) {
	base := []C2db_zip_entry_t{{Key: 1, Ver: 1, Val_crc: 100}}
	ours := []C2db_zip_entry_t{{Key: 1, Ver: 2, Val_crc: 200}}
	theirs := []C2db_zip_entry_t{{Key: 1, Ver: 1, Val_crc: 100}}
	out := make([]C2db_zip_entry_t, 4)

	allocs := testing.AllocsPerRun(100, func() {
		_ = C2db_zipper_3way(base, 1, ours, 1, theirs, 1, out, 4)
	})
	if allocs != 0 {
		t.Fatalf("C2db_zipper_3way allocs/op = %.2f, attendu 0", allocs)
	}
}

func TestC2dbZipper3Way_VsCOracle(t *testing.T) {
	base := []C2db_zip_entry_t{
		{Key: 1, Ver: 10, Val_crc: 0x1111, Deleted: 0},
		{Key: 2, Ver: 10, Val_crc: 0x2222, Deleted: 0},
		{Key: 3, Ver: 10, Val_crc: 0x3333, Deleted: 0},
		{Key: 4, Ver: 10, Val_crc: 0x4444, Deleted: 0},
		{Key: 5, Ver: 10, Val_crc: 0x5555, Deleted: 0},
	}
	ours := []C2db_zip_entry_t{
		{Key: 1, Ver: 15, Val_crc: 0xAAAA, Deleted: 0},
		{Key: 2, Ver: 10, Val_crc: 0x2222, Deleted: 0},
		{Key: 3, Ver: 20, Val_crc: 0xBBBB, Deleted: 0},
		{Key: 5, Ver: 25, Val_crc: 0xCCCC, Deleted: 0},
		{Key: 10, Ver: 30, Val_crc: 0x1010, Deleted: 0},
	}
	theirs := []C2db_zip_entry_t{
		{Key: 1, Ver: 10, Val_crc: 0x1111, Deleted: 0},
		{Key: 2, Ver: 18, Val_crc: 0x2B2B, Deleted: 0},
		{Key: 3, Ver: 22, Val_crc: 0xDDDD, Deleted: 0},
		{Key: 20, Ver: 30, Val_crc: 0x2020, Deleted: 0},
	}
	outGo := make([]C2db_zip_entry_t, 16)
	resGo := C2db_zipper_3way(base, 5, ours, 5, theirs, 4, outGo, 16)

	// Cas hostile : capacité insuffisante (cap = 3)
	outHostile := make([]C2db_zip_entry_t, 3)
	resHostileGo := C2db_zipper_3way(base, 5, ours, 5, theirs, 4, outHostile, 3)

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "zip_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/c2db_zipper_3way.c"

int main(void) {
    c2db_zip_entry_t base[5] = {
        {1, 10, 0x1111, 0, {0,0,0}},
        {2, 10, 0x2222, 0, {0,0,0}},
        {3, 10, 0x3333, 0, {0,0,0}},
        {4, 10, 0x4444, 0, {0,0,0}},
        {5, 10, 0x5555, 0, {0,0,0}}
    };
    c2db_zip_entry_t ours[5] = {
        {1, 15, 0xAAAA, 0, {0,0,0}},
        {2, 10, 0x2222, 0, {0,0,0}},
        {3, 20, 0xBBBB, 0, {0,0,0}},
        {5, 25, 0xCCCC, 0, {0,0,0}},
        {10, 30, 0x1010, 0, {0,0,0}}
    };
    c2db_zip_entry_t theirs[4] = {
        {1, 10, 0x1111, 0, {0,0,0}},
        {2, 18, 0x2B2B, 0, {0,0,0}},
        {3, 22, 0xDDDD, 0, {0,0,0}},
        {20, 30, 0x2020, 0, {0,0,0}}
    };
    c2db_zip_entry_t out[16];
    c2db_zip_result_t r = c2db_zipper_3way(base, 5, ours, 5, theirs, 4, out, 16);

    c2db_zip_entry_t out_h[3];
    c2db_zip_result_t rh = c2db_zipper_3way(base, 5, ours, 5, theirs, 4, out_h, 3);

    printf("%lu %lu %u | %lu %lu %u\n",
        r.merged_count, r.conflicts_count, r.ok,
        rh.merged_count, rh.conflicts_count, rh.ok);

    for (uint64_t i = 0; i < r.merged_count; i++) {
        printf("[%lu: k=%lu v=%lu c=%x d=%u] ", i, out[i].key, out[i].ver, out[i].val_crc, out[i].deleted);
    }
    printf("\n");
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

	var goEntriesBuf bytes.Buffer
	for i := uint64(0); i < resGo.Merged_count; i++ {
		fmt.Fprintf(&goEntriesBuf, "[%d: k=%d v=%d c=%x d=%d] ", i, outGo[i].Key, outGo[i].Ver, outGo[i].Val_crc, outGo[i].Deleted)
	}

	expectedHeader := fmt.Sprintf("%d %d %d | %d %d %d",
		resGo.Merged_count, resGo.Conflicts_count, resGo.Ok,
		resHostileGo.Merged_count, resHostileGo.Conflicts_count, resHostileGo.Ok)

	cLines := bytes.Split(bytes.TrimSpace(outC), []byte("\n"))
	if len(cLines) < 2 {
		t.Fatalf("Sortie C tronquée: %q", string(outC))
	}
	if string(cLines[0]) != expectedHeader {
		t.Fatalf("PARITÉ EN-TÊTE ROMPUE : Go=%q, C=%q", expectedHeader, string(cLines[0]))
	}
	if string(bytes.TrimSpace(cLines[1])) != string(bytes.TrimSpace(goEntriesBuf.Bytes())) {
		t.Fatalf("PARITÉ ENTRÉES ROMPUE : Go=%q, C=%q", goEntriesBuf.String(), string(cLines[1]))
	}
	t.Logf("Oracle gcc -O2 parité bit-exacte zipper_3way validée : %s", bytes.TrimSpace(cLines[0]))
}
