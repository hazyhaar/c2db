// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
)

// BlockSize définit la granularité des blocs logiques VFS SQLite (4096 octets).
const BlockSize = 4096

const dirtyFlushThreshold = 256

// Flags des métadonnées du fichier VFS.
const (
	VFSFlagNormal  uint32 = 0
	VFSFlagDeleted uint32 = 1 << 0
)

var (
	// ErrShortRead est retourné lorsqu'une lecture dépasse la fin du fichier.
	ErrShortRead = errors.New("short read")

	// ErrStaleHandle est retourné lorsqu'un descripteur de fichier est devenu périmé
	// suite à une suppression ou ré-instanciation avec une nouvelle génération.
	ErrStaleHandle = errors.New("c2db: stale file handle")

	// ErrInvalidOffset est retourné lorsqu'un offset négatif ou provoquant un débordement arithmétique est fourni.
	ErrInvalidOffset = errors.New("c2db: invalid offset")

	// ErrInvalidSize est retourné lorsqu'une taille négative est fournie.
	ErrInvalidSize = errors.New("c2db: invalid size")
)

// VFSStorage abstrait le magasin clé-valeur c2db sous-jacent (ex: *DB, *Shard, ou mock).
// La signature d'écriture est variadique afin de porter une intention de
// durabilité par appel sans rompre les appelants qui n'en fournissent aucune.
type VFSStorage interface {
	Get(key []byte) ([]byte, error)
	Put(key, val []byte, opts ...WriteOption) error
	Delete(key []byte) error
}

// Syncer permet au magasin sous-jacent d'assurer la durabilité matérielle (fsync/fdatasync).
type Syncer interface {
	Sync() error
}

// Flusher permet de vider les tampons mémoire vers le stockage persistant.
type Flusher interface {
	Flush() error
}

// Checkpointer permet d'exécuter un point de contrôle WAL/journal.
type Checkpointer interface {
	Checkpoint() error
}

// KeySyncer permet de synchroniser de manière ciblée le shard associé à une clé.
type KeySyncer interface {
	SyncKey(key []byte) error
}

// sharedFileState maintient l'état partagé entre plusieurs descripteurs (handles)
// ouverts sur le même fichier virtuel (tenant, fileID). Il fournit un verrou
// unique pour éliminer tout conflit read-modify-write concurrent et assure
// une synchronisation en temps réel de la taille logique du fichier.
type sharedFileState struct {
	mu          sync.Mutex
	size        int64
	generation  uint64
	refCount    int
	dirtyBlocks map[uint64][]byte
	metaDirty   bool
}

type fileRegistry struct {
	mu     sync.Mutex
	states map[string]*sharedFileState
}

var globalFileRegistry = &fileRegistry{
	states: make(map[string]*sharedFileState),
}

func digitsLen(n int) int {
	if n < 10 {
		return 1
	}
	if n < 100 {
		return 2
	}
	if n < 1000 {
		return 3
	}
	if n < 10000 {
		return 4
	}
	count := 0
	for n > 0 {
		count++
		n /= 10
	}
	return count
}

// fileIdentityKey calcule l'identité logique : <len(tenant)>:<tenant>:<len(fileID)>:<fileID>
func fileIdentityKey(tenant, fileID string) string {
	lt := len(tenant)
	lf := len(fileID)
	totalLen := digitsLen(lt) + 1 + lt + 1 + digitsLen(lf) + 1 + lf
	var sb strings.Builder
	sb.Grow(totalLen)
	var tmp [32]byte
	sb.Write(strconv.AppendInt(tmp[:0], int64(lt), 10))
	sb.WriteByte(':')
	sb.WriteString(tenant)
	sb.WriteByte(':')
	sb.Write(strconv.AppendInt(tmp[:0], int64(lf), 10))
	sb.WriteByte(':')
	sb.WriteString(fileID)
	return sb.String()
}

// fileRegistryKey calcule la clé unique du registre, isolée par instance de stockage :
// <ptr(storage)>:<len(tenant)>:<tenant>:<len(fileID)>:<fileID>
func fileRegistryKey(storage VFSStorage, tenant, fileID string) string {
	return fmt.Sprintf("%p:%s", storage, fileIdentityKey(tenant, fileID))
}

func (r *fileRegistry) getOrCreateLocked(storage VFSStorage, tenant, fileID string) *sharedFileState {
	key := fileRegistryKey(storage, tenant, fileID)
	st, ok := r.states[key]
	if !ok {
		st = &sharedFileState{
			dirtyBlocks: make(map[uint64][]byte),
			refCount:    1,
		}
		r.states[key] = st
		return st
	}
	st.refCount++
	return st
}

func (r *fileRegistry) getOrCreate(storage VFSStorage, tenant, fileID string, initialSize int64, gen uint64) *sharedFileState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.getOrCreateLocked(storage, tenant, fileID)
	st.mu.Lock()
	if gen > st.generation {
		st.generation = gen
		st.size = initialSize
		st.dirtyBlocks = make(map[uint64][]byte)
		st.metaDirty = false
	}
	st.mu.Unlock()
	return st
}

func (r *fileRegistry) getState(storage VFSStorage, tenant, fileID string) *sharedFileState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.states[fileRegistryKey(storage, tenant, fileID)]
}

func (r *fileRegistry) releaseLocked(storage VFSStorage, tenant, fileID string) {
	key := fileRegistryKey(storage, tenant, fileID)
	if st, ok := r.states[key]; ok {
		st.refCount--
		if st.refCount <= 0 {
			delete(r.states, key)
		}
	}
}

func (r *fileRegistry) release(storage VFSStorage, tenant, fileID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseLocked(storage, tenant, fileID)
}

// VFSFile implémente le gestionnaire de fichier virtuel SQLite découpé en blocs logiques de 4 Ko.
type VFSFile struct {
	state      *sharedFileState
	storage    VFSStorage
	tenant     string
	fileID     string
	generation uint64
	flags      uint32
	closed     bool
}

// MetaKey retourne la clé c2db pour les métadonnées :
// vfs:<len(tenant)>:<tenant>:<len(fileID)>:<fileID>:meta
func MetaKey(tenant, fileID string) []byte {
	lt := len(tenant)
	lf := len(fileID)
	totalLen := 12 + digitsLen(lt) + lt + digitsLen(lf) + lf
	key := make([]byte, 0, totalLen)
	key = append(key, "vfs:"...)
	key = strconv.AppendInt(key, int64(lt), 10)
	key = append(key, ':')
	key = append(key, tenant...)
	key = append(key, ':')
	key = strconv.AppendInt(key, int64(lf), 10)
	key = append(key, ':')
	key = append(key, fileID...)
	key = append(key, ":meta"...)
	return key
}

// BlockKey retourne la clé c2db pour un bloc de données :
// vfs:<len(tenant)>:<tenant>:<len(fileID)>:<fileID>:g:<generation_8bytes_be>:b:<blockNo_8bytes_be>
// avec generation et blockNo encodés sur 8 octets big-endian.
// Cette séparation stricte immunise complètement le stockage contre tout résidu
// ou collision liée à des descripteurs périmés (zombies) issus d'anciennes générations.
func BlockKey(tenant, fileID string, generation, blockNo uint64) []byte {
	lt := len(tenant)
	lf := len(fileID)
	totalLen := 29 + digitsLen(lt) + lt + digitsLen(lf) + lf
	key := make([]byte, 0, totalLen)
	key = append(key, "vfs:"...)
	key = strconv.AppendInt(key, int64(lt), 10)
	key = append(key, ':')
	key = append(key, tenant...)
	key = append(key, ':')
	key = strconv.AppendInt(key, int64(lf), 10)
	key = append(key, ':')
	key = append(key, fileID...)
	key = append(key, ":g:"...)

	var be [8]byte
	binary.BigEndian.PutUint64(be[:], generation)
	key = append(key, be[:]...)
	key = append(key, ":b:"...)
	binary.BigEndian.PutUint64(be[:], blockNo)
	key = append(key, be[:]...)
	return key
}

// BlockKeyPrefix retourne le préfixe commun à tous les blocs d'une génération donnée du fichier :
// vfs:<len(tenant)>:<tenant>:<len(fileID)>:<fileID>:g:<generation_8bytes_be>:b:
func BlockKeyPrefix(tenant, fileID string, generation uint64) []byte {
	lt := len(tenant)
	lf := len(fileID)
	totalLen := 21 + digitsLen(lt) + lt + digitsLen(lf) + lf
	key := make([]byte, 0, totalLen)
	key = append(key, "vfs:"...)
	key = strconv.AppendInt(key, int64(lt), 10)
	key = append(key, ':')
	key = append(key, tenant...)
	key = append(key, ':')
	key = strconv.AppendInt(key, int64(lf), 10)
	key = append(key, ':')
	key = append(key, fileID...)
	key = append(key, ":g:"...)

	var be [8]byte
	binary.BigEndian.PutUint64(be[:], generation)
	key = append(key, be[:]...)
	key = append(key, ":b:"...)
	return key
}

func numBlocks(size int64) uint64 {
	if size <= 0 {
		return 0
	}
	return uint64((size-1)/BlockSize) + 1
}

type vfsMeta struct {
	size       int64
	generation uint64
	flags      uint32
}

func encodeVFSMeta(m vfsMeta) []byte {
	buf := make([]byte, 20)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(m.size))
	binary.LittleEndian.PutUint64(buf[8:16], m.generation)
	binary.LittleEndian.PutUint32(buf[16:20], m.flags)
	return buf
}

func decodeVFSMeta(buf []byte) (vfsMeta, error) {
	if len(buf) < 20 {
		return vfsMeta{}, errors.New("c2db: invalid vfs metadata payload length")
	}
	return vfsMeta{
		size:       int64(binary.LittleEndian.Uint64(buf[0:8])),
		generation: binary.LittleEndian.Uint64(buf[8:16]),
		flags:      binary.LittleEndian.Uint32(buf[16:20]),
	}, nil
}

// isZeroBlock4K vérifie si une page de 4 Ko est entièrement remplie de zéros.
// Tente d'utiliser le noyau SIMD Page4k_is_zero_avx2 si disponible, avec repli rapide 0-alloc uint64.
func isZeroBlock4K(page []byte) bool {
	if len(page) != BlockSize {
		for _, b := range page {
			if b != 0 {
				return false
			}
		}
		return true
	}
	if Page4k_is_zero_avx2(page, BlockSize) == 1 {
		return true
	}
	for i := 0; i < BlockSize; i += 8 {
		if binary.LittleEndian.Uint64(page[i:i+8]) != 0 {
			return false
		}
	}
	return true
}

// OpenVFSFile ouvre un fichier virtuel existant ou en crée un nouveau si absent.
func OpenVFSFile(storage VFSStorage, tenant, fileID string) (*VFSFile, error) {
	if storage == nil {
		return nil, os.ErrInvalid
	}
	k := MetaKey(tenant, fileID)
	raw, err := storage.Get(k)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			meta := vfsMeta{
				size:       0,
				generation: 1,
				flags:      VFSFlagNormal,
			}
			if err := storage.Put(k, encodeVFSMeta(meta)); err != nil {
				return nil, err
			}
			st := globalFileRegistry.getOrCreate(storage, tenant, fileID, 0, 1)
			return &VFSFile{
				state:      st,
				storage:    storage,
				tenant:     tenant,
				fileID:     fileID,
				generation: 1,
				flags:      VFSFlagNormal,
			}, nil
		}
		return nil, err
	}

	meta, err := decodeVFSMeta(raw)
	if err != nil {
		return nil, err
	}
	if meta.flags&VFSFlagDeleted != 0 {
		// Fichier marqué supprimé : ré-instanciation avec incrément de génération
		meta.generation++
		meta.flags = VFSFlagNormal
		meta.size = 0
		if err := storage.Put(k, encodeVFSMeta(meta)); err != nil {
			return nil, err
		}
	}

	st := globalFileRegistry.getOrCreate(storage, tenant, fileID, meta.size, meta.generation)

	return &VFSFile{
		state:      st,
		storage:    storage,
		tenant:     tenant,
		fileID:     fileID,
		generation: meta.generation,
		flags:      meta.flags,
	}, nil
}

// CreateVFSFile crée ou recrée un fichier virtuel.
// Si le fichier existait déjà, ses blocs sont purgés et sa génération est incrémentée.
func CreateVFSFile(storage VFSStorage, tenant, fileID string) (*VFSFile, error) {
	if storage == nil {
		return nil, os.ErrInvalid
	}
	k := MetaKey(tenant, fileID)

	globalFileRegistry.mu.Lock()
	defer globalFileRegistry.mu.Unlock()

	st := globalFileRegistry.getOrCreateLocked(storage, tenant, fileID)

	raw, err := storage.Get(k)
	var oldGen uint64
	var oldSize int64
	var hasOld bool
	if err == nil {
		oldMeta, errDec := decodeVFSMeta(raw)
		if errDec != nil {
			globalFileRegistry.releaseLocked(storage, tenant, fileID)
			return nil, errDec
		}
		oldGen = oldMeta.generation
		oldSize = oldMeta.size
		hasOld = true
	} else if !errors.Is(err, ErrNotFound) {
		globalFileRegistry.releaseLocked(storage, tenant, fileID)
		return nil, err
	}

	st.mu.Lock()
	memGen := st.generation
	st.mu.Unlock()
	newGen := max(oldGen, memGen) + 1

	meta := vfsMeta{
		size:       0,
		generation: newGen,
		flags:      VFSFlagNormal,
	}
	if err := storage.Put(k, encodeVFSMeta(meta)); err != nil {
		globalFileRegistry.releaseLocked(storage, tenant, fileID)
		return nil, err
	}

	if hasOld {
		oldBlocks := numBlocks(oldSize)
		for b := uint64(0); b < oldBlocks; b++ {
			if err := storage.Delete(BlockKey(tenant, fileID, oldGen, b)); err != nil && !errors.Is(err, ErrNotFound) {
				globalFileRegistry.releaseLocked(storage, tenant, fileID)
				return nil, err
			}
		}
	}

	st.mu.Lock()
	st.generation = newGen
	st.size = 0
	st.dirtyBlocks = make(map[uint64][]byte)
	st.metaDirty = false
	st.mu.Unlock()

	return &VFSFile{
		state:      st,
		storage:    storage,
		tenant:     tenant,
		fileID:     fileID,
		generation: newGen,
		flags:      VFSFlagNormal,
	}, nil
}

// DeleteVFSFile supprime un fichier virtuel : ses blocs de données sont effacés,
// et sa génération est incrémentée avec le drapeau VFSFlagDeleted pour invalider
// immédiatement tout ancien descripteur encore ouvert.
func DeleteVFSFile(storage VFSStorage, tenant, fileID string) error {
	if storage == nil {
		return os.ErrInvalid
	}

	st := globalFileRegistry.getState(storage, tenant, fileID)
	if st != nil {
		st.mu.Lock()
		defer st.mu.Unlock()
	}

	k := MetaKey(tenant, fileID)
	raw, err := storage.Get(k)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}

	oldMeta, err := decodeVFSMeta(raw)
	if err != nil {
		return err
	}

	oldBlocks := numBlocks(oldMeta.size)
	for b := uint64(0); b < oldBlocks; b++ {
		if err := storage.Delete(BlockKey(tenant, fileID, oldMeta.generation, b)); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}

	newMeta := vfsMeta{
		size:       0,
		generation: oldMeta.generation + 1,
		flags:      VFSFlagDeleted,
	}
	if err := storage.Put(k, encodeVFSMeta(newMeta)); err != nil {
		return err
	}

	if st != nil {
		st.size = 0
		st.generation = newMeta.generation
		st.dirtyBlocks = make(map[uint64][]byte)
		st.metaDirty = false
	}
	return nil
}

func (f *VFSFile) checkStaleLocked() error {
	if f.closed {
		return os.ErrClosed
	}
	// Chemin chaud O(1) : le bump de génération (suppression/recréation par une
	// autre connexion) est déjà reflété dans sharedFileState.generation, partagé
	// entre tous les handles du même fichier. Aucune relecture du stockage.
	if f.generation != f.state.generation {
		return ErrStaleHandle
	}
	return nil
}

func (f *VFSFile) saveMetaLocked() error {
	if f.generation < f.state.generation {
		return ErrStaleHandle
	}
	k := MetaKey(f.tenant, f.fileID)
	raw, err := f.storage.Get(k)
	if err == nil {
		existing, decErr := decodeVFSMeta(raw)
		if decErr != nil {
			return decErr
		}
		if existing.generation > f.generation {
			return ErrStaleHandle
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	meta := vfsMeta{
		size:       f.state.size,
		generation: f.generation,
		flags:      f.flags,
	}
	if err := f.storage.Put(k, encodeVFSMeta(meta)); err != nil {
		return err
	}
	f.state.metaDirty = false
	return nil
}

// ReadAt lit len(buf) octets à partir de l'offset off.
// Conforme aux contrats stricts SQLite :
//   - Si off >= FileSize : ne lit rien, remplit buf de zéros et retourne (0, ErrShortRead).
//   - Si off < FileSize mais off + len(buf) > FileSize : lit jusqu'à FileSize, remplit le reste
//     du buffer de zéros (0x00) et retourne (nAvailable, ErrShortRead).
//   - Si un bloc intermédiaire n'existe pas en base (sparse) : le considère comme 4096 octets 0x00.
//   - Protégé contre tout dépassement arithmétique via des contrôles de limites soustractifs stricts.
func (f *VFSFile) ReadAt(buf []byte, off int64) (int, error) {
	if off < 0 || int64(len(buf)) > math.MaxInt64-off {
		return 0, ErrInvalidOffset
	}
	if len(buf) == 0 {
		f.state.mu.Lock()
		defer f.state.mu.Unlock()
		if err := f.checkStaleLocked(); err != nil {
			return 0, err
		}
		if off >= f.state.size {
			return 0, ErrShortRead
		}
		return 0, nil
	}

	f.state.mu.Lock()
	defer f.state.mu.Unlock()

	if err := f.checkStaleLocked(); err != nil {
		return 0, err
	}

	fileSize := f.state.size

	if off >= fileSize {
		clear(buf)
		return 0, ErrShortRead
	}

	toRead := len(buf)
	var retErr error
	if int64(toRead) > fileSize-off {
		toRead = int(fileSize - off)
		retErr = ErrShortRead
		clear(buf[toRead:])
	}

	bytesRead := 0
	for bytesRead < toRead {
		curOff := off + int64(bytesRead)
		blockNo := uint64(curOff / BlockSize)
		blockStartOff := int64(blockNo) * BlockSize
		var blockEndOff int64
		if blockStartOff > math.MaxInt64-BlockSize {
			blockEndOff = math.MaxInt64
		} else {
			blockEndOff = blockStartOff + BlockSize
		}

		offsetInBlock := int(curOff - blockStartOff)
		availInBlock := int(blockEndOff - curOff)
		chunk := toRead - bytesRead
		if chunk > availInBlock {
			chunk = availInBlock
		}

		if page, ok := f.state.dirtyBlocks[blockNo]; ok {
			avail := 0
			if offsetInBlock < len(page) {
				avail = copy(buf[bytesRead:bytesRead+chunk], page[offsetInBlock:])
			}
			if avail < chunk {
				clear(buf[bytesRead+avail : bytesRead+chunk])
			}
		} else {
			k := BlockKey(f.tenant, f.fileID, f.generation, blockNo)
			raw, err := f.storage.Get(k)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					clear(buf[bytesRead : bytesRead+chunk])
				} else {
					return bytesRead, err
				}
			} else {
				avail := 0
				if offsetInBlock < len(raw) {
					avail = copy(buf[bytesRead:bytesRead+chunk], raw[offsetInBlock:])
				}
				if avail < chunk {
					clear(buf[bytesRead+avail : bytesRead+chunk])
				}
			}
		}
		bytesRead += chunk
	}

	return bytesRead, retErr
}

// WriteAt écrit len(buf) octets à l'offset off.
// Conforme aux contrats stricts SQLite :
// - Accepte des écritures arbitraires traversant plusieurs frontières de blocs 4 Ko.
// - Effectue un read-modify-write propre pour les écritures partielles.
// - Coalesce les pages dans dirtyBlocks ; la persistance (Put/Delete sparse) a lieu au flush.
// - Met à jour atomiquement la taille du fichier si off + len(buf) > FileSize.
// - Protégé contre tout dépassement arithmétique via des contrôles de limites soustractifs stricts.
func (f *VFSFile) WriteAt(buf []byte, off int64) (int, error) {
	if off < 0 || off == math.MaxInt64 || int64(len(buf)) > math.MaxInt64-off {
		return 0, ErrInvalidOffset
	}
	if len(buf) == 0 {
		return 0, nil
	}

	f.state.mu.Lock()
	defer f.state.mu.Unlock()

	if err := f.checkStaleLocked(); err != nil {
		return 0, err
	}

	if f.state.dirtyBlocks == nil {
		f.state.dirtyBlocks = make(map[uint64][]byte)
	}

	bufLen := int64(len(buf))
	firstBlock := uint64(off / BlockSize)
	lastBlock := uint64((off + bufLen - 1) / BlockSize)

	written := 0
	for b := firstBlock; b <= lastBlock; b++ {
		blockStartOff := int64(b) * BlockSize
		var blockEndOff int64
		if blockStartOff > math.MaxInt64-BlockSize {
			blockEndOff = math.MaxInt64
		} else {
			blockEndOff = blockStartOff + BlockSize
		}

		writeStart := off + int64(written)
		writeEnd := off + bufLen
		if writeEnd > blockEndOff {
			writeEnd = blockEndOff
		}
		writeLen := int(writeEnd - writeStart)
		offsetInBlock := int(writeStart - blockStartOff)

		if offsetInBlock == 0 && writeLen == BlockSize {
			page := make([]byte, BlockSize)
			copy(page, buf[written:written+BlockSize])
			f.state.dirtyBlocks[b] = page
		} else {
			page, ok := f.state.dirtyBlocks[b]
			if !ok {
				page = make([]byte, BlockSize)
				k := BlockKey(f.tenant, f.fileID, f.generation, b)
				raw, err := f.storage.Get(k)
				if err == nil {
					copy(page, raw)
				} else if !errors.Is(err, ErrNotFound) {
					return written, err
				}
			}
			copy(page[offsetInBlock:offsetInBlock+writeLen], buf[written:written+writeLen])
			f.state.dirtyBlocks[b] = page
		}
		written += writeLen
	}

	newEnd := off + bufLen
	if newEnd > f.state.size {
		f.state.size = newEnd
		f.state.metaDirty = true
	}

	if len(f.state.dirtyBlocks) >= dirtyFlushThreshold {
		if err := f.flushDirtyLocked(); err != nil {
			return written, err
		}
	}

	return written, nil
}

// Truncate ajuste la taille du fichier.
// Conforme aux contrats stricts SQLite :
//   - Si la taille diminue : supprime tous les blocs strictement supérieurs à newSize / 4096.
//   - Pour le dernier bloc partiel (à l'offset newSize / 4096) : efface (zéro-fill) les octets
//     entre newSize % 4096 et 4096, empêchant toute résurrection d'octets fantômes.
//   - Si size == 0 : tous les blocs du fichier sont intégralement supprimés.
//   - Si size > oldSize : la taille logique est étendue sans allouer de blocs inutiles (sparse).
//   - Propage scrupuleusement les erreurs de suppression de blocs au lieu de les masquer.
func (f *VFSFile) Truncate(size int64) error {
	if size < 0 {
		return ErrInvalidSize
	}

	f.state.mu.Lock()
	defer f.state.mu.Unlock()

	if err := f.checkStaleLocked(); err != nil {
		return err
	}

	if f.state.dirtyBlocks == nil {
		f.state.dirtyBlocks = make(map[uint64][]byte)
	}

	oldSize := f.state.size
	if size < oldSize {
		lastBlockIdx := uint64(size / BlockSize)
		remainder := size % BlockSize

		if remainder > 0 {
			page, ok := f.state.dirtyBlocks[lastBlockIdx]
			if !ok {
				k := BlockKey(f.tenant, f.fileID, f.generation, lastBlockIdx)
				raw, err := f.storage.Get(k)
				if err == nil {
					page = make([]byte, BlockSize)
					copy(page, raw)
					f.state.dirtyBlocks[lastBlockIdx] = page
				} else if !errors.Is(err, ErrNotFound) {
					return err
				}
			}
			if page != nil {
				clear(page[remainder:])
			}
		}

		var firstBlockToDelete uint64
		if remainder > 0 {
			firstBlockToDelete = lastBlockIdx + 1
		} else {
			firstBlockToDelete = lastBlockIdx
		}

		for b := range f.state.dirtyBlocks {
			if b >= firstBlockToDelete {
				delete(f.state.dirtyBlocks, b)
			}
		}

		oldNumBlocks := numBlocks(oldSize)
		for b := firstBlockToDelete; b < oldNumBlocks; b++ {
			k := BlockKey(f.tenant, f.fileID, f.generation, b)
			if err := f.storage.Delete(k); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}

	f.state.size = size
	f.state.metaDirty = true
	return f.saveMetaLocked()
}

func (f *VFSFile) flushDirtyLocked() error {
	if f.state.dirtyBlocks == nil {
		f.state.dirtyBlocks = make(map[uint64][]byte)
	}
	// Flush atomique : les blocs sales ne sont vidés qu'après le succès intégral
	// de toutes les opérations de bloc ET de la méta. Un échec intermédiaire
	// préserve les dirtyBlocks pour une nouvelle tentative (idempotence Put/Delete).
	for b, page := range f.state.dirtyBlocks {
		k := BlockKey(f.tenant, f.fileID, f.generation, b)
		if isZeroBlock4K(page) {
			if err := f.storage.Delete(k); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		} else {
			if err := f.storage.Put(k, page); err != nil {
				return err
			}
		}
	}
	if err := f.saveMetaLocked(); err != nil {
		return err
	}
	f.state.dirtyBlocks = make(map[uint64][]byte)
	f.state.metaDirty = false
	return nil
}

// Sync garantit la durabilité des écritures et métadonnées du fichier.
// Enregistre les métadonnées puis relaie l'ordre de persistance au stockage sous-jacent
// s'il implémente Syncer, Flusher ou Checkpointer.
func (f *VFSFile) Sync() error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()

	if err := f.checkStaleLocked(); err != nil {
		return err
	}

	if len(f.state.dirtyBlocks) > 0 || f.state.metaDirty {
		if err := f.flushDirtyLocked(); err != nil {
			return err
		}
	}

	if ks, ok := f.storage.(KeySyncer); ok {
		k := MetaKey(f.tenant, f.fileID)
		return ks.SyncKey(k)
	}
	if s, ok := f.storage.(Syncer); ok {
		return s.Sync()
	}
	if fl, ok := f.storage.(Flusher); ok {
		return fl.Flush()
	}
	if cp, ok := f.storage.(Checkpointer); ok {
		return cp.Checkpoint()
	}
	return nil
}

// Size retourne la taille actuelle du fichier.
func (f *VFSFile) Size() (int64, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	if err := f.checkStaleLocked(); err != nil {
		return 0, err
	}
	return f.state.size, nil
}

// FileSize est un alias de Size compatible avec les conventions VFS.
func (f *VFSFile) FileSize() (int64, error) {
	return f.Size()
}

// Generation retourne le numéro de génération associé à ce descripteur.
func (f *VFSFile) Generation() uint64 {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	return f.generation
}

// FileID retourne l'identifiant du fichier.
func (f *VFSFile) FileID() string {
	return f.fileID
}

// Tenant retourne le tenant du fichier.
func (f *VFSFile) Tenant() string {
	return f.tenant
}

// Close ferme le descripteur de fichier et libère sa référence dans le registre partagé.
func (f *VFSFile) Close() error {
	f.state.mu.Lock()
	if f.closed {
		f.state.mu.Unlock()
		return nil
	}
	if err := f.checkStaleLocked(); err != nil {
		if errors.Is(err, ErrStaleHandle) || f.generation != f.state.generation {
			f.closed = true
			f.state.mu.Unlock()
			globalFileRegistry.release(f.storage, f.tenant, f.fileID)
			return nil
		}
		f.state.mu.Unlock()
		return err
	}
	if len(f.state.dirtyBlocks) > 0 || f.state.metaDirty {
		if flushErr := f.flushDirtyLocked(); flushErr != nil {
			f.state.mu.Unlock()
			return flushErr
		}
	}
	f.closed = true
	f.state.mu.Unlock()

	globalFileRegistry.release(f.storage, f.tenant, f.fileID)
	return nil
}

// ShardColocatedStorage adapte un *Shard pour satisfaire VFSStorage, permettant
// de colocaliser toutes les données et métadonnées d'un fichier virtuel (ainsi que
// ses journaux/WAL associés) sur un shard unique et déterminé d'un *DB c2db.
type ShardColocatedStorage struct {
	shard *Shard
}

// NewShardColocatedStorage crée un adaptateur VFSStorage ciblant un shard spécifique d'une base c2db.
func NewShardColocatedStorage(db *DB, shardID uint16) (*ShardColocatedStorage, error) {
	if db == nil {
		return nil, os.ErrInvalid
	}
	s, err := db.GetShard(shardID)
	if err != nil {
		return nil, err
	}
	return &ShardColocatedStorage{shard: s}, nil
}

// RouteFileShard calcule un shardID déterministe pour un tenant et un nom de fichier,
// garantissant que le fichier principal et ses fichiers auxiliaires (.db-wal, .db-journal, etc.)
// puissent être colocalisés sur le même shard local.
func RouteFileShard(tenant, baseFileID string) uint16 {
	return Route([]byte(fileIdentityKey(tenant, baseFileID)))
}

// Get lit la clé depuis le Shard colocalisé.
func (s *ShardColocatedStorage) Get(key []byte) ([]byte, error) {
	return s.shard.Get(key)
}

// Put écrit la clé/valeur dans le Shard colocalisé.
func (s *ShardColocatedStorage) Put(key, val []byte, opts ...WriteOption) error {
	return s.shard.Put(key, val, opts...)
}

// Delete supprime la clé du Shard colocalisé.
func (s *ShardColocatedStorage) Delete(key []byte) error {
	return s.shard.Delete(key)
}

// Checkpoint déclenche la persistance et la synchronisation du Shard colocalisé.
func (s *ShardColocatedStorage) Checkpoint() error {
	return s.shard.Checkpoint()
}

// Sync satisfait l'interface Syncer via le Checkpoint du Shard.
func (s *ShardColocatedStorage) Sync() error {
	return s.shard.Checkpoint()
}

// Flush satisfait l'interface Flusher via le Checkpoint du Shard.
func (s *ShardColocatedStorage) Flush() error {
	return s.shard.Checkpoint()
}

// Shard retourne le pointeur sous-jacent *Shard.
func (s *ShardColocatedStorage) Shard() *Shard {
	return s.shard
}
