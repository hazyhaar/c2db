// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"math/bits"

	"golang.org/x/sys/unix"
)

type FullCopyResult struct {
	Pages       int     `json:"pages_copied"`
	SizeBytes   int     `json:"size_bytes"`
	StatsCycles Stats   `json:"cycles"`
	StatsNanos  Stats   `json:"nanos"`
	GBPerSec    float64 `json:"dram_gb_s"`
}

type DirtyMaskCopyResult struct {
	DirtyPagesCount int     `json:"dirty_pages_count"`
	TotalHeapPages  int     `json:"total_heap_pages"`
	StatsCycles     Stats   `json:"cycles"`
	StatsNanos      Stats   `json:"nanos"`
	SpeedupVsFull64 float64 `json:"speedup_vs_full_64mb"`
}

const (
	benchPageN     = 16384
	benchHeapPages = 4096
	benchHeapBytes = benchHeapPages * benchPageN // 64 Mio
)

// RunFullHeapCopyBenchmark mesure le coût de la copie intégrale du tas (comportement antérieur).
func RunFullHeapCopyBenchmark(tscFreq float64) ([]FullCopyResult, error) {
	pub, err := unix.Mmap(-1, 0, benchHeapBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(pub)

	dirty, err := unix.Mmap(-1, 0, benchHeapBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(dirty)

	for i := 0; i < benchHeapBytes; i += 4096 {
		pub[i] = byte(i)
	}

	pageCounts := []int{16, 64, 256, 1024, 4096}
	var results []FullCopyResult
	const iters = 100

	for _, np := range pageCounts {
		sz := np * benchPageN
		cycles := make([]float64, iters)
		nanos := make([]float64, iters)

		for i := 0; i < iters; i++ {
			c0, _ := rdtscp()
			copy(dirty[:sz], pub[:sz])
			c1, _ := rdtscp()

			cDelta := float64(c1 - c0)
			cycles[i] = cDelta
			nanos[i] = cDelta / tscFreq
		}

		sC := ComputeStats(cycles)
		sN := ComputeStats(nanos)

		gbPerSec := 0.0
		if sN.P50 > 0 {
			gbPerSec = (float64(sz) / (1024 * 1024 * 1024)) / (sN.P50 * 1e-9)
		}

		results = append(results, FullCopyResult{
			Pages:       np,
			SizeBytes:   sz,
			StatsCycles: sC,
			StatsNanos:  sN,
			GBPerSec:    gbPerSec,
		})
	}

	return results, nil
}

// RunDirtyMaskCopyBenchmark mesure le coût de la synchronisation sélective par masque de bits 64-bit.
func RunDirtyMaskCopyBenchmark(tscFreq float64, full64mbNanos float64) ([]DirtyMaskCopyResult, error) {
	pub, err := unix.Mmap(-1, 0, benchHeapBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(pub)

	dirty, err := unix.Mmap(-1, 0, benchHeapBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(dirty)

	for i := 0; i < benchHeapBytes; i += 4096 {
		pub[i] = byte(i)
	}

	// 4096 pages = 64 mots de 64 bits = 512 octets
	type dirtyMask [64]uint64

	dirtyCases := []int{1, 2, 4, 8, 16, 32}
	var results []DirtyMaskCopyResult
	const iters = 500

	for _, ndirty := range dirtyCases {
		var mask dirtyMask
		for p := 0; p < ndirty; p++ {
			// Distribution des pages sales éparpillées dans le tas
			pageIdx := (p * 257) % benchHeapPages
			mask[pageIdx/64] |= 1 << (pageIdx % 64)
		}

		cycles := make([]float64, iters)
		nanos := make([]float64, iters)

		for i := 0; i < iters; i++ {
			c0, _ := rdtscp()

			// Balayage vectorisé du masque par tzcnt/blsr
			for w := 0; w < 64; w++ {
				word := mask[w]
				for word != 0 {
					bit := bits.TrailingZeros64(word)
					pageIdx := w*64 + bit
					off := pageIdx * benchPageN
					copy(dirty[off:off+benchPageN], pub[off:off+benchPageN])
					word &= word - 1 // blsr
				}
			}

			c1, _ := rdtscp()
			cDelta := float64(c1 - c0)
			cycles[i] = cDelta
			nanos[i] = cDelta / tscFreq
		}

		sC := ComputeStats(cycles)
		sN := ComputeStats(nanos)

		speedup := 0.0
		if sN.P50 > 0 && full64mbNanos > 0 {
			speedup = full64mbNanos / sN.P50
		}

		results = append(results, DirtyMaskCopyResult{
			DirtyPagesCount: ndirty,
			TotalHeapPages:  benchHeapPages,
			StatsCycles:     sC,
			StatsNanos:      sN,
			SpeedupVsFull64: speedup,
		})
	}

	return results, nil
}
