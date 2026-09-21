// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build !linux

package benchcmp

type l1Counters struct {
	Cycles   uint64  `json:"cycles"`
	Insn     uint64  `json:"instructions"`
	Branches uint64  `json:"branches"`
	BrMiss   uint64  `json:"branch_misses"`
	L1DMiss  uint64  `json:"l1d_misses"`
	IPC      float64 `json:"ipc"`
	OK       bool    `json:"ok"`
}

type perfSampler struct{}

func openPerf() *perfSampler            { return &perfSampler{} }
func (p *perfSampler) start()           {}
func (p *perfSampler) snap() l1Counters { return l1Counters{} }
func (p *perfSampler) close()           {}
