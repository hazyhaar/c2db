#ifndef C2DB_BTREE_PREFIX_SEARCH_H
#define C2DB_BTREE_PREFIX_SEARCH_H

#include <stdint.h>

#define C2DB_PAGE_N        16384
#define C2DB_TYPE_OFFSET   20
#define C2DB_NSLOTS_OFFSET 22
#define C2DB_BODY_OFFSET   64
#define C2DB_BT_SLOT_SIZE  2
#define C2DB_TYPE_LEAF     1

typedef struct {
    uint64_t slot_idx;
    uint64_t count;
    uint8_t  found;
} c2db_prefix_result_t;

c2db_prefix_result_t c2db_btree_prefix_search(
    const uint8_t *page,
    uint64_t n,
    const uint8_t *pref,
    uint64_t plen
);

#endif
