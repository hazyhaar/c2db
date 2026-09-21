// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"sort"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	BT_HLCOffset    = 8
	BT_TypeOffset   = 20
	BT_NSlotsOffset = 22
	BT_BodyOffset   = 64
	BT_TypeLeaf     = 1
	BT_TypeInternal = 2
	BT_SlotSize     = 2
	BT_IDLen        = 16

	cursorHLCOffset    = BT_HLCOffset
	cursorTypeOffset   = BT_TypeOffset
	cursorNSlotsOffset = BT_NSlotsOffset
	cursorBodyOffset   = BT_BodyOffset
	cursorTypeLeaf     = BT_TypeLeaf
	cursorTypeInternal = BT_TypeInternal
	cursorBTSlotSize   = BT_SlotSize
	cursorBTIDLen      = BT_IDLen
)

// Cursor fournit un itérateur bidirectionnel de streaming zéro-allocation
// sur les paires clé-valeur actives d'un Shard ou d'une View selon un instantané MVCC.
type Cursor struct {
	heap   []byte
	npages uint64
	root   uint64
	snap   [16]byte
	shard  *Shard
	pin    *liveHeap

	// Séquence ordonnée des pages feuilles de gauche à droite
	leafPages    []uint64
	inlineLeaves [64]uint64

	// Position courante
	leafIdx int
	slotIdx int
	valid   bool

	// Vues directes mémoire mmap / liveHeap (0 allocation)
	currKey []byte
	currVal []byte
	currID  []byte

	// Protection concurrence interne
	mu sync.Mutex
}

// newCursor initialise la structure Cursor sur un tas, une racine et un snapshot.
func newCursor(heap []byte, root uint64, snap [16]byte, shard *Shard, pin *liveHeap) *Cursor {
	c := &Cursor{
		heap:   heap,
		npages: uint64(len(heap)) / pageN,
		root:   root,
		snap:   snap,
		shard:  shard,
		pin:    pin,
	}
	c.initLeafPages()
	return c
}

// initLeafPages parcourt l'arbre pour construire la liste ordonnée des pages feuilles.
func (c *Cursor) initLeafPages() {
	c.leafPages = c.inlineLeaves[:0]
	if c.heap == nil || c.npages == 0 || c.root >= c.npages {
		return
	}

	// Descente jusqu'à la première feuille (leftmost)
	page := c.root
	walked := uint64(0)
	for walked < c.npages {
		base := page * pageN
		if base+cursorTypeOffset >= uint64(len(c.heap)) {
			return
		}
		typ := c.heap[base+cursorTypeOffset]
		if typ == cursorTypeLeaf {
			break
		}
		if typ != cursorTypeInternal {
			return
		}
		if base+cursorHLCOffset+8 > uint64(len(c.heap)) {
			return
		}
		next := binary.LittleEndian.Uint64(c.heap[base+cursorHLCOffset:])
		if next >= c.npages || next == page {
			return
		}
		page = next
		walked++
	}

	// Parcours horizontal de la chaîne de feuilles ordonnées
	walked = 0
	for page < c.npages && walked < c.npages {
		c.leafPages = append(c.leafPages, page)
		base := page * pageN
		if base+cursorHLCOffset+8 > uint64(len(c.heap)) {
			break
		}
		next := binary.LittleEndian.Uint64(c.heap[base+cursorHLCOffset:])
		if next == 0 || next == page || next >= c.npages {
			break
		}
		page = next
		walked++
	}
}

// readSlotCell extrait la clé, l'id16, la valeur et la longueur de valeur d'un slot donné.
// Ne réalise aucune allocation : retourne des slices pointant directement dans c.heap.
// Verrouillage UB Pinning [64,16320] : toute cellule hors bornes physiques est rejetée
// sans panique ni accès mémoire hors limites (fail-closed).
func (c *Cursor) readSlotCell(leafIdx, slotIdx int) (key, id16, val []byte, vlen uint16, ok bool) {
	if leafIdx < 0 || leafIdx >= len(c.leafPages) {
		return nil, nil, nil, 0, false
	}
	page := c.leafPages[leafIdx]
	base := page * pageN
	if base+cursorNSlotsOffset+2 > uint64(len(c.heap)) {
		return nil, nil, nil, 0, false
	}
	nslots := binary.LittleEndian.Uint16(c.heap[base+cursorNSlotsOffset:])
	if slotIdx < 0 || slotIdx >= int(nslots) {
		return nil, nil, nil, 0, false
	}

	slotAddr := base + cursorBodyOffset + uint64(slotIdx)*cursorBTSlotSize
	// Le slot doit résider intégralement dans la zone utile [64,16384[ de la page.
	if slotAddr+cursorBTSlotSize > base+pageN {
		return nil, nil, nil, 0, false
	}
	if slotAddr+2 > uint64(len(c.heap)) {
		return nil, nil, nil, 0, false
	}
	cellOff := uint64(binary.LittleEndian.Uint16(c.heap[slotAddr:]))
	// --- UB Pinning : bornes physiques de cellule [64, pageN[ et taille bornée à OverflowChunkCapacity (16320) ---
	if cellOff < cursorBodyOffset || cellOff > pageN-4 {
		return nil, nil, nil, 0, false
	}
	cellBase := base + cellOff
	if cellBase+4 > uint64(len(c.heap)) || cellBase+4 > base+pageN {
		return nil, nil, nil, 0, false
	}

	klen := uint64(binary.LittleEndian.Uint16(c.heap[cellBase:]))
	vlen = binary.LittleEndian.Uint16(c.heap[cellBase+2:])
	cellSize := 4 + klen + cursorBTIDLen + uint64(vlen)
	if klen > pageN || uint64(vlen) > pageN {
		return nil, nil, nil, 0, false
	}
	if cellSize > OverflowChunkCapacity || cellSize > pageN-cellOff {
		return nil, nil, nil, 0, false
	}
	if cellBase+cellSize > uint64(len(c.heap)) || cellBase+cellSize > base+pageN {
		return nil, nil, nil, 0, false
	}

	kStart := cellBase + 4
	// Vérifications de bornes pour chaque sous-tranche avant déréférencement
	if kStart+klen > base+pageN || kStart+klen > uint64(len(c.heap)) {
		return nil, nil, nil, 0, false
	}
	if kStart+klen+cursorBTIDLen > base+pageN || kStart+klen+cursorBTIDLen > uint64(len(c.heap)) {
		return nil, nil, nil, 0, false
	}
	if kStart+klen+cursorBTIDLen+uint64(vlen) > base+pageN || kStart+klen+cursorBTIDLen+uint64(vlen) > uint64(len(c.heap)) {
		return nil, nil, nil, 0, false
	}
	key = c.heap[kStart : kStart+klen]
	idStart := kStart + klen
	id16 = c.heap[idStart : idStart+cursorBTIDLen]
	valStart := idStart + cursorBTIDLen
	val = c.heap[valStart : valStart+uint64(vlen)]
	return key, id16, val, vlen, true
}

// leafSlotCount renvoie le nombre de slots d'une feuille donnée.
func (c *Cursor) leafSlotCount(leafIdx int) int {
	if leafIdx < 0 || leafIdx >= len(c.leafPages) {
		return 0
	}
	base := c.leafPages[leafIdx] * pageN
	if base+cursorNSlotsOffset+2 > uint64(len(c.heap)) {
		return 0
	}
	return int(binary.LittleEndian.Uint16(c.heap[base+cursorNSlotsOffset:]))
}

// leafFirstKey renvoie la clé du premier slot d'une feuille pour la recherche binaire.
func (c *Cursor) leafFirstKey(leafIdx int) []byte {
	k, _, _, _, ok := c.readSlotCell(leafIdx, 0)
	if !ok {
		return nil
	}
	return k
}

// resolveKeyAt résout la version visible de la clé située à (leafIdx, slotIdx).
// Puisque les versions d'une même clé sont ordonnées par id16 croissant,
// on parcourt tous les slots consécutifs partageant cette même clé.
// Renvoie :
// - key, val de la version active (si active)
// - endLeafIdx, endSlotIdx : position du dernier slot de ce groupe de versions
// - active : true si une version <= snap existe et n'est pas un tombstone (vlen > 0).
func (c *Cursor) resolveKeyAt(leafIdx, slotIdx int) (key, val, id16 []byte, endLeafIdx, endSlotIdx int, active bool) {
	initKey, initID, initVal, initVLen, ok := c.readSlotCell(leafIdx, slotIdx)
	if !ok {
		return nil, nil, nil, leafIdx, slotIdx, false
	}

	key = initKey
	var bestVal []byte
	var bestID []byte
	var bestVLen uint16
	var foundVer bool

	if bytes.Compare(initID, c.snap[:]) <= 0 {
		bestVal = initVal
		bestID = initID
		bestVLen = initVLen
		foundVer = true
	}

	curL := leafIdx
	curS := slotIdx

	for {
		nextL := curL
		nextS := curS + 1
		if nextS >= c.leafSlotCount(nextL) {
			nextL++
			nextS = 0
		}
		if nextL >= len(c.leafPages) {
			break
		}

		nk, nid, nval, nvlen, nok := c.readSlotCell(nextL, nextS)
		if !nok || !bytes.Equal(nk, key) {
			break
		}

		curL = nextL
		curS = nextS

		if bytes.Compare(nid, c.snap[:]) <= 0 {
			bestVal = nval
			bestID = nid
			bestVLen = nvlen
			foundVer = true
		}
	}

	endLeafIdx = curL
	endSlotIdx = curS
	if foundVer && bestVLen > 0 {
		return key, bestVal, bestID, endLeafIdx, endSlotIdx, true
	}
	return key, nil, nil, endLeafIdx, endSlotIdx, false
}

// resolveKeyEndingAt résout la version visible de la clé dont le dernier slot est à (leafIdx, slotIdx).
// Renvoie :
// - key, val de la version active
// - startLeafIdx, startSlotIdx : position du premier slot de ce groupe de versions
// - active : true si active
func (c *Cursor) resolveKeyEndingAt(leafIdx, slotIdx int) (key, val, id16 []byte, startLeafIdx, startSlotIdx int, active bool) {
	initKey, _, _, _, ok := c.readSlotCell(leafIdx, slotIdx)
	if !ok {
		return nil, nil, nil, leafIdx, slotIdx, false
	}

	// Reculer jusqu'au premier slot ayant cette même clé
	curL := leafIdx
	curS := slotIdx
	for {
		prevL := curL
		prevS := curS - 1
		if prevS < 0 {
			prevL--
			if prevL < 0 {
				break
			}
			prevS = c.leafSlotCount(prevL) - 1
		}
		if prevL < 0 || prevS < 0 {
			break
		}
		pk, _, _, _, pok := c.readSlotCell(prevL, prevS)
		if !pok || !bytes.Equal(pk, initKey) {
			break
		}
		curL = prevL
		curS = prevS
	}

	startLeafIdx = curL
	startSlotIdx = curS

	k, v, id, _, _, act := c.resolveKeyAt(startLeafIdx, startSlotIdx)
	return k, v, id, startLeafIdx, startSlotIdx, act
}

// stepForward avance à la prochaine clé distincte active.
func (c *Cursor) stepForward() bool {
	curL := c.leafIdx
	curS := c.slotIdx

	for {
		// Avancer d'un slot
		curS++
		if curS >= c.leafSlotCount(curL) {
			curL++
			curS = 0
		}
		if curL >= len(c.leafPages) {
			c.valid = false
			c.currKey = nil
			c.currVal = nil
			c.currID = nil
			return false
		}

		k, v, id, endL, endS, active := c.resolveKeyAt(curL, curS)
		if active {
			c.leafIdx = curL
			c.slotIdx = curS
			c.currKey = k
			c.currVal = v
			c.currID = id
			c.valid = true
			return true
		}
		// Sauter tout le groupe de versions inactives
		curL = endL
		curS = endS
	}
}

// stepBackward recule à la précédente clé distincte active.
func (c *Cursor) stepBackward() bool {
	curL := c.leafIdx
	curS := c.slotIdx

	for {
		// Reculer d'un slot
		curS--
		if curS < 0 {
			curL--
			if curL < 0 {
				c.valid = false
				c.currKey = nil
				c.currVal = nil
				c.currID = nil
				return false
			}
			curS = c.leafSlotCount(curL) - 1
		}
		if curL < 0 || curS < 0 {
			c.valid = false
			c.currKey = nil
			c.currVal = nil
			c.currID = nil
			return false
		}

		k, v, id, startL, _, active := c.resolveKeyEndingAt(curL, curS)
		if active {
			c.leafIdx = startL
			c.slotIdx = curS
			c.currKey = k
			c.currVal = v
			c.currID = id
			c.valid = true
			return true
		}
		// Sauter tout le groupe de versions inactives vers la gauche
		curL = startL
		curS = 0
	}
}

// First positionne le curseur sur la plus petite clé active as-of snap.
func (c *Cursor) First() (key, val []byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.leafPages) == 0 {
		c.valid = false
		c.currID = nil
		return nil, nil, false
	}

	curL := 0
	curS := 0
	for curL < len(c.leafPages) {
		if c.leafSlotCount(curL) == 0 {
			curL++
			curS = 0
			continue
		}

		k, v, id, endL, endS, active := c.resolveKeyAt(curL, curS)
		if active {
			c.leafIdx = curL
			c.slotIdx = curS
			c.currKey = k
			c.currVal = v
			c.currID = id
			c.valid = true
			return k, c.resolveVal(v), true
		}
		curL = endL
		curS = endS + 1
		if curS >= c.leafSlotCount(curL) {
			curL++
			curS = 0
		}
	}

	c.valid = false
	c.currKey = nil
	c.currVal = nil
	c.currID = nil
	return nil, nil, false
}

// Last positionne le curseur sur la plus grande clé active as-of snap.
func (c *Cursor) Last() (key, val []byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.leafPages) == 0 {
		c.valid = false
		c.currID = nil
		return nil, nil, false
	}

	curL := len(c.leafPages) - 1
	for curL >= 0 {
		cnt := c.leafSlotCount(curL)
		if cnt == 0 {
			curL--
			continue
		}
		curS := cnt - 1

		for curS >= 0 {
			k, v, id, startL, startS, active := c.resolveKeyEndingAt(curL, curS)
			if active {
				c.leafIdx = startL
				c.slotIdx = curS
				c.currKey = k
				c.currVal = v
				c.currID = id
				c.valid = true
				return k, c.resolveVal(v), true
			}
			curL = startL
			curS = startS - 1
			if curS < 0 {
				curL--
				break
			}
		}
	}

	c.valid = false
	c.currKey = nil
	c.currVal = nil
	c.currID = nil
	return nil, nil, false
}

// Seek positionne le curseur sur la première clé active >= target.
func (c *Cursor) Seek(target []byte) (key, val []byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.leafPages) == 0 {
		c.valid = false
		c.currID = nil
		return nil, nil, false
	}

	// Recherche dichotomique de la première feuille potentielle
	// Trouver le plus grand idx tel que leafFirstKey(idx) <= target
	leafIdx := sort.Search(len(c.leafPages), func(i int) bool {
		fk := c.leafFirstKey(i)
		if fk == nil {
			return true
		}
		return bytes.Compare(fk, target) > 0
	}) - 1

	if leafIdx < 0 {
		leafIdx = 0
	}

	curL := leafIdx
	curS := 0

	// Recherche binaire dans la feuille curL du premier slot >= target
	cnt := c.leafSlotCount(curL)
	if cnt > 0 {
		curS = sort.Search(cnt, func(i int) bool {
			k, _, _, _, ok := c.readSlotCell(curL, i)
			if !ok {
				return true
			}
			return bytes.Compare(k, target) >= 0
		})
		if curS >= cnt {
			curL++
			curS = 0
		}
	}

	for curL < len(c.leafPages) {
		if c.leafSlotCount(curL) == 0 {
			curL++
			curS = 0
			continue
		}

		k, v, id, endL, endS, active := c.resolveKeyAt(curL, curS)
		if active && bytes.Compare(k, target) >= 0 {
			c.leafIdx = curL
			c.slotIdx = curS
			c.currKey = k
			c.currVal = v
			c.currID = id
			c.valid = true
			return k, c.resolveVal(v), true
		}
		curL = endL
		curS = endS + 1
		if curS >= c.leafSlotCount(curL) {
			curL++
			curS = 0
		}
	}

	c.valid = false
	c.currKey = nil
	c.currVal = nil
	c.currID = nil
	return nil, nil, false
}

// Next avance le curseur à la clé distincte active suivante.
// Garantie zéro-allocation : 0 B/op, 0 allocs/op pour les entrées inline.
func (c *Cursor) Next() (key, val []byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.valid {
		return nil, nil, false
	}

	_, _, _, endL, endS, _ := c.resolveKeyAt(c.leafIdx, c.slotIdx)
	c.leafIdx = endL
	c.slotIdx = endS

	if !c.stepForward() {
		return nil, nil, false
	}
	return c.currKey, c.resolveVal(c.currVal), true
}

// Prev recule le curseur à la clé distincte active précédente.
// Garantie zéro-allocation : 0 B/op, 0 allocs/op pour les entrées inline.
func (c *Cursor) Prev() (key, val []byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.valid {
		return nil, nil, false
	}

	_, _, _, startL, startS, _ := c.resolveKeyEndingAt(c.leafIdx, c.slotIdx)
	c.leafIdx = startL
	c.slotIdx = startS

	if !c.stepBackward() {
		return nil, nil, false
	}
	return c.currKey, c.resolveVal(c.currVal), true
}

// Key renvoie la clé courante sous forme de sous-slice directe du tas mmap (zéro allocation).
func (c *Cursor) Key() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil
	}
	return c.currKey
}

func (c *Cursor) VersionID() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil
	}
	return c.currID
}

// Value renvoie la valeur courante (sous-slice directe zéro allocation si inline, ou résolue depuis les pages d'overflow).
func (c *Cursor) Value() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil
	}
	return c.resolveVal(c.currVal)
}

func (c *Cursor) resolveVal(raw []byte) []byte {
	val, err := decodeValue(c.heap, c.npages, raw)
	if err == nil {
		return val
	}
	return raw
}

// RawValue renvoie la tranche d'octets brute stockée dans la cellule de feuille
// sans décoder la chaîne d'overflow (zéro allocation, prédicat pushdown).
func (c *Cursor) RawValue() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil
	}
	return c.currVal
}

// IsOverflow indique si la valeur courante est stockée dans des pages d'overflow.
func (c *Cursor) IsOverflow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return false
	}
	isOf, _, _ := checkOverflowDesc(c.currVal)
	return isOf
}

// ValueLen renvoie la taille exacte en octets de la valeur courante
// sans décoder l'intégralité du corps d'overflow.
func (c *Cursor) ValueLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return 0
	}
	if tLen, _, ok := isOverflowDescriptor(c.currVal); ok {
		return int(tLen)
	}
	if len(c.currVal) > 0 && c.currVal[0] == cellKindInline {
		return len(c.currVal) - 1
	}
	return len(c.currVal)
}

// Valid indique si le curseur est positionné sur une entrée active valide.
func (c *Cursor) Valid() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.valid
}

// Close libère l'épinglage du liveHeap du Shard ou de la View.
func (c *Cursor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.valid = false
	c.currKey = nil
	c.currVal = nil
	c.currID = nil
	if c.pin != nil {
		c.pin.refs.Add(-1)
		c.pin = nil
	}
	c.shard = nil
	c.heap = nil
	return nil
}

// Range parcourt en streaming continu les clés actives dans l'intervalle [start, end[.
// Si start est nil, commence à la première clé. Si end est nil, va jusqu'à la fin.
// Invoque fn pour chaque paire. Si fn retourne false, l'itération s'arrête.
func (c *Cursor) Range(start, end []byte, fn func(key, val []byte) bool) error {
	var k, v []byte
	var ok bool

	if len(start) == 0 {
		k, v, ok = c.First()
	} else {
		k, v, ok = c.Seek(start)
	}

	for ok {
		if len(end) > 0 && bytes.Compare(k, end) >= 0 {
			break
		}
		if !fn(k, v) {
			break
		}
		k, v, ok = c.Next()
	}
	return nil
}

// Cursor ouvre un nouvel itérateur bidirectionnel sur le Shard.
// Le Shard épingle le tas actif (liveHeap) jusqu'à l'appel de c.Close().
func (s *Shard) Cursor() (*Cursor, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	h := s.pinLive()
	if h == nil {
		return nil, unix.EBADF
	}

	var snap [16]byte
	if s.hasLast {
		snap = s.lastID
	} else {
		for i := range snap {
			snap[i] = 0xFF
		}
	}

	return newCursor(h.buf, h.root, snap, s, h), nil
}

// Range exécute un parcours en streaming zéro-allocation sur le Shard dans [start, end[.
func (s *Shard) Range(start, end []byte, fn func(key, val []byte) bool) error {
	c, err := s.Cursor()
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Range(start, end, fn)
}

// Cursor ouvre un nouvel itérateur bidirectionnel sur la vue snapshot isolée.
func (v *View) Cursor() (*Cursor, error) {
	if v == nil || v.heap == nil {
		return nil, unix.EBADF
	}
	if v.pin != nil {
		v.pin.refs.Add(1)
	}
	return newCursor(v.heap, v.root, v.snap, nil, v.pin), nil
}

// Range exécute un parcours en streaming zéro-allocation sur la View dans [start, end[.
func (v *View) Range(start, end []byte, fn func(key, val []byte) bool) error {
	c, err := v.Cursor()
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Range(start, end, fn)
}
