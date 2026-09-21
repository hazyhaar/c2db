// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

type ReadBenchResult struct {
	Size         int     `json:"size_bytes"`
	Aligned      bool    `json:"aligned_16k"`
	StatsCycles  Stats   `json:"cycles"`
	StatsNanos   Stats   `json:"nanos"`
	ThroughputMB float64 `json:"throughput_mb_s"`
	NanosPerKB   float64 `json:"nanos_per_kb"`
}

type CacheLineSplitResult struct {
	OffsetBytes int     `json:"offset_bytes"`
	StatsCycles Stats   `json:"cycles"`
	StatsNanos  Stats   `json:"nanos"`
	PenaltyPct  float64 `json:"penalty_pct_vs_aligned"`
}

// RunNVMeReadBenchmark mesure les lectures O_DIRECT de 512B à 256KB sur le NVMe hôte.
func RunNVMeReadBenchmark(dir string, tscFreq float64) ([]ReadBenchResult, error) {
	const totalFileSize = 32 * 1024 * 1024 // 32 Mo
	filePath := filepath.Join(dir, "bench_read_nvme.bin")

	// Préparation du fichier de test aligné O_DIRECT
	fd, err := unix.Open(filePath, unix.O_RDWR|unix.O_CREAT|unix.O_TRUNC|unix.O_DIRECT, 0600)
	if err != nil {
		return nil, fmt.Errorf("open O_DIRECT: %w", err)
	}
	defer func() {
		_ = unix.Close(fd)
		_ = os.Remove(filePath)
	}()

	initBuf, err := unix.Mmap(-1, 0, totalFileSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	for i := range initBuf {
		initBuf[i] = byte(i % 251)
	}
	if _, err := unix.Pwrite(fd, initBuf, 0); err != nil {
		_ = unix.Munmap(initBuf)
		return nil, fmt.Errorf("pwrite init: %w", err)
	}
	_ = unix.Fdatasync(fd)
	_ = unix.Munmap(initBuf)

	sizes := []int{512, 4096, 8192, 16384, 32768, 65536, 131072, 262144}
	var results []ReadBenchResult

	const iters = 500
	const maxBufSize = 262144

	alignMmap, err := unix.Mmap(-1, 0, maxBufSize+4096, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(alignMmap)

	readBuf := alignMmap[:maxBufSize]

	for _, sz := range sizes {
		// Test A : Aligné sur 16 Kio
		cycles := make([]float64, iters)
		nanos := make([]float64, iters)

		for i := 0; i < iters; i++ {
			// Offset circulaire aligné sur la taille
			maxOff := int64((totalFileSize - sz) / sz)
			offset := int64(i%int(maxOff)) * int64(sz)

			cStart, _ := rdtscp()
			n, err := unix.Pread(fd, readBuf[:sz], offset)
			cEnd, _ := rdtscp()

			if err != nil || n != sz {
				return nil, fmt.Errorf("pread err=%v n=%d sz=%d", err, n, sz)
			}
			cDelta := float64(cEnd - cStart)
			nDelta := cDelta / tscFreq

			cycles[i] = cDelta
			nanos[i] = nDelta
		}

		sC := ComputeStats(cycles)
		sN := ComputeStats(nanos)

		mbPerSec := 0.0
		if sN.P50 > 0 {
			mbPerSec = (float64(sz) / (1024 * 1024)) / (sN.P50 * 1e-9)
		}
		nsPerKB := 0.0
		if sz > 0 {
			nsPerKB = sN.P50 / (float64(sz) / 1024.0)
		}

		results = append(results, ReadBenchResult{
			Size:         sz,
			Aligned:      true,
			StatsCycles:  sC,
			StatsNanos:   sN,
			ThroughputMB: mbPerSec,
			NanosPerKB:   nsPerKB,
		})
	}

	return results, nil
}

// RunCacheLineSplitBenchmark mesure la pénalité CPU des décalages d'alignement L1D (64 octets).
func RunCacheLineSplitBenchmark(tscFreq float64) ([]CacheLineSplitResult, error) {
	const iters = 2000
	const bufSize = 16384 + 128

	// Allouer mémoire alignée
	raw, err := unix.Mmap(-1, 0, bufSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(raw)

	for i := range raw {
		raw[i] = byte(i)
	}

	// Aligner à la ligne de cache 64B
	addr := uintptr(unsafe.Pointer(&raw[0]))
	alignedBase := (addr + 63) &^ 63
	baseOffset := int(alignedBase - addr)

	offsets := []int{0, 1, 8, 16, 31, 32, 63}
	var results []CacheLineSplitResult
	var baseP50 float64

	for idx, off := range offsets {
		startPtr := baseOffset + off
		target := raw[startPtr : startPtr+16384]

		cycles := make([]float64, iters)
		sink := uint64(0)

		// Warmup
		for w := 0; w < 100; w++ {
			for j := 0; j < 16384; j += 64 {
				sink += *(*uint64)(unsafe.Pointer(&target[j]))
			}
		}

		for i := 0; i < iters; i++ {
			c0, _ := rdtscp()
			acc := uint64(0)
			// Parcours des 256 lignes de 64 octets
			for j := 0; j < 16384; j += 64 {
				acc += *(*uint64)(unsafe.Pointer(&target[j]))
				acc += *(*uint64)(unsafe.Pointer(&target[j+32]))
			}
			c1, _ := rdtscp()
			sink += acc
			cycles[i] = float64(c1 - c0)
		}

		nanos := make([]float64, iters)
		for i := range cycles {
			nanos[i] = cycles[i] / tscFreq
		}
		_ = sink
		stC := ComputeStats(cycles)
		stN := ComputeStats(nanos)
		if idx == 0 {
			baseP50 = stC.P50
		}

		penalty := 0.0
		if baseP50 > 0 {
			penalty = ((stC.P50 - baseP50) / baseP50) * 100.0
		}

		results = append(results, CacheLineSplitResult{
			OffsetBytes: off,
			StatsCycles: stC,
			StatsNanos:  stN,
			PenaltyPct:  penalty,
		})
	}

	return results, nil
}
