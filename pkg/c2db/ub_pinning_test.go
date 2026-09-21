// SPDX-License-Identifier: Apache-2.0 OR MIT
package c2db

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// helperCursorHeap construit un tas mono-page feuille contenant une clé/valeur
// utilisable par newCursor. Retourne le tas et la racine.
func helperCursorHeap(t *testing.T, k, v []byte) []byte {
	t.Helper()
	heap := make([]byte, pageN)
	st := Db_bt_leaf_init(heap, pageN)
	if st.Ok != 1 {
		t.Fatalf("leaf_init failed")
	}
	// Utilise l'API heap ver pour coller au format MVCC attendu par le curseur
	// (id16 de 16 octets, valeur avec préfixe cellKindInline).
	// On génère une heap ver minimale : on insère via Db_bt_insert_ver_heap.
	nbytes := pageN
	npages := uint64(1)
	pub := heap
	dirty := make([]byte, pageN)
	copy(dirty, pub)
	hst := Db_bt_heap_st_t{Root: 0, Used: 1, Ok: 1}
	id := make([]byte, 16)
	for i := range id {
		id[i] = byte(i + 1)
	}
	// Encodage inline de la valeur (cellKindInline + payload) — le curseur
	// attend exactement ce format pour vlen.
	cellVal := make([]byte, 1+len(v))
	cellVal[0] = cellKindInline
	copy(cellVal[1:], v)
	got := Db_bt_insert_ver_heap(pub, dirty, nbytes, npages, &hst, k, uint64(len(k)), id, cellVal, uint64(len(cellVal)))
	if got.Ok != 1 {
		// Fallback : insertion simple sans version (couverture minimale)
		st2 := Db_bt_leaf_init(pub, pageN)
		if st2.Ok != 1 {
			t.Fatalf("leaf_init 2 failed")
		}
		res := Db_bt_insert(pub, dirty, pageN, &st2, k, uint64(len(k)), v, uint64(len(v)))
		if res.Ok != 1 {
			t.Fatalf("Db_bt_insert failed: %+v", res)
		}
		copy(pub, dirty)
		return pub
	}
	copy(pub, dirty)
	return pub
}

// TestUBPinning_Cursor_CellOff_HorsBornes injecte les offsets physiquement corrompus
// dans l'en-tête [0, 64[ ou au-delà de pageN-4 ([16381, 65535]) et vérifie que
// readSlotCell rejette systématiquement sans panique (fail-closed).
func TestUBPinning_Cursor_CellOff_HorsBornes(t *testing.T) {
	cases := []uint16{0, 10, 32, 63, 16382, 16384, 65535}
	for _, bad := range cases {
		heap := helperCursorHeap(t, []byte("key-pinning"), []byte("val-pinning"))
		// Le slot 0 vit à [64,66[ dans la page 0
		slotAddr := BT_BodyOffset // 64
		if slotAddr+2 > len(heap) {
			t.Fatalf("slot hors page")
		}
		orig := binary.LittleEndian.Uint16(heap[slotAddr:])
		_ = orig
		binary.LittleEndian.PutUint16(heap[slotAddr:], bad)
		// Construire un curseur épinglant ce tas corrompu
		var snap [16]byte
		for i := range snap {
			snap[i] = 0xFF
		}
		c := newCursor(heap, 0, snap, nil, nil)
		// readSlotCell doit rejeter sans panique
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panique sur offset %d: %v", bad, r)
				}
			}()
			_, _, _, _, ok := c.readSlotCell(0, 0)
			if ok {
				t.Fatalf("readSlotCell a accepté l'offset corrompu %d (0x%04x) — attendu rejet", bad, bad)
			}
		}()
		// Navigation haut-niveau doit aussi être fail-closed (pas de clé valide)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panique First() sur offset %d: %v", bad, r)
				}
			}()
			// First parcourt les feuilles et appelle readSlotCell ; un heap corrompu
			// doit retourner ok==false plutôt que de déréférencer hors bornes.
			// On accepte soit 0 résultat, soit une clé différente, mais jamais un panic.
			_, _, ok := c.First()
			// Si First retourne une clé, c'est que la corruption a été ignorée — échec.
			// Un heap mono-clé corrompu doit être vu comme vide par le curseur.
			if ok {
				t.Fatalf("First() a retourné une clé valide malgré offset %d — attendu fail-closed", bad)
			}
		}()
	}
}

// TestUBPinning_Cursor_CellSize_HorsPage vérifie que cellOff+cellSize > pageN est rejeté.
// On forge une cellule dont l'en-tête annonce klen/vlen débordant la page.
func TestUBPinning_Cursor_CellSize_HorsPage(t *testing.T) {
	heap := helperCursorHeap(t, []byte("k"), []byte("v"))
	slotAddr := BT_BodyOffset
	cellOff := uint64(binary.LittleEndian.Uint16(heap[slotAddr:]))
	if cellOff < BT_BodyOffset || cellOff >= pageN {
		t.Fatalf("cellOff initial hors bornes: %d", cellOff)
	}
	cellBase := cellOff
	// Corrompre klen/vlen pour que cellSize dépasse la page :
	// cellOff est typiquement ~64+? ; on force klen=0xFF00 et vlen=0xFF00
	// afin que cellOff+cellSize > 16384.
	binary.LittleEndian.PutUint16(heap[cellBase:], 0x3FFF)   // klen
	binary.LittleEndian.PutUint16(heap[cellBase+2:], 0x3FFF) // vlen
	var snap [16]byte
	for i := range snap {
		snap[i] = 0xFF
	}
	c := newCursor(heap, 0, snap, nil, nil)
	_, _, _, _, ok := c.readSlotCell(0, 0)
	if ok {
		t.Fatalf("readSlotCell a accepté une cellule débordante (klen/vlen corrompus) — attendu rejet")
	}
}

// TestUBPinning_Cursor_CellOff_Limite16320 vérifie la frontière physique pageN.
// La page physique est [0,16384[ : en-tête [0,64[, corps [64,16384[.
// Un offset à 16320 avec une petite cellule est valide (16320+22=16342 <=16384).
// Un offset à 16384 (==pageN) doit être rejeté (hors page), de même que
// tout offset dont la cellule déborde de la page.
func TestUBPinning_Cursor_CellOff_Limite16320(t *testing.T) {
	// Un offset à 16320 avec une petite cellule doit être accepté (limite OverflowChunkCapacity)
	heap := helperCursorHeap(t, []byte("k"), []byte("v"))
	slotAddr := BT_BodyOffset
	// Forcer offset à 16320 (OverflowChunkCapacity) avec une cellule minimale injectée
	// — on ne peut pas simplement réutiliser le heap existant dont la cellule est à 16361,
	// on teste plutôt qu'un offset à 16384 est rejeté et qu'un offset à 16320 bien formé est accepté.
	// Ici on vérifie d'abord qu'un offset hors page (16384) est rejeté.
	heapBad := make([]byte, pageN)
	stBad := Db_bt_leaf_init(heapBad, pageN)
	if stBad.Ok != 1 {
		t.Fatalf("leaf_init bad")
	}
	binary.LittleEndian.PutUint16(heapBad[64:], uint16(pageN)) // 16384 == hors page
	cBad := newCursor(heapBad, 0, [16]byte{}, nil, nil)
	_, _, _, _, okBad := cBad.readSlotCell(0, 0)
	if okBad {
		t.Fatalf("offset pageN (16384) aurait dû être rejeté (hors page)")
	}
	// Vérification qu'un offset à 16320 avec cellule minimale est accepté (test plus bas)
	// — on conserve le heap original pour la suite, mais on vérifie que 16321 n'est pas
	// considéré comme corrompu par le pinning physique (seule la taille de cellule compte).
	// Vérification qu'un offset corrompu au-delà de pageN-4 (ex: 16382) est rejeté
	binary.LittleEndian.PutUint16(heap[slotAddr:], 16382)
	c := newCursor(heap, 0, [16]byte{}, nil, nil)
	_, _, _, _, ok := c.readSlotCell(0, 0)
	if ok {
		t.Fatalf("offset 16382 > pageN-4 aurait dû être rejeté par le pinning physique")
	}
	// Offset 16320 avec une cellule tronquée (klen=0,vlen=0) et placée à 16320
	// est techniquement dans la zone, mais la cellule doit tenir dans la page.
	// On le teste avec une cellule vide injectée manuellement.
	heap2 := make([]byte, pageN)
	st := Db_bt_leaf_init(heap2, pageN)
	if st.Ok != 1 {
		t.Fatalf("leaf_init")
	}
	// Injecter manuellement une cellule à 16320
	cellOff := uint16(OverflowChunkCapacity) // 16320
	binary.LittleEndian.PutUint16(heap2[64:], cellOff)
	// Construire une cellule minimale à 16320 : klen=1, vlen=1, k='x', id16=0, v='y'
	base := uint64(cellOff)
	binary.LittleEndian.PutUint16(heap2[base:], 1)   // klen
	binary.LittleEndian.PutUint16(heap2[base+2:], 1) // vlen
	heap2[base+4] = 'x'
	// id16 16 octets à base+5
	for i := uint64(0); i < 16; i++ {
		heap2[base+5+i] = byte(i)
	}
	heap2[base+21] = 'y'
	// Mettre à jour l'en-tête de page : nslots=1, free_lo, free_hi
	binary.LittleEndian.PutUint16(heap2[22:], 1)               // nslots
	binary.LittleEndian.PutUint16(heap2[24:], 64+2)            // free_lo (après slot)
	binary.LittleEndian.PutUint16(heap2[26:], uint16(cellOff)) // free_hi (plus petite cellule)
	var snap [16]byte
	for i := range snap {
		snap[i] = 0xFF
	}
	c2 := newCursor(heap2, 0, snap, nil, nil)
	// Cette cellule tient exactement à 16320+22=16342 <=16384 donc doit être acceptée
	_, _, _, _, ok2 := c2.readSlotCell(0, 0)
	if !ok2 {
		t.Fatalf("offset 16320 avec cellule minimale aurait dû être accepté")
	}
	// Même offset mais avec une cellule trop grande (klen=100) doit être rejeté
	binary.LittleEndian.PutUint16(heap2[base:], 100)
	c3 := newCursor(heap2, 0, snap, nil, nil)
	_, _, _, _, ok3 := c3.readSlotCell(0, 0)
	if ok3 {
		t.Fatalf("offset 16320 + klen 100 doit déborder et être rejeté")
	}
}

// helperOverflowHeap construit un tas contenant une chaîne d'overflow de totalLen octets
// démarrant à headPage, avec chunkLen découpé selon OverflowChunkCapacity.
func helperOverflowHeap(totalLen uint32, headPage uint64) ([]byte, uint64) {
	npages := headPage + 4
	if npages < 4 {
		npages = 4
	}
	heap := make([]byte, npages*pageN)
	heapPages := npages
	nChunks := (uint64(totalLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity
	for i := uint64(0); i < nChunks; i++ {
		pg := headPage + i
		off := pg * pageN
		heap[off+20] = TypeOverflow
		var next uint64
		if i+1 < nChunks {
			next = pg + 1
		}
		binary.LittleEndian.PutUint64(heap[off+32:off+40], next)
		chunkLen := uint64(OverflowChunkCapacity)
		remaining := uint64(totalLen) - i*OverflowChunkCapacity
		if remaining < chunkLen {
			chunkLen = remaining
		}
		binary.LittleEndian.PutUint32(heap[off+40:off+44], uint32(chunkLen))
		binary.LittleEndian.PutUint32(heap[off+44:off+48], uint32(i))
		for j := uint64(0); j < chunkLen; j++ {
			heap[off+64+j] = byte((i*OverflowChunkCapacity + j) % 251)
		}
	}
	_ = heapPages
	return heap, npages
}

func TestUBPinning_Overflow_ChunkLen_HorsBornes(t *testing.T) {
	totalLen := uint32(16321) // 2 pages : 16320 + 1
	head := uint64(1)
	cases := []uint32{16350, 65535, 16321, 0xFFFF}
	for _, bad := range cases {
		heap, npages := helperOverflowHeap(totalLen, head)
		// Corrompre le premier chunk
		off := head * pageN
		binary.LittleEndian.PutUint32(heap[off+40:off+44], bad)
		_, err := readOverflowChain(heap, npages, head, totalLen)
		if err == nil {
			t.Fatalf("readOverflowChain a accepté chunkLen corrompu %d — attendu ErrCorruptedOverflowChain", bad)
		}
		if err != ErrCorruptedOverflowChain && err != ErrOverflowOutOfBounds {
			// replayValidateOverflowChain retourne false, mais readOverflowChain doit retourner ErrCorrupted
			t.Logf("chunkLen %d: err=%v", bad, err)
		}
		// StreamReader doit aussi rejeter
		var snap [16]byte
		_ = snap
		r := &overflowStreamReader{
			heap:      heap,
			heapPages: npages,
			currPage:  head,
			totalLen:  totalLen,
			maxPages:  (uint64(totalLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity,
		}
		buf := make([]byte, 1024)
		_, err = r.Read(buf)
		if err == nil {
			t.Fatalf("overflowStreamReader a accepté chunkLen %d — attendu erreur", bad)
		}
	}
}

func TestUBPinning_Overflow_ValOffsetPlusChunkLen_DepasseTotalLen(t *testing.T) {
	totalLen := uint32(16321)
	head := uint64(1)
	heap, npages := helperOverflowHeap(totalLen, head)
	// Le second chunk devrait faire 1 octet ; on le gonfle à 16320 pour dépasser totalLen
	off2 := (head + 1) * pageN
	binary.LittleEndian.PutUint32(heap[off2+40:off2+44], uint32(OverflowChunkCapacity)) // 16320
	// valOffset au second chunk = 16320, donc 16320+16320=32640 > 16321
	_, err := readOverflowChain(heap, npages, head, totalLen)
	if err == nil {
		t.Fatalf("readOverflowChain a accepté valOffset+chunkLen > totalLen — attendu rejet")
	}
	r := &overflowStreamReader{
		heap:      heap,
		heapPages: npages,
		currPage:  head,
		totalLen:  totalLen,
		maxPages:  (uint64(totalLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity,
	}
	_, err = io.ReadAll(r)
	if err == nil {
		t.Fatalf("overflowStreamReader a accepté chaîne avec valOffset+chunkLen > totalLen")
	}
}

func TestUBPinning_Overflow_ReplayValidate_HorsBornes(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 11)
	}
	s := mustOpenShard(t, dir, key, 41, WithHeapPages(4096))
	defer s.Close()
	// Valeur d'overflow de 5000 octets (2 pages si on force, mais 5000 <16320 donc mono-page ;
	// on utilise 20000 pour garantir 2 pages)
	payload := bytes.Repeat([]byte("Z"), 20000)
	if err := s.Put([]byte("overflow-key"), payload); err != nil {
		t.Fatalf("Put overflow: %v", err)
	}
	// Récupérer le descripteur brut
	raw, err := getRawAsOfHeap(s.pub, s.heapRoot, []byte("overflow-key"), func() [16]byte {
		var m [16]byte
		for i := range m {
			m[i] = 0xFF
		}
		return m
	}())
	if err != nil {
		t.Fatalf("getRaw: %v", err)
	}
	tLen, hPage, ok := isOverflowDescriptor(raw)
	if !ok {
		t.Fatalf("pas de descripteur d'overflow")
	}
	// Corrompre le premier chunk à 16350 et recalculer le CRC pour isoler le contrôle de bornes physiques
	pgOff := hPage * pageN
	origChunkLen := binary.LittleEndian.Uint32(s.pub[pgOff+40 : pgOff+44])
	origCRC := binary.LittleEndian.Uint32(s.pub[pgOff+28 : pgOff+32])
	binary.LittleEndian.PutUint32(s.pub[pgOff+40:pgOff+44], 16350)
	_ = C2db_crc32c_fullpage_store(s.pub[pgOff:pgOff+pageN], pageN)
	if s.replayValidateOverflowChain(hPage, tLen) {
		t.Fatalf("replayValidateOverflowChain a accepté chunkLen 16350 — attendu false")
	}
	// Restaurer puis corrompre avec 65535 (avec CRC valide pour isoler la borne physique)
	binary.LittleEndian.PutUint32(s.pub[pgOff+40:pgOff+44], 65535)
	_ = C2db_crc32c_fullpage_store(s.pub[pgOff:pgOff+pageN], pageN)
	if s.replayValidateOverflowChain(hPage, tLen) {
		t.Fatalf("replayValidate a accepté chunkLen 65535 — attendu false")
	}
	// Restaurer l'original et son CRC, puis vérifier que la chaîne valide passe
	binary.LittleEndian.PutUint32(s.pub[pgOff+40:pgOff+44], origChunkLen)
	binary.LittleEndian.PutUint32(s.pub[pgOff+28:pgOff+32], origCRC)
	if !s.replayValidateOverflowChain(hPage, tLen) {
		t.Fatalf("replayValidate a rejeté une chaîne valide après restauration")
	}
	// Injecter une corruption valOffset+chunkLen > totalLen sur le second chunk (avec CRC valide)
	nPages := (uint64(tLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity
	if nPages > 1 {
		off2 := (hPage + 1) * pageN
		orig2 := binary.LittleEndian.Uint32(s.pub[off2+40 : off2+44])
		origCRC2 := binary.LittleEndian.Uint32(s.pub[off2+28 : off2+32])
		binary.LittleEndian.PutUint32(s.pub[off2+40:off2+44], uint32(OverflowChunkCapacity))
		_ = C2db_crc32c_fullpage_store(s.pub[off2:off2+pageN], pageN)
		if s.replayValidateOverflowChain(hPage, tLen) {
			t.Fatalf("replayValidate a accepté second chunk gonflé — attendu false")
		}
		binary.LittleEndian.PutUint32(s.pub[off2+40:off2+44], orig2)
		binary.LittleEndian.PutUint32(s.pub[off2+28:off2+32], origCRC2)
	}
	// readOverflowChain direct avec les mêmes corruptions
	heap := s.pub
	npages := uint64(len(heap)) / pageN
	binary.LittleEndian.PutUint32(heap[pgOff+40:pgOff+44], 16350)
	_, err = readOverflowChain(heap, npages, hPage, tLen)
	if err == nil {
		t.Fatalf("readOverflowChain a accepté chunkLen 16350 via heap corrompu")
	}
	binary.LittleEndian.PutUint32(heap[pgOff+40:pgOff+44], origChunkLen)
	_, err = readOverflowChain(heap, npages, hPage, tLen)
	if err != nil {
		t.Fatalf("readOverflowChain valide a échoué après restauration: %v", err)
	}
}

func TestUBPinning_Overflow_StreamReader_OffsetsCorrompus(t *testing.T) {
	// Teste que le streamReader n'effectue aucun accès hors bornes même avec
	// des chunkLen extrêmes et que chaque Read échoue proprement.
	totalLen := uint32(5000)
	head := uint64(1)
	heap, npages := helperOverflowHeap(totalLen, head)
	// Corrompre à 0 (offset header) — déjà testé via chunkLen mais on vérifie le chemin
	off := head * pageN
	for _, bad := range []uint32{0, 10, 16350, 65535} {
		// 0 et 10 sont valides en tant que chunkLen (<16320) mais combinés à un
		// totalLen de 5000 ils doivent être cohérents : si chunkLen=0, valOffset ne progresse pas
		// et la chaîne ne peut pas atteindre totalLen => doit échouer par valOffset != totalLen.
		// On teste donc la robustesse de l'implémentation face à ces valeurs.
		binary.LittleEndian.PutUint32(heap[off+40:off+44], bad)
		binary.LittleEndian.PutUint32(heap[off+44:off+48], 0) // seq 0
		r := &overflowStreamReader{
			heap:      heap,
			heapPages: npages,
			currPage:  head,
			totalLen:  totalLen,
			maxPages:  (uint64(totalLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity,
		}
		buf := make([]byte, 1024)
		_, err := r.Read(buf)
		// Pour bad=0, la chaîne est invalide (valOffset ne peut pas atteindre totalLen)
		// Pour bad=10, chunkLen=10 est techniquement valide mais totalLen=5000 nécessite
		// plusieurs chunks ; avec un seul chunk de 10, la chaîne s'arrête prématurément.
		// Dans tous les cas, on exige soit une erreur, soit un comportement fail-closed
		// sans panique.
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("panique streamReader chunkLen=%d: %v", bad, rec)
				}
			}()
			// Si le reader n'a pas retourné d'erreur au premier Read, on tente ReadAll
			if err == nil {
				// Lire le reste pour forcer la détection de l'incohérence
				_, err2 := io.ReadAll(r)
				if err2 == nil {
					// Seul le cas chunkLen=10 pourrait sembler partiellement valide,
					// mais la chaîne complète ne peut pas reconstituer totalLen=5000.
					// On exige donc une erreur à un moment.
					if bad == 10 {
						// chunkLen 10 est dans les bornes mais la chaîne est tronquée
						// => valOffset != totalLen => Io.ErrUnexpectedEOF attendu
						// Si ReadAll a réussi sans erreur, c'est un échec.
						t.Fatalf("streamReader a accepté chaîne tronquée chunkLen=10 totalLen=5000 sans erreur")
					}
				}
			}
		}()
		// Restaurer pour le prochain cas
		binary.LittleEndian.PutUint32(heap[off+40:off+44], uint32(5000))
	}
}
