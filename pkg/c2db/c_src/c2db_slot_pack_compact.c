#include "c2db_slot_pack_compact.h"

static inline uint8_t c2db_pack_bounds(const uint8_t *p, uint64_t n, uint64_t addr, uint64_t len) {
    if (p == 0) return 0;
    if (addr < n && len <= n - addr) return 1;
    return 0;
}

static inline uint8_t c2db_pack_read8(const uint8_t *p, uint64_t n, uint64_t off) {
    if (c2db_pack_bounds(p, n, off, 1) != 0) {
        return p[off];
    }
    return 0;
}

static inline void c2db_pack_write8(uint8_t *p, uint64_t n, uint64_t off, uint8_t val) {
    if (c2db_pack_bounds(p, n, off, 1) != 0) {
        p[off] = val;
    }
}

static inline uint16_t c2db_pack_read16(const uint8_t *p, uint64_t n, uint64_t off) {
    if (c2db_pack_bounds(p, n, off, 2) != 0) {
        return (uint16_t)((uint16_t)p[off] | ((uint16_t)p[off + 1] << 8));
    }
    return 0;
}

static inline void c2db_pack_write16(uint8_t *p, uint64_t n, uint64_t off, uint16_t val) {
    if (c2db_pack_bounds(p, n, off, 2) != 0) {
        p[off] = (uint8_t)(val & 0xFF);
        p[off + 1] = (uint8_t)((val >> 8) & 0xFF);
    }
}

static inline void c2db_pack_copy(uint8_t *dst, uint64_t dn, uint64_t doff, const uint8_t *src, uint64_t sn, uint64_t soff, uint64_t len) {
    uint64_t i;
    if (c2db_pack_bounds(dst, dn, doff, len) == 0) return;
    if (c2db_pack_bounds(src, sn, soff, len) == 0) return;
    i = 0;
    while (i < len) {
        dst[doff + i] = src[soff + i];
        i = i + 1;
    }
}

c2db_compact_result_t c2db_slot_pack_compact(
    uint8_t *page,
    uint64_t n,
    uint8_t *scratch,
    uint64_t scratch_n,
    uint64_t idlen,
    uint8_t drop_tombstones
) {
    c2db_compact_result_t res;
    uint64_t nslots;
    uint64_t old_free_lo;
    uint64_t old_free_hi;
    uint64_t cur_free_lo;
    uint64_t cur_free_hi;
    uint64_t new_nslots;
    uint64_t i;
    uint64_t old_free_space;
    uint64_t new_free_space;
    uint8_t typ;

    res.slots_before = 0;
    res.slots_after = 0;
    res.bytes_freed = 0;
    res.ok = 0;

    if (page == 0 || n != (uint64_t)C2DB_PAGE_N) {
        return res;
    }
    if (scratch == 0 || scratch_n < (uint64_t)C2DB_PAGE_N) {
        return res;
    }

    typ = c2db_pack_read8(page, n, (uint64_t)C2DB_TYPE_OFFSET);
    if (typ != (uint8_t)C2DB_TYPE_LEAF) {
        return res;
    }

    nslots = (uint64_t)c2db_pack_read16(page, n, (uint64_t)C2DB_NSLOTS_OFFSET);
    old_free_lo = (uint64_t)c2db_pack_read16(page, n, (uint64_t)C2DB_FREE_LO_OFFSET);
    old_free_hi = (uint64_t)c2db_pack_read16(page, n, (uint64_t)C2DB_FREE_HI_OFFSET);

    if (old_free_lo > (uint64_t)C2DB_PAGE_N || old_free_hi > (uint64_t)C2DB_PAGE_N || old_free_lo > old_free_hi) {
        return res;
    }

    // Copie de sauvegarde dans scratch
    c2db_pack_copy(scratch, scratch_n, 0, page, n, 0, (uint64_t)C2DB_PAGE_N);

    // Réinitialisation de la région des cellules et des slots
    cur_free_lo = (uint64_t)C2DB_BODY_OFFSET;
    cur_free_hi = (uint64_t)C2DB_PAGE_N;
    new_nslots = 0;

    i = 0;
    while (i < nslots) {
        uint64_t slot_addr;
        uint64_t cell_off;
        uint64_t cklen;
        uint64_t cvlen;
        uint64_t cell_size;
        uint8_t is_tombstone;

        slot_addr = (uint64_t)C2DB_BODY_OFFSET + i * (uint64_t)C2DB_BT_SLOT_SIZE;
        if (c2db_pack_bounds(scratch, scratch_n, slot_addr, (uint64_t)C2DB_BT_SLOT_SIZE) == 0) {
            return res;
        }
        cell_off = (uint64_t)c2db_pack_read16(scratch, scratch_n, slot_addr);
        if (c2db_pack_bounds(scratch, scratch_n, cell_off, 4) == 0) {
            return res;
        }
        cklen = (uint64_t)c2db_pack_read16(scratch, scratch_n, cell_off);
        cvlen = (uint64_t)c2db_pack_read16(scratch, scratch_n, cell_off + 2);
        cell_size = (uint64_t)4 + cklen + idlen + cvlen;
        if (c2db_pack_bounds(scratch, scratch_n, cell_off, cell_size) == 0) {
            return res;
        }

        is_tombstone = 0;
        if (drop_tombstones != 0) {
            if (cvlen == 0) {
                is_tombstone = 1;
            } else if (idlen == 16) {
                uint64_t id8_off = cell_off + (uint64_t)4 + cklen + (uint64_t)8;
                if (c2db_pack_bounds(scratch, scratch_n, id8_off, 1) != 0) {
                    uint8_t id8 = scratch[id8_off];
                    uint8_t kind = (uint8_t)((id8 & 0x3F) >> 2);
                    if (kind == 1) {
                        is_tombstone = 1;
                    }
                }
            }
        }

        if (is_tombstone == 0) {
            uint64_t new_cell_off;
            uint64_t new_slot_addr;

            if (cell_size > cur_free_hi) {
                return res;
            }
            new_cell_off = cur_free_hi - cell_size;
            if (cur_free_lo + (uint64_t)C2DB_BT_SLOT_SIZE > new_cell_off) {
                return res;
            }

            // Copie de la cellule compactée vers la zone haute
            c2db_pack_copy(page, n, new_cell_off, scratch, scratch_n, cell_off, cell_size);

            // Mise à jour du slot pointant vers la cellule compactée
            new_slot_addr = (uint64_t)C2DB_BODY_OFFSET + new_nslots * (uint64_t)C2DB_BT_SLOT_SIZE;
            c2db_pack_write16(page, n, new_slot_addr, (uint16_t)new_cell_off);

            cur_free_lo = cur_free_lo + (uint64_t)C2DB_BT_SLOT_SIZE;
            cur_free_hi = new_cell_off;
            new_nslots = new_nslots + 1;
        }

        i = i + 1;
    }

    // Mise à jour de l'en-tête de page
    c2db_pack_write16(page, n, (uint64_t)C2DB_NSLOTS_OFFSET, (uint16_t)new_nslots);
    c2db_pack_write16(page, n, (uint64_t)C2DB_FREE_LO_OFFSET, (uint16_t)cur_free_lo);
    c2db_pack_write16(page, n, (uint64_t)C2DB_FREE_HI_OFFSET, (uint16_t)cur_free_hi);

    old_free_space = old_free_hi - old_free_lo;
    new_free_space = cur_free_hi - cur_free_lo;
    if (new_free_space >= old_free_space) {
        res.bytes_freed = new_free_space - old_free_space;
    } else {
        res.bytes_freed = 0;
    }

    res.slots_before = nslots;
    res.slots_after = new_nslots;
    res.ok = 1;
    return res;
}
