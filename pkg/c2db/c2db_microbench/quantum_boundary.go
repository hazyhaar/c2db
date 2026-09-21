// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type BoundaryCaseResult struct {
	Name             string  `json:"name"`
	Size             int     `json:"size_bytes"`
	OffsetShift      int     `json:"offset_shift_bytes"`
	PwriteLatencyP50 float64 `json:"pwrite_p50_us"`
	PwriteLatencyP99 float64 `json:"pwrite_p99_us"`
	FsyncLatencyP50  float64 `json:"fsync_p50_us"`
	TotalLatencyP50  float64 `json:"total_p50_us"`
	ThroughputMB     float64 `json:"throughput_mb_s"`
	Jitter           float64 `json:"jitter_p99_p50"`
	KernelSplit      bool    `json:"kernel_split_expected"`
}

// RunFineBoundarySweep effectue un balayage fin de 64 Ko à 320 Ko par pas de 16 Ko et teste les cas limites.
func RunFineBoundarySweep(dir string, tscFreq float64) ([]BoundaryCaseResult, error) {
	const iters = 200
	const maxTestBuf = 512 * 1024 // 512 Kio

	buf, err := unix.Mmap(-1, 0, maxTestBuf+8192, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(buf)

	for i := range buf {
		buf[i] = byte(i % 241)
	}

	// 1. Balayage fin en taille (puissances et intermédiaires)
	// 64K, 96K, 112K, 124K, 128K, 132K, 144K, 160K, 192K, 224K, 252K, 256K, 260K, 288K, 320K
	sweepSizes := []struct {
		name string
		size int
	}{
		{"64K (Quantum standard)", 64 * 1024},
		{"96K (Intermédiaire 6 canaux)", 96 * 1024},
		{"124K (128K - 4K)", 124 * 1024},
		{"128K (Quantum 8 canaux pile)", 128 * 1024},
		{"132K (128K + 4K débordement)", 132 * 1024},
		{"160K (10 canaux)", 160 * 1024},
		{"192K (12 canaux)", 192 * 1024},
		{"224K (14 canaux)", 224 * 1024},
		{"252K (256K - 4K)", 252 * 1024},
		{"256K (MDTS frontière matérielle)", 256 * 1024},
		{"260K (256K + 4K scission noyau)", 260 * 1024},
		{"288K (Dépassement MDTS)", 288 * 1024},
		{"320K (Dépassement 2 x 160K)", 320 * 1024},
	}

	var results []BoundaryCaseResult

	for _, tc := range sweepSizes {
		sz := tc.size
		filePath := filepath.Join(dir, fmt.Sprintf("bench_bound_%d.bin", sz))
		fd, err := unix.Open(filePath, unix.O_RDWR|unix.O_CREAT|unix.O_TRUNC|unix.O_DIRECT, 0600)
		if err != nil {
			return nil, fmt.Errorf("open O_DIRECT for %s: %w", tc.name, err)
		}

		const preallocSize = 16 * 1024 * 1024
		if err := unix.Fallocate(fd, 0, 0, preallocSize); err != nil {
			_ = unix.Close(fd)
			_ = os.Remove(filePath)
			return nil, err
		}

		maxOff := int64((preallocSize - sz) / sz)
		pwriteNanos := make([]float64, iters)
		fsyncNanos := make([]float64, iters)
		totalNanos := make([]float64, iters)

		for i := 0; i < iters; i++ {
			off := int64(i%int(maxOff)) * int64(sz)

			c0, _ := rdtscp()
			n, err := unix.Pwrite(fd, buf[:sz], off)
			c1, _ := rdtscp()

			if err != nil || n != sz {
				_ = unix.Close(fd)
				_ = os.Remove(filePath)
				return nil, fmt.Errorf("pwrite %s failed: %v", tc.name, err)
			}

			c2, _ := rdtscp()
			err = unix.Fdatasync(fd)
			c3, _ := rdtscp()

			if err != nil {
				_ = unix.Close(fd)
				_ = os.Remove(filePath)
				return nil, fmt.Errorf("fsync %s failed: %v", tc.name, err)
			}

			pwNs := float64(c1-c0) / tscFreq
			fsNs := float64(c3-c2) / tscFreq

			pwriteNanos[i] = pwNs
			fsyncNanos[i] = fsNs
			totalNanos[i] = pwNs + fsNs
		}

		_ = unix.Close(fd)
		_ = os.Remove(filePath)

		sPw := ComputeStats(pwriteNanos)
		sFs := ComputeStats(fsyncNanos)
		sTot := ComputeStats(totalNanos)

		mbPerSec := 0.0
		if sTot.P50 > 0 {
			mbPerSec = (float64(sz) / (1024 * 1024)) / (sTot.P50 * 1e-9)
		}

		results = append(results, BoundaryCaseResult{
			Name:             tc.name,
			Size:             sz,
			OffsetShift:      0,
			PwriteLatencyP50: sPw.P50 / 1000.0,
			PwriteLatencyP99: sPw.P99 / 1000.0,
			FsyncLatencyP50:  sFs.P50 / 1000.0,
			TotalLatencyP50:  sTot.P50 / 1000.0,
			ThroughputMB:     mbPerSec,
			Jitter:           sTot.Jitter,
			KernelSplit:      sz > 256*1024,
		})
	}

	// 2. Cas frontière de décalage d'alignement physique (512B shift sur tranche 128K)
	// Vérifie si écrire 128K avec un offset décalé de 512B viole l'alignement de secteur/page NAND
	shiftCases := []struct {
		name  string
		size  int
		shift int
	}{
		{"128K aligné sur 16K (Optimal)", 128 * 1024, 0},
		{"128K décalé de 512B (Désalignement secteur)", 128 * 1024, 512},
		{"128K décalé de 4K (Désalignement page)", 128 * 1024, 4096},
	}

	for _, sc := range shiftCases {
		filePath := filepath.Join(dir, "bench_shift.bin")
		fd, err := unix.Open(filePath, unix.O_RDWR|unix.O_CREAT|unix.O_TRUNC|unix.O_DIRECT, 0600)
		if err != nil {
			return nil, err
		}

		const preallocSize = 16 * 1024 * 1024
		_ = unix.Fallocate(fd, 0, 0, preallocSize)

		pwriteNanos := make([]float64, iters)
		fsyncNanos := make([]float64, iters)
		totalNanos := make([]float64, iters)

		sz := sc.size
		shift := sc.shift
		maxOff := int64((preallocSize - sz - shift) / sz)

		for i := 0; i < iters; i++ {
			off := int64(i%int(maxOff))*int64(sz) + int64(shift)

			c0, _ := rdtscp()
			n, err := unix.Pwrite(fd, buf[:sz], off)
			c1, _ := rdtscp()

			if err != nil || n != sz {
				_ = unix.Close(fd)
				_ = os.Remove(filePath)
				return nil, fmt.Errorf("pwrite shift failed: %v", err)
			}

			c2, _ := rdtscp()
			_ = unix.Fdatasync(fd)
			c3, _ := rdtscp()

			pwNs := float64(c1-c0) / tscFreq
			fsNs := float64(c3-c2) / tscFreq

			pwriteNanos[i] = pwNs
			fsyncNanos[i] = fsNs
			totalNanos[i] = pwNs + fsNs
		}

		_ = unix.Close(fd)
		_ = os.Remove(filePath)

		sPw := ComputeStats(pwriteNanos)
		sFs := ComputeStats(fsyncNanos)
		sTot := ComputeStats(totalNanos)

		mbPerSec := 0.0
		if sTot.P50 > 0 {
			mbPerSec = (float64(sz) / (1024 * 1024)) / (sTot.P50 * 1e-9)
		}

		results = append(results, BoundaryCaseResult{
			Name:             sc.name,
			Size:             sz,
			OffsetShift:      shift,
			PwriteLatencyP50: sPw.P50 / 1000.0,
			PwriteLatencyP99: sPw.P99 / 1000.0,
			FsyncLatencyP50:  sFs.P50 / 1000.0,
			TotalLatencyP50:  sTot.P50 / 1000.0,
			ThroughputMB:     mbPerSec,
			Jitter:           sTot.Jitter,
			KernelSplit:      false,
		})
	}

	return results, nil
}
