#include <immintrin.h>
#include <stdint.h>
#include "page4k_is_zero.h"

uint8_t page4k_is_zero_avx2(const uint8_t *page, uint64_t n) {
    if (page == 0 || n != 4096) {
        return 0;
    }
    __m256i acc = _mm256_setzero_si256();
    for (uint64_t i = 0; i < 4096; i += 32) {
        __m256i v = _mm256_loadu_si256((const __m256i *)(page + i));
        acc = _mm256_or_si256(acc, v);
    }
    return (uint8_t)_mm256_testz_si256(acc, acc);
}
