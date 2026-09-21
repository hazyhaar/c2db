// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Journal d'événements de transaction (txnlog).
//
// Le txnlog est un journal append-only de disposition binaire FIXE, distinct du
// WAL : il consigne les mutations, compactions, élagages, archivages et refus
// sans être tronqué par la reprise du WAL. L'en-tête de 64 octets porte des
// scalaires positionnés par décalage ; la clé et la charge utile sont des
// blocs d'octets OPAQUES, jamais interprétés par ce codec. Le schéma de
// référence vit dans spec/txn_event.cue et doit coïncider avec les constantes
// ci-dessous, ce qu'un test de parité vérifie.

const (
	// TxLogMagic identifie un enregistrement de txnlog sur le disque.
	TxLogMagic = uint32(0x434C4F47)

	// versionSchema est la révision de disposition binaire de l'enregistrement.
	versionSchema = uint16(1)

	txnEventHeaderSize = 64
	txnEventMaxKey     = 65536
	txnEventMaxPayload = 1 << 24

	// DefaultTxLogSegmentBytes plafonne un segment quand l'appelant n'impose
	// aucune borne.
	DefaultTxLogSegmentBytes = int64(64 << 20)
)

// Décalages et tailles des champs de l'en-tête. Ils sont la contrepartie
// mécanique de spec/txn_event.cue.
const (
	offMagic      = 0
	offSchema     = 4
	offKind       = 6
	offFlags      = 7
	offShard      = 8
	offReserved   = 10
	offSeq        = 12
	offTimestamp  = 20
	offVersion    = 28
	offWatermark  = 44
	offKeyLen     = 52
	offPayloadLen = 56
	offCRC        = 60

	szMagic      = 4
	szSchema     = 2
	szKind       = 1
	szFlags      = 1
	szShard      = 2
	szReserved   = 2
	szSeq        = 8
	szTimestamp  = 8
	szVersion    = 16
	szWatermark  = 8
	szKeyLen     = 4
	szPayloadLen = 4
	szCRC        = 4
)

const (
	txLogSegPrefix = "txnlog-"
	txLogSegSuffix = ".seg"
)

// EventKind est le discriminant de genre d'un événement de transaction.
type EventKind uint8

const (
	// EventMutation consigne une écriture ou suppression validée.
	EventMutation EventKind = 1
	// EventCompaction consigne une compaction de pages ou de versions.
	EventCompaction EventKind = 2
	// EventPrune consigne un élagage de versions mortes.
	EventPrune EventKind = 3
	// EventArchive consigne un déport vers une archive scellée.
	EventArchive EventKind = 4
	// EventRefusal consigne un refus d'admission ou de transaction.
	EventRefusal EventKind = 5
)

// String rend le nom canonique du genre.
func (k EventKind) String() string {
	switch k {
	case EventMutation:
		return "mutation"
	case EventCompaction:
		return "compaction"
	case EventPrune:
		return "prune"
	case EventArchive:
		return "archive"
	case EventRefusal:
		return "refusal"
	default:
		return "invalid"
	}
}

// Valid indique si le genre appartient à l'énumération close.
func (k EventKind) Valid() bool {
	return k >= EventMutation && k <= EventRefusal
}

var txLogCastagnoli = crc32.MakeTable(crc32.Castagnoli)

var (
	errTxLogShort  = errors.New("c2db: txnlog record truncated")
	errTxLogMagic  = errors.New("c2db: txnlog bad magic")
	errTxLogSchema = errors.New("c2db: txnlog unsupported schema revision")
	errTxLogKind   = errors.New("c2db: txnlog invalid event kind")
	errTxLogRange  = errors.New("c2db: txnlog field out of range")
	errTxLogCRC    = errors.New("c2db: txnlog crc mismatch")
	errTxLogClosed = errors.New("c2db: txnlog closed")
)

// TxnEvent est la forme logique d'un enregistrement du journal. Les noms des
// champs exportés coïncident avec ceux déclarés par spec/txn_event.cue.
type TxnEvent struct {
	Kind      EventKind `json:"kind"`
	Shard     uint16    `json:"shard"`
	Seq       uint64    `json:"seq"`
	Timestamp uint64    `json:"timestamp"`
	VersionID [16]byte  `json:"version_id"`
	Watermark uint64    `json:"watermark"`
	Key       []byte    `json:"key"`
	Payload   []byte    `json:"payload"`
}

// EventSink est le contrat d'émission que le moteur peut appeler. Il reste
// volontairement synchrone et sans contexte : l'appelant décide de la
// cadence de Sync.
type EventSink interface {
	Emit(ev TxnEvent) error
	Sync() error
	Close() error
}

// EncodeTxnEvent sérialise un événement en disposition fixe. Le CRC32-C couvre
// l'en-tête hors champ CRC, puis la clé et la charge utile brutes.
func EncodeTxnEvent(ev TxnEvent) ([]byte, error) {
	if !ev.Kind.Valid() {
		return nil, errTxLogKind
	}
	if ev.Shard >= NumShards {
		return nil, fmt.Errorf("%w: shard %d >= %d", errTxLogRange, ev.Shard, NumShards)
	}
	if len(ev.Key) > txnEventMaxKey {
		return nil, fmt.Errorf("%w: key len %d > %d", errTxLogRange, len(ev.Key), txnEventMaxKey)
	}
	if len(ev.Payload) > txnEventMaxPayload {
		return nil, fmt.Errorf("%w: payload len %d > %d", errTxLogRange, len(ev.Payload), txnEventMaxPayload)
	}

	hdr := make([]byte, txnEventHeaderSize)
	binary.LittleEndian.PutUint32(hdr[offMagic:], TxLogMagic)
	binary.LittleEndian.PutUint16(hdr[offSchema:], versionSchema)
	hdr[offKind] = byte(ev.Kind)
	hdr[offFlags] = 0
	binary.LittleEndian.PutUint16(hdr[offShard:], ev.Shard)
	binary.LittleEndian.PutUint16(hdr[offReserved:], 0)
	binary.LittleEndian.PutUint64(hdr[offSeq:], ev.Seq)
	binary.LittleEndian.PutUint64(hdr[offTimestamp:], ev.Timestamp)
	copy(hdr[offVersion:offVersion+szVersion], ev.VersionID[:])
	binary.LittleEndian.PutUint64(hdr[offWatermark:], ev.Watermark)
	binary.LittleEndian.PutUint32(hdr[offKeyLen:], uint32(len(ev.Key)))
	binary.LittleEndian.PutUint32(hdr[offPayloadLen:], uint32(len(ev.Payload)))

	crc := crc32.Checksum(hdr[:offCRC], txLogCastagnoli)
	crc = crc32.Update(crc, txLogCastagnoli, ev.Key)
	crc = crc32.Update(crc, txLogCastagnoli, ev.Payload)
	binary.LittleEndian.PutUint32(hdr[offCRC:], crc)

	out := make([]byte, 0, txnEventHeaderSize+len(ev.Key)+len(ev.Payload))
	out = append(out, hdr...)
	out = append(out, ev.Key...)
	out = append(out, ev.Payload...)
	return out, nil
}

// DecodeTxnEvent lit un enregistrement et retourne l'événement ainsi que le
// nombre d'octets consommés, ce qui permet de parcourir un segment entier.
func DecodeTxnEvent(buf []byte) (TxnEvent, int, error) {
	var ev TxnEvent
	if len(buf) < txnEventHeaderSize {
		return ev, 0, errTxLogShort
	}
	if binary.LittleEndian.Uint32(buf[offMagic:]) != TxLogMagic {
		return ev, 0, errTxLogMagic
	}
	if binary.LittleEndian.Uint16(buf[offSchema:]) != versionSchema {
		return ev, 0, errTxLogSchema
	}
	ev.Kind = EventKind(buf[offKind])
	if !ev.Kind.Valid() {
		return ev, 0, errTxLogKind
	}
	ev.Shard = binary.LittleEndian.Uint16(buf[offShard:])
	ev.Seq = binary.LittleEndian.Uint64(buf[offSeq:])
	ev.Timestamp = binary.LittleEndian.Uint64(buf[offTimestamp:])
	copy(ev.VersionID[:], buf[offVersion:offVersion+szVersion])
	ev.Watermark = binary.LittleEndian.Uint64(buf[offWatermark:])
	keyLen := int(binary.LittleEndian.Uint32(buf[offKeyLen:]))
	payloadLen := int(binary.LittleEndian.Uint32(buf[offPayloadLen:]))
	if keyLen < 0 || payloadLen < 0 || keyLen > txnEventMaxKey || payloadLen > txnEventMaxPayload {
		return ev, 0, errTxLogRange
	}
	total := txnEventHeaderSize + keyLen + payloadLen
	if len(buf) < total {
		return ev, 0, errTxLogShort
	}
	key := buf[txnEventHeaderSize : txnEventHeaderSize+keyLen]
	payload := buf[txnEventHeaderSize+keyLen : total]

	crc := crc32.Checksum(buf[:offCRC], txLogCastagnoli)
	crc = crc32.Update(crc, txLogCastagnoli, key)
	crc = crc32.Update(crc, txLogCastagnoli, payload)
	if crc != binary.LittleEndian.Uint32(buf[offCRC:]) {
		return ev, 0, errTxLogCRC
	}

	ev.Key = append([]byte(nil), key...)
	ev.Payload = append([]byte(nil), payload...)
	return ev, total, nil
}

// TxLog est un écrivain append-only borné. Chaque segment est un fichier
// txnlog-NNNNNNNN.seg ; un nouvel enregistrement qui ferait dépasser le
// plafond de segment déclenche la rotation. Un enregistrement isolé plus gros
// que le plafond est écrit seul dans un segment, faute de pouvoir le découper.
type TxLog struct {
	dir             string
	maxSegmentBytes int64

	f          *os.File
	segIndex   uint32
	segCount   int
	curBytes   int64
	totalBytes int64
	seq        uint64
	closed     bool
}

// OpenTxLog ouvre ou crée un journal dans dir. La reprise réutilise le segment
// de plus fort indice. Un maxSegmentBytes nul ou négatif applique
// DefaultTxLogSegmentBytes.
func OpenTxLog(dir string, maxSegmentBytes int64) (*TxLog, error) {
	if maxSegmentBytes <= 0 {
		maxSegmentBytes = DefaultTxLogSegmentBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l := &TxLog{dir: dir, maxSegmentBytes: maxSegmentBytes}
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
		idx, ok := parseTxLogSegmentName(e.Name())
		if !ok {
			continue
		}
		l.segCount++
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		l.totalBytes += fi.Size()
		if !has || idx >= highest {
			highest = idx
			has = true
		}
	}
	if has {
		path := filepath.Join(dir, txLogSegmentName(highest))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		fi, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		l.f = f
		l.segIndex = highest
		l.curBytes = fi.Size()
		if evs, err := ReadTxLogSegment(path); err == nil && len(evs) > 0 {
			l.seq = evs[len(evs)-1].Seq
		}
	}
	return l, nil
}

// Emit encode et ajoute un événement. Un Seq nul est remplacé par le
// successeur monotone local ; un Seq non nul supérieur met à jour le
// compteur, ce qui permet la reprise.
func (l *TxLog) Emit(ev TxnEvent) error {
	if l.closed {
		return errTxLogClosed
	}
	if ev.Seq == 0 {
		l.seq++
		ev.Seq = l.seq
	} else if ev.Seq > l.seq {
		l.seq = ev.Seq
	}
	rec, err := EncodeTxnEvent(ev)
	if err != nil {
		return err
	}
	need := int64(len(rec))
	if l.f != nil && l.curBytes > 0 && l.curBytes+need > l.maxSegmentBytes {
		if err := l.rotate(); err != nil {
			return err
		}
	}
	if l.f == nil {
		if err := l.openSegment(); err != nil {
			return err
		}
	}
	n, err := l.f.Write(rec)
	if err != nil {
		return err
	}
	l.curBytes += int64(n)
	l.totalBytes += int64(n)
	return nil
}

// Sync force le vidage du segment courant.
func (l *TxLog) Sync() error {
	if l.f == nil {
		return nil
	}
	return l.f.Sync()
}

// Close vide et ferme le segment courant.
func (l *TxLog) Close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	if l.f == nil {
		return nil
	}
	if err := l.f.Sync(); err != nil {
		_ = l.f.Close()
		return err
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Segments retourne le nombre de segments ouverts depuis l'ouverture.
func (l *TxLog) Segments() int { return l.segCount }

// BytesWritten retourne le volume total écrit, segments existants inclus.
func (l *TxLog) BytesWritten() int64 { return l.totalBytes }

func (l *TxLog) openSegment() error {
	path := filepath.Join(l.dir, txLogSegmentName(l.segIndex))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.f = f
	l.curBytes = fi.Size()
	l.segCount++
	return nil
}

func (l *TxLog) rotate() error {
	if l.f != nil {
		if err := l.f.Sync(); err != nil {
			_ = l.f.Close()
			return err
		}
		if err := l.f.Close(); err != nil {
			return err
		}
		l.f = nil
	}
	l.segIndex++
	return nil
}

func txLogSegmentName(idx uint32) string {
	return txLogSegPrefix + fmt.Sprintf("%08d", idx) + txLogSegSuffix
}

func parseTxLogSegmentName(name string) (uint32, bool) {
	if !strings.HasPrefix(name, txLogSegPrefix) || !strings.HasSuffix(name, txLogSegSuffix) {
		return 0, false
	}
	mid := name[len(txLogSegPrefix) : len(name)-len(txLogSegSuffix)]
	if mid == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(mid, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// ReadTxLogSegment décode tous les enregistrements d'un fichier de segment.
func ReadTxLogSegment(path string) ([]TxnEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]TxnEvent, 0, 8)
	off := 0
	for off < len(data) {
		ev, n, err := DecodeTxnEvent(data[off:])
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
		off += n
	}
	return out, nil
}

// ReadTxLogDir décode tous les segments d'un journal, dans l'ordre croissant
// de leur indice.
func ReadTxLogDir(dir string) ([]TxnEvent, error) {
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
		idx, ok := parseTxLogSegmentName(e.Name())
		if !ok {
			continue
		}
		segs = append(segs, seg{idx: idx, path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].idx < segs[j].idx })
	out := make([]TxnEvent, 0, 8)
	for _, s := range segs {
		evs, err := ReadTxLogSegment(s.path)
		if err != nil {
			return nil, err
		}
		out = append(out, evs...)
	}
	return out, nil
}
