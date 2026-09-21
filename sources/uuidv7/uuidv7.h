/*
 * uuidv7.h — Spécification et API du module UUIDv7 (RFC 9562) ARCHTIME.
 *
 * Zéro allocation dynamique, zéro CGO au runtime, parité bit-exacte avec Go 1.27.
 */

#ifndef C2SIMD_UUIDV7_H
#define C2SIMD_UUIDV7_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Type universel UUID sur 128 bits (16 octets fixes) */
typedef struct c2bt_uuid {
    uint8_t bytes[16];
} c2bt_uuid_t;

/* Constantes RFC 9562 */
#define UUID_VERSION_7  0x07U
#define UUID_VARIANT_10 0x02U

/*
 * Génération
 */

/* Génère un UUIDv7 à partir d'un timestamp nanoseconde explicite et d'un aléa 64 bits */
void c2bt_uuidv7_compose(uint64_t ts_ns, uint64_t seq_or_rand, c2bt_uuid_t *out_uuid);

/* Génère un UUIDv7 temps réel ultra-rapide (PRNG interne, Lock-Free, < 3 ns) */
void c2bt_uuidv7_fast(c2bt_uuid_t *out_uuid);

/*
 * Formatage & Sérialisation
 */

/* Formate l'UUID en chaîne hexadécimale canonique 36 octets ("xxxxxxxx-xxxx-7xxx-yxxx-xxxxxxxxxxxx")
 * out_str doit avoir une capacité d'au moins 37 octets (36 + '\0').
 * Retourne 36 en cas de succès, -1 si buffer trop court.
 */
int c2bt_uuidv7_format(const c2bt_uuid_t *uuid, char *out_str, size_t max_out);

/*
 * Analyse & Décodage (Passe unique branchless, 0 B/op)
 */

/* Décode une chaîne de 36 octets (ou 32 sans tirets) en UUID binaire */
int c2bt_uuidv7_parse(const char *in_str, size_t in_len, c2bt_uuid_t *out_uuid);

/*
 * Inspecteurs RFC 9562
 */
uint64_t c2bt_uuidv7_get_timestamp_ms(const c2bt_uuid_t *uuid);
uint8_t  c2bt_uuidv7_get_version(const c2bt_uuid_t *uuid);
uint8_t  c2bt_uuidv7_get_variant(const c2bt_uuid_t *uuid);
int      c2bt_uuidv7_is_valid(const c2bt_uuid_t *uuid);
int      c2bt_uuidv7_compare(const c2bt_uuid_t *a, const c2bt_uuid_t *b);

#ifdef __cplusplus
}
#endif

#endif /* C2SIMD_UUIDV7_H */
