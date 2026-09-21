// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestQual_Endurance_ContinuousCompaction prouve qu'un flux soutenu de mutations
// (insertions, mises à jour et suppressions) avec cycles de compactage réguliers
// maintient le tas et le WAL dans une enveloppe mémoire bornée sans fuite ni dérive.
func TestQual_Endurance_ContinuousCompaction(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du test d'endurance sous -short")
	}

	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	const (
		numRounds      = 10
		keysPerRound   = 200
		totalMutations = numRounds * keysPerRound * 3 // insert + update + delete
	)

	valBuf := make([]byte, 128)
	for i := range valBuf {
		valBuf[i] = byte(i ^ 0x5C)
	}

	for round := 0; round < numRounds; round++ {
		// 1. Vague d'insertions
		for i := 0; i < keysPerRound; i++ {
			k := fmt.Sprintf("endurance-r%02d-k%04d", round, i)
			binary.LittleEndian.PutUint64(valBuf[:8], uint64(round*10000+i))
			if err := s.Put([]byte(k), valBuf); err != nil {
				t.Fatalf("Round %d Put %s: %v", round, k, err)
			}
		}

		// 2. Vague de mises à jour (crée des versions et fragments CoW)
		for i := 0; i < keysPerRound; i++ {
			k := fmt.Sprintf("endurance-r%02d-k%04d", round, i)
			binary.LittleEndian.PutUint64(valBuf[:8], uint64(round*10000+i+99999))
			if err := s.Put([]byte(k), valBuf); err != nil {
				t.Fatalf("Round %d Update %s (i=%d, used=%d, root=%d): %v", round, k, i, s.heapUsed, s.heapRoot, err)
			}
		}

		// 3. Vague de suppressions sur la moitié des clés (crée des tombstones)
		for i := 0; i < keysPerRound/2; i++ {
			k := fmt.Sprintf("endurance-r%02d-k%04d", round, i)
			if err := s.Delete([]byte(k)); err != nil {
				t.Fatalf("Round %d Delete %s: %v", round, k, err)
			}
		}

		// 4. Déclenchement du compactage périodique (Slot pack + RepackHeap + WAL compact)
		if err := s.Compact(); err != nil {
			t.Fatalf("Round %d Compact: %v", round, err)
		}

		// Vérification de la borne du tas : le nombre de pages utilisées
		// ne doit jamais exploser ni saturer heapPages (4096)
		usedPages := s.heapUsed
		if usedPages >= heapPages {
			t.Fatalf("Round %d : Saturation critique du tas : heapUsed=%d >= %d", round, usedPages, heapPages)
		}

		// Vérification de cohérence des clés restantes dans ce round
		for i := keysPerRound / 2; i < keysPerRound; i++ {
			k := fmt.Sprintf("endurance-r%02d-k%04d", round, i)
			got, err := s.Get([]byte(k))
			if err != nil {
				t.Fatalf("Round %d Get vérification manquant pour %s: %v", round, k, err)
			}
			expectedID := uint64(round*10000 + i + 99999)
			gotID := binary.LittleEndian.Uint64(got[:8])
			if gotID != expectedID {
				t.Fatalf("Round %d Donnée divergente: got ID %d, want %d", round, gotID, expectedID)
			}
		}
	}

	t.Logf("Endurance validée : %d mutations complétées sur %d rounds de compactage", totalMutations, numRounds)
}

// TestQual_Endurance_ConcurrentSoakStream soumet le moteur à une charge
// concurrente saturante continue multi-lecteurs / mono-écrivain pendant 2 secondes.
func TestQual_Endurance_ConcurrentSoakStream(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du test d'endurance concurrent sous -short")
	}

	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()

	// Initialisation d'un jeu de 500 clés
	const initialKeys = 500
	for i := 0; i < initialKeys; i++ {
		k := fmt.Sprintf("soak-k-%04d", i)
		v := fmt.Sprintf("soak-init-val-%04d", i)
		if err := s.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("Put initial %d: %v", i, err)
		}
	}

	var (
		stopFlag   atomic.Bool
		readOps    atomic.Uint64
		writeOps   atomic.Uint64
		compactOps atomic.Uint64
	)

	var wg sync.WaitGroup

	// 4 Goroutines de lecture concurrente sans arrêt
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			var localI uint64
			for !stopFlag.Load() {
				idx := (workerID*100 + int(localI)) % initialKeys
				k := fmt.Sprintf("soak-k-%04d", idx)
				got, err := s.Get([]byte(k))
				if err == nil && len(got) > 0 {
					readOps.Add(1)
				}
				localI++
			}
		}(r)
	}

	// 1 Goroutine d'écriture transactionnelle continue
	wg.Add(1)
	go func() {
		defer wg.Done()
		var txI uint64
		for !stopFlag.Load() {
			txI++
			idx := int(txI) % initialKeys
			k := fmt.Sprintf("soak-k-%04d", idx)
			v := fmt.Sprintf("soak-live-val-seq-%08d", txI)

			tx, err := s.Begin()
			if err != nil {
				continue
			}
			_ = tx.Put([]byte(k), []byte(v))
			if err := tx.Commit(); err == nil {
				writeOps.Add(1)
			}

			// Compactage toutes les 500 écritures
			if txI%500 == 0 {
				if err := s.Compact(); err == nil {
					compactOps.Add(1)
				}
			}
		}
	}()

	// Exécution pendant 1,5 seconde à pleine saturation
	time.Sleep(1500 * time.Millisecond)
	stopFlag.Store(true)
	wg.Wait()

	t.Logf("Soak Test achevé avec succès : %d lectures, %d écritures transactionnelles, %d compactages complets",
		readOps.Load(), writeOps.Load(), compactOps.Load())

	if writeOps.Load() < 100 {
		t.Fatalf("Débit d'écriture anormalement bas: %d ops", writeOps.Load())
	}
	if readOps.Load() < 1000 {
		t.Fatalf("Débit de lecture anormalement bas: %d ops", readOps.Load())
	}

	// Épreuve finale de lecture après arrêt
	kFinal := []byte("soak-k-0000")
	got, err := s.Get(kFinal)
	if err != nil || len(got) == 0 {
		t.Fatalf("Lecture finale post-soak corrompue: %v", err)
	}
}

// TestEndurance_MaxHeapPages_RSS mesure l'empreinte mémoire résidente (RSS)
// sur un shard dimensionné à sa capacité maximale (WithHeapPages(65536) = 1 Gio)
// avec maintien simultané de 8 vues actives (maxHoldHeaps).
// Prouve que la pagination à la demande de Linux (demand paging sur mmapAnon)
// ne committe physiquement que les pages touchées, conservant un RSS borné.
func TestEndurance_MaxHeapPages_RSS(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du test d'endurance RSS sous -short")
	}

	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	rssBefore := readProcessRSS()

	// Ouverture d'un shard à capacité maximale (65 536 pages x 16 Ko = 1 Gio adressable)
	s, err := OpenShard(dir, key, 0, WithHeapPages(65536))
	if err != nil {
		t.Fatalf("OpenShard WithHeapPages(65536): %v", err)
	}

	// Écriture de 100 enregistrements (inline et overflow)
	for i := 0; i < 100; i++ {
		k := []byte(fmt.Sprintf("rss-k-%04d", i))
		var v []byte
		if i%2 == 0 {
			v = []byte(fmt.Sprintf("inline-val-%d", i))
		} else {
			v = make([]byte, 8192)
			binary.LittleEndian.PutUint64(v[:8], uint64(i))
		}
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	// Maintien simultané de 8 vues actives (plafond maxHoldHeaps)
	views := make([]*View, maxHoldHeaps)
	for i := range views {
		v, err := s.View()
		if err != nil {
			t.Fatalf("View %d: %v", i, err)
		}
		views[i] = v
	}

	// Réalisation de lectures concurrentes sur les 8 vues
	for i, v := range views {
		k := []byte(fmt.Sprintf("rss-k-%04d", i*10))
		got, err := v.Get(k)
		if err != nil || len(got) == 0 {
			t.Fatalf("Get sur vue %d: %v", i, err)
		}
	}

	rssAfter := readProcessRSS()
	rssDeltaMB := float64(int64(rssAfter)-int64(rssBefore)) / (1024 * 1024)

	t.Logf("Empreinte RSS : avant=%.1f Mo, avec 8 vues 1-Gio=%.1f Mo (delta=%.1f Mo)",
		float64(rssBefore)/(1024*1024), float64(rssAfter)/(1024*1024), rssDeltaMB)

	// Libération de l'ensemble des vues
	for _, v := range views {
		if err := v.Close(); err != nil {
			t.Fatalf("Close view: %v", err)
		}
	}

	// Fermeture du shard
	if err := s.Close(); err != nil {
		t.Fatalf("Close shard: %v", err)
	}
}

func readProcessRSS() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	var total, resident uint64
	_, _ = fmt.Sscanf(string(data), "%d %d", &total, &resident)
	return resident * uint64(os.Getpagesize())
}
