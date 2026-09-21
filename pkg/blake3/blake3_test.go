package blake3archtsim

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type xorshift struct {
	s uint64
}

func (x *xorshift) next() uint64 {
	v := x.s
	v ^= v << 13
	v ^= v >> 7
	v ^= v << 17
	x.s = v
	return v
}

func TestBlake3VsCOracle(t *testing.T) {
	srcCandidates := []string{
		filepath.Join("..", "..", "sources", "blake3archtsim"),
		filepath.Join("..", "..", "c2simd", "sources", "blake3archtsim"),
		filepath.Join("sources", "blake3archtsim"),
	}
	var srcDir string
	for _, c := range srcCandidates {
		if _, err := os.Stat(filepath.Join(c, "test_blake3_oracle.c")); err == nil {
			srcDir = c
			break
		}
	}
	if srcDir == "" {
		t.Fatalf("Oracle C introuvable dans: %v", srcCandidates)
	}

	tmpBin := filepath.Join(t.TempDir(), "test_blake3_oracle")
	oracleSrc := filepath.Join(srcDir, "test_blake3_oracle.c")
	refSrc := filepath.Join(srcDir, "blake3_ref.c")

	cmd := exec.Command("gcc", "-O2", "-fsanitize=address,undefined", "-I", srcDir, oracleSrc, refSrc, "-o", tmpBin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc compile failed: %v\n%s", err, string(out))
	}

	cOut, err := exec.Command(tmpBin).CombinedOutput()
	if err != nil {
		t.Fatalf("C oracle failed: %v\n%s", err, string(cOut))
	}

	var wantFold string
	for _, line := range bytes.Split(cOut, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("FOLD_BLAKE3=")) {
			wantFold = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("FOLD_BLAKE3="))))
		}
	}
	if wantFold == "" {
		t.Fatalf("Sortie oracle C invalide: %s", string(cOut))
	}

	// Calcul du même fold en Go
	rng := xorshift{s: 0x853c49e6748fea9b}
	buffer := make([]byte, 65536)
	var foldHash uint64

	var step int
	for length := 0; length <= 4096; {
		for i := 0; i < length; i++ {
			buffer[i] = byte(rng.next())
		}
		got := Sum256(buffer[:length])
		for i := 0; i < 32; i += 8 {
			w := uint64(got[i]) |
				uint64(got[i+1])<<8 |
				uint64(got[i+2])<<16 |
				uint64(got[i+3])<<24 |
				uint64(got[i+4])<<32 |
				uint64(got[i+5])<<40 |
				uint64(got[i+6])<<48 |
				uint64(got[i+7])<<56
			foldHash ^= w
			foldHash = (foldHash << 7) | (foldHash >> 57)
		}

		if length < 128 {
			step = 1
		} else if length < 1024 {
			step = 31
		} else {
			step = 257
		}
		length += step
	}

	// La comparaison porte sur le seul pli rendu par l'oracle. La version
	// antérieure ajoutait « && wantFold != "0xA32E3B5A5AFF302E" », c'est-à-dire
	// la valeur que l'oracle rend précisément sur ce tirage : la condition
	// d'échec était donc toujours fausse et l'assertion ne mordait sur rien.
	if got := foldHex(foldHash); !strings.EqualFold(got, wantFold) {
		t.Fatalf("Parité oracle C échouée: Go %s vs C %s", got, wantFold)
	}

	t.Logf("PARITÉ BLAKE3 BIT-EXACTE 100%% VALIDÉE VS ORACLE C GCC -O2 ASAN/UBSAN (Fold: 0x%016X)", foldHash)
}

func foldHex(fold uint64) string {
	return "0x" + hex.EncodeToString([]byte{
		byte(fold >> 56), byte(fold >> 48), byte(fold >> 40), byte(fold >> 32),
		byte(fold >> 24), byte(fold >> 16), byte(fold >> 8), byte(fold),
	})
}

func TestDeriveKeyVsCOracle(t *testing.T) {
	srcCandidates := []string{
		filepath.Join("..", "..", "sources", "blake3archtsim"),
		filepath.Join("..", "..", "c2simd", "sources", "blake3archtsim"),
		filepath.Join("sources", "blake3archtsim"),
	}
	var srcDir string
	for _, c := range srcCandidates {
		if _, err := os.Stat(filepath.Join(c, "test_blake3_derive_oracle.c")); err == nil {
			srcDir = c
			break
		}
	}
	if srcDir == "" {
		t.Fatalf("Oracle C derive introuvable dans: %v", srcCandidates)
	}
	tmpBin := filepath.Join(t.TempDir(), "test_blake3_derive_oracle")
	cmd := exec.Command("gcc", "-O2", "-fsanitize=address,undefined", "-I", srcDir,
		filepath.Join(srcDir, "test_blake3_derive_oracle.c"),
		filepath.Join(srcDir, "blake3_ref.c"),
		"-o", tmpBin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gcc derive oracle: %v\n%s", err, string(out))
	}
	cOut, err := exec.Command(tmpBin).CombinedOutput()
	if err != nil {
		t.Fatalf("C derive oracle: %v\n%s", err, string(cOut))
	}
	var want, wantBad string
	for _, line := range bytes.Split(cOut, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("FOLD_DERIVE=")) && !bytes.Contains(line, []byte("BADCTX")) {
			want = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("FOLD_DERIVE="))))
		}
		if bytes.HasPrefix(line, []byte("FOLD_DERIVE_BADCTX=")) {
			wantBad = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("FOLD_DERIVE_BADCTX="))))
		}
	}
	if want == "" || wantBad == "" {
		t.Fatalf("sortie oracle C invalide: %s", cOut)
	}

	rng := xorshift{s: 0x853c49e6748fea9b}
	buffer := make([]byte, 4096)
	var fold, foldBad uint64
	ctx := "c2db/tenant-mac/v1"
	ctxBad := "c2db/tenant-mac/v0"
	var step int
	for length := 0; length <= 2048; {
		for i := 0; i < length; i++ {
			buffer[i] = byte(rng.next())
		}
		got := DeriveKey(ctx, buffer[:length])
		gotBad := DeriveKey(ctxBad, buffer[:length])
		for i := 0; i < 32; i += 8 {
			w := uint64(got[i]) |
				uint64(got[i+1])<<8 |
				uint64(got[i+2])<<16 |
				uint64(got[i+3])<<24 |
				uint64(got[i+4])<<32 |
				uint64(got[i+5])<<40 |
				uint64(got[i+6])<<48 |
				uint64(got[i+7])<<56
			fold ^= w
			fold = (fold << 7) | (fold >> 57)
			wb := uint64(gotBad[i]) |
				uint64(gotBad[i+1])<<8 |
				uint64(gotBad[i+2])<<16 |
				uint64(gotBad[i+3])<<24 |
				uint64(gotBad[i+4])<<32 |
				uint64(gotBad[i+5])<<40 |
				uint64(gotBad[i+6])<<48 |
				uint64(gotBad[i+7])<<56
			foldBad ^= wb
			foldBad = (foldBad << 7) | (foldBad >> 57)
		}
		if length < 128 {
			step = 1
		} else if length < 1024 {
			step = 31
		} else {
			step = 257
		}
		length += step
	}
	if !strings.EqualFold(foldHex(fold), want) {
		t.Fatalf("derive_key vs gcc: Go %s C %s", foldHex(fold), want)
	}
	if !strings.EqualFold(foldHex(foldBad), wantBad) {
		t.Fatalf("derive_key badctx vs gcc: Go %s C %s", foldHex(foldBad), wantBad)
	}
	if want == wantBad {
		t.Fatal("oracle C: contexte v1 et v0 produisent le même pli")
	}
}

func TestZeroAlloc(t *testing.T) {
	data := make([]byte, 65536)
	allocs := testing.AllocsPerRun(50, func() {
		_ = Sum256(data)
	})
	if allocs > 0 {
		t.Fatalf("BLAKE3 viole la règle 0-allocation: allocs/op = %.2f", allocs)
	}
}
