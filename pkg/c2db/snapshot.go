// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/mldsa"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"os"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
)

const (
	SnapMagic     = uint32(0x4332534E)
	SnapFormatVer = uint32(1)
	SnapSigSize   = 64
	SnapTagSize   = 16
	SnapHdrSize   = 128
)

const (
	snapMagicOff   = 0
	snapFmtOff     = 4
	snapTopoOff    = 8
	snapIDOff      = 12
	snapNEntOff    = 28
	snapSigTypeOff = 36
	snapSigLenOff  = 38
	snapSigOff     = 40
	snapTagOff     = SnapHdrSize - SnapTagSize
)

type SnapshotHeader struct {
	Magic     uint32
	FormatVer uint32
	TopoVer   uint32
	SnapID    [16]byte
	NEntries  uint64
	SigType   uint16
	SigLen    uint16
	Sig       [SnapSigSize]byte
	Tag       [SnapTagSize]byte
}

var (
	errSnapMagic    = errors.New("c2db: snapshot bad magic")
	errSnapFormat   = errors.New("c2db: snapshot bad format")
	errSnapSize     = errors.New("c2db: snapshot size mismatch")
	errSnapBadTag   = errors.New("c2db: snapshot poly1305 tag mismatch")
	errSnapPage     = errors.New("c2db: snapshot page size")
	errSnapNeedEnc  = errors.New("c2db: snapshot encrypted, enc key required")
	errSnapNeedPub  = errors.New("c2db: snapshot signed, public key required")
	errSnapBadSig   = errors.New("c2db: snapshot ML-DSA signature mismatch")
	errSnapUnsigned = errors.New("c2db: snapshot unsigned")
)

func (s *Shard) Freeze() ([16]byte, error) {
	var zero [16]byte
	if err := s.ready(); err != nil {
		return zero, err
	}
	recs, err := s.wal.Replay()
	if err != nil {
		return zero, err
	}
	if len(recs) > 0 {
		return recs[len(recs)-1].ID, nil
	}
	id, err := NewID(uint64(time.Now().UnixNano()), s.id, s.counter)
	if err != nil {
		return zero, err
	}
	s.counter++
	return id, nil
}

func Export(s *Shard, dstPath string, key [32]byte) error {
	return exportSnap(s, dstPath, key, nil, nil)
}

func ExportEnc(s *Shard, dstPath string, key, encKey [32]byte) error {
	return exportSnap(s, dstPath, key, &encKey, nil)
}

func ExportSigned(s *Shard, dstPath string, key [32]byte, sk *mldsa.PrivateKey) error {
	return exportSnap(s, dstPath, key, nil, sk)
}

func ExportEncSigned(s *Shard, dstPath string, key, encKey [32]byte, sk *mldsa.PrivateKey) error {
	return exportSnap(s, dstPath, key, &encKey, sk)
}

func exportSnap(s *Shard, dstPath string, key [32]byte, encKey *[32]byte, sk *mldsa.PrivateKey) error {
	if err := s.ready(); err != nil {
		return err
	}
	if uint64(len(s.pub)) < shardBytes {
		return errSnapPage
	}
	id, err := s.Freeze()
	if err != nil {
		return err
	}
	src := make([]byte, len(s.pub))
	pack, err := EncodeDelta(src, s.pub, nil)
	if err != nil {
		return err
	}
	var h SnapshotHeader
	h.Magic = SnapMagic
	h.FormatVer = SnapFormatVer
	h.SnapID = id
	h.NEntries = uint64(binary.LittleEndian.Uint32(pack[8:12]))
	h.SigType = SigTypeNone
	h.SigLen = SnapSigSize
	if sk != nil {
		h.SigType = SigTypeMLDSA44
		h.SigLen = uint16(mldsa.MLDSA44SignatureSize)
	}
	hdr, err := encodeSnapHeader(h, key, encKey)
	if err != nil {
		return err
	}
	body := pack
	var bodyTag []byte
	if encKey != nil {
		ct := make([]byte, len(pack))
		n := ietfNonce(hdr[encPadOff : encPadOff+EncNoncePadSize])
		if err := xorChaCha(ct, pack, encKey[:], n[:]); err != nil {
			return err
		}
		body = ct
		bodyTag = make([]byte, EncBodyTagSize)
		sealBodyMAC(bodyTag, ct, key)
	}

	var sig []byte
	if sk != nil {
		prefix := make([]byte, 0, len(hdr)+len(body)+len(bodyTag))
		prefix = append(prefix, hdr...)
		prefix = append(prefix, body...)
		prefix = append(prefix, bodyTag...)
		sig, err = sk.SignDeterministic(prefix, nil)
		if err != nil {
			return err
		}
		if len(sig) != mldsa.MLDSA44SignatureSize {
			return errSnapFormat
		}
	}

	f, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(hdr); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if len(bodyTag) > 0 {
		if _, err := f.Write(bodyTag); err != nil {
			_ = f.Close()
			return err
		}
	}
	if len(sig) > 0 {
		if _, err := f.Write(sig); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func Apply(srcPath string, destPub []byte, key [32]byte) error {
	return applySnap(srcPath, destPub, key, nil, false)
}

func ApplyEnc(srcPath string, destPub []byte, key, encKey [32]byte) error {
	return applySnap(srcPath, destPub, key, &encKey, false)
}

func ApplyAuth(srcPath string, destPub []byte, key [32]byte, pub *mldsa.PublicKey) error {
	if pub == nil {
		return errSnapNeedPub
	}
	if err := VerifySnapshotAuth(srcPath, key, pub); err != nil {
		return err
	}
	return applySnap(srcPath, destPub, key, nil, true)
}

func ApplyEncAuth(srcPath string, destPub []byte, key, encKey [32]byte, pub *mldsa.PublicKey) error {
	if pub == nil {
		return errSnapNeedPub
	}
	if err := VerifySnapshotAuth(srcPath, key, pub); err != nil {
		return err
	}
	return applySnap(srcPath, destPub, key, &encKey, true)
}

func applySnap(srcPath string, destPub []byte, key [32]byte, encKey *[32]byte, signedOK bool) error {
	if uint64(len(destPub)) < shardBytes {
		return errSnapPage
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	if err := verifySnapData(data, key); err != nil {
		return err
	}
	if binary.LittleEndian.Uint16(data[snapSigTypeOff:]) == SigTypeMLDSA44 && !signedOK {
		return errSnapNeedPub
	}
	body := snapBody(data)
	if headerEncrypted(data[:SnapHdrSize]) {
		if encKey == nil {
			return errSnapNeedEnc
		}
		ct := body[:len(body)-EncBodyTagSize]
		pt := make([]byte, len(ct))
		n := ietfNonce(data[encPadOff : encPadOff+EncNoncePadSize])
		if err := xorChaCha(pt, ct, encKey[:], n[:]); err != nil {
			return err
		}
		body = pt
	}
	zeros := make([]byte, len(destPub))
	dst, err := ApplyDelta(zeros, body, nil)
	if err != nil {
		return err
	}
	copy(destPub, dst)
	return nil
}

func VerifySnapshot(path string, key [32]byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := verifySnapData(data, key); err != nil {
		return err
	}
	if binary.LittleEndian.Uint16(data[snapSigTypeOff:]) == SigTypeMLDSA44 {
		return errSnapNeedPub
	}
	return nil
}

func VerifySnapshotAuth(path string, key [32]byte, pub *mldsa.PublicKey) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := verifySnapData(data, key); err != nil {
		return err
	}
	sigType := binary.LittleEndian.Uint16(data[snapSigTypeOff:])
	if sigType == SigTypeNone {
		if pub != nil {
			return errSnapUnsigned
		}
		return nil
	}
	if pub == nil {
		return errSnapNeedPub
	}
	prefix := data[:len(data)-mldsa.MLDSA44SignatureSize]
	trailer := data[len(data)-mldsa.MLDSA44SignatureSize:]
	if err := mldsa.Verify(pub, prefix, trailer, nil); err != nil {
		return errSnapBadSig
	}
	return nil
}

func encodeSnapHeader(h SnapshotHeader, key [32]byte, encKey *[32]byte) ([]byte, error) {
	hdr := make([]byte, SnapHdrSize)
	binary.LittleEndian.PutUint32(hdr[snapMagicOff:], h.Magic)
	binary.LittleEndian.PutUint32(hdr[snapFmtOff:], h.FormatVer)
	binary.LittleEndian.PutUint32(hdr[snapTopoOff:], h.TopoVer)
	copy(hdr[snapIDOff:snapIDOff+16], h.SnapID[:])
	binary.LittleEndian.PutUint64(hdr[snapNEntOff:], h.NEntries)
	binary.LittleEndian.PutUint16(hdr[snapSigTypeOff:], h.SigType)
	binary.LittleEndian.PutUint16(hdr[snapSigLenOff:], h.SigLen)
	copy(hdr[snapSigOff:snapSigOff+SnapSigSize], h.Sig[:])
	if encKey != nil {
		if err := fillEncNonce(hdr[encPadOff : encPadOff+EncNoncePadSize]); err != nil {
			return nil, err
		}
	}
	sealSnapHeader(hdr, key)
	return hdr, nil
}

func snapBody(data []byte) []byte {
	rest := data[SnapHdrSize:]
	if binary.LittleEndian.Uint16(data[snapSigTypeOff:]) == SigTypeMLDSA44 {
		rest = rest[:len(rest)-mldsa.MLDSA44SignatureSize]
	}
	return rest
}

func verifySnapData(data []byte, key [32]byte) error {
	if uint64(len(data)) < uint64(SnapHdrSize) {
		return errSnapSize
	}
	hdr := data[:SnapHdrSize]
	if binary.LittleEndian.Uint32(hdr[snapMagicOff:]) != SnapMagic {
		return errSnapMagic
	}
	if binary.LittleEndian.Uint32(hdr[snapFmtOff:]) != SnapFormatVer {
		return errSnapFormat
	}
	sigType := binary.LittleEndian.Uint16(hdr[snapSigTypeOff:])
	sigLen := binary.LittleEndian.Uint16(hdr[snapSigLenOff:])
	switch sigType {
	case SigTypeNone:
		if sigLen != SnapSigSize {
			return errSnapFormat
		}
		sig := hdr[snapSigOff : snapSigOff+SnapSigSize]
		for i := 0; i < len(sig); i++ {
			if sig[i] != 0 {
				return errSnapFormat
			}
		}
		if !verifySnapHeader(hdr, key) {
			return errSnapBadTag
		}
		return verifySnapBody(data[SnapHdrSize:], hdr, key)
	case SigTypeMLDSA44:
		if !verifySnapHeader(hdr, key) {
			return errSnapBadTag
		}
		sig := hdr[snapSigOff : snapSigOff+SnapSigSize]
		for i := 0; i < len(sig); i++ {
			if sig[i] != 0 {
				return errSnapFormat
			}
		}
		if sigLen != uint16(mldsa.MLDSA44SignatureSize) {
			return errSnapFormat
		}
		if uint64(len(data)) < uint64(SnapHdrSize)+uint64(mldsa.MLDSA44SignatureSize) {
			return errSnapSize
		}
		return verifySnapBody(data[SnapHdrSize:len(data)-mldsa.MLDSA44SignatureSize], hdr, key)
	default:
		return errSnapFormat
	}
}

func verifySnapBody(rest []byte, hdr []byte, key [32]byte) error {
	if headerEncrypted(hdr) {
		if uint64(len(rest)) < uint64(EncBodyTagSize) {
			return errSnapSize
		}
		if _, ok := verifyBodyMAC(rest, key); !ok {
			return errSnapBadTag
		}
	}
	return nil
}

func sealSnapHeader(hdr []byte, key [32]byte) {
	c2poly1305.Crypto_poly1305(hdr[snapTagOff:SnapHdrSize], hdr[:snapTagOff], uint64(snapTagOff), key[:])
}

func verifySnapHeader(hdr []byte, key [32]byte) bool {
	var tag [SnapTagSize]byte
	c2poly1305.Crypto_poly1305(tag[:], hdr[:snapTagOff], uint64(snapTagOff), key[:])
	return subtle.ConstantTimeCompare(tag[:], hdr[snapTagOff:SnapHdrSize]) == 1
}
