/* SPDX-License-Identifier: Apache-2.0 OR MIT
 *
 * Oracle de parité du noyau de compression transpilé.
 *
 * Trois implémentations sont confrontées sur le même tirage de blocs :
 *   - blake3_compress_ref     (blake3_ref.c, référence scalaire indépendante) ;
 *   - blake3archtsim_compress_scalar (repli de la source transpilée) ;
 *   - blake3archtsim_compress_avx2   (noyau vectoriel de la source transpilée).
 *
 * La sortie porte le pli des seize mots produits par la référence. Toute
 * divergence d'une des deux autres implémentations arrête le programme avec un
 * code non nul : le pli n'est imprimé que si la parité est bit-exacte.
 */

#include "blake3_ref.h"
#include "../blake3archtsim.h"
#include <stdio.h>
#include <string.h>

static uint64_t xs_state = 0x853c49e6748fea9bULL;

static uint64_t xs_next(void) {
    uint64_t v = xs_state;
    v ^= v << 13;
    v ^= v >> 7;
    v ^= v << 17;
    xs_state = v;
    return v;
}

static int cmp16(const uint32_t a[16], const uint32_t b[16]) {
    for (int i = 0; i < 16; i++) {
        if (a[i] != b[i]) {
            return i;
        }
    }
    return -1;
}

int main(void) {
    uint64_t fold = 0;
    uint8_t block[BLAKE3_BLOCK_LEN];
    uint32_t cv[8];
    uint32_t want[16];
    uint32_t got_scalar[16];
    uint32_t got_avx2[16];

    for (int iter = 0; iter < 4096; iter++) {
        for (int i = 0; i < BLAKE3_BLOCK_LEN; i++) {
            block[i] = (uint8_t)xs_next();
        }
        for (int i = 0; i < 8; i++) {
            cv[i] = (uint32_t)xs_next();
        }
        uint8_t block_len = (uint8_t)(xs_next() % (BLAKE3_BLOCK_LEN + 1));
        uint64_t counter = xs_next();
        uint8_t flags = (uint8_t)(xs_next() & 0x7f);

        blake3_compress_ref(cv, block, block_len, counter, flags, want);
        blake3archtsim_compress_scalar(cv, block, block_len, counter, flags, got_scalar);
        blake3archtsim_compress_avx2(cv, block, block_len, counter, flags, got_avx2);

        int d = cmp16(want, got_scalar);
        if (d >= 0) {
            fprintf(stderr, "iter %d: scalaire diverge au mot %d: %08x != %08x\n",
                    iter, d, got_scalar[d], want[d]);
            return 1;
        }
        d = cmp16(want, got_avx2);
        if (d >= 0) {
            fprintf(stderr, "iter %d: avx2 diverge au mot %d: %08x != %08x\n",
                    iter, d, got_avx2[d], want[d]);
            return 2;
        }

        for (int i = 0; i < 16; i++) {
            fold ^= (uint64_t)want[i];
            fold = (fold << 7) | (fold >> 57);
        }
    }

    printf("FOLD_COMPRESS=0x%016llX\n", (unsigned long long)fold);
    printf("PARITE=3/3\n");
    return 0;
}
