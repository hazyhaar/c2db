// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2poly1305

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/poly1305"
)

// Vecteur RFC 8439 §2.5.2.
func TestRFC8439Vector(t *testing.T) {
	key, _ := hex.DecodeString("85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b")
	msg := []byte("Cryptographic Forum Research Group")
	want, _ := hex.DecodeString("a8061dc1305136c6c22b8baf0c0127a9")
	var mac [16]byte
	Crypto_poly1305(mac[:], msg, uint64(len(msg)), key)
	if !bytes.Equal(mac[:], want) {
		t.Fatalf("RFC 8439 : obtenu %x, attendu %x", mac, want)
	}
}

// Parité contre golang.org/x/crypto/poly1305 sur 0..1088 octets, en une
// passe et en mises à jour fragmentées.
func TestParityXCrypto(t *testing.T) {
	for n := 0; n <= 1088; n++ {
		var key [32]byte
		msg := make([]byte, n)
		rand.Read(key[:])
		rand.Read(msg)
		var want [16]byte
		poly1305.Sum(&want, msg, &key)

		var got [16]byte
		Crypto_poly1305(got[:], msg, uint64(n), key[:])
		if got != want {
			t.Fatalf("n=%d une passe : %x != %x", n, got, want)
		}

		var ctx Crypto_poly1305_ctx
		Crypto_poly1305_init(&ctx, key[:])
		for off := 0; off < n; {
			step := 1 + (off*7+n)%29
			if off+step > n {
				step = n - off
			}
			Crypto_poly1305_update(&ctx, msg[off:off+step], uint64(step))
			off += step
		}
		Crypto_poly1305_final(&ctx, got[:])
		if got != want {
			t.Fatalf("n=%d fragmenté : %x != %x", n, got, want)
		}
	}
}

// Même LCG que sources/test_poly1305_oracle.c.
type lcg struct{ s uint64 }

func (l *lcg) seed(n uint64) { l.s = n*0x9E3779B97F4A7C15 + 1 }
func (l *lcg) next() byte {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	return byte(l.s >> 56)
}

func TestPoly1305VsCOracle(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc absent : oracle C non jouable")
	}
	srcDir := filepath.Join("..", "..", "sources")
	mcDir := filepath.Join("..", "..", "spec", "c_sources", "upstream", "monocypher", "4.0.2")
	for _, p := range []string{filepath.Join(srcDir, "test_poly1305_oracle.c"), filepath.Join(mcDir, "monocypher.c")} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("source oracle absente : %s", p)
		}
	}
	tmpBin := filepath.Join(t.TempDir(), "poly1305_c_oracle")
	build := exec.Command("gcc", "-O2", "-Wall", "-Wextra", "-std=gnu99", "-I", mcDir,
		filepath.Join(srcDir, "test_poly1305_oracle.c"), filepath.Join(mcDir, "monocypher.c"), "-o", tmpBin)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compilation oracle C gcc -O2 : %v\n%s", err, out)
	}
	out, err := exec.Command(tmpBin).Output()
	if err != nil {
		t.Fatalf("exécution oracle C : %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 24 {
		t.Fatalf("oracle C : %d lignes, attendu 24", len(lines))
	}
	var g lcg
	for _, line := range lines {
		var n int
		var hexmac string
		if _, err := fmt.Sscanf(line, "%d:%s", &n, &hexmac); err != nil {
			t.Fatalf("ligne oracle illisible %q : %v", line, err)
		}
		g.seed(uint64(n))
		key := make([]byte, 32)
		for k := range key {
			key[k] = g.next()
		}
		msg := make([]byte, n)
		for k := range msg {
			msg[k] = g.next()
		}
		var mac [16]byte
		Crypto_poly1305(mac[:], msg, uint64(n), key)

		// Remplacement du fold probabiliste par une vérification octet par octet
		// (Le code d'origine le fait déjà avec hex.EncodeToString, mais on explicite
		// la comparaison stricte byte à byte pour respecter la mission 2).
		wantMac, _ := hex.DecodeString(hexmac)
		if !bytes.Equal(mac[:], wantMac) {
			t.Fatalf("n=%d : Go %x, oracle C %s", n, mac, hexmac)
		}
	}
}

func TestZeroAlloc(t *testing.T) {
	key := make([]byte, 32)
	msg := make([]byte, 64)
	var mac [16]byte
	allocs := testing.AllocsPerRun(200, func() {
		var ctx Crypto_poly1305_ctx
		Crypto_poly1305_init(&ctx, key)
		Crypto_poly1305_update(&ctx, msg, 64)
		Crypto_poly1305_final(&ctx, mac[:])
	})
	if allocs != 0 {
		t.Fatalf("allocations = %.2f, attendu 0", allocs)
	}
}

func BenchmarkPoly1305(b *testing.B) {
	for _, n := range []int{64, 1350, 8192} {
		key := make([]byte, 32)
		msg := make([]byte, n)
		rand.Read(key)
		rand.Read(msg)
		var mac [16]byte
		b.Run(fmt.Sprintf("emis/%d", n), func(b *testing.B) {
			b.SetBytes(int64(n))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var ctx Crypto_poly1305_ctx
				Crypto_poly1305_init(&ctx, key)
				Crypto_poly1305_update(&ctx, msg, uint64(n))
				Crypto_poly1305_final(&ctx, mac[:])
			}
		})
		b.Run(fmt.Sprintf("xcrypto_asm/%d", n), func(b *testing.B) {
			var k [32]byte
			copy(k[:], key)
			b.SetBytes(int64(n))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				poly1305.Sum(&mac, msg, &k)
			}
		})
	}
}
