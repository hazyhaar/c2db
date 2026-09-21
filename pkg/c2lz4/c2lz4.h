//go:build ignore

#ifndef C2LZ4_H
#define C2LZ4_H

#include <stdint.h>

int lz4_decompress_safe(const uint8_t *src, int src_len, uint8_t *dst, int dst_len);
int lz4_compress_fast(const uint8_t *src, int src_len, uint8_t *dst, int dst_max_len);

#endif /* C2LZ4_H */
