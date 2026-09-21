// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
	"golang.org/x/sys/unix"
)

// TestQual_07_DistributionUniformity valide métrologiquement l'uniformité du
// partitionnement sur les 1024 shards de c2db via le test d'adéquation du Chi-deux (Pearson).
//
// Métrologie :
//   - Échantillon N = 100 000 clés arbitraires ou identifiants.
//   - Nombre de classes K = 1024 shards (indices 0 à 1023).
//   - Fréquence théorique sous H0 d'équirépartition : E = N / K = 100 000 / 1024 = 97.65625.
//   - Statistique observée : \chi^2 = \sum_{i=0}^{1023} \frac{(O_i - E)^2}{E}.
//   - Degrés de liberté : \nu = K - 1 = 1023.
//   - Espérance : \mu = 1023, Variance : \sigma^2 = 2046 (\sigma \approx 45.23).
//   - Intervalle de confiance à 99% bilatéral : [910.1, 1142.3] (encadré par la garde [850, 1200]).
func TestQual_07_DistributionUniformity(t *testing.T) {
	const (
		totalKeys = 100_000
		numShards = NumShards                               // 1024
		expectedE = float64(totalKeys) / float64(numShards) // 97.65625
		minChi2   = 850.0
		maxChi2   = 1200.0
	)

	// Scénario A : Équirépartition du routage par hachage cryptographique Blake3 mod 1024.
	t.Run("Blake3_Route", func(t *testing.T) {
		var counts [numShards]int
		var keyBuf [8]byte
		for i := 0; i < totalKeys; i++ {
			binary.LittleEndian.PutUint64(keyBuf[:], uint64(i))
			shard := Route(keyBuf[:])
			if shard >= numShards {
				t.Fatalf("shard %d >= %d", shard, numShards)
			}
			counts[shard]++
		}

		var chi2 float64
		for i := 0; i < numShards; i++ {
			if counts[i] == 0 {
				t.Fatalf("shard %d vide : rupture d'uniformité de couverture", i)
			}
			diff := float64(counts[i]) - expectedE
			chi2 += (diff * diff) / expectedE
		}

		t.Logf("Blake3_Route: \u03c7\u00b2 = %.4f (intervalle toléré [%.1f, %.1f], E = %.5f, 1023 ddl)", chi2, minChi2, maxChi2, expectedE)
		if chi2 < minChi2 || chi2 > maxChi2 {
			t.Fatalf("\u03c7\u00b2 = %.4f hors de l'intervalle de confiance [%.1f, %.1f]", chi2, minChi2, maxChi2)
		}
	})

	// Scénario B : Équirépartition des identifiants UUIDv7 projetés par ShardOf.
	t.Run("UUIDv7_ShardOf", func(t *testing.T) {
		var counts [numShards]int
		for i := 0; i < totalKeys; i++ {
			id := c2uuidv7.NewV7Fast()
			shard := ShardOf(id)
			if shard >= numShards {
				t.Fatalf("shard %d >= %d", shard, numShards)
			}
			counts[shard]++
		}

		var chi2 float64
		for i := 0; i < numShards; i++ {
			if counts[i] == 0 {
				t.Fatalf("shard %d vide : rupture d'uniformité de couverture", i)
			}
			diff := float64(counts[i]) - expectedE
			chi2 += (diff * diff) / expectedE
		}

		t.Logf("UUIDv7_ShardOf: \u03c7\u00b2 = %.4f (intervalle toléré [%.1f, %.1f], E = %.5f, 1023 ddl)", chi2, minChi2, maxChi2, expectedE)
		if chi2 < minChi2 || chi2 > maxChi2 {
			t.Fatalf("\u03c7\u00b2 = %.4f hors de l'intervalle de confiance [%.1f, %.1f]", chi2, minChi2, maxChi2)
		}
	})
}

// openTestShard initialise un Shard dans un sous-répertoire isolé avec WAL pré-alloué
// pour garantir l'exécution optimale sous synctest sans allocation superflue.
func openTestShard(t *testing.T, dir string, key [32]byte, id uint16, walSize uint64) *Shard {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("os.MkdirAll %s: %v", dir, err)
	}
	w, err := CreateWAL(filepath.Join(dir, "wal.img"), walSize, key)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers (EINVAL à l'ouverture)")
		}
		t.Fatalf("CreateWAL shard %d: %v", id, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("CreateWAL Close shard %d: %v", id, err)
	}
	s := mustOpenShard(t, dir, key, id)
	s.SetBusyTimeout(100 * time.Millisecond)
	return s
}

// TestQual_11_Synctest_10kSimulatedAccesses simule 10 000 requêtes réparties sur plusieurs
// shards par vagues de goroutines concurrentes dans l'environnement synthétique Go 1.27 synctest.
//
// Propriétés démontrées :
//   - Absence totale d'interblocage (deadlock) : détecté et prouvé par synctest.Wait().
//   - Absence de panique sous concurrence agressive.
//   - Cohérence stricte de lecture après écriture (Read-After-Write bit-exact).
//   - Absence de data race validée sous `go test -race`.
func TestQual_11_Synctest_10kSimulatedAccesses(t *testing.T) {
	const (
		numShards     = 8
		totalExpected = 10_000
	)

	dir := t.TempDir()
	var masterKey [32]byte
	for i := range masterKey {
		masterKey[i] = byte(i + 0x42)
	}

	shards := make([]*Shard, numShards)
	for i := 0; i < numShards; i++ {
		sDir := filepath.Join(dir, fmt.Sprintf("%04x", i))
		shards[i] = openTestShard(t, sDir, masterKey, uint16(i), 512*1024)
	}
	defer func() {
		for _, s := range shards {
			if s != nil {
				_ = s.Close()
			}
		}
	}()

	synctest.Test(t, func(t *testing.T) {
		var reqCount atomic.Int64
		var store sync.Map

		// -------------------------------------------------------------------------
		// Vague 1 : Ensemencement concurrent et validation immédiate de relecture
		// 8 shards x (300 Put + 300 Get immédiat) = 4 800 requêtes
		// -------------------------------------------------------------------------
		const (
			w1Shards = numShards
			w1Iters  = 300
		)
		for sIdx := 0; sIdx < w1Shards; sIdx++ {
			go func(shardID int) {
				sh := shards[shardID]
				for j := 0; j < w1Iters; j++ {
					key := []byte(fmt.Sprintf("s%02d_k_%04d", shardID, j))
					val := []byte(fmt.Sprintf("s%02d_v_%04d_seeded", shardID, j))

					// Écriture
					if err := sh.Put(key, val); err != nil {
						t.Errorf("Vague 1 Put shard=%d j=%d err=%v", shardID, j, err)
						return
					}
					reqCount.Add(1)
					store.Store(string(key), val)

					// Cohérence stricte de lecture immédiate (Read-Your-Own-Writes)
					got, err := sh.Get(key)
					if err != nil {
						t.Errorf("Vague 1 Get shard=%d j=%d err=%v", shardID, j, err)
						return
					}
					if !bytes.Equal(got, val) {
						t.Errorf("Vague 1 incohérence: shard=%d j=%d got=%q want=%q", shardID, j, got, val)
						return
					}
					reqCount.Add(1)
				}
			}(sIdx)
		}
		synctest.Wait()

		// -------------------------------------------------------------------------
		// Vague 2 : Charge mixte concurrente (Lectures croisées + Nouvelles Écritures)
		// 2 400 lectures croisées + 800 opérations d'écriture/relecture = 3 200 requêtes
		// -------------------------------------------------------------------------

		// 2.1 : 16 goroutines de lectures intensives croisées (16 workers x 150 lectures = 2 400 requêtes)
		const (
			w2aWorkers = 16
			w2aReads   = 150
		)
		for w := 0; w < w2aWorkers; w++ {
			go func(workerID int) {
				for j := 0; j < w2aReads; j++ {
					targetShard := (workerID*3 + j*7) % w1Shards
					targetIter := (workerID*19 + j*13) % w1Iters
					key := []byte(fmt.Sprintf("s%02d_k_%04d", targetShard, targetIter))
					sh := shards[targetShard]

					rawVal, ok := store.Load(string(key))
					if !ok {
						t.Errorf("Vague 2A clé manquante: %s", key)
						return
					}
					want := rawVal.([]byte)

					got, err := sh.Get(key)
					if err != nil {
						t.Errorf("Vague 2A Get err: %v clé=%s", err, key)
						return
					}
					if !bytes.Equal(got, want) {
						t.Errorf("Vague 2A incohérence: got=%q want=%q clé=%s", got, want, key)
						return
					}
					reqCount.Add(1)
				}
			}(w)
		}

		// 2.2 : Écritures supplémentaires concurrentes de nouvelles clés + relecture immédiate
		// 8 shards x (50 Put + 50 Get) = 800 requêtes
		const (
			w2bShards = numShards
			w2bIters  = 50
		)
		for sIdx := 0; sIdx < w2bShards; sIdx++ {
			go func(shardID int) {
				sh := shards[shardID]
				for j := 0; j < w2bIters; j++ {
					key := []byte(fmt.Sprintf("s%02d_upd_%04d", shardID, j))
					val := []byte(fmt.Sprintf("s%02d_updval_%04d", shardID, j))

					if err := sh.Put(key, val); err != nil {
						t.Errorf("Vague 2B Put shard=%d j=%d err=%v", shardID, j, err)
						return
					}
					reqCount.Add(1)
					store.Store(string(key), val)

					got, err := sh.Get(key)
					if err != nil {
						t.Errorf("Vague 2B Get shard=%d j=%d err=%v", shardID, j, err)
						return
					}
					if !bytes.Equal(got, val) {
						t.Errorf("Vague 2B incohérence: shard=%d j=%d got=%q want=%q", shardID, j, got, val)
						return
					}
					reqCount.Add(1)
				}
			}(sIdx)
		}
		synctest.Wait()

		// -------------------------------------------------------------------------
		// Vague 3 : Validation finale exhaustive croisée = 2 000 requêtes
		// 20 workers x 100 lectures réparties sur toutes les clés générées
		// -------------------------------------------------------------------------
		const (
			w3Workers = 20
			w3Reads   = 100
		)
		for w := 0; w < w3Workers; w++ {
			go func(workerID int) {
				for j := 0; j < w3Reads; j++ {
					var targetShard int
					var key []byte
					if j%2 == 0 {
						targetShard = (workerID*5 + j*3) % w1Shards
						ti := (workerID*11 + j*17) % w1Iters
						key = []byte(fmt.Sprintf("s%02d_k_%04d", targetShard, ti))
					} else {
						targetShard = (workerID*7 + j*13) % w2bShards
						ti := (workerID*19 + j*23) % w2bIters
						key = []byte(fmt.Sprintf("s%02d_upd_%04d", targetShard, ti))
					}

					rawVal, ok := store.Load(string(key))
					if !ok {
						t.Errorf("Vague 3 clé introuvable: %s", key)
						return
					}
					want := rawVal.([]byte)

					sh := shards[targetShard]
					got, err := sh.Get(key)
					if err != nil {
						t.Errorf("Vague 3 Get err: %v clé=%s", err, key)
						return
					}
					if !bytes.Equal(got, want) {
						t.Errorf("Vague 3 incohérence: got=%q want=%q clé=%s", got, want, key)
						return
					}
					reqCount.Add(1)
				}
			}(w)
		}
		synctest.Wait()

		totalDone := reqCount.Load()
		t.Logf("Simulation concurrente achevée : %d requêtes validées avec succès sur %d shards", totalDone, numShards)
		if totalDone != totalExpected {
			t.Fatalf("Total requêtes exécutées = %d, attendu = %d", totalDone, totalExpected)
		}
	})
}
