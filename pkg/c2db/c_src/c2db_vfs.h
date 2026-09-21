// SPDX-License-Identifier: Apache-2.0 OR MIT

#ifndef C2DB_VFS_H
#define C2DB_VFS_H

/* Constantes officielles SQLite v1 */
#define SQLITE_OK               0
#define SQLITE_ERROR            1
#define SQLITE_BUSY             5
#define SQLITE_IOERR           10
#define SQLITE_IOERR_SHORT_READ 522

/* Flags d'ouverture (open flags) */
#define SQLITE_OPEN_READONLY       0x00000001
#define SQLITE_OPEN_READWRITE      0x00000002
#define SQLITE_OPEN_CREATE         0x00000004
#define SQLITE_OPEN_DELETEONCLOSE  0x00000008
#define SQLITE_OPEN_EXCLUSIVE      0x00000010
#define SQLITE_OPEN_AUTOPROXY      0x00000020
#define SQLITE_OPEN_URI            0x00000040
#define SQLITE_OPEN_MEMORY         0x00000080
#define SQLITE_OPEN_MAIN_DB        0x00000100
#define SQLITE_OPEN_TEMP_DB        0x00000200
#define SQLITE_OPEN_TRANSIENT_DB   0x00000400
#define SQLITE_OPEN_MAIN_JOURNAL   0x00000800
#define SQLITE_OPEN_TEMP_JOURNAL   0x00001000
#define SQLITE_OPEN_SUBJOURNAL     0x00002000
#define SQLITE_OPEN_SUPER_JOURNAL  0x00004000
#define SQLITE_OPEN_NOMUTEX        0x00008000
#define SQLITE_OPEN_FULLMUTEX      0x00010000
#define SQLITE_OPEN_SHAREDCACHE    0x00020000
#define SQLITE_OPEN_PRIVATECACHE   0x00040000
#define SQLITE_OPEN_WAL            0x00080000

/* Flags de synchronisation */
#define SQLITE_SYNC_NORMAL         0x00002
#define SQLITE_SYNC_FULL           0x00003
#define SQLITE_SYNC_DATAONLY       0x00010

/* Flags de verrouillage */
#define SQLITE_LOCK_NONE           0
#define SQLITE_LOCK_SHARED         1
#define SQLITE_LOCK_RESERVED       2
#define SQLITE_LOCK_PENDING        3
#define SQLITE_LOCK_EXCLUSIVE      4

/* Flags d'accès (xAccess) */
#define SQLITE_ACCESS_EXISTS       0
#define SQLITE_ACCESS_READWRITE    1
#define SQLITE_ACCESS_READ         2

/* Caractéristiques matérielles de périphérique d'E/S */
#define SQLITE_IOCAP_ATOMIC                 0x00000001
#define SQLITE_IOCAP_ATOMIC512              0x00000002
#define SQLITE_IOCAP_ATOMIC1K               0x00000004
#define SQLITE_IOCAP_ATOMIC2K               0x00000008
#define SQLITE_IOCAP_ATOMIC4K               0x00000010
#define SQLITE_IOCAP_ATOMIC8K               0x00000020
#define SQLITE_IOCAP_ATOMIC16K              0x00000040
#define SQLITE_IOCAP_ATOMIC32K              0x00000080
#define SQLITE_IOCAP_ATOMIC64K              0x00000100
#define SQLITE_IOCAP_SAFE_APPEND            0x00000200
#define SQLITE_IOCAP_SEQUENTIAL             0x00000400
#define SQLITE_IOCAP_UNDELETABLE_WHEN_OPEN  0x00000800
#define SQLITE_IOCAP_POWERSAFE_OVERWRITE    0x00001000
#define SQLITE_IOCAP_IMMUTABLE              0x00002000
#define SQLITE_IOCAP_BATCH_ATOMIC           0x00004000

/* Définitions des types officiels SQLite v1 */
typedef struct sqlite3_file sqlite3_file;
typedef struct sqlite3_io_methods sqlite3_io_methods;
typedef struct sqlite3_vfs sqlite3_vfs;
typedef long long sqlite3_int64;

struct sqlite3_io_methods {
    int iVersion;
    int (*xClose)(sqlite3_file*);
    int (*xRead)(sqlite3_file*, void*, int, sqlite3_int64);
    int (*xWrite)(sqlite3_file*, const void*, int, sqlite3_int64);
    int (*xTruncate)(sqlite3_file*, sqlite3_int64);
    int (*xSync)(sqlite3_file*, int flags);
    int (*xFileSize)(sqlite3_file*, sqlite3_int64 *pSize);
    int (*xLock)(sqlite3_file*, int);
    int (*xUnlock)(sqlite3_file*, int);
    int (*xCheckReservedLock)(sqlite3_file*, int *pResOut);
    int (*xFileControl)(sqlite3_file*, int op, void *pArg);
    int (*xSectorSize)(sqlite3_file*);
    int (*xDeviceCharacteristics)(sqlite3_file*);
};

struct sqlite3_file {
    const struct sqlite3_io_methods *pMethods;
};

typedef struct c2db_file {
    sqlite3_file base;
    int lockState;
    int _pad;
    sqlite3_int64 fileSize;
    void *pStorage;
} c2db_file;

typedef void (*sqlite3_syscall_ptr)(void);

struct sqlite3_vfs {
    int iVersion;
    int szOsFile;
    int mxPathname;
    sqlite3_vfs *pNext;
    const char *zName;
    void *pAppData;
    int (*xOpen)(sqlite3_vfs*, const char *zName, sqlite3_file*, int flags, int *pOutFlags);
    int (*xDelete)(sqlite3_vfs*, const char *zName, int syncDir);
    int (*xAccess)(sqlite3_vfs*, const char *zName, int flags, int *pResOut);
    int (*xFullPathname)(sqlite3_vfs*, const char *zName, int nOut, char *zOut);
    void *(*xDlOpen)(sqlite3_vfs*, const char *zFilename);
    void (*xDlError)(sqlite3_vfs*, int nByte, char *zErrMsg);
#if 0
    void (*(*xDlSym)(sqlite3_vfs*, void*, const char *zSymbol))(void);
#else
    void (*xDlSym)(sqlite3_vfs*, void*, const char *zSymbol);
#endif
    void (*xDlClose)(sqlite3_vfs*, void*);
    int (*xRandomness)(sqlite3_vfs*, int nByte, char *zOut);
    int (*xSleep)(sqlite3_vfs*, int microseconds);
    int (*xCurrentTime)(sqlite3_vfs*, double*);
    int (*xGetLastError)(sqlite3_vfs*, int, char *);
};

/* Prototypes des callbacks d'E/S */
int c2db_close(sqlite3_file *pFile);
int c2db_read(sqlite3_file *pFile, void *zBuf, int iAmt, sqlite3_int64 iOfst);
int c2db_write(sqlite3_file *pFile, const void *zBuf, int iAmt, sqlite3_int64 iOfst);
int c2db_truncate(sqlite3_file *pFile, sqlite3_int64 size);
int c2db_sync(sqlite3_file *pFile, int flags);
int c2db_file_size(sqlite3_file *pFile, sqlite3_int64 *pSize);
int c2db_lock(sqlite3_file *pFile, int lock);
int c2db_unlock(sqlite3_file *pFile, int lock);
int c2db_check_reserved_lock(sqlite3_file *pFile, int *pResOut);
int c2db_file_control(sqlite3_file *pFile, int op, void *pArg);
int c2db_sector_size(sqlite3_file *pFile);
int c2db_device_characteristics(sqlite3_file *pFile);

/* Prototypes des callbacks VFS */
int c2db_open(sqlite3_vfs *pVfs, const char *zName, sqlite3_file *pFile, int flags, int *pOutFlags);
int c2db_delete(sqlite3_vfs *pVfs, const char *zName, int syncDir);
int c2db_access(sqlite3_vfs *pVfs, const char *zName, int flags, int *pResOut);
int c2db_full_pathname(sqlite3_vfs *pVfs, const char *zName, int nOut, char *zOut);
int c2db_randomness(sqlite3_vfs *pVfs, int nByte, char *zOut);
int c2db_sleep(sqlite3_vfs *pVfs, int microseconds);
int c2db_current_time(sqlite3_vfs *pVfs, double *pTime);
int c2db_get_last_error(sqlite3_vfs *pVfs, int nByte, char *zErrMsg);

#endif /* C2DB_VFS_H */
