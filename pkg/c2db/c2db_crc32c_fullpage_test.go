// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func fillFullPageKnown(page []byte) {
	for i := range page {
		page[i] = byte(i*31 + 7)
	}
	page[28] = 0x12
	page[29] = 0x34
	page[30] = 0x56
	page[31] = 0x78
}

func TestC2dbCRC32CFullpage_ZeroAlloc(t *testing.T) {
	page := make([]byte, 16384)
	fillFullPageKnown(page)

	allocs := testing.AllocsPerRun(100, func() {
		_ = C2db_crc32c_fullpage(page, 16384)
	})
	if allocs != 0 {
		t.Fatalf("C2db_crc32c_fullpage allocs/op = %.2f, attendu 0", allocs)
	}

	allocsStore := testing.AllocsPerRun(100, func() {
		_ = C2db_crc32c_fullpage_store(page, 16384)
	})
	if allocsStore != 0 {
		t.Fatalf("C2db_crc32c_fullpage_store allocs/op = %.2f, attendu 0", allocsStore)
	}
}

func TestC2dbCRC32CFullpage_MaskingAndCoverage(t *testing.T) {
	const pageN = 16384
	page := make([]byte, pageN)
	fillFullPageKnown(page)

	crcRef := C2db_crc32c_fullpage(page, pageN)
	if crcRef == 0 {
		t.Fatalf("CRC32-C pleine page nul")
	}

	// Invariant 1 : La mutation du champ CRC (octets 28..31) NE DOIT PAS modifier le CRC calculé
	pageMasked := append([]byte(nil), page...)
	for off := 28; off < 32; off++ {
		pageMasked[off] ^= 0xFF
		crcMut := C2db_crc32c_fullpage(pageMasked, pageN)
		if crcMut != crcRef {
			t.Fatalf("Mutation offset %d a changé le CRC: %#08x != %#08x (masquage défaillant)", off, crcMut, crcRef)
		}
	}

	// Invariant 2 : La mutation de l'en-tête (octets 0..27, trou de 64 octets comblé) DOIT modifier le CRC
	pageHdr := append([]byte(nil), page...)
	pageHdr[10] ^= 0x5A
	if crcHdr := C2db_crc32c_fullpage(pageHdr, pageN); crcHdr == crcRef {
		t.Fatalf("Mutation en-tête offset 10 non détectée : trou de couverture résiduel")
	}

	// Invariant 3 : La mutation du corps (octets 64..16383) DOIT modifier le CRC
	pageBody := append([]byte(nil), page...)
	pageBody[1024] ^= 0xA5
	if crcBody := C2db_crc32c_fullpage(pageBody, pageN); crcBody == crcRef {
		t.Fatalf("Mutation corps offset 1024 non détectée")
	}

	// Invariant 4 : Store et relecture
	pageStore := append([]byte(nil), page...)
	if ok := C2db_crc32c_fullpage_store(pageStore, pageN); ok != 1 {
		t.Fatalf("C2db_crc32c_fullpage_store a échoué")
	}
	storedVal := binary.LittleEndian.Uint32(pageStore[28:32])
	if storedVal != crcRef {
		t.Fatalf("Valeur stockée %#08x != calculée %#08x", storedVal, crcRef)
	}
	if crcAfter := C2db_crc32c_fullpage(pageStore, pageN); crcAfter != crcRef {
		t.Fatalf("CRC après store altéré: %#08x != %#08x", crcAfter, crcRef)
	}
}

func TestC2dbCRC32CFullpage_VsCOracle(t *testing.T) {
	const pageN = 16384
	page := make([]byte, pageN)
	fillFullPageKnown(page)

	crcGoNominal := C2db_crc32c_fullpage(page, pageN)
	crcGoBadLen := C2db_crc32c_fullpage(page, 16383)
	crcGoNull := C2db_crc32c_fullpage(nil, pageN)

	pageTampered := append([]byte(nil), page...)
	pageTampered[500] ^= 0x3C
	crcGoTampered := C2db_crc32c_fullpage(pageTampered, pageN)

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "crc32c_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/c2db_crc32c_fullpage.c"

int main(void) {
    uint8_t page[16384];
    uint64_t i;
    uint32_t crc_nom, crc_bad_len, crc_null, crc_tampered;

    for (i = 0; i < 16384; i = i + 1) {
        page[i] = (uint8_t)(i * 31 + 7);
    }
    page[28] = 0x12;
    page[29] = 0x34;
    page[30] = 0x56;
    page[31] = 0x78;

    crc_nom = c2db_crc32c_fullpage(page, 16384);
    crc_bad_len = c2db_crc32c_fullpage(page, 16383);
    crc_null = c2db_crc32c_fullpage(NULL, 16384);

    page[500] ^= 0x3C;
    crc_tampered = c2db_crc32c_fullpage(page, 16384);

    printf("%u %u %u %u\n", crc_nom, crc_bad_len, crc_null, crc_tampered);
    return 0;
}
`
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("Échec écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Échec compilation gcc -O2: %v, out=%s", err, out)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("Échec exécution binaire C oracle: %v", err)
	}

	expectedOutput := fmt.Sprintf("%d %d %d %d\n", crcGoNominal, crcGoBadLen, crcGoNull, crcGoTampered)
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(expectedOutput))) {
		t.Fatalf("PARITÉ ROMPUE C2DB CRC32C FULLPAGE VS GCC -O2 : Go=%q, C=%q", expectedOutput, string(outC))
	}
	t.Logf("Oracle gcc -O2 parité bit-exacte validée : %s", bytes.TrimSpace(outC))
}
