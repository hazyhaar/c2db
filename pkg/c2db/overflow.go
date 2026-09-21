// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	cellKindInline        byte = 0
	cellKindOverflow      byte = 1
	TypeOverflow               = uint8(3)
	InlineCutoff               = 2048       // Valeurs <= 2 Ko stockées inline avec préfixe cellKindInline dans les feuilles
	OverflowDescLen            = 13         // cellKindOverflow (1 octet) + total_len uint32 LE (4 octets) + head_page uint64 LE (8 octets)
	OverflowChunkCapacity      = pageN - 64 // 16 320 octets utiles par page d'overflow
)

var (
	ErrCorruptedOverflowChain = errors.New("c2db: corrupted overflow chain")
	ErrOverflowOutOfBounds    = errors.New("c2db: overflow page out of bounds")
	ErrOverflowCycleDetected  = errors.New("c2db: cycle detected in overflow chain")
)

// encodeValue prépare la charge utile à stocker dans la cellule de feuille B-Tree.
// Si len(val) <= InlineCutoff, la valeur est préfixée par cellKindInline (0).
// Si len(val) > InlineCutoff, des pages d'overflow sont allouées et un descripteur canonique de 13 octets préfixé par cellKindOverflow (1) est retourné.
func (s *Shard) encodeValue(val []byte) (cellVal []byte, isOverflow bool, headPage uint64, numPages uint64, err error) {
	if len(val) <= InlineCutoff {
		out := make([]byte, 1+len(val))
		out[0] = cellKindInline
		copy(out[1:], val)
		return out, false, 0, 0, nil
	}
	if uint64(len(val)) > 0xFFFFFFFF {
		return nil, false, 0, 0, ErrPayloadTooLarge
	}

	head, nPages, err := s.allocOverflowPages(val)
	if err != nil {
		return nil, false, 0, 0, err
	}

	desc := make([]byte, OverflowDescLen)
	desc[0] = cellKindOverflow
	binary.LittleEndian.PutUint32(desc[1:5], uint32(len(val)))
	binary.LittleEndian.PutUint64(desc[5:13], head)
	return desc, true, head, nPages, nil
}

// decodeValue extrait la valeur utilisateur depuis le contenu brut de la cellule de feuille.
// Si le contenu correspond à un descripteur d'overflow de 13 octets commençant par cellKindOverflow (1), la chaîne est résolue.
// Si le premier octet est cellKindInline (0), la sous-tranche utile rawVal[1:] est retournée en zéro allocation.
// Sinon, la tranche brute est retournée en repli de compatibilité.
func decodeValue(heap []byte, heapPages uint64, rawVal []byte) ([]byte, error) {
	if len(rawVal) == 0 {
		return rawVal, nil
	}
	if len(rawVal) == OverflowDescLen && rawVal[0] == cellKindOverflow {
		totalLen := binary.LittleEndian.Uint32(rawVal[1:5])
		headPage := binary.LittleEndian.Uint64(rawVal[5:13])
		return readOverflowChain(heap, heapPages, headPage, totalLen)
	}
	if rawVal[0] == cellKindInline {
		return rawVal[1:], nil
	}
	return rawVal, nil
}

// isOverflowDescriptor vérifie si une tranche est un descripteur d'overflow valide.
func isOverflowDescriptor(rawVal []byte) (totalLen uint32, headPage uint64, ok bool) {
	if len(rawVal) == OverflowDescLen && rawVal[0] == cellKindOverflow {
		tLen := binary.LittleEndian.Uint32(rawVal[1:5])
		hPage := binary.LittleEndian.Uint64(rawVal[5:13])
		return tLen, hPage, true
	}
	return 0, 0, false
}

// checkOverflowDesc extrait les métadonnées de pages d'overflow si la cellule contient un descripteur d'overflow.
func checkOverflowDesc(rawVal []byte) (isOverflow bool, headPage uint64, numPages uint64) {
	if tLen, hPage, ok := isOverflowDescriptor(rawVal); ok {
		nPages := (uint64(tLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity
		return true, hPage, nPages
	}
	return false, 0, 0
}

// overflowChainRef désigne une chaîne de débordement par sa page de tête et le
// nombre de pages déduit de la longueur totale portée par le descripteur.
type overflowChainRef struct {
	head  uint64
	pages uint64
}

// collectOverflowChainsForDelete rassemble, sans muter les tampons, les chaînes
// de débordement des cellules de la feuille cible qui portent key. Le noyau
// Db_bt_del_heap retire physiquement toutes ces cellules ; lire leurs
// descripteurs au préalable est la condition pour que la suppression puisse
// libérer les pages au lieu de les laisser orphelines. La descente s'arrête à la
// feuille cible, comme la suppression.
func (s *Shard) collectOverflowChainsForDelete(src, key []byte) []overflowChainRef {
	if s == nil || len(key) == 0 || uint64(len(src)) < s.shardBytes {
		return nil
	}
	leaf, ok := findTargetLeaf(src, s.heapRoot, key)
	if !ok || leaf >= s.heapPages {
		return nil
	}
	off := leaf * pageN
	if off+pageN > uint64(len(src)) {
		return nil
	}
	page := src[off : off+pageN]
	if page[BT_TypeOffset] != BT_TypeLeaf {
		return nil
	}
	nslots := binary.LittleEndian.Uint16(page[22:24])
	var refs []overflowChainRef
	for i := uint16(0); i < nslots; i++ {
		slotOff := uint64(64) + uint64(i)*2
		if slotOff+2 > pageN {
			break
		}
		cellOff := uint64(binary.LittleEndian.Uint16(page[slotOff : slotOff+2]))
		if cellOff+4 > pageN {
			continue
		}
		cklen := uint64(binary.LittleEndian.Uint16(page[cellOff : cellOff+2]))
		cvlen := uint64(binary.LittleEndian.Uint16(page[cellOff+2 : cellOff+4]))
		if cklen != uint64(len(key)) || cellOff+4+cklen+BT_IDLen+cvlen > pageN {
			continue
		}
		if !bytes.Equal(page[cellOff+4:cellOff+4+cklen], key) {
			continue
		}
		valOff := cellOff + 4 + cklen + BT_IDLen
		if cvlen != OverflowDescLen {
			continue
		}
		if tLen, hPage, ok := isOverflowDescriptor(page[valOff : valOff+OverflowDescLen]); ok {
			nPages := (uint64(tLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity
			refs = append(refs, overflowChainRef{head: hPage, pages: nPages})
		}
	}
	return refs
}

// retireOverflowChain marque mortes les pages d'une chaîne de débordement
// devenue orpheline : chacune est remise à zéro, typée 0 (ni B-tree ni
// débordement) et ré-estampillée CRC32-C, puis marquée sale pour publication.
// Le parcours est borné par le nombre de pages du descripteur et s'interrompt
// dès qu'une page n'est pas de type débordement : une page encore vivante n'est
// jamais altérée.
func (s *Shard) retireOverflowChain(headPage, numPages uint64) {
	if headPage == 0 || headPage >= s.heapPages || numPages == 0 {
		return
	}
	curr := headPage
	walked := uint64(0)
	for curr != 0 && walked < numPages {
		if curr >= s.heapPages || curr >= s.heapUsed {
			break
		}
		off := curr * pageN
		if off+pageN > uint64(len(s.dirty)) {
			break
		}
		page := s.dirty[off : off+pageN]
		if page[BT_TypeOffset] != TypeOverflow {
			break
		}
		next := binary.LittleEndian.Uint64(page[32:40])
		clear(page)
		_ = C2db_crc32c_fullpage_store(page, pageN)
		s.markDirty(curr)
		walked++
		curr = next
	}
}

// retireDeletedOverflow consigne comme mortes les chaînes rendues orphelines par
// une suppression, puis réclame immédiatement le suffixe de queue devenu libre.
// Les pages réclamées ne sont pas publiées (elles sortent de [1, heapUsed)) ;
// les pages mortes intérieures restent marquées pour le repack, qui les
// élimine par reachability.
func (s *Shard) retireDeletedOverflow(chains []overflowChainRef) {
	if len(chains) == 0 {
		return
	}
	for _, ch := range chains {
		s.retireOverflowChain(ch.head, ch.pages)
	}
	s.trimDeadOverflowTail()
}

// trimDeadOverflowTail rétracte heapUsed tant que la dernière page allouée est
// libre (type 0). Une page de type 1 ou 2 arrête la rétraction : la queue vive
// n'est jamais franchie.
func (s *Shard) trimDeadOverflowTail() {
	for s.heapUsed > 1 {
		pg := s.heapUsed - 1
		off := pg * pageN
		if off+pageN > uint64(len(s.dirty)) {
			return
		}
		if s.dirty[off+BT_TypeOffset] != 0 {
			return
		}
		s.heapUsed--
	}
}

// allocOverflowPages alloue des pages d'overflow contiguës à partir de s.heapUsed dans s.dirty.
func (s *Shard) allocOverflowPages(val []byte) (headPage uint64, numPages uint64, err error) {
	totalLen := uint64(len(val))
	numPages = (totalLen + OverflowChunkCapacity - 1) / OverflowChunkCapacity

	if s.heapUsed >= s.heapWatermark || numPages > s.heapWatermark-s.heapUsed {
		ok, compactErr := s.tryAutoCompact()
		if compactErr != nil {
			return 0, 0, compactErr
		}
		if !ok {
			return 0, 0, ErrHeapFull
		}
		if s.heapUsed >= s.heapWatermark || numPages > s.heapWatermark-s.heapUsed {
			return 0, 0, ErrHeapFull
		}
	}

	headPage = s.heapUsed
	currPage := headPage
	valOffset := uint64(0)

	for seq := uint64(0); seq < numPages; seq++ {
		nextPage := uint64(0)
		if seq+1 < numPages {
			nextPage = currPage + 1
		}
		chunkLen := uint64(OverflowChunkCapacity)
		if totalLen-valOffset < chunkLen {
			chunkLen = totalLen - valOffset
		}

		pageOff := currPage * pageN
		pageBuf := s.dirty[pageOff : pageOff+pageN]
		for i := 0; i < 64; i++ {
			pageBuf[i] = 0
		}
		pageBuf[20] = TypeOverflow
		binary.LittleEndian.PutUint64(pageBuf[32:40], nextPage)
		binary.LittleEndian.PutUint32(pageBuf[40:44], uint32(chunkLen))
		binary.LittleEndian.PutUint32(pageBuf[44:48], uint32(seq))

		copy(pageBuf[64:64+chunkLen], val[valOffset:valOffset+chunkLen])
		_ = C2db_crc32c_fullpage_store(pageBuf, pageN)

		s.markDirty(currPage)

		valOffset += chunkLen
		currPage = nextPage
	}

	s.heapUsed += numPages
	return headPage, numPages, nil
}

// readOverflowChain parcourt la chaîne de pages d'overflow et assemble la charge utile complète.
// Verrouillage UB Pinning : chaque chunk est ancré à l'offset physique 64 et borné à OverflowChunkCapacity (16320).
// Tout chunk hors [64,64+chunkLen<=16384[ ou violant valOffset+chunkLen<=totalLen est rejeté fail-closed.
func readOverflowChain(heap []byte, heapPages, headPage uint64, totalLen uint32) ([]byte, error) {
	if headPage == 0 || headPage >= heapPages {
		return nil, ErrOverflowOutOfBounds
	}
	maxPages := uint64((uint64(totalLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity)
	out := make([]byte, totalLen)
	currPage := headPage
	walked := uint64(0)
	valOffset := uint64(0)

	for currPage != 0 && walked <= maxPages && valOffset < uint64(totalLen) {
		if currPage >= heapPages {
			return nil, ErrOverflowOutOfBounds
		}
		pageOff := currPage * pageN
		if pageOff+pageN > uint64(len(heap)) {
			return nil, ErrOverflowOutOfBounds
		}
		pageBuf := heap[pageOff : pageOff+pageN]
		if pageBuf[20] != TypeOverflow {
			return nil, ErrCorruptedOverflowChain
		}

		nextPage := binary.LittleEndian.Uint64(pageBuf[32:40])
		chunkLen := uint64(binary.LittleEndian.Uint32(pageBuf[40:44]))
		seq := binary.LittleEndian.Uint32(pageBuf[44:48])

		if uint64(seq) != walked {
			return nil, ErrCorruptedOverflowChain
		}
		// UB Pinning : le chunk est physiquement à [64, 64+chunkLen[ dans la page
		if chunkLen > OverflowChunkCapacity {
			return nil, ErrCorruptedOverflowChain
		}
		if uint64(64)+chunkLen > pageN {
			return nil, ErrCorruptedOverflowChain
		}
		if valOffset > uint64(totalLen) || chunkLen > uint64(totalLen)-valOffset {
			return nil, ErrCorruptedOverflowChain
		}

		copy(out[valOffset:valOffset+chunkLen], pageBuf[64:64+chunkLen])
		valOffset += chunkLen
		walked++
		currPage = nextPage
	}

	if valOffset != uint64(totalLen) {
		return nil, ErrCorruptedOverflowChain
	}
	return out, nil
}

// ValueReader retourne un io.ReadCloser streamant la valeur sans nécessiter d'allocation globale en mémoire.
func (c *Cursor) ValueReader() (io.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		return nil, errors.New("c2db: cursor not valid")
	}

	if totalLen, headPage, ok := isOverflowDescriptor(c.currVal); ok {
		if c.pin != nil {
			c.pin.refs.Add(1)
		}
		return &overflowStreamReader{
			heap:      c.heap,
			heapPages: c.npages,
			currPage:  headPage,
			totalLen:  totalLen,
			maxPages:  uint64((uint64(totalLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity),
			pin:       c.pin,
		}, nil
	}

	// Valeur inline
	val, err := decodeValue(c.heap, c.npages, c.currVal)
	if err != nil {
		return nil, err
	}
	if c.pin != nil {
		c.pin.refs.Add(1)
	}
	return &inlineStreamReader{Reader: bytes.NewReader(val), pin: c.pin}, nil
}

type inlineStreamReader struct {
	*bytes.Reader
	pin *liveHeap
}

func (r *inlineStreamReader) Close() error {
	if r.pin != nil {
		r.pin.refs.Add(-1)
		r.pin = nil
	}
	return nil
}

type overflowStreamReader struct {
	heap       []byte
	heapPages  uint64
	currPage   uint64
	activePage uint64
	totalLen   uint32
	valOffset  uint32
	pageOff    uint32
	chunkLen   uint32
	walked     uint64
	maxPages   uint64
	pin        *liveHeap
}

func (r *overflowStreamReader) Close() error {
	if r.pin != nil {
		r.pin.refs.Add(-1)
		r.pin = nil
	}
	return nil
}

func (r *overflowStreamReader) Read(p []byte) (int, error) {
	if r.valOffset >= r.totalLen {
		return 0, io.EOF
	}

	for r.pageOff >= r.chunkLen {
		if r.currPage == 0 || r.walked >= r.maxPages {
			if r.valOffset < r.totalLen {
				return 0, io.ErrUnexpectedEOF
			}
			return 0, io.EOF
		}
		if r.currPage >= r.heapPages {
			return 0, ErrOverflowOutOfBounds
		}
		base := r.currPage * pageN
		if base+pageN > uint64(len(r.heap)) {
			return 0, ErrOverflowOutOfBounds
		}
		pageBuf := r.heap[base : base+pageN]
		if pageBuf[20] != TypeOverflow {
			return 0, ErrCorruptedOverflowChain
		}

		thisChunkLen := binary.LittleEndian.Uint32(pageBuf[40:44])
		seq := binary.LittleEndian.Uint32(pageBuf[44:48])

		// UB Pinning : chaque chunk est à offset 64 et borné à OverflowChunkCapacity
		if uint64(thisChunkLen) > OverflowChunkCapacity {
			return 0, ErrCorruptedOverflowChain
		}
		if 64+uint64(thisChunkLen) > pageN {
			return 0, ErrCorruptedOverflowChain
		}
		if uint64(r.valOffset) > uint64(r.totalLen) || uint64(thisChunkLen) > uint64(r.totalLen)-uint64(r.valOffset) {
			return 0, ErrCorruptedOverflowChain
		}
		if uint64(seq) != r.walked {
			return 0, ErrCorruptedOverflowChain
		}

		r.activePage = r.currPage
		r.currPage = binary.LittleEndian.Uint64(pageBuf[32:40])
		r.chunkLen = thisChunkLen
		r.pageOff = 0
		r.walked++
	}

	// Le chunk utile est à [64, 64+chunkLen[ dans la page active
	if r.pageOff >= r.chunkLen {
		return 0, io.ErrUnexpectedEOF
	}
	base := (r.activePage * pageN) + 64 + uint64(r.pageOff)
	available := r.chunkLen - r.pageOff
	if uint32(len(p)) < available {
		available = uint32(len(p))
	}
	if r.valOffset+available > r.totalLen {
		available = r.totalLen - r.valOffset
	}
	// Vérification de bornes physique avant copie : base+available <= (activePage+1)*pageN
	if uint64(available) > OverflowChunkCapacity || 64+uint64(r.pageOff)+uint64(available) > pageN {
		return 0, ErrCorruptedOverflowChain
	}
	if base+uint64(available) > uint64(len(r.heap)) {
		return 0, ErrOverflowOutOfBounds
	}
	if base+uint64(available) > (r.activePage+1)*pageN {
		return 0, ErrCorruptedOverflowChain
	}

	copy(p, r.heap[base:base+uint64(available)])
	r.pageOff += available
	r.valOffset += available
	return int(available), nil
}

// replayValidateOverflowChain valide l'intégrité intégrale de la chaîne d'overflow lors du replay.
// Vérifie : existence physique de chaque page, type TypeOverflow, validité CRC32-C pleine page,
// séquence séquentielle stricte (seq == walked), et bornes exactes des segments.
// Si une page manque dans pub mais existe sur le pager disque, elle est contrôlée et reportée.
// En cas de déchirure (torn-chunk) ou de CRC corrompu, retourne false pour faire ignorer le WAL record.
func (s *Shard) replayValidateOverflowChain(headPage uint64, totalLen uint32) bool {
	if headPage == 0 || headPage >= s.heapPages {
		return false
	}
	maxPages := uint64((uint64(totalLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity)
	currPage := headPage
	walked := uint64(0)
	valOffset := uint64(0)

	for currPage != 0 && walked <= maxPages && valOffset < uint64(totalLen) {
		if currPage >= s.heapPages {
			return false
		}
		pageOff := currPage * pageN
		var pg []byte

		if pageOff+pageN <= uint64(len(s.pub)) && s.pub[pageOff+BT_TypeOffset] == TypeOverflow {
			pg = s.pub[pageOff : pageOff+pageN]
		} else {
			diskPg, err := s.pager.GetPage(currPage * pageLBAs)
			if err != nil || len(diskPg) < int(pageN) || diskPg[BT_TypeOffset] != TypeOverflow {
				return false
			}
			if pageOff+pageN > uint64(len(s.dirty)) {
				return false
			}
			// Copie immédiate dans le tampon anonyme stable s.dirty :
			// élimine tout risque d'écrasement différé par réutilisation de slot dans le pager.
			// s.pub reste quant à lui strictement intact jusqu'au commit.
			copy(s.dirty[pageOff:pageOff+pageN], diskPg)
			s.markDirty(currPage)
			pg = s.dirty[pageOff : pageOff+pageN]
		}

		// Vérification d'intégrité bit-exacte du CRC32-C pleine page
		storedCRC := binary.LittleEndian.Uint32(pg[28:32])
		computedCRC := C2db_crc32c_fullpage(pg, pageN)
		if storedCRC != computedCRC {
			return false
		}

		nextPage := binary.LittleEndian.Uint64(pg[32:40])
		chunkLen := uint64(binary.LittleEndian.Uint32(pg[40:44]))
		seq := binary.LittleEndian.Uint32(pg[44:48])

		if uint64(seq) != walked {
			return false
		}
		// UB Pinning : chunk à offset 64, borné à OverflowChunkCapacity, et valOffset+chunkLen <= totalLen
		if chunkLen > OverflowChunkCapacity {
			return false
		}
		if 64+chunkLen > pageN {
			return false
		}
		if valOffset > uint64(totalLen) || chunkLen > uint64(totalLen)-valOffset {
			return false
		}

		valOffset += chunkLen
		walked++
		currPage = nextPage
	}

	if valOffset != uint64(totalLen) || walked != maxPages || currPage != 0 {
		return false
	}

	return true
}
