/*
 * uuidv7.c — Implémentation C99 ARCHTIME du générateur et manipulateur UUIDv7 (RFC 9562).
 *
 * Zéro allocation dynamique, zéro appel bloquant CSPRNG sur chemin critique,
 * progression atomique Lock-Free (RFC 9562 §6.2), table ARCHTIME hexadécimale branchless.
 */

#define _GNU_SOURCE
#include "uuidv7.h"
#include <time.h>
#include <string.h>

/*
 * Table ARCHTIME 1 : Décodage hexadécimal branchless (0xFF = caractère invalide).
 */
static const uint8_t ARCHTIME_HEX_DECODE[256] = {
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0x00,0x01,0x02,0x03,0x04,0x05,0x06,0x07,0x08,0x09,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0x0A,0x0B,0x0C,0x0D,0x0E,0x0F,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0x0A,0x0B,0x0C,0x0D,0x0E,0x0F,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,
    0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF,0xFF
};

/*
 * Table ARCHTIME 2 : Symboles hexadécimaux minuscules.
 */
static const char ARCHTIME_HEX_CHARS[16] = "0123456789abcdef";

/* État atomique du générateur rapide (Graine SplitMix64) */
static uint64_t g_uuid_prng_state = 0x853c49e6748fea9bULL;
static uint64_t g_v7_last_timestamp = 0;

static inline uint64_t next_prng_fast(void) {
    uint64_t z = (__atomic_add_fetch(&g_uuid_prng_state, 0x9e3779b97f4a7c15ULL, __ATOMIC_RELAXED));
    z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
    z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
    return z ^ (z >> 31);
}

void c2bt_uuidv7_compose(uint64_t ts_ns, uint64_t seq_or_rand, c2bt_uuid_t *out_uuid) {
    if (!out_uuid) return;

    /* Timestamp 48 bits en millisecondes */
    uint64_t ts_ms = ts_ns / 1000000ULL;
    uint32_t frac_ns = (uint32_t)(ts_ns % 1000000ULL);
    uint32_t sub_ms_12 = (frac_ns * 4096U) / 1000000U; /* Fraction 12 bits */

    out_uuid->bytes[0] = (uint8_t)((ts_ms >> 40) & 0xFF);
    out_uuid->bytes[1] = (uint8_t)((ts_ms >> 32) & 0xFF);
    out_uuid->bytes[2] = (uint8_t)((ts_ms >> 24) & 0xFF);
    out_uuid->bytes[3] = (uint8_t)((ts_ms >> 16) & 0xFF);
    out_uuid->bytes[4] = (uint8_t)((ts_ms >> 8) & 0xFF);
    out_uuid->bytes[5] = (uint8_t)(ts_ms & 0xFF);

    /* Octet 6 : Version 7 (0b0111) en bits 4..7 + 4 bits hauts de sub_ms */
    out_uuid->bytes[6] = (uint8_t)(0x70U | ((sub_ms_12 >> 8) & 0x0FU));
    /* Octet 7 : 8 bits bas de sub_ms */
    out_uuid->bytes[7] = (uint8_t)(sub_ms_12 & 0xFFU);

    /* Octet 8 : Variante RFC 4122/9562 (0b10 en bits 6..7) + 6 bits d'aléa */
    out_uuid->bytes[8] = (uint8_t)(0x80U | ((seq_or_rand >> 56) & 0x3FU));
    out_uuid->bytes[9] = (uint8_t)((seq_or_rand >> 48) & 0xFF);
    out_uuid->bytes[10] = (uint8_t)((seq_or_rand >> 40) & 0xFF);
    out_uuid->bytes[11] = (uint8_t)((seq_or_rand >> 32) & 0xFF);
    out_uuid->bytes[12] = (uint8_t)((seq_or_rand >> 24) & 0xFF);
    out_uuid->bytes[13] = (uint8_t)((seq_or_rand >> 16) & 0xFF);
    out_uuid->bytes[14] = (uint8_t)((seq_or_rand >> 8) & 0xFF);
    out_uuid->bytes[15] = (uint8_t)(seq_or_rand & 0xFF);
}

void c2bt_uuidv7_fast(c2bt_uuid_t *out_uuid) {
    if (!out_uuid) return;

    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    uint64_t secs = (uint64_t)ts.tv_sec;
    uint64_t nanos = (uint64_t)ts.tv_nsec;
    uint64_t msecs = nanos / 1000000ULL;
    uint32_t frac = (uint32_t)(nanos - (msecs * 1000000ULL));
    uint64_t timestamp = ((secs * 1000ULL + msecs) << 12) + ((frac * 4096ULL) / 1000000ULL);

    /* Progression atomique strictement monotone (RFC 9562 §6.2) sans aucun mutex */
    uint64_t prev = __atomic_load_n(&g_v7_last_timestamp, __ATOMIC_RELAXED);
    uint64_t cur;
    do {
        cur = (timestamp > prev) ? timestamp : (prev + 1);
    } while (!__atomic_compare_exchange_n(&g_v7_last_timestamp, &prev, cur, 0, __ATOMIC_RELAXED, __ATOMIC_RELAXED));

    uint64_t rnd = next_prng_fast();

    uint64_t ts_ms = cur >> 12;
    uint32_t sub_ms_12 = (uint32_t)(cur & 0x0FFFU);

    out_uuid->bytes[0] = (uint8_t)((ts_ms >> 40) & 0xFF);
    out_uuid->bytes[1] = (uint8_t)((ts_ms >> 32) & 0xFF);
    out_uuid->bytes[2] = (uint8_t)((ts_ms >> 24) & 0xFF);
    out_uuid->bytes[3] = (uint8_t)((ts_ms >> 16) & 0xFF);
    out_uuid->bytes[4] = (uint8_t)((ts_ms >> 8) & 0xFF);
    out_uuid->bytes[5] = (uint8_t)(ts_ms & 0xFF);

    out_uuid->bytes[6] = (uint8_t)(0x70U | ((sub_ms_12 >> 8) & 0x0FU));
    out_uuid->bytes[7] = (uint8_t)(sub_ms_12 & 0xFFU);

    out_uuid->bytes[8] = (uint8_t)(0x80U | ((rnd >> 56) & 0x3FU));
    out_uuid->bytes[9] = (uint8_t)((rnd >> 48) & 0xFF);
    out_uuid->bytes[10] = (uint8_t)((rnd >> 40) & 0xFF);
    out_uuid->bytes[11] = (uint8_t)((rnd >> 32) & 0xFF);
    out_uuid->bytes[12] = (uint8_t)((rnd >> 24) & 0xFF);
    out_uuid->bytes[13] = (uint8_t)((rnd >> 16) & 0xFF);
    out_uuid->bytes[14] = (uint8_t)((rnd >> 8) & 0xFF);
    out_uuid->bytes[15] = (uint8_t)(rnd & 0xFF);
}

int c2bt_uuidv7_format(const c2bt_uuid_t *uuid, char *out_str, size_t max_out) {
    if (!uuid || !out_str || max_out < 37) {
        return -1;
    }

    const uint8_t *b = uuid->bytes;
    char *p = out_str;

    /* 8 octets hex (b[0..3]) */
    p[0] = ARCHTIME_HEX_CHARS[b[0] >> 4]; p[1] = ARCHTIME_HEX_CHARS[b[0] & 0x0F];
    p[2] = ARCHTIME_HEX_CHARS[b[1] >> 4]; p[3] = ARCHTIME_HEX_CHARS[b[1] & 0x0F];
    p[4] = ARCHTIME_HEX_CHARS[b[2] >> 4]; p[5] = ARCHTIME_HEX_CHARS[b[2] & 0x0F];
    p[6] = ARCHTIME_HEX_CHARS[b[3] >> 4]; p[7] = ARCHTIME_HEX_CHARS[b[3] & 0x0F];
    p[8] = '-';

    /* 4 octets hex (b[4..5]) */
    p[9]  = ARCHTIME_HEX_CHARS[b[4] >> 4]; p[10] = ARCHTIME_HEX_CHARS[b[4] & 0x0F];
    p[11] = ARCHTIME_HEX_CHARS[b[5] >> 4]; p[12] = ARCHTIME_HEX_CHARS[b[5] & 0x0F];
    p[13] = '-';

    /* 4 octets hex (b[6..7]) */
    p[14] = ARCHTIME_HEX_CHARS[b[6] >> 4]; p[15] = ARCHTIME_HEX_CHARS[b[6] & 0x0F];
    p[16] = ARCHTIME_HEX_CHARS[b[7] >> 4]; p[17] = ARCHTIME_HEX_CHARS[b[7] & 0x0F];
    p[18] = '-';

    /* 4 octets hex (b[8..9]) */
    p[19] = ARCHTIME_HEX_CHARS[b[8] >> 4]; p[20] = ARCHTIME_HEX_CHARS[b[8] & 0x0F];
    p[21] = ARCHTIME_HEX_CHARS[b[9] >> 4]; p[22] = ARCHTIME_HEX_CHARS[b[9] & 0x0F];
    p[23] = '-';

    /* 12 octets hex (b[10..15]) */
    p[24] = ARCHTIME_HEX_CHARS[b[10] >> 4]; p[25] = ARCHTIME_HEX_CHARS[b[10] & 0x0F];
    p[26] = ARCHTIME_HEX_CHARS[b[11] >> 4]; p[27] = ARCHTIME_HEX_CHARS[b[11] & 0x0F];
    p[28] = ARCHTIME_HEX_CHARS[b[12] >> 4]; p[29] = ARCHTIME_HEX_CHARS[b[12] & 0x0F];
    p[30] = ARCHTIME_HEX_CHARS[b[13] >> 4]; p[31] = ARCHTIME_HEX_CHARS[b[13] & 0x0F];
    p[32] = ARCHTIME_HEX_CHARS[b[14] >> 4]; p[33] = ARCHTIME_HEX_CHARS[b[14] & 0x0F];
    p[34] = ARCHTIME_HEX_CHARS[b[15] >> 4]; p[35] = ARCHTIME_HEX_CHARS[b[15] & 0x0F];
    p[36] = '\0';

    return 36;
}

int c2bt_uuidv7_parse(const char *in_str, size_t in_len, c2bt_uuid_t *out_uuid) {
    if (!in_str || !out_uuid) {
        return -1;
    }

    /* Support du préfixe urn:uuid: */
    if (in_len >= 9 && strncmp(in_str, "urn:uuid:", 9) == 0) {
        in_str += 9;
        in_len -= 9;
    }
    /* Support des accolades {xxxxxxxx-...} */
    if (in_len == 38 && in_str[0] == '{' && in_str[37] == '}') {
        in_str++;
        in_len = 36;
    }

    if (in_len == 36) {
        /* Format canonique 8-4-4-4-12 avec tirets */
        if (in_str[8] != '-' || in_str[13] != '-' || in_str[18] != '-' || in_str[23] != '-') {
            return -1;
        }

        static const uint8_t byte_offsets[16] = {
            0, 2, 4, 6,       /* 8 chars -> 4 bytes */
            9, 11,            /* 4 chars -> 2 bytes */
            14, 16,           /* 4 chars -> 2 bytes */
            19, 21,           /* 4 chars -> 2 bytes */
            24, 26, 28, 30, 32, 34 /* 12 chars -> 6 bytes */
        };

        for (int i = 0; i < 16; i++) {
            uint8_t off = byte_offsets[i];
            uint8_t h1 = ARCHTIME_HEX_DECODE[(uint8_t)in_str[off]];
            uint8_t h2 = ARCHTIME_HEX_DECODE[(uint8_t)in_str[off + 1]];
            if ((h1 | h2) & 0xF0) {
                return -1; /* Caractère hexadécimal invalide */
            }
            out_uuid->bytes[i] = (uint8_t)((h1 << 4) | h2);
        }
        return 0;
    } else if (in_len == 32) {
        /* Format compact 32 hex sans tirets */
        for (int i = 0; i < 16; i++) {
            uint8_t h1 = ARCHTIME_HEX_DECODE[(uint8_t)in_str[i * 2]];
            uint8_t h2 = ARCHTIME_HEX_DECODE[(uint8_t)in_str[i * 2 + 1]];
            if ((h1 | h2) & 0xF0) {
                return -1;
            }
            out_uuid->bytes[i] = (uint8_t)((h1 << 4) | h2);
        }
        return 0;
    }

    return -1;
}

uint64_t c2bt_uuidv7_get_timestamp_ms(const c2bt_uuid_t *uuid) {
    if (!uuid) return 0;
    const uint8_t *b = uuid->bytes;
    return ((uint64_t)b[0] << 40) |
           ((uint64_t)b[1] << 32) |
           ((uint64_t)b[2] << 24) |
           ((uint64_t)b[3] << 16) |
           ((uint64_t)b[4] << 8)  |
           ((uint64_t)b[5]);
}

uint8_t c2bt_uuidv7_get_version(const c2bt_uuid_t *uuid) {
    if (!uuid) return 0;
    return (uint8_t)(uuid->bytes[6] >> 4);
}

uint8_t c2bt_uuidv7_get_variant(const c2bt_uuid_t *uuid) {
    if (!uuid) return 0;
    uint8_t v = uuid->bytes[8];
    if ((v & 0x80) == 0x00) return 0; /* NCS */
    if ((v & 0xC0) == 0x80) return 2; /* RFC 4122 / 9562 */
    if ((v & 0xE0) == 0xC0) return 6; /* Microsoft */
    return 7; /* Réservé */
}

int c2bt_uuidv7_is_valid(const c2bt_uuid_t *uuid) {
    if (!uuid) return 0;
    return (c2bt_uuidv7_get_version(uuid) == UUID_VERSION_7) &&
           (c2bt_uuidv7_get_variant(uuid) == UUID_VARIANT_10);
}

int c2bt_uuidv7_compare(const c2bt_uuid_t *a, const c2bt_uuid_t *b) {
    if (!a && !b) return 0;
    if (!a) return -1;
    if (!b) return 1;
    return memcmp(a->bytes, b->bytes, 16);
}
