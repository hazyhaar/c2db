// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2db"
)

func fuzzMasterKey(salt string) [32]byte {
	return sha256.Sum256([]byte("c2db/fuzzing_department/" + salt))
}

// FuzzKeysValues éprouve la robustesse du moteur C2DB face à des clés et valeurs arbitraires :
// clés nulles, séparateurs complexes, clés de 1024 octets, grandes valeurs (>4Ko) déclenchant les pages d'overflow.
func FuzzKeysValues(f *testing.F) {
	// Seeds de clés et valeurs caractéristiques
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("alpha"), []byte("beta"))
	f.Add([]byte("key\x00with\x00nulls"), []byte("val\x00with\x00nulls"))
	f.Add([]byte("vfs:1000:my_database_file_id-journal:btree_root"), []byte("vfs_metadata_payload"))
	f.Add(bytes.Repeat([]byte("K"), 1024), []byte("value_for_1024_bytes_key"))
	f.Add([]byte("overflow_key"), bytes.Repeat([]byte("O"), 8192))
	f.Add(bytes.Repeat([]byte{0xFF}, 64), bytes.Repeat([]byte{0x00}, 256))
	f.Add([]byte("/api/v1/metrics?node=42&cluster=prod#anchor"), []byte(`{"status":"healthy","uptime":99.99}`))

	f.Fuzz(func(t *testing.T, key, val []byte) {
		// Limite structurelle de l'en-tête de cellule B-Tree : taille maximale uint16 (65535)
		if len(key) > 65535 || len(val) > 65535 {
			return
		}

		dir := t.TempDir()
		keyMaster := fuzzMasterKey("keys_values")

		sh, err := c2db.OpenShard(dir, keyMaster, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
		if err != nil {
			t.Fatalf("OpenShard échec d'initialisation: %v", err)
		}
		defer sh.Close()

		putErr := sh.Put(key, val)
		if len(key) == 0 {
			// Une clé vide doit être systématiquement rejetée sans panique
			if putErr == nil {
				t.Fatalf("Put a accepté une clé vide de longueur 0")
			}
			return
		}

		if putErr != nil {
			// Si le Put a échoué (ex: tas saturé ou contrainte de taille), le moteur ne doit pas paniquer
			return
		}

		// Vérification de lecture directe exacte
		gotVal, getErr := sh.Get(key)
		if getErr != nil {
			t.Fatalf("Get après Put réussi a échoué: %v (clé len=%d)", getErr, len(key))
		}
		if !bytes.Equal(gotVal, val) {
			t.Fatalf("corruption de valeur lue: attendu len=%d, obtenu len=%d", len(val), len(gotVal))
		}

		// Vérification via View snapshot MVCC
		v, vErr := sh.View()
		if vErr == nil {
			viewVal, vGetErr := v.Get(key)
			if vGetErr == nil && !bytes.Equal(viewVal, val) {
				_ = v.Close()
				t.Fatalf("View MVCC a retourné une valeur divergente: len=%d vs len=%d", len(viewVal), len(val))
			}
			_ = v.Close()
		}

		// Vérification de suppression
		delErr := sh.Delete(key)
		if delErr == nil {
			afterVal, afterErr := sh.Get(key)
			if afterErr == nil && bytes.Equal(afterVal, val) {
				t.Fatalf("la clé est toujours présente avec son ancienne valeur après Delete")
			}
		}
	})
}

// FuzzMVCCConcurrency éprouve les transactions MVCC concurrentes sous permutations aléatoires :
// exécutions entrelacées de Get, Put, Delete, Commit et Rollback sur un ensemble de clés concurrentes.
func FuzzMVCCConcurrency(f *testing.F) {
	// Graines de séquences d'opérations concurrentes
	f.Add([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07})
	f.Add(bytes.Repeat([]byte{0xAA, 0x55, 0x12, 0x34}, 8))
	f.Add([]byte{0x00, 0x00, 0x01, 0x01, 0x02, 0x02, 0x03, 0x03, 0x04, 0x04, 0x05, 0x05})

	f.Fuzz(func(t *testing.T, cmdStream []byte) {
		if len(cmdStream) < 4 {
			return
		}

		dir := t.TempDir()
		keyMaster := fuzzMasterKey("mvcc_concurrency")

		sh, err := c2db.OpenShard(dir, keyMaster, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
		if err != nil {
			t.Fatalf("OpenShard: %v", err)
		}
		defer sh.Close()
		sh.SetBusyTimeout(50 * time.Millisecond)

		keys := [][]byte{
			[]byte("concurrent_k0"),
			[]byte("concurrent_k1"),
			[]byte("concurrent_k2"),
			[]byte("concurrent_k3"),
		}

		// Initialisation déterministe des clés
		for _, k := range keys {
			_ = sh.Put(k, []byte("init_val"))
		}

		const numWorkers = 3
		var wg sync.WaitGroup
		chunkSize := len(cmdStream) / numWorkers
		if chunkSize == 0 {
			chunkSize = 1
		}

		for w := 0; w < numWorkers; w++ {
			start := w * chunkSize
			end := start + chunkSize
			if end > len(cmdStream) {
				end = len(cmdStream)
			}
			workerStream := cmdStream[start:end]

			wg.Add(1)
			go func(stream []byte, workerID int) {
				defer wg.Done()
				for i := 0; i+1 < len(stream); i += 2 {
					op := stream[i] % 6
					k := keys[stream[i+1]%byte(len(keys))]

					switch op {
					case 0:
						// Tx Put + Commit
						tx, bErr := sh.Begin()
						if bErr == nil {
							_ = tx.Put(k, []byte(fmt.Sprintf("w%d_v%d", workerID, stream[i])))
							_ = tx.Commit()
						}
					case 1:
						// Tx Put + Rollback
						tx, bErr := sh.Begin()
						if bErr == nil {
							_ = tx.Put(k, []byte("ephemeral_uncommitted"))
							_ = tx.Rollback()
						}
					case 2:
						// Tx Delete + Commit
						tx, bErr := sh.Begin()
						if bErr == nil {
							_ = tx.Delete(k)
							_ = tx.Commit()
						}
					case 3:
						// Tx Delete + Rollback
						tx, bErr := sh.Begin()
						if bErr == nil {
							_ = tx.Delete(k)
							_ = tx.Rollback()
						}
					case 4:
						// Shard.Get direct
						_, _ = sh.Get(k)
					case 5:
						// View MVCC snapshot
						v, vErr := sh.View()
						if vErr == nil {
							_, _ = v.Get(k)
							_ = v.Close()
						}
					}
				}
			}(workerStream, w)
		}

		wg.Wait()

		// Vérification finale d'absence de crash et intégrité structurelle
		for _, k := range keys {
			_, _ = sh.Get(k)
		}
	})
}

// FuzzWALCrashRecovery éprouve la restauration après crash et l'intégrité du journal WAL
// face à des fragments tronqués, bit flips et corruptions de trames de log.
func FuzzWALCrashRecovery(f *testing.F) {
	// Graines de corruptions
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})
	f.Add([]byte{0xFF, 0xAA, 0x55, 0x00, 0x12, 0x34})
	f.Add(bytes.Repeat([]byte{0xCC}, 32))

	f.Fuzz(func(t *testing.T, corruptData []byte) {
		if len(corruptData) < 4 {
			return
		}

		dir := t.TempDir()
		keyMaster := fuzzMasterKey("wal_crash")

		// 1. Initialiser une base avec plusieurs transactions confirmées
		{
			sh, err := c2db.OpenShard(dir, keyMaster, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
			if err != nil {
				t.Fatalf("OpenShard: %v", err)
			}
			for i := 0; i < 6; i++ {
				k := []byte(fmt.Sprintf("crash_k_%d", i))
				v := []byte(fmt.Sprintf("crash_val_%d", i))
				if err := sh.Put(k, v); err != nil {
					_ = sh.Close()
					t.Fatalf("Put: %v", err)
				}
			}
			if err := sh.Close(); err != nil {
				t.Fatalf("Close avant corruption: %v", err)
			}
		}

		walPath := filepath.Join(dir, "wal.img")
		fi, err := os.Stat(walPath)
		if err != nil {
			t.Fatalf("stat wal.img: %v", err)
		}
		fileSize := fi.Size()
		if fileSize == 0 {
			return
		}

		// 2. Appliquer une corruption contrôlée selon le flux corruptData
		mode := corruptData[0] % 3
		targetOffset := int64(binary.LittleEndian.Uint32(corruptData[:4]) % uint32(fileSize))

		switch mode {
		case 0:
			// Troncature brutale du fichier WAL (panne d'alimentation au milieu d'un bloc)
			if targetOffset > 0 {
				_ = os.Truncate(walPath, targetOffset)
			}
		case 1:
			// Bit flip sur trame (corruption physique du stockage)
			f, oErr := os.OpenFile(walPath, os.O_RDWR, 0o600)
			if oErr == nil {
				var b [1]byte
				if _, rErr := f.ReadAt(b[:], targetOffset); rErr == nil {
					b[0] ^= 0xFF
					_, _ = f.WriteAt(b[:], targetOffset)
				}
				_ = f.Close()
			}
		case 2:
			// Injection d'octets arbitraires à l'offset ciblé
			f, oErr := os.OpenFile(walPath, os.O_RDWR, 0o600)
			if oErr == nil {
				injectLen := len(corruptData)
				if int64(injectLen) > fileSize-targetOffset {
					injectLen = int(fileSize - targetOffset)
				}
				if injectLen > 0 {
					_, _ = f.WriteAt(corruptData[:injectLen], targetOffset)
				}
				_ = f.Close()
			}
		}

		// 3. Réouverture à froid : le moteur DOIT rejeter proprement ou récupérer sans panique
		reopenedSh, openErr := c2db.OpenShard(dir, keyMaster, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
		if openErr != nil {
			// En cas d'erreur, celle-ci doit être contrôlée (pas de panique non attrapée)
			return
		}
		defer reopenedSh.Close()

		// Si la réouverture a réussi, le moteur doit pouvoir lire et écrire sans panique
		for i := 0; i < 6; i++ {
			k := []byte(fmt.Sprintf("crash_k_%d", i))
			_, _ = reopenedSh.Get(k)
		}

		// Vérifier la capacité à continuer d'écrire après le rejeu
		_ = reopenedSh.Put([]byte("post_recovery_key"), []byte("post_recovery_value"))
	})
}
