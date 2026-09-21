#include "blake3_ref.h"
#include <string.h>

static const uint32_t IV[8] = {
    0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
    0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
};

static const size_t MSG_PERMUTATION[16] = {
    2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8
};

static inline uint32_t rotr32(uint32_t w, uint32_t c) {
    return (w >> c) | (w << (32 - c));
}

static inline void g(uint32_t *state, size_t a, size_t b, size_t c, size_t d, uint32_t mx, uint32_t my) {
    state[a] = state[a] + state[b] + mx;
    state[d] = rotr32(state[d] ^ state[a], 16);
    state[c] = state[c] + state[d];
    state[b] = rotr32(state[b] ^ state[c], 12);
    state[a] = state[a] + state[b] + my;
    state[d] = rotr32(state[d] ^ state[a], 8);
    state[c] = state[c] + state[d];
    state[b] = rotr32(state[b] ^ state[c], 7);
}

static inline void round_fn(uint32_t state[16], const uint32_t msg[16]) {
    g(state, 0, 4, 8, 12, msg[0], msg[1]);
    g(state, 1, 5, 9, 13, msg[2], msg[3]);
    g(state, 2, 6, 10, 14, msg[4], msg[5]);
    g(state, 3, 7, 11, 15, msg[6], msg[7]);
    g(state, 0, 5, 10, 15, msg[8], msg[9]);
    g(state, 1, 6, 11, 12, msg[10], msg[11]);
    g(state, 2, 7, 8, 13, msg[12], msg[13]);
    g(state, 3, 4, 9, 14, msg[14], msg[15]);
}

void blake3_compress_ref(const uint32_t cv[8],
                         const uint8_t block[BLAKE3_BLOCK_LEN],
                         uint8_t block_len,
                         uint64_t counter,
                         uint8_t flags,
                         uint32_t out[16]) {
    uint32_t state[16] = {
        cv[0], cv[1], cv[2], cv[3], cv[4], cv[5], cv[6], cv[7],
        IV[0], IV[1], IV[2], IV[3],
        (uint32_t)counter, (uint32_t)(counter >> 32), (uint32_t)block_len, (uint32_t)flags,
    };
    uint32_t block_words[16];
    for (size_t i = 0; i < 16; i++) {
        block_words[i] = (uint32_t)block[i*4] |
                         ((uint32_t)block[i*4+1] << 8) |
                         ((uint32_t)block[i*4+2] << 16) |
                         ((uint32_t)block[i*4+3] << 24);
    }
    uint32_t msg[16];
    memcpy(msg, block_words, sizeof(msg));

    for (size_t r = 0; r < 7; r++) {
        round_fn(state, msg);
        uint32_t new_msg[16];
        for (size_t i = 0; i < 16; i++) {
            new_msg[i] = msg[MSG_PERMUTATION[i]];
        }
        memcpy(msg, new_msg, sizeof(msg));
    }

    for (size_t i = 0; i < 8; i++) {
        state[i] ^= state[i + 8];
        state[i + 8] ^= cv[i];
    }
    memcpy(out, state, sizeof(uint32_t) * 16);
}

typedef struct {
    uint32_t cv[8];
    uint64_t chunk_counter;
    uint8_t buf[BLAKE3_BLOCK_LEN];
    uint8_t buf_len;
    uint8_t blocks_compressed;
    uint8_t flags;
} chunk_state;

static void chunk_state_init(chunk_state *self, const uint32_t key_words[8], uint64_t chunk_counter, uint8_t flags) {
    memcpy(self->cv, key_words, sizeof(self->cv));
    self->chunk_counter = chunk_counter;
    memset(self->buf, 0, sizeof(self->buf));
    self->buf_len = 0;
    self->blocks_compressed = 0;
    self->flags = flags;
}

static void chunk_state_update(chunk_state *self, const uint8_t *input, size_t input_len) {
    while (input_len > 0) {
        if (self->buf_len == BLAKE3_BLOCK_LEN) {
            uint32_t out[16];
            uint8_t flags = self->flags;
            if (self->blocks_compressed == 0) flags |= CHUNK_START;
            blake3_compress_ref(self->cv, self->buf, BLAKE3_BLOCK_LEN, self->chunk_counter, flags, out);
            memcpy(self->cv, out, sizeof(uint32_t) * 8);
            self->blocks_compressed++;
            memset(self->buf, 0, sizeof(self->buf));
            self->buf_len = 0;
        }
        size_t take = BLAKE3_BLOCK_LEN - self->buf_len;
        if (take > input_len) take = input_len;
        memcpy(&self->buf[self->buf_len], input, take);
        self->buf_len += take;
        input += take;
        input_len -= take;
    }
}

static void chunk_state_output(const chunk_state *self, uint32_t out[16]) {
    uint8_t flags = self->flags | CHUNK_END;
    if (self->blocks_compressed == 0) flags |= CHUNK_START;
    blake3_compress_ref(self->cv, self->buf, self->buf_len, self->chunk_counter, flags, out);
}

static void blake3_hash_internal(const uint32_t key_words[8], uint8_t hasher_flags,
                                 const uint8_t *input, size_t input_len, uint8_t out[32]) {
    if (input_len == 0) {
        chunk_state chunk;
        chunk_state_init(&chunk, key_words, 0, (uint8_t)(hasher_flags | ROOT));
        uint32_t out16[16];
        chunk_state_output(&chunk, out16);
        for (int i = 0; i < 8; i++) {
            out[i*4] = (uint8_t)out16[i];
            out[i*4+1] = (uint8_t)(out16[i] >> 8);
            out[i*4+2] = (uint8_t)(out16[i] >> 16);
            out[i*4+3] = (uint8_t)(out16[i] >> 24);
        }
        return;
    }

    uint32_t cv_stack[54][8];
    size_t stack_len = 0;
    uint64_t chunk_counter = 0;

    while (input_len > 0) {
        size_t chunk_len = input_len;
        if (chunk_len > BLAKE3_CHUNK_LEN) chunk_len = BLAKE3_CHUNK_LEN;

        chunk_state chunk;
        chunk_state_init(&chunk, key_words, chunk_counter, hasher_flags);
        chunk_state_update(&chunk, input, chunk_len);

        input += chunk_len;
        input_len -= chunk_len;
        chunk_counter++;

        uint32_t chunk_out[16];
        if (input_len == 0 && stack_len == 0) {
            // Single chunk root
            chunk.flags |= ROOT;
            chunk_state_output(&chunk, chunk_out);
            for (int i = 0; i < 8; i++) {
                out[i*4] = (uint8_t)chunk_out[i];
                out[i*4+1] = (uint8_t)(chunk_out[i] >> 8);
                out[i*4+2] = (uint8_t)(chunk_out[i] >> 16);
                out[i*4+3] = (uint8_t)(chunk_out[i] >> 24);
            }
            return;
        }

        chunk_state_output(&chunk, chunk_out);
        uint32_t current_cv[8];
        memcpy(current_cv, chunk_out, sizeof(current_cv));

        uint64_t total_chunks = chunk_counter;
        while ((total_chunks & 1) == 0) {
            uint8_t parent_block[64];
            memcpy(&parent_block[0], cv_stack[stack_len - 1], 32);
            memcpy(&parent_block[32], current_cv, 32);
            stack_len--;

            uint8_t flags = (uint8_t)(PARENT | hasher_flags);
            if (input_len == 0 && stack_len == 0) {
                flags |= ROOT;
            }
            uint32_t parent_out[16];
            blake3_compress_ref(key_words, parent_block, BLAKE3_BLOCK_LEN, 0, flags, parent_out);
            memcpy(current_cv, parent_out, sizeof(current_cv));
            total_chunks >>= 1;
        }

        memcpy(cv_stack[stack_len], current_cv, sizeof(current_cv));
        stack_len++;
    }

    while (stack_len > 1) {
        uint8_t parent_block[64];
        memcpy(&parent_block[0], cv_stack[stack_len - 2], 32);
        memcpy(&parent_block[32], cv_stack[stack_len - 1], 32);
        stack_len -= 2;

        uint8_t flags = (uint8_t)(PARENT | hasher_flags);
        if (stack_len == 0) flags |= ROOT;
        uint32_t parent_out[16];
        blake3_compress_ref(key_words, parent_block, BLAKE3_BLOCK_LEN, 0, flags, parent_out);
        memcpy(cv_stack[stack_len], parent_out, sizeof(uint32_t) * 8);
        stack_len++;
    }

    for (int i = 0; i < 8; i++) {
        out[i*4] = (uint8_t)cv_stack[0][i];
        out[i*4+1] = (uint8_t)(cv_stack[0][i] >> 8);
        out[i*4+2] = (uint8_t)(cv_stack[0][i] >> 16);
        out[i*4+3] = (uint8_t)(cv_stack[0][i] >> 24);
    }
}

void blake3_hash_ref(const uint8_t *input, size_t input_len, uint8_t out[32]) {
    uint32_t key_words[8];
    memcpy(key_words, IV, sizeof(IV));
    blake3_hash_internal(key_words, 0, input, input_len, out);
}

void blake3_derive_key_ref(const uint8_t *context, size_t context_len,
                           const uint8_t *key_material, size_t key_material_len,
                           uint8_t out[32]) {
    uint8_t context_key[32];
    uint32_t key_words[8];
    memcpy(key_words, IV, sizeof(IV));
    blake3_hash_internal(key_words, DERIVE_KEY_CONTEXT, context, context_len, context_key);
    for (int i = 0; i < 8; i++) {
        key_words[i] = (uint32_t)context_key[i * 4] |
                       ((uint32_t)context_key[i * 4 + 1] << 8) |
                       ((uint32_t)context_key[i * 4 + 2] << 16) |
                       ((uint32_t)context_key[i * 4 + 3] << 24);
    }
    blake3_hash_internal(key_words, DERIVE_KEY_MATERIAL, key_material, key_material_len, out);
}
