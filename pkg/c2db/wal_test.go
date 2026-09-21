// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWALCycleComplete(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c2db-wal.img"
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}

	w, err := CreateWAL(path, testImageSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	assertODirect(t, w)

	too := Record{Type: RecPut, Payload: make([]byte, testImageSize+LBASize)}
	copy(too.ID[:], []byte("too-long-record!"))
	if err := w.Append(too); err == nil {
		t.Fatalf("Append trop long: expected error")
	}

	var idPut, idDel [16]byte
	copy(idPut[:], []byte("put-record-00001"))
	copy(idDel[:], []byte("del-record-00002"))
	putPayload := []byte("payload-put-bit-exact")
	delPayload := []byte("payload-del-bit-exact")

	if err := w.Append(Record{ID: idPut, Type: RecPut, Payload: putPayload}); err != nil {
		t.Fatalf("Append put: %v", err)
	}
	if err := w.Append(Record{ID: idDel, Type: RecDel, Payload: delPayload}); err != nil {
		t.Fatalf("Append del: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w, err = OpenWAL(path, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("OpenWAL: %v", err)
	}
	assertODirect(t, w)
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("Replay count: got %d want 2", len(recs))
	}
	if recs[0].ID != idPut || recs[0].Type != RecPut || !bytes.Equal(recs[0].Payload, putPayload) {
		t.Fatalf("replay put mismatch: id=%x type=%d payload=%q", recs[0].ID, recs[0].Type, recs[0].Payload)
	}
	if recs[1].ID != idDel || recs[1].Type != RecDel || !bytes.Equal(recs[1].Payload, delPayload) {
		t.Fatalf("replay del mismatch: id=%x type=%d payload=%q", recs[1].ID, recs[1].Type, recs[1].Payload)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close after replay: %v", err)
	}

	dev, err := Open(path)
	if err != nil {
		t.Fatalf("Open device for corrupt: %v", err)
	}
	blk := mmapAligned(t, LBASize)
	if err := dev.Read(0, blk); err != nil {
		t.Fatalf("Read LBA 0: %v", err)
	}
	blk[walPayloadOff] ^= 0xFF
	if err := dev.Write(0, blk); err != nil {
		t.Fatalf("Write corrupted LBA 0: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatalf("Flush corrupt: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close device: %v", err)
	}

	w, err = OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL after corrupt: %v", err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Replay(); err == nil {
		t.Fatalf("Replay after payload flip: expected tag error")
	} else if !errors.Is(err, errWALBadTag) {
		t.Fatalf("Replay after payload flip: got %v, want tag mismatch", err)
	}
}

func TestWALCheckpointCompact(t *testing.T) {
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)

	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("compact-put-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append put %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay before checkpoint: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("Replay count: got %d want 4", len(recs))
	}

	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := w.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	recs, err = w.Replay()
	if err != nil {
		t.Fatalf("Replay after compact: %v", err)
	}
	nPut := 0
	for _, rec := range recs {
		if rec.Type == RecPut {
			nPut++
		}
	}
	if nPut != 0 {
		t.Fatalf("Replay after compact: got %d put, want 0 (prefix dropped)", nPut)
	}
	if len(recs) > 1 {
		t.Fatalf("Replay after compact: got %d records, want 0 or 1 checkpoint", len(recs))
	}
	if len(recs) == 1 && recs[0].Type != RecCheckpointEnd {
		t.Fatalf("Replay after compact: single record type %d, want RecCheckpointEnd", recs[0].Type)
	}

	extra := walCrashRecord(4, []byte("compact-put-after"))
	if err := w.Append(extra); err != nil {
		t.Fatalf("Append after compact: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush after compact: %v", err)
	}
	recs, err = w.Replay()
	if err != nil {
		t.Fatalf("Replay after post-compact put: %v", err)
	}
	found := false
	for _, rec := range recs {
		if rec.Type == RecPut && rec.ID == extra.ID && bytes.Equal(rec.Payload, extra.Payload) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Replay after compact+put: missing post-compact put")
	}
	assertODirect(t, w)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWALCompactReopen(t *testing.T) {
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)

	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("reopen-put-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append put %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := w.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	arch := path + ".arch"
	if _, err := os.Stat(arch); err != nil {
		t.Fatalf("archive absente: %v", err)
	}
	if err := VerifyArchive(arch, key); err != nil {
		t.Fatalf("VerifyArchive: %v", err)
	}
	if !w.hasCheck || w.checkLBA != 0 {
		t.Fatalf("après compact: hasCheck=%v checkLBA=%d, want true/0", w.hasCheck, w.checkLBA)
	}

	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay after compact: %v", err)
	}
	if len(recs) == 0 || recs[0].Type != RecCheckpointEnd {
		t.Fatalf("LBA 0: want RecCheckpointEnd, got %v", recs)
	}
	if len(recs[0].Payload) < 8 {
		t.Fatalf("checkpoint payload trop court: %d", len(recs[0].Payload))
	}
	gotNext := binary.LittleEndian.Uint64(recs[0].Payload)
	if gotNext != w.next {
		t.Fatalf("checkpoint payload %d, want next %d", gotNext, w.next)
	}
	nPut := 0
	for _, rec := range recs {
		if rec.Type == RecPut {
			nPut++
		}
	}
	if nPut != 0 {
		t.Fatalf("Replay after compact: got %d put, want 0", nPut)
	}

	extra := walCrashRecord(4, []byte("reopen-put-after"))
	if err := w.Append(extra); err != nil {
		t.Fatalf("Append after compact: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush after compact: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w = mustOpenWAL(t, path, key)
	defer func() { _ = w.Close() }()
	if !w.hasCheck || w.checkLBA != 0 {
		t.Fatalf("OpenWAL: hasCheck=%v checkLBA=%d, want true/0", w.hasCheck, w.checkLBA)
	}
	recs, err = w.Replay()
	if err != nil {
		t.Fatalf("Replay after reopen: %v", err)
	}
	nPut = 0
	found := false
	for _, rec := range recs {
		if rec.Type == RecCheckpointBegin || rec.Type == RecCheckpointEnd {
			continue
		}
		if rec.Type == RecPut {
			nPut++
			if rec.ID == extra.ID && bytes.Equal(rec.Payload, extra.Payload) {
				found = true
			}
		}
	}
	if nPut != 1 {
		t.Fatalf("Replay after reopen: got %d put, want 1 (suffix only)", nPut)
	}
	if !found {
		t.Fatalf("Replay after reopen: put post-compact absent")
	}
}

func TestWALCompactPrefixZeroed(t *testing.T) {
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)
	oldNext := uint64(0)
	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("zero-put-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	oldNext = w.next
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := w.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if w.next == 0 || w.next >= oldNext {
		t.Fatalf("next après compact=%d old=%d", w.next, oldNext)
	}
	blk := mmapAligned(t, LBASize)
	defer func() { _ = unix.Munmap(blk) }()
	for i := w.next; i < oldNext; i++ {
		if err := w.dev.Read(i, blk); err != nil {
			t.Fatalf("Read LBA %d: %v", i, err)
		}
		for j := 0; j < len(blk); j++ {
			if blk[j] != 0 {
				t.Fatalf("préfixe non zéro LBA %d off %d=%d", i, j, blk[j])
			}
		}
	}
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	nPut := 0
	for _, rec := range recs {
		if rec.Type == RecPut {
			nPut++
		}
	}
	if nPut != 0 {
		t.Fatalf("puts persistés dans le préfixe: %d", nPut)
	}
	extra := walCrashRecord(9, []byte("after-zero"))
	if err := w.Append(extra); err != nil {
		t.Fatalf("Append extra: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush extra: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	w = mustOpenWAL(t, path, key)
	defer func() { _ = w.Close() }()
	recs, err = w.Replay()
	if err != nil {
		t.Fatalf("Replay reopen: %v", err)
	}
	found := false
	for _, rec := range recs {
		if rec.Type == RecPut && rec.ID == extra.ID && bytes.Equal(rec.Payload, extra.Payload) {
			found = true
		}
	}
	if !found {
		t.Fatal("put post-compact absent après réouverture")
	}
}

func TestWALArchiveTamper(t *testing.T) {
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)
	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("tamper-put-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append put %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := w.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	arch := path + ".arch"
	if err := VerifyArchive(arch, key); err != nil {
		t.Fatalf("VerifyArchive intact: %v", err)
	}
	data, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}
	if len(data) <= ArchHdrSize+walPayloadOff {
		t.Fatalf("archive trop courte: %d", len(data))
	}
	data[ArchHdrSize+walPayloadOff] ^= 0xFF
	if err := os.WriteFile(arch, data, 0o600); err != nil {
		t.Fatalf("WriteFile tamper: %v", err)
	}
	if err := VerifyArchive(arch, key); err == nil {
		t.Fatalf("VerifyArchive après falsification: expected error")
	}
}

func TestWALArchiveEncRoundtrip(t *testing.T) {
	path, key := walCrashImage(t)
	var encKey [32]byte
	for i := range encKey {
		encKey[i] = byte(i + 19)
	}
	w := mustCreateWAL(t, path, key)
	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("enc-put-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append put %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	nPrefix := w.checkLBA
	if nPrefix == 0 {
		t.Fatalf("checkLBA=0, want prefix")
	}
	want := make([]byte, nPrefix*uint64(LBASize))
	blk := mmapAligned(t, LBASize)
	for i := uint64(0); i < nPrefix; i++ {
		if err := w.dev.Read(i, blk); err != nil {
			t.Fatalf("Read LBA %d: %v", i, err)
		}
		copy(want[i*uint64(LBASize):], blk)
	}
	arch := path + ".arch"
	if err := w.CompactToEnc(arch, encKey); err != nil {
		t.Fatalf("CompactToEnc: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := VerifyArchive(arch, key); err != nil {
		t.Fatalf("VerifyArchive sans encKey: %v", err)
	}
	if _, err := OpenArchive(arch, key, nil); err != errArchNeedEnc {
		t.Fatalf("OpenArchive sans encKey: %v", err)
	}
	got, err := OpenArchive(arch, key, &encKey)
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("OpenArchive plaintext distinct du prefixe WAL")
	}
	raw, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if uint64(len(raw)) < uint64(ArchHdrSize)+uint64(EncBodyTagSize)+nPrefix*uint64(LBASize) {
		t.Fatalf("archive chiffrée trop courte: %d", len(raw))
	}
	ct := raw[ArchHdrSize : len(raw)-EncBodyTagSize]
	if bytes.Equal(ct, want) {
		t.Fatalf("corps d'archive non chiffré")
	}
}

func TestWALArchiveEncTamper(t *testing.T) {
	path, key := walCrashImage(t)
	var encKey [32]byte
	for i := range encKey {
		encKey[i] = byte(i + 23)
	}
	w := mustCreateWAL(t, path, key)
	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("enc-tamper-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append put %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	arch := path + ".arch"
	if err := w.CompactToEnc(arch, encKey); err != nil {
		t.Fatalf("CompactToEnc: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := VerifyArchive(arch, key); err != nil {
		t.Fatalf("VerifyArchive intact: %v", err)
	}
	data, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}
	if len(data) <= ArchHdrSize {
		t.Fatalf("archive trop courte: %d", len(data))
	}
	data[ArchHdrSize] ^= 0xFF
	if err := os.WriteFile(arch, data, 0o600); err != nil {
		t.Fatalf("WriteFile tamper: %v", err)
	}
	if err := VerifyArchive(arch, key); err == nil {
		t.Fatalf("VerifyArchive après falsification ciphertext: expected error")
	}
}

func TestWALCompactEnc(t *testing.T) {
	path, key := walCrashImage(t)
	var encKey [32]byte
	for i := range encKey {
		encKey[i] = byte(i + 29)
	}
	w := mustCreateWAL(t, path, key)
	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("compact-enc-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append put %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	arch := path + ".enc.arch"
	if err := w.CompactToEnc(arch, encKey); err != nil {
		t.Fatalf("CompactToEnc: %v", err)
	}
	if err := VerifyArchive(arch, key); err != nil {
		t.Fatalf("VerifyArchive: %v", err)
	}
	if _, err := OpenArchive(arch, key, nil); err != errArchNeedEnc {
		t.Fatalf("OpenArchive sans encKey: %v", err)
	}
	if _, err := OpenArchive(arch, key, &encKey); err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay after compact: %v", err)
	}
	nPut := 0
	for _, rec := range recs {
		if rec.Type == RecPut {
			nPut++
		}
	}
	if nPut != 0 {
		t.Fatalf("Replay after compact: got %d put, want 0", nPut)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func assertODirect(t *testing.T, w *WAL) {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(w.dev.fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("F_GETFL: %v", err)
	}
	if flags&unix.O_DIRECT == 0 {
		t.Fatalf("WAL device fd lacks O_DIRECT (flags=%#x)", flags)
	}
}

func TestWALCrashWriteFlush(t *testing.T) {
	const durableN = 3
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)

	durable := make([]Record, durableN)
	for i := range durable {
		durable[i] = walCrashRecord(i, []byte("durable-payload-bit-exact-"+itoa(i)))
		if err := w.Append(durable[i]); err != nil {
			t.Fatalf("Append durable %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush durable: %v", err)
	}

	extra := walCrashRecord(durableN, []byte("unflushed-nplus1-bit-exact-payload"))
	if err := w.Append(extra); err != nil {
		t.Fatalf("Append N+1: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close without Flush: %v", err)
	}

	w = mustOpenWAL(t, path, key)
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay after crash write/flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close after replay: %v", err)
	}
	assertReplayPrefixOrNext(t, recs, durable, extra)
}

func TestWALCrashWriteFlushCorruptNPlus1(t *testing.T) {
	const durableN = 3
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)

	durable := make([]Record, durableN)
	for i := range durable {
		durable[i] = walCrashRecord(i, []byte("durable-payload-bit-exact-"+itoa(i)))
		if err := w.Append(durable[i]); err != nil {
			t.Fatalf("Append durable %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush durable: %v", err)
	}
	extra := walCrashRecord(durableN, []byte("unflushed-nplus1-bit-exact-payload"))
	if err := w.Append(extra); err != nil {
		t.Fatalf("Append N+1: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close without Flush: %v", err)
	}

	xfer, checkCanaries := mmapXferWithCanaries(t)
	flipWALBlock(t, path, uint64(durableN), walPayloadOff, xfer)
	checkCanaries()

	w = mustOpenWAL(t, path, key)
	recs, replayErr := w.Replay()
	if err := w.Close(); err != nil {
		t.Fatalf("Close after corrupt replay: %v", err)
	}
	if replayErr == nil {
		t.Fatalf("Replay after N+1 flip: expected errWALBadTag, got %d records", len(recs))
	}
	if !errors.Is(replayErr, errWALBadTag) {
		t.Fatalf("Replay after N+1 flip: got %v, want errWALBadTag", replayErr)
	}
	if recs != nil {
		t.Fatalf("Replay after N+1 flip mixed records with N: got %d", len(recs))
	}
}

func TestWALPoly1305FaultThenHealthy(t *testing.T) {
	const durableN = 3
	path, key := walCrashImage(t)
	w := mustCreateWAL(t, path, key)
	durable := make([]Record, durableN)
	for i := range durable {
		durable[i] = walCrashRecord(i, []byte("poly-healthy-"+itoa(i)))
		if err := w.Append(durable[i]); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	xfer, checkCanaries := mmapXferWithCanaries(t)
	flipWALBlock(t, path, 0, walPayloadOff, xfer)
	checkCanaries()
	w = mustOpenWAL(t, path, key)
	_, replayErr := w.Replay()
	if err := w.Close(); err != nil {
		t.Fatalf("Close after faute: %v", err)
	}
	if !errors.Is(replayErr, errWALBadTag) {
		t.Fatalf("faute: got %v want errWALBadTag", replayErr)
	}
	flipWALBlock(t, path, 0, walPayloadOff, xfer)
	checkCanaries()
	w = mustOpenWAL(t, path, key)
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("reprise: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close reprise: %v", err)
	}
	if len(recs) < durableN {
		t.Fatalf("reprise n=%d want >= %d", len(recs), durableN)
	}
	for i := range durable {
		if recs[i].Type != durable[i].Type || recs[i].ID != durable[i].ID || !bytes.Equal(recs[i].Payload, durable[i].Payload) {
			t.Fatalf("reprise record %d divergente", i)
		}
	}
}

func TestWALCrashWriteFlushTornTail(t *testing.T) {
	cases := []struct {
		name string
		off  int
		n    int
	}{
		{name: "magic_incomplet", off: 0, n: 2},
		{name: "tag_incomplet", off: walTagOff + 8, n: 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const durableN = 3
			path, key := walCrashImage(t)
			w := mustCreateWAL(t, path, key)
			durable := make([]Record, durableN)
			for i := range durable {
				durable[i] = walCrashRecord(i, []byte("durable-payload-bit-exact-"+itoa(i)))
				if err := w.Append(durable[i]); err != nil {
					t.Fatalf("Append durable %d: %v", i, err)
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatalf("Flush durable: %v", err)
			}
			extra := walCrashRecord(durableN, []byte("unflushed-nplus1-bit-exact-payload"))
			if err := w.Append(extra); err != nil {
				t.Fatalf("Append N+1: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close without Flush: %v", err)
			}

			xfer, checkCanaries := mmapXferWithCanaries(t)
			zeroWALBlockRange(t, path, uint64(durableN), tc.off, tc.n, xfer)
			checkCanaries()

			w = mustOpenWAL(t, path, key)
			recs, replayErr := w.Replay()
			if err := w.Close(); err != nil {
				t.Fatalf("Close after torn replay: %v", err)
			}
			if replayErr != nil {
				if !errors.Is(replayErr, errWALBadTag) && !errors.Is(replayErr, errWALMagic) {
					t.Fatalf("torn tail: got %v, want errWALBadTag|errWALMagic or prefix", replayErr)
				}
				if recs != nil {
					t.Fatalf("torn tail error mixed records: got %d", len(recs))
				}
				return
			}
			if len(recs) != durableN {
				t.Fatalf("torn tail absence: got %d records, want prefix %d", len(recs), durableN)
			}
			for i := range durable {
				assertRecordEqual(t, recs[i], durable[i], i)
			}
		})
	}
}

func TestWALCrashWriteFlushFuzz(t *testing.T) {
	const seed = int64(0xC2DBF100)
	pass1 := runWALCrashFuzzPass(t, seed)
	pass2 := runWALCrashFuzzPass(t, seed)
	if !bytes.Equal(pass1, pass2) {
		t.Fatalf("fuzz determinism: two passes with seed %d diverged", seed)
	}
}

func runWALCrashFuzzPass(t *testing.T, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var out []byte
	for i := 0; i < 32; i++ {
		durableN := 1 + rng.Intn(4)
		flip := rng.Intn(2) == 1
		flipOff := 4 + rng.Intn(LBASize-4)
		path, key := walCrashImage(t)
		w := mustCreateWAL(t, path, key)
		durable := make([]Record, durableN)
		for j := range durable {
			payload := make([]byte, 16+rng.Intn(48))
			rng.Read(payload)
			durable[j] = walCrashRecord(j, payload)
			if err := w.Append(durable[j]); err != nil {
				t.Fatalf("fuzz Append durable %d: %v", j, err)
			}
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("fuzz Flush: %v", err)
		}
		extraPayload := make([]byte, 24+rng.Intn(32))
		rng.Read(extraPayload)
		extra := walCrashRecord(durableN, extraPayload)
		if err := w.Append(extra); err != nil {
			t.Fatalf("fuzz Append N+1: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("fuzz Close: %v", err)
		}

		xfer, checkCanaries := mmapXferWithCanaries(t)
		if flip {
			flipWALBlock(t, path, uint64(durableN), flipOff, xfer)
		}
		want, wantErr := oracleCrashReplay(t, path, key, uint64(durableN), durable, extra, xfer)
		checkCanaries()

		w = mustOpenWAL(t, path, key)
		got, replayErr := w.Replay()
		if err := w.Close(); err != nil {
			t.Fatalf("fuzz Close after replay: %v", err)
		}
		if wantErr != nil {
			if replayErr == nil {
				t.Fatalf("fuzz iter %d: Replay succeeded with %d records, want %v", i, len(got), wantErr)
			}
			if !errors.Is(replayErr, wantErr) {
				t.Fatalf("fuzz iter %d: Replay err %v, want %v", i, replayErr, wantErr)
			}
			if got != nil {
				t.Fatalf("fuzz iter %d: mixed records under error: %d", i, len(got))
			}
			out = append(out, 0xEE, byte(durableN), byte(flipOff>>8), byte(flipOff))
			continue
		}
		if replayErr != nil {
			t.Fatalf("fuzz iter %d: Replay: %v", i, replayErr)
		}
		if len(got) != len(want) {
			t.Fatalf("fuzz iter %d: got %d records want %d", i, len(got), len(want))
		}
		for j := range want {
			assertRecordEqual(t, got[j], want[j], j)
		}
		out = append(out, byte(len(got)), byte(durableN))
		for _, rec := range got {
			out = append(out, rec.Payload...)
		}
		checkCanaries()
	}
	return out
}

func walCrashImage(t *testing.T) (string, [32]byte) {
	t.Helper()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	return t.TempDir() + "/c2db-wal-crash.img", key
}

func mustCreateWAL(t *testing.T, path string, key [32]byte) *WAL {
	t.Helper()
	w, err := CreateWAL(path, testImageSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	assertODirect(t, w)
	return w
}

func mustOpenWAL(t *testing.T, path string, key [32]byte) *WAL {
	t.Helper()
	w, err := OpenWAL(path, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("OpenWAL: %v", err)
	}
	assertODirect(t, w)
	return w
}

func walCrashRecord(seq int, payload []byte) Record {
	var id [16]byte
	copy(id[:], []byte("crash-rec"))
	binary.LittleEndian.PutUint32(id[12:], uint32(seq))
	p := make([]byte, len(payload))
	copy(p, payload)
	return Record{ID: id, Type: RecPut, Payload: p}
}

func itoa(n int) string {
	if n < 0 || n > 9 {
		return string(rune('0' + (n % 10)))
	}
	return string(rune('0' + n))
}

func assertRecordEqual(t *testing.T, got, want Record, i int) {
	t.Helper()
	if got.ID != want.ID || got.Type != want.Type || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("record %d mismatch: id=%x type=%d payload=%q want id=%x type=%d payload=%q",
			i, got.ID, got.Type, got.Payload, want.ID, want.Type, want.Payload)
	}
}

func assertReplayPrefixOrNext(t *testing.T, recs []Record, durable []Record, extra Record) {
	t.Helper()
	n := len(durable)
	if len(recs) != n && len(recs) != n+1 {
		t.Fatalf("Replay count: got %d want %d or %d", len(recs), n, n+1)
	}
	for i := 0; i < n; i++ {
		assertRecordEqual(t, recs[i], durable[i], i)
	}
	if len(recs) == n+1 {
		assertRecordEqual(t, recs[n], extra, n)
	}
}

func mmapXferWithCanaries(t *testing.T) ([]byte, func()) {
	t.Helper()
	const canary = LBASize
	buf, err := unix.Mmap(-1, 0, canary+LBASize+canary, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		t.Fatalf("mmap canaries: %v", err)
	}
	t.Cleanup(func() { _ = unix.Munmap(buf) })
	for i := 0; i < canary; i++ {
		buf[i] = 0xAA
		buf[canary+LBASize+i] = 0x55
	}
	xfer := buf[canary : canary+LBASize]
	check := func() {
		t.Helper()
		for i := 0; i < canary; i++ {
			if buf[i] != 0xAA {
				t.Fatalf("canari avant corrompu à %d: %#x", i, buf[i])
			}
			if buf[canary+LBASize+i] != 0x55 {
				t.Fatalf("canari arrière corrompu à %d: %#x", i, buf[canary+LBASize+i])
			}
		}
	}
	return xfer, check
}

func flipWALBlock(t *testing.T, path string, lba uint64, off int, xfer []byte) {
	t.Helper()
	dev, err := Open(path)
	if err != nil {
		t.Fatalf("Open device for flip: %v", err)
	}
	if err := dev.Read(lba, xfer); err != nil {
		_ = dev.Close()
		t.Fatalf("Read LBA %d: %v", lba, err)
	}
	xfer[off] ^= 0xFF
	if err := dev.Write(lba, xfer); err != nil {
		_ = dev.Close()
		t.Fatalf("Write flipped LBA %d: %v", lba, err)
	}
	if err := dev.Flush(); err != nil {
		_ = dev.Close()
		t.Fatalf("Flush flip: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close device after flip: %v", err)
	}
}

func zeroWALBlockRange(t *testing.T, path string, lba uint64, off, n int, xfer []byte) {
	t.Helper()
	dev, err := Open(path)
	if err != nil {
		t.Fatalf("Open device for zero: %v", err)
	}
	if err := dev.Read(lba, xfer); err != nil {
		_ = dev.Close()
		t.Fatalf("Read LBA %d: %v", lba, err)
	}
	for i := 0; i < n; i++ {
		xfer[off+i] = 0
	}
	if err := dev.Write(lba, xfer); err != nil {
		_ = dev.Close()
		t.Fatalf("Write zeroed LBA %d: %v", lba, err)
	}
	if err := dev.Flush(); err != nil {
		_ = dev.Close()
		t.Fatalf("Flush zero: %v", err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("Close device after zero: %v", err)
	}
}

func oracleCrashReplay(t *testing.T, path string, key [32]byte, extraLBA uint64, durable []Record, extra Record, xfer []byte) ([]Record, error) {
	t.Helper()
	dev, err := Open(path)
	if err != nil {
		t.Fatalf("oracle Open: %v", err)
	}
	if err := dev.Read(extraLBA, xfer); err != nil {
		_ = dev.Close()
		t.Fatalf("oracle Read LBA %d: %v", extraLBA, err)
	}
	if err := dev.Close(); err != nil {
		t.Fatalf("oracle Close: %v", err)
	}
	magic := binary.LittleEndian.Uint32(xfer[:4])
	if magic != WALMagic {
		return durable, nil
	}
	if !verifyWAL(xfer, key) {
		return nil, errWALBadTag
	}
	var rec Record
	d := Db_wal_unpack(xfer, uint64(LBASize), rec.ID[:])
	if d.Ok == 0 {
		if d.Magic != WALMagic {
			return nil, errWALMagic
		}
		return nil, errWALTooLong
	}
	rec.Type = RecType(d.Typ)
	off := int(Db_wal_payload_off())
	rec.Payload = make([]byte, d.Plen)
	copy(rec.Payload, xfer[off:off+int(d.Plen)])
	if rec.ID != extra.ID || rec.Type != extra.Type || !bytes.Equal(rec.Payload, extra.Payload) {
		t.Fatalf("oracle N+1 payload not bit-exact: id=%x type=%d payload=%q", rec.ID, rec.Type, rec.Payload)
	}
	out := make([]Record, 0, len(durable)+1)
	out = append(out, durable...)
	out = append(out, rec)
	return out, nil
}

func TestWALPackMultipleRecordsOneBlock(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c2db-wal.img"
	var key [32]byte
	w, err := CreateWAL(path, testImageSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("CreateWAL: %v", err)
	}
	w.BeginPack()
	for i := 0; i < 5; i++ {
		var id [16]byte
		id[0] = byte(i + 1)
		if err := w.Append(Record{ID: id, Type: RecPut, Payload: []byte{byte('a' + i)}}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if w.next != 0 {
		t.Fatalf("blocs avant EndPack=%d want 0", w.next)
	}
	if err := w.EndPack(); err != nil {
		t.Fatalf("EndPack: %v", err)
	}
	if w.next != 1 {
		t.Fatalf("blocs après pack=%d want 1", w.next)
	}
	recs, err := w.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) != 5 {
		t.Fatalf("Replay n=%d want 5", len(recs))
	}
	if recs[0].Payload[0] != 'a' || recs[4].Payload[0] != 'e' {
		t.Fatalf("payloads %q %q", recs[0].Payload, recs[4].Payload)
	}
	_ = w.Close()
}

func TestWALMultiLBAChunkedLargeRecord(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c2db-wal-large.img"
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 42)
	}

	// Image WAL de 4 Mo
	const largeImgSize = 4 * 1024 * 1024
	w, err := CreateWAL(path, largeImgSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("CreateWAL: %v", err)
	}

	// Payload de 100 Ko (> 24 LBAs de 4055 octets)
	const payloadSize = 100 * 1024
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte((i*31 + 7) & 0xFF)
	}

	var recID [16]byte
	copy(recID[:], []byte("chunked-large-01"))

	rec := Record{
		ID:      recID,
		Type:    RecPut,
		Payload: payload,
	}

	if err := w.Append(rec); err != nil {
		t.Fatalf("Append large record: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Réouverture et relecture
	w2, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	defer w2.Close()

	recs, err := w2.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(recs) != 1 {
		t.Fatalf("Replay records count=%d want 1", len(recs))
	}
	if recs[0].ID != recID {
		t.Fatalf("Replay ID mismatch: got %v want %v", recs[0].ID, recID)
	}
	if recs[0].Type != RecPut {
		t.Fatalf("Replay Type mismatch: got %v want %v", recs[0].Type, RecPut)
	}
	if !bytes.Equal(recs[0].Payload, payload) {
		t.Fatalf("Replay Payload bit-exact mismatch (len %d vs %d)", len(recs[0].Payload), len(payload))
	}
}

func TestWALGroupCommit128K(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c2db-wal-group.img"
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 13)
	}

	const imgSize = 2 * 1024 * 1024
	w, err := CreateWAL(path, imgSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("CreateWAL: %v", err)
	}

	// Écrire 64 enregistrements (devant déclencher au moins 2 flushs quantum 128 Ko = 32 LBAs)
	for i := 0; i < 64; i++ {
		var id [16]byte
		binary.LittleEndian.PutUint64(id[:8], uint64(i+1))
		payload := make([]byte, 2000)
		for j := range payload {
			payload[j] = byte(i + j)
		}
		if err := w.Append(Record{ID: id, Type: RecPut, Payload: payload}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Réouverture
	w2, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	defer w2.Close()

	recs, err := w2.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) != 64 {
		t.Fatalf("Replay count=%d want 64", len(recs))
	}
	for i := 0; i < 64; i++ {
		var expID [16]byte
		binary.LittleEndian.PutUint64(expID[:8], uint64(i+1))
		if recs[i].ID != expID {
			t.Fatalf("Record %d ID mismatch", i)
		}
		if len(recs[i].Payload) != 2000 {
			t.Fatalf("Record %d Payload len=%d want 2000", i, len(recs[i].Payload))
		}
	}
}

func TestWALAutoRecycleUnderLoad(t *testing.T) {
	path, key := walCrashImage(t)
	// WAL borné à 32 LBAs (128 Ko)
	w, err := CreateWAL(path, 32*LBASize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("CreateWAL: %v", err)
	}

	// Écrire 100 enregistrements (chaque writeOne consomme 1 LBA).
	// Sans recyclage, l'écriture échouerait dès le 32e LBA.
	for i := 0; i < 100; i++ {
		var id [16]byte
		binary.LittleEndian.PutUint64(id[:8], uint64(i+1))
		rec := Record{
			ID:      id,
			Type:    RecPut,
			Payload: []byte("val-" + itoa(i)),
		}
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		// Poser un point de contrôle tous les 10 enregistrements
		if (i+1)%10 == 0 {
			if err := w.Checkpoint(); err != nil {
				t.Fatalf("Checkpoint %d: %v", i, err)
			}
		}
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Réouverture et intégrité
	w2, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	defer w2.Close()

	recs, err := w2.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(recs) == 0 {
		t.Fatalf("Replay: expected records, got 0")
	}
}
