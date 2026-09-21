// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import "golang.org/x/sys/unix"

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

func (p *Pager) pageIndex(lba uint64) uint64 { return lba / pageLBAs }

func (p *Pager) verifyPage(lba uint64, buf []byte) error { return nil }

func (p *Pager) sealPage(lba uint64, buf []byte) error { return nil }

func (p *Pager) readTag(pageIdx uint64) ([16]byte, error) {
	return [16]byte{}, nil
}

func (p *Pager) writeTag(pageIdx uint64, tag [16]byte) error { return nil }
