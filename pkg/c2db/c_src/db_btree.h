// SPDX-License-Identifier: Apache-2.0 OR MIT

#ifndef DB_BTREE_H
#define DB_BTREE_H

#include <stdint.h>

#define PAGE_N     16384
#define TYPE       20
#define NSLOTS     22
#define FREE_LO    24
#define FREE_HI    26
#define BODY       64
#define TYPE_LEAF  1
#define BT_SLOT    2

typedef struct {
    uint64_t nkeys;
    uint8_t  ok;
    uint8_t  copied;
} db_bt_state_t;

typedef struct {
    uint64_t len;
    uint64_t walked;
    uint8_t  found;
    uint8_t  fallback;
} db_bt_get_t;

db_bt_state_t db_bt_leaf_init(uint8_t *page, uint64_t n);
db_bt_state_t db_bt_insert(const uint8_t *pub, uint8_t *dirty, uint64_t n, db_bt_state_t st, const uint8_t *key, uint64_t klen, const uint8_t *val, uint64_t vlen);
db_bt_get_t db_bt_get(const uint8_t *page, uint64_t n, const uint8_t *key, uint64_t klen, uint8_t *out, uint64_t outn);
uint64_t db_bt_child_of(const uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t root, const uint8_t *key, uint64_t klen);
uint8_t db_bt_leaf_has_prefix(const uint8_t *page, uint64_t n, const uint8_t *pref, uint64_t plen);
uint64_t db_bt_merge_u64(const uint64_t *a, uint64_t na, const uint64_t *b, uint64_t nb, uint64_t *out, uint64_t cap);

#endif
