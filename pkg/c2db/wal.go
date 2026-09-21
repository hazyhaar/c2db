// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/mldsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
	"golang.org/x/sys/unix"
)

const (
	WALMagic      = uint32(0x43324442)
	walLenOff     = 4
	walIDOff      = 8
	walTypeOff    = 24
	walPayloadOff = 25
	walTagOff     = 4080
	walTagSize    = 16
	walMaxPayload = walTagOff - walPayloadOff

	walQuantumLBAs  = 32
	walQuantumBytes = walQuantumLBAs * LBASize // 128 KiB
	walChunkMagic   = uint32(0x4332434B)       // "C2CK"
	walChunkHdrSize = 16
	walChunkMaxData = walMaxPayload - walChunkHdrSize
)

type RecType byte

const (
	RecInvalid         RecType = 0
	RecPut             RecType = 1
	RecDel             RecType = 2
	RecCommitBatch     RecType = 3
	RecCheckpointBegin RecType = 4
	RecCheckpointEnd   RecType = 5
	RecTopoTransition  RecType = 6
	RecMigrate         RecType = 7
	RecMut             RecType = 8
	RecTxCommit        RecType = 9
)

type Record struct {
	ID      [16]byte
	Type    RecType
	Payload []byte
}

const walBatchHdr = 1 + 16 + 4

type WAL struct {
	dev       *Device
	path      string
	key       [32]byte
	next      uint64
	lsn       uint64
	block     []byte
	checkLBA  uint64
	hasCheck  bool
	packDepth int
	pending   []Record
	pendingN  int

	qBuf     []byte
	qLBA     uint64
	qCount   int
	probeBuf []byte
	// synced indique que tout octet écrit sur le dispositif du journal a fait
	// l'objet d'une barrière (dev.Flush) depuis la dernière écriture. Il évite
	// de réémettre une fdatasync redondante à la refermeture d'un pack dont les
	// enregistrements ont déjà été scellés.
	synced bool
}

var (
	errWALTooLong         = errors.New("c2db: wal record exceeds block")
	errWALBadTag          = errors.New("c2db: wal poly1305 tag mismatch")
	errWALMagic           = errors.New("c2db: wal bad magic")
	errWALCorruptedMedian = errors.New("c2db: corrupted median block in wal (downstream valid blocks exist)")
)

func CreateWAL(path string, size uint64, key [32]byte) (*WAL, error) {
	dev, err := Create(path, size)
	if err != nil {
		return nil, err
	}
	w, err := newWAL(dev, key, path)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	return w, nil
}

func OpenWAL(path string, key [32]byte) (*WAL, error) {
	dev, err := Open(path)
	if err != nil {
		return nil, err
	}
	w, err := newWAL(dev, key, path)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	if err := w.scanTip(); err != nil {
		_ = w.Close()
		return nil, err
	}
	return w, nil
}

func newWAL(dev *Device, key [32]byte, path string) (*WAL, error) {
	block, err := unix.Mmap(-1, 0, LBASize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	qBuf, err := unix.Mmap(-1, 0, walQuantumBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		_ = unix.Munmap(block)
		return nil, err
	}
	probeBuf, err := unix.Mmap(-1, 0, LBASize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		_ = unix.Munmap(block)
		_ = unix.Munmap(qBuf)
		return nil, err
	}
	return &WAL{dev: dev, path: path, key: key, block: block, qBuf: qBuf, probeBuf: probeBuf}, nil
}

func (w *WAL) BeginPack() {
	if w == nil {
		return
	}
	w.packDepth++
}

func (w *WAL) EndPack() error {
	if w == nil {
		return unix.EBADF
	}
	if w.packDepth > 0 {
		w.packDepth--
	}
	if w.packDepth > 0 {
		return nil
	}
	return w.Flush()
}

// EndPackNoFlush referme un pack sans émettre de barrière. L'appelant garantit
// que les enregistrements du pack sont déjà durablement écrits, ou qu'il n'en
// reste aucun : la barrière est alors déjà acquise (cas d'une transaction qui a
// vidé le journal avant publication) ou sans objet. La refermeture maintient
// l'équilibre de packDepth, exigé par la suite du protocole.
func (w *WAL) EndPackNoFlush() {
	if w == nil {
		return
	}
	if w.packDepth > 0 {
		w.packDepth--
	}
}

// SyncIfDirty émet la barrière du journal seulement si des octets restent à
// écrire ou n'ont pas été synchronisés depuis leur écriture. Un pack refermé
// après un Flush explicite (transaction) ne paie donc pas de fdatasync
// supplémentaire, tandis qu'un lot groupé non encore vidé est scellé.
func (w *WAL) SyncIfDirty() error {
	if w == nil {
		return unix.EBADF
	}
	if w.pendingN > 0 || w.qCount > 0 || !w.synced {
		return w.Flush()
	}
	return nil
}

func (w *WAL) DiscardPack() {
	if w == nil {
		return
	}
	w.pending = w.pending[:0]
	w.pendingN = 0
	w.qCount = 0
	w.next = w.qLBA
	w.lsn = w.next
	if w.packDepth > 0 {
		w.packDepth--
	}
}

func (w *WAL) rollbackTo(initialNext uint64) {
	if w == nil {
		return
	}
	w.pending = w.pending[:0]
	w.pendingN = 0
	if initialNext >= w.qLBA && initialNext <= w.next {
		w.next = initialNext
		w.qCount = int(initialNext - w.qLBA)
		w.lsn = w.next
	}
	if w.packDepth > 0 {
		w.packDepth--
	}
}

func (w *WAL) Append(rec Record) error {
	if err := w.ready(); err != nil {
		return err
	}
	maxLBA := w.dev.size / LBASize
	if w.next >= maxLBA {
		if w.hasCheck {
			if err := w.TruncateCheckpoint(); err != nil {
				return err
			}
		}
		if w.next >= maxLBA {
			probeEmit(0, ProbeOpWALFull, w.next, 0, 0, 0, fmt.Sprintf("wal full at lba %d", w.next))
			return fmt.Errorf("c2db: wal full (lba %d)", w.next)
		}
	}
	if walIsCheckpoint(rec.Type) {
		if err := w.flushPending(); err != nil {
			return err
		}
		return w.writeOne(rec)
	}
	if len(rec.Payload) > walMaxPayload {
		if err := w.flushPending(); err != nil {
			return err
		}
		return w.writeChunked(rec)
	}
	if w.packDepth > 0 {
		return w.appendPacked(rec)
	}
	return w.writeOne(rec)
}

func (w *WAL) writeChunked(rec Record) error {
	totalLen := len(rec.Payload)
	numChunks := (totalLen + walChunkMaxData - 1) / walChunkMaxData
	if numChunks > 0xFFFF {
		return fmt.Errorf("c2db: payload too large (%d bytes, %d chunks > 65535)", totalLen, numChunks)
	}
	maxLBA := w.dev.size / LBASize
	if w.next+uint64(numChunks) > maxLBA {
		if w.hasCheck {
			if err := w.TruncateCheckpoint(); err != nil {
				return err
			}
		}
		if w.next+uint64(numChunks) > maxLBA {
			probeEmit(0, ProbeOpWALFull, w.next, 0, 0, 0, fmt.Sprintf("wal full for chunked write (needs %d LBAs, at %d)", numChunks, w.next))
			return fmt.Errorf("c2db: wal full (needs %d LBAs, at %d)", numChunks, w.next)
		}
	}

	for i := 0; i < numChunks; i++ {
		start := i * walChunkMaxData
		end := start + walChunkMaxData
		if end > totalLen {
			end = totalLen
		}
		chunkData := rec.Payload[start:end]
		chunkPayload := make([]byte, walChunkHdrSize+len(chunkData))
		binary.LittleEndian.PutUint32(chunkPayload[0:4], walChunkMagic)
		binary.LittleEndian.PutUint16(chunkPayload[4:6], uint16(i))
		binary.LittleEndian.PutUint16(chunkPayload[6:8], uint16(numChunks))
		binary.LittleEndian.PutUint32(chunkPayload[8:12], uint32(totalLen))
		chunkPayload[12] = byte(rec.Type)
		copy(chunkPayload[walChunkHdrSize:], chunkData)

		subRec := Record{
			ID:      rec.ID,
			Type:    rec.Type,
			Payload: chunkPayload,
		}
		if err := w.writeOne(subRec); err != nil {
			return err
		}
	}
	return nil
}

func packedRecSize(rec Record) int {
	return walBatchHdr + len(rec.Payload)
}

func (w *WAL) appendPacked(rec Record) error {
	sz := packedRecSize(rec)
	if sz > walMaxPayload {
		return errWALTooLong
	}
	maxLBA := w.dev.size / LBASize
	if w.next >= maxLBA {
		if w.hasCheck {
			if err := w.TruncateCheckpoint(); err != nil {
				return err
			}
		}
		if w.next >= maxLBA {
			probeEmit(0, ProbeOpWALFull, w.next, 0, 0, 0, fmt.Sprintf("wal full at lba %d", w.next))
			return fmt.Errorf("c2db: wal full (lba %d)", w.next)
		}
	}
	if len(w.pending) > 0 && w.pendingN+sz > walMaxPayload {
		if err := w.flushPending(); err != nil {
			return err
		}
		if w.next >= maxLBA {
			if w.hasCheck {
				if err := w.TruncateCheckpoint(); err != nil {
					return err
				}
			}
			if w.next >= maxLBA {
				probeEmit(0, ProbeOpWALFull, w.next, 0, 0, 0, fmt.Sprintf("wal full at lba %d after flush", w.next))
				return fmt.Errorf("c2db: wal full (lba %d)", w.next)
			}
		}
	}
	w.pending = append(w.pending, rec)
	w.pendingN += sz
	return nil
}

func packBatch(recs []Record) []byte {
	n := 0
	for i := range recs {
		n += packedRecSize(recs[i])
	}
	out := make([]byte, n)
	off := 0
	for i := range recs {
		out[off] = byte(recs[i].Type)
		copy(out[off+1:off+17], recs[i].ID[:])
		binary.LittleEndian.PutUint32(out[off+17:off+21], uint32(len(recs[i].Payload)))
		off += walBatchHdr
		copy(out[off:], recs[i].Payload)
		off += len(recs[i].Payload)
	}
	return out
}

func unpackBatch(payload []byte) ([]Record, bool) {
	var out []Record
	for len(payload) > 0 {
		if len(payload) < walBatchHdr {
			return nil, false
		}
		typ := RecType(payload[0])
		var id [16]byte
		copy(id[:], payload[1:17])
		plen := binary.LittleEndian.Uint32(payload[17:21])
		payload = payload[walBatchHdr:]
		if uint32(len(payload)) < plen {
			return nil, false
		}
		p := make([]byte, plen)
		copy(p, payload[:plen])
		payload = payload[plen:]
		out = append(out, Record{ID: id, Type: typ, Payload: p})
	}
	return out, true
}

func (w *WAL) flushPending() error {
	if len(w.pending) == 0 {
		return nil
	}
	var rec Record
	if len(w.pending) == 1 {
		rec = w.pending[0]
	} else {
		rec = Record{
			ID:      w.pending[len(w.pending)-1].ID,
			Type:    RecCommitBatch,
			Payload: packBatch(w.pending),
		}
	}
	if err := w.writeOne(rec); err != nil {
		return err
	}
	w.pending = w.pending[:0]
	w.pendingN = 0
	return nil
}

func (w *WAL) flushQuantum() error {
	if w.qCount == 0 {
		return nil
	}
	nBytes := w.qCount * LBASize
	err := w.dev.Write(w.qLBA, w.qBuf[:nBytes])
	if err != nil {
		return err
	}
	w.qCount = 0
	w.qLBA = w.next
	w.synced = false
	return nil
}

func (w *WAL) writeOne(rec Record) error {
	maxLBA := w.dev.size / LBASize
	if w.next >= maxLBA {
		if w.hasCheck {
			if err := w.TruncateCheckpoint(); err != nil {
				return err
			}
		}
		if w.next >= maxLBA {
			return fmt.Errorf("c2db: wal full (lba %d)", w.next)
		}
	}
	if w.qCount == 0 {
		w.qLBA = w.next
	}
	off := w.qCount * LBASize
	target := w.qBuf[off : off+LBASize]
	if Db_wal_pack(target, uint64(LBASize), rec.ID[:], byte(rec.Type), rec.Payload, uint64(len(rec.Payload))) == 0 {
		return errWALTooLong
	}
	sealWAL(target, w.key)
	w.qCount++
	w.next++
	w.lsn++
	w.synced = false
	if w.qCount == walQuantumLBAs {
		return w.flushQuantum()
	}
	return nil
}

type chunkCollector struct {
	origType RecType
	total    uint16
	totalLen uint32
	parts    map[uint16][]byte
	count    uint16
}

// ReplayResumable parcourt le journal depuis le LBA 0 et rend tous les
// enregistrements, plus l'index du premier à appliquer, c'est-à-dire le premier
// situé après le dernier pointage durable (checkLBA+1). Le préfixe reste observé
// par l'appelant pour réamorcer le compteur et le dernier identifiant, mais il
// n'est pas réappliqué sur le tas : un marqueur RecCheckpointEnd durable
// implique que tout ce qui le précède est déjà dans data.img.
func (w *WAL) ReplayResumable() ([]Record, int, error) {
	if err := w.ready(); err != nil {
		return nil, 0, err
	}
	if err := w.flushPending(); err != nil {
		return nil, 0, err
	}
	if err := w.flushQuantum(); err != nil {
		return nil, 0, err
	}
	resumeAt := 0
	chunks := make(map[[16]byte]*chunkCollector)
	out := make([]Record, 0, w.next)
	for lba := uint64(0); lba < w.next; lba++ {
		if err := w.dev.Read(lba, w.block); err != nil {
			return nil, 0, err
		}
		if binary.LittleEndian.Uint32(w.block[:4]) != WALMagic {
			return nil, 0, errWALMagic
		}
		if !verifyWAL(w.block, w.key) {
			return nil, 0, errWALBadTag
		}
		var rec Record
		d := Db_wal_unpack(w.block, uint64(LBASize), rec.ID[:])
		if d.Ok == 0 {
			if d.Magic != WALMagic {
				return nil, 0, errWALMagic
			}
			return nil, 0, errWALTooLong
		}
		rec.Type = RecType(d.Typ)
		off := int(Db_wal_payload_off())
		rec.Payload = make([]byte, d.Plen)
		copy(rec.Payload, w.block[off:off+int(d.Plen)])
		if rec.Type == RecCommitBatch {
			inner, ok := unpackBatch(rec.Payload)
			if !ok {
				return nil, 0, errPayload
			}
			out = append(out, inner...)
			continue
		}
		if len(rec.Payload) >= walChunkHdrSize && binary.LittleEndian.Uint32(rec.Payload[0:4]) == walChunkMagic {
			idx := binary.LittleEndian.Uint16(rec.Payload[4:6])
			total := binary.LittleEndian.Uint16(rec.Payload[6:8])
			totalLen := binary.LittleEndian.Uint32(rec.Payload[8:12])
			origType := RecType(rec.Payload[12])
			data := rec.Payload[walChunkHdrSize:]

			col, exists := chunks[rec.ID]
			if !exists {
				col = &chunkCollector{
					origType: origType,
					total:    total,
					totalLen: totalLen,
					parts:    make(map[uint16][]byte, total),
				}
				chunks[rec.ID] = col
			}
			if _, already := col.parts[idx]; !already {
				col.parts[idx] = data
				col.count++
			}
			if col.count == col.total {
				fullPayload := make([]byte, col.totalLen)
				var written int
				for ci := uint16(0); ci < col.total; ci++ {
					pData := col.parts[ci]
					copy(fullPayload[written:], pData)
					written += len(pData)
				}
				delete(chunks, rec.ID)
				out = append(out, Record{
					ID:      rec.ID,
					Type:    col.origType,
					Payload: fullPayload,
				})
			}
			continue
		}
		out = append(out, rec)
		// Le WAL empaquette plusieurs enregistrements par LBA : reprendre au
		// LBA suivant le marqueur sauterait la queue qui partage ce LBA. On
		// reprend donc juste apres le dernier marqueur, par index de record.
		if walIsCheckpoint(rec.Type) {
			resumeAt = len(out)
		}
	}
	return out, resumeAt, nil
}

// Replay conserve le contrat historique : tous les enregistrements, sans point
// de reprise. Les appelants qui veulent éviter de réappliquer le préfixe déjà
// durable emploient ReplayResumable.
func (w *WAL) Replay() ([]Record, error) {
	recs, _, err := w.ReplayResumable()
	return recs, err
}

func (w *WAL) Flush() error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.flushPending(); err != nil {
		return err
	}
	if err := w.flushQuantum(); err != nil {
		return err
	}
	if err := w.dev.Flush(); err != nil {
		return err
	}
	w.synced = true
	return nil
}

func (w *WAL) Checkpoint() error {
	if err := w.ready(); err != nil {
		return err
	}
	check := w.next
	var payload [8]byte
	binary.LittleEndian.PutUint64(payload[:], check+1)
	if err := w.Append(Record{Type: RecCheckpointEnd, Payload: payload[:]}); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	w.checkLBA = check
	w.hasCheck = true
	return nil
}

func (w *WAL) Compact() error {
	if err := w.ready(); err != nil {
		return err
	}
	if !w.hasCheck {
		return nil
	}
	return w.CompactTo(w.path + ".arch")
}

// TruncateCheckpoint recycle l'espace du journal jusqu'au dernier point de contrôle,
// sans générer d'archive externe. Le point de contrôle terminal est repositionné
// au LBA 0 et les blocs suivants sont remis à zéro.
func (w *WAL) TruncateCheckpoint() error {
	return w.compactTo("", nil, nil)
}

// NeedsRecycle indique si le journal WAL a atteint ou dépassé la moitié de sa capacité.
func (w *WAL) NeedsRecycle() bool {
	if w == nil || w.dev == nil {
		return false
	}
	maxLBA := w.dev.size / LBASize
	return w.next >= maxLBA/2
}

func (w *WAL) CompactTo(archPath string) error {
	return w.compactTo(archPath, nil, nil)
}

func (w *WAL) CompactToEnc(archPath string, encKey [32]byte) error {
	return w.compactTo(archPath, &encKey, nil)
}

func (w *WAL) CompactToSigned(archPath string, sk *mldsa.PrivateKey) error {
	return w.compactTo(archPath, nil, sk)
}

func (w *WAL) CompactToEncSigned(archPath string, encKey [32]byte, sk *mldsa.PrivateKey) error {
	return w.compactTo(archPath, &encKey, sk)
}

func (w *WAL) compactTo(archPath string, encKey *[32]byte, sk *mldsa.PrivateKey) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.flushPending(); err != nil {
		return err
	}
	if err := w.flushQuantum(); err != nil {
		return err
	}
	if !w.hasCheck {
		return nil
	}
	src := w.checkLBA
	oldNext := w.next
	nPrefix := src
	if src > oldNext {
		nPrefix = oldNext
		src = oldNext
	}
	readBlk := func(i uint64) error {
		return w.dev.Read(i, w.block)
	}
	if archPath != "" {
		if err := writeWALArchive(archPath, w.key, 0, 0, nPrefix, nPrefix, readBlk, w.block, encKey, sk); err != nil {
			return err
		}
	}
	nKeep := uint64(0)
	if src < oldNext {
		nKeep = oldNext - src
	}
	firstIsCheck := false
	if nKeep > 0 {
		if err := w.dev.Read(src, w.block); err != nil {
			return err
		}
		firstIsCheck = walIsCheckpoint(RecType(w.block[walTypeOff]))
	}
	for i := uint64(0); i < nKeep; i++ {
		if err := w.dev.Read(src+i, w.block); err != nil {
			return err
		}
		if err := w.dev.Write(i, w.block); err != nil {
			return err
		}
	}
	if !firstIsCheck {
		maxLBA := w.dev.size / LBASize
		if nKeep >= maxLBA {
			return fmt.Errorf("c2db: wal full (lba %d)", nKeep)
		}
		for i := nKeep; i > 0; i-- {
			if err := w.dev.Read(i-1, w.block); err != nil {
				return err
			}
			if err := w.dev.Write(i, w.block); err != nil {
				return err
			}
		}
		nKeep++
	}
	var payload [8]byte
	binary.LittleEndian.PutUint64(payload[:], nKeep)
	rec := Record{Type: RecCheckpointEnd, Payload: payload[:]}
	if Db_wal_pack(w.block, uint64(LBASize), rec.ID[:], byte(rec.Type), rec.Payload, uint64(len(rec.Payload))) == 0 {
		return errWALTooLong
	}
	sealWAL(w.block, w.key)
	if err := w.dev.Write(0, w.block); err != nil {
		return err
	}
	clear(w.block)
	for i := nKeep; i < oldNext; i++ {
		if err := w.dev.Write(i, w.block); err != nil {
			return err
		}
	}
	if err := w.dev.Flush(); err != nil {
		return err
	}
	w.next = nKeep
	w.checkLBA = 0
	w.hasCheck = true
	return nil
}

func (w *WAL) Close() error {
	if err := w.ready(); err != nil {
		return err
	}
	_ = w.flushPending()
	_ = w.flushQuantum()
	err := w.dev.Close()
	w.dev = nil
	if w.block != nil {
		_ = unix.Munmap(w.block)
		w.block = nil
	}
	if w.qBuf != nil {
		_ = unix.Munmap(w.qBuf)
		w.qBuf = nil
	}
	if w.probeBuf != nil {
		_ = unix.Munmap(w.probeBuf)
		w.probeBuf = nil
	}
	return err
}

// KillWithoutFlush simule un arrêt matériel immédiat (SIGKILL / chute de tension) :
// les descripteurs de fichiers sont fermés et les mappages détachés SANS vidange
// des écritures en attente (aucun appel à flushPending, flushQuantum ou fsync).
func (w *WAL) KillWithoutFlush() error {
	if w == nil || w.dev == nil {
		return unix.EBADF
	}
	err := w.dev.Close()
	w.dev = nil
	if w.block != nil {
		_ = unix.Munmap(w.block)
		w.block = nil
	}
	if w.qBuf != nil {
		_ = unix.Munmap(w.qBuf)
		w.qBuf = nil
	}
	if w.probeBuf != nil {
		_ = unix.Munmap(w.probeBuf)
		w.probeBuf = nil
	}
	return err
}

func (w *WAL) ready() error {
	if w == nil || w.dev == nil {
		return unix.EBADF
	}
	return w.dev.checkReady()
}

func (w *WAL) scanTip() error {
	maxLBA := w.dev.size / LBASize
	w.next = 0
	w.hasCheck = false
	w.checkLBA = 0
	var tmpID [16]byte
	for lba := uint64(0); lba < maxLBA; lba++ {
		if err := w.dev.Read(lba, w.block); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(w.block[:4]) != WALMagic {
			probeEmit(0, ProbeOpWALScanTip, lba, 0, 0, 0, fmt.Sprintf("scanTip stopped at lba %d: magic absent", lba))
			if err := w.checkMedianRupture(lba+1, maxLBA); err != nil {
				return err
			}
			break
		}
		d := Db_wal_unpack(w.block, uint64(LBASize), tmpID[:])
		if d.Ok == 0 {
			probeEmit(0, ProbeOpWALScanTip, lba, 0, 0, 0, fmt.Sprintf("scanTip stopped at lba %d: incomplete header len=%d", lba, d.Len_))
			if err := w.checkMedianRupture(lba+1, maxLBA); err != nil {
				return err
			}
			break
		}
		if walIsCheckpoint(RecType(w.block[walTypeOff])) {
			// Le dernier pointage, quel que soit son LBA, fixe le point de
			// reprise. Le borner au LBA 0 rendait invisible tout pointage
			// posterieur, et le rejeu rejouait alors tout le prefixe.
			w.hasCheck = true
			w.checkLBA = lba
		}
		w.next = lba + 1
	}
	w.lsn = w.next
	w.qLBA = w.next
	return nil
}

func (w *WAL) checkMedianRupture(startLBA, maxLBA uint64) error {
	var probeID [16]byte
	for probeLBA := startLBA; probeLBA < maxLBA; probeLBA++ {
		if err := w.dev.Read(probeLBA, w.probeBuf); err != nil {
			// Erreur I/O ou rejet matériel : fail-closed absolu, interdiction de masquer l'erreur
			return fmt.Errorf("c2db: io error probing downstream wal at lba %d: %w", probeLBA, err)
		}
		if binary.LittleEndian.Uint32(w.probeBuf[:4]) == WALMagic {
			d := Db_wal_unpack(w.probeBuf, uint64(LBASize), probeID[:])
			if d.Ok == 1 && verifyWAL(w.probeBuf, w.key) {
				return errWALCorruptedMedian
			}
		}
	}
	return nil
}

func walIsCheckpoint(t RecType) bool {
	return t == RecCheckpointBegin || t == RecCheckpointEnd
}

// WALRepairReport est le compte-rendu d'une réparation « tronquer au trou ».
type WALRepairReport struct {
	Repaired             bool     // vrai si un trou médian avéré a été coupé
	StartLBA             uint64   // premier bloc invalide, point de coupe
	DiscardedBlocks      uint64   // blocs [StartLBA, ancienne pointe) mis en quarantaine
	DownstreamCheckpoint bool     // un pointage scellé valide existait en aval du trou
	DownstreamCheckLBA   uint64   // LBA du pointage aval le plus haut (si présent)
	QuarantinePath       string   // chemin du fichier de quarantaine
	QuarantineSHA256     [32]byte // empreinte SHA-256 des blocs écartés
	Next                 uint64   // next recalculé
	HasCheck             bool     // hasCheck recalculé sur le préfixe [0, StartLBA)
	CheckLBA             uint64   // checkLBA recalculé sur le préfixe
}

// RepairedWAL interroge le journal sans le modifier et rend le compte-rendu
// qu'une réparation produirait. Une erreur d'E/S est propagée telle quelle,
// sans fail-open.
func RepairedWAL(path string, key [32]byte) (*WALRepairReport, error) {
	return withWALDevice(path, key, func(w *WAL) (*WALRepairReport, error) {
		return w.previewMedianRupture()
	})
}

// RepairWAL répare un journal présentant un trou médian avéré : il coupe au
// premier bloc invalide, met en quarantaine les blocs [startLBA, ancienne
// pointe), les scelle par une empreinte SHA-256, remet à zéro le suffixe
// écarté, fsync, puis recalcule l'état en mémoire. L'acte est opt-in : un
// journal intact ou une queue tronquée normale n'entraînent aucun changement.
//
// RepairWAL suppose que l'appelant détient l'exclusivité du fichier ; l'entrée
// au niveau du shard est RepairShardWAL, qui acquiert le verrou écrivain.
func RepairWAL(path string, key [32]byte) (*WALRepairReport, error) {
	return withWALDevice(path, key, func(w *WAL) (*WALRepairReport, error) {
		return w.repairMedianRupture()
	})
}

// RepairShardWAL est l'entrée de réparation au niveau du shard. Elle acquiert
// le verrou écrivain (dir/.lock) avant toute lecture-écriture du journal, de
// sorte qu'aucun append ni pointage concurrent ne puisse s'intercaler.
func RepairShardWAL(dir string, key [32]byte) (*WALRepairReport, error) {
	lockPath := filepath.Join(dir, ".lock")
	lockFd, err := unix.Open(lockPath, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(lockFd) }()
	fl := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
	if err := unix.FcntlFlock(uintptr(lockFd), unix.F_OFD_SETLK, &fl); err != nil {
		if err == unix.EAGAIN || err == unix.EACCES {
			return nil, ErrWriterBusy
		}
		return nil, err
	}
	defer func() {
		unlock := unix.Flock_t{Type: unix.F_UNLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
		_ = unix.FcntlFlock(uintptr(lockFd), unix.F_OFD_SETLK, &unlock)
	}()
	return RepairWAL(filepath.Join(dir, "wal.img"), key)
}

func withWALDevice(path string, key [32]byte, fn func(*WAL) (*WALRepairReport, error)) (*WALRepairReport, error) {
	dev, err := Open(path)
	if err != nil {
		return nil, err
	}
	w, err := newWAL(dev, key, path)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	defer func() { _ = w.Close() }()
	return fn(w)
}

type walRepairScan struct {
	startLBA           uint64
	prefixHasCheck     bool
	prefixCheckLBA     uint64
	downstream         bool
	downstreamCheck    bool
	downstreamCheckLBA uint64
	oldTip             uint64
}

// analyzeRepair parcourt le journal une seule fois. Le préfixe [0, startLBA)
// est accepté comme scanTip l'accepte (magie et en-tête) ; le suffixe est sondé
// pour un bloc scellé valide en aval, signature du trou médian. Une erreur
// d'E/S interrompt le balayage et remonte fail-closed.
func (w *WAL) analyzeRepair() (walRepairScan, error) {
	var sc walRepairScan
	maxLBA := w.dev.size / LBASize
	var id [16]byte
	sc.startLBA = maxLBA
	prefixValid := true
	for lba := uint64(0); lba < maxLBA; lba++ {
		if err := w.dev.Read(lba, w.block); err != nil {
			return sc, err
		}
		if binary.LittleEndian.Uint32(w.block[:4]) != WALMagic {
			sc.startLBA = lba
			prefixValid = false
			break
		}
		d := Db_wal_unpack(w.block, uint64(LBASize), id[:])
		if d.Ok == 0 {
			sc.startLBA = lba
			prefixValid = false
			break
		}
		if walIsCheckpoint(RecType(w.block[walTypeOff])) {
			sc.prefixHasCheck = true
			sc.prefixCheckLBA = lba
		}
	}
	if prefixValid {
		return sc, nil
	}
	for lba := sc.startLBA; lba < maxLBA; lba++ {
		if err := w.dev.Read(lba, w.block); err != nil {
			return sc, err
		}
		if binary.LittleEndian.Uint32(w.block[:4]) != WALMagic {
			continue
		}
		if lba+1 > sc.oldTip {
			sc.oldTip = lba + 1
		}
		d := Db_wal_unpack(w.block, uint64(LBASize), id[:])
		if d.Ok == 1 && verifyWAL(w.block, w.key) {
			sc.downstream = true
			if walIsCheckpoint(RecType(w.block[walTypeOff])) {
				sc.downstreamCheck = true
				sc.downstreamCheckLBA = lba
			}
		}
	}
	return sc, nil
}

func (w *WAL) previewMedianRupture() (*WALRepairReport, error) {
	if err := w.ready(); err != nil {
		return nil, err
	}
	sc, err := w.analyzeRepair()
	if err != nil {
		return nil, err
	}
	return w.reportFor(sc, false), nil
}

func (w *WAL) reportFor(sc walRepairScan, repaired bool) *WALRepairReport {
	return &WALRepairReport{
		Repaired:             repaired,
		StartLBA:             sc.startLBA,
		DiscardedBlocks:      0,
		DownstreamCheckpoint: sc.downstreamCheck,
		DownstreamCheckLBA:   sc.downstreamCheckLBA,
		Next:                 sc.startLBA,
		HasCheck:             sc.prefixHasCheck,
		CheckLBA:             sc.prefixCheckLBA,
	}
}

func (w *WAL) repairMedianRupture() (*WALRepairReport, error) {
	if err := w.ready(); err != nil {
		return nil, err
	}
	sc, err := w.analyzeRepair()
	if err != nil {
		return nil, err
	}
	// C8 : seul un trou médian avéré (bloc scellé valide en aval) déclenche la
	// réparation. Un journal intact ou une queue tronquée normale est rendu tel
	// quel ; une erreur d'E/S a déjà été propagée par analyzeRepair.
	if !sc.downstream {
		return w.reportFor(sc, false), nil
	}
	oldTip := sc.oldTip
	if oldTip <= sc.startLBA {
		oldTip = sc.startLBA + 1
	}
	// C2 / C3(a) : consigner l'intention et sceller la quarantaine AVANT toute
	// écriture destructive du journal.
	qPath, sum, err := w.writeQuarantine(sc.startLBA, oldTip)
	if err != nil {
		return nil, err
	}
	rep := w.reportFor(sc, true)
	rep.DiscardedBlocks = oldTip - sc.startLBA
	rep.QuarantinePath = qPath
	rep.QuarantineSHA256 = sum
	probeEmit(0, ProbeOpWALRepair, sc.startLBA, 0, 0, 0,
		fmt.Sprintf("wal repair: startLBA=%d discarded=%d downstreamCheckLBA=%d quarantine=%s sha256=%x",
			sc.startLBA, rep.DiscardedBlocks, sc.downstreamCheckLBA, qPath, sum))
	// C3(b) : remettre à zéro le suffixe écarté. L'écriture est monotone : une
	// réparation interrompue laisse le premier bloc invalide au même startLBA,
	// sans fabriquer un nouveau trou.
	clear(w.block)
	for lba := sc.startLBA; lba < oldTip; lba++ {
		if err := w.dev.Write(lba, w.block); err != nil {
			return nil, err
		}
	}
	// C3(c) : rendre la coupe durable avant de toucher à l'état en mémoire.
	if err := w.dev.Flush(); err != nil {
		return nil, err
	}
	// C3(d) / C5 : recalculer l'état en mémoire sur le préfixe.
	w.next = sc.startLBA
	w.lsn = w.next
	w.qLBA = w.next
	w.qCount = 0
	w.pending = w.pending[:0]
	w.pendingN = 0
	w.hasCheck = sc.prefixHasCheck
	w.checkLBA = sc.prefixCheckLBA
	rep.Next = w.next
	rep.HasCheck = w.hasCheck
	rep.CheckLBA = w.checkLBA
	return rep, nil
}

// writeQuarantine copie les blocs [startLBA, oldTip) sous un chemin dédié
// <wal>.quarantine-<horodatage>, calcule leur SHA-256 et fsync le fichier. Un
// chemin déjà occupé est évité par suffixe numérique, jamais écrasé.
func (w *WAL) writeQuarantine(startLBA, oldTip uint64) (string, [32]byte, error) {
	var sum [32]byte
	base := fmt.Sprintf("%s.quarantine-%s", w.path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	qPath := base
	for n := 0; ; n++ {
		f, err := os.OpenFile(qPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			h := sha256.New()
			for lba := startLBA; lba < oldTip; lba++ {
				if rerr := w.dev.Read(lba, w.block); rerr != nil {
					_ = f.Truncate(0)
					_ = f.Close()
					return "", sum, rerr
				}
				if _, werr := f.Write(w.block); werr != nil {
					_ = f.Truncate(0)
					_ = f.Close()
					return "", sum, werr
				}
				_, _ = h.Write(w.block)
			}
			if serr := f.Sync(); serr != nil {
				_ = f.Close()
				return "", sum, serr
			}
			if cerr := f.Close(); cerr != nil {
				return "", sum, cerr
			}
			copy(sum[:], h.Sum(nil))
			return qPath, sum, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", sum, err
		}
		qPath = fmt.Sprintf("%s.%d", base, n+1)
	}
}

func sealWAL(block []byte, key [32]byte) {
	canonicalMsg := block[walLenOff:walTagOff]
	derivedKey := DeriveWALSealKey(key, canonicalMsg)
	c2poly1305.Crypto_poly1305(block[walTagOff:LBASize], canonicalMsg, uint64(len(canonicalMsg)), derivedKey[:])
}

func verifyWAL(block []byte, key [32]byte) bool {
	canonicalMsg := block[walLenOff:walTagOff]
	derivedKey := DeriveWALSealKey(key, canonicalMsg)
	var tag [walTagSize]byte
	c2poly1305.Crypto_poly1305(tag[:], canonicalMsg, uint64(len(canonicalMsg)), derivedKey[:])
	return subtle.ConstantTimeCompare(tag[:], block[walTagOff:LBASize]) == 1
}
