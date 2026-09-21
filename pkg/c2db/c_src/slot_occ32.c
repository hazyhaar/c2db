#include "slot_occ32.h"

uint32_t c2db_slot_occ32(const uint8_t *p) {
    uint32_t n;
    uint32_t i;

    if (p == 0) {
        return 0;
    }
    n = 0;
    i = 0;
    while (i < 32) {
        if (p[i] != 0) {
            n = n + 1;
        }
        i = i + 1;
    }
    return n;
}
