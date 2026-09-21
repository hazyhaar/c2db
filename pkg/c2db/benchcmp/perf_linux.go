// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build linux

package benchcmp

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

const (
	perfTypeHardware = 0
	perfTypeHWCache  = 3
	perfCycles       = 0
	perfInsn         = 1
	perfBranches     = 4
	perfBrMiss       = 5
	perfL1D          = 0
	perfCacheRead    = 0
	perfCacheMiss    = 1
	perfIOCReset     = 0x2402
	perfIOCEnable    = 0x2400
)

type perfAttr [136]byte

type l1Counters struct {
	Cycles   uint64  `json:"cycles"`
	Insn     uint64  `json:"instructions"`
	Branches uint64  `json:"branches"`
	BrMiss   uint64  `json:"branch_misses"`
	L1DMiss  uint64  `json:"l1d_misses"`
	IPC      float64 `json:"ipc"`
	OK       bool    `json:"ok"`
}

type perfSampler struct {
	fdCycles, fdInsn, fdBr, fdBrMiss, fdL1 int
	ok                                     bool
}

func openPerf() *perfSampler {
	p := &perfSampler{fdCycles: -1, fdInsn: -1, fdBr: -1, fdBrMiss: -1, fdL1: -1}
	var err error
	p.fdCycles, err = openHw(perfTypeHardware, perfCycles)
	if err != nil {
		return p
	}
	p.fdInsn, _ = openHw(perfTypeHardware, perfInsn)
	p.fdBr, _ = openHw(perfTypeHardware, perfBranches)
	p.fdBrMiss, _ = openHw(perfTypeHardware, perfBrMiss)
	l1 := uint64(perfL1D) | uint64(perfCacheRead)<<8 | uint64(perfCacheMiss)<<16
	p.fdL1, _ = openHw(perfTypeHWCache, l1)
	p.ok = true
	return p
}

func openHw(typ uint32, config uint64) (int, error) {
	var a perfAttr
	binary.LittleEndian.PutUint32(a[0:4], typ)
	binary.LittleEndian.PutUint32(a[4:8], 136)
	binary.LittleEndian.PutUint64(a[8:16], config)
	a[40] = 0x61
	fd, _, errno := syscall.Syscall6(syscall.SYS_PERF_EVENT_OPEN, uintptr(unsafe.Pointer(&a)), 0, ^uintptr(0), ^uintptr(0), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func (p *perfSampler) start() {
	if p == nil || !p.ok {
		return
	}
	for _, fd := range []int{p.fdCycles, p.fdInsn, p.fdBr, p.fdBrMiss, p.fdL1} {
		if fd >= 0 {
			_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), perfIOCReset, 0)
			_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), perfIOCEnable, 0)
		}
	}
}

func readFD(fd int) uint64 {
	if fd < 0 {
		return 0
	}
	var b [8]byte
	n, err := syscall.Read(fd, b[:])
	if err != nil || n < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(b[:])
}

func (p *perfSampler) snap() l1Counters {
	var c l1Counters
	if p == nil || !p.ok {
		return c
	}
	c.OK = true
	c.Cycles = readFD(p.fdCycles)
	c.Insn = readFD(p.fdInsn)
	c.Branches = readFD(p.fdBr)
	c.BrMiss = readFD(p.fdBrMiss)
	c.L1DMiss = readFD(p.fdL1)
	if c.Cycles > 0 {
		c.IPC = float64(c.Insn) / float64(c.Cycles)
	}
	return c
}

func (p *perfSampler) close() {
	if p == nil {
		return
	}
	for _, fd := range []int{p.fdCycles, p.fdInsn, p.fdBr, p.fdBrMiss, p.fdL1} {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
	}
}
