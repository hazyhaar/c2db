// SPDX-License-Identifier: Apache-2.0 OR MIT

#include "c2db_vfs.h"

int c2db_close(sqlite3_file *pFile) {
    if (pFile == 0) {
        return SQLITE_ERROR;
    }
    return SQLITE_OK;
}

int c2db_read(sqlite3_file *pFile, void *zBuf, int iAmt, sqlite3_int64 iOfst) {
    if (pFile == 0 || zBuf == 0) {
        return SQLITE_IOERR;
    }
    return SQLITE_OK;
}

int c2db_write(sqlite3_file *pFile, const void *zBuf, int iAmt, sqlite3_int64 iOfst) {
    if (pFile == 0 || zBuf == 0) {
        return SQLITE_IOERR;
    }
    return SQLITE_OK;
}

int c2db_truncate(sqlite3_file *pFile, sqlite3_int64 size) {
    if (pFile == 0) {
        return SQLITE_IOERR;
    }
    return SQLITE_OK;
}

int c2db_sync(sqlite3_file *pFile, int flags) {
    if (pFile == 0) {
        return SQLITE_IOERR;
    }
    return SQLITE_OK;
}

int c2db_file_size(sqlite3_file *pFile, sqlite3_int64 *pSize) {
    if (pFile == 0 || pSize == 0) {
        return SQLITE_IOERR;
    }
    *pSize = 0;
    return SQLITE_OK;
}

int c2db_lock(sqlite3_file *pFile, int lock) {
    if (pFile == 0) {
        return SQLITE_IOERR;
    }
    return SQLITE_OK;
}

int c2db_unlock(sqlite3_file *pFile, int lock) {
    if (pFile == 0) {
        return SQLITE_IOERR;
    }
    return SQLITE_OK;
}

int c2db_check_reserved_lock(sqlite3_file *pFile, int *pResOut) {
    if (pResOut == 0) {
        return SQLITE_IOERR;
    }
    *pResOut = 0;
    return SQLITE_OK;
}

int c2db_file_control(sqlite3_file *pFile, int op, void *pArg) {
    return SQLITE_OK;
}

int c2db_sector_size(sqlite3_file *pFile) {
    return 4096;
}

int c2db_device_characteristics(sqlite3_file *pFile) {
    return 0;
}

int c2db_open(sqlite3_vfs *pVfs, const char *zName, sqlite3_file *pFile, int flags, int *pOutFlags) {
    if (pFile == 0) {
        return SQLITE_IOERR;
    }
    if (pOutFlags != 0) {
        *pOutFlags = flags;
    }
    return SQLITE_OK;
}

int c2db_delete(sqlite3_vfs *pVfs, const char *zName, int syncDir) {
    return SQLITE_OK;
}

int c2db_access(sqlite3_vfs *pVfs, const char *zName, int flags, int *pResOut) {
    if (pResOut != 0) {
        *pResOut = 0;
    }
    return SQLITE_OK;
}

int c2db_full_pathname(sqlite3_vfs *pVfs, const char *zName, int nOut, char *zOut) {
    return SQLITE_OK;
}

int c2db_randomness(sqlite3_vfs *pVfs, int nByte, char *zOut) {
    return SQLITE_OK;
}

int c2db_sleep(sqlite3_vfs *pVfs, int microseconds) {
    return SQLITE_OK;
}

int c2db_current_time(sqlite3_vfs *pVfs, double *pTime) {
    if (pTime != 0) {
        *pTime = 0.0;
    }
    return SQLITE_OK;
}

int c2db_get_last_error(sqlite3_vfs *pVfs, int nByte, char *zErrMsg) {
    return SQLITE_OK;
}

static const struct sqlite3_io_methods c2db_io_methods = {
    1,
    c2db_close,
    c2db_read,
    c2db_write,
    c2db_truncate,
    c2db_sync,
    c2db_file_size,
    c2db_lock,
    c2db_unlock,
    c2db_check_reserved_lock,
    c2db_file_control,
    c2db_sector_size,
    c2db_device_characteristics
};

static struct sqlite3_vfs c2db_vfs_driver = {
    1,
    32,
    1024,
    0,
    "c2db",
    0,
    c2db_open,
    c2db_delete,
    c2db_access,
    c2db_full_pathname,
    0,
    0,
    0,
    0,
    c2db_randomness,
    c2db_sleep,
    c2db_current_time,
    c2db_get_last_error
};
