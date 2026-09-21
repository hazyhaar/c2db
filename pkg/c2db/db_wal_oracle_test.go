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

func dbWalFillKnown(block []byte) {
	for i := range block {
		block[i] = byte(i*17 + 3)
	}
}

func TestDbWal_VsCOracle(t *testing.T) {
	const walN = 4096
	payload := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	id := make([]byte, 16)
	for i := range id {
		id[i] = byte(0xA0 + i)
	}

	block := make([]byte, walN)
	dbWalFillKnown(block)
	tag0, tag15 := block[4080], block[4095]

	if Db_wal_pack(block, 4095, id, 0, payload, uint64(len(payload))) != 0 {
		t.Fatalf("pack n=4095 doit rejeter")
	}
	if block[4080] != tag0 || block[4095] != tag15 {
		t.Fatalf("pack n=4095 a touché le tag")
	}

	pok := Db_wal_pack(block, walN, id, 0, payload, uint64(len(payload)))
	if pok != 1 {
		t.Fatalf("pack nominal rejeté")
	}
	if binary.LittleEndian.Uint32(block[0:4]) != WALMagic {
		t.Fatalf("magic Go=%#08x", binary.LittleEndian.Uint32(block[0:4]))
	}
	if binary.LittleEndian.Uint32(block[4:8]) != uint32(len(payload)) {
		t.Fatalf("len Go=%d", binary.LittleEndian.Uint32(block[4:8]))
	}
	if block[24] != 0 {
		t.Fatalf("type Go=%d", block[24])
	}
	if !bytes.Equal(block[8:24], id) {
		t.Fatalf("id pack mismatch")
	}
	if !bytes.Equal(block[25:25+len(payload)], payload) {
		t.Fatalf("payload pack mismatch")
	}
	for i := 25 + len(payload); i < 4080; i++ {
		if block[i] != 0 {
			t.Fatalf("pad non nul à %d: %d", i, block[i])
		}
	}
	if block[4080] != tag0 || block[4095] != tag15 {
		t.Fatalf("pack a touché le tag : got %d %d want %d %d", block[4080], block[4095], tag0, tag15)
	}

	idOut := make([]byte, 16)
	d := Db_wal_unpack(block, walN, idOut)
	if d.Ok != 1 || d.Magic != WALMagic || d.Len_ != uint32(len(payload)) || d.Typ != 0 || d.Plen != uint32(len(payload)) {
		t.Fatalf("unpack nominal: %+v", d)
	}
	if !bytes.Equal(idOut, id) {
		t.Fatalf("unpack id mismatch")
	}

	dBadN := Db_wal_unpack(block, 4095, make([]byte, 16))
	if dBadN.Ok != 0 {
		t.Fatalf("unpack n=4095 doit rejeter, ok=%d", dBadN.Ok)
	}
	pokBadN := Db_wal_pack(append([]byte(nil), block...), 4095, id, 0, payload, uint64(len(payload)))

	blockMagic := append([]byte(nil), block...)
	blockMagic[0] ^= 0xFF
	dMagic := Db_wal_unpack(blockMagic, walN, make([]byte, 16))
	if dMagic.Ok != 0 {
		t.Fatalf("unpack magic corrompu doit rejeter, ok=%d", dMagic.Ok)
	}
	if dMagic.Magic == WALMagic {
		t.Fatalf("unpack magic corrompu a encore le magique nominal")
	}

	block2 := make([]byte, walN)
	dbWalFillKnown(block2)
	if Db_wal_pack(block2, walN, id, 0, payload, uint64(len(payload))) != 1 {
		t.Fatalf("reprise pack après rejet")
	}
	d2 := Db_wal_unpack(block2, walN, make([]byte, 16))
	if d2.Ok != 1 {
		t.Fatalf("reprise unpack après rejet: ok=%d", d2.Ok)
	}

	if Db_wal_type_ok(8) != 1 || Db_wal_type_ok(9) != 1 || Db_wal_type_ok(10) != 0 {
		t.Fatalf("table de saut type 8=%d 9=%d 10=%d", Db_wal_type_ok(8), Db_wal_type_ok(9), Db_wal_type_ok(10))
	}
	if Db_wal_pack(make([]byte, walN), walN, id, 10, payload, uint64(len(payload))) != 0 {
		t.Fatalf("pack type 10 doit rejeter")
	}

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "db_wal_oracle")
	cSrc := `
#include <stdio.h>
#include <stdint.h>

#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_wal.c"

int main(void) {
    uint8_t block[4096];
    uint8_t id[16];
    uint8_t id_out[16];
    uint8_t payload[8];
    uint8_t id_tmp[16];
    uint64_t i;
    uint8_t pok;
    uint8_t pok_badn;
    uint32_t acc;
    db_wal_desc_t d;
    db_wal_desc_t d_badn;
    db_wal_desc_t d_magic;

    for (i = 0; i < 4096; i = i + 1) {
        block[i] = (uint8_t)(i * 17 + 3);
    }
    for (i = 0; i < 16; i = i + 1) {
        id[i] = (uint8_t)(0xA0 + i);
        id_out[i] = 0;
        id_tmp[i] = 0;
    }
    payload[0] = 0x11; payload[1] = 0x22; payload[2] = 0x33; payload[3] = 0x44;
    payload[4] = 0x55; payload[5] = 0x66; payload[6] = 0x77; payload[7] = 0x88;

    pok_badn = db_wal_pack(block, 4095, id, 0, payload, 8);
    pok = db_wal_pack(block, 4096, id, 0, payload, 8);
    d = db_wal_unpack(block, 4096, id_out);
    d_badn = db_wal_unpack(block, 4095, id_tmp);
    block[0] = (uint8_t)(block[0] ^ 0xFF);
    d_magic = db_wal_unpack(block, 4096, id_tmp);
    block[0] = (uint8_t)(block[0] ^ 0xFF);

    acc = 0;
    for (i = 0; i < 4096; i = i + 1) {
        acc = acc + (uint32_t)block[i];
    }
    printf("%u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %u %llu %llu\n",
        pok, acc,
        block[0], block[1], block[2], block[3],
        (unsigned)(block[4] | ((uint32_t)block[5] << 8) | ((uint32_t)block[6] << 16) | ((uint32_t)block[7] << 24)),
        block[24],
        block[25], block[33],
        block[4079],
        block[4080], block[4095],
        d.ok, d.magic, d.len, d.typ, d.plen,
        id_out[0], id_out[15],
        pok_badn, d_badn.ok, d_magic.ok, d_magic.magic,
        (unsigned long long)db_wal_payload_off(),
        (unsigned long long)db_wal_tag_off());
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

	want := fmt.Sprintf("%d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d\n",
		pok,
		func() uint32 {
			var acc uint32
			for i := 0; i < 4096; i++ {
				acc = acc + uint32(block[i])
			}
			return acc
		}(),
		block[0], block[1], block[2], block[3],
		binary.LittleEndian.Uint32(block[4:8]),
		block[24],
		block[25], block[33],
		block[4079],
		block[4080], block[4095],
		d.Ok, d.Magic, d.Len_, d.Typ, d.Plen,
		idOut[0], idOut[15],
		pokBadN, dBadN.Ok, dMagic.Ok, dMagic.Magic,
		Db_wal_payload_off(), Db_wal_tag_off())

	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(want))) {
		t.Fatalf("PARITÉ ROMPUE C2DB WAL VS GCC -O2 : Go=%q, C=%q", want, string(outC))
	}
	t.Logf("Vecteur nominal : parité bit-exacte gcc -O2 : %s", bytes.TrimSpace(outC))
}

// TestDbWal_RecTxCommit_VsCOracle valide la parité bit-exacte intégrale (4 096 octets sur 4 096)
// et les vecteurs de rejet de RecTxCommit (type 9, len=0, payload=NULL)
// entre le code transpilé sgoiter et un binaire C compilé avec gcc -O2.
func TestDbWal_RecTxCommit_VsCOracle(t *testing.T) {
	const walN = 4096
	var id [16]byte
	for i := range id {
		id[i] = byte(0x70 + i)
	}

	blockGo := make([]byte, walN)
	dbWalFillKnown(blockGo)

	// 1. Vecteur nominal Go
	pokGo := Db_wal_pack(blockGo, walN, id[:], 9, nil, 0)
	if pokGo != 1 {
		t.Fatalf("Go Db_wal_pack(type 9) a échoué: got %d want 1", pokGo)
	}
	var idOutGo [16]byte
	dGo := Db_wal_unpack(blockGo, walN, idOutGo[:])
	if dGo.Ok != 1 || dGo.Typ != 9 || dGo.Len_ != 0 || dGo.Plen != 0 {
		t.Fatalf("Go Db_wal_unpack(type 9): ok=%d typ=%d len=%d plen=%d", dGo.Ok, dGo.Typ, dGo.Len_, dGo.Plen)
	}
	if idOutGo != id {
		t.Fatalf("Go id mismatch")
	}

	// 2. Vecteurs de rejet Go pour RecTxCommit
	if Db_wal_pack(make([]byte, walN), 4095, id[:], 9, nil, 0) != 0 {
		t.Fatalf("Go Db_wal_pack n=4095 doit rejeter")
	}
	if Db_wal_pack(make([]byte, walN), walN, nil, 9, nil, 0) != 0 {
		t.Fatalf("Go Db_wal_pack id=nil doit rejeter")
	}
	dummyPayload := make([]byte, 4056)
	if Db_wal_pack(make([]byte, walN), walN, id[:], 9, dummyPayload, 4056) != 0 {
		t.Fatalf("Go Db_wal_pack plen=4056 doit rejeter")
	}

	// 3. Oracle C gcc -O2 : sérialisation bit-exacte des 4 096 octets et vérification des rejets
	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "db_wal_commit_oracle")
	blockOutFile := filepath.Join(tmpDir, "block_c.bin")
	idOutFile := filepath.Join(tmpDir, "id_c.bin")

	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/db_wal.c"

int main(void) {
    uint8_t block[4096];
    uint8_t id[16];
    uint8_t id_out[16];
    uint8_t dummy[4056];
    uint64_t i;
    uint8_t pok;
    uint8_t r_badn, r_nullid, r_toolong;
    db_wal_desc_t d;
    FILE *f;

    for (i = 0; i < 4096; i = i + 1) {
        block[i] = (uint8_t)(i * 17 + 3);
    }
    for (i = 0; i < 16; i = i + 1) {
        id[i] = (uint8_t)(0x70 + i);
        id_out[i] = 0;
    }

    // Vecteurs de rejet C
    r_badn = db_wal_pack(block, 4095, id, 9, 0, 0);
    r_nullid = db_wal_pack(block, 4096, 0, 9, 0, 0);
    r_toolong = db_wal_pack(block, 4096, id, 9, dummy, 4056);

    // Sérialisation nominale C
    pok = db_wal_pack(block, 4096, id, 9, 0, 0);
    d = db_wal_unpack(block, 4096, id_out);

    f = fopen("%s", "wb");
    if (!f) return 1;
    fwrite(block, 1, 4096, f);
    fclose(f);

    f = fopen("%s", "wb");
    if (!f) return 2;
    fwrite(id_out, 1, 16, f);
    fclose(f);

    printf("%%u %%u %%u %%u %%u %%u %%u %%u %%u\n",
        pok, d.ok, d.magic, d.len, d.typ, d.plen,
        r_badn, r_nullid, r_toolong);
    return 0;
}
`, blockOutFile, idOutFile)

	srcFile := filepath.Join(tmpDir, "main_commit.c")
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

	// 4. Comparaison bit-exacte des 4 096 octets complets du bloc
	blockC, err := os.ReadFile(blockOutFile)
	if err != nil {
		t.Fatalf("Lecture block C: %v", err)
	}
	if len(blockC) != walN {
		t.Fatalf("Taille block C: got %d want %d", len(blockC), walN)
	}
	if !bytes.Equal(blockGo, blockC) {
		t.Fatalf("PARITÉ ROMPUE C2DB WAL COMMIT VS GCC -O2 : les 4096 octets du bloc diffèrent !")
	}

	// 5. Comparaison bit-exacte des 16 octets de l'identifiant unpacké
	idC, err := os.ReadFile(idOutFile)
	if err != nil {
		t.Fatalf("Lecture id C: %v", err)
	}
	if !bytes.Equal(idOutGo[:], idC) {
		t.Fatalf("Identifiant C vs Go diffère")
	}

	// 6. Validation des champs du descripteur et des rejets (doivent tous être 0)
	wantDesc := fmt.Sprintf("%d %d %d %d %d %d 0 0 0\n",
		pokGo, dGo.Ok, dGo.Magic, dGo.Len_, dGo.Typ, dGo.Plen)
	if string(bytes.TrimSpace(outC)) != string(bytes.TrimSpace([]byte(wantDesc))) {
		t.Fatalf("Descripteur ou rejets C diffèrent : Go=%q, C=%q", wantDesc, string(outC))
	}
	t.Logf("RecTxCommit (type 9) : PARITÉ INTÉGRALE 4096/4096 OCTETS ET REJETS VALIDÉS CONTRE GCC -O2")
}
