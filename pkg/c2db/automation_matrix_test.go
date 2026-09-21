// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2db"
)

func testKey(s string) [32]byte {
	return sha256.Sum256([]byte("c2db_test_automation/" + s))
}

// TestAutomation_SnapshotIsolation valide strictement la sémantique MVCC Snapshot Isolation via View :
// Une View V1 ouverte avant des Put postérieurs ne doit JAMAIS voir les modifications ultérieures.
func TestAutomation_SnapshotIsolation(t *testing.T) {
	dir := t.TempDir()
	key := testKey("snap_iso")

	sh, err := c2db.OpenShard(dir, key, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(4*1024*1024))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer sh.Close()

	// 1. Initialiser une clé "config/timeout" = "10s"
	if err := sh.Put([]byte("config/timeout"), []byte("10s")); err != nil {
		t.Fatalf("Put config/timeout: %v", err)
	}

	// 2. Ouvrir View v1 à t1
	v1, err := sh.View()
	if err != nil {
		t.Fatalf("View v1: %v", err)
	}
	defer v1.Close()

	// 3. Effectuer des écritures à t2 : modifier "config/timeout" = "60s" et ajouter "config/retries" = "3"
	if err := sh.Put([]byte("config/timeout"), []byte("60s")); err != nil {
		t.Fatalf("Put timeout 60s: %v", err)
	}
	if err := sh.Put([]byte("config/retries"), []byte("3")); err != nil {
		t.Fatalf("Put retries: %v", err)
	}

	// 4. Vérifier que v1 lit toujours l'état à t1 ("10s" et "config/retries" inexistant)
	valTimeout, err := v1.Get([]byte("config/timeout"))
	if err != nil {
		t.Fatalf("v1 Get timeout: %v", err)
	}
	if string(valTimeout) != "10s" {
		t.Fatalf("violation Snapshot Isolation: v1 a lu %q, attendu '10s'", string(valTimeout))
	}

	_, errRetries := v1.Get([]byte("config/retries"))
	if errRetries == nil {
		t.Fatalf("violation Snapshot Isolation: v1 a vu 'config/retries' committé postérieurement")
	}

	// 5. Une nouvelle View v2 ou sh.Get direct doit voir l'état à jour
	valDirect, err := sh.Get([]byte("config/timeout"))
	if err != nil || string(valDirect) != "60s" {
		t.Fatalf("sh.Get direct attendu '60s', obtenu %q (err: %v)", string(valDirect), err)
	}
}

// TestAutomation_SingleKeyRepeatedUpdates vérifie la gestion de l'écrasement intensif d'une même clé :
// 200 mises à jour successives pour éprouver les chaînes MVCC et l'absence de split parasite.
func TestAutomation_SingleKeyRepeatedUpdates(t *testing.T) {
	dir := t.TempDir()
	key := testKey("single_key_rapid")

	sh, err := c2db.OpenShard(dir, key, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer sh.Close()

	k := []byte("metrics/counter_rapid")
	const iterations = 200

	for i := 0; i < iterations; i++ {
		val := []byte(fmt.Sprintf("val_%08d", i))
		if err := sh.Put(k, val); err != nil {
			t.Fatalf("Put i=%d: %v", i, err)
		}
	}

	// Vérification finale
	finalVal, err := sh.Get(k)
	if err != nil {
		t.Fatalf("Get final: %v", err)
	}
	expected := fmt.Sprintf("val_%08d", iterations-1)
	if string(finalVal) != expected {
		t.Fatalf("valeur finale altérée: obtenu %q, attendu %q", string(finalVal), expected)
	}
}

// TestAutomation_WALReplayIntegrity vérifie la persistance intégrale des transactions après fermeture
// et rejeu à froid du WAL lors de la réouverture.
func TestAutomation_WALReplayIntegrity(t *testing.T) {
	dir := t.TempDir()
	key := testKey("wal_replay")

	const numKeys = 500
	{
		sh, err := c2db.OpenShard(dir, key, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
		if err != nil {
			t.Fatalf("OpenShard 1: %v", err)
		}

		pairs := make([][2][]byte, numKeys)
		for i := 0; i < numKeys; i++ {
			k := []byte(fmt.Sprintf("cluster/node_%04d/status", i))
			v := []byte(fmt.Sprintf("active_seq_%08d", i*42))
			pairs[i] = [2][]byte{k, v}
		}
		if err := sh.PutBatch(pairs); err != nil {
			t.Fatalf("PutBatch: %v", err)
		}
		if err := sh.Close(); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
	}

	// Réouverture à froid : le moteur recharge le tas et applique le WAL
	{
		sh2, err := c2db.OpenShard(dir, key, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
		if err != nil {
			t.Fatalf("OpenShard 2 (recharge): %v", err)
		}
		defer sh2.Close()

		for i := 0; i < numKeys; i++ {
			k := []byte(fmt.Sprintf("cluster/node_%04d/status", i))
			expected := fmt.Sprintf("active_seq_%08d", i*42)
			v, err := sh2.Get(k)
			if err != nil {
				t.Fatalf("Get post-recharge key %d: %v", i, err)
			}
			if string(v) != expected {
				t.Fatalf("clé %d altérée après recharge: obtenu %q, attendu %q", i, string(v), expected)
			}
		}
	}
}

// TestAutomation_ConcurrentReadersWriters teste 16 lecteurs concurrents en présence d'écritures continues
// sous détection stricte de race conditions (-race).
func TestAutomation_ConcurrentReadersWriters(t *testing.T) {
	dir := t.TempDir()
	key := testKey("concurrency")

	sh, err := c2db.OpenShard(dir, key, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer sh.Close()

	// Initialiser 50 clés
	for i := 0; i < 50; i++ {
		_ = sh.Put([]byte(fmt.Sprintf("item_%03d", i)), []byte("initial_value"))
	}

	var wgWriter sync.WaitGroup
	var wgReaders sync.WaitGroup
	stopWriters := make(chan struct{})

	// 1 goroutine écrivain
	wgWriter.Add(1)
	go func() {
		defer wgWriter.Done()
		seq := 0
		for {
			select {
			case <-stopWriters:
				return
			default:
				k := []byte(fmt.Sprintf("item_%03d", seq%50))
				_ = sh.Put(k, []byte(fmt.Sprintf("update_%d", seq)))
				seq++
			}
		}
	}()

	// 16 goroutines lectrices
	const numReaders = 16
	for r := 0; r < numReaders; r++ {
		wgReaders.Add(1)
		go func(id int) {
			defer wgReaders.Done()
			for iter := 0; iter < 50; iter++ {
				k := []byte(fmt.Sprintf("item_%03d", (id+iter)%50))
				val, err := sh.Get(k)
				if err == nil && len(val) == 0 {
					t.Errorf("lecteur %d a lu une valeur vide pour %s", id, k)
				}
			}
		}(r)
	}

	wgReaders.Wait()
	close(stopWriters)
	wgWriter.Wait()
}

// TestAutomation_MultiLevelPageSplits insère 500 clés volumineuses avec distribution dispersée
// pour forcer des dizaines de scissions de pages B-Tree consécutives et vérifier l'intégrité globale.
func TestAutomation_MultiLevelPageSplits(t *testing.T) {
	dir := t.TempDir()
	key := testKey("multi_splits")

	sh, err := c2db.OpenShard(dir, key, 1, c2db.WithHeapPages(4096), c2db.WithWALBytes(8*1024*1024))
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}
	defer sh.Close()

	const totalKeys = 500
	payload := bytes.Repeat([]byte("Z"), 256) // 256 octets par valeur

	pairs := make([][2][]byte, totalKeys)
	for i := 0; i < totalKeys; i++ {
		k := []byte(fmt.Sprintf("partition_%03d/segment_%04d/key_%08d", (i*7)%97, (i*13)%509, i))
		pairs[i] = [2][]byte{k, payload}
	}

	if err := sh.PutBatch(pairs); err != nil {
		t.Fatalf("PutBatch large split: %v", err)
	}

	// Vérifier l'intégralité des clés
	for i := 0; i < totalKeys; i++ {
		k := []byte(fmt.Sprintf("partition_%03d/segment_%04d/key_%08d", (i*7)%97, (i*13)%509, i))
		v, err := sh.Get(k)
		if err != nil {
			t.Fatalf("clé perdue après splits multiples %d: %v", i, err)
		}
		if !bytes.Equal(v, payload) {
			t.Fatalf("payload altéré pour clé %d", i)
		}
	}
}
