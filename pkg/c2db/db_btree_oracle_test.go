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

func TestDbBtree_VsCOracle(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	for i := range pub {
		pub[i] = byte(i*17 + 3)
	}

	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	key := []byte{'k', '1'}
	val := []byte{'v', '1'}
	ins := Db_bt_insert(pub, dirty, pageN, &st, key, 2, val, 2)
	if ins.Ok != 1 || ins.Copied != 1 || ins.Nkeys != 1 {
		t.Fatalf("insert nominal: %+v", ins)
	}
	out := make([]byte, 8)
	g := Db_bt_get(dirty, pageN, key, 2, out, 8)
	if g.Found != 1 || g.Len_ != 2 || out[0] != 'v' || out[1] != '1' {
		t.Fatalf("get nominal: %+v", g)
	}
	gPub := Db_bt_get(pub, pageN, key, 2, out, 8)
	if gPub.Found != 0 {
		t.Fatalf("get pub nominal a trouvé la clé")
	}

	insBadN := Db_bt_insert(pub, append([]byte(nil), dirty...), 16383, &st, key, 2, val, 2)
	if insBadN.Ok != 0 || insBadN.Copied != 0 {
		t.Fatalf("insert n=16383 doit rejeter sans copie: %+v", insBadN)
	}
	gBadN := Db_bt_get(dirty, 16383, key, 2, make([]byte, 8), 8)
	if gBadN.Found != 0 {
		t.Fatalf("get n=16383 doit rejeter, found=%d", gBadN.Found)
	}

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "db_btree_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>

#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_btree.c"

int main(void) {
    uint8_t pub[16384];
    uint8_t dirty[16384];
    uint8_t out[8];
    uint8_t key[2];
    uint8_t val[2];
    uint64_t i;
    db_bt_state_t st;
    db_bt_state_t ins;
    db_bt_state_t ins_badn;
    db_bt_get_t g;
    db_bt_get_t g_pub;
    db_bt_get_t g_badn;

    for (i = 0; i < 16384; i = i + 1) {
        pub[i] = (uint8_t)(i * 17 + 3);
        dirty[i] = 0;
        if (i < 8) {
            out[i] = 0;
        }
    }
    key[0] = 'k'; key[1] = '1';
    val[0] = 'v'; val[1] = '1';

    st = db_bt_leaf_init(pub, 16384);
    ins = db_bt_insert(pub, dirty, 16384, st, key, 2, val, 2);
    g = db_bt_get(dirty, 16384, key, 2, out, 8);
    g_pub = db_bt_get(pub, 16384, key, 2, out, 8);
    ins_badn = db_bt_insert(pub, dirty, 16383, st, key, 2, val, 2);
    g_badn = db_bt_get(dirty, 16383, key, 2, out, 8);

    printf("%u %u %u %llu %u %llu %u %u %u %u %u %u %u %u %u\n",
        st.ok, ins.ok, ins.copied, (unsigned long long)ins.nkeys,
        g.found, (unsigned long long)g.len, out[0], out[1],
        g_pub.found, ins_badn.ok, ins_badn.copied, g_badn.found,
        dirty[20], dirty[22], pub[22]);
    return 0;
}
`
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("Échec écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Échec compilation gcc: %v, out=%s", err, o)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("Échec exécution binaire C oracle: %v", err)
	}

	want := fmt.Sprintf("%d %d %d %d %d %d %d %d %d %d %d %d %d %d %d\n",
		st.Ok, ins.Ok, ins.Copied, ins.Nkeys,
		g.Found, g.Len_, out[0], out[1],
		gPub.Found, insBadN.Ok, insBadN.Copied, gBadN.Found,
		dirty[20], dirty[22], pub[22])
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(want))) {
		t.Fatalf("PARITÉ ROMPUE C2DB BTREE VS GCC -O2 : Go=%q, C=%q", want, string(outC))
	}
	t.Logf("Vecteur nominal : parité bit-exacte gcc -O2 : %s", bytes.TrimSpace(outC))

	if got := Db_bt_insert(pub, make([]byte, pageN), 0, &st, key, 2, val, 2); got.Ok != 0 {
		t.Fatalf("n=0 doit rejeter, ok=%d", got.Ok)
	}
	resumePub := append([]byte(nil), pub...)
	resumeDirty := make([]byte, pageN)
	resume := Db_bt_insert(resumePub, resumeDirty, pageN, &st, key, 2, val, 2)
	if resume.Ok != 1 || resume.Nkeys != 1 {
		t.Fatalf("reprise après rejet n: %+v", resume)
	}
	gout := make([]byte, 8)
	gr := Db_bt_get(resumeDirty, pageN, key, 2, gout, 8)
	if gr.Found != 1 || gr.Len_ != 2 || gout[0] != 'v' {
		t.Fatalf("reprise get: %+v", gr)
	}
}

func TestDbBtree_SplitOrChainVsCOracle(t *testing.T) {
	const pageN = uint64(16384)
	const npages = uint64(2)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub[:pageN], pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Nkeys: 0, Ok: 1}
	valBig := make([]byte, 7000)
	for i := range valBig {
		valBig[i] = byte(i)
	}
	keyA := []byte{'A'}
	keyB := []byte{'B'}
	keyC := []byte{'C'}

	insA := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, keyA, 1, valBig, uint64(len(valBig)))
	if insA.Ok != 1 || insA.Used != 1 {
		t.Fatalf("insert A: %+v", insA)
	}
	copy(pub, dirty)
	hst = insA
	insB := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, keyB, 1, valBig, uint64(len(valBig)))
	if insB.Ok != 1 || insB.Used != 1 {
		t.Fatalf("insert B: %+v", insB)
	}
	copy(pub, dirty)
	hst = insB
	snap := append([]byte(nil), pub...)
	insC := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, keyC, 1, valBig, uint64(len(valBig)))
	if insC.Ok != 1 || insC.Used != 2 || insC.Nkeys != 3 {
		t.Fatalf("insert C overflow: %+v", insC)
	}
	if !bytes.Equal(pub, snap) {
		t.Fatalf("pub muté par insert_heap CoW")
	}
	out := make([]byte, 8)
	gA := Db_bt_get_heap(dirty, nbytes, npages, insC.Root, keyA, 1, out, 8)
	gB := Db_bt_get_heap(dirty, nbytes, npages, insC.Root, keyB, 1, out, 8)
	gC := Db_bt_get_heap(dirty, nbytes, npages, insC.Root, keyC, 1, out, 8)
	if gA.Found != 1 || gA.Len_ != 7000 || gB.Found != 1 || gB.Len_ != 7000 || gC.Found != 1 || gC.Len_ != 7000 || out[0] != 0 {
		t.Fatalf("get après scission A=%+v B=%+v C=%+v out0=%d", gA, gB, gC, out[0])
	}
	gPubC := Db_bt_get_heap(pub, nbytes, npages, 0, keyC, 1, out, 8)
	if gPubC.Found != 0 {
		t.Fatalf("pub a la clé C avant publication")
	}
	insBad := Db_bt_insert_heap(pub, dirty, 16384, 2, &hst, keyC, 1, valBig, 7000)
	if insBad.Ok != 0 {
		t.Fatalf("nbytes≠npages*PAGE doit rejeter, ok=%d", insBad.Ok)
	}

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "db_btree_heap_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>
#include <string.h>

#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_btree.c"

int main(void) {
    uint8_t pub[32768];
    uint8_t dirty[32768];
    uint8_t val_big[7000];
    uint8_t key_a[1];
    uint8_t key_b[1];
    uint8_t key_c[1];
    uint8_t out[8];
    uint64_t i;
    db_bt_state_t init;
    db_bt_heap_st_t hst;
    db_bt_heap_st_t ins_a;
    db_bt_heap_st_t ins_b;
    db_bt_heap_st_t ins_c;
    db_bt_heap_st_t ins_bad;
    db_bt_get_t g_a;
    db_bt_get_t g_b;
    db_bt_get_t g_c;
    db_bt_get_t g_pub;

    memset(pub, 0, 32768);
    memset(dirty, 0, 32768);
    memset(out, 0, 8);
    for (i = 0; i < 7000; i = i + 1) {
        val_big[i] = (uint8_t)i;
    }
    key_a[0] = 'A';
    key_b[0] = 'B';
    key_c[0] = 'C';

    init = db_bt_leaf_init(pub, 16384);
    hst.root = 0;
    hst.used = 1;
    hst.nkeys = 0;
    hst.ok = 1;
    ins_a = db_bt_insert_heap(pub, dirty, 32768, 2, hst, key_a, 1, val_big, 7000);
    memcpy(pub, dirty, 32768);
    hst = ins_a;
    ins_b = db_bt_insert_heap(pub, dirty, 32768, 2, hst, key_b, 1, val_big, 7000);
    memcpy(pub, dirty, 32768);
    hst = ins_b;
    ins_c = db_bt_insert_heap(pub, dirty, 32768, 2, hst, key_c, 1, val_big, 7000);
    g_a = db_bt_get_heap(dirty, 32768, 2, ins_c.root, key_a, 1, out, 8);
    g_b = db_bt_get_heap(dirty, 32768, 2, ins_c.root, key_b, 1, out, 8);
    g_c = db_bt_get_heap(dirty, 32768, 2, ins_c.root, key_c, 1, out, 8);
    g_pub = db_bt_get_heap(pub, 32768, 2, 0, key_c, 1, out, 8);
    ins_bad = db_bt_insert_heap(pub, dirty, 16384, 2, hst, key_c, 1, val_big, 7000);

    printf("%u %u %u %llu %u %u %llu %u %llu %u %llu %u %u %u\n",
        init.ok, ins_a.ok, ins_b.ok,
        (unsigned long long)ins_c.used, ins_c.ok,
        g_a.found, (unsigned long long)g_a.len,
        g_b.found, (unsigned long long)g_b.len,
        g_c.found, (unsigned long long)g_c.len, out[0],
        g_pub.found, ins_bad.ok);
    return 0;
}
`
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("Échec écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Échec compilation gcc: %v, out=%s", err, o)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("Échec exécution binaire C oracle: %v", err)
	}

	want := fmt.Sprintf("%d %d %d %d %d %d %d %d %d %d %d %d %d %d\n",
		st.Ok, insA.Ok, insB.Ok,
		insC.Used, insC.Ok,
		gA.Found, gA.Len_,
		gB.Found, gB.Len_,
		gC.Found, gC.Len_, out[0],
		gPubC.Found, insBad.Ok)
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(want))) {
		t.Fatalf("PARITÉ ROMPUE C2DB BTREE HEAP VS GCC -O2 : Go=%q, C=%q", want, string(outC))
	}
	t.Logf("Vecteur scission : parité bit-exacte gcc -O2 : %s", bytes.TrimSpace(outC))
}

func TestDbBtree_DelHeapVsCOracle(t *testing.T) {
	const pageN = uint64(16384)
	const npages = uint64(2)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub[:pageN], pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Nkeys: 0, Ok: 1}
	valA := []byte("valA")
	valB := []byte("valB")
	valC := []byte("valC")
	keyA := []byte{'A'}
	keyB := []byte{'B'}
	keyC := []byte{'C'}
	keyZ := []byte{'Z'}

	insA := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, keyA, 1, valA, uint64(len(valA)))
	copy(pub, dirty)
	hst = insA
	insB := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, keyB, 1, valB, uint64(len(valB)))
	copy(pub, dirty)
	hst = insB
	insC := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, keyC, 1, valC, uint64(len(valC)))
	copy(pub, dirty)
	hst = insC

	delB := Db_bt_del_heap(pub, dirty, nbytes, npages, &hst, keyB, 1)
	if delB.Ok != 1 || delB.Nkeys != 2 {
		t.Fatalf("del B: %+v", delB)
	}
	out := make([]byte, 8)
	gA := Db_bt_get_heap(dirty, nbytes, npages, delB.Root, keyA, 1, out, 8)
	gB := Db_bt_get_heap(dirty, nbytes, npages, delB.Root, keyB, 1, out, 8)
	gC := Db_bt_get_heap(dirty, nbytes, npages, delB.Root, keyC, 1, out, 8)
	gPubB := Db_bt_get_heap(pub, nbytes, npages, delB.Root, keyB, 1, out, 8)

	copy(pub, dirty)
	hst = delB
	delZ := Db_bt_del_heap(pub, dirty, nbytes, npages, &hst, keyZ, 1)
	delBadN := Db_bt_del_heap(pub, dirty, 16383, npages, &hst, keyA, 1)

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "db_btree_del_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>
#include <string.h>

#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_btree.c"

int main(void) {
    uint8_t pub[32768];
    uint8_t dirty[32768];
    uint8_t val_a[4];
    uint8_t val_b[4];
    uint8_t val_c[4];
    uint8_t key_a[1];
    uint8_t key_b[1];
    uint8_t key_c[1];
    uint8_t key_z[1];
    uint8_t out[8];
    db_bt_state_t init;
    db_bt_heap_st_t hst;
    db_bt_heap_st_t ins_a;
    db_bt_heap_st_t ins_b;
    db_bt_heap_st_t ins_c;
    db_bt_heap_st_t del_b;
    db_bt_heap_st_t del_z;
    db_bt_heap_st_t del_badn;
    db_bt_get_t g_a;
    db_bt_get_t g_b;
    db_bt_get_t g_c;
    db_bt_get_t g_pub_b;

    memset(pub, 0, 32768);
    memset(dirty, 0, 32768);
    memset(out, 0, 8);
    memcpy(val_a, "valA", 4);
    memcpy(val_b, "valB", 4);
    memcpy(val_c, "valC", 4);
    key_a[0] = 'A';
    key_b[0] = 'B';
    key_c[0] = 'C';
    key_z[0] = 'Z';

    init = db_bt_leaf_init(pub, 16384);
    hst.root = 0;
    hst.used = 1;
    hst.nkeys = 0;
    hst.ok = 1;
    ins_a = db_bt_insert_heap(pub, dirty, 32768, 2, hst, key_a, 1, val_a, 4);
    memcpy(pub, dirty, 32768);
    hst = ins_a;
    ins_b = db_bt_insert_heap(pub, dirty, 32768, 2, hst, key_b, 1, val_b, 4);
    memcpy(pub, dirty, 32768);
    hst = ins_b;
    ins_c = db_bt_insert_heap(pub, dirty, 32768, 2, hst, key_c, 1, val_c, 4);
    memcpy(pub, dirty, 32768);
    hst = ins_c;

    del_b = db_bt_del_heap(pub, dirty, 32768, 2, hst, key_b, 1);
    g_a = db_bt_get_heap(dirty, 32768, 2, del_b.root, key_a, 1, out, 8);
    g_b = db_bt_get_heap(dirty, 32768, 2, del_b.root, key_b, 1, out, 8);
    g_c = db_bt_get_heap(dirty, 32768, 2, del_b.root, key_c, 1, out, 8);
    g_pub_b = db_bt_get_heap(pub, 32768, 2, del_b.root, key_b, 1, out, 8);

    memcpy(pub, dirty, 32768);
    hst = del_b;
    del_z = db_bt_del_heap(pub, dirty, 32768, 2, hst, key_z, 1);
    del_badn = db_bt_del_heap(pub, dirty, 16383, 2, hst, key_a, 1);

    printf("%u %u %llu %u %llu %u %llu %u %llu %u %u %llu %u\n",
        init.ok, del_b.ok, (unsigned long long)del_b.nkeys,
        g_a.found, (unsigned long long)g_a.len,
        g_b.found, (unsigned long long)g_b.len,
        g_c.found, (unsigned long long)g_c.len,
        g_pub_b.found, del_z.ok, (unsigned long long)del_z.nkeys, del_badn.ok);
    return 0;
}
`
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("Échec écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Échec compilation gcc: %v, out=%s", err, o)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("Échec exécution binaire C oracle: %v", err)
	}

	want := fmt.Sprintf("%d %d %d %d %d %d %d %d %d %d %d %d %d\n",
		st.Ok, delB.Ok, delB.Nkeys,
		gA.Found, gA.Len_,
		gB.Found, gB.Len_,
		gC.Found, gC.Len_,
		gPubB.Found, delZ.Ok, delZ.Nkeys, delBadN.Ok)
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(want))) {
		t.Fatalf("PARITÉ ROMPUE C2DB BTREE DEL HEAP VS GCC -O2 : Go=%q, C=%q", want, string(outC))
	}
	t.Logf("Vecteur del_heap : parité bit-exacte gcc -O2 : %s", bytes.TrimSpace(outC))
}

// TestLayoutParity_GoWalkersVsCOracle prouve la parité bit-exacte entre la disposition
// mémoire C (db_btree.h / db_btree.c) et l'ensemble des marcheurs Go (Cursor, replayGetLatestID, findTargetLeaf).
func TestLayoutParity_GoWalkersVsCOracle(t *testing.T) {
	// 1. Contrôle statique d'alignement des constantes contre oracle gcc -O2
	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "c_layout_oracle")
	cSrc := `
#include <stdio.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_btree.c"

int main() {
    printf("%d %d %d %d %d %d\n",
        (int)TYPE, (int)HLC, (int)NSLOTS, (int)BODY, (int)BT_SLOT, (int)BT_IDLEN);
    return 0;
}
`
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("Échec écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Échec compilation gcc: %v, out=%s", err, o)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("Échec exécution oracle C: %v", err)
	}

	wantOffsets := fmt.Sprintf("%d %d %d %d %d %d\n",
		BT_TypeOffset, BT_HLCOffset, BT_NSlotsOffset, BT_BodyOffset, BT_SlotSize, BT_IDLen)
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(wantOffsets))) {
		t.Fatalf("DIVERGENCE DE CONSTANTES D'OFFSET : C=%q, Go=%q", string(outC), wantOffsets)
	}

	// 2. Construction d'un arbre B-Tree multi-niveaux et confrontation systématique
	const npages = uint64(64)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)

	st := Db_bt_leaf_init(pub[:pageN], pageN)
	if st.Ok != 1 {
		t.Fatalf("leaf init rejeté")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}

	// Insérer 200 clés avec 2 versions chacune pour forcer scissions internes et chaînage
	type keyVer struct {
		key []byte
		id1 [16]byte
		v1  []byte
		id2 [16]byte
		v2  []byte
	}
	entries := make([]keyVer, 200)
	for i := 0; i < 200; i++ {
		k := []byte(fmt.Sprintf("key-%05d", i*3+1))
		var id1, id2 [16]byte
		binary.BigEndian.PutUint64(id1[0:8], 1000)
		binary.BigEndian.PutUint64(id1[8:16], uint64(i+1))
		binary.BigEndian.PutUint64(id2[0:8], 2000)
		binary.BigEndian.PutUint64(id2[8:16], uint64(i+1))

		v1 := []byte(fmt.Sprintf("v1-payload-%05d", i))
		v2 := []byte(fmt.Sprintf("v2-payload-%05d", i))
		entries[i] = keyVer{key: k, id1: id1, v1: v1, id2: id2, v2: v2}

		// Insérer v1
		got1 := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), id1[:], v1, uint64(len(v1)))
		if got1.Ok != 1 {
			t.Fatalf("insert v1 pour clé %d rejeté", i)
		}
		copy(pub, dirty)
		hst = got1

		// Insérer v2
		got2 := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), id2[:], v2, uint64(len(v2)))
		if got2.Ok != 1 {
			t.Fatalf("insert v2 pour clé %d rejeté", i)
		}
		copy(pub, dirty)
		hst = got2
	}

	// 3. Vérifier que pour chaque clé, les marcheurs Go (findTargetLeaf, replayGetLatestID, Cursor)
	// sont en parfaite parité avec le moteur C (Db_bt_get_as_of_heap)
	var snapMax [16]byte
	for i := range snapMax {
		snapMax[i] = 0xFF
	}

	sh := &Shard{pub: pub, dirty: dirty, heapRoot: hst.Root, heapPages: npages, shardBytes: nbytes}
	cur := newCursor(pub, hst.Root, snapMax, nil, nil)
	defer cur.Close()

	outBuf := make([]byte, 256)
	for i, e := range entries {
		// A. Oracle C
		gC := Db_bt_get_as_of_heap(pub, nbytes, npages, hst.Root, e.key, uint64(len(e.key)), snapMax[:], outBuf, uint64(len(outBuf)))
		if gC.Found != 1 {
			t.Fatalf("Oracle C n'a pas trouvé clé %d (%s)", i, e.key)
		}
		gotValC := outBuf[:gC.Len_]
		if !bytes.Equal(gotValC, e.v2) {
			t.Fatalf("Oracle C valeur divergente: got %s want %s", gotValC, e.v2)
		}

		// B. Marcheur Go: findTargetLeaf
		leaf, ok := findTargetLeaf(pub, hst.Root, e.key)
		if !ok || leaf >= npages {
			t.Fatalf("findTargetLeaf a échoué pour clé %d (%s)", i, e.key)
		}

		// C. Marcheur Go: replayGetLatestID
		latestID, okID := sh.replayGetLatestID(e.key)
		if !okID {
			t.Fatalf("replayGetLatestID n'a pas trouvé clé %d (%s)", i, e.key)
		}
		if latestID != e.id2 {
			t.Fatalf("replayGetLatestID divergent pour %s: got %x want %x", e.key, latestID, e.id2)
		}

		// D. Marcheur Go: Cursor Seek
		kSeek, vSeek, okSeek := cur.Seek(e.key)
		if !okSeek {
			t.Fatalf("Cursor Seek n'a pas trouvé clé %d (%s)", i, e.key)
		}
		if !bytes.Equal(kSeek, e.key) {
			t.Fatalf("Cursor Seek clé divergente: got %s want %s", kSeek, e.key)
		}
		if !bytes.Equal(vSeek, e.v2) {
			t.Fatalf("Cursor Seek valeur divergente: got %s want %s", vSeek, e.v2)
		}
	}
}

// TestDbBtree_InsertVerHeapPruneVsCOracle prouve la parité bit-exacte entre le
// noyau Go émis par sgoiter et l'oracle gcc -O2 sur le chemin de rétention en
// ligne (db_bt_insert_ver_heap_prune) : une réécriture à valeur identique
// remplace la cellule (nkeys inchangé, même page), une valeur distincte empile
// une version. Le C est pris dans l'arborescence courante du paquet (worktree),
// et non dans un chemin figé, pour que l'oracle porte bien sur le noyau modifié.
func TestDbBtree_InsertVerHeapPruneVsCOracle(t *testing.T) {
	const pageN = uint64(16384)
	const npages = uint64(4)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub[:pageN], pageN).Ok != 1 {
		t.Fatal("leaf init rejeté")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	key := []byte("prune-key")
	id1 := bytes.Repeat([]byte{0x01}, BT_IDLen)
	id2 := bytes.Repeat([]byte{0x02}, BT_IDLen)
	id3 := bytes.Repeat([]byte{0x03}, BT_IDLen)
	v1 := []byte("valeur-identique")
	v2 := []byte("valeur-distincte")

	run := func(id, val []byte) Db_bt_heap_st_t {
		got := Db_bt_insert_ver_heap_prune(pub, dirty, nbytes, npages, &hst, key, uint64(len(key)), id, val, uint64(len(val)), 1)
		copy(pub, dirty)
		hst = got
		return got
	}
	ins1 := run(id1, v1)
	ins2 := run(id2, v1)
	ins3 := run(id3, v2)

	var snapMax [16]byte
	for i := range snapMax {
		snapMax[i] = 0xFF
	}
	out := make([]byte, 64)
	g3 := Db_bt_get_as_of_heap(pub, nbytes, npages, hst.Root, key, uint64(len(key)), snapMax[:], out, uint64(len(out)))
	g3v := append([]byte(nil), out[:g3.Len_]...)
	g2 := Db_bt_get_as_of_heap(pub, nbytes, npages, hst.Root, key, uint64(len(key)), id2, out, uint64(len(out)))
	g1 := Db_bt_get_as_of_heap(pub, nbytes, npages, hst.Root, key, uint64(len(key)), id1, out, uint64(len(out)))

	want := fmt.Sprintf("%d %d %d %d %d %d %d %d %d %d %d %s %d %d\n",
		ins1.Ok, ins1.Used, ins1.Nkeys,
		ins2.Ok, ins2.Used, ins2.Nkeys,
		ins3.Ok, ins3.Used, ins3.Nkeys,
		g3.Found, g3.Len_, string(g3v),
		g2.Found, g1.Found)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cSrcPath := filepath.Join(cwd, "c_src", "db_btree.c")
	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "db_btree_prune_oracle")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include <string.h>

#include "%s"

int main(void) {
    uint8_t pub[65536];
    uint8_t dirty[65536];
    uint8_t key[9];
    uint8_t id1[16];
    uint8_t id2[16];
    uint8_t id3[16];
    uint8_t v1[17];
    uint8_t v2[17];
    uint8_t out[64];
    uint8_t out3[64];
    uint8_t snap[16];
    uint64_t i;
    db_bt_heap_st_t hst;
    db_bt_heap_st_t ins1;
    db_bt_heap_st_t ins2;
    db_bt_heap_st_t ins3;
    db_bt_get_t g3;
    db_bt_get_t g2;
    db_bt_get_t g1;

    memset(pub, 0, 65536);
    memset(dirty, 0, 65536);
    memset(out, 0, 64);
    memcpy(key, "prune-key", 9);
    for (i = 0; i < 16; i = i + 1) { id1[i] = 1; id2[i] = 2; id3[i] = 3; }
    memcpy(v1, "valeur-identique", 17);
    memcpy(v2, "valeur-distincte", 17);

    if (db_bt_leaf_init(pub, 16384).ok != 1) { return 1; }
    hst.root = 0; hst.used = 1; hst.ok = 1;
    ins1 = db_bt_insert_ver_heap_prune(pub, dirty, 65536, 4, hst, key, 9, id1, v1, 16, 1);
    memcpy(pub, dirty, 65536);
    hst = ins1;
    ins2 = db_bt_insert_ver_heap_prune(pub, dirty, 65536, 4, hst, key, 9, id2, v1, 16, 1);
    memcpy(pub, dirty, 65536);
    hst = ins2;
    ins3 = db_bt_insert_ver_heap_prune(pub, dirty, 65536, 4, hst, key, 9, id3, v2, 16, 1);
    memcpy(pub, dirty, 65536);
    hst = ins3;

    for (i = 0; i < 16; i = i + 1) { snap[i] = 0xFF; }
    g3 = db_bt_get_as_of_heap(pub, 65536, 4, hst.root, key, 9, snap, out, 64);
    memset(out3, 0, 64);
    memcpy(out3, out, (size_t)g3.len);
    g2 = db_bt_get_as_of_heap(pub, 65536, 4, hst.root, key, 9, id2, out, 64);
    g1 = db_bt_get_as_of_heap(pub, 65536, 4, hst.root, key, 9, id1, out, 64);

    printf("%%u %%u %%u %%u %%u %%u %%u %%u %%u %%u %%llu %%s %%u %%u\n",
        ins1.ok, ins1.used, ins1.nkeys,
        ins2.ok, ins2.used, ins2.nkeys,
        ins3.ok, ins3.used, ins3.nkeys,
        g3.found, (unsigned long long)g3.len, out3,
        g2.found, g1.found);
    return 0;
}
`, cSrcPath)
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compilation gcc: %v out=%s", err, o)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("exécution oracle C: %v", err)
	}
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(want))) {
		t.Fatalf("PARITÉ ROMPUE prune VS GCC -O2 : Go=%q, C=%q", want, string(bytes.TrimSpace(outC)))
	}
	t.Logf("Vecteur prune : parité bit-exacte gcc -O2 : %s", bytes.TrimSpace(outC))
}
