//go:build ignore

#include "c2lz4.h"

int lz4_decompress_safe(const uint8_t *src, int src_len, uint8_t *dst, int dst_len) {
    if (!src || src_len <= 0 || !dst || dst_len <= 0) {
        return -1;
    }

    int ip = 0;
    int op = 0;

    while (ip < src_len) {
        uint8_t token = src[ip++];
        int literal_len = (token >> 4) & 0x0F;

        if (literal_len == 15) {
            while (ip < src_len) {
                uint8_t s = src[ip++];
                literal_len += s;
                if (s != 255) {
                    break;
                }
            }
        }

        if (literal_len < 0 || op + literal_len > dst_len || ip + literal_len > src_len) {
            return -2;
        }

        int i;
        for (i = 0; i < literal_len; i++) {
            dst[op + i] = src[ip + i];
        }
        op += literal_len;
        ip += literal_len;

        if (ip >= src_len) {
            break;
        }

        if (ip + 2 > src_len) {
            return -3;
        }

        uint16_t offset = (uint16_t)src[ip] | ((uint16_t)src[ip + 1] << 8);
        ip += 2;

        if (offset == 0 || (int)offset > op) {
            return -4;
        }

        int match_len = (token & 0x0F) + 4;
        if (match_len == 19) {
            while (ip < src_len) {
                uint8_t s = src[ip++];
                match_len += s;
                if (s != 255) {
                    break;
                }
            }
        }

        if (match_len < 0 || op + match_len > dst_len) {
            return -5;
        }

        int match_src = op - (int)offset;
        for (i = 0; i < match_len; i++) {
            dst[op + i] = dst[match_src + i];
        }
        op += match_len;
    }

    return op;
}

int lz4_compress_fast(const uint8_t *src, int src_len, uint8_t *dst, int dst_max_len) {
    if (!src || src_len <= 0 || !dst || dst_max_len <= 0) {
        return 0;
    }

    int ip = 0;
    int op = 0;
    int anchor = 0;

    int hash_table[4096];
    int i;
    for (i = 0; i < 4096; i++) {
        hash_table[i] = -1;
    }

    while (ip + 4 <= src_len) {
        uint32_t val = (uint32_t)src[ip] | ((uint32_t)src[ip + 1] << 8) | ((uint32_t)src[ip + 2] << 16) | ((uint32_t)src[ip + 3] << 24);
        uint32_t h = (val * 2654435761U) >> 20;
        int match_pos = hash_table[h];
        hash_table[h] = ip;

        if (match_pos >= 0 && (ip - match_pos) < 65536 && match_pos + 4 <= src_len) {
            uint32_t match_val = (uint32_t)src[match_pos] | ((uint32_t)src[match_pos + 1] << 8) | ((uint32_t)src[match_pos + 2] << 16) | ((uint32_t)src[match_pos + 3] << 24);
            if (val == match_val) {
                /* Match found */
                int match_len = 4;
                while (ip + match_len < src_len && match_pos + match_len < ip && src[ip + match_len] == src[match_pos + match_len]) {
                    match_len++;
                }

                int literal_len = ip - anchor;
                int token_lit = literal_len < 15 ? literal_len : 15;
                int token_match = (match_len - 4) < 15 ? (match_len - 4) : 15;
                uint8_t token = (uint8_t)((token_lit << 4) | token_match);

                if (op >= dst_max_len) return 0;
                dst[op++] = token;

                if (token_lit == 15) {
                    int rem_lit = literal_len - 15;
                    while (rem_lit >= 255) {
                        if (op >= dst_max_len) return 0;
                        dst[op++] = 255;
                        rem_lit -= 255;
                    }
                    if (op >= dst_max_len) return 0;
                    dst[op++] = (uint8_t)rem_lit;
                }

                for (i = 0; i < literal_len; i++) {
                    if (op >= dst_max_len) return 0;
                    dst[op++] = src[anchor + i];
                }

                uint16_t offset = (uint16_t)(ip - match_pos);
                if (op + 2 > dst_max_len) return 0;
                dst[op++] = (uint8_t)(offset & 0xFF);
                dst[op++] = (uint8_t)((offset >> 8) & 0xFF);

                if (token_match == 15) {
                    int rem_match = (match_len - 4) - 15;
                    while (rem_match >= 255) {
                        if (op >= dst_max_len) return 0;
                        dst[op++] = 255;
                        rem_match -= 255;
                    }
                    if (op >= dst_max_len) return 0;
                    dst[op++] = (uint8_t)rem_match;
                }

                ip += match_len;
                anchor = ip;
                continue;
            }
        }
        ip++;
    }

    /* Trailing literals */
    int literal_len = src_len - anchor;
    if (literal_len > 0) {
        int token_lit = literal_len < 15 ? literal_len : 15;
        uint8_t token = (uint8_t)(token_lit << 4);
        if (op >= dst_max_len) return 0;
        dst[op++] = token;

        if (token_lit == 15) {
            int rem_lit = literal_len - 15;
            while (rem_lit >= 255) {
                if (op >= dst_max_len) return 0;
                dst[op++] = 255;
                rem_lit -= 255;
            }
            if (op >= dst_max_len) return 0;
            dst[op++] = (uint8_t)rem_lit;
        }

        for (i = 0; i < literal_len; i++) {
            if (op >= dst_max_len) return 0;
            dst[op++] = src[anchor + i];
        }
    }

    return op;
}
