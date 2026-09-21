package blake3archtsim

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sourceDir localise le répertoire des oracles C, quel que soit le point
// d'entrée du module de test.
func sourceDir(t *testing.T, marker string) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "sources", "blake3archtsim"),
		filepath.Join("..", "..", "c2simd", "sources", "blake3archtsim"),
		filepath.Join("sources", "blake3archtsim"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, marker)); err == nil {
			return c
		}
	}
	t.Fatalf("oracle C %s introuvable dans: %v", marker, candidates)
	return ""
}

// compressOracleFold rejoue en Go le tirage de l'oracle C et rend le pli des
// seize mots produits par Blake3archtsim_compress.
func compressOracleFold(compress func(cv []uint32, block []byte, blockLen byte, counter uint64, flags byte, out []uint32)) uint64 {
	rng := xorshift{s: 0x853c49e6748fea9b}
	var fold uint64
	var block [BlockSize]byte
	var cv [8]uint32
	var out [16]uint32

	for iter := 0; iter < 4096; iter++ {
		for i := 0; i < BlockSize; i++ {
			block[i] = byte(rng.next())
		}
		for i := 0; i < 8; i++ {
			cv[i] = uint32(rng.next())
		}
		blockLen := byte(rng.next() % (BlockSize + 1))
		counter := rng.next()
		flags := byte(rng.next() & 0x7f)

		compress(cv[:], block[:], blockLen, counter, flags, out[:])

		for i := 0; i < 16; i++ {
			fold ^= uint64(out[i])
			fold = (fold << 7) | (fold >> 57)
		}
	}
	return fold
}

// TestCompressVsCOracle confronte le noyau transpilé au binaire gcc -O2 de la
// source dont il est issu, lui-même confronté dans le même programme à
// l'implémentation de référence blake3_ref.c. L'oracle n'imprime son pli que
// si ses trois implémentations concordent bit à bit.
func TestCompressVsCOracle(t *testing.T) {
	srcDir := sourceDir(t, "test_blake3_compress_oracle.c")
	flatSrc := filepath.Join(srcDir, "..", "blake3archtsim.c")
	if _, err := os.Stat(flatSrc); err != nil {
		t.Fatalf("source transpilée introuvable: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "test_blake3_compress_oracle")
	cmd := exec.Command("gcc", "-O2", "-Wall", "-Wextra", "-Werror",
		"-fsanitize=address,undefined", "-I", srcDir,
		filepath.Join(srcDir, "test_blake3_compress_oracle.c"),
		filepath.Join(srcDir, "blake3_ref.c"),
		flatSrc,
		"-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc oracle compression: %v\n%s", err, out)
	}

	cOut, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("oracle C compression: %v\n%s", err, cOut)
	}

	var want string
	var parite string
	for _, line := range bytes.Split(cOut, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("FOLD_COMPRESS=")) {
			want = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("FOLD_COMPRESS="))))
		}
		if bytes.HasPrefix(line, []byte("PARITE=")) {
			parite = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("PARITE="))))
		}
	}
	if want == "" {
		t.Fatalf("sortie oracle C invalide: %s", cOut)
	}
	if parite != "3/3" {
		t.Fatalf("l'oracle C ne déclare pas la parité de ses trois implémentations: %q", parite)
	}

	got := compressOracleFold(Blake3archtsim_compress)
	if !strings.EqualFold(foldHex(got), want) {
		t.Fatalf("compression vs gcc -O2: Go %s C %s", foldHex(got), want)
	}
	t.Logf("noyau transpilé bit-exact contre gcc -O2 ASAN/UBSAN (pli %s)", want)
}

// TestCompressAiguillageParite vérifie que la branche prise à l'exécution et le
// repli scalaire rendent le même résultat : un aiguillage qui divergerait selon
// le processeur produirait deux hachages différents pour la même entrée.
func TestCompressAiguillageParite(t *testing.T) {
	aiguille := compressOracleFold(Blake3archtsim_compress)
	scalaire := compressOracleFold(Blake3archtsim_compress_scalar)
	if aiguille != scalaire {
		t.Fatalf("aiguillage et repli scalaire divergent: %s vs %s",
			foldHex(aiguille), foldHex(scalaire))
	}
}
