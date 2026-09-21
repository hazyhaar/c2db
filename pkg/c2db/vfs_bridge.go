// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// bridgeFile maintient l'association entre le handle SQLite pFile, l'instance VFSFile,
// et la connexion pour les verrous FileLockState.
type bridgeFile struct {
	id            uint64
	file          *VFSFile
	connID        uint64
	vfsCtx        *c2dbVFSContext
	lock          *FileLockState
	path          string
	tenant        string
	flags         int32
	deleteOnClose bool
}

// c2dbVFSContext conserve l'état et les ressources associées à un VFS c2db enregistré.
type c2dbVFSContext struct {
	id      uintptr
	name    string
	storage VFSStorage
	locks   *FileLockRegistry
	tls     *libc.TLS
	cname   uintptr
	cvfs    uintptr
	tempSeq uint64
}

var (
	vfsContextMu sync.RWMutex
	vfsContexts  = make(map[uintptr]*c2dbVFSContext)
	nextVFSID    uintptr

	bridgeHandlesMu sync.RWMutex
	bridgeHandles   = make(map[uint64]*bridgeFile)
	nextHandleID    uint64
	nextConnID      uint64

	c2dbIOMethodsOnce sync.Once
	c2dbIOMethods     sqlite3.Tsqlite3_io_methods
	c2dbIOMethodsPtr  uintptr
)

func funcToUintptr[T any](f T) uintptr {
	type wrapper struct {
		fn T
	}
	w := wrapper{fn: f}
	return *(*uintptr)(unsafe.Pointer(&w))
}

func initC2DBIOMethods() {
	c2dbIOMethodsOnce.Do(func() {
		c2dbIOMethods = sqlite3.Tsqlite3_io_methods{
			FiVersion:               1,
			FxClose:                 funcToUintptr(bridgeClose),
			FxRead:                  funcToUintptr(bridgeRead),
			FxWrite:                 funcToUintptr(bridgeWrite),
			FxTruncate:              funcToUintptr(bridgeTruncate),
			FxSync:                  funcToUintptr(bridgeSync),
			FxFileSize:              funcToUintptr(bridgeFileSize),
			FxLock:                  funcToUintptr(bridgeLock),
			FxUnlock:                funcToUintptr(bridgeUnlock),
			FxCheckReservedLock:     funcToUintptr(bridgeCheckReservedLock),
			FxFileControl:           funcToUintptr(bridgeFileControl),
			FxSectorSize:            funcToUintptr(bridgeSectorSize),
			FxDeviceCharacteristics: funcToUintptr(bridgeDeviceCharacteristics),
		}
		c2dbIOMethodsPtr = uintptr(unsafe.Pointer(&c2dbIOMethods))
	})
}

func getVFSContext(pVfs uintptr) *c2dbVFSContext {
	if pVfs == 0 {
		return nil
	}
	appData := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(pVfs)).FpAppData
	vfsContextMu.RLock()
	ctx := vfsContexts[appData]
	vfsContextMu.RUnlock()
	return ctx
}

func getBridgeFile(pFile uintptr) *bridgeFile {
	if pFile == 0 {
		return nil
	}
	id := *(*uint64)(unsafe.Pointer(pFile + 8))
	bridgeHandlesMu.RLock()
	bf := bridgeHandles[id]
	bridgeHandlesMu.RUnlock()
	return bf
}

func unregisterBridgeFile(pFile uintptr) *bridgeFile {
	if pFile == 0 {
		return nil
	}
	id := *(*uint64)(unsafe.Pointer(pFile + 8))
	bridgeHandlesMu.Lock()
	bf := bridgeHandles[id]
	delete(bridgeHandles, id)
	bridgeHandlesMu.Unlock()
	return bf
}

func normalizePath(raw string) string {
	if idx := strings.IndexByte(raw, '?'); idx >= 0 {
		raw = raw[:idx]
	}
	p := filepath.Clean(raw)
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimPrefix(p, "\\")
	if p == "" || p == "." {
		p = "main.db"
	}
	return p
}

// bridgeOpen implémente xOpen selon les contrats SQLite.
func bridgeOpen(tls *libc.TLS, pVfs uintptr, zName uintptr, pFile uintptr, flags int32, pOutFlags uintptr) int32 {
	if pFile == 0 {
		return sqlite3.SQLITE_IOERR
	}
	(*sqlite3.Tsqlite3_file)(unsafe.Pointer(pFile)).FpMethods = 0

	vfsCtx := getVFSContext(pVfs)
	if vfsCtx == nil {
		return sqlite3.SQLITE_IOERR
	}

	var fileName string
	var deleteOnClose bool

	if zName == 0 {
		tempSeq := atomic.AddUint64(&vfsCtx.tempSeq, 1)
		fileName = fmt.Sprintf("__sqlite_temp_%d_%d.tmp", vfsCtx.id, tempSeq)
		deleteOnClose = true
	} else {
		fileName = normalizePath(libc.GoString(zName))
		deleteOnClose = (flags & sqlite3.SQLITE_OPEN_DELETEONCLOSE) != 0
	}

	tenant := vfsCtx.name

	// Si SQLITE_OPEN_CREATE n'est pas spécifié, le fichier doit déjà exister.
	if (flags & sqlite3.SQLITE_OPEN_CREATE) == 0 {
		k := MetaKey(tenant, fileName)
		raw, err := vfsCtx.storage.Get(k)
		if err != nil {
			// La contention de verrou doit rester reconnaissable comme BUSY
			// par la couche SQLite ; seule l'absence ou l'illisibilité rend
			// CANTOPEN. Un aplatissement en CANTOPEN ferait abandonner le
			// client au lieu de réessayer le verrou.
			if isVFSBusy(err) {
				return sqlite3.SQLITE_BUSY
			}
			return sqlite3.SQLITE_CANTOPEN
		}
		meta, err := decodeVFSMeta(raw)
		if err != nil || (meta.flags&VFSFlagDeleted != 0) {
			return sqlite3.SQLITE_CANTOPEN
		}
	}

	// Si SQLITE_OPEN_EXCLUSIVE avec SQLITE_OPEN_CREATE : doit échouer si le fichier existe déjà.
	if (flags & (sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE)) == (sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE) {
		k := MetaKey(tenant, fileName)
		raw, err := vfsCtx.storage.Get(k)
		if err == nil {
			meta, errDec := decodeVFSMeta(raw)
			if errDec == nil && (meta.flags&VFSFlagDeleted == 0) {
				return sqlite3.SQLITE_CANTOPEN
			}
		}
	}

	vfsFile, err := OpenVFSFile(vfsCtx.storage, tenant, fileName)
	if err != nil {
		return sqlite3.SQLITE_CANTOPEN
	}

	connID := atomic.AddUint64(&nextConnID, 1)
	lockState := vfsCtx.locks.File(tenant + "/" + fileName)
	handleID := atomic.AddUint64(&nextHandleID, 1)

	bf := &bridgeFile{
		id:            handleID,
		file:          vfsFile,
		connID:        connID,
		vfsCtx:        vfsCtx,
		lock:          lockState,
		path:          fileName,
		tenant:        tenant,
		flags:         flags,
		deleteOnClose: deleteOnClose,
	}

	bridgeHandlesMu.Lock()
	bridgeHandles[handleID] = bf
	bridgeHandlesMu.Unlock()

	(*sqlite3.Tsqlite3_file)(unsafe.Pointer(pFile)).FpMethods = c2dbIOMethodsPtr
	*(*uint64)(unsafe.Pointer(pFile + 8)) = handleID

	if pOutFlags != 0 {
		*(*int32)(unsafe.Pointer(pOutFlags)) = flags
	}

	return sqlite3.SQLITE_OK
}

// bridgeClose implémente xClose selon les contrats SQLite.
func bridgeClose(tls *libc.TLS, pFile uintptr) int32 {
	bf := unregisterBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_OK
	}
	_ = bf.lock.Unlock(bf.connID, LockNone)
	_ = bf.file.Close()
	if bf.deleteOnClose {
		_ = DeleteVFSFile(bf.vfsCtx.storage, bf.tenant, bf.path)
	}
	(*sqlite3.Tsqlite3_file)(unsafe.Pointer(pFile)).FpMethods = 0
	return sqlite3.SQLITE_OK
}

func isVFSBusy(err error) bool {
	return errors.Is(err, ErrBusy) || errors.Is(err, ErrWriterBusy) || errors.Is(err, ErrViewHeld) || errors.Is(err, ErrHeapFull) || errors.Is(err, ErrTreeFull) || errors.Is(err, errInsert)
}

func vfsBusyOr(err error, ioerr int32) int32 {
	if isVFSBusy(err) {
		return sqlite3.SQLITE_BUSY
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "c2db vfsBusyOr unmapped err=%v ioerr=%d\n", err, ioerr)
	}
	return ioerr
}

// bridgeRead implémente xRead selon les contrats SQLite.
func bridgeRead(tls *libc.TLS, pFile uintptr, zBuf uintptr, iAmt int32, iOfst int64) int32 {
	bf := getBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_IOERR_READ
	}
	if iAmt <= 0 {
		return sqlite3.SQLITE_OK
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(zBuf)), int(iAmt))
	n, err := bf.file.ReadAt(buf, iOfst)
	if err == nil {
		if n == int(iAmt) {
			return sqlite3.SQLITE_OK
		}
		clear(buf[n:])
		return sqlite3.SQLITE_IOERR_SHORT_READ
	}
	if errors.Is(err, ErrShortRead) {
		if n < int(iAmt) {
			clear(buf[n:])
		}
		return sqlite3.SQLITE_IOERR_SHORT_READ
	}
	if isVFSBusy(err) {
		return sqlite3.SQLITE_BUSY
	}
	clear(buf)
	return sqlite3.SQLITE_IOERR_READ
}

// bridgeWrite implémente xWrite selon les contrats SQLite.
func bridgeWrite(tls *libc.TLS, pFile uintptr, zBuf uintptr, iAmt int32, iOfst int64) int32 {
	bf := getBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_IOERR_WRITE
	}
	if iAmt <= 0 {
		return sqlite3.SQLITE_OK
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(zBuf)), int(iAmt))
	n, err := bf.file.WriteAt(buf, iOfst)
	if err != nil || n < int(iAmt) {
		return vfsBusyOr(err, sqlite3.SQLITE_IOERR_WRITE)
	}
	return sqlite3.SQLITE_OK
}

// bridgeTruncate implémente xTruncate selon les contrats SQLite.
func bridgeTruncate(tls *libc.TLS, pFile uintptr, size int64) int32 {
	bf := getBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_IOERR_TRUNCATE
	}
	if err := bf.file.Truncate(size); err != nil {
		return vfsBusyOr(err, sqlite3.SQLITE_IOERR_TRUNCATE)
	}
	return sqlite3.SQLITE_OK
}

// bridgeSync implémente xSync selon les contrats SQLite.
func bridgeSync(tls *libc.TLS, pFile uintptr, flags int32) int32 {
	bf := getBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_IOERR_FSYNC
	}
	if err := bf.file.Sync(); err != nil {
		return vfsBusyOr(err, sqlite3.SQLITE_IOERR_FSYNC)
	}
	return sqlite3.SQLITE_OK
}

// bridgeFileSize implémente xFileSize selon les contrats SQLite.
func bridgeFileSize(tls *libc.TLS, pFile uintptr, pSize uintptr) int32 {
	bf := getBridgeFile(pFile)
	if bf == nil || pSize == 0 {
		return sqlite3.SQLITE_IOERR_FSTAT
	}
	sz, err := bf.file.Size()
	if err != nil {
		return sqlite3.SQLITE_IOERR_FSTAT
	}
	*(*int64)(unsafe.Pointer(pSize)) = sz
	return sqlite3.SQLITE_OK
}

// bridgeLock implémente xLock avec conversion vers vfs_locks.go.
func bridgeLock(tls *libc.TLS, pFile uintptr, lock int32) int32 {
	bf := getBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_IOERR_LOCK
	}
	if lock < sqlite3.SQLITE_LOCK_SHARED || lock > sqlite3.SQLITE_LOCK_EXCLUSIVE {
		return sqlite3.SQLITE_OK
	}
	target := LockState(lock)
	err := bf.lock.Lock(bf.connID, target)
	if errors.Is(err, ErrBusy) {
		return sqlite3.SQLITE_BUSY
	}
	if err != nil {
		return sqlite3.SQLITE_IOERR_LOCK
	}
	return sqlite3.SQLITE_OK
}

// bridgeUnlock implémente xUnlock avec conversion vers vfs_locks.go.
func bridgeUnlock(tls *libc.TLS, pFile uintptr, lock int32) int32 {
	bf := getBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_IOERR_UNLOCK
	}
	if lock < sqlite3.SQLITE_LOCK_NONE || lock > sqlite3.SQLITE_LOCK_EXCLUSIVE {
		return sqlite3.SQLITE_OK
	}
	target := LockState(lock)
	err := bf.lock.Unlock(bf.connID, target)
	if err != nil {
		return sqlite3.SQLITE_IOERR_UNLOCK
	}
	return sqlite3.SQLITE_OK
}

// bridgeCheckReservedLock implémente xCheckReservedLock via FileLockState.
func bridgeCheckReservedLock(tls *libc.TLS, pFile uintptr, pResOut uintptr) int32 {
	if pResOut == 0 {
		return sqlite3.SQLITE_IOERR
	}
	bf := getBridgeFile(pFile)
	if bf == nil {
		return sqlite3.SQLITE_IOERR
	}
	if bf.lock.CheckReservedLock() {
		*(*int32)(unsafe.Pointer(pResOut)) = 1
	} else {
		*(*int32)(unsafe.Pointer(pResOut)) = 0
	}
	return sqlite3.SQLITE_OK
}

// bridgeFileControl implémente xFileControl (retourne SQLITE_NOTFOUND par défaut).
func bridgeFileControl(tls *libc.TLS, pFile uintptr, op int32, pArg uintptr) int32 {
	return sqlite3.SQLITE_NOTFOUND
}

// bridgeSectorSize retourne la granularité de secteur 4 Ko c2db.
func bridgeSectorSize(tls *libc.TLS, pFile uintptr) int32 {
	return BlockSize
}

// bridgeDeviceCharacteristics retourne les propriétés atomiques et d'append de c2db.
func bridgeDeviceCharacteristics(tls *libc.TLS, pFile uintptr) int32 {
	return 0
}

// bridgeDelete implémente xDelete.
func bridgeDelete(tls *libc.TLS, pVfs uintptr, zName uintptr, syncDir int32) int32 {
	vfsCtx := getVFSContext(pVfs)
	if vfsCtx == nil || zName == 0 {
		return sqlite3.SQLITE_IOERR_DELETE
	}
	fileName := normalizePath(libc.GoString(zName))
	if err := DeleteVFSFile(vfsCtx.storage, vfsCtx.name, fileName); err != nil && !errors.Is(err, ErrNotFound) {
		return sqlite3.SQLITE_IOERR_DELETE
	}
	if syncDir != 0 {
		if syncer, ok := vfsCtx.storage.(Syncer); ok {
			if err := syncer.Sync(); err != nil {
				return sqlite3.SQLITE_IOERR_DELETE
			}
		} else if flusher, ok := vfsCtx.storage.(Flusher); ok {
			if err := flusher.Flush(); err != nil {
				return sqlite3.SQLITE_IOERR_DELETE
			}
		} else if checkpointer, ok := vfsCtx.storage.(Checkpointer); ok {
			if err := checkpointer.Checkpoint(); err != nil {
				return sqlite3.SQLITE_IOERR_DELETE
			}
		}
	}
	return sqlite3.SQLITE_OK
}

// bridgeAccess implémente xAccess.
func bridgeAccess(tls *libc.TLS, pVfs uintptr, zName uintptr, flags int32, pResOut uintptr) int32 {
	if pResOut == 0 {
		return sqlite3.SQLITE_IOERR
	}
	vfsCtx := getVFSContext(pVfs)
	if vfsCtx == nil {
		*(*int32)(unsafe.Pointer(pResOut)) = 0
		return sqlite3.SQLITE_IOERR_ACCESS
	}
	if zName == 0 {
		*(*int32)(unsafe.Pointer(pResOut)) = 0
		return sqlite3.SQLITE_OK
	}
	fileName := normalizePath(libc.GoString(zName))
	k := MetaKey(vfsCtx.name, fileName)
	raw, err := vfsCtx.storage.Get(k)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrNotFound) {
			*(*int32)(unsafe.Pointer(pResOut)) = 0
			return sqlite3.SQLITE_OK
		}
		*(*int32)(unsafe.Pointer(pResOut)) = 0
		return sqlite3.SQLITE_IOERR_ACCESS
	}
	meta, err := decodeVFSMeta(raw)
	if err != nil || (meta.flags&VFSFlagDeleted != 0) {
		*(*int32)(unsafe.Pointer(pResOut)) = 0
		return sqlite3.SQLITE_OK
	}
	*(*int32)(unsafe.Pointer(pResOut)) = 1
	return sqlite3.SQLITE_OK
}

// bridgeFullPathname implémente xFullPathname avec normalisation filepath.Clean.
func bridgeFullPathname(tls *libc.TLS, pVfs uintptr, zName uintptr, nOut int32, zOut uintptr) int32 {
	if zName == 0 || zOut == 0 || nOut <= 0 {
		return sqlite3.SQLITE_CANTOPEN
	}
	rawName := libc.GoString(zName)
	cleanPath := filepath.Clean(rawName)
	if int32(len(cleanPath)+1) > nOut {
		return sqlite3.SQLITE_CANTOPEN
	}
	outSlice := unsafe.Slice((*byte)(unsafe.Pointer(zOut)), int(nOut))
	copy(outSlice, cleanPath)
	outSlice[len(cleanPath)] = 0
	return sqlite3.SQLITE_OK
}

// bridgeRandomness remplit zOut avec du pseudo-aléa cryptographique.
func bridgeRandomness(tls *libc.TLS, pVfs uintptr, nByte int32, zOut uintptr) int32 {
	if nByte <= 0 || zOut == 0 {
		return 0
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(zOut)), int(nByte))
	n, _ := rand.Read(buf)
	return int32(n)
}

// bridgeSleep met en pause l'exécution pour la durée demandée.
func bridgeSleep(tls *libc.TLS, pVfs uintptr, microseconds int32) int32 {
	time.Sleep(time.Duration(microseconds) * time.Microsecond)
	return sqlite3.SQLITE_OK
}

// bridgeCurrentTime calcule le Julian Day Number flottant.
func bridgeCurrentTime(tls *libc.TLS, pVfs uintptr, pTime uintptr) int32 {
	if pTime == 0 {
		return sqlite3.SQLITE_IOERR
	}
	now := time.Now()
	julian := float64(now.UnixNano())/86400e9 + 2440587.5
	*(*float64)(unsafe.Pointer(pTime)) = julian
	return sqlite3.SQLITE_OK
}

// bridgeCurrentTimeInt64 calcule le Julian Day Number en millisecondes.
func bridgeCurrentTimeInt64(tls *libc.TLS, pVfs uintptr, pTime uintptr) int32 {
	if pTime == 0 {
		return sqlite3.SQLITE_IOERR
	}
	now := time.Now()
	unixMs := now.UnixNano() / 1e6
	julianMs := unixMs + 210866760000000
	*(*int64)(unsafe.Pointer(pTime)) = julianMs
	return sqlite3.SQLITE_OK
}

// RegisterC2DBVFS enregistre un VFS personnalisé c2db sous le nom donné (ex: "c2db_fixture")
// adossé au VFSStorage spécifié et au registre de verrous.
// Retourne une fonction de nettoyage (unregister).
func RegisterC2DBVFS(vfsName string, storage VFSStorage) (unregister func(), err error) {
	if vfsName == "" {
		return nil, errors.New("c2db: vfs name cannot be empty")
	}
	if storage == nil {
		return nil, errors.New("c2db: vfs storage cannot be nil")
	}
	if db, ok := storage.(*DB); ok && db.busyTimeout <= 0 {
		db.SetBusyTimeout(5 * time.Second)
	}

	initC2DBIOMethods()

	tls := libc.NewTLS()
	cname, err := libc.CString(vfsName)
	if err != nil {
		tls.Close()
		return nil, fmt.Errorf("c2db: failed to allocate cstring for vfs name: %w", err)
	}

	vfsSize := libc.Tsize_t(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{}))
	vfsPtr := libc.Xmalloc(tls, vfsSize)
	if vfsPtr == 0 {
		libc.Xfree(tls, cname)
		tls.Close()
		return nil, errors.New("c2db: failed to allocate sqlite3_vfs")
	}

	appDataID := atomic.AddUintptr(&nextVFSID, 1)
	ctx := &c2dbVFSContext{
		id:      appDataID,
		name:    vfsName,
		storage: storage,
		locks:   NewFileLockRegistry(),
		tls:     tls,
		cname:   cname,
		cvfs:    vfsPtr,
	}

	vfsContextMu.Lock()
	vfsContexts[appDataID] = ctx
	vfsContextMu.Unlock()

	*(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(vfsPtr)) = sqlite3.Tsqlite3_vfs{
		FiVersion:          2,
		FszOsFile:          64,
		FmxPathname:        1024,
		FzName:             cname,
		FpAppData:          appDataID,
		FxOpen:             funcToUintptr(bridgeOpen),
		FxDelete:           funcToUintptr(bridgeDelete),
		FxAccess:           funcToUintptr(bridgeAccess),
		FxFullPathname:     funcToUintptr(bridgeFullPathname),
		FxDlOpen:           0,
		FxDlError:          0,
		FxDlSym:            0,
		FxDlClose:          0,
		FxRandomness:       funcToUintptr(bridgeRandomness),
		FxSleep:            funcToUintptr(bridgeSleep),
		FxCurrentTime:      funcToUintptr(bridgeCurrentTime),
		FxGetLastError:     0,
		FxCurrentTimeInt64: funcToUintptr(bridgeCurrentTimeInt64),
	}

	if rc := sqlite3.Xsqlite3_vfs_register(tls, vfsPtr, 0); rc != sqlite3.SQLITE_OK {
		vfsContextMu.Lock()
		delete(vfsContexts, appDataID)
		vfsContextMu.Unlock()

		libc.Xfree(tls, cname)
		libc.Xfree(tls, vfsPtr)
		tls.Close()
		return nil, fmt.Errorf("c2db: sqlite3_vfs_register returned %d", rc)
	}

	var unregisterOnce sync.Once
	unregister = func() {
		unregisterOnce.Do(func() {
			uTLS := libc.NewTLS()
			defer uTLS.Close()

			sqlite3.Xsqlite3_vfs_unregister(uTLS, vfsPtr)

			vfsContextMu.Lock()
			delete(vfsContexts, appDataID)
			vfsContextMu.Unlock()

			bridgeHandlesMu.Lock()
			for hid, bf := range bridgeHandles {
				if bf.vfsCtx == ctx {
					delete(bridgeHandles, hid)
					_ = bf.file.Close()
				}
			}
			bridgeHandlesMu.Unlock()

			libc.Xfree(uTLS, cname)
			libc.Xfree(uTLS, vfsPtr)
			tls.Close()
		})
	}

	return unregister, nil
}
