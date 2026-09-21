// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/mldsa"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"os"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
)

const (
	ArchMagic      = uint32(0x43324152)
	ArchFormatVer  = uint32(1)
	ArchSigSize    = 64
	ArchTagSize    = 16
	ArchHdrSize    = 128
	SigTypeNone    = 0
	SigTypeMLDSA44 = 1
)

const (
	archMagicOff   = 0
	archFmtOff     = 4
	archTopoOff    = 8
	archLsnLoOff   = 12
	archLsnHiOff   = 20
	archNBlocksOff = 28
	archSigTypeOff = 36
	archSigLenOff  = 38
	archSigOff     = 40
	archTagOff     = ArchHdrSize - ArchTagSize
)

var (
	errArchMagic    = errors.New("c2db: archive bad magic")
	errArchFormat   = errors.New("c2db: archive bad format")
	errArchSize     = errors.New("c2db: archive size mismatch")
	errArchBadTag   = errors.New("c2db: archive poly1305 tag mismatch")
	errArchNeedEnc  = errors.New("c2db: archive encrypted, enc key required")
	errArchNeedPub  = errors.New("c2db: archive signed, public key required")
	errArchBadSig   = errors.New("c2db: archive ML-DSA signature mismatch")
	errArchUnsigned = errors.New("c2db: archive unsigned")
)

func writeWALArchive(path string, key [32]byte, topoVer uint32, lsnLo, lsnHi, nblocks uint64, readBlock func(i uint64) error, block []byte, encKey *[32]byte, sk *mldsa.PrivateKey) error {
	hdr := make([]byte, ArchHdrSize)
	binary.LittleEndian.PutUint32(hdr[archMagicOff:], ArchMagic)
	binary.LittleEndian.PutUint32(hdr[archFmtOff:], ArchFormatVer)
	binary.LittleEndian.PutUint32(hdr[archTopoOff:], topoVer)
	binary.LittleEndian.PutUint64(hdr[archLsnLoOff:], lsnLo)
	binary.LittleEndian.PutUint64(hdr[archLsnHiOff:], lsnHi)
	binary.LittleEndian.PutUint64(hdr[archNBlocksOff:], nblocks)
	if sk != nil {
		binary.LittleEndian.PutUint16(hdr[archSigTypeOff:], SigTypeMLDSA44)
		binary.LittleEndian.PutUint16(hdr[archSigLenOff:], uint16(mldsa.MLDSA44SignatureSize))
	} else {
		binary.LittleEndian.PutUint16(hdr[archSigTypeOff:], SigTypeNone)
		binary.LittleEndian.PutUint16(hdr[archSigLenOff:], ArchSigSize)
	}
	if encKey != nil {
		if err := fillEncNonce(hdr[encPadOff : encPadOff+EncNoncePadSize]); err != nil {
			return err
		}
	}
	if sk != nil {
		return writeWALArchiveSigned(path, key, hdr, nblocks, readBlock, block, encKey, sk)
	}
	sealArchHeader(hdr, key)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(hdr); err != nil {
		_ = f.Close()
		return err
	}
	var stream interface{ XORKeyStream(dst, src []byte) }
	var encBuf []byte
	var macCtx c2poly1305.Crypto_poly1305_ctx
	if encKey != nil {
		n := ietfNonce(hdr[encPadOff : encPadOff+EncNoncePadSize])
		c, err := newChaCha(encKey[:], n[:])
		if err != nil {
			_ = f.Close()
			return err
		}
		stream = c
		encBuf = make([]byte, LBASize)
		c2poly1305.Crypto_poly1305_init(&macCtx, key[:])
	}
	for i := uint64(0); i < nblocks; i++ {
		if err := readBlock(i); err != nil {
			_ = f.Close()
			return err
		}
		out := block
		if stream != nil {
			stream.XORKeyStream(encBuf, block)
			out = encBuf
			c2poly1305.Crypto_poly1305_update(&macCtx, out, uint64(len(out)))
		}
		if _, err := f.Write(out); err != nil {
			_ = f.Close()
			return err
		}
	}
	if stream != nil {
		var tag [EncBodyTagSize]byte
		c2poly1305.Crypto_poly1305_final(&macCtx, tag[:])
		if _, err := f.Write(tag[:]); err != nil {
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

func writeWALArchiveSigned(path string, key [32]byte, hdr []byte, nblocks uint64, readBlock func(i uint64) error, block []byte, encKey *[32]byte, sk *mldsa.PrivateKey) error {
	bodyCap := nblocks * uint64(LBASize)
	if encKey != nil {
		bodyCap += uint64(EncBodyTagSize)
	}
	body := make([]byte, 0, bodyCap)
	var stream interface{ XORKeyStream(dst, src []byte) }
	var encBuf []byte
	var macCtx c2poly1305.Crypto_poly1305_ctx
	if encKey != nil {
		n := ietfNonce(hdr[encPadOff : encPadOff+EncNoncePadSize])
		c, err := newChaCha(encKey[:], n[:])
		if err != nil {
			return err
		}
		stream = c
		encBuf = make([]byte, LBASize)
		c2poly1305.Crypto_poly1305_init(&macCtx, key[:])
	}
	for i := uint64(0); i < nblocks; i++ {
		if err := readBlock(i); err != nil {
			return err
		}
		out := block
		if stream != nil {
			stream.XORKeyStream(encBuf, block)
			out = encBuf
			c2poly1305.Crypto_poly1305_update(&macCtx, out, uint64(len(out)))
		}
		body = append(body, out...)
	}
	if stream != nil {
		var tag [EncBodyTagSize]byte
		c2poly1305.Crypto_poly1305_final(&macCtx, tag[:])
		body = append(body, tag[:]...)
	}
	sealArchHeader(hdr, key)
	prefix := make([]byte, 0, len(hdr)+len(body))
	prefix = append(prefix, hdr...)
	prefix = append(prefix, body...)
	sig, err := sk.SignDeterministic(prefix, nil)
	if err != nil {
		return err
	}
	if len(sig) != mldsa.MLDSA44SignatureSize {
		return errArchFormat
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
	if _, err := f.Write(sig); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func VerifyArchive(path string, key [32]byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := verifyArchiveData(data, key); err != nil {
		return err
	}
	if binary.LittleEndian.Uint16(data[archSigTypeOff:]) == SigTypeMLDSA44 {
		return errArchNeedPub
	}
	return nil
}

func VerifyArchiveAuth(path string, key [32]byte, pub *mldsa.PublicKey) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := verifyArchiveData(data, key); err != nil {
		return err
	}
	sigType := binary.LittleEndian.Uint16(data[archSigTypeOff:])
	if sigType == SigTypeNone {
		if pub != nil {
			return errArchUnsigned
		}
		return nil
	}
	if pub == nil {
		return errArchNeedPub
	}
	prefix := data[:len(data)-mldsa.MLDSA44SignatureSize]
	trailer := data[len(data)-mldsa.MLDSA44SignatureSize:]
	if err := mldsa.Verify(pub, prefix, trailer, nil); err != nil {
		return errArchBadSig
	}
	return nil
}

func OpenArchive(path string, key [32]byte, encKey *[32]byte) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := verifyArchiveData(data, key); err != nil {
		return nil, err
	}
	hdr := data[:ArchHdrSize]
	rest := archiveBody(data)
	if !headerEncrypted(hdr) {
		out := make([]byte, len(rest))
		copy(out, rest)
		return out, nil
	}
	if encKey == nil {
		return nil, errArchNeedEnc
	}
	ct := rest[:len(rest)-EncBodyTagSize]
	pt := make([]byte, len(ct))
	n := ietfNonce(hdr[encPadOff : encPadOff+EncNoncePadSize])
	if err := xorChaCha(pt, ct, encKey[:], n[:]); err != nil {
		return nil, err
	}
	if err := verifyPlainWALBlocks(pt, key); err != nil {
		return nil, err
	}
	return pt, nil
}

func archiveBody(data []byte) []byte {
	rest := data[ArchHdrSize:]
	if binary.LittleEndian.Uint16(data[archSigTypeOff:]) == SigTypeMLDSA44 {
		rest = rest[:len(rest)-mldsa.MLDSA44SignatureSize]
	}
	return rest
}

func verifyArchiveData(data []byte, key [32]byte) error {
	if uint64(len(data)) < uint64(ArchHdrSize) {
		return errArchSize
	}
	hdr := data[:ArchHdrSize]
	if binary.LittleEndian.Uint32(hdr[archMagicOff:]) != ArchMagic {
		return errArchMagic
	}
	if binary.LittleEndian.Uint32(hdr[archFmtOff:]) != ArchFormatVer {
		return errArchFormat
	}
	sigType := binary.LittleEndian.Uint16(hdr[archSigTypeOff:])
	sigLen := binary.LittleEndian.Uint16(hdr[archSigLenOff:])
	switch sigType {
	case SigTypeNone:
		if sigLen != ArchSigSize {
			return errArchFormat
		}
		sig := hdr[archSigOff : archSigOff+ArchSigSize]
		for i := 0; i < len(sig); i++ {
			if sig[i] != 0 {
				return errArchFormat
			}
		}
		if !verifyArchHeader(hdr, key) {
			return errArchBadTag
		}
		return verifyArchiveBody(data[ArchHdrSize:], hdr, key)
	case SigTypeMLDSA44:
		if !verifyArchHeader(hdr, key) {
			return errArchBadTag
		}
		sig := hdr[archSigOff : archSigOff+ArchSigSize]
		for i := 0; i < len(sig); i++ {
			if sig[i] != 0 {
				return errArchFormat
			}
		}
		if sigLen != uint16(mldsa.MLDSA44SignatureSize) {
			return errArchFormat
		}
		if uint64(len(data)) < uint64(ArchHdrSize)+uint64(mldsa.MLDSA44SignatureSize) {
			return errArchSize
		}
		return verifyArchiveBody(data[ArchHdrSize:len(data)-mldsa.MLDSA44SignatureSize], hdr, key)
	default:
		return errArchFormat
	}
}

func verifyArchiveBody(rest []byte, hdr []byte, key [32]byte) error {
	nblocks := binary.LittleEndian.Uint64(hdr[archNBlocksOff:])
	if headerEncrypted(hdr) {
		ct, ok := verifyBodyMAC(rest, key)
		if uint64(len(rest)) < uint64(EncBodyTagSize) {
			return errArchSize
		}
		if !ok {
			return errArchBadTag
		}
		if uint64(len(ct))%uint64(LBASize) != 0 {
			return errArchSize
		}
		if uint64(len(ct))/uint64(LBASize) != nblocks {
			return errArchSize
		}
		return nil
	}
	if uint64(len(rest))%uint64(LBASize) != 0 {
		return errArchSize
	}
	if uint64(len(rest))/uint64(LBASize) != nblocks {
		return errArchSize
	}
	return verifyPlainWALBlocks(rest, key)
}

func verifyPlainWALBlocks(body []byte, key [32]byte) error {
	nblocks := uint64(len(body)) / uint64(LBASize)
	off := 0
	for i := uint64(0); i < nblocks; i++ {
		blk := body[off : off+LBASize]
		if binary.LittleEndian.Uint32(blk[:4]) != WALMagic {
			return errWALMagic
		}
		if !verifyWAL(blk, key) {
			return errWALBadTag
		}
		off += LBASize
	}
	return nil
}

func sealArchHeader(hdr []byte, key [32]byte) {
	c2poly1305.Crypto_poly1305(hdr[archTagOff:ArchHdrSize], hdr[:archTagOff], uint64(archTagOff), key[:])
}

func verifyArchHeader(hdr []byte, key [32]byte) bool {
	var tag [ArchTagSize]byte
	c2poly1305.Crypto_poly1305(tag[:], hdr[:archTagOff], uint64(archTagOff), key[:])
	return subtle.ConstantTimeCompare(tag[:], hdr[archTagOff:ArchHdrSize]) == 1
}
