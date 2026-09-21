#include "blake3_ref.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static uint64_t rng_state = 0x853c49e6748fea9bULL;

static uint64_t xorshift64(void) {
    uint64_t x = rng_state;
    x ^= x << 13;
    x ^= x >> 7;
    x ^= x << 17;
    rng_state = x;
    return x;
}

int main(void) {
    uint8_t buffer[65536];
    uint64_t fold_hash = 0;

    for (size_t len = 0; len <= 4096; len += (len < 128 ? 1 : (len < 1024 ? 31 : 257))) {
        for (size_t i = 0; i < len; i++) {
            buffer[i] = (uint8_t)xorshift64();
        }
        uint8_t out[32];
        blake3_hash_ref(buffer, len, out);
        for (size_t i = 0; i < 32; i += 8) {
            uint64_t w = (uint64_t)out[i] |
                         ((uint64_t)out[i+1] << 8) |
                         ((uint64_t)out[i+2] << 16) |
                         ((uint64_t)out[i+3] << 24) |
                         ((uint64_t)out[i+4] << 32) |
                         ((uint64_t)out[i+5] << 40) |
                         ((uint64_t)out[i+6] << 48) |
                         ((uint64_t)out[i+7] << 56);
            fold_hash ^= w;
            fold_hash = (fold_hash << 7) | (fold_hash >> 57);
        }
    }
    printf("FOLD_BLAKE3=0x%016llX\n", (unsigned long long)fold_hash);
    return 0;
}
