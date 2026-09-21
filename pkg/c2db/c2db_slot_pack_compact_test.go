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

func buildTestFragmentedPage() []byte {
	page := make([]byte, 16384)
	page[20] = 1 // TYPE_LEAF

	// On insère 5 slots
	// Slot 0: "k1", vivant (vlen=10)
	// Slot 1: "k2", tombstone car vlen=0
	// Slot 2: "k3", vivant (vlen=8)
	// Slot 3: "k4", tombstone car kind=1 (IDKindDel)
	// Slot 4: "k5", vivant (vlen=4)

	type testCell struct {
		key      string
		val      string
		isDel    bool
		zeroVlen bool
	}

	cells := []testCell{
		{key: "k1", val: "0123456789", isDel: false, zeroVlen: false},
		{key: "k2", val: "", isDel: false, zeroVlen: true},
		{key: "k3", val: "abcdefgh", isDel: false, zeroVlen: false},
		{key: "k4", val: "deadbeef", isDel: true, zeroVlen: false},
		{key: "k5", val: "1234", isDel: false, zeroVlen: false},
	}

	nslots := uint16(len(cells))
	binary.LittleEndian.PutUint16(page[22:24], nslots)
	freeLo := uint16(64 + nslots*2)
	binary.LittleEndian.PutUint16(page[24:26], freeLo)

	curHi := uint16(16384)
	for i, c := range cells {
		klen := uint16(len(c.key))
		vlen := uint16(len(c.val))
		if c.zeroVlen {
			vlen = 0
		}
		// Structure cellule: 4 (klen+vlen) + klen + 16 (id) + vlen
		cellLen := 4 + klen + 16 + vlen
		curHi -= cellLen

		// Slot table
		slotAddr := 64 + i*2
		binary.LittleEndian.PutUint16(page[slotAddr:slotAddr+2], curHi)

		// Cellule
		binary.LittleEndian.PutUint16(page[curHi:curHi+2], klen)
		binary.LittleEndian.PutUint16(page[curHi+2:curHi+4], vlen)
		copy(page[curHi+4:curHi+4+klen], []byte(c.key))

		// ID 16 octets
		idOff := curHi + 4 + klen
		for b := range 16 {
			page[int(idOff)+b] = byte(b + 1)
		}
		if c.isDel {
			// kind = 1 (IDKindDel) -> kindShift 58 -> id[8] bits: (1 << 2) | 0x80 (variant)
			page[idOff+8] = (1 << 2) | 0x80
		} else {
			// kind = 0 (IDKindPut)
			page[idOff+8] = 0x80
		}

		if vlen > 0 {
			copy(page[idOff+16:idOff+16+vlen], []byte(c.val))
		}
	}
	binary.LittleEndian.PutUint16(page[26:28], curHi)
	return page
}

func TestC2dbSlotPackCompact_Functional(t *testing.T) {
	page := buildTestFragmentedPage()
	scratch := make([]byte, 16384)

	res := C2db_slot_pack_compact(page, 16384, scratch, 16384, 16, 1)
	if res.Ok != 1 {
		t.Fatalf("C2db_slot_pack_compact a échoué")
	}
	if res.Slots_before != 5 {
		t.Fatalf("Slots_before = %d, attendu 5", res.Slots_before)
	}
	if res.Slots_after != 3 {
		t.Fatalf("Slots_after = %d, attendu 3 (2 tombstones éliminés)", res.Slots_after)
	}
	if res.Bytes_freed == 0 {
		t.Fatalf("Bytes_freed = 0, de l'espace aurait dû être libéré")
	}

	// Contrôle de l'en-tête
	newNslots := binary.LittleEndian.Uint16(page[22:24])
	if newNslots != 3 {
		t.Fatalf("En-tête nslots = %d, attendu 3", newNslots)
	}
	newFreeLo := binary.LittleEndian.Uint16(page[24:26])
	if newFreeLo != 64+3*2 {
		t.Fatalf("En-tête free_lo = %d, attendu %d", newFreeLo, 64+3*2)
	}

	// Contrôle des 3 clés vivantes restantes
	expectedKeys := []string{"k1", "k3", "k5"}
	expectedVals := []string{"0123456789", "abcdefgh", "1234"}
	for i := range 3 {
		slotAddr := 64 + i*2
		cellOff := binary.LittleEndian.Uint16(page[slotAddr : slotAddr+2])
		klen := binary.LittleEndian.Uint16(page[cellOff : cellOff+2])
		vlen := binary.LittleEndian.Uint16(page[cellOff+2 : cellOff+4])
		key := string(page[cellOff+4 : cellOff+4+klen])
		val := string(page[cellOff+4+klen+16 : cellOff+4+klen+16+vlen])

		if key != expectedKeys[i] {
			t.Fatalf("Slot %d: clé = %q, attendu %q", i, key, expectedKeys[i])
		}
		if val != expectedVals[i] {
			t.Fatalf("Slot %d: val = %q, attendu %q", i, val, expectedVals[i])
		}
	}
}

func TestC2dbSlotPackCompact_ZeroAlloc(t *testing.T) {
	page := buildTestFragmentedPage()
	scratch := make([]byte, 16384)

	allocs := testing.AllocsPerRun(100, func() {
		_ = C2db_slot_pack_compact(page, 16384, scratch, 16384, 16, 1)
	})
	if allocs != 0 {
		t.Fatalf("C2db_slot_pack_compact allocs/op = %.2f, attendu 0", allocs)
	}
}

func TestC2dbSlotPackCompact_VsCOracle(t *testing.T) {
	pageGo := buildTestFragmentedPage()
	scratchGo := make([]byte, 16384)
	resGo := C2db_slot_pack_compact(pageGo, 16384, scratchGo, 16384, 16, 1)

	// Rejet hostile : scratch trop petit (1024 octets)
	scratchHostile := make([]byte, 1024)
	resHostileGo := C2db_slot_pack_compact(pageGo, 16384, scratchHostile, 1024, 16, 1)

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "compact_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>
#include <string.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/c2db_slot_pack_compact.c"

void build_test_page(uint8_t *page) {
    memset(page, 0, 16384);
    page[20] = 1; // TYPE_LEAF
    page[22] = 5; // nslots = 5
    page[23] = 0;
    uint16_t free_lo = 64 + 5 * 2;
    page[24] = (uint8_t)(free_lo & 0xFF);
    page[25] = (uint8_t)((free_lo >> 8) & 0xFF);

    const char *keys[5] = {"k1", "k2", "k3", "k4", "k5"};
    const char *vals[5] = {"0123456789", "", "abcdefgh", "deadbeef", "1234"};
    int is_del[5] = {0, 0, 0, 1, 0};
    int zero_vlen[5] = {0, 1, 0, 0, 0};

    uint16_t cur_hi = 16384;
    for (int i = 0; i < 5; i++) {
        uint16_t klen = (uint16_t)strlen(keys[i]);
        uint16_t vlen = zero_vlen[i] ? 0 : (uint16_t)strlen(vals[i]);
        uint16_t clen = 4 + klen + 16 + vlen;
        cur_hi -= clen;

        uint16_t saddr = 64 + i * 2;
        page[saddr] = (uint8_t)(cur_hi & 0xFF);
        page[saddr + 1] = (uint8_t)((cur_hi >> 8) & 0xFF);

        page[cur_hi] = (uint8_t)(klen & 0xFF);
        page[cur_hi + 1] = (uint8_t)((klen >> 8) & 0xFF);
        page[cur_hi + 2] = (uint8_t)(vlen & 0xFF);
        page[cur_hi + 3] = (uint8_t)((vlen >> 8) & 0xFF);
        memcpy(page + cur_hi + 4, keys[i], klen);

        uint16_t id_off = cur_hi + 4 + klen;
        for (int b = 0; b < 16; b++) {
            page[id_off + b] = (uint8_t)(b + 1);
        }
        if (is_del[i]) {
            page[id_off + 8] = (1 << 2) | 0x80;
        } else {
            page[id_off + 8] = 0x80;
        }

        if (vlen > 0) {
            memcpy(page + id_off + 16, vals[i], vlen);
        }
    }
    page[26] = (uint8_t)(cur_hi & 0xFF);
    page[27] = (uint8_t)((cur_hi >> 8) & 0xFF);
}

int main(void) {
    uint8_t page[16384];
    uint8_t scratch[16384];
    build_test_page(page);

    c2db_compact_result_t r = c2db_slot_pack_compact(page, 16384, scratch, 16384, 16, 1);

    uint8_t scratch_hostile[1024];
    c2db_compact_result_t rh = c2db_slot_pack_compact(page, 16384, scratch_hostile, 1024, 16, 1);

    printf("%lu %lu %lu %u | %lu %lu %lu %u\n",
        r.slots_before, r.slots_after, r.bytes_freed, r.ok,
        rh.slots_before, rh.slots_after, rh.bytes_freed, rh.ok);

    for (uint64_t i = 0; i < r.slots_after; i++) {
        uint16_t saddr = 64 + (uint16_t)(i * 2);
        uint16_t coff = (uint16_t)(page[saddr] | (page[saddr + 1] << 8));
        uint16_t klen = (uint16_t)(page[coff] | (page[coff + 1] << 8));
        uint16_t vlen = (uint16_t)(page[coff + 2] | (page[coff + 3] << 8));
        printf("[%lu: off=%u klen=%u vlen=%u] ", i, coff, klen, vlen);
    }
    printf("\n");
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

	var goEntriesBuf bytes.Buffer
	for i := range resGo.Slots_after {
		slotAddr := 64 + i*2
		cellOff := binary.LittleEndian.Uint16(pageGo[slotAddr : slotAddr+2])
		klen := binary.LittleEndian.Uint16(pageGo[cellOff : cellOff+2])
		vlen := binary.LittleEndian.Uint16(pageGo[cellOff+2 : cellOff+4])
		fmt.Fprintf(&goEntriesBuf, "[%d: off=%d klen=%d vlen=%d] ", i, cellOff, klen, vlen)
	}

	expectedHeader := fmt.Sprintf("%d %d %d %d | %d %d %d %d",
		resGo.Slots_before, resGo.Slots_after, resGo.Bytes_freed, resGo.Ok,
		resHostileGo.Slots_before, resHostileGo.Slots_after, resHostileGo.Bytes_freed, resHostileGo.Ok)

	cLines := bytes.Split(bytes.TrimSpace(outC), []byte("\n"))
	if len(cLines) < 2 {
		t.Fatalf("Sortie C tronquée: %q", string(outC))
	}
	if string(cLines[0]) != expectedHeader {
		t.Fatalf("PARITÉ EN-TÊTE ROMPUE : Go=%q, C=%q", expectedHeader, string(cLines[0]))
	}
	if string(bytes.TrimSpace(cLines[1])) != string(bytes.TrimSpace(goEntriesBuf.Bytes())) {
		t.Fatalf("PARITÉ ENTRÉES ROMPUE : Go=%q, C=%q", goEntriesBuf.String(), string(cLines[1]))
	}
	t.Logf("Oracle gcc -O2 parité bit-exacte slot_pack_compact validée : %s", bytes.TrimSpace(cLines[0]))
}
