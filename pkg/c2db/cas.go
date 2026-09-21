// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"golang.org/x/sys/unix"
)

const (
	casHashSize = 32
	casLenSize  = 4
	casHdrSize  = casHashSize + casLenSize
)

type CAS struct {
	dev   *Device
	index map[[32]byte]uint64
	next  uint64
	hdr   []byte
}

var errCASNotFound = errors.New("c2db: cas hash not found")

func CreateCAS(path string, size uint64) (*CAS, error) {
	dev, err := Create(path, size)
	if err != nil {
		return nil, err
	}
	c, err := newCAS(dev)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	return c, nil
}

func OpenCAS(path string) (*CAS, error) {
	dev, err := Open(path)
	if err != nil {
		return nil, err
	}
	c, err := newCAS(dev)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	if err := c.replay(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func newCAS(dev *Device) (*CAS, error) {
	hdr, err := mmapAnon(LBASize)
	if err != nil {
		return nil, err
	}
	return &CAS{dev: dev, index: make(map[[32]byte]uint64), hdr: hdr}, nil
}

func (c *CAS) Put(buf []byte) ([32]byte, error) {
	var zero [32]byte
	if err := c.ready(); err != nil {
		return zero, err
	}
	h := blake3archtsim.Sum256(buf)
	if _, ok := c.index[h]; ok {
		return h, nil
	}
	if uint64(len(buf)) > uint64(^uint32(0)) {
		return zero, fmt.Errorf("c2db: cas payload %d exceeds u32", len(buf))
	}
	nLBA := packedLBAs(uint32(len(buf)))
	maxLBA := c.dev.size / LBASize
	if c.next >= maxLBA || nLBA > maxLBA-c.next {
		return zero, fmt.Errorf("c2db: cas full (lba %d n %d)", c.next, nLBA)
	}
	nbytes := int(nLBA * LBASize)
	blk, err := mmapAnon(nbytes)
	if err != nil {
		return zero, err
	}
	defer unix.Munmap(blk)
	copy(blk[:casHashSize], h[:])
	binary.LittleEndian.PutUint32(blk[casHashSize:casHdrSize], uint32(len(buf)))
	copy(blk[casHdrSize:], buf)
	if err := c.dev.Write(c.next, blk); err != nil {
		return zero, err
	}
	if err := c.dev.Flush(); err != nil {
		return zero, err
	}
	c.index[h] = c.next
	c.next += nLBA
	return h, nil
}

func (c *CAS) Get(h [32]byte) ([]byte, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	lba, ok := c.index[h]
	if !ok {
		return nil, errCASNotFound
	}
	if err := c.dev.Read(lba, c.hdr); err != nil {
		return nil, err
	}
	plen := binary.LittleEndian.Uint32(c.hdr[casHashSize:casHdrSize])
	nLBA := packedLBAs(plen)
	maxLBA := c.dev.size / LBASize
	if lba >= maxLBA || nLBA > maxLBA-lba {
		return nil, fmt.Errorf("c2db: cas get lba %d len %d out of range", lba, plen)
	}
	blk := c.hdr
	if nLBA > 1 {
		var err error
		blk, err = mmapAnon(int(nLBA * LBASize))
		if err != nil {
			return nil, err
		}
		defer unix.Munmap(blk)
		if err := c.dev.Read(lba, blk); err != nil {
			return nil, err
		}
	}
	out := make([]byte, plen)
	copy(out, blk[casHdrSize:casHdrSize+int(plen)])
	return out, nil
}

func (c *CAS) Close() error {
	if err := c.ready(); err != nil {
		return err
	}
	err := c.dev.Close()
	c.dev = nil
	if c.hdr != nil {
		_ = unix.Munmap(c.hdr)
		c.hdr = nil
	}
	c.index = nil
	return err
}

func (c *CAS) ready() error {
	if c == nil || c.dev == nil {
		return unix.EBADF
	}
	return c.dev.checkReady()
}

func (c *CAS) replay() error {
	maxLBA := c.dev.size / LBASize
	c.next = 0
	c.index = make(map[[32]byte]uint64)
	for lba := uint64(0); lba < maxLBA; {
		if err := c.dev.Read(lba, c.hdr); err != nil {
			return err
		}
		plen := binary.LittleEndian.Uint32(c.hdr[casHashSize:casHdrSize])
		if casVacant(c.hdr, plen) {
			break
		}
		nLBA := packedLBAs(plen)
		if lba >= maxLBA || nLBA > maxLBA-lba {
			return fmt.Errorf("c2db: cas replay lba %d len %d out of range", lba, plen)
		}
		var h [32]byte
		copy(h[:], c.hdr[:casHashSize])
		c.index[h] = lba
		lba += nLBA
		c.next = lba
	}
	return nil
}

func packedLBAs(payloadLen uint32) uint64 {
	need := casHdrSize + uint64(payloadLen)
	return (need + LBASize - 1) / LBASize
}

func casVacant(hdr []byte, plen uint32) bool {
	return plen == 0 && hdr[0] == 0 && hdr[1] == 0 && hdr[2] == 0 && hdr[3] == 0
}
