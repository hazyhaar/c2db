#ifndef BLAKE3_REF_H
#define BLAKE3_REF_H

#include <stdint.h>
#include <stddef.h>

#define BLAKE3_OUT_LEN 32
#define BLAKE3_KEY_LEN 32
#define BLAKE3_BLOCK_LEN 64
#define BLAKE3_CHUNK_LEN 1024

#define CHUNK_START         (1 << 0)
#define CHUNK_END           (1 << 1)
#define PARENT              (1 << 2)
#define ROOT                (1 << 3)
#define KEYED_HASH          (1 << 4)
#define DERIVE_KEY_CONTEXT  (1 << 5)
#define DERIVE_KEY_MATERIAL (1 << 6)

void blake3_compress_ref(const uint32_t cv[8],
                         const uint8_t block[BLAKE3_BLOCK_LEN],
                         uint8_t block_len,
                         uint64_t counter,
                         uint8_t flags,
                         uint32_t out[16]);

void blake3_hash_ref(const uint8_t *input, size_t input_len, uint8_t out[32]);

void blake3_derive_key_ref(const uint8_t *context, size_t context_len,
                           const uint8_t *key_material, size_t key_material_len,
                           uint8_t out[32]);

#endif
