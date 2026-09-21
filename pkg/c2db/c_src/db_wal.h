// SPDX-License-Identifier: Apache-2.0 OR MIT

#ifndef DB_WAL_H
#define DB_WAL_H

#include <stdint.h>

#define WAL_N           4096
#define WAL_MAGIC       0x43324442U
#define WAL_MAGIC_OFF   0
#define WAL_LEN_OFF     4
#define WAL_ID_OFF      8
#define WAL_TYPE_OFF    24
#define WAL_PAYLOAD_OFF 25
#define WAL_TAG_OFF     4080
#define WAL_TAG_SIZE    16
#define WAL_ID_SIZE     16

#define DB_WAL_PUT               0
#define DB_WAL_DEL               1
#define DB_WAL_COMMIT_BATCH      2
#define DB_WAL_CHECKPOINT_BEGIN  3
#define DB_WAL_CHECKPOINT_END    4
#define DB_WAL_TOPO_TRANSITION   5
#define DB_WAL_MIGRATE_PACK_REF  6
#define DB_WAL_RESERVED          7
#define DB_WAL_TX_COMMIT         9

typedef struct {
    uint32_t magic;
    uint32_t len;
    uint8_t  typ;
    uint32_t plen;
    uint8_t  ok;
} db_wal_desc_t;

uint8_t db_wal_bounds(const uint8_t *restrict block, uint64_t n, uint64_t addr, uint64_t len);
uint8_t db_wal_type_ok(uint8_t t);
uint64_t db_wal_payload_off(void);
uint64_t db_wal_tag_off(void);
uint8_t db_wal_pack(uint8_t *restrict block, uint64_t n, const uint8_t *restrict id, uint8_t typ, const uint8_t *restrict payload, uint64_t plen);
db_wal_desc_t db_wal_unpack(const uint8_t *restrict block, uint64_t n, uint8_t *restrict id);
uint8_t db_wal_tag_slot(uint8_t *restrict block, uint64_t n, const uint8_t *restrict src, uint8_t *restrict dst);

#endif
