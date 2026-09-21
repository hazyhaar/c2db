// SPDX-License-Identifier: Apache-2.0 OR MIT

#ifndef DB_PAGE_H
#define DB_PAGE_H

#include <stdint.h>

#define PAGE_N     16384
#define PAGE_ID    0
#define HLC        8
#define TOPO       16
#define TYPE       20
#define FLAGS      21
#define NSLOTS     22
#define FREE_LO    24
#define FREE_HI    26
#define CRC        28
#define BODY       64
#define SLOT_BYTES 16

typedef struct {
    uint64_t nslots;
    uint16_t free_lo;
    uint16_t free_hi;
    uint32_t crc;
} db_page_hdr_t;

typedef struct {
    uint64_t off;
    uint64_t len;
    uint8_t  valid;
} db_page_slot_t;

uint8_t db_page_bounds(const uint8_t *restrict page, uint64_t n, uint64_t addr, uint64_t len);
db_page_hdr_t db_page_hdr_read(const uint8_t *restrict page, uint64_t n);
uint8_t db_page_hdr_write(uint8_t *restrict page, uint64_t n, db_page_hdr_t hdr);
db_page_slot_t db_page_slot_get(const uint8_t *restrict page, uint64_t n, uint64_t idx);
uint8_t db_page_slot_set(uint8_t *restrict page, uint64_t n, uint64_t idx, db_page_slot_t slot);
uint32_t db_page_crc32c(const uint8_t *restrict page, uint64_t n);
uint8_t db_page_crc32c_store(uint8_t *restrict page, uint64_t n);

#endif
