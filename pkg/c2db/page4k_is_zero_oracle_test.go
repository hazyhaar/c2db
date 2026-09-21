// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPage4kIsZero_VsCOracle(t *testing.T) {
	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "page4k_oracle")
	srcFile := filepath.Join(tmpDir, "oracle_main.c")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include "%s"

int main(int argc, char **argv) {
    if (argc < 3) {
        return 1;
    }
    const char *mode = argv[1];
    uint64_t n = strtoull(argv[2], NULL, 10);
    if (strcmp(mode, "null") == 0) {
        printf("%%u\n", (unsigned)page4k_is_zero_avx2(NULL, n));
        return 0;
    }
    uint8_t buf[4096];
    memset(buf, 0, sizeof(buf));
    size_t rd = fread(buf, 1, 4096, stdin);
    (void)rd;
    printf("%%u\n", (unsigned)page4k_is_zero_avx2(buf, n));
    return 0;
}
`, filepath.Join("/devhoros/c2simd/c2pkg/c2db/c_src", "page4k_is_zero.c"))

	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("write oracle_main.c: %v", err)
	}

	cmd := exec.Command("gcc", "-O2", "-mavx2", "-o", cBin, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc compilation failed: %v\n%s", err, out)
	}

	callCOracle := func(t *testing.T, mode string, n uint64, data []byte) uint8 {
		t.Helper()
		cmd := exec.Command(cBin, mode, strconv.FormatUint(n, 10))
		if mode != "null" && len(data) > 0 {
			cmd.Stdin = bytes.NewReader(data)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("c oracle execution failed (mode=%s n=%d): %v\n%s", mode, n, err, out)
		}
		val, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 8)
		if err != nil {
			t.Fatalf("parse c oracle output %q: %v", string(out), err)
		}
		return uint8(val)
	}

	// 1. Page de 4096 zéros -> 1
	t.Run("AllZeros", func(t *testing.T) {
		page := make([]byte, 4096)
		gotGo := Page4k_is_zero_avx2(page, 4096)
		gotC := callCOracle(t, "buf", 4096, page)
		if gotGo != 1 {
			t.Fatalf("Go AllZeros got %d, want 1", gotGo)
		}
		if gotC != 1 {
			t.Fatalf("C AllZeros got %d, want 1", gotC)
		}
		if gotGo != gotC {
			t.Fatalf("Parity mismatch AllZeros: Go=%d, C=%d", gotGo, gotC)
		}
	})

	// 2. Page avec 1 octet non-nul à divers offsets (0, 1, 100, 2048, 4095) -> 0
	t.Run("SingleNonZeroByte", func(t *testing.T) {
		offsets := []int{0, 1, 100, 2048, 4095}
		nonZeroValues := []byte{1, 0x7F, 0x80, 0xFF}
		for _, off := range offsets {
			for _, val := range nonZeroValues {
				t.Run(fmt.Sprintf("off_%d_val_%d", off, val), func(t *testing.T) {
					page := make([]byte, 4096)
					page[off] = val
					gotGo := Page4k_is_zero_avx2(page, 4096)
					gotC := callCOracle(t, "buf", 4096, page)
					if gotGo != 0 {
						t.Fatalf("Go offset %d val %d got %d, want 0", off, val, gotGo)
					}
					if gotC != 0 {
						t.Fatalf("C offset %d val %d got %d, want 0", off, val, gotC)
					}
					if gotGo != gotC {
						t.Fatalf("Parity mismatch offset %d val %d: Go=%d, C=%d", off, val, gotGo, gotC)
					}
				})
			}
		}
	})

	// 3. Page aléatoire -> 0
	t.Run("RandomPages", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			page := make([]byte, 4096)
			if _, err := rand.Read(page); err != nil {
				t.Fatal(err)
			}
			gotGo := Page4k_is_zero_avx2(page, 4096)
			gotC := callCOracle(t, "buf", 4096, page)
			if gotGo != 0 {
				t.Fatalf("Go random page %d got %d, want 0", i, gotGo)
			}
			if gotC != 0 {
				t.Fatalf("C random page %d got %d, want 0", i, gotC)
			}
			if gotGo != gotC {
				t.Fatalf("Parity mismatch random page %d: Go=%d, C=%d", i, gotGo, gotC)
			}
		}
	})

	// 4. Tailles invalides (n != 4096) ou pointeur nul -> 0
	t.Run("InvalidSizesAndNull", func(t *testing.T) {
		page := make([]byte, 4096)
		invalidSizes := []uint64{0, 1, 32, 1024, 4095, 4097, 8192}
		for _, n := range invalidSizes {
			t.Run(fmt.Sprintf("size_%d", n), func(t *testing.T) {
				gotGo := Page4k_is_zero_avx2(page, n)
				gotC := callCOracle(t, "buf", n, page)
				if gotGo != 0 {
					t.Fatalf("Go invalid size %d got %d, want 0", n, gotGo)
				}
				if gotC != 0 {
					t.Fatalf("C invalid size %d got %d, want 0", n, gotC)
				}
				if gotGo != gotC {
					t.Fatalf("Parity mismatch invalid size %d: Go=%d, C=%d", n, gotGo, gotC)
				}
			})
		}

		// Pointeur nul
		t.Run("NullPointer", func(t *testing.T) {
			gotGo := Page4k_is_zero_avx2(nil, 4096)
			gotC := callCOracle(t, "null", 4096, nil)
			if gotGo != 0 {
				t.Fatalf("Go nil page got %d, want 0", gotGo)
			}
			if gotC != 0 {
				t.Fatalf("C NULL page got %d, want 0", gotC)
			}
			if gotGo != gotC {
				t.Fatalf("Parity mismatch NULL page: Go=%d, C=%d", gotGo, gotC)
			}

			gotGo0 := Page4k_is_zero_avx2(nil, 0)
			gotC0 := callCOracle(t, "null", 0, nil)
			if gotGo0 != 0 || gotC0 != 0 {
				t.Fatalf("nil/NULL with n=0 want 0 got Go=%d, C=%d", gotGo0, gotC0)
			}
		})
	})
}
