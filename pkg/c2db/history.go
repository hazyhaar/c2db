// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Segment d'historique immuable par shard.
//
// L'historique est un journal append-only de disposition binaire FIXE, distinct
// du WAL et du txnlog. Il reçoit, lors d'une compaction sous le contrat
// RetentionArchiveSuperseded, les versions supplantées déportées hors du tas
// chaud : chaque enregistrement porte la clé, l'identifiant de version (16
// octets) et la valeur utilisateur décodée, plus un drapeau de pierre tombale.
// Le déport rend licite l'aplatissement du tas chaud sans perte de version :
// une lecture as-of retombe sur ce segment quand aucune version chaude ne
// satisfait l'instantané.
//
// La disposition est alignée sur celle de txnlog.go : en-tête à décalages
// scalaires fixes, clé et valeur en blocs d'octets OPAQUES, CRC32-C couvrant
// l'en-tête hors champ CRC puis les deux blocs. Le segment est réputé immuable
// une fois écrit : les seules opérations sont l'ajout en fin et la relecture.

const (
	// HistoryMagic identifie un enregistrement d'historique sur le disque.
	HistoryMagic = uint32(0x48535452) // "HSTR"

	// historyVersionSchema est la révision de disposition binaire de
	// l'enregistrement.
	historyVersionSchema = uint16(1)

	historyRecordHeaderSize = 48
	historyMaxKey           = 65536
	historyMaxValue         = 1 << 24

	// historyFlagTombstone marque une version qui supprime la clé : sa valeur
	// est vide et une résolution as-of qui tombe sur elle doit rendre absente
	// la clé, sans retomber sur une version antérieure.
	historyFlagTombstone byte = 1 << 0

	// DefaultHistorySegmentBytes plafonne un segment quand l'appelant n'impose
	// aucune borne.
	DefaultHistorySegmentBytes = int64(64 << 20)
)

// Décalages et tailles des champs de l'en-tête. Disposition fixe, versionnée.
const (
	hOffMagic     = 0
	hOffSchema    = 4
	hOffFlags     = 6
	hOffReserved  = 7
	hOffSeq       = 8
	hOffVersion   = 16
	hOffKeyLen    = 32
	hOffValueLen  = 36
	hOffReserved2 = 40
	hOffCRC       = 44

	hSzMagic     = 4
	hSzSchema    = 2
	hSzFlags     = 1
	hSzReserved  = 1
	hSzSeq       = 8
	hSzVersion   = 16
	hSzKeyLen    = 4
	hSzValueLen  = 4
	hSzReserved2 = 4
	hSzCRC       = 4
)

const (
	historySegPrefix = "history-"
	historySegSuffix = ".seg"
)

var historyCastagnoli = crc32.MakeTable(crc32.Castagnoli)

var (
	errHistoryShort  = errors.New("c2db: history record truncated")
	errHistoryMagic  = errors.New("c2db: history bad magic")
	errHistorySchema = errors.New("c2db: history unsupported schema revision")
	errHistoryRange  = errors.New("c2db: history field out of range")
	errHistoryCRC    = errors.New("c2db: history crc mismatch")
	errHistoryClosed = errors.New("c2db: history closed")

	// errHistoryUnavailable signale une compaction ArchiveSuperseded sans
	// segment d'historique ouvert : le déport est impossible, la compaction est
	// refusée plutôt que de perdre silencieusement des versions.
	errHistoryUnavailable = errors.New("c2db: retention ArchiveSuperseded sans segment d'historique")
)

// HistoryRecord est la forme logique d'une version déportée. Tombstone vaut
// vrai pour une version de suppression : Value est alors vide.
type HistoryRecord struct {
	Key       []byte
	VersionID [16]byte
	Value     []byte
	Tombstone bool
}

// EncodeHistoryRecord sérialise un enregistrement en disposition fixe. Le
// CRC32-C couvre l'en-tête hors champ CRC, puis la clé et la valeur.
func EncodeHistoryRecord(rec HistoryRecord) ([]byte, error) {
	if len(rec.Key) == 0 {
		return nil, fmt.Errorf("%w: clé vide", errHistoryRange)
	}
	if len(rec.Key) > historyMaxKey {
		return nil, fmt.Errorf("%w: key len %d > %d", errHistoryRange, len(rec.Key), historyMaxKey)
	}
	if len(rec.Value) > historyMaxValue {
		return nil, fmt.Errorf("%w: value len %d > %d", errHistoryRange, len(rec.Value), historyMaxValue)
	}
	if rec.Tombstone && len(rec.Value) != 0 {
		return nil, fmt.Errorf("%w: pierre tombale porteuse de valeur", errHistoryRange)
	}

	hdr := make([]byte, historyRecordHeaderSize)
	binary.LittleEndian.PutUint32(hdr[hOffMagic:], HistoryMagic)
	binary.LittleEndian.PutUint16(hdr[hOffSchema:], historyVersionSchema)
	if rec.Tombstone {
		hdr[hOffFlags] = historyFlagTombstone
	}
	copy(hdr[hOffVersion:hOffVersion+hSzVersion], rec.VersionID[:])
	binary.LittleEndian.PutUint32(hdr[hOffKeyLen:], uint32(len(rec.Key)))
	binary.LittleEndian.PutUint32(hdr[hOffValueLen:], uint32(len(rec.Value)))

	crc := crc32.Checksum(hdr[:hOffCRC], historyCastagnoli)
	crc = crc32.Update(crc, historyCastagnoli, rec.Key)
	crc = crc32.Update(crc, historyCastagnoli, rec.Value)
	binary.LittleEndian.PutUint32(hdr[hOffCRC:], crc)

	out := make([]byte, 0, historyRecordHeaderSize+len(rec.Key)+len(rec.Value))
	out = append(out, hdr...)
	out = append(out, rec.Key...)
	out = append(out, rec.Value...)
	return out, nil
}

// DecodeHistoryRecord lit un enregistrement et retourne le nombre d'octets
// consommés, ce qui permet de parcourir un segment entier.
func DecodeHistoryRecord(buf []byte) (HistoryRecord, int, error) {
	var rec HistoryRecord
	if len(buf) < historyRecordHeaderSize {
		return rec, 0, errHistoryShort
	}
	if binary.LittleEndian.Uint32(buf[hOffMagic:]) != HistoryMagic {
		return rec, 0, errHistoryMagic
	}
	if binary.LittleEndian.Uint16(buf[hOffSchema:]) != historyVersionSchema {
		return rec, 0, errHistorySchema
	}
	rec.Tombstone = buf[hOffFlags]&historyFlagTombstone != 0
	copy(rec.VersionID[:], buf[hOffVersion:hOffVersion+hSzVersion])
	keyLen := int(binary.LittleEndian.Uint32(buf[hOffKeyLen:]))
	valueLen := int(binary.LittleEndian.Uint32(buf[hOffValueLen:]))
	if keyLen <= 0 || valueLen < 0 || keyLen > historyMaxKey || valueLen > historyMaxValue {
		return rec, 0, errHistoryRange
	}
	total := historyRecordHeaderSize + keyLen + valueLen
	if len(buf) < total {
		return rec, 0, errHistoryShort
	}
	key := buf[historyRecordHeaderSize : historyRecordHeaderSize+keyLen]
	value := buf[historyRecordHeaderSize+keyLen : total]

	crc := crc32.Checksum(buf[:hOffCRC], historyCastagnoli)
	crc = crc32.Update(crc, historyCastagnoli, key)
	crc = crc32.Update(crc, historyCastagnoli, value)
	if crc != binary.LittleEndian.Uint32(buf[hOffCRC:]) {
		return rec, 0, errHistoryCRC
	}

	rec.Key = append([]byte(nil), key...)
	rec.Value = append([]byte(nil), value...)
	return rec, total, nil
}

// historyLoc est la localisation froide indexée d'une version déportée :
// segment, décalage et longueur encodée. La version et le drapeau de pierre
// tombale y sont dupliqués pour résoudre as-of sans lire la charge utile.
type historyLoc struct {
	seg     uint32
	off     int64
	length  int32
	version [16]byte
	tomb    bool
}

// History est un écrivain append-only borné de segments d'historique. Chaque
// segment est un fichier history-NNNNNNNN.seg ; un nouvel enregistrement qui
// ferait dépasser le plafond déclenche la rotation. Un enregistrement isolé
// plus gros que le plafond est écrit seul dans un segment.
//
// Un index clé -> liste de localisations est tenu à jour à l'ajout et bâti
// une seule fois à l'ouverture par une passe sur les segments existants. Une
// résolution froide lit alors uniquement les enregistrements candidats de la
// clé, au lieu de relire chaque lecture l'intégralité du répertoire.
type History struct {
	dir             string
	maxSegmentBytes int64

	f          *os.File
	segIndex   uint32
	segCount   int
	curBytes   int64
	totalBytes int64
	seq        uint64
	closed     bool

	idxMu     sync.RWMutex
	index     map[string][]historyLoc
	readBytes atomic.Int64
}

// OpenHistory ouvre ou crée un historique dans dir. La reprise réutilise le
// segment de plus fort indice. Un maxSegmentBytes nul ou négatif applique
// DefaultHistorySegmentBytes.
func OpenHistory(dir string, maxSegmentBytes int64) (*History, error) {
	if maxSegmentBytes <= 0 {
		maxSegmentBytes = DefaultHistorySegmentBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	h := &History{dir: dir, maxSegmentBytes: maxSegmentBytes}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	highest := uint32(0)
	has := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		idx, ok := parseHistorySegmentName(e.Name())
		if !ok {
			continue
		}
		h.segCount++
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		h.totalBytes += fi.Size()
		if !has || idx >= highest {
			highest = idx
			has = true
		}
	}
	if has {
		path := filepath.Join(dir, historySegmentName(highest))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		fi, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		h.f = f
		h.segIndex = highest
		h.curBytes = fi.Size()
	}
	if err := h.buildIndex(); err != nil {
		if h.f != nil {
			_ = h.f.Close()
			h.f = nil
		}
		return nil, err
	}
	return h, nil
}

// Append encode et ajoute un enregistrement en fin de segment courant.
func (h *History) Append(rec HistoryRecord) error {
	if h.closed {
		return errHistoryClosed
	}
	buf, err := EncodeHistoryRecord(rec)
	if err != nil {
		return err
	}
	need := int64(len(buf))
	if h.f != nil && h.curBytes > 0 && h.curBytes+need > h.maxSegmentBytes {
		if err := h.rotate(); err != nil {
			return err
		}
	}
	if h.f == nil {
		if err := h.openSegment(); err != nil {
			return err
		}
	}
	off := h.curBytes
	n, err := h.f.Write(buf)
	if err != nil {
		return err
	}
	h.curBytes += int64(n)
	h.totalBytes += int64(n)
	h.seq++
	if n == len(buf) {
		h.indexAppend(string(rec.Key), historyLoc{
			seg:     h.segIndex,
			off:     off,
			length:  int32(n),
			version: rec.VersionID,
			tomb:    rec.Tombstone,
		})
	}
	return nil
}

// buildIndex parcourt une fois les segments existants et construit la table
// clé -> localisations. C'est le seul moment où l'historique froid est lu
// intégralement ; toutes les résolutions ultérieures passent par l'index.
func (h *History) buildIndex() error {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return err
	}
	type seg struct {
		idx  uint32
		path string
	}
	segs := make([]seg, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		idx, ok := parseHistorySegmentName(e.Name())
		if !ok {
			continue
		}
		segs = append(segs, seg{idx: idx, path: filepath.Join(h.dir, e.Name())})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].idx < segs[j].idx })
	index := make(map[string][]historyLoc)
	var count uint64
	for _, s := range segs {
		data, err := os.ReadFile(s.path)
		if err != nil {
			return err
		}
		off := 0
		for off < len(data) {
			rec, n, err := DecodeHistoryRecord(data[off:])
			if err != nil {
				// Une queue partielle ne peut provenir que du segment courant,
				// interrompu par une panne en pleine écriture. Les segments
				// antérieurs sont scellés par la rotation : leur corruption est
				// franche et refusée. La queue est tronquée pour rétablir une
				// frontière d'enregistrement saine avant toute réouverture.
				if s.idx != h.segIndex {
					return err
				}
				if truncErr := os.Truncate(s.path, int64(off)); truncErr != nil {
					return truncErr
				}
				h.totalBytes -= int64(len(data) - off)
				h.curBytes = int64(off)
				break
			}
			index[string(rec.Key)] = append(index[string(rec.Key)], historyLoc{
				seg:     s.idx,
				off:     int64(off),
				length:  int32(n),
				version: rec.VersionID,
				tomb:    rec.Tombstone,
			})
			off += n
			count++
		}
	}
	h.idxMu.Lock()
	h.index = index
	h.idxMu.Unlock()
	h.seq = count
	return nil
}

func (h *History) indexAppend(key string, loc historyLoc) {
	h.idxMu.Lock()
	if h.index == nil {
		h.index = make(map[string][]historyLoc)
	}
	h.index[key] = append(h.index[key], loc)
	h.idxMu.Unlock()
}

// Resolve retourne, parmi les versions déportées de la clé dont l'identifiant
// est inférieur ou égal à snap, celle de plus grand identifiant. La lecture
// est bornée aux enregistrements candidats indexés : l'intégralité du
// répertoire n'est jamais relue. L'enregistrement retourné peut être une
// pierre tombale ; l'appelant conclut alors à l'absence de la clé.
func (h *History) Resolve(key []byte, snap [16]byte) (HistoryRecord, bool, error) {
	if h.closed {
		return HistoryRecord{}, false, errHistoryClosed
	}
	h.idxMu.RLock()
	locs := h.index[string(key)]
	var best historyLoc
	have := false
	for i := range locs {
		loc := locs[i]
		if bytes.Compare(loc.version[:], snap[:]) > 0 {
			continue
		}
		if !have || bytes.Compare(loc.version[:], best.version[:]) > 0 {
			best = loc
			have = true
		}
	}
	h.idxMu.RUnlock()
	if !have {
		return HistoryRecord{}, false, nil
	}
	rec, err := h.readLoc(best)
	if err != nil {
		return HistoryRecord{}, false, err
	}
	return rec, true, nil
}

// readLoc lit le seul enregistrement désigné par l'index et vérifie son CRC.
func (h *History) readLoc(loc historyLoc) (HistoryRecord, error) {
	path := filepath.Join(h.dir, historySegmentName(loc.seg))
	f, err := os.Open(path)
	if err != nil {
		return HistoryRecord{}, err
	}
	defer f.Close()
	buf := make([]byte, loc.length)
	if _, err := f.ReadAt(buf, loc.off); err != nil {
		return HistoryRecord{}, err
	}
	rec, n, err := DecodeHistoryRecord(buf)
	if err != nil {
		return HistoryRecord{}, err
	}
	if n != int(loc.length) {
		return HistoryRecord{}, errHistoryRange
	}
	h.readBytes.Add(int64(loc.length))
	return rec, nil
}

// HasTombstone indique si une pierre tombale de la clé porte déjà cet
// identifiant de version. Le déport de suppression étant rejouable, cette
// garde rend l'ajout idempotent.
func (h *History) HasTombstone(key []byte, id [16]byte) bool {
	h.idxMu.RLock()
	defer h.idxMu.RUnlock()
	for _, loc := range h.index[string(key)] {
		if loc.tomb && loc.version == id {
			return true
		}
	}
	return false
}

// ReadBytes retourne le volume d'octets effectivement lu par les résolutions
// indexées depuis la dernière remise à zéro. Sert à mesurer le coût froid.
func (h *History) ReadBytes() int64 { return h.readBytes.Load() }

// ResetReadBytes remet à zéro le compteur de lecture des résolutions.
func (h *History) ResetReadBytes() { h.readBytes.Store(0) }

// Sync force le vidage du segment courant.
func (h *History) Sync() error {
	if h.f == nil {
		return nil
	}
	return h.f.Sync()
}

// Close vide et ferme le segment courant.
func (h *History) Close() error {
	if h.closed {
		return nil
	}
	h.closed = true
	if h.f == nil {
		return nil
	}
	if err := h.f.Sync(); err != nil {
		_ = h.f.Close()
		return err
	}
	err := h.f.Close()
	h.f = nil
	return err
}

// Dir retourne le répertoire des segments.
func (h *History) Dir() string { return h.dir }

// Segments retourne le nombre de segments ouverts depuis l'ouverture.
func (h *History) Segments() int { return h.segCount }

// BytesWritten retourne le volume total écrit, segments existants inclus.
func (h *History) BytesWritten() int64 { return h.totalBytes }

func (h *History) openSegment() error {
	path := filepath.Join(h.dir, historySegmentName(h.segIndex))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	h.f = f
	h.curBytes = fi.Size()
	h.segCount++
	return nil
}

func (h *History) rotate() error {
	if h.f != nil {
		if err := h.f.Sync(); err != nil {
			_ = h.f.Close()
			return err
		}
		if err := h.f.Close(); err != nil {
			return err
		}
		h.f = nil
	}
	h.segIndex++
	return nil
}

func historySegmentName(idx uint32) string {
	return historySegPrefix + fmt.Sprintf("%08d", idx) + historySegSuffix
}

func parseHistorySegmentName(name string) (uint32, bool) {
	if !strings.HasPrefix(name, historySegPrefix) || !strings.HasSuffix(name, historySegSuffix) {
		return 0, false
	}
	mid := name[len(historySegPrefix) : len(name)-len(historySegSuffix)]
	if mid == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(mid, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// ReadHistorySegment décode tous les enregistrements d'un fichier de segment.
func ReadHistorySegment(path string) ([]HistoryRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryRecord, 0, 8)
	off := 0
	for off < len(data) {
		rec, n, err := DecodeHistoryRecord(data[off:])
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
		off += n
	}
	return out, nil
}

// ReadHistoryDir décode tous les segments d'un historique, dans l'ordre
// croissant de leur indice.
func ReadHistoryDir(dir string) ([]HistoryRecord, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type seg struct {
		idx  uint32
		path string
	}
	segs := make([]seg, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		idx, ok := parseHistorySegmentName(e.Name())
		if !ok {
			continue
		}
		segs = append(segs, seg{idx: idx, path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].idx < segs[j].idx })
	out := make([]HistoryRecord, 0, 8)
	for _, s := range segs {
		recs, err := ReadHistorySegment(s.path)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

// ResolveHistoryDir résout une clé à un instantané donné : parmi les versions
// déportées de cette clé dont l'identifiant est inférieur ou égal à snap, la
// plus grande l'emporte. L'enregistrement retourné peut être une pierre
// tombale ; l'appelant interprète alors la clé comme absente à cet instantané.
// La relecture est faite depuis le disque : le chemin est froid et
// nécessairement postérieur à une compaction.
func ResolveHistoryDir(dir string, key []byte, snap [16]byte) (HistoryRecord, bool, error) {
	var best HistoryRecord
	var have bool
	recs, err := ReadHistoryDir(dir)
	if err != nil {
		return HistoryRecord{}, false, err
	}
	for i := range recs {
		rec := recs[i]
		if !bytes.Equal(rec.Key, key) {
			continue
		}
		if bytes.Compare(rec.VersionID[:], snap[:]) > 0 {
			continue
		}
		if !have || bytes.Compare(rec.VersionID[:], best.VersionID[:]) > 0 {
			best = rec
			have = true
		}
	}
	return best, have, nil
}
