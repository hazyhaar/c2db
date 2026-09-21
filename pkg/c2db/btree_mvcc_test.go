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

func mkID(b byte) []byte {
	id := make([]byte, 16)
	id[15] = b
	return id
}

func TestDbBtree_InsertVerGetAsOf(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	snap := append([]byte(nil), pub...)

	key := []byte("k")
	val1 := []byte("v1")
	val2 := []byte("v2")
	id0 := mkID(0)
	id1 := mkID(1)
	id2 := mkID(2)

	got := Db_bt_insert_ver(pub, dirty, pageN, key, uint64(len(key)), id1, val1, uint64(len(val1)))
	if got.Ok != 1 || got.Copied != 1 || got.Nkeys != 1 {
		t.Fatalf("insert_ver id1: %+v", got)
	}
	if !bytes.Equal(pub, snap) {
		t.Fatalf("pub muté par insert_ver")
	}
	copy(pub, dirty)
	got2 := Db_bt_insert_ver(pub, dirty, pageN, key, uint64(len(key)), id2, val2, uint64(len(val2)))
	if got2.Ok != 1 || got2.Nkeys != 2 {
		t.Fatalf("insert_ver id2: %+v", got2)
	}

	out := make([]byte, 8)
	g1 := Db_bt_get_as_of(dirty, pageN, key, uint64(len(key)), id1, out, uint64(len(out)))
	if g1.Found != 1 || g1.Len_ != 2 || !bytes.Equal(out[:2], val1) {
		t.Fatalf("GetAsOf(id1): %+v out=%q", g1, out[:2])
	}
	g2 := Db_bt_get_as_of(dirty, pageN, key, uint64(len(key)), id2, out, uint64(len(out)))
	if g2.Found != 1 || g2.Len_ != 2 || !bytes.Equal(out[:2], val2) {
		t.Fatalf("GetAsOf(id2): %+v out=%q", g2, out[:2])
	}
	g0 := Db_bt_get_as_of(dirty, pageN, key, uint64(len(key)), id0, out, uint64(len(out)))
	if g0.Found != 0 {
		t.Fatalf("GetAsOf(id0) doit être absent, found=%d", g0.Found)
	}
}

func TestDbBtree_GcBefore(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}

	key := []byte("k")
	val1 := []byte("v1")
	val2 := []byte("v2")
	id1 := mkID(1)
	id2 := mkID(2)

	got := Db_bt_insert_ver(pub, dirty, pageN, key, 1, id1, val1, 2)
	if got.Ok != 1 {
		t.Fatalf("insert_ver id1: %+v", got)
	}
	copy(pub, dirty)
	got = Db_bt_insert_ver(pub, dirty, pageN, key, 1, id2, val2, 2)
	if got.Ok != 1 {
		t.Fatalf("insert_ver id2: %+v", got)
	}
	copy(pub, dirty)

	gc := Db_bt_gc_before(pub, dirty, pageN, id2)
	if gc.Ok != 1 || gc.Copied != 1 {
		t.Fatalf("gc_before: %+v", gc)
	}
	out := make([]byte, 8)
	g1 := Db_bt_get_as_of(dirty, pageN, key, 1, id1, out, 8)
	if g1.Found != 0 {
		t.Fatalf("GetAsOf(id1) après GC doit être absent, found=%d", g1.Found)
	}
	g2 := Db_bt_get_as_of(dirty, pageN, key, 1, id2, out, 8)
	if g2.Found != 1 || g2.Len_ != 2 || !bytes.Equal(out[:2], val2) {
		t.Fatalf("GetAsOf(id2) après GC: %+v out=%q", g2, out[:2])
	}
}

func TestDbBtree_InsertVerSlotsSorted(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}

	id := mkID(1)
	keys := [][]byte{[]byte("c"), []byte("a"), []byte("b")}
	for _, k := range keys {
		got := Db_bt_insert_ver(pub, dirty, pageN, k, uint64(len(k)), id, []byte("v"), 1)
		if got.Ok != 1 {
			t.Fatalf("insert_ver %q: %+v", k, got)
		}
		copy(pub, dirty)
	}
	id0 := mkID(0)
	got := Db_bt_insert_ver(pub, dirty, pageN, []byte("a"), 1, id0, []byte("z"), 1)
	if got.Ok != 1 {
		t.Fatalf("insert_ver a/id0: %+v", got)
	}

	nslots := uint64(bt_read16(dirty, pageN, 22))
	if nslots != 4 {
		t.Fatalf("nslots=%d want 4", nslots)
	}
	prevK := []byte(nil)
	prevID := []byte(nil)
	for i := uint64(0); i < nslots; i++ {
		slot := uint64(64) + i*uint64(2)
		cell := uint64(bt_read16(dirty, pageN, slot))
		klen := uint64(bt_read16(dirty, pageN, cell))
		k := make([]byte, klen)
		copy(k, dirty[cell+4:cell+4+klen])
		idb := make([]byte, 16)
		copy(idb, dirty[cell+4+klen:cell+4+klen+16])
		if prevK != nil {
			kc := bytes.Compare(prevK, k)
			if kc > 0 {
				t.Fatalf("slots non triés par clé: %q puis %q", prevK, k)
			}
			if kc == 0 && bytes.Compare(prevID, idb) > 0 {
				t.Fatalf("slots non triés par id pour %q", k)
			}
		}
		prevK = k
		prevID = idb
	}
}

func TestDbBtree_InsertVerVsCOracle(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}

	key := []byte{'k'}
	val1 := []byte{'a'}
	val2 := []byte{'b'}
	id0 := mkID(0)
	id1 := mkID(1)
	id2 := mkID(2)

	ins1 := Db_bt_insert_ver(pub, dirty, pageN, key, 1, id1, val1, 1)
	if ins1.Ok != 1 || ins1.Copied != 1 || ins1.Nkeys != 1 {
		t.Fatalf("insert_ver id1: %+v", ins1)
	}
	copy(pub, dirty)
	ins2 := Db_bt_insert_ver(pub, dirty, pageN, key, 1, id2, val2, 1)
	if ins2.Ok != 1 || ins2.Nkeys != 2 {
		t.Fatalf("insert_ver id2: %+v", ins2)
	}

	out := make([]byte, 8)
	g1 := Db_bt_get_as_of(dirty, pageN, key, 1, id1, out, 8)
	o1 := out[0]
	g2 := Db_bt_get_as_of(dirty, pageN, key, 1, id2, out, 8)
	o2 := out[0]
	g0 := Db_bt_get_as_of(dirty, pageN, key, 1, id0, out, 8)
	if g1.Found != 1 || g1.Len_ != 1 || o1 != 'a' {
		t.Fatalf("GetAsOf id1: %+v o=%d", g1, o1)
	}
	if g2.Found != 1 || g2.Len_ != 1 || o2 != 'b' {
		t.Fatalf("GetAsOf id2: %+v o=%d", g2, o2)
	}
	if g0.Found != 0 {
		t.Fatalf("GetAsOf id0 found=%d", g0.Found)
	}

	insBad := Db_bt_insert_ver(pub, make([]byte, pageN), 16383, key, 1, id1, val1, 1)
	if insBad.Ok != 0 || insBad.Copied != 0 {
		t.Fatalf("insert_ver n=16383 doit rejeter sans copie: %+v", insBad)
	}
	gBad := Db_bt_get_as_of(dirty, 16383, key, 1, id2, out, 8)
	if gBad.Found != 0 {
		t.Fatalf("get_as_of n=16383 doit rejeter, found=%d", gBad.Found)
	}

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "db_btree_mvcc_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>

#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_btree.c"

int main(void) {
    uint8_t pub[16384];
    uint8_t dirty[16384];
    uint8_t out[8];
    uint8_t key[1];
    uint8_t val1[1];
    uint8_t val2[1];
    uint8_t id0[16];
    uint8_t id1[16];
    uint8_t id2[16];
    uint64_t i;
    db_bt_state_t init;
    db_bt_state_t ins1;
    db_bt_state_t ins2;
    db_bt_state_t ins_bad;
    db_bt_get_t g1;
    db_bt_get_t g2;
    db_bt_get_t g0;
    db_bt_get_t g_bad;
    uint8_t o1;
    uint8_t o2;

    for (i = 0; i < 16384; i = i + 1) {
        pub[i] = 0;
        dirty[i] = 0;
        if (i < 8) {
            out[i] = 0;
        }
        if (i < 16) {
            id0[i] = 0;
            id1[i] = 0;
            id2[i] = 0;
        }
    }
    id1[15] = 1;
    id2[15] = 2;
    key[0] = 'k';
    val1[0] = 'a';
    val2[0] = 'b';

    init = db_bt_leaf_init(pub, 16384);
    ins1 = db_bt_insert_ver(pub, dirty, 16384, key, 1, id1, val1, 1);
    for (i = 0; i < 16384; i = i + 1) {
        pub[i] = dirty[i];
    }
    ins2 = db_bt_insert_ver(pub, dirty, 16384, key, 1, id2, val2, 1);
    g1 = db_bt_get_as_of(dirty, 16384, key, 1, id1, out, 8);
    o1 = out[0];
    g2 = db_bt_get_as_of(dirty, 16384, key, 1, id2, out, 8);
    o2 = out[0];
    g0 = db_bt_get_as_of(dirty, 16384, key, 1, id0, out, 8);
    ins_bad = db_bt_insert_ver(pub, dirty, 16383, key, 1, id1, val1, 1);
    g_bad = db_bt_get_as_of(dirty, 16383, key, 1, id2, out, 8);

    printf("%u %u %u %llu %u %llu %u %u %llu %u %u %u %u %u %u\n",
        init.ok, ins1.ok, ins1.copied, (unsigned long long)ins1.nkeys,
        g1.found, (unsigned long long)g1.len, o1,
        g2.found, (unsigned long long)g2.len, o2,
        g0.found, ins2.ok, ins_bad.ok, ins_bad.copied, g_bad.found);
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
		st.Ok, ins1.Ok, ins1.Copied, ins1.Nkeys,
		g1.Found, g1.Len_, o1,
		g2.Found, g2.Len_, o2,
		g0.Found, ins2.Ok, insBad.Ok, insBad.Copied, gBad.Found)
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(want))) {
		t.Fatalf("PARITÉ ROMPUE C2DB BTREE MVCC VS GCC -O2 : Go=%q, C=%q", want, string(outC))
	}
}

func TestDbBtree_PrefixScan(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}

	id := mkID(1)
	keys := [][]byte{[]byte("aa"), []byte("ab"), []byte("ba")}
	for _, k := range keys {
		got := Db_bt_insert_ver(pub, dirty, pageN, k, uint64(len(k)), id, []byte("v"), 1)
		if got.Ok != 1 {
			t.Fatalf("insert_ver %q: %+v", k, got)
		}
		copy(pub, dirty)
	}

	packed := make([]byte, 32)
	sa := Db_bt_scan_prefix(dirty, pageN, []byte("a"), 1, packed, uint64(len(packed)))
	if sa.Ok != 1 || sa.Nfound != 2 {
		t.Fatalf("scan a: %+v", sa)
	}
	sb := Db_bt_scan_prefix(dirty, pageN, []byte("b"), 1, packed, uint64(len(packed)))
	if sb.Ok != 1 || sb.Nfound != 1 {
		t.Fatalf("scan b: %+v", sb)
	}
	sz := Db_bt_scan_prefix(dirty, pageN, []byte("z"), 1, packed, uint64(len(packed)))
	if sz.Ok != 1 || sz.Nfound != 0 {
		t.Fatalf("scan z: %+v", sz)
	}

	sa = Db_bt_scan_prefix(dirty, pageN, []byte("a"), 1, packed, uint64(len(packed)))
	seen := map[string]bool{}
	for i := uint64(0); i < sa.Nfound; i++ {
		off := uint64(binary.LittleEndian.Uint32(packed[i*4 : i*4+4]))
		klen := uint64(bt_read16(dirty, pageN, off))
		k := make([]byte, klen)
		copy(k, dirty[off+4:off+4+klen])
		if len(k) == 0 || k[0] != 'a' {
			t.Fatalf("offset empaqueté %d clé=%q hors préfixe a", off, k)
		}
		seen[string(k)] = true
	}
	if !seen["aa"] || !seen["ab"] || seen["ba"] {
		t.Fatalf("clés préfixe a: %v", seen)
	}

	bad := Db_bt_scan_prefix(dirty, 16383, []byte("a"), 1, packed, uint64(len(packed)))
	if bad.Ok != 0 || bad.Nfound != 0 {
		t.Fatalf("n=16383 doit rejeter: %+v", bad)
	}
}

func TestDbBtree_DelHeapMVCC(t *testing.T) {
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

	k1 := []byte("mvcc-k1")
	k2 := []byte("mvcc-k2")
	v1_1 := []byte("v1.1")
	v1_2 := []byte("v1.2")
	v2_1 := []byte("v2.1")
	id1 := mkID(1)
	id2 := mkID(2)

	ins1 := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k1, uint64(len(k1)), id1, v1_1, uint64(len(v1_1)))
	if ins1.Ok != 1 {
		t.Fatalf("ins1: %+v", ins1)
	}
	copy(pub, dirty)
	hst = ins1

	ins2 := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k1, uint64(len(k1)), id2, v1_2, uint64(len(v1_2)))
	if ins2.Ok != 1 {
		t.Fatalf("ins2: %+v", ins2)
	}
	copy(pub, dirty)
	hst = ins2

	ins3 := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k2, uint64(len(k2)), id1, v2_1, uint64(len(v2_1)))
	if ins3.Ok != 1 {
		t.Fatalf("ins3: %+v", ins3)
	}
	copy(pub, dirty)
	hst = ins3

	if hst.Nkeys != 3 {
		t.Fatalf("nkeys=%d want 3", hst.Nkeys)
	}

	del := Db_bt_del_heap(pub, dirty, nbytes, npages, &hst, k1, uint64(len(k1)))
	if del.Ok != 1 || del.Nkeys != 1 {
		t.Fatalf("del: %+v", del)
	}

	out := make([]byte, 16)
	g1 := Db_bt_get_as_of_heap(dirty, nbytes, npages, del.Root, k1, uint64(len(k1)), id1, out, 16)
	if g1.Found != 0 {
		t.Fatalf("k1/id1 trouvé après del: %+v", g1)
	}
	g2 := Db_bt_get_as_of_heap(dirty, nbytes, npages, del.Root, k1, uint64(len(k1)), id2, out, 16)
	if g2.Found != 0 {
		t.Fatalf("k1/id2 trouvé après del: %+v", g2)
	}

	g3 := Db_bt_get_as_of_heap(dirty, nbytes, npages, del.Root, k2, uint64(len(k2)), id1, out, 16)
	if g3.Found != 1 || g3.Len_ != uint64(len(v2_1)) || !bytes.Equal(out[:len(v2_1)], v2_1) {
		t.Fatalf("k2/id1 corrompu après del: %+v out=%q", g3, out[:g3.Len_])
	}
}
