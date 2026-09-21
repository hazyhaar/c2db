/* SPDX-License-Identifier: Apache-2.0 OR MIT
 *
 * Compression BLAKE3 d'un bloc, forme vectorielle 4x32 bits.
 *
 * L'état de 16 mots tient dans quatre registres de quatre voies : r0 = état[0..3],
 * r1 = état[4..7], r2 = état[8..11], r3 = état[12..15]. Les quatre appels de la
 * fonction de mélange G d'un demi-tour portent alors sur les mêmes voies de quatre
 * registres distincts, donc s'exécutent en une seule passe vectorielle. Le demi-tour
 * diagonal s'obtient en faisant tourner r1, r2 et r3 d'une, deux et trois voies avant
 * la passe, puis en défaisant la rotation après.
 *
 * Les rotations de BLAKE3 vont vers la DROITE (16, 12, 8, 7). Celles de 16 et de 8
 * bits sont des permutations d'octets et passent par pshufb ; celles de 12 et de 7
 * bits demandent deux décalages et un ou logique.
 *
 * La permutation du message entre deux tours reste scalaire : elle réordonne seize
 * mots sans arithmétique, et la charger en vecteurs coûterait plus de brassage que
 * les quatre constructions de vecteurs qu'elle évite.
 */

#include "blake3archtsim.h"
#include <immintrin.h>

static const uint32_t BLAKE3ARCHTSIM_IV[8] = {
    0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
    0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
};

static const uint8_t BLAKE3ARCHTSIM_MSG_PERMUTATION[16] = {
    2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8
};

/* Rotation à droite de 16 bits d'un mot little-endian : les octets (b0,b1,b2,b3)
 * deviennent (b2,b3,b0,b1). */
static const uint8_t BLAKE3ARCHTSIM_ROT16[16] = {
    2, 3, 0, 1, 6, 7, 4, 5, 10, 11, 8, 9, 14, 15, 12, 13
};

/* Rotation à droite de 8 bits : (b0,b1,b2,b3) deviennent (b1,b2,b3,b0). */
static const uint8_t BLAKE3ARCHTSIM_ROT8[16] = {
    1, 2, 3, 0, 5, 6, 7, 4, 9, 10, 11, 8, 13, 14, 15, 12
};

/* Sonde d'exécution : interroge le processeur, ne présume rien de la cible de
 * compilation. Sous gcc et clang, __builtin_cpu_supports lit le CPUID. */
static int __avx2(void) {
    return __builtin_cpu_supports("avx2") ? 1 : 0;
}

static uint32_t blake3archtsim_rotr32(uint32_t w, uint32_t c) {
    return (w >> c) | (w << (32 - c));
}

static void blake3archtsim_g(uint32_t *state, int a, int b, int c, int d,
                             uint32_t mx, uint32_t my) {
    state[a] = state[a] + state[b] + mx;
    state[d] = blake3archtsim_rotr32(state[d] ^ state[a], 16);
    state[c] = state[c] + state[d];
    state[b] = blake3archtsim_rotr32(state[b] ^ state[c], 12);
    state[a] = state[a] + state[b] + my;
    state[d] = blake3archtsim_rotr32(state[d] ^ state[a], 8);
    state[c] = state[c] + state[d];
    state[b] = blake3archtsim_rotr32(state[b] ^ state[c], 7);
}

void blake3archtsim_compress_scalar(const uint32_t cv[8],
                                    const uint8_t block[BLAKE3ARCHTSIM_BLOCK_LEN],
                                    uint8_t block_len,
                                    uint64_t counter,
                                    uint8_t flags,
                                    uint32_t out[16]) {
    uint32_t state[16];
    uint32_t m[16];
    uint32_t nm[16];
    int i;
    int r;

    for (i = 0; i < 8; i = i + 1) {
        state[i] = cv[i];
    }
    for (i = 0; i < 4; i = i + 1) {
        state[8 + i] = BLAKE3ARCHTSIM_IV[i];
    }
    state[12] = (uint32_t)counter;
    state[13] = (uint32_t)(counter >> 32);
    state[14] = (uint32_t)block_len;
    state[15] = (uint32_t)flags;

    for (i = 0; i < 16; i = i + 1) {
        m[i] = (uint32_t)block[i * 4] |
               ((uint32_t)block[i * 4 + 1] << 8) |
               ((uint32_t)block[i * 4 + 2] << 16) |
               ((uint32_t)block[i * 4 + 3] << 24);
    }

    for (r = 0; r < 7; r = r + 1) {
        blake3archtsim_g(state, 0, 4, 8, 12, m[0], m[1]);
        blake3archtsim_g(state, 1, 5, 9, 13, m[2], m[3]);
        blake3archtsim_g(state, 2, 6, 10, 14, m[4], m[5]);
        blake3archtsim_g(state, 3, 7, 11, 15, m[6], m[7]);
        blake3archtsim_g(state, 0, 5, 10, 15, m[8], m[9]);
        blake3archtsim_g(state, 1, 6, 11, 12, m[10], m[11]);
        blake3archtsim_g(state, 2, 7, 8, 13, m[12], m[13]);
        blake3archtsim_g(state, 3, 4, 9, 14, m[14], m[15]);
        for (i = 0; i < 16; i = i + 1) {
            nm[i] = m[BLAKE3ARCHTSIM_MSG_PERMUTATION[i]];
        }
        for (i = 0; i < 16; i = i + 1) {
            m[i] = nm[i];
        }
    }

    for (i = 0; i < 8; i = i + 1) {
        out[i] = state[i] ^ state[i + 8];
        out[i + 8] = state[i + 8] ^ cv[i];
    }
}

__attribute__((target("avx2")))
void blake3archtsim_compress_avx2(const uint32_t cv[8],
                                  const uint8_t block[BLAKE3ARCHTSIM_BLOCK_LEN],
                                  uint8_t block_len,
                                  uint64_t counter,
                                  uint8_t flags,
                                  uint32_t out[16]) {
    __m128i rot16;
    __m128i rot8;
    __m128i c0;
    __m128i c1;
    __m128i r0;
    __m128i r1;
    __m128i r2;
    __m128i r3;
    __m128i mx;
    __m128i my;
    __m128i t;
    uint32_t m[16];
    uint32_t nm[16];
    int i;
    int r;

    rot16 = _mm_loadu_si128((const __m128i *)BLAKE3ARCHTSIM_ROT16);
    rot8 = _mm_loadu_si128((const __m128i *)BLAKE3ARCHTSIM_ROT8);

    for (i = 0; i < 16; i = i + 1) {
        m[i] = (uint32_t)block[i * 4] |
               ((uint32_t)block[i * 4 + 1] << 8) |
               ((uint32_t)block[i * 4 + 2] << 16) |
               ((uint32_t)block[i * 4 + 3] << 24);
    }

    c0 = _mm_loadu_si128((const __m128i *)(cv + 0));
    c1 = _mm_loadu_si128((const __m128i *)(cv + 4));
    r0 = c0;
    r1 = c1;
    r2 = _mm_set_epi32((int)BLAKE3ARCHTSIM_IV[3], (int)BLAKE3ARCHTSIM_IV[2],
                       (int)BLAKE3ARCHTSIM_IV[1], (int)BLAKE3ARCHTSIM_IV[0]);
    r3 = _mm_set_epi32((int)(uint32_t)flags, (int)(uint32_t)block_len,
                       (int)(uint32_t)(counter >> 32), (int)(uint32_t)counter);

    for (r = 0; r < 7; r = r + 1) {
        /* Demi-tour colonne : la voie k porte la colonne (k, 4+k, 8+k, 12+k). */
        mx = _mm_set_epi32((int)m[6], (int)m[4], (int)m[2], (int)m[0]);
        my = _mm_set_epi32((int)m[7], (int)m[5], (int)m[3], (int)m[1]);
        r0 = _mm_add_epi32(_mm_add_epi32(r0, r1), mx);
        r3 = _mm_shuffle_epi8(_mm_xor_si128(r3, r0), rot16);
        r2 = _mm_add_epi32(r2, r3);
        t = _mm_xor_si128(r1, r2);
        r1 = _mm_or_si128(_mm_srli_epi32(t, 12), _mm_slli_epi32(t, 20));
        r0 = _mm_add_epi32(_mm_add_epi32(r0, r1), my);
        r3 = _mm_shuffle_epi8(_mm_xor_si128(r3, r0), rot8);
        r2 = _mm_add_epi32(r2, r3);
        t = _mm_xor_si128(r1, r2);
        r1 = _mm_or_si128(_mm_srli_epi32(t, 7), _mm_slli_epi32(t, 25));

        /* Mise en diagonale : r1 d'une voie, r2 de deux, r3 de trois. */
        r1 = _mm_shuffle_epi32(r1, 0x39);
        r2 = _mm_shuffle_epi32(r2, 0x4e);
        r3 = _mm_shuffle_epi32(r3, 0x93);

        mx = _mm_set_epi32((int)m[14], (int)m[12], (int)m[10], (int)m[8]);
        my = _mm_set_epi32((int)m[15], (int)m[13], (int)m[11], (int)m[9]);
        r0 = _mm_add_epi32(_mm_add_epi32(r0, r1), mx);
        r3 = _mm_shuffle_epi8(_mm_xor_si128(r3, r0), rot16);
        r2 = _mm_add_epi32(r2, r3);
        t = _mm_xor_si128(r1, r2);
        r1 = _mm_or_si128(_mm_srli_epi32(t, 12), _mm_slli_epi32(t, 20));
        r0 = _mm_add_epi32(_mm_add_epi32(r0, r1), my);
        r3 = _mm_shuffle_epi8(_mm_xor_si128(r3, r0), rot8);
        r2 = _mm_add_epi32(r2, r3);
        t = _mm_xor_si128(r1, r2);
        r1 = _mm_or_si128(_mm_srli_epi32(t, 7), _mm_slli_epi32(t, 25));

        /* Retour en colonnes. */
        r1 = _mm_shuffle_epi32(r1, 0x93);
        r2 = _mm_shuffle_epi32(r2, 0x4e);
        r3 = _mm_shuffle_epi32(r3, 0x39);

        for (i = 0; i < 16; i = i + 1) {
            nm[i] = m[BLAKE3ARCHTSIM_MSG_PERMUTATION[i]];
        }
        for (i = 0; i < 16; i = i + 1) {
            m[i] = nm[i];
        }
    }

    _mm_storeu_si128((__m128i *)(out + 0), _mm_xor_si128(r0, r2));
    _mm_storeu_si128((__m128i *)(out + 4), _mm_xor_si128(r1, r3));
    _mm_storeu_si128((__m128i *)(out + 8), _mm_xor_si128(r2, c0));
    _mm_storeu_si128((__m128i *)(out + 12), _mm_xor_si128(r3, c1));
}

void blake3archtsim_compress(const uint32_t cv[8],
                             const uint8_t block[BLAKE3ARCHTSIM_BLOCK_LEN],
                             uint8_t block_len,
                             uint64_t counter,
                             uint8_t flags,
                             uint32_t out[16]) {
    if (__avx2()) {
        blake3archtsim_compress_avx2(cv, block, block_len, counter, flags, out);
    } else {
        blake3archtsim_compress_scalar(cv, block, block_len, counter, flags, out);
    }
}
