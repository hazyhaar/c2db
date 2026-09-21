// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/mldsa"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func testMLDSASeed(fill byte) []byte {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = fill
	}
	return seed
}

func testMLDSAKey(t *testing.T, fill byte) *mldsa.PrivateKey {
	t.Helper()
	sk, err := mldsa.NewPrivateKey(mldsa.MLDSA44(), testMLDSASeed(fill))
	if err != nil {
		t.Fatalf("NewPrivateKey: %v", err)
	}
	return sk
}

func TestMLDSAExportVerifyOK(t *testing.T) {
	sk := testMLDSAKey(t, 0x11)
	pub := sk.PublicKey()
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 3)
	}

	s := mustOpenShard(t, dir, key, 7)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snap := filepath.Join(dir, "snap.c2sn")
	if err := ExportSigned(s, snap, key, sk); err != nil {
		t.Fatalf("ExportSigned: %v", err)
	}
	raw, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("ReadFile snap: %v", err)
	}
	if uint64(len(raw)) < uint64(SnapHdrSize)+uint64(mldsa.MLDSA44SignatureSize) {
		t.Fatalf("snap trop court: %d", len(raw))
	}
	if binary.LittleEndian.Uint16(raw[snapSigTypeOff:]) != SigTypeMLDSA44 {
		t.Fatalf("snap sigType=%d", binary.LittleEndian.Uint16(raw[snapSigTypeOff:]))
	}
	if binary.LittleEndian.Uint16(raw[snapSigLenOff:]) != uint16(mldsa.MLDSA44SignatureSize) {
		t.Fatalf("snap sigLen=%d", binary.LittleEndian.Uint16(raw[snapSigLenOff:]))
	}
	slot := raw[snapSigOff : snapSigOff+SnapSigSize]
	for i := 0; i < len(slot); i++ {
		if slot[i] != 0 {
			t.Fatalf("snap slot non nul à %d", i)
		}
	}
	if err := VerifySnapshotAuth(snap, key, pub); err != nil {
		t.Fatalf("VerifySnapshotAuth: %v", err)
	}
	if err := VerifySnapshot(snap, key); err != errSnapNeedPub {
		t.Fatalf("VerifySnapshot signed: %v", err)
	}

	wpath, wkey := walCrashImage(t)
	w := mustCreateWAL(t, wpath, wkey)
	defer func() { _ = w.Close() }()
	for i := 0; i < 4; i++ {
		rec := walCrashRecord(i, []byte("mldsa-put-"+itoa(i)))
		if err := w.Append(rec); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	arch := wpath + ".arch"
	if err := w.CompactToSigned(arch, sk); err != nil {
		t.Fatalf("CompactToSigned: %v", err)
	}
	araw, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile arch: %v", err)
	}
	if uint64(len(araw)) < uint64(ArchHdrSize)+uint64(mldsa.MLDSA44SignatureSize) {
		t.Fatalf("arch trop court: %d", len(araw))
	}
	if binary.LittleEndian.Uint16(araw[archSigTypeOff:]) != SigTypeMLDSA44 {
		t.Fatalf("arch sigType=%d", binary.LittleEndian.Uint16(araw[archSigTypeOff:]))
	}
	aslot := araw[archSigOff : archSigOff+ArchSigSize]
	for i := 0; i < len(aslot); i++ {
		if aslot[i] != 0 {
			t.Fatalf("arch slot non nul à %d", i)
		}
	}
	if err := VerifyArchiveAuth(arch, wkey, pub); err != nil {
		t.Fatalf("VerifyArchiveAuth: %v", err)
	}
	if err := VerifyArchive(arch, wkey); err != errArchNeedPub {
		t.Fatalf("VerifyArchive signed: %v", err)
	}
}

func TestMLDSAOctetFlipMAC(t *testing.T) {
	sk := testMLDSAKey(t, 0x11)
	pub := sk.PublicKey()
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 5)
	}
	s := mustOpenShard(t, dir, key, 3)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snap := filepath.Join(dir, "snap.c2sn")
	if err := ExportSigned(s, snap, key, sk); err != nil {
		t.Fatalf("ExportSigned: %v", err)
	}
	sdata, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("ReadFile snap: %v", err)
	}
	sdata[SnapHdrSize-1] ^= 0xFF
	if err := os.WriteFile(snap, sdata, 0o600); err != nil {
		t.Fatalf("WriteFile snap: %v", err)
	}
	if err := VerifySnapshotAuth(snap, key, pub); err != errSnapBadTag {
		t.Fatalf("snap tag flip: %v", err)
	}

	wpath, wkey := walCrashImage(t)
	w := mustCreateWAL(t, wpath, wkey)
	defer func() { _ = w.Close() }()
	for i := 0; i < 4; i++ {
		if err := w.Append(walCrashRecord(i, []byte("flip-"+itoa(i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	arch := wpath + ".arch"
	if err := w.CompactToSigned(arch, sk); err != nil {
		t.Fatalf("CompactToSigned: %v", err)
	}
	adata, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile arch: %v", err)
	}
	hdrFlip := bytes.Clone(adata)
	hdrFlip[ArchHdrSize-1] ^= 0xFF
	hdrPath := arch + ".hdrflip"
	if err := os.WriteFile(hdrPath, hdrFlip, 0o600); err != nil {
		t.Fatalf("WriteFile hdrflip: %v", err)
	}
	if err := VerifyArchiveAuth(hdrPath, wkey, pub); err != errArchBadTag {
		t.Fatalf("arch tag flip: %v", err)
	}
	bodyFlip := bytes.Clone(adata)
	if len(bodyFlip) <= ArchHdrSize+walPayloadOff {
		t.Fatalf("arch trop courte: %d", len(bodyFlip))
	}
	bodyFlip[ArchHdrSize+walPayloadOff] ^= 0xFF
	bodyPath := arch + ".bodyflip"
	if err := os.WriteFile(bodyPath, bodyFlip, 0o600); err != nil {
		t.Fatalf("WriteFile bodyflip: %v", err)
	}
	err = VerifyArchiveAuth(bodyPath, wkey, pub)
	if err == errArchBadSig {
		t.Fatalf("body flip rapporté comme BadSig")
	}
	if err != errWALBadTag && err != errArchBadTag {
		t.Fatalf("body flip: %v", err)
	}
}

func TestMLDSATruncatedSig(t *testing.T) {
	sk := testMLDSAKey(t, 0x11)
	pub := sk.PublicKey()
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 7)
	}
	s := mustOpenShard(t, dir, key, 9)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snap := filepath.Join(dir, "snap.c2sn")
	if err := ExportSigned(s, snap, key, sk); err != nil {
		t.Fatalf("ExportSigned: %v", err)
	}
	sdata, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("ReadFile snap: %v", err)
	}
	if err := os.WriteFile(snap, sdata[:SnapHdrSize+mldsa.MLDSA44SignatureSize-1], 0o600); err != nil {
		t.Fatalf("WriteFile snap trunc: %v", err)
	}
	if err := VerifySnapshotAuth(snap, key, pub); err != errSnapSize {
		t.Fatalf("snap trunc: %v", err)
	}

	wpath, wkey := walCrashImage(t)
	w := mustCreateWAL(t, wpath, wkey)
	defer func() { _ = w.Close() }()
	for i := 0; i < 4; i++ {
		if err := w.Append(walCrashRecord(i, []byte("trunc-"+itoa(i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	arch := wpath + ".arch"
	if err := w.CompactToSigned(arch, sk); err != nil {
		t.Fatalf("CompactToSigned: %v", err)
	}
	adata, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile arch: %v", err)
	}
	if err := os.WriteFile(arch, adata[:len(adata)-1], 0o600); err != nil {
		t.Fatalf("WriteFile arch trunc: %v", err)
	}
	if err := VerifyArchiveAuth(arch, wkey, pub); err != errArchSize {
		t.Fatalf("arch trunc: %v", err)
	}
}

func TestMLDSAWrongPub(t *testing.T) {
	sk := testMLDSAKey(t, 0x11)
	wrong := testMLDSAKey(t, 0x22).PublicKey()
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 9)
	}
	s := mustOpenShard(t, dir, key, 4)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snap := filepath.Join(dir, "snap.c2sn")
	if err := ExportSigned(s, snap, key, sk); err != nil {
		t.Fatalf("ExportSigned: %v", err)
	}
	if err := VerifySnapshotAuth(snap, key, wrong); err != errSnapBadSig {
		t.Fatalf("snap wrong pub: %v", err)
	}

	wpath, wkey := walCrashImage(t)
	w := mustCreateWAL(t, wpath, wkey)
	defer func() { _ = w.Close() }()
	for i := 0; i < 4; i++ {
		if err := w.Append(walCrashRecord(i, []byte("wrong-"+itoa(i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	arch := wpath + ".arch"
	if err := w.CompactToSigned(arch, sk); err != nil {
		t.Fatalf("CompactToSigned: %v", err)
	}
	if err := VerifyArchiveAuth(arch, wkey, wrong); err != errArchBadSig {
		t.Fatalf("arch wrong pub: %v", err)
	}
}

func TestMLDSAUnsignedStillWorks(t *testing.T) {
	sk := testMLDSAKey(t, 0x11)
	pub := sk.PublicKey()
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 11)
	}
	s := mustOpenShard(t, dir, key, 2)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snap := filepath.Join(dir, "unsigned.c2sn")
	if err := Export(s, snap, key); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := VerifySnapshot(snap, key); err != nil {
		t.Fatalf("VerifySnapshot unsigned: %v", err)
	}
	if err := VerifySnapshotAuth(snap, key, nil); err != nil {
		t.Fatalf("VerifySnapshotAuth unsigned nil pub: %v", err)
	}
	if err := VerifySnapshotAuth(snap, key, pub); err != errSnapUnsigned {
		t.Fatalf("VerifySnapshotAuth unsigned+pub: %v", err)
	}

	wpath, wkey := walCrashImage(t)
	w := mustCreateWAL(t, wpath, wkey)
	defer func() { _ = w.Close() }()
	for i := 0; i < 4; i++ {
		if err := w.Append(walCrashRecord(i, []byte("u-"+itoa(i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	arch := wpath + ".arch"
	if err := w.CompactTo(arch); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if err := VerifyArchive(arch, wkey); err != nil {
		t.Fatalf("VerifyArchive unsigned: %v", err)
	}
	if err := VerifyArchiveAuth(arch, wkey, nil); err != nil {
		t.Fatalf("VerifyArchiveAuth unsigned nil pub: %v", err)
	}
	if err := VerifyArchiveAuth(arch, wkey, pub); err != errArchUnsigned {
		t.Fatalf("VerifyArchiveAuth unsigned+pub: %v", err)
	}

	signed := filepath.Join(dir, "signed.c2sn")
	if err := ExportSigned(s, signed, key, sk); err != nil {
		t.Fatalf("ExportSigned: %v", err)
	}
	if err := VerifySnapshot(signed, key); err != errSnapNeedPub {
		t.Fatalf("VerifySnapshot signed: %v", err)
	}
	sarch := wpath + ".signed.arch"
	w2path, w2key := walCrashImage(t)
	w2 := mustCreateWAL(t, w2path, w2key)
	defer func() { _ = w2.Close() }()
	for i := 0; i < 4; i++ {
		if err := w2.Append(walCrashRecord(i, []byte("s-"+itoa(i)))); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if err := w2.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := w2.CompactToSigned(sarch, sk); err != nil {
		t.Fatalf("CompactToSigned: %v", err)
	}
	if err := VerifyArchive(sarch, w2key); err != errArchNeedPub {
		t.Fatalf("VerifyArchive signed: %v", err)
	}
}

func TestApplySignedNeedPub(t *testing.T) {
	sk := testMLDSAKey(t, 0x11)
	pub := sk.PublicKey()
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 9)
	}
	s := mustOpenShard(t, dir, key, 4)
	defer func() { _ = s.Close() }()
	if err := s.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snap := filepath.Join(dir, "signed.c2sn")
	if err := ExportSigned(s, snap, key, sk); err != nil {
		t.Fatalf("ExportSigned: %v", err)
	}
	dest := make([]byte, shardBytes)
	if err := Apply(snap, dest, key); err != errSnapNeedPub {
		t.Fatalf("Apply signed: %v", err)
	}
	if err := ApplyAuth(snap, dest, key, nil); err != errSnapNeedPub {
		t.Fatalf("ApplyAuth nil pub: %v", err)
	}
	wrong := testMLDSAKey(t, 0x22).PublicKey()
	if err := ApplyAuth(snap, dest, key, wrong); err != errSnapBadSig {
		t.Fatalf("ApplyAuth wrong pub: %v", err)
	}
	if err := ApplyAuth(snap, dest, key, pub); err != nil {
		t.Fatalf("ApplyAuth: %v", err)
	}
	if !bytes.Equal(dest, s.pub) {
		t.Fatal("ApplyAuth tas distinct")
	}
}
