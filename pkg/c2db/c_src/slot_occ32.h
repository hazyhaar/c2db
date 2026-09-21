#ifndef C2DB_SLOT_OCC32_H
#define C2DB_SLOT_OCC32_H

#include <stdint.h>

uint32_t c2db_slot_occ32(const uint8_t *p);
uint32_t c2db_slot_occ32_avx2(const uint8_t *p);

#endif
