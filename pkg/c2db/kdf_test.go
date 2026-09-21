// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
	"golang.org/x/sys/unix"
)

func testMaster() [32]byte {
	var m [32]byte
	for i := range m {
		m[i] = byte(i + 1)
	}
	return m
}

func TestDeriveTenantMACDistinct(t *testing.T) {
	master := testMaster()
	ep := c2uuidv7.Compose(1_700_000_000_000_000_000, 1)
	a := DeriveTenantMAC(master, 1, ep)
	b := DeriveTenantMAC(master, 2, ep)
	if a == b {
		t.Fatal("même maître, locataires 1 et 2 : clés MAC identiques")
	}
	a2 := DeriveTenantMAC(master, 1, ep)
	if a != a2 {
		t.Fatal("DeriveTenantMAC non déterministe")
	}
	ep2 := c2uuidv7.Compose(1_700_000_000_000_000_000, 2)
	c := DeriveTenantMAC(master, 1, ep2)
	if a == c {
		t.Fatal("même locataire, époques distinctes : clés MAC identiques")
	}
}

func TestTenantMACEscapeWAL(t *testing.T) {
	master := testMaster()
	ep := c2uuidv7.Compose(1_700_000_000_000_000_000, 7)
	ka := DeriveTenantMAC(master, 1, ep)
	kb := DeriveTenantMAC(master, 2, ep)
	path := t.TempDir() + "/escape-wal.img"
	w, err := CreateWAL(path, testImageSize, ka)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	var id [16]byte
	copy(id[:], []byte("escape-record-01"))
	if err := w.Append(Record{ID: id, Type: RecPut, Payload: []byte("tenant-a-only")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wb, err := OpenWAL(path, kb)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté")
		}
		t.Fatalf("OpenWAL B: %v", err)
	}
	defer wb.Close()
	_, err = wb.Replay()
	if !errors.Is(err, errWALBadTag) {
		t.Fatalf("évasion locataire: Replay avec clé B = %v, want errWALBadTag", err)
	}
	wa, err := OpenWAL(path, ka)
	if err != nil {
		t.Fatalf("OpenWAL A: %v", err)
	}
	defer wa.Close()
	recs, err := wa.Replay()
	if err != nil {
		t.Fatalf("Replay A: %v", err)
	}
	if len(recs) != 1 || string(recs[0].Payload) != "tenant-a-only" {
		t.Fatalf("Replay A: %+v", recs)
	}
}

func TestEpochRingRotateRevoke(t *testing.T) {
	master := testMaster()
	e1 := c2uuidv7.Compose(1_700_000_000_000_000_000, 1)
	e2 := c2uuidv7.Compose(1_700_000_000_000_000_001, 2)
	k1 := DeriveTenantMAC(master, 1, e1)
	k2 := DeriveTenantMAC(master, 1, e2)
	var ring EpochRing
	ring.Install(e1, k1)
	ring.Install(e2, k2)
	cur, ep, gen, ok := ring.Current()
	if !ok || ep != e2 || cur != k2 || gen == 0 {
		t.Fatalf("Current: ok=%v ep=%v gen=%d", ok, ep, gen)
	}
	got, g1, ok := ring.Lookup(e1)
	if !ok || got != k1 || g1 == 0 {
		t.Fatalf("Lookup e1 après rotation: ok=%v", ok)
	}
	if !ring.Revoke(e1) {
		t.Fatal("Revoke e1")
	}
	if _, _, ok := ring.Lookup(e1); ok {
		t.Fatal("Lookup e1 après révocation")
	}
	got, _, ok = ring.Lookup(e2)
	if !ok || got != k2 {
		t.Fatal("Lookup e2 après révocation e1")
	}
	path := t.TempDir() + "/epoch-wal.img"
	w, err := CreateWAL(path, testImageSize, k1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	var id [16]byte
	copy(id[:], []byte("epoch-old-rec-01"))
	if err := w.Append(Record{ID: id, Type: RecPut, Payload: []byte("under-e1")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	w2, err := OpenWAL(path, k2)
	if err != nil {
		t.Fatalf("OpenWAL k2: %v", err)
	}
	defer w2.Close()
	if _, err := w2.Replay(); !errors.Is(err, errWALBadTag) {
		t.Fatalf("archive e1 lue avec clé e2: %v", err)
	}
	old, _, ok := ring.Lookup(e1)
	if ok {
		t.Fatal("clé révoquée encore dans l'anneau")
	}
	_ = old
	w1, err := OpenWAL(path, k1)
	if err != nil {
		t.Fatalf("OpenWAL k1: %v", err)
	}
	defer w1.Close()
	recs, err := w1.Replay()
	if err != nil || len(recs) != 1 {
		t.Fatalf("clé e1 hors anneau (copie locale) doit encore vérifier: %v n=%d", err, len(recs))
	}
}

func TestOpenTenantShard(t *testing.T) {
	master := testMaster()
	ep := c2uuidv7.Compose(1_700_000_000_000_000_000, 3)
	dir := t.TempDir()
	s, err := OpenTenantShard(dir, master, 1, ep)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté")
		}
		t.Fatalf("OpenTenantShard: %v", err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mac := DeriveTenantMAC(master, 1, ep)
	s2 := mustOpenShard(t, dir, mac, 1)
	defer func() { _ = s2.Close() }()
	got, err := s2.Get([]byte("k"))
	if err != nil || string(got) != "v" {
		t.Fatalf("Get via MAC dérivée: err=%v val=%q", err, got)
	}
}

func TestDerivePageSealKey(t *testing.T) {
	master := testMaster()
	pageContent1 := make([]byte, pageN)
	pageContent2 := make([]byte, pageN)
	pageContent2[100] = 0xFF

	k1 := DerivePageSealKey(master, 1, 0, pageContent1)
	k2 := DerivePageSealKey(master, 1, 0, pageContent1)
	if k1 != k2 {
		t.Fatal("DerivePageSealKey non déterministe")
	}

	// Shard différent
	kShard := DerivePageSealKey(master, 2, 0, pageContent1)
	if k1 == kShard {
		t.Fatal("DerivePageSealKey : même clé pour deux shards distincts")
	}

	// PageIdx différent
	kPage := DerivePageSealKey(master, 1, 1, pageContent1)
	if k1 == kPage {
		t.Fatal("DerivePageSealKey : même clé pour deux pageIdx distincts")
	}

	// Contenu différent
	kContent := DerivePageSealKey(master, 1, 0, pageContent2)
	if k1 == kContent {
		t.Fatal("DerivePageSealKey : même clé pour deux contenus distincts")
	}

	// 0 allocation
	allocs := testing.AllocsPerRun(100, func() {
		_ = DerivePageSealKey(master, 1, 0, pageContent1)
	})
	if allocs > 0 {
		t.Fatalf("DerivePageSealKey alloue %f fois par run (attendu 0)", allocs)
	}
}

func TestDeriveWALSealKey(t *testing.T) {
	master := testMaster()
	msg1 := make([]byte, 4076)
	msg2 := make([]byte, 4076)
	msg2[50] = 0xAA

	k1 := DeriveWALSealKey(master, msg1)
	k2 := DeriveWALSealKey(master, msg1)
	if k1 != k2 {
		t.Fatal("DeriveWALSealKey non déterministe")
	}

	kDiff := DeriveWALSealKey(master, msg2)
	if k1 == kDiff {
		t.Fatal("DeriveWALSealKey : même clé pour deux messages WAL distincts")
	}

	// 0 allocation
	allocs := testing.AllocsPerRun(100, func() {
		_ = DeriveWALSealKey(master, msg1)
	})
	if allocs > 0 {
		t.Fatalf("DeriveWALSealKey alloue %f fois par run (attendu 0)", allocs)
	}
}
