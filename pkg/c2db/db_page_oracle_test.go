// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func dbPageFillKnown(page []byte) {
	for i := range page {
		page[i] = byte(i*17 + 3)
	}
	page[28] = 0xEF
	page[29] = 0xBE
	page[30] = 0xAD
	page[31] = 0xDE
}

func TestDbPage_VsCOracle(t *testing.T) {
	const pageN = 16384

	{
		page := make([]byte, pageN)
		dbPageFillKnown(page)

		crcGo := Db_page_crc32c(page, pageN)
		if crcGo == 0xDEADBEEF {
			t.Fatalf("Db_page_crc32c relit encore le champ à l'offset 28 (0xDEADBEEF)")
		}
		wantBody := crc32.Checksum(page[64:pageN], crc32.MakeTable(crc32.Castagnoli))
		if crcGo != wantBody {
			t.Fatalf("CRC Go=%#08x, Castagnoli corps=%#08x", crcGo, wantBody)
		}
		pageHdr := append([]byte(nil), page...)
		pageHdr[28] ^= 0xFF
		if Db_page_crc32c(pageHdr, pageN) != crcGo {
			t.Fatalf("une mutation de l'offset 28 a changé le CRC : le corps n'est plus le seul domaine")
		}

		pageStore := append([]byte(nil), page...)
		if Db_page_crc32c_store(pageStore, pageN) != 1 {
			t.Fatalf("Db_page_crc32c_store a rejeté une page nominale")
		}
		stored := binary.LittleEndian.Uint32(pageStore[28:32])
		if stored != crcGo {
			t.Fatalf("store écrit %#08x, calculé %#08x", stored, crcGo)
		}
		if Db_page_crc32c(pageStore, pageN) != crcGo {
			t.Fatalf("store a muté le corps : CRC après écriture du champ")
		}

		tmpDir := t.TempDir()
		cBin := filepath.Join(tmpDir, "db_page_oracle")
		cSrc := `
#include <stdio.h>
#include <stdint.h>

#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_page.c"

int main(void) {
    uint8_t page[16384];
    uint64_t i;
    uint32_t crc;
    uint32_t crc_bad_n;
    uint32_t crc_tamper;

    for (i = 0; i < 16384; i = i + 1) {
        page[i] = (uint8_t)(i * 17 + 3);
    }
    page[28] = 0xEF;
    page[29] = 0xBE;
    page[30] = 0xAD;
    page[31] = 0xDE;

    crc = db_page_crc32c(page, 16384);
    crc_bad_n = db_page_crc32c(page, 16383);
    page[100] = (uint8_t)(page[100] ^ 1u);
    crc_tamper = db_page_crc32c(page, 16384);
    printf("%u %u %u\n", crc, crc_bad_n, crc_tamper);
    return 0;
}
`
		srcFile := filepath.Join(tmpDir, "main.c")
		if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
			t.Fatalf("Échec écriture source C: %v", err)
		}
		cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Échec compilation gcc: %v, out=%s", err, out)
		}
		outC, err := exec.Command(cBin).Output()
		if err != nil {
			t.Fatalf("Échec exécution binaire C oracle: %v", err)
		}

		pageTamper := append([]byte(nil), page...)
		pageTamper[100] ^= 1
		crcTamperGo := Db_page_crc32c(pageTamper, pageN)
		crcBadNGo := Db_page_crc32c(page, 16383)
		expectedOutput := fmt.Sprintf("%d %d %d\n", crcGo, crcBadNGo, crcTamperGo)
		if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(expectedOutput))) {
			t.Fatalf("PARITÉ ROMPUE C2DB PAGE CRC VS GCC -O2 : Go=%q, C=%q", expectedOutput, string(outC))
		}
		t.Logf("Vecteur nominal : parité bit-exacte gcc -O2 : %s", bytes.TrimSpace(outC))
	}

	{
		page := make([]byte, pageN)
		dbPageFillKnown(page)
		crcNom := Db_page_crc32c(page, pageN)
		if crcNom == 0 {
			t.Fatalf("CRC nominal nul, le vecteur de rejet n'est pas discriminant")
		}
		if got := Db_page_crc32c(page, 16383); got != 0 {
			t.Fatalf("n=16383 doit renvoyer 0, obtenu %#08x", got)
		}
		if got := Db_page_crc32c(page, 0); got != 0 {
			t.Fatalf("n=0 doit renvoyer 0, obtenu %#08x", got)
		}
		page[100] ^= 1
		crcTamper := Db_page_crc32c(page, pageN)
		if crcTamper == crcNom {
			t.Fatalf("tamper du corps à l'offset 100 : CRC inchangé %#08x", crcTamper)
		}
		if crcTamper == 0xDEADBEEF {
			t.Fatalf("tamper du corps : CRC encore égal au champ 28")
		}
	}
}
