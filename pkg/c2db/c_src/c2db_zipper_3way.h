#ifndef C2DB_ZIPPER_3WAY_H
#define C2DB_ZIPPER_3WAY_H

#include <stdint.h>

typedef struct {
    uint64_t key;
    uint64_t ver;
    uint32_t val_crc;
    uint8_t  deleted;
    uint8_t  _pad[3];
} c2db_zip_entry_t;

typedef struct {
    uint64_t merged_count;
    uint64_t conflicts_count;
    uint8_t  ok;
} c2db_zip_result_t;

c2db_zip_result_t c2db_zipper_3way(
    const c2db_zip_entry_t *base,   uint64_t n_base,
    const c2db_zip_entry_t *ours,   uint64_t n_ours,
    const c2db_zip_entry_t *theirs, uint64_t n_theirs,
    c2db_zip_entry_t *out,          uint64_t cap
);

#endif
