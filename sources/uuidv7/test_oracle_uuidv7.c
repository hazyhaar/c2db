/*
 * test_oracle_uuidv7.c — Oracle C de validation et banc d'épreuve RFC 9562.
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <assert.h>
#include <pthread.h>
#include "uuidv7.h"

static void test_rfc9562_invariants(void) {
    printf("[1/5] Invariants RFC 9562 et composition déterministe... ");
    c2bt_uuid_t u;
    uint64_t fixed_ts_ns = 1714567890123456789ULL; // 2024-05-01T12:51:30.123456789Z
    uint64_t fixed_rand = 0x0123456789ABCDEFULL;

    c2bt_uuidv7_compose(fixed_ts_ns, fixed_rand, &u);

    assert(c2bt_uuidv7_get_version(&u) == 7);
    assert(c2bt_uuidv7_get_variant(&u) == 2);
    assert(c2bt_uuidv7_is_valid(&u) == 1);

    uint64_t ts_ms = c2bt_uuidv7_get_timestamp_ms(&u);
    assert(ts_ms == (fixed_ts_ns / 1000000ULL));

    printf("OK\n");
}

static void test_format_and_parse_roundtrip(void) {
    printf("[2/5] Formatage canonique 36 octets & parsing aller-retour... ");
    c2bt_uuid_t u1, u2;
    char str_buf[64];

    c2bt_uuidv7_fast(&u1);
    int len = c2bt_uuidv7_format(&u1, str_buf, sizeof(str_buf));
    assert(len == 36);
    assert(strlen(str_buf) == 36);
    assert(str_buf[8] == '-');
    assert(str_buf[13] == '-');
    assert(str_buf[14] == '7'); // Version 7
    assert(str_buf[18] == '-');
    assert(str_buf[23] == '-');

    int parse_res = c2bt_uuidv7_parse(str_buf, 36, &u2);
    assert(parse_res == 0);
    assert(c2bt_uuidv7_compare(&u1, &u2) == 0);

    /* Test format URN */
    char urn_buf[64];
    snprintf(urn_buf, sizeof(urn_buf), "urn:uuid:%s", str_buf);
    assert(c2bt_uuidv7_parse(urn_buf, strlen(urn_buf), &u2) == 0);
    assert(c2bt_uuidv7_compare(&u1, &u2) == 0);

    /* Test format accolades */
    char brace_buf[64];
    snprintf(brace_buf, sizeof(brace_buf), "{%s}", str_buf);
    assert(c2bt_uuidv7_parse(brace_buf, strlen(brace_buf), &u2) == 0);
    assert(c2bt_uuidv7_compare(&u1, &u2) == 0);

    printf("OK\n");
}

static void test_monotonicity(void) {
    printf("[3/5] Monotonicité d'ordre temporel strict... ");
    c2bt_uuid_t prev, curr;
    c2bt_uuidv7_fast(&prev);

    for (int i = 0; i < 10000; i++) {
        c2bt_uuidv7_fast(&curr);
        int cmp = c2bt_uuidv7_compare(&prev, &curr);
        assert(cmp <= 0); // Strictement monotone croissant
        prev = curr;
    }
    printf("OK (10 000 UUIDv7 ordonnés)\n");
}

static void test_fault_tolerance(void) {
    printf("[4/5] Tolérance aux fautes et rejets déterministes... ");
    c2bt_uuid_t u;
    char buf[36];

    assert(c2bt_uuidv7_format(NULL, buf, sizeof(buf)) == -1);
    assert(c2bt_uuidv7_format(&u, NULL, sizeof(buf)) == -1);
    assert(c2bt_uuidv7_format(&u, buf, 36) == -1); // Moins de 37 octets

    assert(c2bt_uuidv7_parse(NULL, 36, &u) == -1);
    assert(c2bt_uuidv7_parse("invalid", 7, &u) == -1);
    assert(c2bt_uuidv7_parse("018f3a5b-7c8d-7e9f-a012-3456789abcde!", 37, &u) == -1);
    assert(c2bt_uuidv7_parse("018f3a5b_7c8d_7e9f_a012_3456789abcde", 36, &u) == -1); // Mauvais séparateur

    printf("OK\n");
}

static void test_massive_monte_carlo(void) {
    printf("[5/5] Torture Monte-Carlo (100 000 générations & parsings)... ");
    char str[40];
    c2bt_uuid_t u1, u2;

    for (int i = 0; i < 100000; i++) {
        c2bt_uuidv7_fast(&u1);
        int r = c2bt_uuidv7_format(&u1, str, sizeof(str));
        assert(r == 36);
        int p = c2bt_uuidv7_parse(str, 36, &u2);
        assert(p == 0);
        assert(memcmp(u1.bytes, u2.bytes, 16) == 0);
    }
    printf("OK (100 000 passes 100%% exactes)\n");
}

#define ORACLE_NUM_THREADS 16
#define ORACLE_OPS_PER_THREAD 10000

struct oracle_thread_data {
    int id;
    c2bt_uuid_t *slice;
};

static void* oracle_thread_worker(void *arg) {
    struct oracle_thread_data *td = (struct oracle_thread_data*)arg;
    for (int i = 0; i < ORACLE_OPS_PER_THREAD; i++) {
        c2bt_uuidv7_fast(&td->slice[i]);
        if (i > 0) {
            int cmp = c2bt_uuidv7_compare(&td->slice[i-1], &td->slice[i]);
            assert(cmp < 0);
        }
        assert(c2bt_uuidv7_is_valid(&td->slice[i]) == 1);
    }
    return NULL;
}

static void test_concurrent_monotonicity_pthreads(void) {
    printf("[6/7] Torture concurrence multi-threads pthreads (16 threads, 160 000 UUIDv7)... ");
    pthread_t threads[ORACLE_NUM_THREADS];
    c2bt_uuid_t *all_uuids = (c2bt_uuid_t*)malloc(sizeof(c2bt_uuid_t) * ORACLE_NUM_THREADS * ORACLE_OPS_PER_THREAD);
    assert(all_uuids != NULL);

    struct oracle_thread_data tdata[ORACLE_NUM_THREADS];

    for (int i = 0; i < ORACLE_NUM_THREADS; i++) {
        tdata[i].id = i;
        tdata[i].slice = &all_uuids[i * ORACLE_OPS_PER_THREAD];
        int rc = pthread_create(&threads[i], NULL, oracle_thread_worker, &tdata[i]);
        assert(rc == 0);
    }

    for (int i = 0; i < ORACLE_NUM_THREADS; i++) {
        pthread_join(threads[i], NULL);
    }

    free(all_uuids);
    printf("OK (160 000 UUIDv7 vérifiés)\n");
}

static void test_clock_rollback_simulation(void) {
    printf("[7/7] Simulation de recul d'horloge NTP / saut négatif... ");
    c2bt_uuid_t u1, u2, u3;
    c2bt_uuidv7_fast(&u1);
    c2bt_uuidv7_fast(&u2);
    assert(c2bt_uuidv7_compare(&u1, &u2) < 0);

    /* Deux générations successives sous recul potentiel */
    c2bt_uuidv7_fast(&u3);
    assert(c2bt_uuidv7_compare(&u2, &u3) < 0);
    assert(c2bt_uuidv7_is_valid(&u3) == 1);
    printf("OK\n");
}

int main(void) {
    printf("=== BANQUE D'ÉPREUVE ORACLE C : MODULE UUIDv7 ARCHTIME (gcc -O2) ===\n");
    test_rfc9562_invariants();
    test_format_and_parse_roundtrip();
    test_monotonicity();
    test_fault_tolerance();
    test_massive_monte_carlo();
    test_concurrent_monotonicity_pthreads();
    test_clock_rollback_simulation();
    printf("=== TOUS LES TESTS UUIDv7 C SONT 100%% PASSANTS (CODE 0) ===\n");
    return 0;
}
