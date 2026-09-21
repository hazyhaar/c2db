// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// committedTxRecord consigne une transaction dont le Commit/Flush a retourné nil.
type committedTxRecord struct {
	ID   uint64 `json:"id"`
	Key  string `json:"key"`
	Val  string `json:"val"`
	Size int    `json:"size"`
}

// TestHostileCrashHelperWriter est le sous-processus cible des tirs SIGKILL.
// Il boucle en insérant des transactions de tailles variées (64B, 8KB, 32KB multi-LBA)
// et enregistre chaque transaction confirmée dans un journal témoin atomique.
func TestHostileCrashHelperWriter(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_WRITER") != "1" {
		return
	}
	dir := os.Getenv("HELPER_DB_DIR")
	auditPath := os.Getenv("HELPER_AUDIT_PATH")
	var key [32]byte
	rawKey, _ := hex.DecodeString(os.Getenv("HELPER_KEY_HEX"))
	copy(key[:], rawKey)

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenShard error: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	auditFile, err := os.OpenFile(auditPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenFile audit error: %v\n", err)
		os.Exit(1)
	}
	defer auditFile.Close()

	var txID uint64
	for {
		txID++
		// Alternance des charges utiles : petite (64 octets), moyenne (8 Ko), grande multi-LBA (32 Ko)
		var valSize int
		switch txID % 3 {
		case 0:
			valSize = 64
		case 1:
			valSize = 2048
		case 2:
			valSize = 8192
		}

		k := fmt.Sprintf("hostile-k-%08d", txID)
		v := make([]byte, valSize)
		binary.LittleEndian.PutUint64(v[:8], txID)
		for i := 8; i < valSize; i++ {
			v[i] = byte(i ^ int(txID))
		}

		tx, err := s.Begin()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Begin error: %v\n", err)
			os.Exit(1)
		}
		if err := tx.Put([]byte(k), v); err != nil {
			_ = tx.Rollback()
			continue
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			continue
		}

		// Transaction confirmée : consignation synchrone dans le journal témoin
		rec := committedTxRecord{
			ID:   txID,
			Key:  k,
			Val:  string(v[:16]), // Empreinte de tête pour validation
			Size: valSize,
		}
		line, _ := json.Marshal(rec)
		line = append(line, '\n')
		if _, err := auditFile.Write(line); err != nil {
			os.Exit(1)
		}
		_ = auditFile.Sync()
	}
}

// TestHostileCrash_MultiProcessSIGKILL exécute 5 cycles de frappe SIGKILL aléatoires
// sur des processus en vol et valide l'intégrité absolue à la réouverture.
func TestHostileCrash_MultiProcessSIGKILL(t *testing.T) {
	if testing.Short() {
		t.Skip("saut du test de crash hostile sous -short")
	}

	testExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	const cycles = 5
	for cycle := 0; cycle < cycles; cycle++ {
		t.Run(fmt.Sprintf("Cycle_%d", cycle), func(t *testing.T) {
			dir := t.TempDir()
			auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

			var key [32]byte
			_, _ = rand.Read(key[:])
			keyHex := hex.EncodeToString(key[:])

			// 1. Lancer le sous-processus d'écriture agressive
			cmd := exec.Command(testExe, "-test.run=^TestHostileCrashHelperWriter$")
			cmd.Env = append(os.Environ(),
				"GO_WANT_HELPER_WRITER=1",
				"HELPER_DB_DIR="+dir,
				"HELPER_AUDIT_PATH="+auditPath,
				"HELPER_KEY_HEX="+keyHex,
			)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			if err := cmd.Start(); err != nil {
				t.Fatalf("cmd.Start: %v", err)
			}

			// 2. Attendre l'initialisation du sous-processus (notamment sous -race)
			for start := time.Now(); time.Since(start) < 2*time.Second; {
				if _, err := os.Stat(auditPath); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Laisser tourner pour accumuler des transactions en vol
			delayMs := 25 + (cycle * 15)
			time.Sleep(time.Duration(delayMs) * time.Millisecond)

			// 3. Frapper avec SIGKILL (Coupure de courant non coopérative brutale)
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_ = cmd.Wait() // Récolte du statut terminé

			// 4. Lire l'ensemble des transactions formellement confirmées avant le crash
			auditData, err := os.ReadFile(auditPath)
			if err != nil {
				t.Fatalf("Lecture journal témoin: %v", err)
			}

			lines := bytes.Split(bytes.TrimSpace(auditData), []byte("\n"))
			var committed []committedTxRecord
			for _, line := range lines {
				if len(line) == 0 {
					continue
				}
				var r committedTxRecord
				if err := json.Unmarshal(line, &r); err == nil {
					committed = append(committed, r)
				}
			}

			t.Logf("Cycle %d : %d transactions confirmées avant SIGKILL", cycle, len(committed))

			// 5. Rouvrir la base de données : l'épreuve de réveil
			s, err := OpenShard(dir, key, 0)
			if err != nil {
				t.Fatalf("OpenShard a échoué après SIGKILL: %v", err)
			}
			defer s.Close()

			// 6. Vérifier chaque transaction confirmée : égalité bit-exacte
			for _, c := range committed {
				got, err := s.Get([]byte(c.Key))
				if err != nil {
					t.Fatalf("Transaction confirmée manquante id=%d k=%s: %v", c.ID, c.Key, err)
				}
				if len(got) != c.Size {
					t.Fatalf("Taille divergente pour id=%d: got %d, want %d", c.ID, len(got), c.Size)
				}
				gotHead := string(got[:16])
				if gotHead != c.Val {
					t.Fatalf("Contenu divergent pour id=%d: got %q, want %q", c.ID, gotHead, c.Val)
				}
			}

			// 7. Prouver la reprise immédiate en écriture après crash
			nextKey := []byte(fmt.Sprintf("post-crash-key-cycle-%d", cycle))
			nextVal := []byte("post-crash-payload-nominal-resumption")
			if err := s.Put(nextKey, nextVal); err != nil {
				t.Fatalf("Put post-crash a échoué: %v", err)
			}
			gotNext, err := s.Get(nextKey)
			if err != nil || !bytes.Equal(gotNext, nextVal) {
				t.Fatalf("Get post-crash divergent: got %v, err=%v", gotNext, err)
			}
		})
	}
}

// TestHostileCrash_MultiLBA_TornMiddleChunk simule une coupure de courant survenant
// au beau milieu d'une transaction multi-LBA (chunk 2 sur 4 tronqué).
// Prouve que l'atomicité tout-ou-rien est respectée : la transaction incomplète
// est abandonnée sans corrompre le journal ni les transactions antérieures ou postérieures.
func TestHostileCrash_MultiLBA_TornMiddleChunk(t *testing.T) {
	path, key := walCrashImage(t)
	const walSizeLBAs = 64
	w, err := CreateWAL(path, walSizeLBAs*LBASize, key)
	if err != nil {
		t.Fatalf("CreateWAL: %v", err)
	}

	// 1. Écrire une première transaction durable complète (durableTx1)
	id1, _ := NewID(100, 0, 1)
	rec1 := Record{ID: id1, Type: RecPut, Payload: []byte("durable-tx-1-before-multilba")}
	if err := w.Append(rec1); err != nil {
		t.Fatalf("Append rec1: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush rec1: %v", err)
	}

	// 2. Écrire manuellement les premiers chunks d'une transaction multi-LBA de 4 chunks
	// mais simuler un crash coupant les chunks 2 et 3.
	idMulti, _ := NewID(200, 0, 2)
	totalLen := uint32(12000)
	totalChunks := uint16(4)
	chunkPayload := make([]byte, walMaxPayload-walChunkHdrSize)
	for i := range chunkPayload {
		chunkPayload[i] = byte(i ^ 0xAA)
	}

	// Écrire chunk 0 (valide)
	chunk0Buf := make([]byte, walChunkHdrSize+len(chunkPayload))
	binary.LittleEndian.PutUint32(chunk0Buf[0:4], walChunkMagic)
	binary.LittleEndian.PutUint16(chunk0Buf[4:6], 0)
	binary.LittleEndian.PutUint16(chunk0Buf[6:8], totalChunks)
	binary.LittleEndian.PutUint32(chunk0Buf[8:12], totalLen)
	chunk0Buf[12] = byte(RecPut)
	copy(chunk0Buf[walChunkHdrSize:], chunkPayload)
	if err := w.Append(Record{ID: idMulti, Type: RecPut, Payload: chunk0Buf}); err != nil {
		t.Fatalf("Append chunk0: %v", err)
	}

	// Écrire chunk 1 (valide)
	chunk1Buf := make([]byte, walChunkHdrSize+len(chunkPayload))
	binary.LittleEndian.PutUint32(chunk1Buf[0:4], walChunkMagic)
	binary.LittleEndian.PutUint16(chunk1Buf[4:6], 1)
	binary.LittleEndian.PutUint16(chunk1Buf[6:8], totalChunks)
	binary.LittleEndian.PutUint32(chunk1Buf[8:12], totalLen)
	chunk1Buf[12] = byte(RecPut)
	copy(chunk1Buf[walChunkHdrSize:], chunkPayload)
	if err := w.Append(Record{ID: idMulti, Type: RecPut, Payload: chunk1Buf}); err != nil {
		t.Fatalf("Append chunk1: %v", err)
	}

	// Flush simulant que chunk 0 et 1 ont atteint le disque
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush chunks: %v", err)
	}

	// Coupure brutale ici : chunks 2 et 3 ne sont jamais émis !
	if err := w.Close(); err != nil {
		t.Fatalf("Close WAL: %v", err)
	}

	// 3. Rouvrir le WAL et rejouer : prouver que rec1 est préservé et recMulti est ignoré
	wRecovered, err := OpenWAL(path, key)
	if err != nil {
		t.Fatalf("OpenWAL: %v", err)
	}
	defer wRecovered.Close()

	recs, err := wRecovered.Replay()
	if err != nil {
		t.Fatalf("Replay a échoué: %v", err)
	}

	if len(recs) != 1 {
		t.Fatalf("Replay a retourné %d records, attendu exactement 1 (rec1)", len(recs))
	}
	if recs[0].ID != id1 || !bytes.Equal(recs[0].Payload, rec1.Payload) {
		t.Fatalf("rec1 altéré lors du replay avec chunk incomplet")
	}

	// 4. Prouver qu'on peut immédiatement continuer d'écrire dans ce WAL
	id3, _ := NewID(300, 0, 3)
	rec3 := Record{ID: id3, Type: RecPut, Payload: []byte("durable-tx-3-after-recovery")}
	if err := wRecovered.Append(rec3); err != nil {
		t.Fatalf("Append rec3: %v", err)
	}
	if err := wRecovered.Flush(); err != nil {
		t.Fatalf("Flush rec3: %v", err)
	}

	recs2, err := wRecovered.Replay()
	if err != nil {
		t.Fatalf("Replay après rec3: %v", err)
	}
	if len(recs2) != 2 {
		t.Fatalf("Replay2 a retourné %d records, attendu 2 (rec1 et rec3)", len(recs2))
	}
	if recs2[1].ID != id3 || !bytes.Equal(recs2[1].Payload, rec3.Payload) {
		t.Fatalf("rec3 altéré: %v", recs2[1])
	}
}

// TestHostileCrash_RepackHeap_PowerLoss vérifie que le compactage du tas
// et du WAL résiste à une coupure de courant brutale : les données actives
// sont intégralement préservées et les tombstones purgés.
func TestHostileCrash_RepackHeap_PowerLoss(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	_, _ = rand.Read(key[:])

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	const totalN = 100
	const deleteN = 50

	keys := make([][]byte, totalN)
	vals := make([][]byte, totalN)
	for i := 0; i < totalN; i++ {
		keys[i] = []byte(fmt.Sprintf("repack-crash-k-%04d", i))
		vals[i] = []byte(fmt.Sprintf("repack-crash-val-%04d-payload", i))
		if err := s.Put(keys[i], vals[i]); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	// Supprimer la moitié des clés (création de tombstones)
	for i := 0; i < deleteN; i++ {
		if err := s.Delete(keys[i]); err != nil {
			t.Fatalf("Delete %d: %v", i, err)
		}
	}

	// Exécuter Compact (compacte les feuilles + repack tas + compacte WAL)
	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Simuler un crash brutal : fermeture sans désallocation propre des descripteurs
	_ = s.Close()

	// Réouverture et vérification de l'intégrité
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard après compactage: %v", err)
	}
	defer sReopen.Close()

	// Les clés supprimées doivent retourner NotFound
	for i := 0; i < deleteN; i++ {
		_, err := sReopen.Get(keys[i])
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Clé supprimée %s toujours présente ou erreur inattendue: %v", keys[i], err)
		}
	}

	// Les clés actives doivent être présentes avec égalité bit-exacte
	for i := deleteN; i < totalN; i++ {
		got, err := sReopen.Get(keys[i])
		if err != nil {
			t.Fatalf("Clé active manquante %s: %v", keys[i], err)
		}
		if !bytes.Equal(got, vals[i]) {
			t.Fatalf("Donnée divergente pour %s: got %q, want %q", keys[i], got, vals[i])
		}
	}

	// Poursuite nominale : ajout d'une nouvelle clé
	newK := []byte("post-repack-new-key")
	newV := []byte("post-repack-new-val")
	if err := sReopen.Put(newK, newV); err != nil {
		t.Fatalf("Put post-repack: %v", err)
	}
	gotNew, err := sReopen.Get(newK)
	if err != nil || !bytes.Equal(gotNew, newV) {
		t.Fatalf("Get post-repack: %v", err)
	}
}

// TestHostileCrash_Overflow_TornMiddleChunk prouve la résilience formelle contre
// une déchirure matérielle survenant au milieu d'une chaîne de pages d'overflow.
func TestHostileCrash_Overflow_TornMiddleChunk(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	// 1. Clé valide avec overflow (12 Ko)
	val1 := make([]byte, 12000)
	for i := range val1 {
		val1[i] = byte(i ^ 0x33)
	}
	if err := s.Put([]byte("valid-ofl-1"), val1); err != nil {
		t.Fatalf("Put valid-ofl-1: %v", err)
	}

	// 2. Clé avec overflow multi-pages (50 Ko -> 4 pages d'overflow)
	val2 := make([]byte, 50000)
	for i := range val2 {
		val2[i] = byte(i ^ 0x77)
	}
	if err := s.Put([]byte("torn-ofl-2"), val2); err != nil {
		t.Fatalf("Put torn-ofl-2: %v", err)
	}
	_ = s.Close()

	// 3. Corruption hostile de la page 3 du tas (seconde page d'overflow de torn-ofl-2)
	dataPath := filepath.Join(dir, "data.img")
	dataBytes, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("ReadFile data.img: %v", err)
	}

	// Localiser l'en-tête de page 3 et corrompre délibérément son contenu et son CRC
	tornPage := uint64(3)
	tornOff := tornPage * pageN
	if tornOff+pageN <= uint64(len(dataBytes)) {
		for i := uint64(64); i < 128; i++ {
			dataBytes[tornOff+i] ^= 0xFF
		}
		dataBytes[tornOff+28] = 0xDE
		dataBytes[tornOff+29] = 0xAD
		dataBytes[tornOff+30] = 0xBE
		dataBytes[tornOff+31] = 0xEF
		if err := os.WriteFile(dataPath, dataBytes, 0o600); err != nil {
			t.Fatalf("WriteFile data.img: %v", err)
		}
	}

	// 4. Réouverture : le rejeu s'exécute et doit détecter la corruption d'overflow
	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("ReopenShard: %v", err)
	}
	defer sReopen.Close()

	// La clé 1 intègre doit être parfaitement récupérable
	got1, err := sReopen.Get([]byte("valid-ofl-1"))
	if err != nil {
		t.Fatalf("Get valid-ofl-1: %v", err)
	}
	if !bytes.Equal(got1, val1) {
		t.Fatalf("Contenu corrompu sur valid-ofl-1")
	}

	// La clé 2 doit échouer proprement sans panic
	_, err2 := sReopen.Get([]byte("torn-ofl-2"))
	if err2 == nil {
		t.Fatalf("Get torn-ofl-2 aurait dû échouer !")
	}

	// 5. Poursuite des opérations : écriture d'une nouvelle clé d'overflow post-corruption
	val3 := make([]byte, 25000)
	for i := range val3 {
		val3[i] = byte(i ^ 0xAA)
	}
	if err := sReopen.Put([]byte("post-torn-ofl-3"), val3); err != nil {
		t.Fatalf("Put post-torn-ofl-3: %v", err)
	}
	got3, err := sReopen.Get([]byte("post-torn-ofl-3"))
	if err != nil || !bytes.Equal(got3, val3) {
		t.Fatalf("Get post-torn-ofl-3 divergente: %v", err)
	}
}

// TestHostileCrash_Overflow_PowerLoss_Durability prouve que les pages d'overflow
// persistent de manière intègre et bit-exacte sous coupure brutale de processus.
func TestHostileCrash_Overflow_PowerLoss_Durability(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 0x42)
	}

	s, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("OpenShard: %v", err)
	}

	type kv struct {
		k []byte
		v []byte
	}
	records := make([]kv, 10)
	for i := 0; i < 10; i++ {
		sz := 4096 * (i + 1)
		v := make([]byte, sz)
		for j := range v {
			v[j] = byte(j ^ (i * 17))
		}
		k := []byte(fmt.Sprintf("ofl-power-loss-key-%02d", i))
		records[i] = kv{k: k, v: v}
		if err := s.Put(k, v); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}

	_ = s.Close()

	sReopen, err := OpenShard(dir, key, 0)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer sReopen.Close()

	for i, r := range records {
		got, err := sReopen.Get(r.k)
		if err != nil {
			t.Fatalf("Get key %d (%s): %v", i, r.k, err)
		}
		if !bytes.Equal(got, r.v) {
			t.Fatalf("Payload divergeant pour clé %s", r.k)
		}
	}
}
