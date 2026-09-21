// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"math"
	"sort"
	"time"
)

// rdtsc lit le compteur TSC du processeur sans barrière de sérialisation.
func rdtsc() uint64

// rdtscp lit le compteur TSC avec barrière de sérialisation et retourne l'identifiant vCPU/core (aux).
func rdtscp() (tsc uint64, aux uint32)

// CalibrateTSC mesure la fréquence effective du TSC en cycles par nanoseconde.
func CalibrateTSC() float64 {
	t0 := time.Now()
	c0 := rdtsc()
	time.Sleep(50 * time.Millisecond)
	t1 := time.Now()
	c1 := rdtsc()

	ns := float64(t1.Sub(t0).Nanoseconds())
	cycles := float64(c1 - c0)
	if ns <= 0 {
		return 3.0 // Fréquence par défaut
	}
	return cycles / ns
}

// Stats contient la distribution statistique opposable d'un jeu d'échantillons.
type Stats struct {
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	P50    float64 `json:"p50"`
	P90    float64 `json:"p90"`
	P99    float64 `json:"p99"`
	P999   float64 `json:"p999"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"std_dev"`
	Jitter float64 `json:"jitter_p99_p50"` // Ratio p99 / p50 (dérive "machine-itself")
}

func ComputeStats(samples []float64) Stats {
	n := len(samples)
	if n == 0 {
		return Stats{}
	}
	sorted := make([]float64, n)
	copy(sorted, samples)
	sort.Float64s(sorted)

	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	mean := sum / float64(n)

	variance := 0.0
	for _, v := range sorted {
		diff := v - mean
		variance += diff * diff
	}
	stdDev := math.Sqrt(variance / float64(n))

	p := func(pct float64) float64 {
		idx := int(pct * float64(n-1))
		if idx >= n {
			idx = n - 1
		}
		return sorted[idx]
	}

	p50 := p(0.50)
	p99 := p(0.99)
	jitter := 0.0
	if p50 > 0 {
		jitter = p99 / p50
	}

	return Stats{
		Count:  n,
		Min:    sorted[0],
		P50:    p50,
		P90:    p(0.90),
		P99:    p99,
		P999:   p(0.999),
		Max:    sorted[n-1],
		Mean:   mean,
		StdDev: stdDev,
		Jitter: jitter,
	}
}
