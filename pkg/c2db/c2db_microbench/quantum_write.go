// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type WriteBenchResult struct {
	Size             int     `json:"size_bytes"`
	PwriteStatsNanos Stats   `json:"pwrite_nanos"`
	FsyncStatsNanos  Stats   `json:"fsync_nanos"`
	TotalStatsNanos  Stats   `json:"total_nanos"`
	ThroughputMB     float64 `json:"throughput_mb_s"`
	IOPS             float64 `json:"iops"`
	Jitter           float64 `json:"jitter_p99_p50"`
}

// RunNVMeWriteBenchmark mesure les écritures directes O_DIRECT et fdatasync selon la taille du bloc.
func RunNVMeWriteBenchmark(dir string, tscFreq float64) ([]WriteBenchResult, error) {
	const iters = 200
	const maxBufSize = 262144 // 256 Kio

	writeMmap, err := unix.Mmap(-1, 0, maxBufSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(writeMmap)

	for i := range writeMmap {
		writeMmap[i] = byte(i % 253)
	}

	sizes := []int{4096, 8192, 16384, 32768, 65536, 131072, 262144}
	var results []WriteBenchResult

	for _, sz := range sizes {
		filePath := filepath.Join(dir, fmt.Sprintf("bench_write_%d.bin", sz))
		fd, err := unix.Open(filePath, unix.O_RDWR|unix.O_CREAT|unix.O_TRUNC|unix.O_DIRECT, 0600)
		if err != nil {
			return nil, fmt.Errorf("open O_DIRECT for write (%d): %w", sz, err)
		}

		pwriteNanos := make([]float64, iters)
		fsyncNanos := make([]float64, iters)
		totalNanos := make([]float64, iters)

		// Pré-allocation de 32 Mo pour éviter les extensions d'inodes pendant la mesure
		const preallocSize = 32 * 1024 * 1024
		if err := unix.Fallocate(fd, 0, 0, preallocSize); err != nil {
			_ = unix.Close(fd)
			_ = os.Remove(filePath)
			return nil, fmt.Errorf("fallocate: %w", err)
		}

		maxOffset := int64((preallocSize - sz) / sz)

		for i := 0; i < iters; i++ {
			off := int64(i%int(maxOffset)) * int64(sz)

			// Mesure 1 : pwrite (Transfert PCIe -> Contrôleur NVMe)
			c0, _ := rdtscp()
			n, err := unix.Pwrite(fd, writeMmap[:sz], off)
			c1, _ := rdtscp()

			if err != nil || n != sz {
				_ = unix.Close(fd)
				_ = os.Remove(filePath)
				return nil, fmt.Errorf("pwrite failed: %v", err)
			}

			// Mesure 2 : fdatasync (Persistance dans la Flash NAND)
			c2, _ := rdtscp()
			err = unix.Fdatasync(fd)
			c3, _ := rdtscp()

			if err != nil {
				_ = unix.Close(fd)
				_ = os.Remove(filePath)
				return nil, fmt.Errorf("fdatasync failed: %v", err)
			}

			pwNs := float64(c1-c0) / tscFreq
			fsNs := float64(c3-c2) / tscFreq
			totNs := pwNs + fsNs

			pwriteNanos[i] = pwNs
			fsyncNanos[i] = fsNs
			totalNanos[i] = totNs
		}

		_ = unix.Close(fd)
		_ = os.Remove(filePath)

		sPw := ComputeStats(pwriteNanos)
		sFs := ComputeStats(fsyncNanos)
		sTot := ComputeStats(totalNanos)

		mbPerSec := 0.0
		iops := 0.0
		if sTot.P50 > 0 {
			totSec := sTot.P50 * 1e-9
			mbPerSec = (float64(sz) / (1024 * 1024)) / totSec
			iops = 1.0 / totSec
		}

		results = append(results, WriteBenchResult{
			Size:             sz,
			PwriteStatsNanos: sPw,
			FsyncStatsNanos:  sFs,
			TotalStatsNanos:  sTot,
			ThroughputMB:     mbPerSec,
			IOPS:             iops,
			Jitter:           sTot.Jitter,
		})
	}

	return results, nil
}
