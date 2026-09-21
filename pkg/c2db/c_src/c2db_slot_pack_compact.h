#ifndef C2DB_SLOT_PACK_COMPACT_H
#define C2DB_SLOT_PACK_COMPACT_H

#include <stdint.h>

#define C2DB_PAGE_N        16384
#define C2DB_TYPE_OFFSET   20
#define C2DB_NSLOTS_OFFSET 22
#define C2DB_FREE_LO_OFFSET 24
#define C2DB_FREE_HI_OFFSET 26
#define C2DB_CRC_OFFSET    28
#define C2DB_BODY_OFFSET   64
#define C2DB_BT_SLOT_SIZE  2
#define C2DB_TYPE_LEAF     1

typedef struct {
    uint64_t slots_before;
    uint64_t slots_after;
    uint64_t bytes_freed;
    uint8_t  ok;
} c2db_compact_result_t;

c2db_compact_result_t c2db_slot_pack_compact(
    uint8_t *page,
    uint64_t n,
    uint8_t *scratch,
    uint64_t scratch_n,
    uint64_t idlen,
    uint8_t drop_tombstones
);

#endif
