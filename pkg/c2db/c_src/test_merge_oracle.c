#include "db_btree.h"
#include <stdio.h>
#include <stdint.h>

int main(void) {
    uint64_t a[4] = {1, 3, 5, 9};
    uint64_t b[3] = {2, 3, 8};
    uint64_t o[8];
    uint64_t n = db_bt_merge_u64(a, 4, b, 3, o, 8);
    printf("N=%llu", (unsigned long long)n);
    {
        uint64_t i;
        i = 0;
        while (i < n) {
            printf(" %llu", (unsigned long long)o[i]);
            i = i + 1;
        }
    }
    printf("\n");
    return 0;
}
