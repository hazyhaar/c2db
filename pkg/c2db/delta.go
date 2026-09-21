// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"

	"github.com/hazyhaar/c2db/pkg/c2lz4"
)

const (
	deltaLBACount       = 4
	deltaHdrSize        = 12
	deltaLBAMax         = 1 << 18
	deltaEntHdr         = 5
	deltaModeFullPage   = 0
	deltaModeByteDelta  = 1
	deltaModeContentRef = 2
)

var (
	deltaCRCTable  = crc32.MakeTable(crc32.Castagnoli)
	errDeltaPage   = errors.New("c2db: delta page size")
	errDeltaTrunc  = errors.New("c2db: delta pack truncated")
	errDeltaSrcCRC = errors.New("c2db: delta src crc mismatch")
	errDeltaDstCRC = errors.New("c2db: delta dst crc mismatch")
	errDeltaMode   = errors.New("c2db: delta mode")
	errDeltaLBA    = errors.New("c2db: delta lba")
	errDeltaNoCAS  = errors.New("c2db: delta content_ref without cas")
	errDeltaRef    = errors.New("c2db: delta content_ref size")
)

func EncodeDelta(src, dst []byte, cas *CAS) ([]byte, error) {
	if len(src) == 0 || len(src)%LBASize != 0 || len(src) != len(dst) {
		return nil, errDeltaPage
	}
	lbaCount := len(src) / LBASize
	if lbaCount > deltaLBAMax {
		return nil, errDeltaPage
	}
	pack := make([]byte, deltaHdrSize, deltaHdrSize+lbaCount*(deltaEntHdr+LBASize))
	binary.LittleEndian.PutUint32(pack[0:4], crc32.Checksum(src, deltaCRCTable))
	binary.LittleEndian.PutUint32(pack[4:8], crc32.Checksum(dst, deltaCRCTable))
	var n uint32
	var idx [4]byte
	for i := 0; i < lbaCount; i++ {
		off := i * LBASize
		if bytes.Equal(src[off:off+LBASize], dst[off:off+LBASize]) {
			continue
		}
		binary.LittleEndian.PutUint32(idx[:], uint32(i))
		if cas != nil {
			h, err := cas.Put(dst[off : off+LBASize])
			if err != nil {
				return nil, err
			}
			pack = append(pack, idx[0], idx[1], idx[2], idx[3], byte(deltaModeContentRef))
			pack = append(pack, h[:]...)
		} else {
			pack = append(pack, idx[0], idx[1], idx[2], idx[3], byte(deltaModeFullPage))
			pack = append(pack, dst[off:off+LBASize]...)
		}
		n++
	}
	binary.LittleEndian.PutUint32(pack[8:12], n)
	return pack, nil
}

func ApplyDelta(src, pack []byte, cas *CAS) ([]byte, error) {
	if len(src) == 0 || len(src)%LBASize != 0 {
		return nil, errDeltaPage
	}
	lbaCount := len(src) / LBASize
	if lbaCount > deltaLBAMax {
		return nil, errDeltaPage
	}
	if len(pack) < deltaHdrSize {
		return nil, errDeltaTrunc
	}
	srcCRC := binary.LittleEndian.Uint32(pack[0:4])
	dstCRC := binary.LittleEndian.Uint32(pack[4:8])
	n := binary.LittleEndian.Uint32(pack[8:12])
	if crc32.Checksum(src, deltaCRCTable) != srcCRC {
		return nil, errDeltaSrcCRC
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	off := deltaHdrSize
	for i := uint32(0); i < n; i++ {
		if off > len(pack) || len(pack)-off < deltaEntHdr {
			return nil, errDeltaTrunc
		}
		lba := int(binary.LittleEndian.Uint32(pack[off : off+4]))
		mode := pack[off+4]
		off += deltaEntHdr
		if lba >= lbaCount {
			return nil, errDeltaLBA
		}
		dstOff := lba * LBASize
		switch mode {
		case deltaModeFullPage:
			if off > len(pack) || len(pack)-off < LBASize {
				return nil, errDeltaTrunc
			}
			copy(dst[dstOff:dstOff+LBASize], pack[off:off+LBASize])
			off += LBASize
		case deltaModeContentRef:
			if cas == nil {
				return nil, errDeltaNoCAS
			}
			if off > len(pack) || len(pack)-off < casHashSize {
				return nil, errDeltaTrunc
			}
			var h [32]byte
			copy(h[:], pack[off:off+casHashSize])
			off += casHashSize
			got, err := cas.Get(h)
			if err != nil {
				return nil, err
			}
			if len(got) != LBASize {
				return nil, errDeltaRef
			}
			copy(dst[dstOff:dstOff+LBASize], got)
		default:
			return nil, errDeltaMode
		}
	}
	if crc32.Checksum(dst, deltaCRCTable) != dstCRC {
		return nil, errDeltaDstCRC
	}
	return dst, nil
}

func EncodeDeltaLZ4(src, dst []byte, cas *CAS) ([]byte, error) {
	pack, err := EncodeDelta(src, dst, cas)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 4+len(pack)*2+65536)
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(pack)))
	n := c2lz4.Compress(pack, buf[4:])
	if n <= 0 {
		return nil, errDeltaPage
	}
	return buf[:4+n], nil
}

func ApplyDeltaLZ4(src, packed []byte, cas *CAS) ([]byte, error) {
	if len(packed) < 4 {
		return nil, errDeltaTrunc
	}
	rawLen := binary.LittleEndian.Uint32(packed[:4])
	if rawLen == 0 {
		return nil, errDeltaTrunc
	}
	raw := make([]byte, rawLen)
	n, ok := c2lz4.Decompress(packed[4:], raw)
	if !ok || n != int(rawLen) {
		return nil, errDeltaTrunc
	}
	return ApplyDelta(src, raw[:n], cas)
}
