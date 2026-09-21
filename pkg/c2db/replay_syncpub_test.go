package c2db

import (
	"bytes"
	"fmt"
	"testing"
)

// TestReplaySyncPubLinearInDirtyPages mord sur le coût du rejeu. syncPubFromDirty
// réaligne s.pub sur s.dirty après chaque enregistrement rejoué. Tant qu'il
// recopie tout le tas utilisé, le rejeu d'un journal de N enregistrements coûte
// O(N x heapUsed) : quadratique sur un journal volumineux, la cause du blocage
// de 35 minutes observé sur le shard c2vec. L'observabilité ReplaySyncPagesCopied
// comptabilise les pages recopiées ; le coût doit rester proportionnel au nombre
// d'enregistrements (profondeur d'arbre + pages neuves), jamais au produit
// N x heapUsed.
//
// Le shard est peuplé puis fermé sans vidange du pager : data.img ne porte que
// la page 0, si bien que la réouverture rejoue réellement les N enregistrements
// et non des versions déjà durables.
func TestReplaySyncPubLinearInDirtyPages(t *testing.T) {
	dir := t.TempDir()
	var mk [32]byte
	for i := range mk {
		mk[i] = byte(i + 0x91)
	}
	const pages = 8192
	const n = 200
	const population = 2000
	bulk := bytes.Repeat([]byte("x"), 20000)
	opts := []Option{WithHeapPages(pages), WithWALBytes(128 * 1024 * 1024)}

	s := mustOpenShard(t, dir, mk, 9, opts...)
	// Réserver les emplacements du pager : sans cela, alloc() déclenche des
	// vidages qui persisteraient les pages dans data.img et feraient sauter le
	// rejeu par idempotence, ce qui masquerait le coût mesuré.
	if err := s.pager.Reserve(int(pages)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	// Peupler un grand tas : des valeurs de 20 Ko occupent plusieurs pages
	// d'overflow chacune, ce qui porte heapUsed à plusieurs milliers de pages,
	// condition du coût O(N x heapUsed) dénoncé.
	for i := 0; i < population; i++ {
		k := []byte(fmt.Sprintf("replay-bulk-%06d", i))
		if err := s.Put(k, bulk); err != nil {
			t.Fatalf("Put population %d: %v", i, err)
		}
	}
	// Pointage : le grand tas devient durable dans data.img. La queue du journal
	// qui suit sera la seule à être rejouée.
	if err := s.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	keys := make([][]byte, n)
	vals := make([][]byte, n)
	for i := 0; i < n; i++ {
		keys[i] = []byte(fmt.Sprintf("replay-linear-%06d", i))
		vals[i] = []byte(fmt.Sprintf("valeur-%06d", i))
		if err := s.Put(keys[i], vals[i]); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	if err := s.wal.Flush(); err != nil {
		t.Fatalf("wal.Flush: %v", err)
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	s2 := mustOpenShard(t, dir, mk, 9, opts...)
	defer func() { _ = s2.Close() }()

	// Oracle de lecture : l'état rejoué doit être identique à l'état vivant.
	for i := 0; i < n; i++ {
		got, err := s2.Get(keys[i])
		if err != nil || !bytes.Equal(got, vals[i]) {
			t.Fatalf("Get %q après rejeu: err=%v val=%q attendu %q", keys[i], err, got, vals[i])
		}
	}

	copied := s2.ReplaySyncPagesCopied
	// Borne de linéarité : le coût doit rester très en deçà du produit
	// N x heapUsed. Avant correctif, chaque enregistrement recopie tout le tas,
	// soit au moins N x heapUsed pages. Après correctif, il ne recopie que les
	// pages divergentes de l'enregistrement (profondeur + pages neuves).
	bound := uint64(n) * s2.heapUsed / 8
	t.Logf("rejeu: N=%d heapUsed=%d pages_copiees=%d borne=%d", n, s2.heapUsed, copied, bound)
	if copied > bound {
		t.Fatalf("syncPubFromDirty non lineaire: %d pages copiees > borne %d "+
			"(cout observe O(N x heapUsed))", copied, bound)
	}
}

// TestReplaySyncPubDeleteParity couvre la parité sur le chemin de suppression :
// après un rejeu sur pager non vidé, l'état lisible (Get, GetAsOf, absence des
// clés supprimées) est identique à l'état obtenu par le chemin unitaire.
func TestReplaySyncPubDeleteParity(t *testing.T) {
	dir := t.TempDir()
	var mk [32]byte
	for i := range mk {
		mk[i] = byte(i + 0xC3)
	}
	const pages = 4096
	opts := []Option{WithHeapPages(pages), WithWALBytes(64 * 1024 * 1024)}

	s := mustOpenShard(t, dir, mk, 10, opts...)
	if err := s.pager.Reserve(int(pages)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	var maxSnap [16]byte
	for i := range maxSnap {
		maxSnap[i] = 0xFF
	}
	const total = 400
	kept := make([]string, 0, total)
	for i := 0; i < total; i++ {
		k := []byte(fmt.Sprintf("replay-del-%06d", i))
		if err := s.Put(k, []byte("v")); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		if i%2 == 0 {
			if err := s.Delete(k); err != nil {
				t.Fatalf("Delete %d: %v", i, err)
			}
		} else {
			kept = append(kept, string(k))
		}
	}
	if err := s.wal.Flush(); err != nil {
		t.Fatalf("wal.Flush: %v", err)
	}
	if err := s.CloseWithoutFlush(); err != nil {
		t.Fatalf("CloseWithoutFlush: %v", err)
	}

	s2 := mustOpenShard(t, dir, mk, 10, opts...)
	defer func() { _ = s2.Close() }()
	for i := 0; i < total; i++ {
		k := []byte(fmt.Sprintf("replay-del-%06d", i))
		_, err := s2.Get(k)
		if i%2 == 0 {
			if err == nil {
				t.Fatalf("clé supprimée %q ressuscitée par le rejeu", k)
			}
			continue
		}
		if err != nil {
			t.Fatalf("clé conservée %q absente après rejeu: %v", k, err)
		}
		if _, err := s2.GetAsOf(k, maxSnap); err != nil {
			t.Fatalf("GetAsOf %q après rejeu: %v", k, err)
		}
	}
	if len(kept) == 0 {
		t.Fatal("précondition: aucune clé conservée")
	}
}
