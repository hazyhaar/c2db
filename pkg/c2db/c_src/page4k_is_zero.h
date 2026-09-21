#ifndef PAGE4K_IS_ZERO_H
#define PAGE4K_IS_ZERO_H

#include <stdint.h>

uint8_t page4k_is_zero_avx2(const uint8_t *page, uint64_t n);

#endif /* PAGE4K_IS_ZERO_H */
