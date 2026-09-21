// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	PagerSlots = 256
	PagerBytes = PagerSlots * int(pageN)
	pageLBAs   = uint64(pageN) / LBASize
)

type pageSlot struct {
	lba   uint64
	buf   []byte
	valid bool
	dirty bool
}

type Pager struct {
	dev         *Device
	slots       []pageSlot
	extraArenas [][]byte
	arena       []byte
	clock       uint64
	flushes     uint64
	sealKey     [32]byte
	sealShard   uint16
	tags        *Device
	tagBlk      []byte
	tagBlkLBA   uint64
	tagBlkValid bool
	tagBlkDirty bool
	sealed      bool
}

const (
	tagHeaderLBAs = 1
	tagsPerLBA    = LBASize / 16
	tagsFileLBAs  = tagHeaderLBAs + heapPages/tagsPerLBA
)

func NewPager(dev *Device) (*Pager, error) {
	if err := dev.checkReady(); err != nil {
		return nil, err
	}
	arena, err := mmapAnon(PagerBytes)
	if err != nil {
		return nil, err
	}
	p := &Pager{
		dev:   dev,
		arena: arena,
		slots: make([]pageSlot, PagerSlots),
	}
	for i := 0; i < PagerSlots; i++ {
		off := i * int(pageN)
		p.slots[i].buf = arena[off : off+int(pageN)]
	}
	return p, nil
}

func (p *Pager) GetPage(lba uint64) ([]byte, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if err := p.checkLBA(lba); err != nil {
		return nil, err
	}
	if i := p.find(lba); i >= 0 {
		return p.slots[i].buf, nil
	}
	i, err := p.alloc(lba)
	if err != nil {
		return nil, err
	}
	if err := p.dev.Read(lba, p.slots[i].buf); err != nil {
		p.slots[i].valid = false
		return nil, err
	}
	if err := p.verifyPage(lba, p.slots[i].buf); err != nil {
		if err2 := p.dev.Read(lba, p.slots[i].buf); err2 != nil {
			p.slots[i].valid = false
			return nil, err2
		}
		if err = p.verifyPage(lba, p.slots[i].buf); err != nil {
			p.slots[i].valid = false
			return nil, err
		}
	}
	return p.slots[i].buf, nil
}

func (p *Pager) PutPage(lba uint64, src []byte) error {
	if err := p.ready(); err != nil {
		return err
	}
	if err := p.checkLBA(lba); err != nil {
		return err
	}
	if len(src) != int(pageN) {
		return fmt.Errorf("c2db: page length %d want %d", len(src), pageN)
	}
	i, err := p.alloc(lba)
	if err != nil {
		return err
	}
	copy(p.slots[i].buf, src)
	p.slots[i].dirty = true
	return nil
}

func (p *Pager) FlushDirty() (int, error) {
	if err := p.ready(); err != nil {
		return 0, err
	}
	n := 0
	for i := range p.slots {
		if !p.slots[i].valid || !p.slots[i].dirty {
			continue
		}
		if err := p.dev.Write(p.slots[i].lba, p.slots[i].buf); err != nil {
			return n, err
		}
		if err := p.sealPage(p.slots[i].lba, p.slots[i].buf); err != nil {
			return n, err
		}
		p.slots[i].dirty = false
		n++
	}
	if n > 0 {
		p.flushes++
		probeEmit(0, ProbeOpBarrierOrder, 0, 0, 0, 0, "flushing data before tags")
		if err := p.dev.Flush(); err != nil {
			return n, err
		}
		if err := p.flushTags(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (p *Pager) FlushesCount() uint64 {
	if p == nil {
		return 0
	}
	return p.flushes
}

// FlushDirtyNoBarrier matérialise les pages sales et leurs étiquettes sans
// émettre de barrière fdatasync. Les octets sont écrits sur le dispositif de
// données (lisibles par un pread O_DIRECT ultérieur) et les emplacements
// redeviennent propres, mais leur durabilité reste portée par le journal : la
// barrière fdatasync n'a lieu qu'au prochain FlushDirty (pointage, fermeture).
// Le WAL demeure la source de durabilité jusqu'à ce pointage.
func (p *Pager) FlushDirtyNoBarrier() (int, error) {
	if err := p.ready(); err != nil {
		return 0, err
	}
	n := 0
	for i := range p.slots {
		if !p.slots[i].valid || !p.slots[i].dirty {
			continue
		}
		if err := p.dev.Write(p.slots[i].lba, p.slots[i].buf); err != nil {
			return n, err
		}
		if err := p.sealPage(p.slots[i].lba, p.slots[i].buf); err != nil {
			return n, err
		}
		p.slots[i].dirty = false
		n++
	}
	if n > 0 {
		if err := p.flushTagBlk(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (p *Pager) NDirty() int {
	if p == nil {
		return 0
	}
	n := 0
	for i := range p.slots {
		if p.slots[i].valid && p.slots[i].dirty {
			n++
		}
	}
	return n
}

func (p *Pager) Close() error {
	if err := p.ready(); err != nil {
		return err
	}
	_, err := p.FlushDirty()
	p.release()
	return err
}

func (p *Pager) CloseWithoutFlush() error {
	if err := p.ready(); err != nil {
		return err
	}
	p.release()
	return nil
}

func (p *Pager) SlotsCount() int {
	if p == nil {
		return 0
	}
	return len(p.slots)
}

// Reserve s'assure que le pager dispose d'au moins needed slots alloués en mémoire.
// Si needed > len(p.slots), des slots supplémentaires sont alloués via mmap anonyme avant l'engagement.
func (p *Pager) Reserve(needed int) error {
	if err := p.ready(); err != nil {
		return err
	}
	if needed <= len(p.slots) {
		return nil
	}
	if needed > 65536 {
		return fmt.Errorf("c2db: pager reserve request %d exceeds max capacity (65536)", needed)
	}
	extraSlots := needed - len(p.slots)
	extraBytes := extraSlots * int(pageN)
	extraArena, err := mmapAnon(extraBytes)
	if err != nil {
		return err
	}
	p.extraArenas = append(p.extraArenas, extraArena)
	oldLen := len(p.slots)
	newSlots := make([]pageSlot, needed)
	copy(newSlots, p.slots)
	for i := 0; i < extraSlots; i++ {
		off := i * int(pageN)
		newSlots[oldLen+i].buf = extraArena[off : off+int(pageN)]
	}
	p.slots = newSlots
	return nil
}

func (p *Pager) find(lba uint64) int {
	for i := range p.slots {
		if p.slots[i].valid && p.slots[i].lba == lba {
			return i
		}
	}
	return -1
}

func (p *Pager) alloc(lba uint64) (int, error) {
	if i := p.find(lba); i >= 0 {
		return i, nil
	}
	for i := range p.slots {
		if !p.slots[i].valid {
			p.slots[i].lba = lba
			p.slots[i].valid = true
			p.slots[i].dirty = false
			return i, nil
		}
	}
	numSlots := len(p.slots)
	// Recherche d'un slot propre (clean) sans déclencher de vidage synchrone.
	for k := 0; k < numSlots; k++ {
		i := int(p.clock % uint64(numSlots))
		p.clock++
		if !p.slots[i].dirty {
			probeEmit(0, ProbeOpPagerEvict, p.slots[i].lba, 0, 0, 0, fmt.Sprintf("evict clean slot %d for lba %d", i, lba))
			p.slots[i].lba = lba
			p.slots[i].valid = true
			p.slots[i].dirty = false
			return i, nil
		}
	}
	// Tous les slots sont sales : vidage complet obligatoire.
	probeEmit(0, ProbeOpPagerFlush, lba, 0, 0, 0, "all pager slots dirty, triggering FlushDirty")
	if _, err := p.FlushDirty(); err != nil {
		return -1, err
	}
	i := int(p.clock % uint64(numSlots))
	p.clock++
	p.slots[i].lba = lba
	p.slots[i].valid = true
	p.slots[i].dirty = false
	return i, nil
}

func (p *Pager) ready() error {
	if p == nil || p.dev == nil || p.arena == nil {
		return unix.EBADF
	}
	return p.dev.checkReady()
}

func (p *Pager) release() {
	if p.arena != nil {
		_ = unix.Munmap(p.arena)
		p.arena = nil
	}
	for _, ea := range p.extraArenas {
		if ea != nil {
			_ = unix.Munmap(ea)
		}
	}
	p.extraArenas = nil
	if p.tagBlk != nil {
		_ = unix.Munmap(p.tagBlk)
		p.tagBlk = nil
	}
	if p.tags != nil {
		_ = p.tags.Close()
		p.tags = nil
	}
	p.slots = nil
	p.dev = nil
	p.sealed = false
}

func checkPageLBA(lba uint64) error {
	if lba%pageLBAs != 0 {
		return fmt.Errorf("c2db: page lba %d not aligned to %d", lba, pageLBAs)
	}
	return nil
}

func (p *Pager) checkLBA(lba uint64) error {
	if err := checkPageLBA(lba); err != nil {
		return err
	}
	if p.dev == nil {
		return unix.EBADF
	}
	maxLBA := p.dev.size / LBASize
	if lba >= maxLBA || pageLBAs > maxLBA-lba {
		return fmt.Errorf("c2db: page lba %d out of range (size %d)", lba, p.dev.size)
	}
	return nil
}
