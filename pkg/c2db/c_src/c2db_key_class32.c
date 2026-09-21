#include "c2db_key_class32.h"

static const uint8_t c2db_key_class_lut16[16] = {
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2
};

void c2db_key_class32(const uint8_t in[32], uint8_t out[32]) {
    int i;
    for (i = 0; i < 32; i++) {
        out[i] = c2db_key_class_lut16[in[i] & 0x0F];
    }
}
