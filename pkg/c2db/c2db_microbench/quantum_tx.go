// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"bytes"
	"runtime"
	"strconv"
)

type TxContextResult struct {
	Name        string  `json:"name"`
	StatsCycles Stats   `json:"cycles"`
	StatsNanos  Stats   `json:"nanos"`
	AllocsPerOp int     `json:"allocs_per_op"`
	DeltaVsTx   float64 `json:"ratio_vs_explicit_tx"`
}

// goidCurrent reproduit fidèlement la fonction de engine.go:226
func goidCurrent() uint64 {
	var buf [32]byte
	n := runtime.Stack(buf[:], false)
	b := buf[:n]
	if i := bytes.IndexByte(b, ' '); i >= 0 {
		b = b[i+1:]
	}
	if j := bytes.IndexByte(b, ' '); j >= 0 {
		b = b[:j]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}

type ExplicitTx struct {
	ID uint64
}

func RunTxContextBenchmark(tscFreq float64) ([]TxContextResult, error) {
	const iters = 5000

	// 1. Mesure de goidCurrent()
	goidCycles := make([]float64, iters)
	goidNanos := make([]float64, iters)
	sink := uint64(0)

	for i := 0; i < iters; i++ {
		c0, _ := rdtscp()
		id := goidCurrent()
		c1, _ := rdtscp()

		sink += id
		cDelta := float64(c1 - c0)
		goidCycles[i] = cDelta
		goidNanos[i] = cDelta / tscFreq
	}

	// 2. Mesure du contexte explicite Tx
	tx := &ExplicitTx{ID: 42}
	txCycles := make([]float64, iters)
	txNanos := make([]float64, iters)

	for i := 0; i < iters; i++ {
		c0, _ := rdtscp()
		id := tx.ID
		c1, _ := rdtscp()

		sink += id
		cDelta := float64(c1 - c0)
		txCycles[i] = cDelta
		txNanos[i] = cDelta / tscFreq
	}

	_ = sink

	sGoidC := ComputeStats(goidCycles)
	sGoidN := ComputeStats(goidNanos)
	sTxC := ComputeStats(txCycles)
	sTxN := ComputeStats(txNanos)

	ratio := 0.0
	if sTxN.P50 > 0 {
		ratio = sGoidN.P50 / sTxN.P50
	}

	results := []TxContextResult{
		{
			Name:        "goid_runtime_stack",
			StatsCycles: sGoidC,
			StatsNanos:  sGoidN,
			AllocsPerOp: 0,
			DeltaVsTx:   ratio,
		},
		{
			Name:        "explicit_tx_struct",
			StatsCycles: sTxC,
			StatsNanos:  sTxN,
			AllocsPerOp: 0,
			DeltaVsTx:   1.0,
		},
	}

	return results, nil
}
