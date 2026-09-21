#ifndef C2DB_CRC32C_FULLPAGE_H
#define C2DB_CRC32C_FULLPAGE_H

#include <stdint.h>

#define C2DB_PAGE_N 16384
#define C2DB_CRC_OFFSET 28

uint32_t c2db_crc32c_fullpage(const uint8_t *page, uint64_t n);
uint8_t c2db_crc32c_fullpage_store(uint8_t *page, uint64_t n);

#endif
