#include <immintrin.h>
#include "slot_occ32.h"

uint32_t c2db_slot_occ32_avx2(const uint8_t *p) {
    __m256i v;
    __m256i z;
    uint32_t empty;

    if (p == 0) {
        return 0;
    }
    v = _mm256_loadu_si256((const __m256i *)p);
    z = _mm256_set1_epi8(0);
    empty = (uint32_t)_mm256_movemask_epi8(_mm256_cmpeq_epi8(v, z));
    return 32u - (uint32_t)__builtin_popcount(empty);
}
