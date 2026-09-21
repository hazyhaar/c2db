#include "c2db_btree_prefix_search.h"

static inline uint8_t c2db_pref_bounds(const uint8_t *page, uint64_t n, uint64_t addr, uint64_t len) {
    if (page == 0) return 0;
    if (addr < n && len <= n - addr) return 1;
    return 0;
}

static inline uint8_t c2db_pref_read8(const uint8_t *page, uint64_t n, uint64_t off) {
    if (c2db_pref_bounds(page, n, off, 1) != 0) {
        return page[off];
    }
    return 0;
}

static inline uint16_t c2db_pref_read16(const uint8_t *page, uint64_t n, uint64_t off) {
    if (c2db_pref_bounds(page, n, off, 2) != 0) {
        return (uint16_t)((uint16_t)page[off] | ((uint16_t)page[off + 1] << 8));
    }
    return 0;
}

static int32_t c2db_pref_cmp(const uint8_t *page, uint64_t n, uint64_t cell_key_off, uint64_t cell_klen, const uint8_t *pref, uint64_t plen) {
    uint64_t i;
    uint64_t m;
    uint8_t a;
    uint8_t b;

    m = cell_klen;
    if (plen < m) {
        m = plen;
    }
    i = 0;
    while (i < m) {
        a = 0;
        b = 0;
        if (cell_key_off < n && i < n - cell_key_off) {
            a = page[cell_key_off + i];
        }
        if (pref != 0 && i < plen) {
            b = pref[i];
        }
        if (a < b) {
            return -1;
        }
        if (a > b) {
            return 1;
        }
        i = i + 1;
    }
    if (cell_klen < plen) {
        return -1;
    }
    return 0;
}

c2db_prefix_result_t c2db_btree_prefix_search(const uint8_t *page, uint64_t n, const uint8_t *pref, uint64_t plen) {
    c2db_prefix_result_t res;
    uint64_t nslots;
    uint64_t low;
    uint64_t high;
    uint64_t mid;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint16_t cklen;
    int32_t cmp;
    uint8_t typ;

    res.slot_idx = 0;
    res.count = 0;
    res.found = 0;

    if (page == 0 || n != (uint64_t)C2DB_PAGE_N) {
        return res;
    }
    if (pref == 0 || plen == 0 || plen > 0xFFFF) {
        return res;
    }

    typ = c2db_pref_read8(page, n, (uint64_t)C2DB_TYPE_OFFSET);
    if (typ != (uint8_t)C2DB_TYPE_LEAF) {
        return res;
    }

    nslots = (uint64_t)c2db_pref_read16(page, n, (uint64_t)C2DB_NSLOTS_OFFSET);
    if (nslots == 0) {
        return res;
    }

    low = 0;
    high = nslots;
    while (low < high) {
        mid = low + (high - low) / 2;
        slot_addr = (uint64_t)C2DB_BODY_OFFSET + mid * (uint64_t)C2DB_BT_SLOT_SIZE;
        if (c2db_pref_bounds(page, n, slot_addr, (uint64_t)C2DB_BT_SLOT_SIZE) == 0) {
            return res;
        }
        cell_off = (uint64_t)c2db_pref_read16(page, n, slot_addr);
        if (c2db_pref_bounds(page, n, cell_off, 4) == 0) {
            return res;
        }
        cklen = c2db_pref_read16(page, n, cell_off);
        if (c2db_pref_bounds(page, n, cell_off + 4, (uint64_t)cklen) == 0) {
            return res;
        }

        cmp = c2db_pref_cmp(page, n, cell_off + 4, (uint64_t)cklen, pref, plen);
        if (cmp < 0) {
            low = mid + 1;
        } else {
            high = mid;
        }
    }

    if (low < nslots) {
        slot_addr = (uint64_t)C2DB_BODY_OFFSET + low * (uint64_t)C2DB_BT_SLOT_SIZE;
        cell_off = (uint64_t)c2db_pref_read16(page, n, slot_addr);
        cklen = c2db_pref_read16(page, n, cell_off);
        cmp = c2db_pref_cmp(page, n, cell_off + 4, (uint64_t)cklen, pref, plen);
        if (cmp == 0) {
            uint64_t idx;
            res.found = 1;
            res.slot_idx = low;
            res.count = 1;

            idx = low + 1;
            while (idx < nslots) {
                slot_addr = (uint64_t)C2DB_BODY_OFFSET + idx * (uint64_t)C2DB_BT_SLOT_SIZE;
                if (c2db_pref_bounds(page, n, slot_addr, (uint64_t)C2DB_BT_SLOT_SIZE) == 0) {
                    break;
                }
                cell_off = (uint64_t)c2db_pref_read16(page, n, slot_addr);
                if (c2db_pref_bounds(page, n, cell_off, 4) == 0) {
                    break;
                }
                cklen = c2db_pref_read16(page, n, cell_off);
                if (c2db_pref_bounds(page, n, cell_off + 4, (uint64_t)cklen) == 0) {
                    break;
                }
                cmp = c2db_pref_cmp(page, n, cell_off + 4, (uint64_t)cklen, pref, plen);
                if (cmp == 0) {
                    res.count = res.count + 1;
                    idx = idx + 1;
                } else {
                    break;
                }
            }
        }
    }

    return res;
}
