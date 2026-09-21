// SPDX-License-Identifier: Apache-2.0 OR MIT

#include <stdint.h>
#include <string.h>

#define PAGE_N     16384
#define HLC        8
#define TYPE       20
#define NSLOTS     22
#define FREE_LO    24
#define FREE_HI    26
#define BODY       64
#define TYPE_LEAF  1
#define TYPE_INTERNAL 2
#define BT_SLOT    2
#define BT_IDLEN   16

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

typedef struct {
    uint64_t root;
    uint64_t used;
    uint64_t nkeys;
    uint64_t pages;
    uint8_t  ok;
    uint8_t  full;
} db_bt_heap_st_t;

#define BT_PATH_MAX 32
#define BT_SPLIT_KEY_MAX 16384

typedef struct {
    uint64_t nfound;
    uint8_t  ok;
} db_bt_scan_t;

static inline uint8_t bt_bounds(const uint8_t *page, uint64_t n, uint64_t addr, uint64_t len) {
    if (page == 0) return 0;
    if (n != PAGE_N) return 0;
    if (addr < n && (uint64_t)len <= n - addr) return 1;
    return 0;
}

static inline uint8_t bt_read8(const uint8_t *page, uint64_t n, uint64_t addr) {
    if (page == 0) return 0;
    if (addr < n && (uint64_t)1 <= n - addr) {
        return page[addr];
    }
    return 0;
}

static inline uint16_t bt_read16(const uint8_t *page, uint64_t n, uint64_t addr) {
    if (page == 0) return 0;
    if (addr < n && (uint64_t)2 <= n - addr) {
        return (uint16_t)page[addr] | ((uint16_t)page[addr + 1] << 8);
    }
    return 0;
}

static inline void bt_write8(uint8_t *page, uint64_t n, uint64_t addr, uint8_t val) {
    if (page == 0) return;
    if (addr < n && (uint64_t)1 <= n - addr) {
        page[addr] = val;
    }
}

static inline void bt_write16(uint8_t *page, uint64_t n, uint64_t addr, uint16_t val) {
    if (page == 0) return;
    if (addr < n && (uint64_t)2 <= n - addr) {
        page[addr] = (uint8_t)(val & 0xFF);
        page[addr + 1] = (uint8_t)((val >> 8) & 0xFF);
    }
}

static inline void bt_write32(uint8_t *page, uint64_t n, uint64_t addr, uint32_t val) {
    if (page == 0) return;
    if (addr < n && (uint64_t)4 <= n - addr) {
        page[addr] = (uint8_t)(val & 0xFF);
        page[addr + 1] = (uint8_t)((val >> 8) & 0xFF);
        page[addr + 2] = (uint8_t)((val >> 16) & 0xFF);
        page[addr + 3] = (uint8_t)((val >> 24) & 0xFF);
    }
}

static inline uint64_t bt_read64(const uint8_t *page, uint64_t n, uint64_t addr) {
    if (page == 0) return 0;
    if (addr < n && (uint64_t)8 <= n - addr) {
        return (uint64_t)page[addr] |
               ((uint64_t)page[addr + 1] << 8) |
               ((uint64_t)page[addr + 2] << 16) |
               ((uint64_t)page[addr + 3] << 24) |
               ((uint64_t)page[addr + 4] << 32) |
               ((uint64_t)page[addr + 5] << 40) |
               ((uint64_t)page[addr + 6] << 48) |
               ((uint64_t)page[addr + 7] << 56);
    }
    return 0;
}

static inline void bt_write64(uint8_t *page, uint64_t n, uint64_t addr, uint64_t val) {
    if (page == 0) return;
    if (addr < n && (uint64_t)8 <= n - addr) {
        page[addr] = (uint8_t)(val & 0xFF);
        page[addr + 1] = (uint8_t)((val >> 8) & 0xFF);
        page[addr + 2] = (uint8_t)((val >> 16) & 0xFF);
        page[addr + 3] = (uint8_t)((val >> 24) & 0xFF);
        page[addr + 4] = (uint8_t)((val >> 32) & 0xFF);
        page[addr + 5] = (uint8_t)((val >> 40) & 0xFF);
        page[addr + 6] = (uint8_t)((val >> 48) & 0xFF);
        page[addr + 7] = (uint8_t)((val >> 56) & 0xFF);
    }
}

static void bt_copy_bytes(uint8_t *dst, uint64_t dn, uint64_t doff, const uint8_t *src, uint64_t sn, uint64_t soff, uint64_t len) {
    uint64_t i;

    if (dst == 0 || src == 0) return;
    i = 0;
    while (i < len) {
        if (doff < dn && i < dn - doff) {
            if (soff < sn && i < sn - soff) {
                dst[doff + i] = src[soff + i];
            }
        }
        i = i + 1;
    }
}

static inline void bt_cow(const uint8_t *pub, uint8_t *dirty, uint64_t n) {
    if (pub == 0 || dirty == 0) return;
    if (n == 0) return;
    memcpy(dirty, pub, n);
}

static uint64_t bt_cow_page(const uint8_t *pub, uint8_t *dirty, uint64_t nbytes, uint64_t pg, uint64_t mask) {
    uint64_t base;
    uint64_t bit;

    if (pub == 0 || dirty == 0) {
        return mask;
    }
    base = pg * (uint64_t)PAGE_N;
    if (base >= nbytes) {
        return mask;
    }
    if ((uint64_t)PAGE_N > nbytes - base) {
        return mask;
    }
    if (pg >= 64) {
        memcpy(dirty + base, pub + base, (size_t)PAGE_N);
        return mask;
    }
    bit = (uint64_t)1 << pg;
    if ((mask & bit) != 0) {
        return mask;
    }
    memcpy(dirty + base, pub + base, (size_t)PAGE_N);
    return mask | bit;
}

static inline void bt_copy_in(uint8_t *page, uint64_t n, uint64_t addr, const uint8_t *src, uint64_t len) {
    uint64_t i;

    if (page == 0 || src == 0) return;
    i = 0;
    while (i < len) {
        if (addr < n && i < n - addr) {
            page[addr + i] = src[i];
        }
        i = i + 1;
    }
}

static inline void bt_copy_out(const uint8_t *page, uint64_t n, uint64_t addr, uint8_t *dst, uint64_t len) {
    uint64_t i;

    if (page == 0 || dst == 0) return;
    i = 0;
    while (i < len) {
        if (addr < n && i < n - addr) {
            dst[i] = page[addr + i];
        }
        i = i + 1;
    }
}

static inline uint8_t bt_keyeq(const uint8_t *page, uint64_t n, uint64_t koff, uint64_t klen, const uint8_t *key) {
    uint64_t i;
    uint64_t addr;

    if (page == 0 || key == 0) return 0;
    i = 0;
    while (i < klen) {
        addr = koff + i;
        if (addr < n && (uint64_t)1 <= n - addr) {
            if (page[addr] != key[i]) {
                return 0;
            }
        } else {
            return 0;
        }
        i = i + 1;
    }
    return 1;
}

static int32_t bt_keycmp(const uint8_t *a, uint64_t alen, const uint8_t *b, uint64_t blen) {
    uint64_t n;
    uint64_t i;
    uint8_t av;
    uint8_t bv;

    n = alen;
    if (blen < n) {
        n = blen;
    }
    i = 0;
    while (i < n) {
        av = 0;
        bv = 0;
        if (a != 0) {
            av = a[i];
        }
        if (b != 0) {
            bv = b[i];
        }
        if (av < bv) {
            return -1;
        }
        if (av > bv) {
            return 1;
        }
        i = i + 1;
    }
    if (alen < blen) {
        return -1;
    }
    if (alen > blen) {
        return 1;
    }
    return 0;
}

static int32_t bt_keycmp_at(const uint8_t *page, uint64_t n, uint64_t aoff, uint64_t alen, const uint8_t *b, uint64_t blen) {
    uint64_t m;
    uint64_t i;
    uint8_t av;
    uint8_t bv;

    m = alen;
    if (blen < m) {
        m = blen;
    }
    i = 0;
    while (i < m) {
        av = 0;
        bv = 0;
        if (page != 0) {
            if (aoff < n && i < n - aoff) {
                av = page[aoff + i];
            }
        }
        if (b != 0) {
            bv = b[i];
        }
        if (av < bv) {
            return -1;
        }
        if (av > bv) {
            return 1;
        }
        i = i + 1;
    }
    if (alen < blen) {
        return -1;
    }
    if (alen > blen) {
        return 1;
    }
    return 0;
}

static int32_t bt_keycmp_pp(const uint8_t *page, uint64_t n, uint64_t aoff, uint64_t alen, uint64_t boff, uint64_t blen) {
    uint64_t m;
    uint64_t i;
    uint8_t av;
    uint8_t bv;

    m = alen;
    if (blen < m) {
        m = blen;
    }
    i = 0;
    while (i < m) {
        av = 0;
        bv = 0;
        if (page != 0) {
            if (aoff < n && i < n - aoff) {
                av = page[aoff + i];
            }
            if (boff < n && i < n - boff) {
                bv = page[boff + i];
            }
        }
        if (av < bv) {
            return -1;
        }
        if (av > bv) {
            return 1;
        }
        i = i + 1;
    }
    if (alen < blen) {
        return -1;
    }
    if (alen > blen) {
        return 1;
    }
    return 0;
}

static uint64_t bt_find(const uint8_t *page, uint64_t n, const uint8_t *key, uint64_t klen) {
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint16_t cklen;

    nslots = (uint64_t)bt_read16(page, n, NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_bounds(page, n, slot_addr, (uint64_t)BT_SLOT) == 0) {
            return nslots;
        }
        cell_off = (uint64_t)bt_read16(page, n, slot_addr);
        if (bt_bounds(page, n, cell_off, 4) != 0) {
            cklen = bt_read16(page, n, cell_off);
            if ((uint64_t)cklen == klen) {
                if (bt_keyeq(page, n, cell_off + 4, klen, key) != 0) {
                    return i;
                }
            }
        }
        i = i + 1;
    }
    return nslots;
}

static inline uint8_t bt_off_ok(uint64_t n, uint64_t base, uint64_t off, uint64_t len) {
    uint64_t addr;

    if (off >= (uint64_t)PAGE_N) return 0;
    if (len > (uint64_t)PAGE_N - off) return 0;
    if (base >= n) return 0;
    if ((uint64_t)PAGE_N > n - base) return 0;
    addr = base + off;
    if (addr < n && len <= n - addr) return 1;
    return 0;
}

static uint64_t bt_find_at(const uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen) {
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint16_t cklen;

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
            return nslots;
        }
        cell_off = (uint64_t)bt_read16(heap, n, base + slot_off);
        if (bt_off_ok(n, base, cell_off, 4) != 0) {
            cklen = bt_read16(heap, n, base + cell_off);
            if ((uint64_t)cklen == klen) {
                if (bt_keyeq(heap, n, base + cell_off + 4, klen, key) != 0) {
                    return i;
                }
            }
        }
        i = i + 1;
    }
    return nslots;
}

static db_bt_get_t bt_get_at(const uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen, uint8_t *out, uint64_t outn) {
    db_bt_get_t g;
    uint64_t nslots;
    uint64_t found;
    uint64_t slot_off;
    uint64_t cell_off;
    uint16_t cklen;
    uint16_t cvlen;
    uint64_t copy_n;
    uint8_t typ;

    g.len = 0;
    g.walked = 0;
    g.found = 0;
    g.fallback = 0;

    if (heap == 0) {
        return g;
    }
    if (key == 0 || klen == 0 || klen > 0xFFFF) {
        return g;
    }

    typ = bt_read8(heap, n, base + TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return g;
    }

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    found = bt_find_at(heap, n, base, key, klen);
    if (found >= nslots) {
        return g;
    }

    slot_off = (uint64_t)BODY + found * (uint64_t)BT_SLOT;
    if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
        return g;
    }
    cell_off = (uint64_t)bt_read16(heap, n, base + slot_off);
    if (bt_off_ok(n, base, cell_off, 4) == 0) {
        return g;
    }
    cklen = bt_read16(heap, n, base + cell_off);
    cvlen = bt_read16(heap, n, base + cell_off + 2);
    if ((uint64_t)cklen != klen) {
        return g;
    }
    if (bt_off_ok(n, base, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)cvlen) == 0) {
        return g;
    }

    g.len = (uint64_t)cvlen;
    g.found = 1;

    copy_n = (uint64_t)cvlen;
    if (copy_n > outn) {
        copy_n = outn;
    }
    if (out != 0 && copy_n > 0) {
        bt_copy_out(heap, n, base + cell_off + 4 + (uint64_t)cklen, out, copy_n);
    }
    return g;
}

static db_bt_state_t bt_insert_at(uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen, const uint8_t *val, uint64_t vlen) {
    db_bt_state_t out;
    uint64_t nslots;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t cell_size;
    uint64_t need_lo;
    uint64_t new_hi;
    uint64_t cell_off;
    uint64_t found;
    uint64_t slot_off;
    uint64_t slot_idx;
    uint8_t typ;

    out.nkeys = 0;
    out.ok = 0;
    out.copied = 0;

    if (heap == 0) {
        return out;
    }
    if (klen == 0 || klen > 0xFFFF || vlen > 0xFFFF) {
        return out;
    }
    if (key == 0) {
        return out;
    }
    if (vlen > 0 && val == 0) {
        return out;
    }

    typ = bt_read8(heap, n, base + TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return out;
    }

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    free_lo = (uint64_t)bt_read16(heap, n, base + FREE_LO);
    free_hi = (uint64_t)bt_read16(heap, n, base + FREE_HI);
    out.nkeys = nslots;

    if (free_lo < (uint64_t)BODY) {
        return out;
    }
    if (free_hi > (uint64_t)PAGE_N) {
        return out;
    }
    if (free_lo > free_hi) {
        return out;
    }

    cell_size = (uint64_t)4 + klen + vlen;
    found = bt_find_at(heap, n, base, key, klen);

    if (found < nslots) {
        need_lo = free_lo;
        slot_idx = found;
    } else {
        if (free_lo >= (uint64_t)PAGE_N) {
            return out;
        }
        if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
            return out;
        }
        need_lo = free_lo + (uint64_t)BT_SLOT;
        slot_idx = nslots;
    }

    if (cell_size > free_hi) {
        return out;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return out;
    }

    cell_off = new_hi;
    slot_off = (uint64_t)BODY + slot_idx * (uint64_t)BT_SLOT;
    if (bt_off_ok(n, base, cell_off, cell_size) == 0) {
        return out;
    }
    if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
        return out;
    }

    bt_write16(heap, n, base + cell_off, (uint16_t)klen);
    bt_write16(heap, n, base + cell_off + 2, (uint16_t)vlen);
    bt_copy_in(heap, n, base + cell_off + 4, key, klen);
    if (vlen > 0) {
        bt_copy_in(heap, n, base + cell_off + 4 + klen, val, vlen);
    }
    bt_write16(heap, n, base + slot_off, (uint16_t)cell_off);

    if (found >= nslots) {
        nslots = nslots + 1;
        bt_write16(heap, n, base + NSLOTS, (uint16_t)nslots);
        bt_write16(heap, n, base + FREE_LO, (uint16_t)need_lo);
    }
    bt_write16(heap, n, base + FREE_HI, (uint16_t)new_hi);

    out.nkeys = nslots;
    out.ok = 1;
    return out;
}

db_bt_state_t db_bt_leaf_init(uint8_t *page, uint64_t n) {
    db_bt_state_t st;

    st.nkeys = 0;
    st.ok = 0;
    st.copied = 0;

    if (page == 0 || n != PAGE_N) {
        return st;
    }
    if (bt_bounds(page, n, TYPE, 1) == 0) {
        return st;
    }
    if (bt_bounds(page, n, NSLOTS, 6) == 0) {
        return st;
    }

    bt_write8(page, n, TYPE, (uint8_t)TYPE_LEAF);
    bt_write16(page, n, NSLOTS, 0);
    bt_write16(page, n, FREE_LO, (uint16_t)BODY);
    bt_write16(page, n, FREE_HI, (uint16_t)PAGE_N);

    st.ok = 1;
    return st;
}

db_bt_state_t db_bt_insert(const uint8_t *pub, uint8_t *dirty, uint64_t n, db_bt_state_t st, const uint8_t *key, uint64_t klen, const uint8_t *val, uint64_t vlen) {
    db_bt_state_t out;
    uint64_t nslots;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t cell_size;
    uint64_t need_lo;
    uint64_t new_hi;
    uint64_t cell_off;
    uint64_t found;
    uint64_t slot_addr;
    uint64_t slot_idx;
    uint8_t typ;

    out.nkeys = 0;
    out.ok = 0;
    out.copied = 0;

    if (pub == 0 || dirty == 0 || n != PAGE_N) {
        return out;
    }

    bt_cow(pub, dirty, n);
    out.copied = 1;

    if (klen == 0 || klen > 0xFFFF || vlen > 0xFFFF) {
        return out;
    }
    if (key == 0) {
        return out;
    }
    if (vlen > 0 && val == 0) {
        return out;
    }

    typ = bt_read8(dirty, n, TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return out;
    }

    nslots = (uint64_t)bt_read16(dirty, n, NSLOTS);
    free_lo = (uint64_t)bt_read16(dirty, n, FREE_LO);
    free_hi = (uint64_t)bt_read16(dirty, n, FREE_HI);
    out.nkeys = nslots;

    if (free_lo < (uint64_t)BODY) {
        return out;
    }
    if (free_hi > n) {
        return out;
    }
    if (free_lo > free_hi) {
        return out;
    }

    cell_size = (uint64_t)4 + klen + vlen;
    found = bt_find(dirty, n, key, klen);

    if (found < nslots) {
        need_lo = free_lo;
        slot_idx = found;
    } else {
        if (free_lo >= n) {
            return out;
        }
        if ((uint64_t)BT_SLOT > n - free_lo) {
            return out;
        }
        need_lo = free_lo + (uint64_t)BT_SLOT;
        slot_idx = nslots;
    }

    if (cell_size > free_hi) {
        return out;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return out;
    }

    cell_off = new_hi;
    slot_addr = (uint64_t)BODY + slot_idx * (uint64_t)BT_SLOT;
    if (bt_bounds(dirty, n, cell_off, cell_size) == 0) {
        return out;
    }
    if (bt_bounds(dirty, n, slot_addr, (uint64_t)BT_SLOT) == 0) {
        return out;
    }

    bt_write16(dirty, n, cell_off, (uint16_t)klen);
    bt_write16(dirty, n, cell_off + 2, (uint16_t)vlen);
    bt_copy_in(dirty, n, cell_off + 4, key, klen);
    if (vlen > 0) {
        bt_copy_in(dirty, n, cell_off + 4 + klen, val, vlen);
    }
    bt_write16(dirty, n, slot_addr, (uint16_t)cell_off);

    if (found >= nslots) {
        nslots = nslots + 1;
        bt_write16(dirty, n, NSLOTS, (uint16_t)nslots);
        bt_write16(dirty, n, FREE_LO, (uint16_t)need_lo);
    }
    bt_write16(dirty, n, FREE_HI, (uint16_t)new_hi);

    out.nkeys = nslots;
    out.ok = 1;
    return out;
}

db_bt_get_t db_bt_get(const uint8_t *page, uint64_t n, const uint8_t *key, uint64_t klen, uint8_t *out, uint64_t outn) {
    db_bt_get_t g;
    uint64_t nslots;
    uint64_t found;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint16_t cklen;
    uint16_t cvlen;
    uint64_t copy_n;
    uint8_t typ;

    g.len = 0;
    g.walked = 0;
    g.found = 0;
    g.fallback = 0;

    if (page == 0 || n != PAGE_N) {
        return g;
    }
    if (key == 0 || klen == 0 || klen > 0xFFFF) {
        return g;
    }

    typ = bt_read8(page, n, TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return g;
    }

    nslots = (uint64_t)bt_read16(page, n, NSLOTS);
    found = bt_find(page, n, key, klen);
    if (found >= nslots) {
        return g;
    }

    slot_addr = (uint64_t)BODY + found * (uint64_t)BT_SLOT;
    if (bt_bounds(page, n, slot_addr, (uint64_t)BT_SLOT) == 0) {
        return g;
    }
    cell_off = (uint64_t)bt_read16(page, n, slot_addr);
    if (bt_bounds(page, n, cell_off, 4) == 0) {
        return g;
    }
    cklen = bt_read16(page, n, cell_off);
    cvlen = bt_read16(page, n, cell_off + 2);
    if ((uint64_t)cklen != klen) {
        return g;
    }
    if (bt_bounds(page, n, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)cvlen) == 0) {
        return g;
    }

    g.len = (uint64_t)cvlen;
    g.found = 1;

    copy_n = (uint64_t)cvlen;
    if (copy_n > outn) {
        copy_n = outn;
    }
    if (out != 0 && copy_n > 0) {
        bt_copy_out(page, n, cell_off + 4 + (uint64_t)cklen, out, copy_n);
    }
    return g;
}

static uint8_t bt_leaf_repack(uint8_t *page, uint64_t n, uint64_t idlen) {
    uint8_t tmp[16384];
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint64_t cklen;
    uint64_t cvlen;
    uint64_t cell_size;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t dslot;
    uint64_t hlc;
    uint8_t typ;
    db_bt_state_t initst;

    if (page == 0 || n != (uint64_t)PAGE_N) {
        return 0;
    }
    memcpy(tmp, page, (size_t)PAGE_N);
    nslots = (uint64_t)bt_read16(tmp, PAGE_N, NSLOTS);
    hlc = bt_read64(tmp, PAGE_N, HLC);
    typ = bt_read8(tmp, PAGE_N, TYPE);
    initst = db_bt_leaf_init(page, PAGE_N);
    if (initst.ok == 0) {
        memcpy(page, tmp, (size_t)PAGE_N);
        return 0;
    }
    bt_write8(page, n, TYPE, typ);
    bt_write64(page, n, HLC, hlc);

    i = 0;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(tmp, PAGE_N, slot_addr);
        if (bt_bounds(tmp, PAGE_N, cell_off, 4) == 0) {
            memcpy(page, tmp, (size_t)PAGE_N);
            return 0;
        }
        cklen = (uint64_t)bt_read16(tmp, PAGE_N, cell_off);
        cvlen = (uint64_t)bt_read16(tmp, PAGE_N, cell_off + 2);
        cell_size = (uint64_t)4 + cklen + idlen + cvlen;
        if (bt_bounds(tmp, PAGE_N, cell_off, cell_size) == 0) {
            memcpy(page, tmp, (size_t)PAGE_N);
            return 0;
        }
        free_lo = (uint64_t)bt_read16(page, n, FREE_LO);
        free_hi = (uint64_t)bt_read16(page, n, FREE_HI);
        if (free_lo >= (uint64_t)PAGE_N) {
            memcpy(page, tmp, (size_t)PAGE_N);
            return 0;
        }
        if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
            memcpy(page, tmp, (size_t)PAGE_N);
            return 0;
        }
        if (cell_size > free_hi) {
            memcpy(page, tmp, (size_t)PAGE_N);
            return 0;
        }
        new_hi = free_hi - cell_size;
        if (free_lo + (uint64_t)BT_SLOT > new_hi) {
            memcpy(page, tmp, (size_t)PAGE_N);
            return 0;
        }
        bt_copy_bytes(page, n, new_hi, tmp, PAGE_N, cell_off, cell_size);
        dslot = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        bt_write16(page, n, dslot, (uint16_t)new_hi);
        bt_write16(page, n, NSLOTS, (uint16_t)(i + 1));
        bt_write16(page, n, FREE_LO, (uint16_t)(free_lo + (uint64_t)BT_SLOT));
        bt_write16(page, n, FREE_HI, (uint16_t)new_hi);
        i = i + 1;
    }
    return 1;
}

static uint8_t bt_chain_split(uint8_t *heap, uint64_t npages, uint64_t src, uint64_t dst) {
    uint8_t *sp;
    uint8_t *dp;
    uint64_t nslots;
    uint64_t mid;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint64_t cklen;
    uint64_t cvlen;
    uint64_t cell_size;
    uint64_t dst_nslots;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t dslot;
    uint64_t min_hi;
    uint64_t old_next;
    db_bt_state_t initst;

    if (src >= npages) return 0;
    if (dst >= npages) return 0;
    if (src == dst) return 0;

    sp = heap + src * (uint64_t)PAGE_N;
    dp = heap + dst * (uint64_t)PAGE_N;

    initst = db_bt_leaf_init(dp, PAGE_N);
    if (initst.ok == 0) {
        return 0;
    }

    nslots = (uint64_t)bt_read16(sp, PAGE_N, NSLOTS);
    mid = nslots / 2;
    if (mid == 0) return 0;
    if (mid >= nslots) return 0;

    uint64_t dst_bytes = 0;
    for (i = mid; i < nslots; i++) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        if (cell_off >= PAGE_N || cell_off < BODY) return 0;
        cklen = (uint64_t)bt_read16(sp, PAGE_N, cell_off);
        cvlen = (uint64_t)bt_read16(sp, PAGE_N, cell_off + 2);
        dst_bytes += (4 + cklen + cvlen + 2);
    }
    while (mid < nslots - 1 && (dst_bytes + 2500 > 16320)) {
        slot_addr = (uint64_t)BODY + mid * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        cklen = (uint64_t)bt_read16(sp, PAGE_N, cell_off);
        cvlen = (uint64_t)bt_read16(sp, PAGE_N, cell_off + 2);
        dst_bytes -= (4 + cklen + cvlen + 2);
        mid++;
    }

    i = mid;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        if (bt_bounds(sp, PAGE_N, cell_off, 4) == 0) {
            return 0;
        }
        cklen = (uint64_t)bt_read16(sp, PAGE_N, cell_off);
        cvlen = (uint64_t)bt_read16(sp, PAGE_N, cell_off + 2);
        cell_size = (uint64_t)4 + cklen + cvlen;
        if (bt_bounds(sp, PAGE_N, cell_off, cell_size) == 0) {
            return 0;
        }
        dst_nslots = (uint64_t)bt_read16(dp, PAGE_N, NSLOTS);
        free_lo = (uint64_t)bt_read16(dp, PAGE_N, FREE_LO);
        free_hi = (uint64_t)bt_read16(dp, PAGE_N, FREE_HI);
        if (free_lo >= (uint64_t)PAGE_N) {
            return 0;
        }
        if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
            return 0;
        }
        if (cell_size > free_hi) {
            return 0;
        }
        new_hi = free_hi - cell_size;
        if (free_lo + (uint64_t)BT_SLOT > new_hi) {
            return 0;
        }
        if (bt_bounds(dp, PAGE_N, new_hi, cell_size) == 0) {
            return 0;
        }
        bt_copy_bytes(dp, PAGE_N, new_hi, sp, PAGE_N, cell_off, cell_size);
        dslot = (uint64_t)BODY + dst_nslots * (uint64_t)BT_SLOT;
        if (bt_bounds(dp, PAGE_N, dslot, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        bt_write16(dp, PAGE_N, dslot, (uint16_t)new_hi);
        dst_nslots = dst_nslots + 1;
        bt_write16(dp, PAGE_N, NSLOTS, (uint16_t)dst_nslots);
        bt_write16(dp, PAGE_N, FREE_LO, (uint16_t)(free_lo + (uint64_t)BT_SLOT));
        bt_write16(dp, PAGE_N, FREE_HI, (uint16_t)new_hi);
        i = i + 1;
    }

    bt_write16(sp, PAGE_N, NSLOTS, (uint16_t)mid);
    bt_write16(sp, PAGE_N, FREE_LO, (uint16_t)((uint64_t)BODY + mid * (uint64_t)BT_SLOT));
    min_hi = (uint64_t)PAGE_N;
    i = 0;
    while (i < mid) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        if (cell_off < min_hi) {
            min_hi = cell_off;
        }
        i = i + 1;
    }
    bt_write16(sp, PAGE_N, FREE_HI, (uint16_t)min_hi);

    old_next = bt_read64(sp, PAGE_N, HLC);
    bt_write64(dp, PAGE_N, HLC, old_next);
    bt_write64(sp, PAGE_N, HLC, dst);
    if (bt_leaf_repack(sp, PAGE_N, 0) == 0) {
        return 0;
    }
    return 1;
}

static uint64_t bt_find_ver_pos(const uint8_t *page, uint64_t n, const uint8_t *key, uint64_t klen, const uint8_t *id16) {
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint16_t cklen;
    int32_t cmp;

    nslots = (uint64_t)bt_read16(page, n, NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_bounds(page, n, slot_addr, (uint64_t)BT_SLOT) == 0) {
            return i;
        }
        cell_off = (uint64_t)bt_read16(page, n, slot_addr);
        if (bt_bounds(page, n, cell_off, 4) == 0) {
            return i;
        }
        cklen = bt_read16(page, n, cell_off);
        if (bt_bounds(page, n, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)BT_IDLEN) == 0) {
            return i;
        }
        cmp = bt_keycmp_at(page, n, cell_off + 4, (uint64_t)cklen, key, klen);
        if (cmp > 0) {
            return i;
        }
        if (cmp == 0) {
            cmp = bt_keycmp_at(page, n, cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, id16, (uint64_t)BT_IDLEN);
            if (cmp > 0) {
                return i;
            }
        }
        i = i + 1;
    }
    return nslots;
}

static void bt_slot_insert(uint8_t *page, uint64_t n, uint64_t pos, uint64_t nslots, uint16_t cell_off) {
    uint64_t cur;
    uint64_t prev;
    uint64_t walk;
    uint64_t src_off;
    uint64_t dst_off;
    uint16_t sval;

    cur = nslots;
    while (cur > pos) {
        prev = 0;
        walk = 0;
        while (walk < cur) {
            prev = walk;
            walk = walk + 1;
        }
        src_off = (uint64_t)BODY + prev * (uint64_t)BT_SLOT;
        dst_off = (uint64_t)BODY + cur * (uint64_t)BT_SLOT;
        sval = bt_read16(page, n, src_off);
        bt_write16(page, n, dst_off, sval);
        cur = prev;
    }
    dst_off = (uint64_t)BODY + pos * (uint64_t)BT_SLOT;
    bt_write16(page, n, dst_off, cell_off);
}

static uint64_t bt_internal_child(const uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen) {
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint64_t child;
    uint16_t cklen;
    int32_t cmp;

    child = bt_read64(heap, n, base + HLC);
    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
            return child;
        }
        cell_off = (uint64_t)bt_read16(heap, n, base + slot_off);
        if (bt_off_ok(n, base, cell_off, 4) == 0) {
            return child;
        }
        cklen = bt_read16(heap, n, base + cell_off);
        if (bt_off_ok(n, base, cell_off, (uint64_t)4 + (uint64_t)cklen + 8) == 0) {
            return child;
        }
        cmp = bt_keycmp_at(heap, n, base + cell_off + 4, (uint64_t)cklen, key, klen);
        if (cmp > 0) {
            return child;
        }
        child = bt_read64(heap, n, base + cell_off + 4 + (uint64_t)cklen);
        i = i + 1;
    }
    return child;
}

uint64_t db_bt_child_of(const uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t root, const uint8_t *key, uint64_t klen) {
    uint64_t base;
    uint8_t typ;

    if (heap == 0 || npages == 0) {
        return npages;
    }
    if (nbytes / (uint64_t)PAGE_N != npages) {
        return npages;
    }
    if (npages * (uint64_t)PAGE_N != nbytes) {
        return npages;
    }
    if (root >= npages) {
        return npages;
    }
    if (key == 0 || klen == 0 || klen > 0xFFFF) {
        return npages;
    }
    base = root * (uint64_t)PAGE_N;
    typ = bt_read8(heap, nbytes, base + TYPE);
    if (typ == (uint8_t)TYPE_INTERNAL) {
        return bt_internal_child(heap, nbytes, base, key, klen);
    }
    return root;
}

static uint8_t bt_make_internal(uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t left, uint64_t right, uint64_t intern) {
    uint64_t ibase;
    uint64_t rbase;
    uint64_t slot0;
    uint64_t rcell;
    uint64_t cklen;
    uint64_t cell_size;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t need_lo;
    uint64_t islot;

    if (intern >= npages) return 0;
    if (left >= npages) return 0;
    if (right >= npages) return 0;
    if (intern == left) return 0;
    if (intern == right) return 0;

    ibase = intern * (uint64_t)PAGE_N;
    if (ibase >= nbytes) return 0;
    if ((uint64_t)PAGE_N > nbytes - ibase) return 0;

    bt_write8(heap, nbytes, ibase + TYPE, (uint8_t)TYPE_INTERNAL);
    bt_write16(heap, nbytes, ibase + NSLOTS, 0);
    bt_write16(heap, nbytes, ibase + FREE_LO, (uint16_t)BODY);
    bt_write16(heap, nbytes, ibase + FREE_HI, (uint16_t)PAGE_N);
    bt_write64(heap, nbytes, ibase + HLC, left);

    rbase = right * (uint64_t)PAGE_N;
    if (rbase >= nbytes) return 0;
    if ((uint64_t)PAGE_N > nbytes - rbase) return 0;
    slot0 = (uint64_t)BODY;
    if (bt_off_ok(nbytes, rbase, slot0, (uint64_t)BT_SLOT) == 0) {
        return 0;
    }
    rcell = (uint64_t)bt_read16(heap, nbytes, rbase + slot0);
    if (bt_off_ok(nbytes, rbase, rcell, 4) == 0) {
        return 0;
    }
    cklen = (uint64_t)bt_read16(heap, nbytes, rbase + rcell);
    if (bt_off_ok(nbytes, rbase, rcell, (uint64_t)4 + cklen) == 0) {
        return 0;
    }

    cell_size = (uint64_t)4 + cklen + 8;
    free_lo = (uint64_t)BODY;
    free_hi = (uint64_t)PAGE_N;
    if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
        return 0;
    }
    need_lo = free_lo + (uint64_t)BT_SLOT;
    if (cell_size > free_hi) {
        return 0;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return 0;
    }
    if (bt_off_ok(nbytes, ibase, new_hi, cell_size) == 0) {
        return 0;
    }

    bt_write16(heap, nbytes, ibase + new_hi, (uint16_t)cklen);
    bt_write16(heap, nbytes, ibase + new_hi + 2, 0);
    bt_copy_bytes(heap, nbytes, ibase + new_hi + 4, heap, nbytes, rbase + rcell + 4, cklen);
    bt_write64(heap, nbytes, ibase + new_hi + 4 + cklen, right);

    islot = (uint64_t)BODY;
    if (bt_off_ok(nbytes, ibase, islot, (uint64_t)BT_SLOT) == 0) {
        return 0;
    }
    bt_write16(heap, nbytes, ibase + islot, (uint16_t)new_hi);
    bt_write16(heap, nbytes, ibase + NSLOTS, 1);
    bt_write16(heap, nbytes, ibase + FREE_LO, (uint16_t)need_lo);
    bt_write16(heap, nbytes, ibase + FREE_HI, (uint16_t)new_hi);
    return 1;
}

static uint8_t bt_internal_insert_kv(uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t intern, uint64_t left, uint64_t right, uint64_t koff, uint64_t cklen) {
    uint64_t ibase;
    uint64_t cell_size;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t need_lo;
    uint64_t nslots;
    uint64_t pos;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint16_t sklen;

    if (intern >= npages) return 0;
    if (right >= npages) return 0;
    if (left >= npages) return 0;
    if (intern == right) return 0;
    if (cklen == 0 || cklen > 0xFFFF) return 0;
    if (koff >= nbytes) return 0;
    if (cklen > nbytes - koff) return 0;

    ibase = intern * (uint64_t)PAGE_N;
    if (ibase >= nbytes) return 0;
    if ((uint64_t)PAGE_N > nbytes - ibase) return 0;
    if (bt_read8(heap, nbytes, ibase + TYPE) != (uint8_t)TYPE_INTERNAL) {
        return 0;
    }

    nslots = (uint64_t)bt_read16(heap, nbytes, ibase + NSLOTS);
    free_lo = (uint64_t)bt_read16(heap, nbytes, ibase + FREE_LO);
    free_hi = (uint64_t)bt_read16(heap, nbytes, ibase + FREE_HI);
    if (free_lo < (uint64_t)BODY) {
        return 0;
    }
    if (free_hi > (uint64_t)PAGE_N) {
        return 0;
    }
    if (free_lo > free_hi) {
        return 0;
    }

    cell_size = (uint64_t)4 + cklen + 8;
    if (free_lo >= (uint64_t)PAGE_N) {
        return 0;
    }
    if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
        return 0;
    }
    need_lo = free_lo + (uint64_t)BT_SLOT;
    if (cell_size > free_hi) {
        return 0;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return 0;
    }
    if (bt_off_ok(nbytes, ibase, new_hi, cell_size) == 0) {
        return 0;
    }

    pos = nslots;
    {
        uint64_t leftmost;
        uint64_t ch;
        uint8_t found_left;
        leftmost = bt_read64(heap, nbytes, ibase + HLC);
        found_left = 0;
        if (leftmost == left) {
            pos = 0;
            found_left = 1;
        } else {
            i = 0;
            while (i < nslots) {
                slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
                if (bt_off_ok(nbytes, ibase, slot_off, (uint64_t)BT_SLOT) == 0) {
                    return 0;
                }
                cell_off = (uint64_t)bt_read16(heap, nbytes, ibase + slot_off);
                if (bt_off_ok(nbytes, ibase, cell_off, 4) == 0) {
                    return 0;
                }
                sklen = bt_read16(heap, nbytes, ibase + cell_off);
                if (bt_off_ok(nbytes, ibase, cell_off, (uint64_t)4 + (uint64_t)sklen + 8) == 0) {
                    return 0;
                }
                ch = bt_read64(heap, nbytes, ibase + cell_off + 4 + (uint64_t)sklen);
                if (ch == left) {
                    pos = i + 1;
                    found_left = 1;
                    break;
                }
                i = i + 1;
            }
        }
        if (found_left == 0) {
            return 0;
        }
    }

    bt_write16(heap, nbytes, ibase + new_hi, (uint16_t)cklen);
    bt_write16(heap, nbytes, ibase + new_hi + 2, 0);
    bt_copy_bytes(heap, nbytes, ibase + new_hi + 4, heap, nbytes, koff, cklen);
    bt_write64(heap, nbytes, ibase + new_hi + 4 + cklen, right);

    bt_slot_insert(heap + intern * (uint64_t)PAGE_N, PAGE_N, pos, nslots, (uint16_t)new_hi);
    nslots = nslots + 1;
    bt_write16(heap, nbytes, ibase + NSLOTS, (uint16_t)nslots);
    bt_write16(heap, nbytes, ibase + FREE_LO, (uint16_t)need_lo);
    bt_write16(heap, nbytes, ibase + FREE_HI, (uint16_t)new_hi);
    return 1;
}

static uint8_t bt_internal_insert_buf(uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t intern, uint64_t left, uint64_t right, const uint8_t *key, uint64_t cklen) {
    uint64_t ibase;
    uint64_t cell_size;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t need_lo;
    uint64_t nslots;
    uint64_t pos;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint16_t sklen;

    if (intern >= npages) return 0;
    if (right >= npages) return 0;
    if (left >= npages) return 0;
    if (intern == right) return 0;
    if (key == 0) return 0;
    if (cklen == 0 || cklen > 0xFFFF) return 0;
    if (cklen > (uint64_t)BT_SPLIT_KEY_MAX) return 0;

    ibase = intern * (uint64_t)PAGE_N;
    if (ibase >= nbytes) return 0;
    if ((uint64_t)PAGE_N > nbytes - ibase) return 0;
    if (bt_read8(heap, nbytes, ibase + TYPE) != (uint8_t)TYPE_INTERNAL) {
        return 0;
    }

    nslots = (uint64_t)bt_read16(heap, nbytes, ibase + NSLOTS);
    free_lo = (uint64_t)bt_read16(heap, nbytes, ibase + FREE_LO);
    free_hi = (uint64_t)bt_read16(heap, nbytes, ibase + FREE_HI);
    if (free_lo < (uint64_t)BODY) {
        return 0;
    }
    if (free_hi > (uint64_t)PAGE_N) {
        return 0;
    }
    if (free_lo > free_hi) {
        return 0;
    }

    cell_size = (uint64_t)4 + cklen + 8;
    if (free_lo >= (uint64_t)PAGE_N) {
        return 0;
    }
    if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
        return 0;
    }
    need_lo = free_lo + (uint64_t)BT_SLOT;
    if (cell_size > free_hi) {
        return 0;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return 0;
    }
    if (bt_off_ok(nbytes, ibase, new_hi, cell_size) == 0) {
        return 0;
    }

    pos = nslots;
    {
        uint64_t leftmost;
        uint64_t ch;
        uint8_t found_left;
        leftmost = bt_read64(heap, nbytes, ibase + HLC);
        found_left = 0;
        if (leftmost == left) {
            pos = 0;
            found_left = 1;
        } else {
            i = 0;
            while (i < nslots) {
                slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
                if (bt_off_ok(nbytes, ibase, slot_off, (uint64_t)BT_SLOT) == 0) {
                    return 0;
                }
                cell_off = (uint64_t)bt_read16(heap, nbytes, ibase + slot_off);
                if (bt_off_ok(nbytes, ibase, cell_off, 4) == 0) {
                    return 0;
                }
                sklen = bt_read16(heap, nbytes, ibase + cell_off);
                if (bt_off_ok(nbytes, ibase, cell_off, (uint64_t)4 + (uint64_t)sklen + 8) == 0) {
                    return 0;
                }
                ch = bt_read64(heap, nbytes, ibase + cell_off + 4 + (uint64_t)sklen);
                if (ch == left) {
                    pos = i + 1;
                    found_left = 1;
                    break;
                }
                i = i + 1;
            }
        }
        if (found_left == 0) {
            return 0;
        }
    }

    bt_write16(heap, nbytes, ibase + new_hi, (uint16_t)cklen);
    bt_write16(heap, nbytes, ibase + new_hi + 2, 0);
    bt_copy_in(heap, nbytes, ibase + new_hi + 4, key, cklen);
    bt_write64(heap, nbytes, ibase + new_hi + 4 + cklen, right);

    bt_slot_insert(heap + intern * (uint64_t)PAGE_N, PAGE_N, pos, nslots, (uint16_t)new_hi);
    nslots = nslots + 1;
    bt_write16(heap, nbytes, ibase + NSLOTS, (uint16_t)nslots);
    bt_write16(heap, nbytes, ibase + FREE_LO, (uint16_t)need_lo);
    bt_write16(heap, nbytes, ibase + FREE_HI, (uint16_t)new_hi);
    return 1;
}

static uint8_t bt_internal_add_sep(uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t intern, uint64_t left, uint64_t right) {
    uint64_t rbase;
    uint64_t slot0;
    uint64_t rcell;
    uint64_t cklen;

    if (right >= npages) return 0;
    rbase = right * (uint64_t)PAGE_N;
    if (rbase >= nbytes) return 0;
    if ((uint64_t)PAGE_N > nbytes - rbase) return 0;
    slot0 = (uint64_t)BODY;
    if (bt_off_ok(nbytes, rbase, slot0, (uint64_t)BT_SLOT) == 0) {
        return 0;
    }
    rcell = (uint64_t)bt_read16(heap, nbytes, rbase + slot0);
    if (bt_off_ok(nbytes, rbase, rcell, 4) == 0) {
        return 0;
    }
    cklen = (uint64_t)bt_read16(heap, nbytes, rbase + rcell);
    if (bt_off_ok(nbytes, rbase, rcell, (uint64_t)4 + cklen) == 0) {
        return 0;
    }
    return bt_internal_insert_kv(heap, nbytes, npages, intern, left, right, rbase + rcell + 4, cklen);
}

static uint8_t bt_internal_has_child(const uint8_t *heap, uint64_t nbytes, uint64_t intern, uint64_t ch, uint64_t npages) {
    uint64_t ibase;
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint64_t child;
    uint16_t sklen;

    if (intern >= npages || ch >= npages) {
        return 0;
    }
    ibase = intern * (uint64_t)PAGE_N;
    if (bt_read8(heap, nbytes, ibase + TYPE) != (uint8_t)TYPE_INTERNAL) {
        return 0;
    }
    if (bt_read64(heap, nbytes, ibase + HLC) == ch) {
        return 1;
    }
    nslots = (uint64_t)bt_read16(heap, nbytes, ibase + NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(nbytes, ibase, slot_off, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        cell_off = (uint64_t)bt_read16(heap, nbytes, ibase + slot_off);
        if (bt_off_ok(nbytes, ibase, cell_off, 4) == 0) {
            return 0;
        }
        sklen = bt_read16(heap, nbytes, ibase + cell_off);
        if (bt_off_ok(nbytes, ibase, cell_off, (uint64_t)4 + (uint64_t)sklen + 8) == 0) {
            return 0;
        }
        child = bt_read64(heap, nbytes, ibase + cell_off + 4 + (uint64_t)sklen);
        if (child == ch) {
            return 1;
        }
        i = i + 1;
    }
    return 0;
}

static uint8_t bt_elevate_root(uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t src, uint64_t dst, uint64_t nroot, uint64_t left, uint64_t right) {
    uint64_t sbase;
    uint64_t dbase;
    uint64_t rbase;
    uint64_t nslots;
    uint64_t mid;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint64_t cklen;
    uint64_t cell_size;
    uint64_t prom_child;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t dslot;
    uint64_t dst_nslots;
    uint64_t parent;
    uint16_t sklen;

    if (src >= npages || dst >= npages || nroot >= npages) {
        return 0;
    }
    if (src == dst || src == nroot || dst == nroot) {
        return 0;
    }
    sbase = src * (uint64_t)PAGE_N;
    if (bt_read8(heap, nbytes, sbase + TYPE) != (uint8_t)TYPE_INTERNAL) {
        return 0;
    }
    nslots = (uint64_t)bt_read16(heap, nbytes, sbase + NSLOTS);
    mid = nslots / 2;
    if (mid == 0) {
        return 0;
    }
    if (mid + 1 >= nslots) {
        return 0;
    }
    slot_off = (uint64_t)BODY + mid * (uint64_t)BT_SLOT;
    if (bt_off_ok(nbytes, sbase, slot_off, (uint64_t)BT_SLOT) == 0) {
        return 0;
    }
    cell_off = (uint64_t)bt_read16(heap, nbytes, sbase + slot_off);
    if (bt_off_ok(nbytes, sbase, cell_off, 4) == 0) {
        return 0;
    }
    cklen = (uint64_t)bt_read16(heap, nbytes, sbase + cell_off);
    if (bt_off_ok(nbytes, sbase, cell_off, (uint64_t)4 + cklen + 8) == 0) {
        return 0;
    }
    prom_child = bt_read64(heap, nbytes, sbase + cell_off + 4 + cklen);

    dbase = dst * (uint64_t)PAGE_N;
    bt_write8(heap, nbytes, dbase + TYPE, (uint8_t)TYPE_INTERNAL);
    bt_write16(heap, nbytes, dbase + NSLOTS, 0);
    bt_write16(heap, nbytes, dbase + FREE_LO, (uint16_t)BODY);
    bt_write16(heap, nbytes, dbase + FREE_HI, (uint16_t)PAGE_N);
    bt_write64(heap, nbytes, dbase + HLC, prom_child);

    i = mid + 1;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(nbytes, sbase, slot_off, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        cell_off = (uint64_t)bt_read16(heap, nbytes, sbase + slot_off);
        if (bt_off_ok(nbytes, sbase, cell_off, 4) == 0) {
            return 0;
        }
        sklen = bt_read16(heap, nbytes, sbase + cell_off);
        cell_size = (uint64_t)4 + (uint64_t)sklen + 8;
        if (bt_off_ok(nbytes, sbase, cell_off, cell_size) == 0) {
            return 0;
        }
        dst_nslots = (uint64_t)bt_read16(heap, nbytes, dbase + NSLOTS);
        free_lo = (uint64_t)bt_read16(heap, nbytes, dbase + FREE_LO);
        free_hi = (uint64_t)bt_read16(heap, nbytes, dbase + FREE_HI);
        if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
            return 0;
        }
        if (cell_size > free_hi) {
            return 0;
        }
        new_hi = free_hi - cell_size;
        if (free_lo + (uint64_t)BT_SLOT > new_hi) {
            return 0;
        }
        bt_copy_bytes(heap, nbytes, dbase + new_hi, heap, nbytes, sbase + cell_off, cell_size);
        dslot = (uint64_t)BODY + dst_nslots * (uint64_t)BT_SLOT;
        bt_write16(heap, nbytes, dbase + dslot, (uint16_t)new_hi);
        bt_write16(heap, nbytes, dbase + NSLOTS, (uint16_t)(dst_nslots + 1));
        bt_write16(heap, nbytes, dbase + FREE_LO, (uint16_t)(free_lo + (uint64_t)BT_SLOT));
        bt_write16(heap, nbytes, dbase + FREE_HI, (uint16_t)new_hi);
        i = i + 1;
    }

    rbase = nroot * (uint64_t)PAGE_N;
    bt_write8(heap, nbytes, rbase + TYPE, (uint8_t)TYPE_INTERNAL);
    bt_write16(heap, nbytes, rbase + NSLOTS, 0);
    bt_write16(heap, nbytes, rbase + FREE_LO, (uint16_t)BODY);
    bt_write16(heap, nbytes, rbase + FREE_HI, (uint16_t)PAGE_N);
    bt_write64(heap, nbytes, rbase + HLC, src);
    cell_size = (uint64_t)4 + cklen + 8;
    free_lo = (uint64_t)BODY;
    free_hi = (uint64_t)PAGE_N;
    new_hi = free_hi - cell_size;
    if (free_lo + (uint64_t)BT_SLOT > new_hi) {
        return 0;
    }
    slot_off = (uint64_t)BODY + mid * (uint64_t)BT_SLOT;
    cell_off = (uint64_t)bt_read16(heap, nbytes, sbase + slot_off);
    bt_write16(heap, nbytes, rbase + new_hi, (uint16_t)cklen);
    bt_write16(heap, nbytes, rbase + new_hi + 2, 0);
    bt_copy_bytes(heap, nbytes, rbase + new_hi + 4, heap, nbytes, sbase + cell_off + 4, cklen);
    bt_write64(heap, nbytes, rbase + new_hi + 4 + cklen, dst);
    bt_write16(heap, nbytes, rbase + (uint64_t)BODY, (uint16_t)new_hi);
    bt_write16(heap, nbytes, rbase + NSLOTS, 1);
    bt_write16(heap, nbytes, rbase + FREE_LO, (uint16_t)(BODY + (uint64_t)BT_SLOT));
    bt_write16(heap, nbytes, rbase + FREE_HI, (uint16_t)new_hi);

    bt_write16(heap, nbytes, sbase + NSLOTS, (uint16_t)mid);
    if (bt_leaf_repack(heap + src * (uint64_t)PAGE_N, PAGE_N, 8) == 0) {
        return 0;
    }

    parent = src;
    if (bt_internal_has_child(heap, nbytes, dst, left, npages) != 0) {
        parent = dst;
    } else if (bt_internal_has_child(heap, nbytes, src, left, npages) == 0) {
        return 0;
    }
    return bt_internal_add_sep(heap, nbytes, npages, parent, left, right);
}

static uint64_t bt_split_internal(uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t src, uint64_t dst, uint8_t *keybuf) {
    uint64_t sbase;
    uint64_t dbase;
    uint64_t nslots;
    uint64_t mid;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint64_t cklen;
    uint64_t cell_size;
    uint64_t prom_child;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t dslot;
    uint64_t dst_nslots;
    uint16_t sklen;

    if (src >= npages || dst >= npages) {
        return 0;
    }
    if (src == dst || keybuf == 0) {
        return 0;
    }
    sbase = src * (uint64_t)PAGE_N;
    if (bt_read8(heap, nbytes, sbase + TYPE) != (uint8_t)TYPE_INTERNAL) {
        return 0;
    }
    nslots = (uint64_t)bt_read16(heap, nbytes, sbase + NSLOTS);
    mid = nslots / 2;
    if (mid == 0) {
        return 0;
    }
    if (mid + 1 >= nslots) {
        return 0;
    }
    slot_off = (uint64_t)BODY + mid * (uint64_t)BT_SLOT;
    if (bt_off_ok(nbytes, sbase, slot_off, (uint64_t)BT_SLOT) == 0) {
        return 0;
    }
    cell_off = (uint64_t)bt_read16(heap, nbytes, sbase + slot_off);
    if (bt_off_ok(nbytes, sbase, cell_off, 4) == 0) {
        return 0;
    }
    cklen = (uint64_t)bt_read16(heap, nbytes, sbase + cell_off);
    if (cklen == 0 || cklen > 0xFFFF || cklen > (uint64_t)BT_SPLIT_KEY_MAX) {
        return 0;
    }
    if (bt_off_ok(nbytes, sbase, cell_off, (uint64_t)4 + cklen + 8) == 0) {
        return 0;
    }
    prom_child = bt_read64(heap, nbytes, sbase + cell_off + 4 + cklen);
    bt_copy_out(heap, nbytes, sbase + cell_off + 4, keybuf, cklen);

    dbase = dst * (uint64_t)PAGE_N;
    bt_write8(heap, nbytes, dbase + TYPE, (uint8_t)TYPE_INTERNAL);
    bt_write16(heap, nbytes, dbase + NSLOTS, 0);
    bt_write16(heap, nbytes, dbase + FREE_LO, (uint16_t)BODY);
    bt_write16(heap, nbytes, dbase + FREE_HI, (uint16_t)PAGE_N);
    bt_write64(heap, nbytes, dbase + HLC, prom_child);

    i = mid + 1;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(nbytes, sbase, slot_off, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        cell_off = (uint64_t)bt_read16(heap, nbytes, sbase + slot_off);
        if (bt_off_ok(nbytes, sbase, cell_off, 4) == 0) {
            return 0;
        }
        sklen = bt_read16(heap, nbytes, sbase + cell_off);
        cell_size = (uint64_t)4 + (uint64_t)sklen + 8;
        if (bt_off_ok(nbytes, sbase, cell_off, cell_size) == 0) {
            return 0;
        }
        dst_nslots = (uint64_t)bt_read16(heap, nbytes, dbase + NSLOTS);
        free_lo = (uint64_t)bt_read16(heap, nbytes, dbase + FREE_LO);
        free_hi = (uint64_t)bt_read16(heap, nbytes, dbase + FREE_HI);
        if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
            return 0;
        }
        if (cell_size > free_hi) {
            return 0;
        }
        new_hi = free_hi - cell_size;
        if (free_lo + (uint64_t)BT_SLOT > new_hi) {
            return 0;
        }
        bt_copy_bytes(heap, nbytes, dbase + new_hi, heap, nbytes, sbase + cell_off, cell_size);
        dslot = (uint64_t)BODY + dst_nslots * (uint64_t)BT_SLOT;
        bt_write16(heap, nbytes, dbase + dslot, (uint16_t)new_hi);
        bt_write16(heap, nbytes, dbase + NSLOTS, (uint16_t)(dst_nslots + 1));
        bt_write16(heap, nbytes, dbase + FREE_LO, (uint16_t)(free_lo + (uint64_t)BT_SLOT));
        bt_write16(heap, nbytes, dbase + FREE_HI, (uint16_t)new_hi);
        i = i + 1;
    }

    bt_write16(heap, nbytes, sbase + NSLOTS, (uint16_t)mid);
    if (bt_leaf_repack(heap + src * (uint64_t)PAGE_N, PAGE_N, 8) == 0) {
        return 0;
    }
    return cklen;
}

static uint8_t bt_make_root_buf(uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t left, uint64_t right, uint64_t nroot, const uint8_t *key, uint64_t cklen) {
    uint64_t rbase;
    uint64_t cell_size;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t need_lo;

    if (nroot >= npages || left >= npages || right >= npages) return 0;
    if (nroot == left || nroot == right) return 0;
    if (key == 0 || cklen == 0 || cklen > 0xFFFF) return 0;
    if (cklen > (uint64_t)BT_SPLIT_KEY_MAX) return 0;
    rbase = nroot * (uint64_t)PAGE_N;
    if (rbase >= nbytes) return 0;
    if ((uint64_t)PAGE_N > nbytes - rbase) return 0;

    bt_write8(heap, nbytes, rbase + TYPE, (uint8_t)TYPE_INTERNAL);
    bt_write16(heap, nbytes, rbase + NSLOTS, 0);
    bt_write16(heap, nbytes, rbase + FREE_LO, (uint16_t)BODY);
    bt_write16(heap, nbytes, rbase + FREE_HI, (uint16_t)PAGE_N);
    bt_write64(heap, nbytes, rbase + HLC, left);

    cell_size = (uint64_t)4 + cklen + 8;
    free_lo = (uint64_t)BODY;
    free_hi = (uint64_t)PAGE_N;
    if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
        return 0;
    }
    need_lo = free_lo + (uint64_t)BT_SLOT;
    if (cell_size > free_hi) {
        return 0;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return 0;
    }
    if (bt_off_ok(nbytes, rbase, new_hi, cell_size) == 0) {
        return 0;
    }
    bt_write16(heap, nbytes, rbase + new_hi, (uint16_t)cklen);
    bt_write16(heap, nbytes, rbase + new_hi + 2, 0);
    bt_copy_in(heap, nbytes, rbase + new_hi + 4, key, cklen);
    bt_write64(heap, nbytes, rbase + new_hi + 4 + cklen, right);
    bt_write16(heap, nbytes, rbase + (uint64_t)BODY, (uint16_t)new_hi);
    bt_write16(heap, nbytes, rbase + NSLOTS, 1);
    bt_write16(heap, nbytes, rbase + FREE_LO, (uint16_t)need_lo);
    bt_write16(heap, nbytes, rbase + FREE_HI, (uint16_t)new_hi);
    return 1;
}

static db_bt_heap_st_t bt_split_attach_deep(const uint8_t *pub, uint8_t *dirty, uint64_t nbytes, uint64_t npages, const uint64_t *anc, uint64_t level, uint64_t root, uint64_t used, uint64_t mask, uint64_t src, uint64_t dst, uint64_t left, uint64_t right) {
    db_bt_heap_st_t out;
    uint8_t keyA[16384];
    uint8_t keyB[16384];
    uint64_t clen;
    uint64_t nlen;
    uint64_t pl;
    uint64_t pr;
    uint64_t lvl;
    uint64_t P;
    uint64_t P2;
    uint64_t target;
    uint64_t nroot;

    out.root = 0;
    out.used = 0;
    out.nkeys = 0;
    out.pages = 0;
    out.ok = 0;
    out.full = 0;

    if (anc == 0 || level == 0 || level >= (uint64_t)BT_PATH_MAX) {
        out.full = 1;
        return out;
    }
    if (pub == 0 || dirty == 0) {
        return out;
    }

    clen = bt_split_internal(dirty, nbytes, npages, src, dst, keyA);
    if (clen == 0) {
        return out;
    }

    target = src;
    if (bt_internal_has_child(dirty, nbytes, dst, left, npages) != 0) {
        target = dst;
    } else if (bt_internal_has_child(dirty, nbytes, src, left, npages) == 0) {
        return out;
    }
    if (bt_internal_add_sep(dirty, nbytes, npages, target, left, right) == 0) {
        return out;
    }

    pl = src;
    pr = dst;
    lvl = level;

    while (lvl > 0) {
        P = anc[lvl - 1];
        if (P >= npages) {
            return out;
        }
        if (bt_internal_insert_buf(dirty, nbytes, npages, P, pl, pr, keyA, clen) != 0) {
            out.root = root;
            out.used = used;
            out.nkeys = 0;
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        if (used + 1 > npages || used + 1 < used) {
            out.full = 1;
            return out;
        }
        P2 = used;
        mask = bt_cow_page(pub, dirty, nbytes, P2, mask);
        nlen = bt_split_internal(dirty, nbytes, npages, P, P2, keyB);
        if (nlen == 0) {
            return out;
        }
        target = P;
        if (bt_internal_has_child(dirty, nbytes, P2, pl, npages) != 0) {
            target = P2;
        } else if (bt_internal_has_child(dirty, nbytes, P, pl, npages) == 0) {
            return out;
        }
        if (bt_internal_insert_buf(dirty, nbytes, npages, target, pl, pr, keyA, clen) == 0) {
            return out;
        }
        used = used + 1;
        pl = P;
        pr = P2;
        bt_copy_bytes(keyA, (uint64_t)BT_SPLIT_KEY_MAX, 0, keyB, (uint64_t)BT_SPLIT_KEY_MAX, 0, nlen);
        clen = nlen;
        lvl = lvl - 1;
    }

    if (used + 1 > npages || used + 1 < used) {
        out.full = 1;
        return out;
    }
    nroot = used;
    mask = bt_cow_page(pub, dirty, nbytes, nroot, mask);
    if (bt_make_root_buf(dirty, nbytes, npages, pl, pr, nroot, keyA, clen) == 0) {
        return out;
    }
    out.root = nroot;
    out.used = used + 1;
    out.nkeys = 0;
    out.pages = mask;
    out.ok = 1;
    return out;
}

static uint64_t bt_heap_nkeys(const uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t root) {
    uint64_t page;
    uint64_t walked;
    uint64_t next;
    uint64_t total;
    uint64_t base;
    uint8_t typ;

    total = 0;
    page = root;
    if (page >= npages) {
        return total;
    }
    base = page * (uint64_t)PAGE_N;
    typ = bt_read8(heap, nbytes, base + TYPE);
    if (typ == (uint8_t)TYPE_INTERNAL) {
        page = bt_read64(heap, nbytes, base + HLC);
    }
    walked = 0;
    while (walked < npages) {
        if (page >= npages) {
            return total;
        }
        base = page * (uint64_t)PAGE_N;
        typ = bt_read8(heap, nbytes, base + TYPE);
        if (typ == (uint8_t)TYPE_LEAF) {
            total = total + (uint64_t)bt_read16(heap, nbytes, base + NSLOTS);
        }
        next = bt_read64(heap, nbytes, base + HLC);
        if (next == 0) {
            return total;
        }
        if (next == page) {
            return total;
        }
        page = next;
        walked = walked + 1;
    }
    return total;
}

db_bt_get_t db_bt_get_heap(const uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t root, const uint8_t *key, uint64_t klen, uint8_t *out, uint64_t outn) {
    db_bt_get_t g;
    uint64_t page;
    uint64_t walked;
    uint64_t next;
    uint64_t base;
    uint64_t chosen;
    uint8_t typ;

    g.len = 0;
    g.walked = 0;
    g.found = 0;
    g.fallback = 0;

    if (heap == 0 || npages == 0) {
        return g;
    }
    if (nbytes / (uint64_t)PAGE_N != npages) {
        return g;
    }
    if (npages * (uint64_t)PAGE_N != nbytes) {
        return g;
    }

    page = root;
    walked = 0;
    if (page >= npages) {
        return g;
    }
    base = page * (uint64_t)PAGE_N;
    typ = bt_read8(heap, nbytes, base + TYPE);
    while (typ == (uint8_t)TYPE_INTERNAL && walked < npages) {
        chosen = bt_internal_child(heap, nbytes, base, key, klen);
        if (chosen >= npages) {
            g.walked = walked + 1;
            return g;
        }
        if (chosen == page) {
            g.walked = walked + 1;
            return g;
        }
        page = chosen;
        walked = walked + 1;
        base = page * (uint64_t)PAGE_N;
        typ = bt_read8(heap, nbytes, base + TYPE);
    }
    if (typ == (uint8_t)TYPE_INTERNAL) {
        g.walked = walked;
        return g;
    }
    if (walked != 0) {
        g = bt_get_at(heap, nbytes, base, key, klen, out, outn);
        g.walked = walked + 1;
        g.fallback = 0;
        return g;
    }
    while (walked < npages) {
        if (page >= npages) {
            g.walked = walked;
            return g;
        }
        base = page * (uint64_t)PAGE_N;
        g = bt_get_at(heap, nbytes, base, key, klen, out, outn);
        if (g.found != 0) {
            g.walked = walked + 1;
            g.fallback = 0;
            return g;
        }
        next = bt_read64(heap, nbytes, base + HLC);
        if (next == 0) {
            g.walked = walked + 1;
            g.fallback = 0;
            return g;
        }
        if (next == page) {
            g.walked = walked + 1;
            g.fallback = 0;
            return g;
        }
        page = next;
        walked = walked + 1;
    }
    g.walked = walked;
    g.fallback = 0;
    return g;
}

db_bt_heap_st_t db_bt_insert_heap(const uint8_t *pub, uint8_t *dirty, uint64_t nbytes, uint64_t npages, db_bt_heap_st_t st, const uint8_t *key, uint64_t klen, const uint8_t *val, uint64_t vlen) {
    db_bt_heap_st_t out;
    db_bt_state_t ins;
    db_bt_get_t g;
    uint64_t page;
    uint64_t walked;
    uint64_t next;
    uint64_t used;
    uint64_t last;
    uint64_t base;
    uint64_t intern;
    uint64_t rcell;
    uint64_t rcklen;
    uint64_t mask;
    uint64_t parent;
    uint64_t chosen;
    uint8_t typ;
    int32_t kcmp;
    uint64_t anc[BT_PATH_MAX];
    uint64_t level;
    db_bt_heap_st_t grown;

    out.root = 0;
    out.used = 0;
    out.nkeys = 0;
    out.pages = 0;
    out.ok = 0;
    out.full = 0;
    mask = 0;

    if (pub == 0 || dirty == 0) {
        return out;
    }
    if (npages == 0) {
        return out;
    }
    if (nbytes / (uint64_t)PAGE_N != npages) {
        return out;
    }
    if (npages * (uint64_t)PAGE_N != nbytes) {
        return out;
    }

    if (klen == 0 || klen > 0xFFFF || vlen > 0xFFFF) {
        return out;
    }
    if (key == 0) {
        return out;
    }
    if (vlen > 0 && val == 0) {
        return out;
    }

    used = st.used;
    if (used == 0) {
        used = 1;
    }
    if (st.root >= npages) {
        return out;
    }

    page = st.root;
    parent = st.root;
    mask = bt_cow_page(pub, dirty, nbytes, page, mask);
    base = page * (uint64_t)PAGE_N;
    typ = bt_read8(dirty, nbytes, base + TYPE);
    if (typ == (uint8_t)TYPE_INTERNAL) {
        anc[0] = page;
        level = 1;
        walked = 0;
        while (typ == (uint8_t)TYPE_INTERNAL && walked < npages) {
            parent = page;
            chosen = bt_internal_child(dirty, nbytes, base, key, klen);
            if (chosen >= npages) {
                return out;
            }
            if (chosen == page) {
                return out;
            }
            page = chosen;
            mask = bt_cow_page(pub, dirty, nbytes, page, mask);
            base = page * (uint64_t)PAGE_N;
            typ = bt_read8(dirty, nbytes, base + TYPE);
            walked = walked + 1;
            if (typ == (uint8_t)TYPE_INTERNAL) {
                if (level >= (uint64_t)BT_PATH_MAX) {
                    return out;
                }
                anc[level] = page;
                level = level + 1;
            }
        }
        if (typ == (uint8_t)TYPE_INTERNAL) {
            return out;
        }
        g = bt_get_at(dirty, nbytes, base, key, klen, 0, 0);
        if (g.found != 0) {
            ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
            if (ins.ok == 0) {
                return out;
            }
            out.root = st.root;
            out.used = used;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
        if (ins.ok != 0) {
            out.root = st.root;
            out.used = used;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        if (used >= npages) {
            return out;
        }
        mask = bt_cow_page(pub, dirty, nbytes, used, mask);
        if (bt_chain_split(dirty, npages, page, used) == 0) {
            return out;
        }
        base = used * (uint64_t)PAGE_N;
        rcell = (uint64_t)bt_read16(dirty, nbytes, base + (uint64_t)BODY);
        if (bt_off_ok(nbytes, base, rcell, 4) == 0) {
            return out;
        }
        rcklen = (uint64_t)bt_read16(dirty, nbytes, base + rcell);
        kcmp = bt_keycmp_at(dirty, nbytes, base + rcell + 4, rcklen, key, klen);
        if (kcmp > 0) {
            base = page * (uint64_t)PAGE_N;
            ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
        } else {
            mask = bt_cow_page(pub, dirty, nbytes, used, mask);
            base = used * (uint64_t)PAGE_N;
            ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
        }
        if (ins.ok == 0) {
            return out;
        }
        if (bt_internal_add_sep(dirty, nbytes, npages, parent, page, used) != 0) {
            out.root = st.root;
            out.used = used + 1;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        intern = used + 1;
        if (parent == st.root) {
            if (intern + 1 >= npages) {
                out.full = 1;
                return out;
            }
            mask = bt_cow_page(pub, dirty, nbytes, intern, mask);
            mask = bt_cow_page(pub, dirty, nbytes, intern + 1, mask);
            if (bt_elevate_root(dirty, nbytes, npages, st.root, intern, intern + 1, page, used) == 0) {
                return out;
            }
            out.root = intern + 1;
            out.used = used + 3;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, intern + 1);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        if (intern >= npages) {
            out.full = 1;
            return out;
        }
        mask = bt_cow_page(pub, dirty, nbytes, intern, mask);
        grown = bt_split_attach_deep(pub, dirty, nbytes, npages, anc, level - 1, st.root, used + 2, mask, parent, intern, page, used);
        if (grown.ok == 0) {
            out.full = grown.full;
            return out;
        }
        out.root = grown.root;
        out.used = grown.used;
        out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, grown.root);
        out.pages = grown.pages;
        out.ok = 1;
        return out;
    }

    page = st.root;
    walked = 0;
    last = page;
    while (walked < npages) {
        if (page >= npages) {
            return out;
        }
        mask = bt_cow_page(pub, dirty, nbytes, page, mask);
        base = page * (uint64_t)PAGE_N;
        g = bt_get_at(dirty, nbytes, base, key, klen, 0, 0);
        if (g.found != 0) {
            ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
            if (ins.ok == 0) {
                return out;
            }
            out.root = st.root;
            out.used = used;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        last = page;
        next = bt_read64(dirty, nbytes, base + HLC);
        if (next == 0) {
            break;
        }
        if (next == page) {
            break;
        }
        page = next;
        walked = walked + 1;
    }

    page = st.root;
    walked = 0;
    while (walked < npages) {
        if (page >= npages) {
            break;
        }
        mask = bt_cow_page(pub, dirty, nbytes, page, mask);
        base = page * (uint64_t)PAGE_N;
        ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
        if (ins.ok != 0) {
            out.root = st.root;
            out.used = used;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        last = page;
        next = bt_read64(dirty, nbytes, base + HLC);
        if (next == 0) {
            break;
        }
        if (next == page) {
            break;
        }
        page = next;
        walked = walked + 1;
    }

    if (last != st.root) {
        return out;
    }
    if (used >= npages) {
        return out;
    }
    if (npages > 2 && (used + 2 > npages || used + 2 < used)) {
        return out;
    }
    mask = bt_cow_page(pub, dirty, nbytes, used, mask);
    if (bt_chain_split(dirty, npages, last, used) == 0) {
        return out;
    }

    base = used * (uint64_t)PAGE_N;
    rcell = (uint64_t)bt_read16(dirty, nbytes, base + (uint64_t)BODY);
    if (bt_off_ok(nbytes, base, rcell, 4) == 0) {
        return out;
    }
    rcklen = (uint64_t)bt_read16(dirty, nbytes, base + rcell);
    kcmp = bt_keycmp_at(dirty, nbytes, base + rcell + 4, rcklen, key, klen);
    if (kcmp > 0) {
        base = last * (uint64_t)PAGE_N;
        ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
    } else {
        base = used * (uint64_t)PAGE_N;
        ins = bt_insert_at(dirty, nbytes, base, key, klen, val, vlen);
    }
    if (ins.ok == 0) {
        return out;
    }

    intern = used + 1;
    if (intern < npages) {
        mask = bt_cow_page(pub, dirty, nbytes, intern, mask);
        if (bt_make_internal(dirty, nbytes, npages, st.root, used, intern) == 0) {
            return out;
        }
        out.root = intern;
        out.used = used + 2;
        out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, intern);
        out.pages = mask;
        out.ok = 1;
        return out;
    }
    if (npages == 2) {
        out.root = st.root;
        out.used = used + 1;
        out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
        out.pages = mask;
        out.ok = 1;
        return out;
    }
    return out;
}

db_bt_state_t db_bt_insert_ver(const uint8_t *pub, uint8_t *dirty, uint64_t n, const uint8_t *key, uint64_t klen, const uint8_t *id16, const uint8_t *val, uint64_t vlen) {
    db_bt_state_t out;
    uint64_t nslots;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t cell_size;
    uint64_t need_lo;
    uint64_t new_hi;
    uint64_t cell_off;
    uint64_t pos;
    uint8_t typ;

    out.nkeys = 0;
    out.ok = 0;
    out.copied = 0;

    if (pub == 0 || dirty == 0 || n != PAGE_N) {
        return out;
    }

    bt_cow(pub, dirty, n);
    out.copied = 1;

    if (klen == 0 || klen > 0xFFFF || vlen > 0xFFFF) {
        return out;
    }
    if (key == 0 || id16 == 0) {
        return out;
    }
    if (vlen > 0 && val == 0) {
        return out;
    }

    typ = bt_read8(dirty, n, TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return out;
    }

    nslots = (uint64_t)bt_read16(dirty, n, NSLOTS);
    free_lo = (uint64_t)bt_read16(dirty, n, FREE_LO);
    free_hi = (uint64_t)bt_read16(dirty, n, FREE_HI);
    out.nkeys = nslots;

    if (free_lo < (uint64_t)BODY) {
        return out;
    }
    if (free_hi > n) {
        return out;
    }
    if (free_lo > free_hi) {
        return out;
    }

    cell_size = (uint64_t)4 + klen + (uint64_t)BT_IDLEN + vlen;
    if (free_lo >= n) {
        return out;
    }
    if ((uint64_t)BT_SLOT > n - free_lo) {
        return out;
    }
    need_lo = free_lo + (uint64_t)BT_SLOT;

    if (cell_size > free_hi) {
        return out;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return out;
    }

    cell_off = new_hi;
    if (bt_bounds(dirty, n, cell_off, cell_size) == 0) {
        return out;
    }

    pos = bt_find_ver_pos(dirty, n, key, klen, id16);
    if (pos > nslots) {
        pos = nslots;
    }

    bt_write16(dirty, n, cell_off, (uint16_t)klen);
    bt_write16(dirty, n, cell_off + 2, (uint16_t)vlen);
    bt_copy_in(dirty, n, cell_off + 4, key, klen);
    bt_copy_in(dirty, n, cell_off + 4 + klen, id16, (uint64_t)BT_IDLEN);
    if (vlen > 0) {
        bt_copy_in(dirty, n, cell_off + 4 + klen + (uint64_t)BT_IDLEN, val, vlen);
    }

    bt_slot_insert(dirty, n, pos, nslots, (uint16_t)cell_off);
    nslots = nslots + 1;
    bt_write16(dirty, n, NSLOTS, (uint16_t)nslots);
    bt_write16(dirty, n, FREE_LO, (uint16_t)need_lo);
    bt_write16(dirty, n, FREE_HI, (uint16_t)new_hi);

    out.nkeys = nslots;
    out.ok = 1;
    return out;
}

db_bt_get_t db_bt_get_as_of(const uint8_t *page, uint64_t n, const uint8_t *key, uint64_t klen, const uint8_t *snap16, uint8_t *out, uint64_t outn) {
    db_bt_get_t g;
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint64_t best_off;
    uint64_t best_id;
    uint64_t copy_n;
    uint16_t cklen;
    uint16_t cvlen;
    uint8_t typ;
    uint8_t have;
    int32_t kcmp;
    int32_t icmp;

    g.len = 0;
    g.walked = 0;
    g.found = 0;
    g.fallback = 0;
    best_off = 0;
    best_id = 0;
    have = 0;

    if (page == 0 || n != PAGE_N) {
        return g;
    }
    if (key == 0 || snap16 == 0 || klen == 0 || klen > 0xFFFF) {
        return g;
    }

    typ = bt_read8(page, n, TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return g;
    }

    nslots = (uint64_t)bt_read16(page, n, NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_bounds(page, n, slot_addr, (uint64_t)BT_SLOT) == 0) {
            return g;
        }
        cell_off = (uint64_t)bt_read16(page, n, slot_addr);
        if (bt_bounds(page, n, cell_off, 4) == 0) {
            return g;
        }
        cklen = bt_read16(page, n, cell_off);
        cvlen = bt_read16(page, n, cell_off + 2);
        if (bt_bounds(page, n, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)BT_IDLEN + (uint64_t)cvlen) == 0) {
            return g;
        }
        kcmp = bt_keycmp_at(page, n, cell_off + 4, (uint64_t)cklen, key, klen);
        if (kcmp == 0) {
            icmp = bt_keycmp_at(page, n, cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, snap16, (uint64_t)BT_IDLEN);
            if (icmp <= 0) {
                if (have == 0) {
                    best_off = cell_off;
                    best_id = cell_off + 4 + (uint64_t)cklen;
                    have = 1;
                } else {
                    icmp = bt_keycmp_pp(page, n, cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, best_id, (uint64_t)BT_IDLEN);
                    if (icmp > 0) {
                        best_off = cell_off;
                        best_id = cell_off + 4 + (uint64_t)cklen;
                    }
                }
            }
        }
        i = i + 1;
    }

    if (have == 0) {
        return g;
    }

    cklen = bt_read16(page, n, best_off);
    cvlen = bt_read16(page, n, best_off + 2);
    g.len = (uint64_t)cvlen;
    g.found = 1;
    copy_n = (uint64_t)cvlen;
    if (copy_n > outn) {
        copy_n = outn;
    }
    if (out != 0 && copy_n > 0) {
        bt_copy_out(page, n, best_off + 4 + (uint64_t)cklen + (uint64_t)BT_IDLEN, out, copy_n);
    }
    return g;
}

db_bt_state_t db_bt_gc_before(const uint8_t *pub, uint8_t *dirty, uint64_t n, const uint8_t *min_id16) {
    db_bt_state_t out;
    uint64_t nslots;
    uint64_t i;
    uint64_t w;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint16_t cklen;
    uint16_t off;
    uint8_t typ;
    int32_t cmp;

    out.nkeys = 0;
    out.ok = 0;
    out.copied = 0;

    if (pub == 0 || dirty == 0 || n != PAGE_N) {
        return out;
    }
    if (min_id16 == 0) {
        return out;
    }

    bt_cow(pub, dirty, n);
    out.copied = 1;

    typ = bt_read8(dirty, n, TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return out;
    }

    nslots = (uint64_t)bt_read16(dirty, n, NSLOTS);
    w = 0;
    i = 0;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_bounds(dirty, n, slot_addr, (uint64_t)BT_SLOT) == 0) {
            return out;
        }
        off = bt_read16(dirty, n, slot_addr);
        cell_off = (uint64_t)off;
        if (bt_bounds(dirty, n, cell_off, 4) == 0) {
            return out;
        }
        cklen = bt_read16(dirty, n, cell_off);
        if (bt_bounds(dirty, n, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)BT_IDLEN) == 0) {
            return out;
        }
        cmp = bt_keycmp_at(dirty, n, cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, min_id16, (uint64_t)BT_IDLEN);
        if (cmp >= 0) {
            if (w != i) {
                bt_write16(dirty, n, (uint64_t)BODY + w * (uint64_t)BT_SLOT, off);
            }
            w = w + 1;
        }
        i = i + 1;
    }
    bt_write16(dirty, n, NSLOTS, (uint16_t)w);
    bt_write16(dirty, n, FREE_LO, (uint16_t)((uint64_t)BODY + w * (uint64_t)BT_SLOT));
    out.nkeys = w;
    out.ok = 1;
    return out;
}

static uint8_t bt_keyhas_prefix(const uint8_t *page, uint64_t n, uint64_t koff, uint64_t klen, const uint8_t *prefix, uint64_t plen) {
    uint64_t i;
    uint64_t addr;

    if (plen == 0) {
        return 1;
    }
    if (page == 0 || prefix == 0) {
        return 0;
    }
    if (klen < plen) {
        return 0;
    }
    i = 0;
    while (i < plen) {
        addr = koff + i;
        if (addr < n && (uint64_t)1 <= n - addr) {
            if (page[addr] != prefix[i]) {
                return 0;
            }
        } else {
            return 0;
        }
        i = i + 1;
    }
    return 1;
}

static db_bt_scan_t bt_scan_prefix_at(const uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *prefix, uint64_t plen, uint8_t *out, uint64_t outn, uint64_t already) {
    db_bt_scan_t r;
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint64_t koff;
    uint64_t woff;
    uint64_t cklen64;
    uint64_t packed;
    uint16_t cklen;
    uint8_t typ;

    r.nfound = 0;
    r.ok = 0;

    if (heap == 0) {
        return r;
    }
    if (plen > 0 && prefix == 0) {
        return r;
    }

    typ = bt_read8(heap, n, base + TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return r;
    }

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
            return r;
        }
        cell_off = (uint64_t)bt_read16(heap, n, base + slot_off);
        if (bt_off_ok(n, base, cell_off, 4) == 0) {
            return r;
        }
        cklen = bt_read16(heap, n, base + cell_off);
        cklen64 = (uint64_t)cklen;
        if (cklen64 > (uint64_t)PAGE_N) {
            return r;
        }
        if ((uint64_t)4 > (uint64_t)PAGE_N - cklen64) {
            return r;
        }
        if (bt_off_ok(n, base, cell_off, (uint64_t)4 + cklen64) == 0) {
            return r;
        }
        if (cell_off >= (uint64_t)PAGE_N) {
            return r;
        }
        if ((uint64_t)4 > (uint64_t)PAGE_N - cell_off) {
            return r;
        }
        koff = cell_off + 4;
        if (base >= n) {
            return r;
        }
        if (koff > n - base) {
            return r;
        }
        if (bt_keyhas_prefix(heap, n, base + koff, cklen64, prefix, plen) != 0) {
            if (already > (uint64_t)0x7FFFFFFFFFFFFFFF - r.nfound) {
                return r;
            }
            packed = already + r.nfound;
            if (packed > (uint64_t)0x7FFFFFFFFFFFFFFF) {
                return r;
            }
            woff = packed * 4;
            if (woff >= outn || (uint64_t)4 > outn - woff) {
                return r;
            }
            if (out != 0) {
                bt_write32(out, outn, woff, (uint32_t)(base + cell_off));
            }
            r.nfound = r.nfound + 1;
        }
        i = i + 1;
    }
    r.ok = 1;
    return r;
}

db_bt_scan_t db_bt_scan_prefix(const uint8_t *page, uint64_t n, const uint8_t *prefix, uint64_t plen, uint8_t *out, uint64_t outn) {
    db_bt_scan_t r;

    r.nfound = 0;
    r.ok = 0;
    if (page == 0 || n != PAGE_N) {
        return r;
    }
    return bt_scan_prefix_at(page, n, 0, prefix, plen, out, outn, 0);
}

db_bt_scan_t db_bt_scan_prefix_heap(const uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t root, const uint8_t *prefix, uint64_t plen, uint8_t *out, uint64_t outn) {
    db_bt_scan_t r;
    db_bt_scan_t one;
    uint64_t page;
    uint64_t walked;
    uint64_t next;
    uint64_t base;
    uint8_t typ;

    r.nfound = 0;
    r.ok = 0;

    if (heap == 0 || npages == 0) {
        return r;
    }
    if (nbytes / (uint64_t)PAGE_N != npages) {
        return r;
    }
    if (npages * (uint64_t)PAGE_N != nbytes) {
        return r;
    }

    page = root;
    if (page >= npages) {
        return r;
    }
    base = page * (uint64_t)PAGE_N;
    typ = bt_read8(heap, nbytes, base + TYPE);
    if (typ == (uint8_t)TYPE_INTERNAL) {
        page = bt_read64(heap, nbytes, base + HLC);
    }
    walked = 0;
    while (walked < npages) {
        if (page >= npages) {
            return r;
        }
        base = page * (uint64_t)PAGE_N;
        typ = bt_read8(heap, nbytes, base + TYPE);
        if (typ == (uint8_t)TYPE_LEAF) {
            one = bt_scan_prefix_at(heap, nbytes, base, prefix, plen, out, outn, r.nfound);
            if (one.ok == 0) {
                return r;
            }
            r.nfound = r.nfound + one.nfound;
            next = bt_read64(heap, nbytes, base + HLC);
            if (next == 0 || next == page || next >= npages) {
                r.ok = 1;
                return r;
            }
        } else if (typ == (uint8_t)TYPE_INTERNAL) {
            next = bt_read64(heap, nbytes, base + HLC);
            if (next >= npages || next == page) {
                return r;
            }
        } else {
            return r;
        }
        page = next;
        walked = walked + 1;
    }
    r.ok = 1;
    return r;
}

static uint8_t bt_chain_split_ver(uint8_t *heap, uint64_t npages, uint64_t src, uint64_t dst) {
    uint8_t *sp;
    uint8_t *dp;
    uint64_t nslots;
    uint64_t mid;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint64_t cklen;
    uint64_t cvlen;
    uint64_t cell_size;
    uint64_t dst_nslots;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t new_hi;
    uint64_t dslot;
    uint64_t min_hi;
    uint64_t old_next;
    db_bt_state_t initst;

    if (src >= npages) return 0;
    if (dst >= npages) return 0;
    if (src == dst) return 0;

    sp = heap + src * (uint64_t)PAGE_N;
    dp = heap + dst * (uint64_t)PAGE_N;

    initst = db_bt_leaf_init(dp, PAGE_N);
    if (initst.ok == 0) {
        return 0;
    }

    nslots = (uint64_t)bt_read16(sp, PAGE_N, NSLOTS);
    mid = nslots / 2;
    if (mid == 0) return 0;
    if (mid >= nslots) return 0;

    uint64_t dst_bytes = 0;
    for (i = mid; i < nslots; i++) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        if (cell_off >= PAGE_N || cell_off < BODY) return 0;
        cklen = (uint64_t)bt_read16(sp, PAGE_N, cell_off);
        cvlen = (uint64_t)bt_read16(sp, PAGE_N, cell_off + 2);
        dst_bytes += (4 + cklen + (uint64_t)BT_IDLEN + cvlen + 2);
    }
    while (mid < nslots - 1 && (dst_bytes + 2500 > 16320)) {
        slot_addr = (uint64_t)BODY + mid * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        cklen = (uint64_t)bt_read16(sp, PAGE_N, cell_off);
        cvlen = (uint64_t)bt_read16(sp, PAGE_N, cell_off + 2);
        dst_bytes -= (4 + cklen + (uint64_t)BT_IDLEN + cvlen + 2);
        mid++;
    }

    uint64_t init_mid = mid;
    while (mid < nslots) {
        uint64_t sa1 = (uint64_t)BODY + (mid - 1) * (uint64_t)BT_SLOT;
        uint64_t sa2 = (uint64_t)BODY + mid * (uint64_t)BT_SLOT;
        uint64_t co1 = (uint64_t)bt_read16(sp, PAGE_N, sa1);
        uint64_t co2 = (uint64_t)bt_read16(sp, PAGE_N, sa2);
        uint64_t l1 = (uint64_t)bt_read16(sp, PAGE_N, co1);
        uint64_t l2 = (uint64_t)bt_read16(sp, PAGE_N, co2);
        if (l1 != l2 || bt_keycmp_at(sp, PAGE_N, co1 + 4, l1, sp + co2 + 4, l2) != 0) {
            break;
        }
        mid = mid + 1;
    }
    if (mid >= nslots) {
        mid = init_mid;
        while (mid > 1) {
            uint64_t sa1 = (uint64_t)BODY + (mid - 1) * (uint64_t)BT_SLOT;
            uint64_t co1 = (uint64_t)bt_read16(sp, PAGE_N, sa1);
            uint64_t l1 = (uint64_t)bt_read16(sp, PAGE_N, co1);
            uint64_t v1 = (uint64_t)bt_read16(sp, PAGE_N, co1 + 2);
            uint64_t sz = 4 + l1 + (uint64_t)BT_IDLEN + v1 + 2;
            if (dst_bytes + sz + 2500 > 16320) {
                return 0;
            }
            dst_bytes += sz;
            uint64_t sa2 = (uint64_t)BODY + mid * (uint64_t)BT_SLOT;
            uint64_t co2 = (uint64_t)bt_read16(sp, PAGE_N, sa2);
            uint64_t l2 = (uint64_t)bt_read16(sp, PAGE_N, co2);
            if (l1 != l2 || bt_keycmp_at(sp, PAGE_N, co1 + 4, l1, sp + co2 + 4, l2) != 0) {
                break;
            }
            mid = mid - 1;
        }
    }
    if (mid <= 1 || mid >= nslots) {
        return 0;
    }

    i = mid;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        if (bt_bounds(sp, PAGE_N, cell_off, 4) == 0) {
            return 0;
        }
        cklen = (uint64_t)bt_read16(sp, PAGE_N, cell_off);
        cvlen = (uint64_t)bt_read16(sp, PAGE_N, cell_off + 2);
        cell_size = (uint64_t)4 + cklen + (uint64_t)BT_IDLEN + cvlen;
        if (bt_bounds(sp, PAGE_N, cell_off, cell_size) == 0) {
            return 0;
        }
        dst_nslots = (uint64_t)bt_read16(dp, PAGE_N, NSLOTS);
        free_lo = (uint64_t)bt_read16(dp, PAGE_N, FREE_LO);
        free_hi = (uint64_t)bt_read16(dp, PAGE_N, FREE_HI);
        if (free_lo >= (uint64_t)PAGE_N) {
            return 0;
        }
        if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
            return 0;
        }
        if (cell_size > free_hi) {
            return 0;
        }
        new_hi = free_hi - cell_size;
        if (free_lo + (uint64_t)BT_SLOT > new_hi) {
            return 0;
        }
        if (bt_bounds(dp, PAGE_N, new_hi, cell_size) == 0) {
            return 0;
        }
        bt_copy_bytes(dp, PAGE_N, new_hi, sp, PAGE_N, cell_off, cell_size);
        dslot = (uint64_t)BODY + dst_nslots * (uint64_t)BT_SLOT;
        if (bt_bounds(dp, PAGE_N, dslot, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        bt_write16(dp, PAGE_N, dslot, (uint16_t)new_hi);
        dst_nslots = dst_nslots + 1;
        bt_write16(dp, PAGE_N, NSLOTS, (uint16_t)dst_nslots);
        bt_write16(dp, PAGE_N, FREE_LO, (uint16_t)(free_lo + (uint64_t)BT_SLOT));
        bt_write16(dp, PAGE_N, FREE_HI, (uint16_t)new_hi);
        i = i + 1;
    }

    bt_write16(sp, PAGE_N, NSLOTS, (uint16_t)mid);
    bt_write16(sp, PAGE_N, FREE_LO, (uint16_t)((uint64_t)BODY + mid * (uint64_t)BT_SLOT));
    min_hi = (uint64_t)PAGE_N;
    i = 0;
    while (i < mid) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        cell_off = (uint64_t)bt_read16(sp, PAGE_N, slot_addr);
        if (cell_off < min_hi) {
            min_hi = cell_off;
        }
        i = i + 1;
    }
    bt_write16(sp, PAGE_N, FREE_HI, (uint16_t)min_hi);

    old_next = bt_read64(sp, PAGE_N, HLC);
    bt_write64(dp, PAGE_N, HLC, old_next);
    bt_write64(sp, PAGE_N, HLC, dst);
    if (bt_leaf_repack(sp, PAGE_N, (uint64_t)BT_IDLEN) == 0) {
        return 0;
    }
    return 1;
}

static uint64_t bt_find_ver_pos_at(const uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen, const uint8_t *id16) {
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint16_t cklen;
    int32_t cmp;

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
            return i;
        }
        cell_off = (uint64_t)bt_read16(heap, n, base + slot_off);
        if (bt_off_ok(n, base, cell_off, 4) == 0) {
            return i;
        }
        cklen = bt_read16(heap, n, base + cell_off);
        if (bt_off_ok(n, base, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)BT_IDLEN) == 0) {
            return i;
        }
        cmp = bt_keycmp_at(heap, n, base + cell_off + 4, (uint64_t)cklen, key, klen);
        if (cmp > 0) {
            return i;
        }
        if (cmp == 0) {
            cmp = bt_keycmp_at(heap, n, base + cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, id16, (uint64_t)BT_IDLEN);
            if (cmp > 0) {
                return i;
            }
        }
        i = i + 1;
    }
    return nslots;
}

static void bt_slot_insert_at(uint8_t *heap, uint64_t n, uint64_t base, uint64_t pos, uint64_t nslots, uint16_t cell_off) {
    uint64_t cur;
    uint64_t prev;
    uint64_t walk;
    uint64_t src_off;
    uint64_t dst_off;
    uint16_t sval;

    cur = nslots;
    while (cur > pos) {
        prev = 0;
        walk = 0;
        while (walk < cur) {
            prev = walk;
            walk = walk + 1;
        }
        src_off = (uint64_t)BODY + prev * (uint64_t)BT_SLOT;
        dst_off = (uint64_t)BODY + cur * (uint64_t)BT_SLOT;
        sval = bt_read16(heap, n, base + src_off);
        bt_write16(heap, n, base + dst_off, sval);
        cur = prev;
    }
    dst_off = (uint64_t)BODY + pos * (uint64_t)BT_SLOT;
    bt_write16(heap, n, base + dst_off, cell_off);
}

// bt_replace_same_value_at : sous rétention par élagage (prune), une réécriture
// d'une clé dont la dernière version porte une valeur identique remplace la
// cellule dans la feuille au lieu d'empiler une version. La cellule de plus
// grand identifiant pour la clé voit son identifiant réécrit en place (aucune
// place consommée, aucun slot ajouté) et la fonction rend 1. Si la clé est
// absente, si sa dernière valeur diffère, ou si les bornes sont invalides, la
// fonction rend 0 et l'insertion nominale empile la nouvelle version.
static uint8_t bt_replace_same_value_at(uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen, const uint8_t *id16, const uint8_t *val, uint64_t vlen) {
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_off;
    uint64_t cell_off;
    uint64_t best_off;
    uint64_t id_off;
    uint16_t cklen;
    uint16_t cvlen;
    uint8_t typ;
    uint8_t have;
    int32_t icmp;

    best_off = 0;
    have = 0;

    if (heap == 0) {
        return 0;
    }
    if (key == 0 || klen == 0 || klen > 0xFFFF || vlen > 0xFFFF) {
        return 0;
    }
    if (id16 == 0) {
        return 0;
    }
    if (vlen > 0 && val == 0) {
        return 0;
    }

    typ = bt_read8(heap, n, base + TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return 0;
    }

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        cell_off = (uint64_t)bt_read16(heap, n, base + slot_off);
        if (bt_off_ok(n, base, cell_off, 4) == 0) {
            return 0;
        }
        cklen = bt_read16(heap, n, base + cell_off);
        cvlen = bt_read16(heap, n, base + cell_off + 2);
        if (bt_off_ok(n, base, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)BT_IDLEN + (uint64_t)cvlen) == 0) {
            return 0;
        }
        if ((uint64_t)cklen == klen && bt_keyeq(heap, n, base + cell_off + 4, klen, key) != 0) {
            if (have == 0) {
                best_off = cell_off;
                have = 1;
            } else {
                icmp = bt_keycmp_pp(heap, n, base + cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, base + best_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN);
                if (icmp > 0) {
                    best_off = cell_off;
                }
            }
        }
        i = i + 1;
    }
    if (have == 0) {
        return 0;
    }

    cklen = bt_read16(heap, n, base + best_off);
    cvlen = bt_read16(heap, n, base + best_off + 2);
    if ((uint64_t)cvlen != vlen) {
        return 0;
    }
    if (bt_off_ok(n, base, best_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)BT_IDLEN + (uint64_t)cvlen) == 0) {
        return 0;
    }
    if (vlen > 0 && bt_keycmp_at(heap, n, base + best_off + 4 + (uint64_t)cklen + (uint64_t)BT_IDLEN, vlen, val, vlen) != 0) {
        return 0;
    }

    id_off = best_off + 4 + (uint64_t)cklen;
    bt_copy_in(heap, n, base + id_off, id16, (uint64_t)BT_IDLEN);
    return 1;
}

static db_bt_state_t bt_insert_ver_at(uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen, const uint8_t *id16, const uint8_t *val, uint64_t vlen) {
    db_bt_state_t out;
    uint64_t nslots;
    uint64_t free_lo;
    uint64_t free_hi;
    uint64_t cell_size;
    uint64_t need_lo;
    uint64_t new_hi;
    uint64_t cell_off;
    uint64_t pos;
    uint8_t typ;

    out.nkeys = 0;
    out.ok = 0;
    out.copied = 0;

    if (heap == 0) {
        return out;
    }
    if (klen == 0 || klen > 0xFFFF || vlen > 0xFFFF) {
        return out;
    }
    if (key == 0 || id16 == 0) {
        return out;
    }
    if (vlen > 0 && val == 0) {
        return out;
    }

    typ = bt_read8(heap, n, base + TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return out;
    }

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    free_lo = (uint64_t)bt_read16(heap, n, base + FREE_LO);
    free_hi = (uint64_t)bt_read16(heap, n, base + FREE_HI);
    out.nkeys = nslots;

    if (free_lo < (uint64_t)BODY) {
        return out;
    }
    if (free_hi > (uint64_t)PAGE_N) {
        return out;
    }
    if (free_lo > free_hi) {
        return out;
    }

    cell_size = (uint64_t)4 + klen + (uint64_t)BT_IDLEN + vlen;
    if (free_lo >= (uint64_t)PAGE_N) {
        return out;
    }
    if ((uint64_t)BT_SLOT > (uint64_t)PAGE_N - free_lo) {
        return out;
    }
    need_lo = free_lo + (uint64_t)BT_SLOT;

    if (cell_size > free_hi) {
        return out;
    }
    new_hi = free_hi - cell_size;
    if (need_lo > new_hi) {
        return out;
    }

    cell_off = new_hi;
    if (bt_off_ok(n, base, cell_off, cell_size) == 0) {
        return out;
    }

    pos = bt_find_ver_pos_at(heap, n, base, key, klen, id16);
    if (pos > nslots) {
        pos = nslots;
    }

    bt_write16(heap, n, base + cell_off, (uint16_t)klen);
    bt_write16(heap, n, base + cell_off + 2, (uint16_t)vlen);
    bt_copy_in(heap, n, base + cell_off + 4, key, klen);
    bt_copy_in(heap, n, base + cell_off + 4 + klen, id16, (uint64_t)BT_IDLEN);
    if (vlen > 0) {
        bt_copy_in(heap, n, base + cell_off + 4 + klen + (uint64_t)BT_IDLEN, val, vlen);
    }

    bt_slot_insert_at(heap, n, base, pos, nslots, (uint16_t)cell_off);
    nslots = nslots + 1;
    bt_write16(heap, n, base + NSLOTS, (uint16_t)nslots);
    bt_write16(heap, n, base + FREE_LO, (uint16_t)need_lo);
    bt_write16(heap, n, base + FREE_HI, (uint16_t)new_hi);

    out.nkeys = nslots;
    out.ok = 1;
    return out;
}

static db_bt_get_t bt_get_as_of_at(const uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen, const uint8_t *snap16, uint8_t *out, uint64_t outn) {
    db_bt_get_t g;
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint64_t best_off;
    uint64_t best_id;
    uint64_t copy_n;
    uint16_t cklen;
    uint16_t cvlen;
    uint8_t typ;
    uint8_t have;
    int32_t kcmp;
    int32_t icmp;

    g.len = 0;
    g.walked = 0;
    g.found = 0;
    g.fallback = 0;
    best_off = 0;
    best_id = 0;
    have = 0;

    if (heap == 0) {
        return g;
    }
    if (key == 0 || snap16 == 0 || klen == 0 || klen > 0xFFFF) {
        return g;
    }

    typ = bt_read8(heap, n, base + TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return g;
    }

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(n, base, slot_addr, (uint64_t)BT_SLOT) == 0) {
            return g;
        }
        cell_off = (uint64_t)bt_read16(heap, n, base + slot_addr);
        if (bt_off_ok(n, base, cell_off, 4) == 0) {
            return g;
        }
        cklen = bt_read16(heap, n, base + cell_off);
        cvlen = bt_read16(heap, n, base + cell_off + 2);
        if (bt_off_ok(n, base, cell_off, (uint64_t)4 + (uint64_t)cklen + (uint64_t)BT_IDLEN + (uint64_t)cvlen) == 0) {
            return g;
        }
        kcmp = bt_keycmp_at(heap, n, base + cell_off + 4, (uint64_t)cklen, key, klen);
        if (kcmp > 0) {
            break;
        }
        if (kcmp == 0) {
            icmp = bt_keycmp_at(heap, n, base + cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, snap16, (uint64_t)BT_IDLEN);
            if (icmp <= 0) {
                if (have == 0) {
                    best_off = cell_off;
                    best_id = base + cell_off + 4 + (uint64_t)cklen;
                    have = 1;
                } else {
                    icmp = bt_keycmp_pp(heap, n, base + cell_off + 4 + (uint64_t)cklen, (uint64_t)BT_IDLEN, best_id, (uint64_t)BT_IDLEN);
                    if (icmp > 0) {
                        best_off = cell_off;
                        best_id = base + cell_off + 4 + (uint64_t)cklen;
                    }
                }
            }
        }
        i = i + 1;
    }

    if (have == 0) {
        return g;
    }

    cklen = bt_read16(heap, n, base + best_off);
    cvlen = bt_read16(heap, n, base + best_off + 2);
    g.len = (uint64_t)cvlen;
    g.found = 1;
    copy_n = (uint64_t)cvlen;
    if (copy_n > outn) {
        copy_n = outn;
    }
    if (out != 0 && copy_n > 0) {
        bt_copy_out(heap, n, base + best_off + 4 + (uint64_t)cklen + (uint64_t)BT_IDLEN, out, copy_n);
    }
    return g;
}

// bt_insert_ver_heap_impl est le noyau commun d'insertion MVCC. Le drapeau
// prune active la rétention en ligne : lorsqu'il vaut 1 et qu'une réécriture
// porte une valeur identique à la dernière version de la clé, la cellule est
// remplacée en place (identifiant réécrit) au lieu d'empiler une version.
static db_bt_heap_st_t bt_insert_ver_heap_impl(const uint8_t *pub, uint8_t *dirty, uint64_t nbytes, uint64_t npages, db_bt_heap_st_t st, const uint8_t *key, uint64_t klen, const uint8_t *id16, const uint8_t *val, uint64_t vlen, uint64_t prune) {
    db_bt_heap_st_t out;
    db_bt_state_t ins;
    db_bt_get_t g;
    uint64_t page;
    uint64_t walked;
    uint64_t next;
    uint64_t used;
    uint64_t last;
    uint64_t base;
    uint64_t intern;
    uint64_t rcell;
    uint64_t rcklen;
    uint64_t mask;
    uint64_t parent;
    uint64_t chosen;
    uint8_t typ;
    int32_t kcmp;
    uint64_t anc[BT_PATH_MAX];
    uint64_t level;
    db_bt_heap_st_t grown;

    out.root = 0;
    out.used = 0;
    out.nkeys = 0;
    out.pages = 0;
    out.ok = 0;
    out.full = 0;
    level = 0;

    if (pub == 0 || dirty == 0) {
        return out;
    }
    if (npages == 0) {
        return out;
    }
    if (nbytes / (uint64_t)PAGE_N != npages) {
        return out;
    }
    if (npages * (uint64_t)PAGE_N != nbytes) {
        return out;
    }

    mask = 0;

    if (klen == 0 || klen > 0xFFFF || vlen > 0xFFFF) {
        return out;
    }
    if (key == 0 || id16 == 0) {
        return out;
    }
    if (vlen > 0 && val == 0) {
        return out;
    }

    used = st.used;
    if (used == 0) {
        used = 1;
    }
    if (st.root >= npages) {
        return out;
    }

    page = st.root;
    parent = st.root;
    mask = bt_cow_page(pub, dirty, nbytes, page, mask);
    base = page * (uint64_t)PAGE_N;
    typ = bt_read8(dirty, nbytes, base + TYPE);
    if (typ == (uint8_t)TYPE_INTERNAL) {
        anc[0] = page;
        level = 1;
        walked = 0;
        while (typ == (uint8_t)TYPE_INTERNAL && walked < npages) {
            parent = page;
            chosen = bt_internal_child(dirty, nbytes, base, key, klen);
            if (chosen >= npages) {
                return out;
            }
            if (chosen == page) {
                return out;
            }
            page = chosen;
            mask = bt_cow_page(pub, dirty, nbytes, page, mask);
            base = page * (uint64_t)PAGE_N;
            typ = bt_read8(dirty, nbytes, base + TYPE);
            walked = walked + 1;
            if (typ == (uint8_t)TYPE_INTERNAL) {
                if (level >= (uint64_t)BT_PATH_MAX) {
                    return out;
                }
                anc[level] = page;
                level = level + 1;
            }
        }
        if (typ == (uint8_t)TYPE_INTERNAL) {
            return out;
        }
        g = bt_get_at(dirty, nbytes, base, key, klen, 0, 0);
        if (g.found != 0) {
            if (prune != 0 && bt_replace_same_value_at(dirty, nbytes, base, key, klen, id16, val, vlen) != 0) {
                out.root = st.root;
                out.used = used;
                out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
                out.pages = mask;
                out.ok = 1;
                return out;
            }
            ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
            if (ins.ok != 0) {
                out.root = st.root;
                out.used = used;
                out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
                out.pages = mask;
                out.ok = 1;
                return out;
            }
        }
        ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
        if (ins.ok != 0) {
            out.root = st.root;
            out.used = used;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        if (used + 2 > npages || used + 2 < used) {
            out.full = 1;
            return out;
        }
        mask = bt_cow_page(pub, dirty, nbytes, used, mask);
        if (bt_chain_split_ver(dirty, npages, page, used) == 0) {
            return out;
        }
        base = used * (uint64_t)PAGE_N;
        rcell = (uint64_t)bt_read16(dirty, nbytes, base + (uint64_t)BODY);
        if (bt_off_ok(nbytes, base, rcell, 4) == 0) {
            return out;
        }
        rcklen = (uint64_t)bt_read16(dirty, nbytes, base + rcell);
        kcmp = bt_keycmp_at(dirty, nbytes, base + rcell + 4, rcklen, key, klen);
        if (kcmp > 0) {
            base = page * (uint64_t)PAGE_N;
            ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
        } else {
            base = used * (uint64_t)PAGE_N;
            ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
        }
        if (ins.ok == 0) {
            return out;
        }
        if (bt_internal_add_sep(dirty, nbytes, npages, parent, page, used) != 0) {
            out.root = st.root;
            out.used = used + 1;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        intern = used + 1;
        if (parent == st.root) {
            if (intern + 1 >= npages) {
                out.full = 1;
                return out;
            }
            mask = bt_cow_page(pub, dirty, nbytes, intern, mask);
            mask = bt_cow_page(pub, dirty, nbytes, intern + 1, mask);
            if (bt_elevate_root(dirty, nbytes, npages, st.root, intern, intern + 1, page, used) == 0) {
                return out;
            }
            out.root = intern + 1;
            out.used = used + 3;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, intern + 1);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        if (intern >= npages) {
            out.full = 1;
            return out;
        }
        mask = bt_cow_page(pub, dirty, nbytes, intern, mask);
        grown = bt_split_attach_deep(pub, dirty, nbytes, npages, anc, level - 1, st.root, used + 2, mask, parent, intern, page, used);
        if (grown.ok == 0) {
            out.full = grown.full;
            return out;
        }
        out.root = grown.root;
        out.used = grown.used;
        out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, grown.root);
        out.pages = grown.pages;
        out.ok = 1;
        return out;
    }

    page = st.root;
    walked = 0;
    last = page;
    while (walked < npages) {
        if (page >= npages) {
            return out;
        }
        mask = bt_cow_page(pub, dirty, nbytes, page, mask);
        base = page * (uint64_t)PAGE_N;
        g = bt_get_at(dirty, nbytes, base, key, klen, 0, 0);
        if (g.found != 0) {
            if (prune != 0 && bt_replace_same_value_at(dirty, nbytes, base, key, klen, id16, val, vlen) != 0) {
                out.root = st.root;
                out.used = used;
                out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
                out.pages = mask;
                out.ok = 1;
                return out;
            }
            ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
            if (ins.ok != 0) {
                out.root = st.root;
                out.used = used;
                out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
                out.pages = mask;
                out.ok = 1;
                return out;
            }
            last = page;
            break;
        }
        last = page;
        next = bt_read64(dirty, nbytes, base + HLC);
        if (next == 0) {
            break;
        }
        if (next == page) {
            break;
        }
        page = next;
        walked = walked + 1;
    }

    page = st.root;
    walked = 0;
    while (walked < npages) {
        if (page >= npages) {
            break;
        }
        mask = bt_cow_page(pub, dirty, nbytes, page, mask);
        base = page * (uint64_t)PAGE_N;
        ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
        if (ins.ok != 0) {
            out.root = st.root;
            out.used = used;
            out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
            out.pages = mask;
            out.ok = 1;
            return out;
        }
        last = page;
        next = bt_read64(dirty, nbytes, base + HLC);
        if (next == 0) {
            break;
        }
        if (next == page) {
            break;
        }
        page = next;
        walked = walked + 1;
    }

    if (last != st.root) {
        return out;
    }
    if (used >= npages) {
        return out;
    }
    if (npages > 2 && (used + 2 > npages || used + 2 < used)) {
        return out;
    }
    mask = bt_cow_page(pub, dirty, nbytes, last, mask);
    mask = bt_cow_page(pub, dirty, nbytes, used, mask);
    if (bt_chain_split_ver(dirty, npages, last, used) == 0) {
        return out;
    }

    base = used * (uint64_t)PAGE_N;
    rcell = (uint64_t)bt_read16(dirty, nbytes, base + (uint64_t)BODY);
    if (bt_off_ok(nbytes, base, rcell, 4) == 0) {
        return out;
    }
    rcklen = (uint64_t)bt_read16(dirty, nbytes, base + rcell);
    kcmp = bt_keycmp_at(dirty, nbytes, base + rcell + 4, rcklen, key, klen);
    if (kcmp > 0) {
        base = last * (uint64_t)PAGE_N;
        ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
    } else {
        base = used * (uint64_t)PAGE_N;
        ins = bt_insert_ver_at(dirty, nbytes, base, key, klen, id16, val, vlen);
    }
    if (ins.ok == 0) {
        return out;
    }

    intern = used + 1;
    if (intern < npages) {
        mask = bt_cow_page(pub, dirty, nbytes, intern, mask);
        if (bt_make_internal(dirty, nbytes, npages, st.root, used, intern) == 0) {
            return out;
        }
        out.root = intern;
        out.used = used + 2;
        out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, intern);
        out.pages = mask;
        out.ok = 1;
        return out;
    }
    if (npages == 2) {
        out.root = st.root;
        out.used = used + 1;
        out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
        out.pages = mask;
        out.ok = 1;
        return out;
    }
    return out;
}

// db_bt_insert_ver_heap conserve le comportement historique : toutes les
// versions sont empilées (rétention KeepAll / ArchiveSuperseded, rejeu
// versionné, oracle). C'est le noyau appelé hors élagage en ligne.
db_bt_heap_st_t db_bt_insert_ver_heap(const uint8_t *pub, uint8_t *dirty, uint64_t nbytes, uint64_t npages, db_bt_heap_st_t st, const uint8_t *key, uint64_t klen, const uint8_t *id16, const uint8_t *val, uint64_t vlen) {
    return bt_insert_ver_heap_impl(pub, dirty, nbytes, npages, st, key, klen, id16, val, vlen, 0);
}

// db_bt_insert_ver_heap_prune active la rétention en ligne pour la rétention
// par élagage (RetentionPruneSuperseded) : une réécriture à valeur identique
// remplace la cellule au lieu d'empiler une version. Le drapeau prune vaut 1
// pour l'élagage, 0 pour le comportement historique.
db_bt_heap_st_t db_bt_insert_ver_heap_prune(const uint8_t *pub, uint8_t *dirty, uint64_t nbytes, uint64_t npages, db_bt_heap_st_t st, const uint8_t *key, uint64_t klen, const uint8_t *id16, const uint8_t *val, uint64_t vlen, uint64_t prune) {
    return bt_insert_ver_heap_impl(pub, dirty, nbytes, npages, st, key, klen, id16, val, vlen, prune);
}

db_bt_get_t db_bt_get_as_of_heap(const uint8_t *heap, uint64_t nbytes, uint64_t npages, uint64_t root, const uint8_t *key, uint64_t klen, const uint8_t *snap16, uint8_t *out, uint64_t outn) {
    db_bt_get_t g;
    uint64_t page;
    uint64_t walked;
    uint64_t next;
    uint64_t base;
    uint64_t chosen;
    uint8_t typ;

    g.len = 0;
    g.walked = 0;
    g.found = 0;
    g.fallback = 0;

    if (heap == 0 || npages == 0) {
        return g;
    }
    if (nbytes / (uint64_t)PAGE_N != npages) {
        return g;
    }
    if (npages * (uint64_t)PAGE_N != nbytes) {
        return g;
    }
    if (key == 0 || snap16 == 0 || klen == 0 || klen > 0xFFFF) {
        return g;
    }

    page = root;
    walked = 0;
    if (page >= npages) {
        return g;
    }
    base = page * (uint64_t)PAGE_N;
    typ = bt_read8(heap, nbytes, base + TYPE);
    while (typ == (uint8_t)TYPE_INTERNAL && walked < npages) {
        chosen = bt_internal_child(heap, nbytes, base, key, klen);
        if (chosen >= npages) {
            g.walked = walked + 1;
            return g;
        }
        if (chosen == page) {
            g.walked = walked + 1;
            return g;
        }
        page = chosen;
        walked = walked + 1;
        base = page * (uint64_t)PAGE_N;
        typ = bt_read8(heap, nbytes, base + TYPE);
    }
    if (typ == (uint8_t)TYPE_INTERNAL) {
        g.walked = walked;
        return g;
    }
    if (walked != 0) {
        g = bt_get_as_of_at(heap, nbytes, base, key, klen, snap16, out, outn);
        g.walked = walked + 1;
        g.fallback = 0;
        return g;
    }
    while (walked < npages) {
        if (page >= npages) {
            g.walked = walked;
            return g;
        }
        base = page * (uint64_t)PAGE_N;
        g = bt_get_as_of_at(heap, nbytes, base, key, klen, snap16, out, outn);
        if (g.found != 0) {
            g.walked = walked + 1;
            g.fallback = 0;
            return g;
        }
        next = bt_read64(heap, nbytes, base + HLC);
        if (next == 0) {
            g.walked = walked + 1;
            g.fallback = 0;
            return g;
        }
        if (next == page) {
            g.walked = walked + 1;
            g.fallback = 0;
            return g;
        }
        page = next;
        walked = walked + 1;
    }
    g.walked = walked;
    g.fallback = 0;
    return g;
}

static uint8_t bt_del_at(uint8_t *heap, uint64_t n, uint64_t base, const uint8_t *key, uint64_t klen) {
    uint64_t nslots;
    uint64_t i;
    uint64_t w;
    uint64_t slot_off;
    uint64_t cell_off;
    uint16_t cklen;
    uint16_t off;
    uint8_t typ;
    uint8_t removed;

    if (heap == 0) {
        return 0;
    }
    if (key == 0 || klen == 0 || klen > 0xFFFF) {
        return 0;
    }

    typ = bt_read8(heap, n, base + TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return 0;
    }

    nslots = (uint64_t)bt_read16(heap, n, base + NSLOTS);
    w = 0;
    i = 0;
    removed = 0;
    while (i < nslots) {
        slot_off = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_off_ok(n, base, slot_off, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        off = bt_read16(heap, n, base + slot_off);
        cell_off = (uint64_t)off;
        if (bt_off_ok(n, base, cell_off, 4) == 0) {
            return 0;
        }
        cklen = bt_read16(heap, n, base + cell_off);
        if (bt_off_ok(n, base, cell_off, (uint64_t)4 + (uint64_t)cklen) == 0) {
            return 0;
        }
        if ((uint64_t)cklen == klen && bt_keyeq(heap, n, base + cell_off + 4, klen, key) != 0) {
            removed = 1;
        } else {
            if (w != i) {
                bt_write16(heap, n, base + (uint64_t)BODY + w * (uint64_t)BT_SLOT, off);
            }
            w = w + 1;
        }
        i = i + 1;
    }
    bt_write16(heap, n, base + NSLOTS, (uint16_t)w);
    bt_write16(heap, n, base + FREE_LO, (uint16_t)((uint64_t)BODY + w * (uint64_t)BT_SLOT));
    return removed;
}

db_bt_heap_st_t db_bt_del_heap(const uint8_t *pub, uint8_t *dirty, uint64_t nbytes, uint64_t npages, db_bt_heap_st_t st, const uint8_t *key, uint64_t klen) {
    db_bt_heap_st_t out;
    uint64_t page;
    uint64_t walked;
    uint64_t used;
    uint64_t base;
    uint64_t chosen;
    uint8_t typ;
    uint64_t mask;

    out.root = 0;
    out.used = 0;
    out.nkeys = 0;
    out.pages = 0;
    out.ok = 0;
    out.full = 0;
    mask = 0;

    if (pub == 0 || dirty == 0) {
        return out;
    }
    if (npages == 0) {
        return out;
    }
    if (nbytes / (uint64_t)PAGE_N != npages) {
        return out;
    }
    if (npages * (uint64_t)PAGE_N != nbytes) {
        return out;
    }

    if (klen == 0 || klen > 0xFFFF) {
        return out;
    }
    if (key == 0) {
        return out;
    }

    used = st.used;
    if (used == 0) {
        used = 1;
    }
    if (st.root >= npages) {
        return out;
    }

    page = st.root;
    walked = 0;
    while (walked < npages) {
        if (page >= npages) {
            return out;
        }
        mask = bt_cow_page(pub, dirty, nbytes, page, mask);
        base = page * (uint64_t)PAGE_N;
        typ = bt_read8(dirty, nbytes, base + TYPE);
        if (typ == (uint8_t)TYPE_INTERNAL) {
            chosen = bt_internal_child(dirty, nbytes, base, key, klen);
            if (chosen >= npages) {
                return out;
            }
            if (chosen == page) {
                return out;
            }
            page = chosen;
            walked = walked + 1;
        } else if (typ == (uint8_t)TYPE_LEAF) {
            // La descente par les nœuds internes a atteint la feuille où la clé
            // se trouve (ou devrait se trouver). Le noyau s'arrête là : que la
            // clé soit retirée ou absente, la chaîne de feuilles n'est pas
            // parcourue, sous peine d'un coût O(feuilles) par suppression.
            bt_del_at(dirty, nbytes, base, key, klen);
            break;
        } else {
            return out;
        }
    }

    out.root = st.root;
    out.used = used;
    out.nkeys = bt_heap_nkeys(dirty, nbytes, npages, st.root);
    out.pages = mask;
    out.ok = 1;
    return out;
}

uint8_t db_bt_leaf_has_prefix(const uint8_t *page, uint64_t n, const uint8_t *pref, uint64_t plen) {
    uint64_t nslots;
    uint64_t i;
    uint64_t slot_addr;
    uint64_t cell_off;
    uint16_t cklen;
    uint8_t typ;

    if (page == 0 || n != PAGE_N) {
        return 0;
    }
    if (pref == 0 || plen == 0 || plen > 0xFFFF) {
        return 0;
    }
    typ = bt_read8(page, n, TYPE);
    if (typ != (uint8_t)TYPE_LEAF) {
        return 0;
    }
    nslots = (uint64_t)bt_read16(page, n, NSLOTS);
    i = 0;
    while (i < nslots) {
        slot_addr = (uint64_t)BODY + i * (uint64_t)BT_SLOT;
        if (bt_bounds(page, n, slot_addr, (uint64_t)BT_SLOT) == 0) {
            return 0;
        }
        cell_off = (uint64_t)bt_read16(page, n, slot_addr);
        if (bt_bounds(page, n, cell_off, 4) == 0) {
            return 0;
        }
        cklen = bt_read16(page, n, cell_off);
        if ((uint64_t)cklen < plen) {
            i = i + 1;
            continue;
        }
        if (bt_bounds(page, n, cell_off, (uint64_t)4 + (uint64_t)cklen) == 0) {
            return 0;
        }
        if (memcmp(page + cell_off + 4, pref, (size_t)plen) == 0) {
            return 1;
        }
        i = i + 1;
    }
    return 0;
}

uint64_t db_bt_merge_u64(const uint64_t *a, uint64_t na, const uint64_t *b, uint64_t nb, uint64_t *out, uint64_t cap) {
    uint64_t i;
    uint64_t j;
    uint64_t k;

    i = 0;
    j = 0;
    k = 0;
    if (a == 0 || b == 0 || out == 0) {
        return 0;
    }
    while (i < na && j < nb && k < cap) {
        if (a[i] < b[j]) {
            out[k] = a[i];
            i = i + 1;
            k = k + 1;
        } else if (b[j] < a[i]) {
            out[k] = b[j];
            j = j + 1;
            k = k + 1;
        } else {
            out[k] = a[i];
            i = i + 1;
            j = j + 1;
            k = k + 1;
        }
    }
    while (i < na && k < cap) {
        out[k] = a[i];
        i = i + 1;
        k = k + 1;
    }
    while (j < nb && k < cap) {
        out[k] = b[j];
        j = j + 1;
        k = k + 1;
    }
    return k;
}

