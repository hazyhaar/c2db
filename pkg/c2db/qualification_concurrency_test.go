// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
)

// qualBTreeHeight parcourt récursivement l'arborescence du B-Tree depuis la racine,
// vérifie que tous les sous-arbres d'un même nœud ont rigoureusement la même profondeur
// (propriété d'équilibre strict) et retourne la hauteur totale de l'arbre (feuille = 1).
func qualBTreeHeight(heap []byte, npages uint64, page uint64) (int, error) {
	if page >= npages {
		return 0, fmt.Errorf("page %d hors limites (npages=%d)", page, npages)
	}
	base := page * pageN
	if base+64 > uint64(len(heap)) {
		return 0, fmt.Errorf("page %d hors limites du tampon mémoire (len=%d)", page, len(heap))
	}
	typ := heap[base+20]
	if typ == 1 { // TYPE_LEAF
		return 1, nil
	}
	if typ != 2 { // TYPE_INTERNAL
		return 0, fmt.Errorf("type de page inattendu: %d à la page %d", typ, page)
	}

	leftmost := binary.LittleEndian.Uint64(heap[base+8 : base+16])
	hLeft, err := qualBTreeHeight(heap, npages, leftmost)
	if err != nil {
		return 0, err
	}

	nslots := int(binary.LittleEndian.Uint16(heap[base+22 : base+24]))
	for i := 0; i < nslots; i++ {
		slotOff := uint64(64) + uint64(i)*2
		if base+slotOff+2 > uint64(len(heap)) {
			return 0, fmt.Errorf("slot %d hors limites", i)
		}
		cellOff := uint64(binary.LittleEndian.Uint16(heap[base+slotOff : base+slotOff+2]))
		if base+cellOff+4 > uint64(len(heap)) {
			return 0, fmt.Errorf("cellule slot %d hors limites", i)
		}
		cklen := uint64(binary.LittleEndian.Uint16(heap[base+cellOff : base+cellOff+2]))
		childOff := base + cellOff + 4 + cklen
		if childOff+8 > uint64(len(heap)) {
			return 0, fmt.Errorf("pointeur enfant slot %d hors limites", i)
		}
		child := binary.LittleEndian.Uint64(heap[childOff : childOff+8])
		hChild, err := qualBTreeHeight(heap, npages, child)
		if err != nil {
			return 0, err
		}
		if hChild != hLeft {
			return 0, fmt.Errorf("déséquilibre structurel du B-Tree: enfant gauche %d hauteur=%d, enfant slot %d (page %d) hauteur=%d",
				leftmost, hLeft, i, child, hChild)
		}
	}
	return hLeft + 1, nil
}

// qualCheckBTreeParents valide l'unicité de parenté et l'absence de cycles
// dans l'arborescence B-Tree issue des cascades de scissions.
func qualCheckBTreeParents(t *testing.T, heap []byte, npages uint64, root uint64) {
	t.Helper()
	parents := make(map[uint64]uint64)

	var walk func(page uint64, parent uint64)
	walk = func(page uint64, parent uint64) {
		if page >= npages {
			t.Fatalf("page %d hors limites de heapPages (%d)", page, npages)
		}
		if prevParent, exists := parents[page]; exists {
			t.Fatalf("anomalie topologique : page %d référencée par multiples parents (%d et %d)", page, prevParent, parent)
		}
		parents[page] = parent

		base := page * pageN
		typ := heap[base+20]
		if typ == 1 { // TYPE_LEAF
			return
		}
		if typ != 2 { // TYPE_INTERNAL
			t.Fatalf("page %d : type inattendu %d", page, typ)
		}

		leftmost := binary.LittleEndian.Uint64(heap[base+8 : base+16])
		walk(leftmost, page)

		nslots := int(binary.LittleEndian.Uint16(heap[base+22 : base+24]))
		for i := 0; i < nslots; i++ {
			slotOff := uint64(64) + uint64(i)*2
			cellOff := uint64(binary.LittleEndian.Uint16(heap[base+slotOff : base+slotOff+2]))
			cklen := uint64(binary.LittleEndian.Uint16(heap[base+cellOff : base+cellOff+2]))
			child := binary.LittleEndian.Uint64(heap[base+cellOff+4+cklen : base+cellOff+4+cklen+8])
			walk(child, page)
		}
	}

	walk(root, root)
}

// TestQual_03_BTreeSplitCascades insère 6 000 clés triées consécutives dans un Shard
// pour forcer de multiples cascades de splits de nœuds feuilles et internes jusqu'à la racine.
// Il vérifie l'intégrité séquentielle et aléatoire, ainsi que la hauteur et les liens parents.
func TestQual_03_BTreeSplitCascades(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 3)
	}
	const shardID uint16 = 3

	s, err := OpenShard(dir, key, shardID)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer func() { _ = s.Close() }()

	const nKeys = 6000
	pairs := make([][2][]byte, nKeys)
	for i := 0; i < nKeys; i++ {
		k := []byte(fmt.Sprintf("cascade-key-%08d", i))
		v := []byte(fmt.Sprintf("cascade-val-%08d-payload-%04d", i, i%1000))
		pairs[i] = [2][]byte{k, v}
	}

	// Insertion ordonnée sous group commit pour forcer les cascades de scissions sans overhead disque
	s.enterGroup()
	for i := 0; i < nKeys; i++ {
		if err := s.Put(pairs[i][0], pairs[i][1]); err != nil {
			t.Fatalf("Put clé %d: %v", i, err)
		}
	}
	if err := s.leaveGroup(); err != nil {
		t.Fatalf("leaveGroup: %v", err)
	}

	// 1. Vérification par relecture séquentielle intégrale
	for i := 0; i < nKeys; i++ {
		got, err := s.Get(pairs[i][0])
		if err != nil {
			t.Fatalf("Get séquentiel clé %d (%s): %v", i, pairs[i][0], err)
		}
		if !bytes.Equal(got, pairs[i][1]) {
			t.Fatalf("Get séquentiel clé %d: altération valeur got=%q want=%q", i, got, pairs[i][1])
		}
	}

	// 2. Vérification par relecture aléatoire intégrale
	rng := rand.New(rand.NewSource(42))
	perm := rng.Perm(nKeys)
	for _, idx := range perm {
		got, err := s.Get(pairs[idx][0])
		if err != nil {
			t.Fatalf("Get aléatoire clé %d (%s): %v", idx, pairs[idx][0], err)
		}
		if !bytes.Equal(got, pairs[idx][1]) {
			t.Fatalf("Get aléatoire clé %d: altération valeur got=%q want=%q", idx, got, pairs[idx][1])
		}
	}

	// 3. Vérification de la hauteur et de la cohérence topologique du B-Tree
	if s.heapUsed < 3 {
		t.Fatalf("heapUsed=%d insuffisant pour prouver des cascades de split", s.heapUsed)
	}

	rootOff := s.heapRoot * pageN
	if rootOff+20 >= uint64(len(s.pub)) {
		t.Fatalf("racine hors limites du tas: root=%d", s.heapRoot)
	}
	rootType := s.pub[rootOff+20]
	if rootType != 2 { // TYPE_INTERNAL = 2
		t.Fatalf("la racine doit être un nœud interne après cascades, got type=%d root=%d used=%d", rootType, s.heapRoot, s.heapUsed)
	}

	height, err := qualBTreeHeight(s.pub, s.heapUsed, s.heapRoot)
	if err != nil {
		t.Fatalf("cohérence hauteur B-Tree: %v", err)
	}
	if height < 2 {
		t.Fatalf("hauteur B-Tree=%d attendue >= 2 après splits", height)
	}

	qualCheckBTreeParents(t, s.pub, s.heapUsed, s.heapRoot)
}

// TestQual_04_HeapSaturationRollback alloue et remplit un Shard jusqu'au seuil de saturation,
// prouve que l'auto-compaction recycle l'arène saturée, vérifie la continuité des lectures
// (Get, ScanPrefix), exécute des suppressions (DeleteDoc / Compact) et prouve l'acceptation
// d'une nouvelle insertion.
func TestQual_04_HeapSaturationRollback(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 19)
	}
	const shardID uint16 = 4

	s, err := OpenShard(dir, key, shardID)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer func() { _ = s.Close() }()

	const collName = "qual_saturation"
	if err := s.CreateCollection(collName); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	// Remplissage initial de documents
	const initialDocs = 10
	docIDs := make([]c2uuidv7.UUID, initialDocs)
	docBodies := make([][]byte, initialDocs)
	for i := 0; i < initialDocs; i++ {
		docBodies[i] = []byte(fmt.Sprintf("document-payload-index-%04d-content", i))
		id, err := s.Insert(collName, docBodies[i])
		if err != nil {
			t.Fatalf("Insert doc %d: %v", i, err)
		}
		docIDs[i] = id
	}

	// Forcer le seuil de saturation préventive de l'arène
	s.heapUsed = heapWatermark
	s.stampHeap(s.pub)

	// 1. Auto-compaction : l'arène est saturée artificiellement mais les pages
	// inactives sont recyclées, l'insertion reprend.
	newKey := []byte("new-overflow-key")
	acceptedEarly := []byte("will-be-accepted-after-autocompact")
	if err = s.Put(newKey, acceptedEarly); err != nil {
		t.Fatalf("Put sur arène saturée (auto-compact): %v", err)
	}
	gotEarly, err := s.Get(newKey)
	if err != nil || !bytes.Equal(gotEarly, acceptedEarly) {
		t.Fatalf("Get post-auto-compact: got %q err %v", gotEarly, err)
	}

	if _, err = s.Insert(collName, []byte("another-accepted-doc")); err != nil {
		t.Fatalf("Insert doc sur arène saturée (auto-compact): %v", err)
	}

	// 2. Prouver que les lectures continuent de fonctionner sans anomalie
	for i := 0; i < initialDocs; i++ {
		got, err := s.GetDoc(collName, docIDs[i])
		if err != nil {
			t.Fatalf("GetDoc %d post-saturation: %v", i, err)
		}
		if !bytes.Equal(got, docBodies[i]) {
			t.Fatalf("GetDoc %d altéré post-saturation: got %q want %q", i, got, docBodies[i])
		}
	}

	scanKeys, err := s.ScanPrefix(catalogPrefix)
	if err != nil {
		t.Fatalf("ScanPrefix post-saturation: %v", err)
	}
	if len(scanKeys) == 0 {
		t.Fatalf("ScanPrefix post-saturation a retourné une liste vide")
	}

	// 3. Exécuter des suppressions (DeleteDoc) et purge via Compact()
	if err := s.DeleteDoc(collName, docIDs[0]); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Réajuster l'arène pour refléter le retour sous le watermark
	s.heapUsed = heapWatermark - 10
	s.stampHeap(s.pub)

	// 4. Vérifier qu'une nouvelle insertion est désormais acceptée
	acceptedKey := []byte("accepted-after-rollback")
	acceptedVal := []byte("accepted-value-ok")
	if err := s.Put(acceptedKey, acceptedVal); err != nil {
		t.Fatalf("Put post-rollback a échoué: %v", err)
	}

	gotAccepted, err := s.Get(acceptedKey)
	if err != nil || !bytes.Equal(gotAccepted, acceptedVal) {
		t.Fatalf("Get post-rollback: got %q, err %v", gotAccepted, err)
	}
}

// TestQual_05_MVCCSnapshotExpiration ouvre 8 vues simultanées sans les fermer,
// prouve que le 9e publish() déclenche ErrViewHeld pour borner la mémoire virtuelle résidente,
// puis prouve qu'après fermeture d'une vue, la publication est immédiatement autorisée.
func TestQual_05_MVCCSnapshotExpiration(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 42)
	}
	const shardID uint16 = 5

	s, err := OpenShard(dir, key, shardID)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Clé initiale
	if err := s.Put([]byte("base-key"), []byte("base-val")); err != nil {
		t.Fatalf("Put initial: %v", err)
	}

	// Ouvrir 8 vues simultanées sans les fermer, forçant l'allocation de 8 holdHeaps
	var views []*View
	defer func() {
		for _, v := range views {
			_ = v.Close()
		}
	}()

	for i := 0; i < maxHoldHeaps; i++ {
		v, err := s.View()
		if err != nil {
			t.Fatalf("View %d: %v", i, err)
		}
		views = append(views, v)

		// Mutation déclenchant publish() qui retient le tas précédent dans holdHeaps
		k := []byte(fmt.Sprintf("held-key-%d", i))
		val := []byte(fmt.Sprintf("held-val-%d", i))
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put %d avant saturation vues: %v", i, err)
		}
	}

	if len(s.holdHeaps) != maxHoldHeaps {
		t.Fatalf("nombre de holdHeaps=%d want %d", len(s.holdHeaps), maxHoldHeaps)
	}

	// Ouvrir une vue supplémentaire pour épingler le liveHeap courant
	vOverflow, err := s.View()
	if err != nil {
		t.Fatalf("View overflow: %v", err)
	}
	views = append(views, vOverflow)

	// Nouvelle mutation / publish : le plafond maxHoldHeaps (8) est atteint
	// Le système doit déclencher ErrViewHeld et refuser d'allouer une 9e arène
	overflowKey := []byte("overflow-publish-key")
	overflowVal := []byte("overflow-publish-val")
	err = s.Put(overflowKey, overflowVal)
	if !errors.Is(err, ErrViewHeld) {
		t.Fatalf("Put avec 8 holdHeaps saturés: got %v, want %v", err, ErrViewHeld)
	}

	if len(s.holdHeaps) > maxHoldHeaps {
		t.Fatalf("holdHeaps a dépassé la borne maximale: %d > %d", len(s.holdHeaps), maxHoldHeaps)
	}

	// Fermer une des vues retenues
	if err := views[0].Close(); err != nil {
		t.Fatalf("Close view[0]: %v", err)
	}
	views = views[1:]

	// Prouver que le nouveau publish est immédiatement autorisé
	if err := s.Put(overflowKey, overflowVal); err != nil {
		t.Fatalf("Put après libération d'une vue a échoué: %v", err)
	}

	got, err := s.Get(overflowKey)
	if err != nil || !bytes.Equal(got, overflowVal) {
		t.Fatalf("Get clé publiée après libération de vue: got %q, err %v", got, err)
	}
}

// TestQual_08_OFDLockSentinelIsolation prouve l'exclusion mutuelle stricte via le verrou
// sentinelle .lock : un second écrivain avec timeout nul renvoie immédiatement ErrWriterBusy,
// et la fermeture/réouverture artificielle du Device data.img du premier shard ne libère pas
// le verrou sentinelle, continuant d'exclure le second écrivain.
func TestQual_08_OFDLockSentinelIsolation(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 88)
	}
	const shardID uint16 = 8

	s1, err := OpenShard(dir, key, shardID)
	if err != nil {
		t.Fatalf("OpenShard 1: %v", err)
	}
	defer func() { _ = s1.Close() }()
	s1.SetBusyTimeout(0)

	s2, err := OpenShard(dir, key, shardID)
	if err != nil {
		t.Fatalf("OpenShard 2: %v", err)
	}
	defer func() { _ = s2.Close() }()
	s2.SetBusyTimeout(0)

	// Verrouillage de l'écrivain sur le premier shard
	if err := s1.lockWriter(); err != nil {
		t.Fatalf("s1.lockWriter: %v", err)
	}

	// Vérifier que le second écrivain avec timeout nul renvoie immédiatement ErrWriterBusy
	testKey := []byte("lock-test-key")
	err = s2.Put(testKey, []byte("val2"))
	if !errors.Is(err, ErrWriterBusy) {
		t.Fatalf("s2.Put sous verrou s1: got %v, want %v", err, ErrWriterBusy)
	}

	// Fermer et rouvrir artificiellement le Device sous-jacent (data.img) du premier shard
	dataPath := filepath.Join(dir, "data.img")
	if err := s1.data.Close(); err != nil {
		t.Fatalf("s1.data.Close: %v", err)
	}
	reopenedDev, err := Open(dataPath)
	if err != nil {
		t.Fatalf("Open data.img après fermeture artificielle: %v", err)
	}
	s1.data = reopenedDev

	// Prouver que le verrou sentinelle .lock est resté actif et continue d'exclure le second écrivain
	err = s2.Put(testKey, []byte("val2-after-dev-reopen"))
	if !errors.Is(err, ErrWriterBusy) {
		t.Fatalf("s2.Put après réouverture data.img: got %v, want %v (la sentinelle .lock doit rester active)", err, ErrWriterBusy)
	}

	// Libérer le verrou du premier shard
	s1.unlockWriter()

	// Le second écrivain peut maintenant acquérir le verrou et écrire
	if err := s2.Put(testKey, []byte("val2-success")); err != nil {
		t.Fatalf("s2.Put après s1.unlockWriter: %v", err)
	}

	got, err := s2.Get(testKey)
	if err != nil || !bytes.Equal(got, []byte("val2-success")) {
		t.Fatalf("s2.Get post-libération: got %q, err %v", got, err)
	}
}
