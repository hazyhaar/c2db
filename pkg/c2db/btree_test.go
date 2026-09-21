// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDbBtree_CoWPubUntouched(t *testing.T) {
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
	snap := append([]byte(nil), pub...)

	key := []byte("alpha")
	val := []byte("bravo")
	got := Db_bt_insert(pub, dirty, pageN, &st, key, uint64(len(key)), val, uint64(len(val)))
	if got.Ok != 1 || got.Copied != 1 || got.Nkeys != 1 {
		t.Fatalf("insert: %+v", got)
	}
	if !bytes.Equal(pub, snap) {
		t.Fatalf("pub muté par insert CoW")
	}

	out := make([]byte, 16)
	gDirty := Db_bt_get(dirty, pageN, key, uint64(len(key)), out, uint64(len(out)))
	if gDirty.Found != 1 || gDirty.Len_ != uint64(len(val)) {
		t.Fatalf("get dirty: %+v", gDirty)
	}
	if !bytes.Equal(out[:len(val)], val) {
		t.Fatalf("valeur dirty=%q", out[:len(val)])
	}
	gPub := Db_bt_get(pub, pageN, key, uint64(len(key)), out, uint64(len(out)))
	if gPub.Found != 0 {
		t.Fatalf("get pub a trouvé la clé avant publication")
	}
}

func TestDbBtree_InsertGet(t *testing.T) {
	const pageN = uint64(16384)
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}

	key := []byte("k1")
	val := []byte("v1")
	got := Db_bt_insert(pub, dirty, pageN, &st, key, uint64(len(key)), val, uint64(len(val)))
	if got.Ok != 1 || got.Nkeys != 1 {
		t.Fatalf("insert: %+v", got)
	}
	out := make([]byte, 8)
	g := Db_bt_get(dirty, pageN, key, uint64(len(key)), out, uint64(len(out)))
	if g.Found != 1 || g.Len_ != 2 || out[0] != 'v' || out[1] != '1' {
		t.Fatalf("get: %+v out=%q", g, out[:2])
	}

	val2 := []byte("v2xx")
	got2 := Db_bt_insert(dirty, dirty, pageN, &got, key, uint64(len(key)), val2, uint64(len(val2)))
	if got2.Ok != 1 || got2.Nkeys != 1 {
		t.Fatalf("replace: %+v", got2)
	}
	out2 := make([]byte, 8)
	g2 := Db_bt_get(dirty, pageN, key, uint64(len(key)), out2, uint64(len(out2)))
	if g2.Found != 1 || g2.Len_ != 4 || !bytes.Equal(out2[:4], val2) {
		t.Fatalf("get replace: %+v out=%q", g2, out2[:4])
	}

	pub2 := append([]byte(nil), dirty...)
	rej := Db_bt_insert(pub2, dirty, pageN, &got2, nil, 0, val, uint64(len(val)))
	if rej.Ok != 0 {
		t.Fatalf("klen==0 doit rejeter, ok=%d", rej.Ok)
	}
	if rej.Copied != 1 {
		t.Fatalf("rejet klen==0 après copie, copied=%d", rej.Copied)
	}

	keyB := []byte("k2")
	valB := []byte("ok")
	resume := Db_bt_insert(pub2, dirty, pageN, &rej, keyB, uint64(len(keyB)), valB, uint64(len(valB)))
	if resume.Ok != 1 || resume.Nkeys != 2 {
		t.Fatalf("reprise après rejet: %+v", resume)
	}
	outB := make([]byte, 8)
	gB := Db_bt_get(dirty, pageN, keyB, uint64(len(keyB)), outB, uint64(len(outB)))
	if gB.Found != 1 || gB.Len_ != 2 || !bytes.Equal(outB[:2], valB) {
		t.Fatalf("get reprise: %+v", gB)
	}
	gOld := Db_bt_get(dirty, pageN, key, uint64(len(key)), out2, uint64(len(out2)))
	if gOld.Found != 1 || gOld.Len_ != 4 {
		t.Fatalf("clé remplacée perdue après reprise: %+v", gOld)
	}

	big := make([]byte, 20000)
	too := Db_bt_insert(pub2, make([]byte, pageN), pageN, &resume, big, uint64(len(big)), valB, 2)
	if too.Ok != 0 {
		t.Fatalf("klen trop grand doit rejeter")
	}
}

func TestDbBtree_DevicePublish(t *testing.T) {
	const pageN = uint64(16384)
	dir := t.TempDir()
	path := dir + "/c2db-btree.img"
	dev, err := Create(path, testImageSize)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Create: %v", err)
	}

	pub := mmapAligned(t, int(pageN))
	dirty := mmapAligned(t, int(pageN))
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	key := []byte("disk")
	val := []byte("yes")
	got := Db_bt_insert(pub, dirty, pageN, &st, key, uint64(len(key)), val, uint64(len(val)))
	if got.Ok != 1 {
		t.Fatalf("insert: %+v", got)
	}
	if err := dev.Write(0, dirty); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dev, err = Open(path)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = dev.Close() }()

	readBack := mmapAligned(t, int(pageN))
	if err := dev.Read(0, readBack); err != nil {
		t.Fatalf("Read: %v", err)
	}
	out := make([]byte, 8)
	g := Db_bt_get(readBack, pageN, key, uint64(len(key)), out, uint64(len(out)))
	if g.Found != 1 || g.Len_ != 3 || !bytes.Equal(out[:3], val) {
		t.Fatalf("get persisté: %+v out=%q", g, out[:3])
	}
	gPub := Db_bt_get(pub, pageN, key, uint64(len(key)), out, uint64(len(out)))
	if gPub.Found != 0 {
		t.Fatalf("pub a la clé sans publication mémoire")
	}
}

func TestDbBtree_SplitOrChain(t *testing.T) {
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
	val := make([]byte, 400)
	for i := range val {
		val[i] = byte(i * 3)
	}

	overflowAt := -1
	for i := 0; i < 128; i++ {
		key := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', '-', '-'}
		snap := append([]byte(nil), pub...)
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, key, uint64(len(key)), val, uint64(len(val)))
		if got.Ok != 1 {
			t.Fatalf("insert %d rejeté: %+v", i, got)
		}
		if !bytes.Equal(pub, snap) {
			t.Fatalf("pub muté à l'insert %d", i)
		}
		if got.Used > 1 {
			overflowAt = i
			hst = got
			break
		}
		copy(pub, dirty)
		hst = got
	}
	if overflowAt < 0 {
		t.Fatalf("aucune scission après remplissage")
	}
	if hst.Used != 2 || hst.Root != 0 {
		t.Fatalf("après scission: %+v", hst)
	}
	if hst.Nkeys != uint64(overflowAt+1) {
		t.Fatalf("nkeys=%d want %d", hst.Nkeys, overflowAt+1)
	}

	out := make([]byte, len(val))
	for i := 0; i <= overflowAt; i++ {
		key := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', '-', '-'}
		g := Db_bt_get_heap(dirty, nbytes, npages, hst.Root, key, uint64(len(key)), out, uint64(len(out)))
		if g.Found != 1 || g.Len_ != uint64(len(val)) || !bytes.Equal(out, val) {
			t.Fatalf("get clé %d après scission: %+v", i, g)
		}
	}
	newKey := []byte{byte(overflowAt), byte(overflowAt >> 8), 'k', 'e', 'y', '-', '-', '-'}
	gPub := Db_bt_get_heap(pub, nbytes, npages, 0, newKey, uint64(len(newKey)), out, uint64(len(out)))
	if gPub.Found != 0 {
		t.Fatalf("get pub a trouvé la clé d'overflow avant publication")
	}

	p1 := make([]byte, pageN)
	d1 := make([]byte, pageN)
	if init1 := Db_bt_leaf_init(p1, pageN); init1.Ok != 1 {
		t.Fatalf("init 1 page rejeté")
	}
	h1 := Db_bt_heap_st_t{Root: 0, Used: 1, Nkeys: 0, Ok: 1}
	rejected := false
	for i := 0; i < 128; i++ {
		key := []byte{byte(i), byte(i >> 8), 'r', 'e', 'j', '-', '-', '-'}
		got := Db_bt_insert_heap(p1, d1, pageN, 1, &h1, key, uint64(len(key)), val, uint64(len(val)))
		if got.Ok != 1 {
			p2 := make([]byte, nbytes)
			d2 := make([]byte, nbytes)
			copy(p2, p1)
			snap := append([]byte(nil), p2...)
			h2 := h1
			resume := Db_bt_insert_heap(p2, d2, nbytes, npages, &h2, key, uint64(len(key)), val, uint64(len(val)))
			if resume.Ok != 1 || resume.Used != 2 {
				t.Fatalf("reprise après rejet 1 page: %+v", resume)
			}
			if !bytes.Equal(p2, snap) {
				t.Fatalf("pub muté à la reprise")
			}
			gNew := Db_bt_get_heap(d2, nbytes, npages, resume.Root, key, uint64(len(key)), out, uint64(len(out)))
			if gNew.Found != 1 {
				t.Fatalf("clé d'overflow absente après reprise")
			}
			oldKey := []byte{0, 0, 'r', 'e', 'j', '-', '-', '-'}
			gOld := Db_bt_get_heap(d2, nbytes, npages, resume.Root, oldKey, uint64(len(oldKey)), out, uint64(len(out)))
			if gOld.Found != 1 {
				t.Fatalf("ancienne clé perdue après reprise")
			}
			rejected = true
			break
		}
		copy(p1, d1)
		h1 = got
	}
	if !rejected {
		t.Fatalf("npages=1 n'a pas rejeté à saturation")
	}
}

func TestDbBtree_InternalDefault(t *testing.T) {
	const pageN = uint64(16384)
	const npages = uint64(3)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub[:pageN], pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Nkeys: 0, Ok: 1}
	val := make([]byte, 400)
	for i := range val {
		val[i] = byte(i * 3)
	}

	overflowAt := -1
	for i := 0; i < 128; i++ {
		key := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', '-', '-'}
		snap := append([]byte(nil), pub...)
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, key, uint64(len(key)), val, uint64(len(val)))
		if got.Ok != 1 {
			t.Fatalf("insert %d rejeté: %+v", i, got)
		}
		if !bytes.Equal(pub, snap) {
			t.Fatalf("pub muté à l'insert %d", i)
		}
		if got.Used > 1 {
			overflowAt = i
			hst = got
			break
		}
		copy(pub, dirty)
		hst = got
	}
	if overflowAt < 0 {
		t.Fatalf("aucune scission après remplissage")
	}
	if hst.Used != 3 {
		t.Fatalf("après promotion: %+v", hst)
	}
	rootOff := hst.Root * pageN
	if uint64(len(dirty)) <= rootOff+20 {
		t.Fatalf("root hors tas: %+v", hst)
	}
	if dirty[rootOff+20] != 2 {
		t.Fatalf("root type=%d want TYPE_INTERNAL=2 root=%d", dirty[rootOff+20], hst.Root)
	}
	if hst.Nkeys != uint64(overflowAt+1) {
		t.Fatalf("nkeys=%d want %d", hst.Nkeys, overflowAt+1)
	}

	out := make([]byte, len(val))
	for i := 0; i <= overflowAt; i++ {
		key := []byte{byte(i), byte(i >> 8), 'k', 'e', 'y', '-', '-', '-'}
		g := Db_bt_get_heap(dirty, nbytes, npages, hst.Root, key, uint64(len(key)), out, uint64(len(out)))
		if g.Found != 1 || g.Len_ != uint64(len(val)) || !bytes.Equal(out, val) {
			t.Fatalf("get clé %d après nœud interne: %+v", i, g)
		}
	}
	newKey := []byte{byte(overflowAt), byte(overflowAt >> 8), 'k', 'e', 'y', '-', '-', '-'}
	gPub := Db_bt_get_heap(pub, nbytes, npages, 0, newKey, uint64(len(newKey)), out, uint64(len(out)))
	if gPub.Found != 0 {
		t.Fatalf("get pub a trouvé la clé d'overflow avant publication")
	}
}

func TestDbBtree_InternalSplit(t *testing.T) {
	const pageN = uint64(16384)
	const npages = uint64(8)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub[:pageN], pageN)
	if st.Ok != 1 {
		t.Fatalf("init rejeté")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Nkeys: 0, Ok: 1}
	val := make([]byte, 4000)
	for i := range val {
		val[i] = byte(i * 3)
	}

	var keys [][]byte
	promoted := false
	grew := false

	for i := 0; i < 32; i++ {
		key := make([]byte, 4000)
		key[0] = byte(i)
		key[1] = byte(i >> 8)
		for j := 2; j < len(key); j++ {
			key[j] = byte(j + i)
		}
		snap := append([]byte(nil), pub...)
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, key, uint64(len(key)), val, uint64(len(val)))
		if got.Ok != 1 {
			if !grew {
				t.Fatalf("insert %d rejeté: %+v", i, got)
			}
			break
		}
		if !bytes.Equal(pub, snap) {
			t.Fatalf("pub muté à l'insert %d", i)
		}
		rootOff := got.Root * pageN
		if uint64(len(dirty)) <= rootOff+20 {
			t.Fatalf("root hors tas: %+v", got)
		}
		if got.Used >= 3 {
			if dirty[rootOff+20] != 2 {
				t.Fatalf("root type=%d want TYPE_INTERNAL=2 insert=%d root=%d", dirty[rootOff+20], i, got.Root)
			}
			if !promoted {
				promoted = true
			} else if got.Used > 3 {
				grew = true
			}
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, key)
	}
	if !promoted {
		t.Fatalf("aucune promotion interne")
	}
	if !grew {
		t.Fatalf("aucune scission interne: %+v", hst)
	}
	rootOff := hst.Root * pageN
	if pub[rootOff+20] != 2 {
		t.Fatalf("root type=%d want TYPE_INTERNAL=2 root=%d", pub[rootOff+20], hst.Root)
	}

	out := make([]byte, len(val))
	for i, key := range keys {
		g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, key, uint64(len(key)), out, uint64(len(out)))
		if g.Found != 1 || g.Len_ != uint64(len(val)) || !bytes.Equal(out, val) {
			t.Fatalf("get clé %d après scission interne: %+v", i, g)
		}
	}

	snapPub := append([]byte(nil), pub...)
	extra := make([]byte, 4000)
	extra[0] = 0xFF
	extra[1] = 0xFE
	for j := 2; j < len(extra); j++ {
		extra[j] = 0xA5
	}
	tail := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, extra, uint64(len(extra)), val, uint64(len(val)))
	if !bytes.Equal(pub, snapPub) {
		t.Fatalf("pub muté à l'insert terminal")
	}
	if tail.Ok == 1 {
		gDirty := Db_bt_get_heap(dirty, nbytes, npages, tail.Root, extra, uint64(len(extra)), out, uint64(len(out)))
		if gDirty.Found != 1 {
			t.Fatalf("clé terminale absente du dirty")
		}
		gP := Db_bt_get_heap(pub, nbytes, npages, hst.Root, extra, uint64(len(extra)), out, uint64(len(out)))
		if gP.Found != 0 {
			t.Fatalf("get pub a trouvé la clé terminale avant publication")
		}
		if dirty[tail.Root*pageN+20] != 2 {
			t.Fatalf("root type=%d après insert terminal", dirty[tail.Root*pageN+20])
		}
	}
}

func TestDbBtree_ElevateRoot(t *testing.T) {
	const npages = uint64(16)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub[:pageN], pageN)
	if st.Ok != 1 {
		t.Fatal("init")
	}
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	val := make([]byte, 4000)
	var keys [][]byte
	var firstIntern uint64
	elevated := false
	for i := 0; i < 64; i++ {
		key := make([]byte, 4000)
		key[0] = byte(i)
		key[1] = byte(i >> 8)
		snap := append([]byte(nil), pub...)
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, key, uint64(len(key)), val, uint64(len(val)))
		if got.Ok != 1 {
			break
		}
		if !bytes.Equal(pub, snap) {
			t.Fatalf("pub muté insert %d", i)
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, key)
		if dirty[hst.Root*pageN+20] != 2 {
			continue
		}
		if firstIntern == 0 {
			firstIntern = hst.Root
			continue
		}
		if hst.Root != firstIntern {
			elevated = true
			break
		}
	}
	if firstIntern == 0 {
		t.Fatal("pas de nœud interne")
	}
	if !elevated {
		t.Fatalf("pas d'élévation root=%d first=%d used=%d n=%d", hst.Root, firstIntern, hst.Used, len(keys))
	}
	out := make([]byte, len(val))
	for i, key := range keys {
		g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, key, uint64(len(key)), out, uint64(len(out)))
		if g.Found != 1 || g.Fallback != 0 {
			t.Fatalf("clé %d après élévation: %+v root=%d", i, g, hst.Root)
		}
	}
	root2 := hst.Root
	attached := false
	for i := len(keys); i < 128; i++ {
		key := make([]byte, 4000)
		key[0] = byte(i)
		key[1] = byte(i >> 8)
		key[2] = 0x5A
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, key, uint64(len(key)), val, uint64(len(val)))
		if got.Ok != 1 {
			break
		}
		copy(pub, dirty)
		if got.Root == root2 && got.Used > hst.Used {
			attached = true
		}
		hst = got
		keys = append(keys, key)
		if attached && len(keys) > 8 {
			break
		}
	}
	if !attached {
		t.Fatalf("pas de scission de fils interne used=%d root=%d n=%d", hst.Used, hst.Root, len(keys))
	}
	for i, key := range keys {
		g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, key, uint64(len(key)), out, uint64(len(out)))
		if g.Found != 1 || g.Fallback != 0 {
			t.Fatalf("clé %d après scission fils: %+v", i, g)
		}
	}
}

func TestDbBtree_VerHeap64FillGet(t *testing.T) {
	const npages = uint64(64)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatal("leaf_init")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	val := make([]byte, 1200)
	id := make([]byte, 16)
	var keys [][]byte
	for i := 0; i < 1000; i++ {
		k := []byte{byte(i), byte(i >> 8), 'f', 'u', 'l', 'l', '-', byte(i >> 16)}
		id[0] = byte(i)
		id[1] = byte(i >> 8)
		got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), id, val, uint64(len(val)))
		if got.Ok != 1 {
			break
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, k)
	}
	if len(keys) < 3 {
		t.Fatalf("trop peu de clés: %d used=%d", len(keys), hst.Used)
	}
	out := make([]byte, len(val))
	for i, k := range keys {
		g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, k, uint64(len(k)), out, uint64(len(out)))
		if g.Found != 1 {
			t.Fatalf("C get_heap clé %d absente used=%d n=%d", i, hst.Used, len(keys))
		}
	}
}

func TestDbBtree_GetNoFallback(t *testing.T) {
	const npages = uint64(64)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatal("leaf_init")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	val := make([]byte, 1200)
	id := make([]byte, 16)
	var keys [][]byte
	for i := 0; i < 1000; i++ {
		k := []byte{byte(i), byte(i >> 8), 'r', 'o', 'u', 't', '-', byte(i >> 16)}
		id[0] = byte(i)
		id[1] = byte(i >> 8)
		got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), id, val, uint64(len(val)))
		if got.Ok != 1 {
			break
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, k)
	}
	if len(keys) < 3 {
		t.Fatalf("trop peu de clés: %d used=%d", len(keys), hst.Used)
	}
	rootOff := hst.Root * pageN
	if pub[rootOff+20] != 2 {
		t.Fatalf("racine non interne type=%d used=%d n=%d", pub[rootOff+20], hst.Used, len(keys))
	}
	out := make([]byte, len(val))
	for i, k := range keys {
		g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, k, uint64(len(k)), out, uint64(len(out)))
		if g.Found != 1 {
			t.Fatalf("clé %d absente used=%d n=%d", i, hst.Used, len(keys))
		}
		if g.Fallback != 0 {
			ch := Db_bt_child_of(pub, nbytes, npages, hst.Root, k, uint64(len(k)))
			t.Fatalf("clé %d via repli walked=%d child=%d used=%d n=%d root=%d k0=%d k1=%d", i, g.Walked, ch, hst.Used, len(keys), hst.Root, k[0], k[1])
		}
	}
	miss := []byte{0xFF, 0xFE, 'm', 'i', 's', 's', '-', 0}
	g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, miss, uint64(len(miss)), out, uint64(len(out)))
	if g.Found != 0 {
		t.Fatalf("miss trouvé: %+v", g)
	}
	if g.Fallback != 0 {
		t.Fatalf("miss a marché la chaîne fallback=%d walked=%d", g.Fallback, g.Walked)
	}
}

func TestDbBtree_MergeU64(t *testing.T) {
	a := []uint64{1, 3, 5, 9}
	b := []uint64{2, 3, 8}
	out := make([]uint64, 8)
	n := Db_bt_merge_u64(a, uint64(len(a)), b, uint64(len(b)), out, uint64(len(out)))
	want := []uint64{1, 2, 3, 5, 8, 9}
	if n != uint64(len(want)) {
		t.Fatalf("n=%d want %d", n, len(want))
	}
	for i, w := range want {
		if out[i] != w {
			t.Fatalf("out[%d]=%d want %d", i, out[i], w)
		}
	}
}

func TestDbBtree_LeafHasPrefix(t *testing.T) {
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatal("init")
	}
	key := []byte("cmd/ab")
	val := []byte("v")
	ins := Db_bt_insert(pub, dirty, pageN, &st, key, uint64(len(key)), val, uint64(len(val)))
	if ins.Ok != 1 {
		t.Fatalf("insert: %+v", ins)
	}
	if Db_bt_leaf_has_prefix(dirty, pageN, []byte("cmd/"), 4) != 1 {
		t.Fatal("prefix cmd/ absent")
	}
	if Db_bt_leaf_has_prefix(dirty, pageN, []byte("zzz"), 3) != 0 {
		t.Fatal("prefix zzz présent")
	}
}

func TestDbBtree_MergeU64VsCOracle(t *testing.T) {
	srcDir := filepath.Join("c_src")
	bin := filepath.Join(t.TempDir(), "merge_oracle")
	cmd := exec.Command("gcc", "-O2", "-I", srcDir, filepath.Join(srcDir, "test_merge_oracle.c"), filepath.Join(srcDir, "db_btree.c"), "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	got, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("oracle: %v\n%s", err, got)
	}
	if string(bytes.TrimSpace(got)) != "N=6 1 2 3 5 8 9" {
		t.Fatalf("oracle %q", got)
	}
}

func TestDbBtree_InternalChildVsCOracle(t *testing.T) {
	const npages = uint64(8)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatal("leaf_init")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	val := make([]byte, 1200)
	id := make([]byte, 16)
	var keys [][]byte
	for i := 0; i < 40; i++ {
		k := []byte{byte(i), byte(i >> 8), 'o', 'r', 'a', 'c', '-', byte(i)}
		id[0] = byte(i)
		got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), id, val, uint64(len(val)))
		if got.Ok != 1 {
			break
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, k)
	}
	if len(keys) < 3 {
		t.Fatalf("trop peu: %d", len(keys))
	}
	var want bytes.Buffer
	out := make([]byte, 8)
	for i, k := range keys {
		ch := Db_bt_child_of(pub, nbytes, npages, hst.Root, k, uint64(len(k)))
		g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, k, uint64(len(k)), out, 8)
		fmt.Fprintf(&want, "%d %d %d %d %d\n", i, ch, g.Found, g.Fallback, g.Walked)
		if g.Found != 1 || g.Fallback != 0 {
			t.Fatalf("go clé %d found=%d fb=%d", i, g.Found, g.Fallback)
		}
	}
	miss := []byte{0xFF, 0xEE, 'm', 'i', 's', 's', '-', 0}
	mch := Db_bt_child_of(pub, nbytes, npages, hst.Root, miss, uint64(len(miss)))
	mg := Db_bt_get_heap(pub, nbytes, npages, hst.Root, miss, uint64(len(miss)), out, 8)
	fmt.Fprintf(&want, "miss %d %d %d %d\n", mch, mg.Found, mg.Fallback, mg.Walked)
	if mg.Found != 0 || mg.Fallback != 0 {
		t.Fatalf("go miss found=%d fb=%d", mg.Found, mg.Fallback)
	}

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "child_oracle")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_btree.c"
int main(void) {
    uint64_t npages = 8;
    uint64_t nbytes = npages * 16384ULL;
    uint8_t *pub = (uint8_t *)malloc((size_t)nbytes);
    uint8_t *dirty = (uint8_t *)malloc((size_t)nbytes);
    uint8_t val[1200];
    uint8_t id[16];
    uint8_t k[8];
    uint8_t out[8];
    uint8_t miss[8];
    db_bt_heap_st_t hst;
    db_bt_get_t g;
    uint64_t i;
    uint64_t n;
    uint64_t ch;
    if (pub == 0 || dirty == 0) return 1;
    memset(pub, 0, (size_t)nbytes);
    memset(dirty, 0, (size_t)nbytes);
    memset(val, 0, 1200);
    memset(id, 0, 16);
    if (db_bt_leaf_init(pub, 16384).ok == 0) return 1;
    memcpy(dirty, pub, (size_t)nbytes);
    hst.root = 0; hst.used = 1; hst.nkeys = 0; hst.pages = 0; hst.ok = 1;
    n = 0;
    for (i = 0; i < 40; i = i + 1) {
        k[0] = (uint8_t)i; k[1] = (uint8_t)(i >> 8);
        k[2] = 'o'; k[3] = 'r'; k[4] = 'a'; k[5] = 'c'; k[6] = '-'; k[7] = (uint8_t)i;
        id[0] = (uint8_t)i;
        hst = db_bt_insert_ver_heap(pub, dirty, nbytes, npages, hst, k, 8, id, val, 1200);
        if (hst.ok == 0) break;
        memcpy(pub, dirty, (size_t)nbytes);
        n = n + 1;
    }
    for (i = 0; i < n; i = i + 1) {
        k[0] = (uint8_t)i; k[1] = (uint8_t)(i >> 8);
        k[2] = 'o'; k[3] = 'r'; k[4] = 'a'; k[5] = 'c'; k[6] = '-'; k[7] = (uint8_t)i;
        ch = db_bt_child_of(pub, nbytes, npages, hst.root, k, 8);
        g = db_bt_get_heap(pub, nbytes, npages, hst.root, k, 8, out, 8);
        printf("%%llu %%llu %%u %%u %%llu\n", (unsigned long long)i, (unsigned long long)ch, (unsigned)g.found, (unsigned)g.fallback, (unsigned long long)g.walked);
    }
    miss[0] = 0xFF; miss[1] = 0xEE; miss[2] = 'm'; miss[3] = 'i';
    miss[4] = 's'; miss[5] = 's'; miss[6] = '-'; miss[7] = 0;
    ch = db_bt_child_of(pub, nbytes, npages, hst.root, miss, 8);
    g = db_bt_get_heap(pub, nbytes, npages, hst.root, miss, 8, out, 8);
    printf("miss %%llu %%u %%u %%llu\n", (unsigned long long)ch, (unsigned)g.found, (unsigned)g.fallback, (unsigned long long)g.walked);
    return 0;
}
`)
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("écriture C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, outb)
	}
	got, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("oracle: %v\n%s", err, got)
	}
	if string(got) != want.String() {
		t.Fatalf("oracle diverge\nC:\n%s\nGo:\n%s", got, want.String())
	}
}

func TestDbBtree_ElevateRootVsCOracle(t *testing.T) {
	const npages = uint64(16)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("init")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	val := make([]byte, 4000)
	var n uint64
	var first uint64
	for i := 0; i < 64; i++ {
		key := make([]byte, 4000)
		key[0] = byte(i)
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, key, 4000, val, 4000)
		if got.Ok != 1 {
			break
		}
		copy(pub, dirty)
		hst = got
		n++
		if pub[hst.Root*pageN+20] == 2 && first == 0 {
			first = hst.Root
		}
		if first != 0 && hst.Root != first {
			break
		}
	}
	if first == 0 || hst.Root == first {
		t.Fatalf("élévation absente first=%d root=%d n=%d", first, hst.Root, n)
	}
	out := make([]byte, 8)
	k0 := make([]byte, 4000)
	g0 := Db_bt_get_heap(pub, nbytes, npages, hst.Root, k0, 4000, out, 8)
	want := fmt.Sprintf("%d %d %d %d %d\n", n, hst.Root, hst.Used, g0.Found, g0.Fallback)
	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "el_oracle")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_btree.c"
int main(void) {
    uint64_t npages = 16, nbytes = npages * 16384ULL, i, n = 0, first = 0;
    uint8_t *pub = (uint8_t *)malloc((size_t)nbytes);
    uint8_t *dirty = (uint8_t *)malloc((size_t)nbytes);
    uint8_t key[4000], val[4000], out[8], k0[1];
    db_bt_heap_st_t hst; db_bt_get_t g;
    if (!pub || !dirty) return 1;
    memset(pub, 0, (size_t)nbytes); memset(dirty, 0, (size_t)nbytes);
    memset(val, 0, 4000);
    if (db_bt_leaf_init(pub, 16384).ok == 0) return 1;
    memcpy(dirty, pub, (size_t)nbytes);
    hst.root = 0; hst.used = 1; hst.ok = 1; hst.nkeys = 0; hst.pages = 0;
    for (i = 0; i < 64; i = i + 1) {
        memset(key, 0, 4000); key[0] = (uint8_t)i;
        hst = db_bt_insert_heap(pub, dirty, nbytes, npages, hst, key, 4000, val, 4000);
        if (hst.ok == 0) break;
        memcpy(pub, dirty, (size_t)nbytes);
        n = n + 1;
        if (pub[hst.root * 16384ULL + 20] == 2 && first == 0) first = hst.root;
        if (first != 0 && hst.root != first) break;
    }
    memset(key, 0, 4000);
    g = db_bt_get_heap(pub, nbytes, npages, hst.root, key, 4000, out, 8);
    printf("%%llu %%llu %%llu %%u %%u\n", (unsigned long long)n, (unsigned long long)hst.root,
        (unsigned long long)hst.used, (unsigned)g.found, (unsigned)g.fallback);
    return 0;
}
`)
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatal(err)
	}
	if outb, err := exec.Command("gcc", "-O2", "-o", cBin, srcFile).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, outb)
	}
	got, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("oracle: %v %s", err, got)
	}
	if string(got) != want {
		t.Fatalf("oracle diverge C=%q Go=%q", got, want)
	}
}

func TestDbBtree_DelMinRightGet(t *testing.T) {
	const npages = uint64(8)
	nbytes := npages * pageN
	pub := make([]byte, nbytes)
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("init")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	val := make([]byte, 4000)
	var keys [][]byte
	for i := 0; i < 16; i++ {
		k := make([]byte, 4000)
		k[0] = byte(i)
		got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, k, 4000, val, 4000)
		if got.Ok != 1 {
			break
		}
		copy(pub, dirty)
		hst = got
		keys = append(keys, k)
	}
	// Une insertion refusée peut laisser dirty partiellement modifiée ; le noyau
	// de suppression ne touche plus que le chemin de la feuille cible et ne
	// répare donc plus les pages hors chemin. On réaligne l'image comme le fait
	// le moteur via convergeDirtyFromPub après un échec d'insertion.
	copy(dirty, pub)
	if pub[hst.Root*pageN+20] != 2 {
		t.Fatalf("pas interne used=%d", hst.Used)
	}
	del := keys[len(keys)/2]
	st := Db_bt_del_heap(pub, dirty, nbytes, npages, &hst, del, 4000)
	if st.Ok != 1 {
		t.Fatalf("del: %+v", st)
	}
	copy(pub, dirty)
	hst = st
	out := make([]byte, 8)
	g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, del, 4000, out, 8)
	if g.Found != 0 || g.Fallback != 0 {
		t.Fatalf("deleted encore là: %+v", g)
	}
	for i, k := range keys {
		if bytes.Equal(k, del) {
			continue
		}
		g = Db_bt_get_heap(pub, nbytes, npages, hst.Root, k, 4000, out, 8)
		if g.Found != 1 || g.Fallback != 0 {
			t.Fatalf("clé %d perdue après del: %+v", i, g)
		}
	}
}

func TestDbBtree_FuzzThreeInvariants(t *testing.T) {
	const npages = uint64(32)
	nbytes := npages * pageN
	canary := byte(0xAA)
	rear := byte(0x55)
	buf := make([]byte, nbytes+2)
	buf[0] = canary
	buf[len(buf)-1] = rear
	pub := buf[1 : 1+nbytes]
	dirty := make([]byte, nbytes)
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("init")
	}
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	type pair struct{ k, v []byte }
	oracle := map[string][]byte{}
	var order []string
	rng := rand.New(rand.NewSource(42))
	run := func() {
		for i := 0; i < 80; i++ {
			k := []byte{byte(rng.Intn(40)), byte(i)}
			v := []byte{byte(rng.Intn(255)), 0x7}
			switch rng.Intn(3) {
			case 0, 1:
				got := Db_bt_insert_heap(pub, dirty, nbytes, npages, &hst, k, 2, v, 2)
				if got.Ok == 1 {
					copy(pub, dirty)
					hst = got
					sk := string(k)
					if _, ok := oracle[sk]; !ok {
						order = append(order, sk)
					}
					oracle[sk] = append([]byte(nil), v...)
				}
			default:
				if len(order) == 0 {
					continue
				}
				dk := order[0]
				order = order[1:]
				st := Db_bt_del_heap(pub, dirty, nbytes, npages, &hst, []byte(dk), 2)
				if st.Ok == 1 {
					copy(pub, dirty)
					hst = st
					delete(oracle, dk)
				}
			}
		}
		out := make([]byte, 8)
		for s, wantv := range oracle {
			g := Db_bt_get_heap(pub, nbytes, npages, hst.Root, []byte(s), 2, out, 8)
			if g.Found != 1 || g.Fallback != 0 || !bytes.Equal(out[:g.Len_], wantv) {
				t.Fatalf("oracle %q found=%d fb=%d", s, g.Found, g.Fallback)
			}
		}
	}
	run()
	if buf[0] != canary || buf[len(buf)-1] != rear {
		t.Fatal("canari mémoire")
	}
	or2 := map[string][]byte{}
	for k, v := range oracle {
		or2[k] = append([]byte(nil), v...)
	}
	rng = rand.New(rand.NewSource(42))
	if Db_bt_leaf_init(pub, pageN).Ok != 1 {
		t.Fatal("reinit")
	}
	copy(dirty, pub)
	hst = Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	oracle = map[string][]byte{}
	order = nil
	run()
	if buf[0] != canary || buf[len(buf)-1] != rear {
		t.Fatal("canari passe 2")
	}
	if len(oracle) != len(or2) {
		t.Fatalf("déterminisme n=%d want %d", len(oracle), len(or2))
	}
	for k, v := range or2 {
		if !bytes.Equal(oracle[k], v) {
			t.Fatalf("déterminisme clé %q", k)
		}
	}
}

func TestDbBtree_InsertZeroAlloc(t *testing.T) {
	pub := make([]byte, pageN)
	dirty := make([]byte, pageN)
	st := Db_bt_leaf_init(pub, pageN)
	if st.Ok != 1 {
		t.Fatal("init")
	}
	copy(dirty, pub)
	key := []byte("k")
	val := []byte("v")
	n := testing.AllocsPerRun(200, func() {
		_ = Db_bt_insert(pub, dirty, pageN, &st, key, 1, val, 1)
	})
	if n != 0 {
		t.Fatalf("insert allocs/op=%.2f want 0", n)
	}
}
