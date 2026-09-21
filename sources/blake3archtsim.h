/* SPDX-License-Identifier: Apache-2.0 OR MIT */
#ifndef BLAKE3ARCHTSIM_H
#define BLAKE3ARCHTSIM_H

#include <stdint.h>
#include <stddef.h>

#define BLAKE3ARCHTSIM_BLOCK_LEN 64

/* Compression d'un bloc BLAKE3 : aiguillage AVX2 / scalaire par sonde CPU. */
void blake3archtsim_compress(const uint32_t cv[8],
                             const uint8_t block[BLAKE3ARCHTSIM_BLOCK_LEN],
                             uint8_t block_len,
                             uint64_t counter,
                             uint8_t flags,
                             uint32_t out[16]);

/* Les deux corps, exposés pour l'oracle de parité C. */
void blake3archtsim_compress_scalar(const uint32_t cv[8],
                                    const uint8_t block[BLAKE3ARCHTSIM_BLOCK_LEN],
                                    uint8_t block_len,
                                    uint64_t counter,
                                    uint8_t flags,
                                    uint32_t out[16]);

void blake3archtsim_compress_avx2(const uint32_t cv[8],
                                  const uint8_t block[BLAKE3ARCHTSIM_BLOCK_LEN],
                                  uint8_t block_len,
                                  uint64_t counter,
                                  uint8_t flags,
                                  uint32_t out[16]);

#endif
