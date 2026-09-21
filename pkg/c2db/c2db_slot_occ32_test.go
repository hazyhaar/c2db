package c2db

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func slotOccWant(p [32]byte) uint32 {
	var n uint32
	for i := 0; i < 32; i++ {
		if p[i] != 0 {
			n++
		}
	}
	return n
}

func TestSlotOcc32KAT(t *testing.T) {
	var in [32]byte
	if C2db_slot_occ32(nil) != 0 || C2db_slot_occ32_avx2(nil) != 0 {
		t.Fatal("nil doit renvoyer 0")
	}
	if n := C2db_slot_occ32(in[:]); n != 0 {
		t.Fatalf("vide: %d", n)
	}
	in[0], in[31], in[7] = 1, 0xFF, 2
	if n := C2db_slot_occ32(in[:]); n != 3 {
		t.Fatalf("trois occupés: %d", n)
	}
	for i := range in {
		in[i] = 1
	}
	if n := C2db_slot_occ32(in[:]); n != 32 {
		t.Fatalf("plein: %d", n)
	}
}

func TestSlotOcc32ParityScalarAvx2(t *testing.T) {
	var in [32]byte
	for pass := 0; pass < 8; pass++ {
		for i := 0; i < 32; i++ {
			if (pass+i)&1 == 0 {
				in[i] = byte(pass + i + 1)
			} else {
				in[i] = 0
			}
		}
		s := C2db_slot_occ32(in[:])
		v := C2db_slot_occ32_avx2(in[:])
		w := slotOccWant(in)
		if s != w || v != w {
			t.Fatalf("pass=%d scal=%d avx2=%d want=%d", pass, s, v, w)
		}
	}
}

func TestSlotOcc32ZeroAlloc(t *testing.T) {
	var in [32]byte
	in[1] = 1
	if n := testing.AllocsPerRun(1000, func() { _ = C2db_slot_occ32(in[:]) }); n != 0 {
		t.Fatalf("scalaire allocs=%.2f", n)
	}
	if n := testing.AllocsPerRun(1000, func() { _ = C2db_slot_occ32_avx2(in[:]) }); n != 0 {
		t.Fatalf("avx2 allocs=%.2f", n)
	}
}

func TestSlotOcc32VsCOracle(t *testing.T) {
	var in [32]byte
	in[0], in[2], in[31] = 1, 9, 0x80
	want := slotOccWant(in)
	if g := C2db_slot_occ32(in[:]); g != want {
		t.Fatalf("Go scal=%d want=%d", g, want)
	}
	if g := C2db_slot_occ32_avx2(in[:]); g != want {
		t.Fatalf("Go avx2=%d want=%d", g, want)
	}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "main.c")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/slot_occ32.c"
int main(void) {
    uint8_t in[32] = {0};
    in[0] = 1; in[2] = 9; in[31] = 0x80;
    printf("%%u\n", c2db_slot_occ32(in));
    return 0;
}
`)
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatal(err)
	}
	cBin := filepath.Join(tmpDir, "occ_oracle")
	if out, err := exec.Command("gcc", "-O2", "-o", cBin, srcFile).CombinedOutput(); err != nil {
		t.Fatalf("gcc scal: %v %s", err, out)
	}
	got, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fmt.Sprintf("%d\n", want) {
		t.Fatalf("oracle scal C=%q want=%d", got, want)
	}

	srcSimd := filepath.Join(tmpDir, "simd.c")
	cSimd := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/slot_occ32_simd.c"
int main(void) {
    uint8_t in[32] = {0};
    in[0] = 1; in[2] = 9; in[31] = 0x80;
    printf("%%u\n", c2db_slot_occ32_avx2(in));
    return 0;
}
`)
	if err := os.WriteFile(srcSimd, []byte(cSimd), 0644); err != nil {
		t.Fatal(err)
	}
	cBin2 := filepath.Join(tmpDir, "occ_simd")
	if out, err := exec.Command("gcc", "-O2", "-mavx2", "-o", cBin2, srcSimd).CombinedOutput(); err != nil {
		t.Fatalf("gcc avx2: %v %s", err, out)
	}
	got2, err := exec.Command(cBin2).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != fmt.Sprintf("%d\n", want) {
		t.Fatalf("oracle avx2 C=%q want=%d", got2, want)
	}
}
