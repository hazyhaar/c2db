// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/rand"
	"errors"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

// memStorage est un magasin en mémoire thread-safe implémentant VFSStorage pour les tests unitaires.
type memStorage struct {
	mu     sync.Mutex
	data   map[string][]byte
	synced bool
}

func newMemStorage() *memStorage {
	return &memStorage{
		data: make(map[string][]byte),
	}
}

func (m *memStorage) Get(key []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	val, ok := m.data[string(key)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	return cp, nil
}

func (m *memStorage) Put(key, val []byte, opts ...WriteOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(val))
	copy(cp, val)
	m.data[string(key)] = cp
	return nil
}

func (m *memStorage) Delete(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, string(key))
	return nil
}

func (m *memStorage) Sync() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.synced = true
	return nil
}

// countingStorage instrumente memStorage avec un compteur de Get pour
// prouver que le chemin chaud VFS ne relit pas le stockage.
type countingStorage struct {
	*memStorage
	gets atomic.Int64
}

func (c *countingStorage) Get(key []byte) ([]byte, error) {
	c.gets.Add(1)
	return c.memStorage.Get(key)
}

// TestVFSFile_CheckStale_NoStorageRead prouve que le contrôle d'obsolescence du
// chemin chaud (ReadAt/WriteAt) ne déclenche aucun storage.Get : il compare au
// cache sharedFileState.generation en O(1). Rouge d'abord : sur le source actuel,
// checkStaleLocked relit le stockage à chaque op, le compteur dépasse le baseline.
func TestVFSFile_CheckStale_NoStorageRead(t *testing.T) {
	storage := &countingStorage{memStorage: newMemStorage()}
	f, err := OpenVFSFile(storage, "tenant_test", "file_stale.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()
	baseline := storage.gets.Load()

	buf := make([]byte, BlockSize)
	buf[0] = 0x42
	n, err := f.WriteAt(buf, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("WriteAt bloc plein : n=%d err=%v", n, err)
	}
	got := make([]byte, BlockSize)
	n, err = f.ReadAt(got, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("ReadAt : n=%d err=%v", n, err)
	}
	if got[0] != 0x42 {
		t.Fatalf("ReadAt données : got %d want 0x42", got[0])
	}

	extra := storage.gets.Load() - baseline
	if extra != 0 {
		t.Fatalf("checkStaleLocked relit le stockage : %d Get(s) au-delà du baseline (attendu 0)", extra)
	}
}

// TestVFSFile_BlockBoundaries teste des écritures chevauchant 1, 2 et 3 frontières de 4 Ko.
func TestVFSFile_BlockBoundaries(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_test", "file_boundaries.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	// 1. Écriture chevauchant 1 frontière de 4 Ko (4096)
	// Offset: 4090, Longueur: 20 octets (plage 4090 à 4110)
	pattern1 := []byte("0123456789abcdefghij")
	if len(pattern1) != 20 {
		t.Fatalf("longueur attendue 20, obtenu %d", len(pattern1))
	}
	n, err := f.WriteAt(pattern1, 4090)
	if err != nil || n != 20 {
		t.Fatalf("WriteAt frontière 1: n=%d err=%v", n, err)
	}

	sz, err := f.Size()
	if err != nil || sz != 4110 {
		t.Fatalf("Size attendu 4110, obtenu %d (err=%v)", sz, err)
	}

	readBuf1 := make([]byte, 20)
	n, err = f.ReadAt(readBuf1, 4090)
	if err != nil || n != 20 {
		t.Fatalf("ReadAt frontière 1: n=%d err=%v", n, err)
	}
	if !bytes.Equal(readBuf1, pattern1) {
		t.Fatalf("ReadAt frontière 1 données corrompues: attendu %q, obtenu %q", pattern1, readBuf1)
	}

	// Vérifier que les octets avant 4090 sont des zéros (sparse)
	prefixZeros := make([]byte, 4090)
	n, err = f.ReadAt(prefixZeros, 0)
	if err != nil || n != 4090 {
		t.Fatalf("ReadAt sparse prefix: n=%d err=%v", n, err)
	}
	for i, b := range prefixZeros {
		if b != 0 {
			t.Fatalf("Octet sparse non nul à index %d: %02x", i, b)
		}
	}

	// 2. Écriture chevauchant 2 frontières de 4 Ko (traversant 4096 et 8192)
	// Offset: 4000, Longueur: 5000 octets (plage 4000 à 9000 -> blocs 0, 1 et 2)
	pattern2 := make([]byte, 5000)
	for i := range pattern2 {
		pattern2[i] = byte((i % 250) + 1)
	}
	n, err = f.WriteAt(pattern2, 4000)
	if err != nil || n != 5000 {
		t.Fatalf("WriteAt 2 frontières: n=%d err=%v", n, err)
	}

	sz, err = f.Size()
	if err != nil || sz != 9000 {
		t.Fatalf("Size attendu 9000, obtenu %d", sz)
	}

	readBuf2 := make([]byte, 5000)
	n, err = f.ReadAt(readBuf2, 4000)
	if err != nil || n != 5000 {
		t.Fatalf("ReadAt 2 frontières: n=%d err=%v", n, err)
	}
	if !bytes.Equal(readBuf2, pattern2) {
		t.Fatalf("ReadAt 2 frontières données corrompues")
	}

	// 3. Écriture chevauchant 3 frontières de 4 Ko (traversant 4096, 8192, 12288)
	// Offset: 3000, Longueur: 11000 octets (plage 3000 à 14000 -> blocs 0, 1, 2, 3)
	pattern3 := make([]byte, 11000)
	for i := range pattern3 {
		pattern3[i] = byte((i*7)%251 + 1)
	}
	n, err = f.WriteAt(pattern3, 3000)
	if err != nil || n != 11000 {
		t.Fatalf("WriteAt 3 frontières: n=%d err=%v", n, err)
	}

	sz, err = f.Size()
	if err != nil || sz != 14000 {
		t.Fatalf("Size attendu 14000, obtenu %d", sz)
	}

	readBuf3 := make([]byte, 11000)
	n, err = f.ReadAt(readBuf3, 3000)
	if err != nil || n != 11000 {
		t.Fatalf("ReadAt 3 frontières: n=%d err=%v", n, err)
	}
	if !bytes.Equal(readBuf3, pattern3) {
		t.Fatalf("ReadAt 3 frontières données corrompues")
	}
}

// TestVFSFile_PartialWriteNeighbors teste les écritures partielles au milieu d'un bloc avec préservation des voisins.
func TestVFSFile_PartialWriteNeighbors(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_test", "file_neighbors.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	// Initialisation d'un bloc complet de 4096 octets avec le caractère 'A'
	initBlock := bytes.Repeat([]byte{'A'}, BlockSize)
	n, err := f.WriteAt(initBlock, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("WriteAt initBlock: n=%d err=%v", n, err)
	}

	// Écriture partielle au milieu du bloc : offset 1000, longueur 200 avec 'B'
	bChunk := bytes.Repeat([]byte{'B'}, 200)
	n, err = f.WriteAt(bChunk, 1000)
	if err != nil || n != 200 {
		t.Fatalf("WriteAt bChunk: n=%d err=%v", n, err)
	}

	// Relecture du bloc entier pour vérifier la préservation des voisins
	readBlock := make([]byte, BlockSize)
	n, err = f.ReadAt(readBlock, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("ReadAt full block: n=%d err=%v", n, err)
	}

	// Vérification de la plage 0..999 ('A')
	for i := 0; i < 1000; i++ {
		if readBlock[i] != 'A' {
			t.Fatalf("Voisin gauche altéré à l'index %d: attendu 'A', obtenu %c", i, readBlock[i])
		}
	}
	// Vérification de la plage 1000..1199 ('B')
	for i := 1000; i < 1200; i++ {
		if readBlock[i] != 'B' {
			t.Fatalf("Donnée modifiée altérée à l'index %d: attendu 'B', obtenu %c", i, readBlock[i])
		}
	}
	// Vérification de la plage 1200..4095 ('A')
	for i := 1200; i < BlockSize; i++ {
		if readBlock[i] != 'A' {
			t.Fatalf("Voisin droit altéré à l'index %d: attendu 'A', obtenu %c", i, readBlock[i])
		}
	}

	// Écriture partielle en queue de bloc : offset 4000, longueur 96 avec 'C'
	cChunk := bytes.Repeat([]byte{'C'}, 96)
	n, err = f.WriteAt(cChunk, 4000)
	if err != nil || n != 96 {
		t.Fatalf("WriteAt cChunk: n=%d err=%v", n, err)
	}

	n, err = f.ReadAt(readBlock, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("ReadAt full block après queue: n=%d err=%v", n, err)
	}
	for i := 1200; i < 4000; i++ {
		if readBlock[i] != 'A' {
			t.Fatalf("Voisin intermédiaire altéré à l'index %d: attendu 'A', obtenu %c", i, readBlock[i])
		}
	}
	for i := 4000; i < BlockSize; i++ {
		if readBlock[i] != 'C' {
			t.Fatalf("Queue altérée à l'index %d: attendu 'C', obtenu %c", i, readBlock[i])
		}
	}
}

// TestVFSFile_ShortRead teste la lecture courte (dépassement partiel et total)
// et vérifie que le buffer est scrupuleusement complété par des zéros.
func TestVFSFile_ShortRead(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_test", "file_shortread.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	// Créer un fichier de taille 1000 octets rempli de données non nulles
	payload := make([]byte, 1000)
	for i := range payload {
		payload[i] = byte((i % 250) + 1) // Tous > 0
	}
	if _, err := f.WriteAt(payload, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	// 1. Dépassement partiel :
	// Offset: 800, Taille buffer: 500 -> disponible: 200 octets (800 à 1000)
	bufPartial := make([]byte, 500)
	for i := range bufPartial {
		bufPartial[i] = 0xFF // Remplissage initial hostile pour tester l'écrasement à zéro
	}

	n, err := f.ReadAt(bufPartial, 800)
	if !errors.Is(err, ErrShortRead) {
		t.Fatalf("Erreur attendue ErrShortRead, obtenu %v", err)
	}
	if n != 200 {
		t.Fatalf("Nombre d'octets disponibles attendu 200, obtenu %d", n)
	}

	// Vérifier que buf[0:200] correspond aux données réelles
	if !bytes.Equal(bufPartial[:200], payload[800:1000]) {
		t.Fatalf("Les octets lus [0:200] ne correspondent pas au contenu du fichier")
	}

	// Vérifier que le reste du buffer [200:500] a été scrupuleusement complété par 0x00
	for i := 200; i < 500; i++ {
		if bufPartial[i] != 0x00 {
			t.Fatalf("Octet résiduel non nul à l'index %d: 0x%02x (attendu 0x00)", i, bufPartial[i])
		}
	}

	// 2. Dépassement total (off == FileSize) :
	bufTotalExact := make([]byte, 256)
	for i := range bufTotalExact {
		bufTotalExact[i] = 0xFE
	}
	n, err = f.ReadAt(bufTotalExact, 1000)
	if !errors.Is(err, ErrShortRead) {
		t.Fatalf("Erreur attendue ErrShortRead, obtenu %v", err)
	}
	if n != 0 {
		t.Fatalf("Nombre d'octets attendu 0, obtenu %d", n)
	}
	for i, b := range bufTotalExact {
		if b != 0x00 {
			t.Fatalf("Octet non nul à index %d lors de dépassement total: 0x%02x", i, b)
		}
	}

	// 3. Dépassement total (off > FileSize) :
	bufTotalBeyond := make([]byte, 128)
	for i := range bufTotalBeyond {
		bufTotalBeyond[i] = 0xAA
	}
	n, err = f.ReadAt(bufTotalBeyond, 2000)
	if !errors.Is(err, ErrShortRead) {
		t.Fatalf("Erreur attendue ErrShortRead, obtenu %v", err)
	}
	if n != 0 {
		t.Fatalf("Nombre d'octets attendu 0, obtenu %d", n)
	}
	for i, b := range bufTotalBeyond {
		if b != 0x00 {
			t.Fatalf("Octet non nul à index %d lors de dépassement au-delà: 0x%02x", i, b)
		}
	}
}

// TestVFSFile_TruncateReExtend teste l'extension, la troncature, puis la ré-extension,
// et prouve formellement qu'aucun ancien octet fantôme n'est ressuscité.
func TestVFSFile_TruncateReExtend(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_test", "file_truncate.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	// Écriture de 6000 octets avec des données non nulles (0x5A)
	// Bloc 0: 4096 octets, Bloc 1: 1904 octets
	dataInit := bytes.Repeat([]byte{0x5A}, 6000)
	if _, err := f.WriteAt(dataInit, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	// Troncature à 4500 octets :
	// 4500 / 4096 = 1 (bloc 1 partiel, remainder = 404).
	// Les octets 404..4095 du bloc 1 doivent être zéro-fillés.
	if err := f.Truncate(4500); err != nil {
		t.Fatalf("Truncate(4500): %v", err)
	}

	sz, err := f.Size()
	if err != nil || sz != 4500 {
		t.Fatalf("Size après troncature: attendu 4500, obtenu %d", sz)
	}

	// Ré-extension à 6000 octets
	if err := f.Truncate(6000); err != nil {
		t.Fatalf("Truncate(6000) ré-extension: %v", err)
	}

	// Lecture de la plage 4500..6000 (1500 octets)
	extendedBuf := make([]byte, 1500)
	for i := range extendedBuf {
		extendedBuf[i] = 0xEE // Motif hostile
	}

	n, err := f.ReadAt(extendedBuf, 4500)
	if err != nil || n != 1500 {
		t.Fatalf("ReadAt plage ré-étendue: n=%d err=%v", n, err)
	}

	// Prouver qu'aucun octet 0x5A fantôme n'est réapparu
	for i, b := range extendedBuf {
		if b != 0x00 {
			t.Fatalf("RÉSURRECTION D'OCTET FANTÔME DÉTECTÉE à l'offset %d: 0x%02x (attendu 0x00)", 4500+i, b)
		}
	}

	// Deuxième scénario : Troncature sur frontière exacte de bloc (4096)
	if err := f.Truncate(4096); err != nil {
		t.Fatalf("Truncate(4096): %v", err)
	}

	// Vérifier que le bloc 1 a bien été supprimé du stockage
	b1Key := BlockKey("tenant_test", "file_truncate.db", f.Generation(), 1)
	if _, err := storage.Get(b1Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Le bloc 1 aurait dû être supprimé lors de la troncature à 4096, err=%v", err)
	}

	// Ré-extension à 8192 octets
	if err := f.Truncate(8192); err != nil {
		t.Fatalf("Truncate(8192): %v", err)
	}

	readBlock1 := make([]byte, 4096)
	n, err = f.ReadAt(readBlock1, 4096)
	if err != nil || n != 4096 {
		t.Fatalf("ReadAt bloc 1 ré-étendu: n=%d err=%v", n, err)
	}
	for i, b := range readBlock1 {
		if b != 0x00 {
			t.Fatalf("Octet non nul dans bloc ré-étendu à offset %d: 0x%02x", 4096+i, b)
		}
	}
}

// TestVFSFile_Concurrency teste les accès concurrents élémentaires sur un même fichier.
func TestVFSFile_Concurrency(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_test", "file_concurrent.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	const numWorkers = 8
	const numIterations = 50
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			offset := int64(workerID * 1024)
			writePattern := bytes.Repeat([]byte{byte(workerID + 1)}, 512)

			for iter := 0; iter < numIterations; iter++ {
				// Écriture concurrente
				n, err := f.WriteAt(writePattern, offset)
				if err != nil || n != 512 {
					t.Errorf("Worker %d WriteAt échoué: n=%d err=%v", workerID, n, err)
					return
				}

				// Lecture concurrente
				readBuf := make([]byte, 512)
				n, err = f.ReadAt(readBuf, offset)
				if err != nil || n != 512 {
					t.Errorf("Worker %d ReadAt échoué: n=%d err=%v", workerID, n, err)
					return
				}
				if !bytes.Equal(readBuf, writePattern) {
					t.Errorf("Worker %d données corrompues", workerID)
					return
				}

				// Consultation de la taille
				if _, err := f.Size(); err != nil {
					t.Errorf("Worker %d Size échoué: %v", workerID, err)
					return
				}

				// Sync occasionnel
				if iter%10 == 0 {
					if err := f.Sync(); err != nil {
						t.Errorf("Worker %d Sync échoué: %v", workerID, err)
						return
					}
				}
			}
		}()
	}

	wg.Wait()
}

// TestVFSFile_Generation teste l'invalidation des descripteurs périmés lors de suppression ou recréation.
func TestVFSFile_Generation(t *testing.T) {
	storage := newMemStorage()

	// 1. Création initiale
	f1, err := OpenVFSFile(storage, "tenant_gen", "file_gen.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	if f1.Generation() != 1 {
		t.Fatalf("Génération initiale attendue 1, obtenu %d", f1.Generation())
	}

	data := []byte("donnees_v1")
	if _, err := f1.WriteAt(data, 0); err != nil {
		t.Fatalf("f1.WriteAt: %v", err)
	}

	// 2. Suppression du fichier
	if err := DeleteVFSFile(storage, "tenant_gen", "file_gen.db"); err != nil {
		t.Fatalf("DeleteVFSFile: %v", err)
	}

	// Vérifier que f1 est désormais périmé
	buf := make([]byte, 10)
	if _, err := f1.ReadAt(buf, 0); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("f1.ReadAt aurait dû retourner ErrStaleHandle, obtenu %v", err)
	}
	if _, err := f1.WriteAt([]byte("test"), 0); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("f1.WriteAt aurait dû retourner ErrStaleHandle, obtenu %v", err)
	}
	if err := f1.Truncate(0); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("f1.Truncate aurait dû retourner ErrStaleHandle, obtenu %v", err)
	}

	// 3. Recréation du fichier via CreateVFSFile
	f2, err := CreateVFSFile(storage, "tenant_gen", "file_gen.db")
	if err != nil {
		t.Fatalf("CreateVFSFile: %v", err)
	}
	if f2.Generation() <= f1.Generation() {
		t.Fatalf("Génération f2 (%d) doit être strictement supérieure à f1 (%d)", f2.Generation(), f1.Generation())
	}

	// f1 doit toujours rester invalide
	if _, err := f1.ReadAt(buf, 0); !errors.Is(err, ErrStaleHandle) {
		t.Fatalf("f1.ReadAt sur ancien descripteur doit retourner ErrStaleHandle, obtenu %v", err)
	}

	// f2 doit fonctionner normalement
	newData := []byte("donnees_v2")
	if _, err := f2.WriteAt(newData, 0); err != nil {
		t.Fatalf("f2.WriteAt: %v", err)
	}
	n, err := f2.ReadAt(buf, 0)
	if err != nil || n != len(newData) {
		t.Fatalf("f2.ReadAt: n=%d err=%v", n, err)
	}
	if !bytes.Equal(buf, newData) {
		t.Fatalf("f2 données corrompues: attendu %q, obtenu %q", newData, buf)
	}
}

// TestVFSFile_SparseZeroBlock teste l'économie d'espace pour les blocs entièrement à zéro.
func TestVFSFile_SparseZeroBlock(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_sparse", "sparse.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	// Écrire 4096 octets non nuls dans le bloc 0
	nonZero := bytes.Repeat([]byte{0x7F}, BlockSize)
	if _, err := f.WriteAt(nonZero, 0); err != nil {
		t.Fatalf("WriteAt nonZero: %v", err)
	}

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync nonZero: %v", err)
	}

	// Vérifier que le bloc 0 existe en base
	b0Key := BlockKey("tenant_sparse", "sparse.db", f.Generation(), 0)
	if _, err := storage.Get(b0Key); err != nil {
		t.Fatalf("Le bloc 0 devrait exister en base: %v", err)
	}

	// Écraser le bloc 0 avec 4096 octets de zéros
	zeroBlock := make([]byte, BlockSize)
	if _, err := f.WriteAt(zeroBlock, 0); err != nil {
		t.Fatalf("WriteAt zeroBlock: %v", err)
	}

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync zeroBlock: %v", err)
	}

	// Vérifier que le bloc a été supprimé pour économiser l'espace (sparse)
	if _, err := storage.Get(b0Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Le bloc 0 aurait dû être supprimé car entièrement à zéro, err=%v", err)
	}

	// Lire le bloc 0 : doit retourner 4096 octets de zéros
	readBuf := make([]byte, BlockSize)
	n, err := f.ReadAt(readBuf, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("ReadAt: n=%d err=%v", n, err)
	}
	for i, b := range readBuf {
		if b != 0 {
			t.Fatalf("Octet non nul à index %d: 0x%02x", i, b)
		}
	}
}

// TestVFSFile_RealC2DB teste le cycle de vie complet de VFSFile adossé à un vrai moteur *c2db.DB sur disque.
func TestVFSFile_RealC2DB(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()

	f, err := OpenVFSFile(db, "tenant_prod", "sqlite_main.db")
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers hôte (EINVAL)")
		}
		t.Fatalf("OpenVFSFile sur c2db.DB: %v", err)
	}

	// Écriture traversant 2 blocs
	data := make([]byte, 8000)
	for i := range data {
		data[i] = byte((i * 13) % 251)
	}

	n, err := f.WriteAt(data, 100)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par le système de fichiers hôte (EINVAL)")
		}
		t.Fatalf("WriteAt: %v", err)
	}
	if n != len(data) {
		t.Fatalf("WriteAt: n=%d, want %d", n, len(data))
	}

	// Sync
	if err := f.Sync(); err != nil {
		t.Fatalf("f.Sync: %v", err)
	}

	// Fermeture du fichier et de la base
	if err := f.Close(); err != nil {
		t.Fatalf("f.Close: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	db = nil

	// Réouverture complète du moteur c2db depuis le disque
	db2, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB après réouverture: %v", err)
	}
	defer db2.Close()

	// Réouverture du fichier virtuel
	f2, err := OpenVFSFile(db2, "tenant_prod", "sqlite_main.db")
	if err != nil {
		t.Fatalf("OpenVFSFile réouvert: %v", err)
	}
	defer f2.Close()

	sz, err := f2.Size()
	if err != nil || sz != 8100 {
		t.Fatalf("Size réouvert: attendu 8100, obtenu %d (err=%v)", sz, err)
	}

	readBack := make([]byte, 8000)
	n, err = f2.ReadAt(readBack, 100)
	if err != nil || n != 8000 {
		t.Fatalf("ReadAt réouvert: n=%d err=%v", n, err)
	}
	if !bytes.Equal(readBack, data) {
		t.Fatalf("Données corrompues après réouverture et persistance c2db !")
	}
}

// TestVFSFile_MultiHandleSharedMutex teste deux descripteurs ouverts sur le même fichier
// effectuant des écritures concurrentes sur des octets voisins d'un même bloc de 4 Ko.
// Démontre formellement l'absence de perte de mise à jour (lost update) et la synchronisation
// de la taille entre handles grâce au sharedFileState.
func TestVFSFile_MultiHandleSharedMutex(t *testing.T) {
	storage := newMemStorage()
	const tenant = "tenant_multi"
	const fileID = "shared_mutex.db"

	// 1. Ouverture de deux descripteurs distincts pour le même fichier
	f1, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile f1: %v", err)
	}
	f2, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile f2: %v", err)
	}

	// Vérification que les deux descripteurs partagent bien le même sharedFileState
	if f1 == f2 {
		t.Fatalf("f1 et f2 doivent être des instances distinctes")
	}
	if f1.state != f2.state {
		t.Fatalf("f1 et f2 doivent référencer le même sharedFileState")
	}

	// 2. Initialisation d'un bloc de 4 Ko avec motif de fond 0x55
	bgPattern := bytes.Repeat([]byte{0x55}, BlockSize)
	if _, err := f1.WriteAt(bgPattern, 0); err != nil {
		t.Fatalf("Initialisation bloc 0: %v", err)
	}

	// Vérifier que f2 voit immédiatement la taille 4096
	sz2, err := f2.Size()
	if err != nil || sz2 != BlockSize {
		t.Fatalf("f2.Size() attendu %d, obtenu %d (err=%v)", BlockSize, sz2, err)
	}

	// 3. Écritures concurrentes sur des octets voisins dans le même bloc de 4 Ko :
	// f1 écrit 64 octets à l'offset 100..163 avec le motif 'A'
	// f2 écrit 64 octets à l'offset 164..227 avec le motif 'B'
	const iterations = 100
	var wg sync.WaitGroup
	wg.Add(2)

	patternA := bytes.Repeat([]byte{'A'}, 64)
	patternB := bytes.Repeat([]byte{'B'}, 64)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			n, err := f1.WriteAt(patternA, 100)
			if err != nil || n != 64 {
				t.Errorf("f1.WriteAt échoué: n=%d err=%v", n, err)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			n, err := f2.WriteAt(patternB, 164)
			if err != nil || n != 64 {
				t.Errorf("f2.WriteAt échoué: n=%d err=%v", n, err)
				return
			}
		}
	}()

	wg.Wait()

	// 4. Relecture du bloc complet via f1 et via f2
	readBuf := make([]byte, BlockSize)
	n, err := f1.ReadAt(readBuf, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("f1.ReadAt: n=%d err=%v", n, err)
	}

	// Vérification zone avant les écritures (0..99)
	for i := 0; i < 100; i++ {
		if readBuf[i] != 0x55 {
			t.Fatalf("Octet altéré à l'index %d avant motif: 0x%02x (attendu 0x55)", i, readBuf[i])
		}
	}
	// Vérification de la plage f1 (100..163)
	if !bytes.Equal(readBuf[100:164], patternA) {
		t.Fatalf("Plage f1 corrompue ou perdue par écriture concurrente f2")
	}
	// Vérification de la plage f2 (164..227)
	if !bytes.Equal(readBuf[164:228], patternB) {
		t.Fatalf("Plage f2 corrompue ou perdue par écriture concurrente f1")
	}
	// Vérification zone après les écritures (228..4095)
	for i := 228; i < BlockSize; i++ {
		if readBuf[i] != 0x55 {
			t.Fatalf("Octet altéré à l'index %d après motif: 0x%02x (attendu 0x55)", i, readBuf[i])
		}
	}

	// 5. Test d'extension dynamique du fichier par f1 et observation par f2
	extendData := []byte("extension_donnees")
	if _, err := f1.WriteAt(extendData, 5000); err != nil {
		t.Fatalf("f1.WriteAt extend: %v", err)
	}
	expectedSize := int64(5000 + len(extendData))
	sz2, err = f2.Size()
	if err != nil || sz2 != expectedSize {
		t.Fatalf("f2.Size() après extension: attendu %d, obtenu %d", expectedSize, sz2)
	}

	// 6. Fermeture de f1 puis de f2 et nettoyage du registre
	if err := f1.Close(); err != nil {
		t.Fatalf("f1.Close: %v", err)
	}
	// f2 doit rester pleinement fonctionnel
	readBackExt := make([]byte, len(extendData))
	n, err = f2.ReadAt(readBackExt, 5000)
	if err != nil || n != len(extendData) || !bytes.Equal(readBackExt, extendData) {
		t.Fatalf("f2.ReadAt après fermeture f1: n=%d err=%v", n, err)
	}

	if err := f2.Close(); err != nil {
		t.Fatalf("f2.Close: %v", err)
	}

	// Vérifier que l'état a bien été déréférencé du registre
	if st := globalFileRegistry.getState(storage, tenant, fileID); st != nil {
		t.Fatalf("L'état partagé aurait dû être libéré du registre après la fermeture de tous les handles")
	}
}

// TestVFSFile_ArithmeticOverflow valide les contrôles de limites soustractifs sûrs
// contre tout débordement d'entier int64.
func TestVFSFile_ArithmeticOverflow(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_overflow", "overflow.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 100)

	// 1. Offsets négatifs
	if _, err := f.ReadAt(buf, -1); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("ReadAt avec offset négatif attend ErrInvalidOffset, obtenu %v", err)
	}
	if _, err := f.WriteAt(buf, -1); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("WriteAt avec offset négatif attend ErrInvalidOffset, obtenu %v", err)
	}
	if err := f.Truncate(-1); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("Truncate avec taille négative attend ErrInvalidSize, obtenu %v", err)
	}

	// 2. Débordements int64
	if _, err := f.ReadAt(buf, math.MaxInt64-50); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("ReadAt avec off + len > MaxInt64 attend ErrInvalidOffset, obtenu %v", err)
	}
	if _, err := f.WriteAt(buf, math.MaxInt64-50); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("WriteAt avec off + len > MaxInt64 attend ErrInvalidOffset, obtenu %v", err)
	}
}

// TestVFSFile_ColocatedShard teste l'adaptateur ShardColocatedStorage pour router
// un fichier virtuel et ses données sur un Shard ciblé d'un vrai *DB c2db.
func TestVFSFile_ColocatedShard(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	db, err := OpenDB(dir, key)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	const tenant = "tenant_coloc"
	const fileID = "coloc_sqlite.db"

	targetShard := RouteFileShard(tenant, fileID)
	colocStorage, err := NewShardColocatedStorage(db, targetShard)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par l'hôte")
		}
		t.Fatalf("NewShardColocatedStorage: %v", err)
	}

	f, err := OpenVFSFile(colocStorage, tenant, fileID)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par l'hôte")
		}
		t.Fatalf("OpenVFSFile sur ShardColocatedStorage: %v", err)
	}

	data := []byte("donnees_colocalisees_sur_shard")
	if _, err := f.WriteAt(data, 0); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT non supporté par l'hôte")
		}
		t.Fatalf("WriteAt: %v", err)
	}

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	readBuf := make([]byte, len(data))
	n, err := f.ReadAt(readBuf, 0)
	if err != nil || n != len(data) || !bytes.Equal(readBuf, data) {
		t.Fatalf("ReadAt: n=%d err=%v, attendu %q, obtenu %q", n, err, data, readBuf)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestVFSFile_KeyInjectivityAndCollisionResistance valide l'injectivité stricte
// des clés de métadonnées, de blocs et de registre pour éliminer tout risque
// de collision inter-tenants ou de noms de fichiers contigus.
func TestVFSFile_KeyInjectivityAndCollisionResistance(t *testing.T) {
	pairs := []struct {
		t1, f1 string
		t2, f2 string
	}{
		{"a:b", "c", "a", "b:c"},
		{"", "test", "test", ""},
		{"user/1", "orders.db", "user", "1/orders.db"},
	}

	for _, tc := range pairs {
		meta1 := MetaKey(tc.t1, tc.f1)
		meta2 := MetaKey(tc.t2, tc.f2)
		if bytes.Equal(meta1, meta2) {
			t.Fatalf("Collision MetaKey détectée entre (%q, %q) et (%q, %q): %s", tc.t1, tc.f1, tc.t2, tc.f2, meta1)
		}

		blk1 := BlockKey(tc.t1, tc.f1, 1, 0)
		blk2 := BlockKey(tc.t2, tc.f2, 1, 0)
		if bytes.Equal(blk1, blk2) {
			t.Fatalf("Collision BlockKey détectée entre (%q, %q) et (%q, %q)", tc.t1, tc.f1, tc.t2, tc.f2)
		}

		pfx1 := BlockKeyPrefix(tc.t1, tc.f1, 1)
		pfx2 := BlockKeyPrefix(tc.t2, tc.f2, 1)
		if bytes.Equal(pfx1, pfx2) {
			t.Fatalf("Collision BlockKeyPrefix détectée entre (%q, %q) et (%q, %q)", tc.t1, tc.f1, tc.t2, tc.f2)
		}

		reg1 := fileIdentityKey(tc.t1, tc.f1)
		reg2 := fileIdentityKey(tc.t2, tc.f2)
		if reg1 == reg2 {
			t.Fatalf("Collision fileIdentityKey détectée entre (%q, %q) et (%q, %q): %s", tc.t1, tc.f1, tc.t2, tc.f2, reg1)
		}
	}

	// Vérifier que dans un fileRegistry partagé, ouvrir deux fichiers avec ces noms distincts
	// donne deux états sharedFileState totalement indépendants.
	storage := newMemStorage()
	for _, tc := range pairs {
		f1, err := OpenVFSFile(storage, tc.t1, tc.f1)
		if err != nil {
			t.Fatalf("OpenVFSFile(%q, %q): %v", tc.t1, tc.f1, err)
		}
		f2, err := OpenVFSFile(storage, tc.t2, tc.f2)
		if err != nil {
			t.Fatalf("OpenVFSFile(%q, %q): %v", tc.t2, tc.f2, err)
		}

		if f1.state == f2.state {
			t.Fatalf("sharedFileState partagé à tort entre (%q, %q) et (%q, %q)", tc.t1, tc.f1, tc.t2, tc.f2)
		}

		// Modifier f1 et vérifier l'isolation totale de f2
		data1 := []byte("isolation_test_f1")
		if _, err := f1.WriteAt(data1, 0); err != nil {
			t.Fatalf("WriteAt f1: %v", err)
		}
		sz2, err := f2.Size()
		if err != nil || sz2 != 0 {
			t.Fatalf("f2 contaminé par f1: taille attendue 0, obtenu %d", sz2)
		}

		data2 := []byte("isolation_test_f2_differente")
		if _, err := f2.WriteAt(data2, 0); err != nil {
			t.Fatalf("WriteAt f2: %v", err)
		}

		read1 := make([]byte, len(data1))
		if _, err := f1.ReadAt(read1, 0); err != nil || !bytes.Equal(read1, data1) {
			t.Fatalf("f1 données corrompues ou écrasées par f2: %q vs %q", read1, data1)
		}

		read2 := make([]byte, len(data2))
		if _, err := f2.ReadAt(read2, 0); err != nil || !bytes.Equal(read2, data2) {
			t.Fatalf("f2 données corrompues ou écrasées par f1: %q vs %q", read2, data2)
		}

		if err := f1.Close(); err != nil {
			t.Fatalf("f1.Close: %v", err)
		}
		if err := f2.Close(); err != nil {
			t.Fatalf("f2.Close: %v", err)
		}
	}
}

type syncStorageStub struct {
	VFSStorage
	mu        sync.Mutex
	syncCount int
	syncErr   error
}

func (s *syncStorageStub) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncCount++
	return s.syncErr
}

type flushStorageStub struct {
	VFSStorage
	mu         sync.Mutex
	flushCount int
	flushErr   error
}

func (s *flushStorageStub) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushCount++
	return s.flushErr
}

type checkpointStorageStub struct {
	VFSStorage
	mu              sync.Mutex
	checkpointCount int
	checkpointErr   error
}

func (s *checkpointStorageStub) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkpointCount++
	return s.checkpointErr
}

// TestVFSFile_SyncContract valide formellement que VFSFile.Sync invoque le contrat
// Syncer du stockage sous-jacent, incrémente le compteur de persistance et propage
// fidèlement les erreurs d'E/S.
func TestVFSFile_SyncContract(t *testing.T) {
	mem := newMemStorage()
	stub := &syncStorageStub{
		VFSStorage: mem,
	}

	f, err := OpenVFSFile(stub, "tenant_sync", "sync.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	if _, err := f.WriteAt([]byte("synced_bytes"), 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	// 1. Appel normal : le compteur de sync doit être incrémenté
	if err := f.Sync(); err != nil {
		t.Fatalf("f.Sync() attendu réussi, obtenu: %v", err)
	}

	stub.mu.Lock()
	cnt := stub.syncCount
	stub.mu.Unlock()
	if cnt != 1 {
		t.Fatalf("syncCount attendu 1, obtenu %d", cnt)
	}

	// 2. Échec du sous-système de persistance : propagation de l'erreur
	expectedErr := errors.New("c2db: fsync disk I/O failure")
	stub.mu.Lock()
	stub.syncErr = expectedErr
	stub.mu.Unlock()

	if err := f.Sync(); !errors.Is(err, expectedErr) {
		t.Fatalf("f.Sync() attendu erreur %v, obtenu %v", expectedErr, err)
	}

	stub.mu.Lock()
	cnt = stub.syncCount
	stub.mu.Unlock()
	if cnt != 2 {
		t.Fatalf("syncCount attendu 2, obtenu %d", cnt)
	}

	// 3. Validation Flusher
	flushStub := &flushStorageStub{VFSStorage: newMemStorage()}
	fFlush, err := OpenVFSFile(flushStub, "tenant_flush", "flush.db")
	if err != nil {
		t.Fatalf("OpenVFSFile flush: %v", err)
	}
	defer fFlush.Close()
	if err := fFlush.Sync(); err != nil {
		t.Fatalf("fFlush.Sync: %v", err)
	}
	flushStub.mu.Lock()
	if flushStub.flushCount != 1 {
		t.Fatalf("flushCount attendu 1, obtenu %d", flushStub.flushCount)
	}
	flushStub.mu.Unlock()

	// 4. Validation Checkpointer
	cpStub := &checkpointStorageStub{VFSStorage: newMemStorage()}
	fCp, err := OpenVFSFile(cpStub, "tenant_cp", "cp.db")
	if err != nil {
		t.Fatalf("OpenVFSFile cp: %v", err)
	}
	defer fCp.Close()
	if err := fCp.Sync(); err != nil {
		t.Fatalf("fCp.Sync: %v", err)
	}
	cpStub.mu.Lock()
	if cpStub.checkpointCount != 1 {
		t.Fatalf("checkpointCount attendu 1, obtenu %d", cpStub.checkpointCount)
	}
	cpStub.mu.Unlock()
}

// TestVFSFile_MaxInt64Boundary valide l'absence de panique et le comportement exact
// aux frontières ultimes de l'espace d'adressage int64 (MaxInt64 - 1 et MaxInt64).
func TestVFSFile_MaxInt64Boundary(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_boundary", "boundary.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	// 1. Écriture de 1 octet (0x7e) à l'offset math.MaxInt64 - 1
	n, err := f.WriteAt([]byte{0x7e}, math.MaxInt64-1)
	if err != nil {
		t.Fatalf("WriteAt(math.MaxInt64-1) échoué: %v", err)
	}
	if n != 1 {
		t.Fatalf("WriteAt(math.MaxInt64-1) n=%d, attendu 1", n)
	}

	// Vérification de la taille résultante
	sz, err := f.Size()
	if err != nil {
		t.Fatalf("f.Size(): %v", err)
	}
	if sz != math.MaxInt64 {
		t.Fatalf("f.Size() attendu %d, obtenu %d", int64(math.MaxInt64), sz)
	}

	// 2. Relecture de cet octet via f.ReadAt
	buf := make([]byte, 1)
	n, err = f.ReadAt(buf, math.MaxInt64-1)
	if err != nil {
		t.Fatalf("ReadAt(math.MaxInt64-1) échoué: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReadAt(math.MaxInt64-1) n=%d, attendu 1", n)
	}
	if buf[0] != 0x7e {
		t.Fatalf("ReadAt(math.MaxInt64-1) attendu 0x7e, obtenu 0x%02x", buf[0])
	}

	// 3. Vérifier qu'une écriture tentée à math.MaxInt64 renvoie explicitement ErrInvalidOffset sans paniquer
	_, err = f.WriteAt([]byte{0x7e}, math.MaxInt64)
	if !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("WriteAt(math.MaxInt64) attendu ErrInvalidOffset, obtenu %v", err)
	}

	// Écriture tentée avec débordement au-delà (2 octets à math.MaxInt64 - 1)
	_, err = f.WriteAt([]byte{0x7e, 0x7e}, math.MaxInt64-1)
	if !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("WriteAt(math.MaxInt64-1, len=2) attendu ErrInvalidOffset, obtenu %v", err)
	}
}

func TestVFSFile_DirtyBufferDefersPut(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_dirty", "defer.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	page := bytes.Repeat([]byte{0xA5}, BlockSize)
	if _, err := f.WriteAt(page, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	b0 := BlockKey("tenant_dirty", "defer.db", f.Generation(), 0)
	if _, err := storage.Get(b0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("le bloc 0 ne doit pas être persisté avant flush, err=%v", err)
	}

	got := make([]byte, BlockSize)
	n, err := f.ReadAt(got, 0)
	if err != nil || n != BlockSize || !bytes.Equal(got, page) {
		t.Fatalf("ReadAt depuis dirty: n=%d err=%v", n, err)
	}

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	raw, err := storage.Get(b0)
	if err != nil {
		t.Fatalf("bloc 0 absent après Sync: %v", err)
	}
	if !bytes.Equal(raw, page) {
		t.Fatalf("contenu persisté corrompu")
	}
}

func TestVFSFile_DirtyBufferPartialRMW(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_dirty", "partial.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	base := bytes.Repeat([]byte{0x11}, BlockSize)
	if _, err := f.WriteAt(base, 0); err != nil {
		t.Fatalf("WriteAt base: %v", err)
	}
	patch := bytes.Repeat([]byte{0x22}, 64)
	if _, err := f.WriteAt(patch, 100); err != nil {
		t.Fatalf("WriteAt patch: %v", err)
	}

	b0 := BlockKey("tenant_dirty", "partial.db", f.Generation(), 0)
	if _, err := storage.Get(b0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RMW partiel ne doit pas Put avant flush, err=%v", err)
	}

	got := make([]byte, BlockSize)
	n, err := f.ReadAt(got, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("ReadAt: n=%d err=%v", n, err)
	}
	if !bytes.Equal(got[100:164], patch) {
		t.Fatalf("plage partielle corrompue")
	}
	if got[99] != 0x11 || got[164] != 0x11 {
		t.Fatalf("voisins du patch altérés")
	}

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	raw, err := storage.Get(b0)
	if err != nil {
		t.Fatalf("Get après Sync: %v", err)
	}
	if !bytes.Equal(raw[100:164], patch) {
		t.Fatalf("patch persisté corrompu")
	}
}

func TestVFSFile_DirtyBufferFlushZeroDeletes(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_dirty", "zero.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	nonZero := bytes.Repeat([]byte{0x7F}, BlockSize)
	if _, err := f.WriteAt(nonZero, 0); err != nil {
		t.Fatalf("WriteAt nonZero: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync nonZero: %v", err)
	}
	b0 := BlockKey("tenant_dirty", "zero.db", f.Generation(), 0)
	if _, err := storage.Get(b0); err != nil {
		t.Fatalf("bloc 0 devrait exister: %v", err)
	}

	zeros := make([]byte, BlockSize)
	if _, err := f.WriteAt(zeros, 0); err != nil {
		t.Fatalf("WriteAt zeros: %v", err)
	}
	if _, err := storage.Get(b0); err != nil {
		t.Fatalf("le bloc persisté doit rester jusqu'au flush: %v", err)
	}

	got := make([]byte, BlockSize)
	n, err := f.ReadAt(got, 0)
	if err != nil || n != BlockSize {
		t.Fatalf("ReadAt zeros: n=%d err=%v", n, err)
	}
	for i, b := range got {
		if b != 0 {
			t.Fatalf("octet dirty non nul à %d: 0x%02x", i, b)
		}
	}

	if err := f.Sync(); err != nil {
		t.Fatalf("Sync zeros: %v", err)
	}
	if _, err := storage.Get(b0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bloc zéro aurait dû être Delete au flush, err=%v", err)
	}
}

func TestVFSFile_DirtyBufferThresholdFlush(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_dirty", "threshold.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	page := bytes.Repeat([]byte{0x3C}, BlockSize)
	for i := 0; i < dirtyFlushThreshold-1; i++ {
		if _, err := f.WriteAt(page, int64(i)*BlockSize); err != nil {
			t.Fatalf("WriteAt bloc %d: %v", i, err)
		}
	}
	b0 := BlockKey("tenant_dirty", "threshold.db", f.Generation(), 0)
	if _, err := storage.Get(b0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("aucun flush sous le seuil, err=%v", err)
	}

	if _, err := f.WriteAt(page, int64(dirtyFlushThreshold-1)*BlockSize); err != nil {
		t.Fatalf("WriteAt bloc seuil: %v", err)
	}
	if _, err := storage.Get(b0); err != nil {
		t.Fatalf("flush automatique attendu au seuil: %v", err)
	}
	last := BlockKey("tenant_dirty", "threshold.db", f.Generation(), uint64(dirtyFlushThreshold-1))
	if _, err := storage.Get(last); err != nil {
		t.Fatalf("dernier bloc du seuil absent: %v", err)
	}
}

func TestVFSFile_DirtyBufferTruncateDrops(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_dirty", "trunc.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	data := bytes.Repeat([]byte{0x5A}, BlockSize*3)
	if _, err := f.WriteAt(data, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := f.Truncate(BlockSize); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	f.state.mu.Lock()
	for b := range f.state.dirtyBlocks {
		if b >= 1 {
			f.state.mu.Unlock()
			t.Fatalf("dirty block %d aurait dû être retiré par Truncate", b)
		}
	}
	f.state.mu.Unlock()

	b1 := BlockKey("tenant_dirty", "trunc.db", f.Generation(), 1)
	if _, err := storage.Get(b1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bloc 1 devrait être absent, err=%v", err)
	}

	if err := f.Truncate(BlockSize * 2); err != nil {
		t.Fatalf("re-extend: %v", err)
	}
	got := make([]byte, BlockSize)
	n, err := f.ReadAt(got, BlockSize)
	if err != nil || n != BlockSize {
		t.Fatalf("ReadAt ré-étendu: n=%d err=%v", n, err)
	}
	for i, b := range got {
		if b != 0 {
			t.Fatalf("fantôme à offset %d: 0x%02x", BlockSize+i, b)
		}
	}
}

func TestVFSFile_DirtyBufferCloseFlushes(t *testing.T) {
	storage := newMemStorage()
	f, err := OpenVFSFile(storage, "tenant_dirty", "close.db")
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	payload := []byte("close_flush_payload")
	if _, err := f.WriteAt(payload, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f2, err := OpenVFSFile(storage, "tenant_dirty", "close.db")
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	defer f2.Close()

	sz, err := f2.Size()
	if err != nil || sz != int64(len(payload)) {
		t.Fatalf("Size réouvert: %d err=%v", sz, err)
	}
	got := make([]byte, len(payload))
	n, err := f2.ReadAt(got, 0)
	if err != nil || n != len(payload) || !bytes.Equal(got, payload) {
		t.Fatalf("ReadAt réouvert: n=%d err=%v got=%q", n, err, got)
	}
}

func TestVFSFile_CloseStalePreservesNewGeneration(t *testing.T) {
	storage := newMemStorage()
	const tenant = "tenant_stale_close"
	const fileID = "stale_close.db"

	f1, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile f1: %v", err)
	}
	gen1 := f1.Generation()
	oldPayload := bytes.Repeat([]byte{0xA1}, 64)
	if _, err := f1.WriteAt(oldPayload, 0); err != nil {
		t.Fatalf("f1.WriteAt: %v", err)
	}
	if err := f1.Sync(); err != nil {
		t.Fatalf("f1.Sync: %v", err)
	}

	f2, err := CreateVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("CreateVFSFile f2: %v", err)
	}
	defer f2.Close()
	gen2 := f2.Generation()
	if gen2 <= gen1 {
		t.Fatalf("génération f2 (%d) doit dépasser f1 (%d)", gen2, gen1)
	}

	newPayload := bytes.Repeat([]byte{0xB2}, 128)
	if _, err := f2.WriteAt(newPayload, 0); err != nil {
		t.Fatalf("f2.WriteAt: %v", err)
	}

	if err := f1.Close(); err != nil {
		t.Fatalf("f1.Close périmé: %v", err)
	}

	k1 := BlockKey(tenant, fileID, gen1, 0)
	if raw1, err := storage.Get(k1); err == nil {
		if len(raw1) >= len(newPayload) && bytes.Equal(raw1[:len(newPayload)], newPayload) {
			t.Fatalf("Close périmé a persisté les dirty blocks sous la génération 1")
		}
	}

	metaRaw, err := storage.Get(MetaKey(tenant, fileID))
	if err != nil {
		t.Fatalf("Get meta après Close périmé: %v", err)
	}
	meta, err := decodeVFSMeta(metaRaw)
	if err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta.generation != gen2 {
		t.Fatalf("Close périmé a réécrit la génération: obtenu %d, attendu %d", meta.generation, gen2)
	}
	if meta.size == int64(len(newPayload)) && meta.generation == gen1 {
		t.Fatalf("Close périmé a réécrit la taille de f2 sous la génération 1")
	}

	sz, err := f2.Size()
	if err != nil || sz != int64(len(newPayload)) {
		t.Fatalf("taille f2 après Close périmé: %d err=%v", sz, err)
	}
	got := make([]byte, len(newPayload))
	n, err := f2.ReadAt(got, 0)
	if err != nil || n != len(newPayload) || !bytes.Equal(got, newPayload) {
		t.Fatalf("données f2 corrompues par Close périmé: n=%d err=%v", n, err)
	}

	if err := f2.Sync(); err != nil {
		t.Fatalf("f2.Sync: %v", err)
	}
	if err := f2.Close(); err != nil {
		t.Fatalf("f2.Close: %v", err)
	}

	f3, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	defer f3.Close()
	if f3.Generation() != gen2 {
		t.Fatalf("génération réouverte %d, attendue %d", f3.Generation(), gen2)
	}
	sz3, err := f3.Size()
	if err != nil || sz3 != int64(len(newPayload)) {
		t.Fatalf("Size réouvert: %d err=%v", sz3, err)
	}
	got3 := make([]byte, len(newPayload))
	n, err = f3.ReadAt(got3, 0)
	if err != nil || n != len(newPayload) || !bytes.Equal(got3, newPayload) {
		t.Fatalf("ReadAt réouvert: n=%d err=%v", n, err)
	}
}

func TestVFSFile_StorageIsolation(t *testing.T) {
	s1 := newMemStorage()
	s2 := newMemStorage()
	const tenant = "tenant_iso"
	const fileID = "same_name.db"

	if fileRegistryKey(s1, tenant, fileID) == fileRegistryKey(s2, tenant, fileID) {
		t.Fatalf("clés de registre identiques pour deux storages distincts")
	}

	f1, err := OpenVFSFile(s1, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile s1: %v", err)
	}
	defer f1.Close()
	f2, err := OpenVFSFile(s2, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile s2: %v", err)
	}
	defer f2.Close()

	if f1.state == f2.state {
		t.Fatalf("sharedFileState partagé entre deux VFSStorage distincts")
	}

	payload1 := bytes.Repeat([]byte{0x11}, BlockSize)
	if _, err := f1.WriteAt(payload1, 0); err != nil {
		t.Fatalf("f1.WriteAt: %v", err)
	}

	f2.state.mu.Lock()
	leaked := len(f2.state.dirtyBlocks)
	f2.state.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("dirtyBlocks de s2 contaminé par s1: %d entrée(s)", leaked)
	}
	sz2, err := f2.Size()
	if err != nil || sz2 != 0 {
		t.Fatalf("taille s2 contaminée: %d err=%v", sz2, err)
	}

	payload2 := bytes.Repeat([]byte{0x22}, BlockSize)
	if _, err := f2.WriteAt(payload2, 0); err != nil {
		t.Fatalf("f2.WriteAt: %v", err)
	}

	got1 := make([]byte, BlockSize)
	n, err := f1.ReadAt(got1, 0)
	if err != nil || n != BlockSize || !bytes.Equal(got1, payload1) {
		t.Fatalf("tampon s1 écrasé par s2: n=%d err=%v", n, err)
	}
	got2 := make([]byte, BlockSize)
	n, err = f2.ReadAt(got2, 0)
	if err != nil || n != BlockSize || !bytes.Equal(got2, payload2) {
		t.Fatalf("tampon s2 écrasé par s1: n=%d err=%v", n, err)
	}

	f1.state.mu.Lock()
	f2.state.mu.Lock()
	sameMap := false
	if len(f1.state.dirtyBlocks) > 0 && len(f2.state.dirtyBlocks) > 0 {
		p1 := f1.state.dirtyBlocks[0]
		p2 := f2.state.dirtyBlocks[0]
		if len(p1) > 0 && len(p2) > 0 && &p1[0] == &p2[0] {
			sameMap = true
		}
	}
	f2.state.mu.Unlock()
	f1.state.mu.Unlock()
	if sameMap {
		t.Fatalf("pages dirtyBlocks partagées entre storages")
	}
}

func TestVFSFile_GenerationRaceFree(t *testing.T) {
	storage := newMemStorage()
	const tenant = "tenant_gen_race"
	const fileID = "race.db"
	const goroutines = 32
	const iters = 40

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				switch id % 3 {
				case 0:
					f, err := OpenVFSFile(storage, tenant, fileID)
					if err != nil {
						continue
					}
					_, _ = f.WriteAt([]byte{byte(id)}, 0)
					_ = f.Close()
				case 1:
					f, err := CreateVFSFile(storage, tenant, fileID)
					if err != nil {
						continue
					}
					_, _ = f.WriteAt([]byte{byte(id)}, 0)
					_ = f.Close()
				default:
					_ = DeleteVFSFile(storage, tenant, fileID)
				}
			}
		}(g)
	}
	wg.Wait()
}

type failMetaPutStorage struct {
	inner    *memStorage
	mu       sync.Mutex
	failOnce bool
	failErr  error
}

func (s *failMetaPutStorage) Get(key []byte) ([]byte, error) {
	return s.inner.Get(key)
}

func (s *failMetaPutStorage) Put(key, val []byte, opts ...WriteOption) error {
	s.mu.Lock()
	if s.failOnce && bytes.HasSuffix(key, []byte(":meta")) {
		s.failOnce = false
		err := s.failErr
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return s.inner.Put(key, val, opts...)
}

func (s *failMetaPutStorage) Delete(key []byte) error {
	return s.inner.Delete(key)
}

func TestVFSFile_MetaDirtyRetryOnFailure(t *testing.T) {
	inner := newMemStorage()
	failErr := errors.New("injected meta put failure")
	storage := &failMetaPutStorage{inner: inner}
	const tenant = "tenant_meta_retry"
	const fileID = "meta_retry.db"

	f, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	payload := []byte("meta_dirty_retry_payload")
	if _, err := f.WriteAt(payload, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	storage.mu.Lock()
	storage.failOnce = true
	storage.failErr = failErr
	storage.mu.Unlock()

	if err := f.Close(); !errors.Is(err, failErr) {
		t.Fatalf("Close injecté: obtenu %v, attendu %v", err, failErr)
	}

	f.state.mu.Lock()
	metaDirty := f.state.metaDirty
	closed := f.closed
	f.state.mu.Unlock()
	if !metaDirty {
		t.Fatalf("metaDirty doit rester true après échec de Close")
	}
	if closed {
		t.Fatalf("handle marqué closed malgré l'échec de persistance des métadonnées")
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close après rétablissement: %v", err)
	}

	f2, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	defer f2.Close()

	sz, err := f2.Size()
	if err != nil || sz != int64(len(payload)) {
		t.Fatalf("Size réouvert: %d err=%v", sz, err)
	}
	got := make([]byte, len(payload))
	n, err := f2.ReadAt(got, 0)
	if err != nil || n != len(payload) || !bytes.Equal(got, payload) {
		t.Fatalf("ReadAt réouvert: n=%d err=%v got=%q", n, err, got)
	}
	metaRaw, err := storage.Get(MetaKey(tenant, fileID))
	if err != nil {
		t.Fatalf("Get meta réouvert: %v", err)
	}
	meta, err := decodeVFSMeta(metaRaw)
	if err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta.generation != f2.Generation() {
		t.Fatalf("génération persistée %d, attendue %d", meta.generation, f2.Generation())
	}
	if meta.size != int64(len(payload)) {
		t.Fatalf("taille persistée %d, attendue %d", meta.size, len(payload))
	}
}

func TestVFSFile_ConcurrentCreateIncrementsMonotonically(t *testing.T) {
	storage := newMemStorage()
	const tenant = "tenant_create_mono"
	const fileID = "mono.db"
	const n = 10

	gens := make([]uint64, n)
	files := make([]*VFSFile, n)
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			f, err := CreateVFSFile(storage, tenant, fileID)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			gens[idx] = f.Generation()
			files[idx] = f
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("CreateVFSFile: %v", firstErr)
	}
	for i := 0; i < n; i++ {
		if files[i] != nil {
			defer files[i].Close()
		}
	}

	seen := make(map[uint64]struct{}, n)
	sorted := make([]uint64, n)
	copy(sorted, gens)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i := 0; i < n; i++ {
		g := gens[i]
		if g == 0 {
			t.Fatalf("génération nulle à l'indice %d", i)
		}
		if _, ok := seen[g]; ok {
			t.Fatalf("collision de génération %d", g)
		}
		seen[g] = struct{}{}
	}
	for i := 1; i < n; i++ {
		if sorted[i] != sorted[i-1]+1 {
			t.Fatalf("générations non strictement monotones: %v", sorted)
		}
	}
	if sorted[0] != 1 || sorted[n-1] != uint64(n) {
		t.Fatalf("séquence attendue 1..%d, obtenu %v", n, sorted)
	}
}

type failGetStorage struct {
	inner   *memStorage
	mu      sync.Mutex
	failErr error
}

func (s *failGetStorage) Get(key []byte) ([]byte, error) {
	s.mu.Lock()
	err := s.failErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.inner.Get(key)
}

func (s *failGetStorage) Put(key, val []byte, opts ...WriteOption) error {
	return s.inner.Put(key, val, opts...)
}

func (s *failGetStorage) Delete(key []byte) error {
	return s.inner.Delete(key)
}

func TestVFSFile_CloseStorageErrorPreservesDirty(t *testing.T) {
	inner := newMemStorage()
	failErr := errors.New("injected storage get failure")
	storage := &failGetStorage{inner: inner}
	const tenant = "tenant_close_io"
	const fileID = "close_io.db"

	f, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}

	payload := bytes.Repeat([]byte{0xC3}, 64)
	if _, err := f.WriteAt(payload, 0); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	f.state.mu.Lock()
	dirtyBefore := len(f.state.dirtyBlocks)
	f.state.mu.Unlock()
	if dirtyBefore == 0 {
		t.Fatalf("WriteAt n'a pas produit de dirtyBlocks")
	}

	storage.mu.Lock()
	storage.failErr = failErr
	storage.mu.Unlock()

	if err := f.Close(); !errors.Is(err, failErr) {
		t.Fatalf("Close injecté: obtenu %v, attendu %v", err, failErr)
	}

	f.state.mu.Lock()
	dirtyAfter := len(f.state.dirtyBlocks)
	closed := f.closed
	f.state.mu.Unlock()
	if closed {
		t.Fatalf("handle marqué closed malgré l'erreur d'I/O")
	}
	if dirtyAfter != dirtyBefore {
		t.Fatalf("dirtyBlocks perdus: avant %d après %d", dirtyBefore, dirtyAfter)
	}

	storage.mu.Lock()
	storage.failErr = nil
	storage.mu.Unlock()

	if err := f.Close(); err != nil {
		t.Fatalf("Close après rétablissement: %v", err)
	}

	f2, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	defer f2.Close()
	sz, err := f2.Size()
	if err != nil || sz != int64(len(payload)) {
		t.Fatalf("Size réouvert: %d err=%v", sz, err)
	}
	got := make([]byte, len(payload))
	n, err := f2.ReadAt(got, 0)
	if err != nil || n != len(payload) || !bytes.Equal(got, payload) {
		t.Fatalf("ReadAt réouvert: n=%d err=%v got=%q", n, err, got)
	}
}

func TestVFSFile_CreateFailurePreservesOldData(t *testing.T) {
	inner := newMemStorage()
	failErr := errors.New("injected meta put failure")
	storage := &failMetaPutStorage{inner: inner}
	const tenant = "tenant_cow_create"
	const fileID = "cow_create.db"
	const nBlocks = 3

	f, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("OpenVFSFile: %v", err)
	}
	defer f.Close()

	patterns := [nBlocks][]byte{
		bytes.Repeat([]byte{0xA1}, BlockSize),
		bytes.Repeat([]byte{0xB2}, BlockSize),
		bytes.Repeat([]byte{0xC3}, BlockSize),
	}
	for i, p := range patterns {
		if _, err := f.WriteAt(p, int64(i)*BlockSize); err != nil {
			t.Fatalf("WriteAt bloc %d: %v", i, err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	oldGen := f.Generation()
	wantSize := int64(nBlocks) * BlockSize

	storage.mu.Lock()
	storage.failOnce = true
	storage.failErr = failErr
	storage.mu.Unlock()

	created, err := CreateVFSFile(storage, tenant, fileID)
	if err == nil {
		created.Close()
		t.Fatal("CreateVFSFile aurait dû échouer sur Put(MetaKey)")
	}
	if !errors.Is(err, failErr) {
		t.Fatalf("CreateVFSFile: obtenu %v, attendu %v", err, failErr)
	}

	sz, err := f.Size()
	if err != nil || sz != wantSize {
		t.Fatalf("Size après échec: %d err=%v, attendu %d", sz, err, wantSize)
	}
	if f.Generation() != oldGen {
		t.Fatalf("génération mutée: %d, attendue %d", f.Generation(), oldGen)
	}

	zeros := make([]byte, BlockSize)
	block := make([]byte, BlockSize)
	for i, p := range patterns {
		n, err := f.ReadAt(block, int64(i)*BlockSize)
		if err != nil || n != BlockSize {
			t.Fatalf("ReadAt handle ancien bloc %d: n=%d err=%v", i, n, err)
		}
		if bytes.Equal(block, zeros) {
			t.Fatalf("zéro fantôme sur le bloc %d après échec de CreateVFSFile", i)
		}
		if !bytes.Equal(block, p) {
			t.Fatalf("bloc %d corrompu: premier octet 0x%02x, attendu 0x%02x", i, block[0], p[0])
		}
		raw, err := inner.Get(BlockKey(tenant, fileID, oldGen, uint64(i)))
		if err != nil {
			t.Fatalf("bloc persisté %d disparu: %v", i, err)
		}
		if !bytes.Equal(raw, p) {
			t.Fatalf("bloc persisté %d altéré", i)
		}
	}

	reopened, err := OpenVFSFile(storage, tenant, fileID)
	if err != nil {
		t.Fatalf("réouverture: %v", err)
	}
	defer reopened.Close()
	sz, err = reopened.Size()
	if err != nil || sz != wantSize {
		t.Fatalf("Size réouvert: %d err=%v, attendu %d", sz, err, wantSize)
	}
	if reopened.Generation() != oldGen {
		t.Fatalf("génération réouverte %d, attendue %d", reopened.Generation(), oldGen)
	}
	for i, p := range patterns {
		n, err := reopened.ReadAt(block, int64(i)*BlockSize)
		if err != nil || n != BlockSize || !bytes.Equal(block, p) {
			t.Fatalf("ReadAt réouvert bloc %d: n=%d err=%v premier=0x%02x", i, n, err, block[0])
		}
	}
}
