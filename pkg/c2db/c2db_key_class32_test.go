package c2db

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func keyClassWant(b byte) byte {
	n := b & 0x0F
	if n <= 9 {
		return 1
	}
	return 2
}

func TestKeyClass32KAT(t *testing.T) {
	var in, out [32]byte
	for i := 0; i < 32; i++ {
		in[i] = byte(i * 17)
	}
	C2db_key_class32(in[:], out[:])
	for i := 0; i < 32; i++ {
		if out[i] != keyClassWant(in[i]) {
			t.Fatalf("i=%d in=%#02x got=%d want=%d", i, in[i], out[i], keyClassWant(in[i]))
		}
	}
}

func TestKeyClass32ZeroAlloc(t *testing.T) {
	var in, out [32]byte
	if n := testing.AllocsPerRun(1000, func() { C2db_key_class32(in[:], out[:]) }); n != 0 {
		t.Fatalf("allocs=%.2f", n)
	}
}

func TestKeyClass32VsCOracle(t *testing.T) {
	var in, out [32]byte
	for i := 0; i < 32; i++ {
		in[i] = byte(i*13 + 3)
	}
	C2db_key_class32(in[:], out[:])
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "main.c")
	cSrc := `
#include <stdio.h>
#include <stdint.h>
#include "/devhoros/c2simd/c2pkg/c2db/c_src/c2db_key_class32.c"
int main(void) {
    uint8_t in[32];
    uint8_t out[32];
    int i;
    for (i = 0; i < 32; i++) {
        in[i] = (uint8_t)(i * 13 + 3);
    }
    c2db_key_class32(in, out);
    for (i = 0; i < 32; i++) {
        printf("%02x", out[i]);
    }
    printf("\n");
    return 0;
}
`
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		t.Fatal(err)
	}
	cBin := filepath.Join(tmpDir, "key_class_oracle")
	if o, err := exec.Command("gcc", "-O2", "-o", cBin, srcFile).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v %s", err, o)
	}
	gotC, err := exec.Command(cBin).Output()
	if err != nil {
		t.Fatal(err)
	}
	wantHex := fmt.Sprintf("%x\n", out[:])
	if string(gotC) != wantHex {
		t.Fatalf("oracle C=%q Go=%q", gotC, wantHex)
	}
	var out2 [32]byte
	C2db_key_class32(in[:], out2[:])
	if !bytes.Equal(out[:], out2[:]) {
		t.Fatal("reprise Go divergente")
	}
}
