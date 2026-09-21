package c2lz4

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildOracleLZ4(t *testing.T, tmpDir, srcC, srcH string) (string, string) {
	cCode := fmt.Sprintf(`#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include "%s"
#include "%s"

int main(int argc, char **argv) {
    if (argc < 4) return 1;
    const char *mode = argv[1];
    const char *in_path = argv[2];
    const char *out_path = argv[3];

    FILE *fin = fopen(in_path, "rb");
    if (!fin) return 2;
    fseek(fin, 0, SEEK_END);
    long in_size = ftell(fin);
    fseek(fin, 0, SEEK_SET);

    uint8_t *in_buf = (uint8_t *)malloc(in_size);
    if (fread(in_buf, 1, in_size, fin) != (size_t)in_size) {
        fclose(fin);
        free(in_buf);
        return 3;
    }
    fclose(fin);

    int out_max = in_size * 2 + 65536;
    uint8_t *out_buf = (uint8_t *)malloc(out_max);

    int res = 0;
    if (mode[0] == 'c') {
        res = lz4_compress_fast(in_buf, (int)in_size, out_buf, out_max);
    } else {
        res = lz4_decompress_safe(in_buf, (int)in_size, out_buf, out_max);
    }

    if (res <= 0) {
        free(in_buf);
        free(out_buf);
        return 4;
    }

    FILE *fout = fopen(out_path, "wb");
    if (!fout) {
        free(in_buf);
        free(out_buf);
        return 5;
    }
    fwrite(out_buf, 1, res, fout);
    fclose(fout);
    free(in_buf);
    free(out_buf);
    return 0;
}
`, srcH, srcC)

	mainPath := filepath.Join(tmpDir, "oracle_main.c")
	if err := os.WriteFile(mainPath, []byte(cCode), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(tmpDir, "oracle_lz4")
	cmd := exec.Command("gcc", "-std=c99", "-Wall", "-Wextra", "-Werror", "-O2", "-o", bin, mainPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Compilation gcc oracle: %v\n%s", err, out)
	}
	return bin, mainPath
}

func TestLZ4VsCOracle(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc non disponible")
	}

	srcC, err := filepath.Abs("c2lz4.c")
	if err != nil {
		t.Fatal(err)
	}
	srcH, err := filepath.Abs("c2lz4.h")
	if err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	binOracle, _ := buildOracleLZ4(t, tmpDir, srcC, srcH)

	testCases := []struct {
		name string
		data []byte
	}{
		{
			name: "SmallText",
			data: []byte("Hello world! Hello world! Hello world!"),
		},
		{
			name: "RepeatedPattern_1KB",
			data: bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 25),
		},
		{
			name: "Random_4KB",
			data: func() []byte {
				rng := rand.New(rand.NewSource(42))
				b := make([]byte, 4096)
				rng.Read(b)
				return b
			}(),
		},
		{
			name: "AllZeros_8KB",
			data: make([]byte, 8192),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			inPath := filepath.Join(tmpDir, tc.name+".raw")
			if err := os.WriteFile(inPath, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}

			// 1. Compression par l'oracle C
			outCompressedC := filepath.Join(tmpDir, tc.name+".lz4.c")
			cmdC := exec.Command(binOracle, "c", inPath, outCompressedC)
			if out, err := cmdC.CombinedOutput(); err != nil {
				t.Fatalf("Oracle C compress: %v\n%s", err, out)
			}
			compC, err := os.ReadFile(outCompressedC)
			if err != nil {
				t.Fatal(err)
			}

			// 2. Compression par Go
			dstGo := make([]byte, len(tc.data)*2+65536)
			nGo := Compress(tc.data, dstGo)
			if nGo <= 0 {
				t.Fatalf("Go compress a échoué")
			}
			compGo := dstGo[:nGo]

			// 3. Décompression croisée : décompresser compGo avec l'oracle C
			inCompGoPath := filepath.Join(tmpDir, tc.name+".lz4.go")
			if err := os.WriteFile(inCompGoPath, compGo, 0o644); err != nil {
				t.Fatal(err)
			}
			outDecompC := filepath.Join(tmpDir, tc.name+".decomp.c")
			cmdDecompC := exec.Command(binOracle, "d", inCompGoPath, outDecompC)
			if out, err := cmdDecompC.CombinedOutput(); err != nil {
				t.Fatalf("Oracle C decompress du flux Go: %v\n%s", err, out)
			}
			decompC, err := os.ReadFile(outDecompC)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decompC, tc.data) {
				t.Fatalf("Oracle C n'a pas restitué les données originales depuis flux Go")
			}

			// 4. Décompression par Go depuis compC
			decompGo := make([]byte, len(tc.data)+1024)
			nDecompGo, ok := Decompress(compC, decompGo)
			if !ok || nDecompGo != len(tc.data) {
				t.Fatalf("Go decompress a échoué depuis flux C")
			}
			if !bytes.Equal(decompGo[:nDecompGo], tc.data) {
				t.Fatalf("Go decompress n'a pas restitué les données originales")
			}
		})
	}
}
