// SPDX-License-Identifier: Apache-2.0 OR MIT

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

uint8_t db_wal_bounds(const uint8_t *restrict block, uint64_t n, uint64_t addr, uint64_t len) {
    if (block == 0) return 0;
    if (n != WAL_N) return 0;
    if (addr < n && (uint64_t)len <= n - addr) return 1;
    return 0;
}

uint8_t db_wal_type_ok(uint8_t t) {
    switch (t) {
    case 0:
        return 1;
    case 1:
        return 1;
    case 2:
        return 1;
    case 3:
        return 1;
    case 4:
        return 1;
    case 5:
        return 1;
    case 6:
        return 1;
    case 7:
        return 1;
    case 8:
        return 1;
    case 9:
        return 1;
    default:
        return 0;
    }
}

uint64_t db_wal_payload_off(void) {
    return WAL_PAYLOAD_OFF;
}

uint64_t db_wal_tag_off(void) {
    return WAL_TAG_OFF;
}

static inline uint8_t wal_read8(const uint8_t *block, uint64_t n, uint64_t addr) {
    if (block == 0) return 0;
    if (addr < n && (uint64_t)1 <= n - addr) {
        return block[addr];
    }
    return 0;
}

static inline uint32_t wal_read32(const uint8_t *block, uint64_t n, uint64_t addr) {
    if (block == 0) return 0;
    if (addr < n && (uint64_t)4 <= n - addr) {
        return (uint32_t)block[addr] |
               ((uint32_t)block[addr + 1] << 8) |
               ((uint32_t)block[addr + 2] << 16) |
               ((uint32_t)block[addr + 3] << 24);
    }
    return 0;
}

static inline void wal_write8(uint8_t *block, uint64_t n, uint64_t addr, uint8_t val) {
    if (block == 0) return;
    if (addr < n && (uint64_t)1 <= n - addr) {
        block[addr] = val;
    }
}

static inline void wal_write32(uint8_t *block, uint64_t n, uint64_t addr, uint32_t val) {
    if (block == 0) return;
    if (addr < n && (uint64_t)4 <= n - addr) {
        block[addr] = (uint8_t)(val & 0xFF);
        block[addr + 1] = (uint8_t)((val >> 8) & 0xFF);
        block[addr + 2] = (uint8_t)((val >> 16) & 0xFF);
        block[addr + 3] = (uint8_t)((val >> 24) & 0xFF);
    }
}

static inline void wal_copy16_in(uint8_t *block, uint64_t n, uint64_t addr, const uint8_t *src) {
    uint64_t i;

    if (block == 0 || src == 0) return;
    if (addr < n && (uint64_t)WAL_ID_SIZE <= n - addr) {
        i = 0;
        while (i < WAL_ID_SIZE) {
            block[addr + i] = src[i];
            i = i + 1;
        }
    }
}

static inline void wal_copy16_out(const uint8_t *block, uint64_t n, uint64_t addr, uint8_t *dst) {
    uint64_t i;

    if (block == 0 || dst == 0) return;
    if (addr < n && (uint64_t)WAL_ID_SIZE <= n - addr) {
        i = 0;
        while (i < WAL_ID_SIZE) {
            dst[i] = block[addr + i];
            i = i + 1;
        }
    }
}

uint8_t db_wal_pack(uint8_t *restrict block, uint64_t n, const uint8_t *restrict id, uint8_t typ, const uint8_t *restrict payload, uint64_t plen) {
    uint64_t i;
    uint64_t addr;
    uint32_t rec_len;

    if (block == 0 || n != WAL_N) {
        return 0;
    }
    if (id == 0) {
        return 0;
    }
    if (db_wal_type_ok(typ) == 0) {
        return 0;
    }
    if (plen > WAL_TAG_OFF - WAL_PAYLOAD_OFF) {
        return 0;
    }
    if (plen > 0 && payload == 0) {
        return 0;
    }
    if (db_wal_bounds(block, n, WAL_MAGIC_OFF, 4) == 0) {
        return 0;
    }
    if (db_wal_bounds(block, n, WAL_ID_OFF, WAL_ID_SIZE) == 0) {
        return 0;
    }
    if (db_wal_bounds(block, n, WAL_TYPE_OFF, 1) == 0) {
        return 0;
    }

    rec_len = (uint32_t)plen;
    wal_write32(block, n, WAL_MAGIC_OFF, WAL_MAGIC);
    wal_write32(block, n, WAL_LEN_OFF, rec_len);
    wal_copy16_in(block, n, WAL_ID_OFF, id);
    wal_write8(block, n, WAL_TYPE_OFF, typ);

    i = 0;
    while (i < plen) {
        addr = WAL_PAYLOAD_OFF + i;
        if (addr < n && (uint64_t)1 <= n - addr) {
            block[addr] = payload[i];
        }
        i = i + 1;
    }

    i = WAL_PAYLOAD_OFF + plen;
    while (i < WAL_TAG_OFF) {
        if (i < n && (uint64_t)1 <= n - i) {
            block[i] = 0;
        }
        i = i + 1;
    }
    return 1;
}

db_wal_desc_t db_wal_unpack(const uint8_t *restrict block, uint64_t n, uint8_t *restrict id) {
    db_wal_desc_t d;
    uint32_t magic;
    uint32_t rec_len;
    uint64_t max_plen;

    d.magic = 0;
    d.len = 0;
    d.typ = 0;
    d.plen = 0;
    d.ok = 0;

    if (block == 0 || n != WAL_N) {
        return d;
    }
    if (db_wal_bounds(block, n, WAL_MAGIC_OFF, 4) == 0) {
        return d;
    }
    if (db_wal_bounds(block, n, WAL_TAG_OFF, WAL_TAG_SIZE) == 0) {
        return d;
    }

    magic = wal_read32(block, n, WAL_MAGIC_OFF);
    rec_len = wal_read32(block, n, WAL_LEN_OFF);
    d.magic = magic;
    d.len = rec_len;
    d.typ = wal_read8(block, n, WAL_TYPE_OFF);

    if (magic != WAL_MAGIC) {
        return d;
    }
    max_plen = WAL_TAG_OFF - WAL_PAYLOAD_OFF;
    if ((uint64_t)rec_len > max_plen) {
        return d;
    }

    d.plen = rec_len;
    if (id != 0) {
        wal_copy16_out(block, n, WAL_ID_OFF, id);
    }
    d.ok = 1;
    return d;
}

uint8_t db_wal_tag_slot(uint8_t *restrict block, uint64_t n, const uint8_t *restrict src, uint8_t *restrict dst) {
    if (block == 0 || n != WAL_N) {
        return 0;
    }
    if (db_wal_bounds(block, n, WAL_TAG_OFF, WAL_TAG_SIZE) == 0) {
        return 0;
    }
    if (src != 0) {
        wal_copy16_in(block, n, WAL_TAG_OFF, src);
    }
    if (dst != 0) {
        wal_copy16_out(block, n, WAL_TAG_OFF, dst);
    }
    return 1;
}
