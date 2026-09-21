package c2db

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func walClassWant(op byte) byte {
	n := op & 0x0F
	if n >= 1 && n <= 7 {
		return n
	}
	return 0
}

func walClassKAT32() (in, want [32]byte) {
	in = [32]byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F,
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x80,
		0x81, 0xF2, 0x08, 0xFF, byte(RecPut), byte(RecDel), 0x03, 0x07,
	}
	for i := 0; i < 32; i++ {
		want[i] = walClassWant(in[i])
	}
	return in, want
}

func TestWalClassKAT(t *testing.T) {
	in, want := walClassKAT32()
	var out [32]byte
	C2db_wal_class32(in[:], out[:])
	if out != want {
		t.Fatalf("KAT scalaire: got %x want %x", out, want)
	}
	if out[28] != byte(RecPut) || out[29] != byte(RecDel) {
		t.Fatalf("LUT non alignée RecType: put=%d del=%d", out[28], out[29])
	}
	if out[0] != 0 || out[8] != 0 || out[23] != 0 || out[27] != 0 {
		t.Fatalf("rejet invalide manqué: %x", out)
	}
	in2, want2 := walClassKAT32()
	var out2 [32]byte
	C2db_wal_class32(in2[:], out2[:])
	if out2 != want2 {
		t.Fatalf("reprise après KAT: got %x want %x", out2, want2)
	}
}

func TestWalClassZeroAlloc(t *testing.T) {
	var in, out [32]byte
	in, _ = walClassKAT32()
	allocs := testing.AllocsPerRun(1000, func() {
		C2db_wal_class32(in[:], out[:])
	})
	if allocs != 0 {
		t.Fatalf("scalaire allocs/op = %.2f, want 0", allocs)
	}
	allocsAvx := testing.AllocsPerRun(1000, func() {
		C2db_wal_class32_avx2(in[:], out[:])
	})
	if allocsAvx != 0 {
		t.Fatalf("avx2 allocs/op = %.2f, want 0", allocsAvx)
	}
}

func TestWalClassParityScalarAvx2(t *testing.T) {
	in, want := walClassKAT32()
	var outS, outV [32]byte
	C2db_wal_class32(in[:], outS[:])
	C2db_wal_class32_avx2(in[:], outV[:])
	if outS != want {
		t.Fatalf("scalaire hors KAT: got %x want %x", outS, want)
	}
	if outS != outV {
		t.Fatalf("parité scalaire vs avx2 rompue: scal=%x avx2=%x", outS, outV)
	}
	var sweep [32]byte
	for pass := 0; pass < 8; pass++ {
		for i := 0; i < 32; i++ {
			sweep[i] = byte(pass*32 + i)
		}
		C2db_wal_class32(sweep[:], outS[:])
		C2db_wal_class32_avx2(sweep[:], outV[:])
		if outS != outV {
			t.Fatalf("parité balayage pass=%d: scal=%x avx2=%x", pass, outS, outV)
		}
		for i := 0; i < 32; i++ {
			if outS[i] != walClassWant(sweep[i]) {
				t.Fatalf("classe pass=%d i=%d op=%#02x got=%d want=%d", pass, i, sweep[i], outS[i], walClassWant(sweep[i]))
			}
		}
	}
}

func TestWalClassVsCOracle(t *testing.T) {
	in, want := walClassKAT32()
	var out [32]byte
	C2db_wal_class32(in[:], out[:])
	if out != want {
		t.Fatalf("Go hors KAT avant oracle: got %x want %x", out, want)
	}

	tmpDir := t.TempDir()
	cBin := filepath.Join(tmpDir, "wal_class_oracle")
	cSrc := fmt.Sprintf(`
#include <stdio.h>
#include <stdint.h>
#include "/devhoros/c2simd/sources/c2archtsim/c2db_wal_class32.c"

int main(void) {
    uint8_t in[32] = {
        0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
        0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F,
        0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x80,
        0x81, 0xF2, 0x08, 0xFF, %d, %d, 0x03, 0x07
    };
    uint8_t out[32];
    int i;
    c2db_wal_class32(in, out);
    for (i = 0; i < 32; i++) {
        printf("%%02x", out[i]);
    }
    printf("\n");
    return 0;
}
`, RecPut, RecDel)
	srcFile := filepath.Join(tmpDir, "main.c")
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatalf("écriture source C: %v", err)
	}
	cmd := exec.Command("gcc", "-O2", "-o", cBin, srcFile)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc -O2: %v out=%s", err, o)
	}
	outC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatalf("oracle C: %v", err)
	}
	gotC := string(bytes.TrimSpace(outC))
	wantHex := fmt.Sprintf("%x", want[:])
	if gotC != wantHex {
		t.Fatalf("PARITÉ ROMPUE vs gcc -O2: Go=%s C=%s", wantHex, gotC)
	}
	var out2 [32]byte
	C2db_wal_class32(in[:], out2[:])
	if out2 != want {
		t.Fatalf("reprise Go après oracle: got %x want %x", out2, want)
	}
}
