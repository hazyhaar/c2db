// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

type txLayoutSpec struct {
	MagicValue    uint32         `json:"magic_value"`
	VersionSchema uint16         `json:"version_schema"`
	HeaderSize    int            `json:"header_size"`
	MaxKeyLen     int            `json:"max_key_len"`
	MaxPayloadLen int            `json:"max_payload_len"`
	OffMagic      int            `json:"off_magic"`
	OffSchema     int            `json:"off_schema"`
	OffKind       int            `json:"off_kind"`
	OffFlags      int            `json:"off_flags"`
	OffShard      int            `json:"off_shard"`
	OffReserved   int            `json:"off_reserved"`
	OffSeq        int            `json:"off_seq"`
	OffTimestamp  int            `json:"off_timestamp"`
	OffVersion    int            `json:"off_version"`
	OffWatermark  int            `json:"off_watermark"`
	OffKeyLen     int            `json:"off_key_len"`
	OffPayloadLen int            `json:"off_payload_len"`
	OffCRC        int            `json:"off_crc"`
	SzMagic       int            `json:"sz_magic"`
	SzSchema      int            `json:"sz_schema"`
	SzKind        int            `json:"sz_kind"`
	SzFlags       int            `json:"sz_flags"`
	SzShard       int            `json:"sz_shard"`
	SzReserved    int            `json:"sz_reserved"`
	SzSeq         int            `json:"sz_seq"`
	SzTimestamp   int            `json:"sz_timestamp"`
	SzVersion     int            `json:"sz_version"`
	SzWatermark   int            `json:"sz_watermark"`
	SzKeyLen      int            `json:"sz_key_len"`
	SzPayloadLen  int            `json:"sz_payload_len"`
	SzCRC         int            `json:"sz_crc"`
	KindCodes     map[string]int `json:"kind_codes"`
	Fields        []string       `json:"fields"`
}

func TestTxnEventSpecParity(t *testing.T) {
	cueBin, err := exec.LookPath("cue")
	if err != nil {
		t.Skip("binaire cue absent, parité spec/codec non vérifiable")
	}
	if out, err := exec.Command(cueBin, "vet", "./spec/").CombinedOutput(); err != nil {
		t.Fatalf("cue vet ./spec/: %v\n%s", err, out)
	}
	out, err := exec.Command(cueBin, "export", "-e", "txn_event_layout", "./spec/").Output()
	if err != nil {
		t.Fatalf("cue export txn_event_layout: %v", err)
	}
	var lay txLayoutSpec
	if err := json.Unmarshal(out, &lay); err != nil {
		t.Fatalf("décodage layout JSON: %v", err)
	}

	checks := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"magic_value", uint64(lay.MagicValue), uint64(TxLogMagic)},
		{"version_schema", uint64(lay.VersionSchema), uint64(versionSchema)},
		{"header_size", uint64(lay.HeaderSize), uint64(txnEventHeaderSize)},
		{"max_key_len", uint64(lay.MaxKeyLen), uint64(txnEventMaxKey)},
		{"max_payload_len", uint64(lay.MaxPayloadLen), uint64(txnEventMaxPayload)},
		{"off_magic", uint64(lay.OffMagic), uint64(offMagic)},
		{"off_schema", uint64(lay.OffSchema), uint64(offSchema)},
		{"off_kind", uint64(lay.OffKind), uint64(offKind)},
		{"off_flags", uint64(lay.OffFlags), uint64(offFlags)},
		{"off_shard", uint64(lay.OffShard), uint64(offShard)},
		{"off_reserved", uint64(lay.OffReserved), uint64(offReserved)},
		{"off_seq", uint64(lay.OffSeq), uint64(offSeq)},
		{"off_timestamp", uint64(lay.OffTimestamp), uint64(offTimestamp)},
		{"off_version", uint64(lay.OffVersion), uint64(offVersion)},
		{"off_watermark", uint64(lay.OffWatermark), uint64(offWatermark)},
		{"off_key_len", uint64(lay.OffKeyLen), uint64(offKeyLen)},
		{"off_payload_len", uint64(lay.OffPayloadLen), uint64(offPayloadLen)},
		{"off_crc", uint64(lay.OffCRC), uint64(offCRC)},
		{"sz_magic", uint64(lay.SzMagic), uint64(szMagic)},
		{"sz_schema", uint64(lay.SzSchema), uint64(szSchema)},
		{"sz_kind", uint64(lay.SzKind), uint64(szKind)},
		{"sz_flags", uint64(lay.SzFlags), uint64(szFlags)},
		{"sz_shard", uint64(lay.SzShard), uint64(szShard)},
		{"sz_reserved", uint64(lay.SzReserved), uint64(szReserved)},
		{"sz_seq", uint64(lay.SzSeq), uint64(szSeq)},
		{"sz_timestamp", uint64(lay.SzTimestamp), uint64(szTimestamp)},
		{"sz_version", uint64(lay.SzVersion), uint64(szVersion)},
		{"sz_watermark", uint64(lay.SzWatermark), uint64(szWatermark)},
		{"sz_key_len", uint64(lay.SzKeyLen), uint64(szKeyLen)},
		{"sz_payload_len", uint64(lay.SzPayloadLen), uint64(szPayloadLen)},
		{"sz_crc", uint64(lay.SzCRC), uint64(szCRC)},
		{"off_crc+sz_crc", uint64(lay.OffCRC + lay.SzCRC), uint64(txnEventHeaderSize)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("parité %s: spec=%d codec=%d", c.name, c.got, c.want)
		}
	}

	wantCodes := map[string]int{
		"mutation":   int(EventMutation),
		"compaction": int(EventCompaction),
		"prune":      int(EventPrune),
		"archive":    int(EventArchive),
		"refusal":    int(EventRefusal),
	}
	if !reflect.DeepEqual(lay.KindCodes, wantCodes) {
		t.Errorf("codes de genre: spec=%v codec=%v", lay.KindCodes, wantCodes)
	}

	gotFields := map[string]bool{}
	rt := reflect.TypeOf(TxnEvent{})
	for i := 0; i < rt.NumField(); i++ {
		gotFields[rt.Field(i).Tag.Get("json")] = true
	}
	wantFields := map[string]bool{}
	for _, f := range lay.Fields {
		wantFields[f] = true
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Errorf("champs: spec=%v codec=%v", wantFields, gotFields)
	}
}

func sampleTxnEvent(kind EventKind, seq uint64, key, payload []byte) TxnEvent {
	var ver [16]byte
	for i := range ver {
		ver[i] = byte(seq + uint64(i))
	}
	return TxnEvent{
		Kind:      kind,
		Shard:     uint16(seq % NumShards),
		Seq:       seq,
		Timestamp: 1_700_000_000_000 + seq,
		VersionID: ver,
		Watermark: seq * 7,
		Key:       key,
		Payload:   payload,
	}
}

func TestTxnEventEncodeDecodeRoundTrip(t *testing.T) {
	kinds := []EventKind{EventMutation, EventCompaction, EventPrune, EventArchive, EventRefusal}
	for i, k := range kinds {
		ev := sampleTxnEvent(k, uint64(i+1), []byte("shard-key"), []byte{0x00, 0xff, 0x10, 0x20})
		enc, err := EncodeTxnEvent(ev)
		if err != nil {
			t.Fatalf("%s: encode: %v", k, err)
		}
		if len(enc) != txnEventHeaderSize+len(ev.Key)+len(ev.Payload) {
			t.Fatalf("%s: taille=%d", k, len(enc))
		}
		got, n, err := DecodeTxnEvent(enc)
		if err != nil {
			t.Fatalf("%s: decode: %v", k, err)
		}
		if n != len(enc) {
			t.Fatalf("%s: consommé=%d attendu=%d", k, n, len(enc))
		}
		if got.Kind != ev.Kind || got.Shard != ev.Shard || got.Seq != ev.Seq ||
			got.Timestamp != ev.Timestamp || got.VersionID != ev.VersionID ||
			got.Watermark != ev.Watermark {
			t.Fatalf("%s: scalaires altérés: %+v", k, got)
		}
		if !bytes.Equal(got.Key, ev.Key) || !bytes.Equal(got.Payload, ev.Payload) {
			t.Fatalf("%s: charge utile altérée", k)
		}
	}
}

func TestTxnEventDecodeRejectsCorrupt(t *testing.T) {
	ev := sampleTxnEvent(EventMutation, 1, []byte("k"), []byte("payload"))
	enc, err := EncodeTxnEvent(ev)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	truncated := enc[:txnEventHeaderSize-1]
	if _, _, err := DecodeTxnEvent(truncated); !errors.Is(err, errTxLogShort) {
		t.Fatalf("troncature: err=%v, attendu %v", err, errTxLogShort)
	}

	badMagic := append([]byte(nil), enc...)
	badMagic[offMagic] ^= 0xff
	if _, _, err := DecodeTxnEvent(badMagic); !errors.Is(err, errTxLogMagic) {
		t.Fatalf("magic: err=%v, attendu %v", err, errTxLogMagic)
	}

	badCRC := append([]byte(nil), enc...)
	badCRC[len(badCRC)-1] ^= 0x01
	if _, _, err := DecodeTxnEvent(badCRC); !errors.Is(err, errTxLogCRC) {
		t.Fatalf("crc: err=%v, attendu %v", err, errTxLogCRC)
	}

	badKind := append([]byte(nil), enc...)
	badKind[offKind] = 0x00
	// Le CRC couvre le genre modifié par le champ kind : recalculer pour
	// isoler le refus d'énumération.
	fixCRC(badKind)
	if _, _, err := DecodeTxnEvent(badKind); !errors.Is(err, errTxLogKind) {
		t.Fatalf("genre: err=%v, attendu %v", err, errTxLogKind)
	}
}

func fixCRC(rec []byte) {
	keyLen := int(binaryLE32(rec[offKeyLen:]))
	payloadLen := int(binaryLE32(rec[offPayloadLen:]))
	crc := crc32.Checksum(rec[:offCRC], txLogCastagnoli)
	crc = crc32.Update(crc, txLogCastagnoli, rec[txnEventHeaderSize:txnEventHeaderSize+keyLen])
	start := txnEventHeaderSize + keyLen
	crc = crc32.Update(crc, txLogCastagnoli, rec[start:start+payloadLen])
	putLE32(rec[offCRC:], crc)
}

func binaryLE32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func putLE32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func TestTxLogAppendAndBounded(t *testing.T) {
	dir := t.TempDir()
	const maxSeg = int64(512)
	l, err := OpenTxLog(dir, maxSeg)
	if err != nil {
		t.Fatalf("OpenTxLog: %v", err)
	}
	const n = 200
	for i := 0; i < n; i++ {
		ev := sampleTxnEvent(EventMutation, uint64(i+1), []byte("key"), []byte("payload-000000"))
		if err := l.Emit(ev); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if l.Segments() <= 1 {
		t.Fatalf("bornage: %d segment(s), attendu > 1", l.Segments())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	segFiles := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := parseTxLogSegmentName(e.Name()); !ok {
			t.Fatalf("fichier inattendu: %s", e.Name())
		}
		segFiles++
		fi, err := e.Info()
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if fi.Size() > maxSeg {
			t.Fatalf("segment %s non borné: %d > %d", e.Name(), fi.Size(), maxSeg)
		}
	}
	if segFiles != l.Segments() {
		t.Fatalf("segments sur disque=%d, comptés=%d", segFiles, l.Segments())
	}

	evs, err := ReadTxLogDir(dir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	if len(evs) != n {
		t.Fatalf("relus=%d, attendu %d", len(evs), n)
	}
	for i := range evs {
		if evs[i].Seq != uint64(i+1) {
			t.Fatalf("ordre rompu en %d: seq=%d", i, evs[i].Seq)
		}
	}
}

func TestTxLogResumeContinuesSequence(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenTxLog(dir, 1<<20)
	if err != nil {
		t.Fatalf("OpenTxLog: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := l.Emit(sampleTxnEvent(EventMutation, 0, []byte("k"), []byte("p"))); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	l2, err := OpenTxLog(dir, 1<<20)
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := l2.Emit(sampleTxnEvent(EventMutation, 0, []byte("k"), []byte("p"))); err != nil {
			t.Fatalf("Emit après reprise: %v", err)
		}
	}
	if err := l2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	evs, err := ReadTxLogDir(dir)
	if err != nil {
		t.Fatalf("ReadTxLogDir: %v", err)
	}
	if len(evs) != 6 {
		t.Fatalf("relus=%d, attendu 6", len(evs))
	}
	for i := range evs {
		if evs[i].Seq != uint64(i+1) {
			t.Fatalf("séquence après reprise en %d: seq=%d", i, evs[i].Seq)
		}
	}
}

func TestTxLogRejectsBadKindAndRange(t *testing.T) {
	ev := sampleTxnEvent(EventKind(0), 1, []byte("k"), nil)
	if _, err := EncodeTxnEvent(ev); !errors.Is(err, errTxLogKind) {
		t.Fatalf("genre nul: err=%v", err)
	}
	ev = sampleTxnEvent(EventMutation, 1, []byte("k"), nil)
	ev.Shard = uint16(NumShards)
	if _, err := EncodeTxnEvent(ev); !errors.Is(err, errTxLogRange) {
		t.Fatalf("shard hors bornes: err=%v", err)
	}
	ev = sampleTxnEvent(EventMutation, 1, make([]byte, txnEventMaxKey+1), nil)
	if _, err := EncodeTxnEvent(ev); !errors.Is(err, errTxLogRange) {
		t.Fatalf("clé hors bornes: err=%v", err)
	}
}

func TestTxnEventVersionIDSize(t *testing.T) {
	if szVersion != 16 {
		t.Fatalf("szVersion=%d, attendu 16", szVersion)
	}
	if !reflect.TypeOf(TxnEvent{}).Field(4).Type.AssignableTo(reflect.TypeOf([16]byte{})) {
		t.Fatal("VersionID doit être un tableau de 16 octets")
	}
}

var _ EventSink = (*TxLog)(nil)

func TestTxLogUsesTempDirOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "txnlog")
	l, err := OpenTxLog(dir, 0)
	if err != nil {
		t.Fatalf("OpenTxLog imbriqué: %v", err)
	}
	if l.maxSegmentBytes != DefaultTxLogSegmentBytes {
		t.Fatalf("plafond par défaut=%d", l.maxSegmentBytes)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
