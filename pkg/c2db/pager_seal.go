// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/subtle"
	"encoding/binary"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
	"golang.org/x/sys/unix"
)

func (p *Pager) SetSeal(key [32]byte, tags *Device, shard uint16) error {
	if p == nil {
		return unix.EBADF
	}
	blk, err := mmapAnon(int(LBASize))
	if err != nil {
		return err
	}
	p.sealKey = key
	p.sealShard = shard
	p.tags = tags
	p.tagBlk = blk
	p.sealed = true
	return nil
}

func (p *Pager) pageIndex(lba uint64) uint64 {
	return lba / pageLBAs
}

func (p *Pager) verifyPage(lba uint64, buf []byte) error {
	if p == nil || !p.sealed || p.tags == nil {
		return nil
	}
	idx := p.pageIndex(lba)
	tag, err := p.readTag(idx)
	if err != nil {
		return err
	}
	var zeroTag [16]byte
	if subtle.ConstantTimeCompare(tag[:], zeroTag[:]) == 1 {
		// La tolérance d'un tag nul est strictement réservée aux pages non allouées.
		// La page racine (index 0) est toujours allouée : un tag nul y est fatal.
		if idx == 0 {
			return errPageSeal
		}
		typ := buf[BT_TypeOffset]
		nslots := binary.LittleEndian.Uint16(buf[BT_NSlotsOffset : BT_NSlotsOffset+2])
		if typ == 0 && nslots == 0 {
			return nil
		}
		return errPageSeal
	}
	derivedKey := DerivePageSealKey(p.sealKey, p.sealShard, idx, buf)
	var got [16]byte
	c2poly1305.Crypto_poly1305(got[:], buf, uint64(len(buf)), derivedKey[:])
	if subtle.ConstantTimeCompare(got[:], tag[:]) != 1 {
		return errPageSeal
	}
	return nil
}

func (p *Pager) sealPage(lba uint64, buf []byte) error {
	if p == nil || !p.sealed || p.tags == nil {
		return nil
	}
	idx := p.pageIndex(lba)
	derivedKey := DerivePageSealKey(p.sealKey, p.sealShard, idx, buf)
	var tag [16]byte
	c2poly1305.Crypto_poly1305(tag[:], buf, uint64(len(buf)), derivedKey[:])
	return p.writeTag(idx, tag)
}

func (p *Pager) loadTagBlk(blk uint64) error {
	if p.tagBlkValid && p.tagBlkLBA == blk {
		return nil
	}
	if err := p.flushTagBlk(); err != nil {
		return err
	}
	if err := p.tags.Read(blk, p.tagBlk); err != nil {
		return err
	}
	p.tagBlkLBA = blk
	p.tagBlkValid = true
	p.tagBlkDirty = false
	return nil
}

func (p *Pager) flushTagBlk() error {
	if !p.tagBlkDirty || !p.tagBlkValid {
		return nil
	}
	if err := p.tags.Write(p.tagBlkLBA, p.tagBlk); err != nil {
		return err
	}
	p.tagBlkDirty = false
	return nil
}

func (p *Pager) flushTags() error {
	if p == nil || !p.sealed || p.tags == nil {
		return nil
	}
	if err := p.flushTagBlk(); err != nil {
		return err
	}
	return p.tags.Flush()
}

func (p *Pager) readTag(pageIdx uint64) ([16]byte, error) {
	var tag [16]byte
	blk := tagHeaderLBAs + pageIdx/tagsPerLBA
	off := (pageIdx % tagsPerLBA) * 16
	if err := p.loadTagBlk(blk); err != nil {
		return tag, err
	}
	copy(tag[:], p.tagBlk[off:off+16])
	return tag, nil
}

func (p *Pager) writeTag(pageIdx uint64, tag [16]byte) error {
	blk := tagHeaderLBAs + pageIdx/tagsPerLBA
	off := (pageIdx % tagsPerLBA) * 16
	if err := p.loadTagBlk(blk); err != nil {
		return err
	}
	copy(p.tagBlk[off:off+16], tag[:])
	p.tagBlkDirty = true
	return nil
}
