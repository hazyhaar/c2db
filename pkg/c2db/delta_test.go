// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestDeltaIdentical(t *testing.T) {
	src := deltaFill(0xA5)
	dst := append([]byte(nil), src...)
	pack, err := EncodeDelta(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDelta: %v", err)
	}
	if len(pack) != deltaHdrSize {
		t.Fatalf("pack len %d, want %d", len(pack), deltaHdrSize)
	}
	if n := binary.LittleEndian.Uint32(pack[8:12]); n != 0 {
		t.Fatalf("nentries=%d, want 0", n)
	}
	got, err := ApplyDelta(src, pack, nil)
	if err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("Apply reconstitue une page distincte")
	}
}

func TestDeltaOneLBA(t *testing.T) {
	src := deltaFill(0xA5)
	dst := append([]byte(nil), src...)
	const lba = 2
	for i := lba * LBASize; i < (lba+1)*LBASize; i++ {
		dst[i] = 0x5A
	}
	pack, err := EncodeDelta(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDelta: %v", err)
	}
	if n := binary.LittleEndian.Uint32(pack[8:12]); n != 1 {
		t.Fatalf("nentries=%d, want 1", n)
	}
	if got := binary.LittleEndian.Uint32(pack[deltaHdrSize : deltaHdrSize+4]); got != lba {
		t.Fatalf("lba=%d, want %d", got, lba)
	}
	if pack[deltaHdrSize+4] != deltaModeFullPage {
		t.Fatalf("mode=%d, want %d (full_page)", pack[deltaHdrSize+4], deltaModeFullPage)
	}
	if len(pack) != deltaHdrSize+deltaEntHdr+LBASize {
		t.Fatalf("pack len %d, want %d", len(pack), deltaHdrSize+deltaEntHdr+LBASize)
	}
	if !bytes.Equal(pack[deltaHdrSize+deltaEntHdr:], dst[lba*LBASize:(lba+1)*LBASize]) {
		t.Fatalf("payload full_page distinct du LBA dst")
	}
	got, err := ApplyDelta(src, pack, nil)
	if err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("Apply reconstitue une page distincte")
	}
}

func TestDeltaTruncated(t *testing.T) {
	src := deltaFill(0x11)
	dst := append([]byte(nil), src...)
	dst[0] = 0x22
	pack, err := EncodeDelta(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDelta: %v", err)
	}
	if _, err := ApplyDelta(src, nil, nil); !errors.Is(err, errDeltaTrunc) {
		t.Fatalf("pack nil: err=%v, want trunc", err)
	}
	if _, err := ApplyDelta(src, pack[:deltaHdrSize-1], nil); !errors.Is(err, errDeltaTrunc) {
		t.Fatalf("header court: err=%v, want trunc", err)
	}
	if _, err := ApplyDelta(src, pack[:deltaHdrSize], nil); !errors.Is(err, errDeltaTrunc) {
		t.Fatalf("nentries sans payload: err=%v, want trunc", err)
	}
	if _, err := ApplyDelta(src, pack[:len(pack)-1], nil); !errors.Is(err, errDeltaTrunc) {
		t.Fatalf("payload court: err=%v, want trunc", err)
	}
}

func TestDeltaSrcCRCMismatch(t *testing.T) {
	src := deltaFill(0x33)
	dst := append([]byte(nil), src...)
	dst[LBASize] = 0x44
	pack, err := EncodeDelta(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDelta: %v", err)
	}
	other := deltaFill(0x99)
	if _, err := ApplyDelta(other, pack, nil); !errors.Is(err, errDeltaSrcCRC) {
		t.Fatalf("err=%v, want src crc mismatch", err)
	}
	got, err := ApplyDelta(src, pack, nil)
	if err != nil {
		t.Fatalf("ApplyDelta nominal: %v", err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("Apply reconstitue une page distincte")
	}
}

func TestDeltaContentRef(t *testing.T) {
	cas := mustCreateCAS(t, t.TempDir()+"/c2db-delta-cas.img")
	defer func() { _ = cas.Close() }()

	src := deltaFill(0x70)
	dst := append([]byte(nil), src...)
	dst[0] = 0x71
	pack, err := EncodeDelta(src, dst, cas)
	if err != nil {
		t.Fatalf("EncodeDelta CAS: %v", err)
	}
	if n := binary.LittleEndian.Uint32(pack[8:12]); n != 1 {
		t.Fatalf("nentries=%d, want 1", n)
	}
	if gotLBA := binary.LittleEndian.Uint32(pack[deltaHdrSize : deltaHdrSize+4]); gotLBA != 0 {
		t.Fatalf("lba=%d, want 0", gotLBA)
	}
	if pack[deltaHdrSize+4] != deltaModeContentRef {
		t.Fatalf("mode=%d, want %d (content_ref)", pack[deltaHdrSize+4], deltaModeContentRef)
	}
	got, err := ApplyDelta(src, pack, cas)
	if err != nil {
		t.Fatalf("ApplyDelta CAS: %v", err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("Apply CAS reconstitue une page distincte")
	}
	if _, err := ApplyDelta(src, pack, nil); !errors.Is(err, errDeltaNoCAS) {
		t.Fatalf("Apply sans CAS: err=%v, want no cas", err)
	}
}

func TestDeltaPaddedPack(t *testing.T) {
	src := deltaFill(0x01)
	dst := append([]byte(nil), src...)
	dst[3*LBASize] = 0x02
	pack, err := EncodeDelta(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDelta: %v", err)
	}
	need := ((len(pack) + LBASize - 1) / LBASize) * LBASize
	padded := make([]byte, need)
	copy(padded, pack)
	got, err := ApplyDelta(src, padded, nil)
	if err != nil {
		t.Fatalf("ApplyDelta padded: %v", err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("Apply padded reconstitue une page distincte")
	}
}

func TestDeltaPageSize(t *testing.T) {
	src := deltaFill(0x08)
	if _, err := EncodeDelta(src[:len(src)-1], src, nil); !errors.Is(err, errDeltaPage) {
		t.Fatalf("src court: err=%v, want page size", err)
	}
	if _, err := ApplyDelta(src[:8], make([]byte, deltaHdrSize), nil); !errors.Is(err, errDeltaPage) {
		t.Fatalf("Apply src court: err=%v, want page size", err)
	}
}

func TestDeltaWide256LBA(t *testing.T) {
	n := 256
	src := make([]byte, n*LBASize)
	dst := make([]byte, n*LBASize)
	for i := range src {
		src[i] = 0x11
		dst[i] = 0x11
	}
	dst[255*LBASize] = 0x22
	pack, err := EncodeDelta(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDelta 256 LBA: %v", err)
	}
	if got := binary.LittleEndian.Uint32(pack[deltaHdrSize : deltaHdrSize+4]); got != 255 {
		t.Fatalf("lba=%d want 255", got)
	}
	out, err := ApplyDelta(src, pack, nil)
	if err != nil {
		t.Fatalf("ApplyDelta: %v", err)
	}
	if !bytes.Equal(out, dst) {
		t.Fatal("256 LBA roundtrip")
	}
}

func TestDelta300LBA(t *testing.T) {
	n := 300
	src := make([]byte, n*LBASize)
	dst := make([]byte, n*LBASize)
	dst[299*LBASize] = 0x33
	pack, err := EncodeDelta(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDelta 300: %v", err)
	}
	if got := binary.LittleEndian.Uint32(pack[deltaHdrSize : deltaHdrSize+4]); got != 299 {
		t.Fatalf("lba=%d want 299", got)
	}
	out, err := ApplyDelta(src, pack, nil)
	if err != nil || out[299*LBASize] != 0x33 {
		t.Fatalf("Apply 300: %v", err)
	}
}

func TestDeltaLZ4Roundtrip(t *testing.T) {
	src := deltaFill(0xA5)
	dst := append([]byte(nil), src...)
	dst[0] = 0x5A
	pack, err := EncodeDeltaLZ4(src, dst, nil)
	if err != nil {
		t.Fatalf("EncodeDeltaLZ4: %v", err)
	}
	out, err := ApplyDeltaLZ4(src, pack, nil)
	if err != nil {
		t.Fatalf("ApplyDeltaLZ4: %v", err)
	}
	if !bytes.Equal(out, dst) {
		t.Fatal("lz4 roundtrip")
	}
}

func deltaFill(v byte) []byte {
	p := make([]byte, deltaLBACount*LBASize)
	for i := range p {
		p[i] = v
	}
	return p
}
