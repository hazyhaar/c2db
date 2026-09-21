// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"
)

type FullMicrobenchReport struct {
	Timestamp          string                 `json:"timestamp"`
	CPUModel           string                 `json:"cpu_arch"`
	CPUCores           int                    `json:"cpu_cores"`
	TSCFrequencyGHz    float64                `json:"tsc_freq_ghz"`
	TargetDirectory    string                 `json:"target_dir"`
	CacheLineSplits    []CacheLineSplitResult `json:"cache_line_splits"`
	NVMeReads          []ReadBenchResult      `json:"nvme_reads"`
	NVMeWrites         []WriteBenchResult     `json:"nvme_writes"`
	FullHeapCopies     []FullCopyResult       `json:"full_heap_copies"`
	DirtyMaskCopies    []DirtyMaskCopyResult  `json:"dirty_mask_copies"`
	TxContextBenchmark []TxContextResult      `json:"tx_context_benchmark"`
	BoundarySweep      []BoundaryCaseResult   `json:"boundary_sweep"`
}

func main() {
	fmt.Println("================================================================================")
	fmt.Println("             c2db_microbench : ÉVALUATION AU SILICIUM ET QUANTA NVMe/CPU        ")
	fmt.Println("================================================================================")

	targetDir := "/devhoros/c2simd/c2pkg/c2db/c2db_microbench/data_test"
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		fmt.Printf("Erreur mkdir targetDir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(targetDir)

	fmt.Printf("1. Calibration du compteur de cycles matériel (TSC)...\n")
	tscFreq := CalibrateTSC()
	fmt.Printf("   -> Fréquence TSC mesurée : %.3f GHz (1 cycle = %.3f ns)\n\n", tscFreq, 1.0/tscFreq)

	// ÉPREUVE 1 : Pénalité de cache-line split (L1D 64 octets)
	fmt.Printf("2. Épreuve 1 : Micro-décalages d'alignement mémoire L1D (Cache-Line Split)...\n")
	splits, err := RunCacheLineSplitBenchmark(tscFreq)
	if err != nil {
		fmt.Printf("Erreur cache line split: %v\n", err)
		os.Exit(1)
	}
	for _, s := range splits {
		fmt.Printf("   Offset +%02dB : %6.1f cycles (p50: %6.1f ns, p99: %6.1f ns) | Pénalité: %+.1f%%\n",
			s.OffsetBytes, s.StatsCycles.P50, s.StatsNanos.P50, s.StatsNanos.P99, s.PenaltyPct)
	}

	// ÉPREUVE 2 : Quantum de lecture physique NVMe (O_DIRECT)
	fmt.Printf("\n3. Épreuve 2 : Lectures directes O_DIRECT sur support NVMe (/devhoros)...\n")
	reads, err := RunNVMeReadBenchmark(targetDir, tscFreq)
	if err != nil {
		fmt.Printf("Erreur NVMe read bench: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   %-8s | %-12s | %-12s | %-12s | %-12s | %-10s\n", "Taille", "P50 Latence", "P99 Latence", "Débit (Mo/s)", "ns / Kio", "Jitter p99/p50")
	fmt.Printf("   ---------+--------------+--------------+--------------+--------------+-----------\n")
	for _, r := range reads {
		fmt.Printf("   %-8s | %9.2f µs | %9.2f µs | %9.1f Mo/s | %9.2f ns | %7.2fx\n",
			formatSize(r.Size), r.StatsNanos.P50/1000.0, r.StatsNanos.P99/1000.0, r.ThroughputMB, r.NanosPerKB, r.StatsNanos.Jitter)
	}

	// ÉPREUVE 3 : Quantum d'écriture physique NVMe (O_DIRECT + fdatasync)
	fmt.Printf("\n4. Épreuve 3 : Écritures directes O_DIRECT et fdatasync NVMe (/devhoros)...\n")
	writes, err := RunNVMeWriteBenchmark(targetDir, tscFreq)
	if err != nil {
		fmt.Printf("Erreur NVMe write bench: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   %-8s | %-11s | %-11s | %-11s | %-10s | %-10s | %-10s\n", "Taille", "Pwrite p50", "Fsync p50", "Total p50", "Débit Mo/s", "IOPS", "Jitter p99/50")
	fmt.Printf("   ---------+-------------+-------------+-------------+------------+------------+-----------\n")
	for _, w := range writes {
		fmt.Printf("   %-8s | %8.2f µs | %8.2f µs | %8.2f µs | %7.1f Mo/s | %7.1f op | %7.2fx\n",
			formatSize(w.Size), w.PwriteStatsNanos.P50/1000.0, w.FsyncStatsNanos.P50/1000.0, w.TotalStatsNanos.P50/1000.0,
			w.ThroughputMB, w.IOPS, w.Jitter)
	}

	// ÉPREUVE 4 : Recopie de tas globale vs Dirty-Mask SIMD
	fmt.Printf("\n5. Épreuve 4 : Recopie mémoire de tas (CoW) : Brute globale vs Masque SIMD...\n")
	fullCopies, err := RunFullHeapCopyBenchmark(tscFreq)
	if err != nil {
		fmt.Printf("Erreur full copy: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   [A] Recopie intégrale du tas public vers tas sale :\n")
	full64mbNanos := 0.0
	for _, fc := range fullCopies {
		fmt.Printf("       %4d pages (%7s) : %8.2f µs (p99: %8.2f µs) | Débit DRAM: %5.1f Go/s\n",
			fc.Pages, formatSize(fc.SizeBytes), fc.StatsNanos.P50/1000.0, fc.StatsNanos.P99/1000.0, fc.GBPerSec)
		if fc.Pages == 4096 {
			full64mbNanos = fc.StatsNanos.P50
		}
	}

	dirtyCopies, err := RunDirtyMaskCopyBenchmark(tscFreq, full64mbNanos)
	if err != nil {
		fmt.Printf("Erreur dirty mask copy: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   [B] Recopie sélective par Dirty-Mask vectorisé (tzcnt/blsr) dans l'arène de 64 Mio :\n")
	for _, dc := range dirtyCopies {
		fmt.Printf("       %2d pages modifiées (%3s) : %6.2f µs (p99: %6.2f µs) | Accélération vs 64 Mo: %6.1fx\n",
			dc.DirtyPagesCount, formatSize(dc.DirtyPagesCount*benchPageN), dc.StatsNanos.P50/1000.0, dc.StatsNanos.P99/1000.0, dc.SpeedupVsFull64)
	}

	// ÉPREUVE 5 : Contexte transactionnel (goid vs Tx explicite)
	fmt.Printf("\n6. Épreuve 5 : Contexte transactionnel (goid runtime.Stack vs *Tx explicite)...\n")
	txBench, err := RunTxContextBenchmark(tscFreq)
	if err != nil {
		fmt.Printf("Erreur tx bench: %v\n", err)
		os.Exit(1)
	}
	for _, tb := range txBench {
		fmt.Printf("   %-22s : %7.1f cycles | %7.2f ns (p50) | %7.2f ns (p99) | Ratio: %7.1fx\n",
			tb.Name, tb.StatsCycles.P50, tb.StatsNanos.P50, tb.StatsNanos.P99, tb.DeltaVsTx)
	}

	// ÉPREUVE 6 : Balayage fin des frontières 64 Ko à 320 Ko et désalignements
	fmt.Printf("\n7. Épreuve 6 : Balayage fin des frontières 64 Ko à 320 Ko et désalignements...\n")
	boundaries, err := RunFineBoundarySweep(targetDir, tscFreq)
	if err != nil {
		fmt.Printf("Erreur boundary sweep: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   %-32s | %-10s | %-10s | %-10s | %-10s | %-8s\n", "Cas Frontière", "Pwrite p50", "Fsync p50", "Total p50", "Débit Mo/s", "Split ?")
	fmt.Printf("   ---------------------------------+------------+------------+------------+------------+---------\n")
	for _, b := range boundaries {
		splitStr := "NON"
		if b.KernelSplit {
			splitStr = "OUI (scission)"
		}
		fmt.Printf("   %-32s | %7.2f µs | %7.2f µs | %7.2f µs | %7.1f Mo/s | %s\n",
			b.Name, b.PwriteLatencyP50, b.FsyncLatencyP50, b.TotalLatencyP50, b.ThroughputMB, splitStr)
	}

	// GÉNÉRATION DES ARTEFACTS
	report := FullMicrobenchReport{
		Timestamp:          time.Now().UTC().Format(time.RFC3339),
		CPUModel:           runtime.GOARCH,
		CPUCores:           runtime.NumCPU(),
		TSCFrequencyGHz:    tscFreq,
		TargetDirectory:    targetDir,
		CacheLineSplits:    splits,
		NVMeReads:          reads,
		NVMeWrites:         writes,
		FullHeapCopies:     fullCopies,
		DirtyMaskCopies:    dirtyCopies,
		TxContextBenchmark: txBench,
		BoundarySweep:      boundaries,
	}

	jsonPath := "/devhoros/c2simd/c2pkg/c2db/c2db_microbench/results_microbench.json"
	data, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile(jsonPath, data, 0644)

	mdPath := "/devhoros/c2simd/c2pkg/c2db/c2db_microbench/results_microbench.md"
	writeMarkdownReport(mdPath, report)

	fmt.Println("\n================================================================================")
	fmt.Printf("Rapports opposables générés avec succès :\n")
	fmt.Printf("  - JSON : %s\n", jsonPath)
	fmt.Printf("  - Markdown : %s\n", mdPath)
	fmt.Println("================================================================================")
}

func formatSize(b int) string {
	if b >= 1024*1024 {
		return fmt.Sprintf("%d Mo", b/(1024*1024))
	}
	if b >= 1024 {
		return fmt.Sprintf("%d Ko", b/1024)
	}
	return fmt.Sprintf("%d B", b)
}

func writeMarkdownReport(path string, r FullMicrobenchReport) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "# Rapport de Micro-Métrologie Silicium c2db (NVMe & CPU)\n\n")
	fmt.Fprintf(f, "- **Date d'exécution :** %s\n", r.Timestamp)
	fmt.Fprintf(f, "- **Processeur hôte :** %s (%d vCPUs)\n", r.CPUModel, r.CPUCores)
	fmt.Fprintf(f, "- **Fréquence TSC étalonnée :** %.3f GHz (résolution 1 cycle = %.3f ns)\n", r.TSCFrequencyGHz, 1.0/r.TSCFrequencyGHz)
	fmt.Fprintf(f, "- **Système de fichiers sous-jacent :** NVMe PCIe Gen4 x4 monté sur `/devhoros`\n\n")

	fmt.Fprintf(f, "## 1. Pénalité de Cache-Line Split (Alignement L1D 64 octets)\n\n")
	fmt.Fprintf(f, "| Décalage (Octets) | Cycles P50 | Latence P50 (ns) | Latence P99 (ns) | Pénalité vs Aligné |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- |\n")
	for _, s := range r.CacheLineSplits {
		fmt.Fprintf(f, "| +%02d B | %.1f | %.1f ns | %.1f ns | %+.1f%% |\n",
			s.OffsetBytes, s.StatsCycles.P50, s.StatsNanos.P50, s.StatsNanos.P99, s.PenaltyPct)
	}

	fmt.Fprintf(f, "\n## 2. Quantum de Lecture NVMe (O_DIRECT)\n\n")
	fmt.Fprintf(f, "| Taille de bloc | P50 (µs) | P90 (µs) | P99 (µs) | Débit (Mo/s) | Coût (ns / Kio) | Jitter (P99/P50) |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, rd := range r.NVMeReads {
		fmt.Fprintf(f, "| %s | %.2f µs | %.2f µs | %.2f µs | %.1f Mo/s | %.2f ns | %.2fx |\n",
			formatSize(rd.Size), rd.StatsNanos.P50/1000.0, rd.StatsNanos.P90/1000.0, rd.StatsNanos.P99/1000.0,
			rd.ThroughputMB, rd.NanosPerKB, rd.StatsNanos.Jitter)
	}

	fmt.Fprintf(f, "\n## 3. Quantum d'Écriture NVMe (O_DIRECT + fdatasync)\n\n")
	fmt.Fprintf(f, "| Taille de bloc | Pwrite P50 (µs) | Fsync P50 (µs) | Total P50 (µs) | Débit effectif (Mo/s) | IOPS | Jitter (P99/P50) |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, w := range r.NVMeWrites {
		fmt.Fprintf(f, "| %s | %.2f µs | %.2f µs | %.2f µs | %.1f Mo/s | %.1f ops | %.2fx |\n",
			formatSize(w.Size), w.PwriteStatsNanos.P50/1000.0, w.FsyncStatsNanos.P50/1000.0, w.TotalStatsNanos.P50/1000.0,
			w.ThroughputMB, w.IOPS, w.Jitter)
	}

	fmt.Fprintf(f, "\n## 4. Comparatif CoW : Recopie Globale vs Masque SIMD\n\n")
	fmt.Fprintf(f, "### Recopie intégrale du tas (comportement d'origine)\n\n")
	fmt.Fprintf(f, "| Pages copiées | Volume brut | P50 Latence (µs) | P99 Latence (µs) | Débit mémoire DRAM |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- |\n")
	for _, fc := range r.FullHeapCopies {
		fmt.Fprintf(f, "| %d pages | %s | %.2f µs | %.2f µs | %.1f Go/s |\n",
			fc.Pages, formatSize(fc.SizeBytes), fc.StatsNanos.P50/1000.0, fc.StatsNanos.P99/1000.0, fc.GBPerSec)
	}

	fmt.Fprintf(f, "\n### Recopie sélective par Masque Dirty SIMD (tzcnt / blsr)\n\n")
	fmt.Fprintf(f, "| Pages modifiées | Volume effectif | P50 Latence (µs) | P99 Latence (µs) | Accélération vs 64 Mo |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- |\n")
	for _, dc := range r.DirtyMaskCopies {
		fmt.Fprintf(f, "| %d pages | %s | %.2f µs | %.2f µs | %.1fx |\n",
			dc.DirtyPagesCount, formatSize(dc.DirtyPagesCount*benchPageN), dc.StatsNanos.P50/1000.0, dc.StatsNanos.P99/1000.0, dc.SpeedupVsFull64)
	}

	fmt.Fprintf(f, "\n## 5. Contexte Transactionnel : goid() vs *Tx explicite\n\n")
	fmt.Fprintf(f, "| Approche | Cycles P50 | Latence P50 (ns) | Latence P99 (ns) | Coût relatif |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- |\n")
	for _, tx := range r.TxContextBenchmark {
		fmt.Fprintf(f, "| %s | %.1f | %.2f ns | %.2f ns | %.1fx |\n",
			tx.Name, tx.StatsCycles.P50, tx.StatsNanos.P50, tx.StatsNanos.P99, tx.DeltaVsTx)
	}

	fmt.Fprintf(f, "\n## 6. Balayage Fin des Frontières (64 Ko à 320 Ko et Désalignements)\n\n")
	fmt.Fprintf(f, "| Épreuve / Cas Frontière | Pwrite P50 (µs) | Fsync P50 (µs) | Total P50 (µs) | Débit (Mo/s) | Jitter (P99/P50) | Scission Noyau ? |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for _, b := range r.BoundarySweep {
		splitStr := "Non"
		if b.KernelSplit {
			splitStr = "**OUI (> 256 Ko)**"
		}
		fmt.Fprintf(f, "| %s | %.2f µs | %.2f µs | %.2f µs | %.1f Mo/s | %.2fx | %s |\n",
			b.Name, b.PwriteLatencyP50, b.FsyncLatencyP50, b.TotalLatencyP50, b.ThroughputMB, b.Jitter, splitStr)
	}
}
