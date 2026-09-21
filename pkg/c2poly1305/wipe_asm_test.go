// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2poly1305

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

var (
	reStore   = regexp.MustCompile(`(?i)^((?:V)?MOV\S*)\s+(.+),\s*(.+)$`)
	reDispReg = regexp.MustCompile(`^((?:0x)?[0-9A-Fa-f]+)?\(([A-Z0-9]+)\)$`)
)

func TestWipeSurvivesInAsm_Poly1305Final(t *testing.T) {
	requireAmd64(t)
	want := int(unsafe.Sizeof(Crypto_poly1305_ctx{}))
	bin := compileTestBinary(t)
	listing := objdumpSymbol(t, bin, `Crypto_poly1305_final$`)
	if err := attestFinalWipe(listing, want); err != nil {
		t.Fatal(err)
	}
}

func TestWipeGuard_NegativeControl(t *testing.T) {
	requireAmd64(t)
	want := int(unsafe.Sizeof(Crypto_poly1305_ctx{}))
	bin := compileTestBinary(t)
	wipeWitnessNoKeepAlive()

	t.Run("update_sans_effacement", func(t *testing.T) {
		listing := objdumpSymbol(t, bin, `Crypto_poly1305_update$`)
		err := attestFinalWipe(listing, want)
		if err == nil {
			t.Fatal("la fonction de vérification a accepté Crypto_poly1305_update, qui n'efface pas le contexte")
		}
	})
	t.Run("temoin_sans_keepalive", func(t *testing.T) {
		listing := objdumpSymbol(t, bin, `wipeWitnessNoKeepAlive$`)
		err := attestFinalWipe(listing, want)
		if err == nil {
			t.Fatal("la fonction de vérification a accepté le témoin sans KeepAlive")
		}
		if err2 := attestStackWipeAfterLive(listing, want); err2 == nil {
			t.Fatal("le témoin conserve un effacement de pile de la taille du contexte malgré l'absence de KeepAlive ; le négatif de pile est absent")
		}
	})
}

//go:noinline
func wipeWitnessNoKeepAlive() {
	var ctx Crypto_poly1305_ctx
	ctx.C[0] = 0xaa
	ctx.C_idx = 0x1111111111111111
	ctx.R[0] = 0x22222222
	ctx.Pad[0] = 0x33333333
	ctx.H[0] = 0x44444444
	ctx = Crypto_poly1305_ctx{}
}

func requireAmd64(t *testing.T) {
	t.Helper()
	if runtime.GOARCH != "amd64" {
		t.Fatalf("garde de désassemblage limitée à amd64, GOARCH=%s", runtime.GOARCH)
	}
}

func compileTestBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "x.test")
	cmd := exec.Command("go", "test", "-c", "-o", out, ".")
	cmd.Env = probeEnv()
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -c : %v\n%s", err, b)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("binaire de test introuvable ou vide : %s", out)
	}
	return out
}

func objdumpSymbol(t *testing.T, bin, re string) string {
	t.Helper()
	script := wipeProbeScript(t)
	cmd := exec.Command(script, bin, re)
	cmd.Env = probeEnv()
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("symbole %q introuvable : %v\n%s", re, err, b)
	}
	s := string(b)
	if !strings.Contains(s, "TEXT ") {
		t.Fatalf("symbole %q : dump sans TEXT\n%s", re, s)
	}
	return s
}

func wipeProbeScript(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		filepath.Join("scripts", "wipe_probe.sh"),
		filepath.Join("..", "scripts", "wipe_probe.sh"),
		filepath.Join("..", "..", "scripts", "wipe_probe.sh"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			abs, err := filepath.Abs(p)
			if err != nil {
				t.Fatal(err)
			}
			return abs
		}
	}
	t.Fatal("scripts/wipe_probe.sh introuvable")
	return ""
}

func probeEnv() []string {
	out := make([]string, 0, 16)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOFLAGS=") {
			v := strings.ReplaceAll(strings.TrimPrefix(e, "GOFLAGS="), "-race", "")
			out = append(out, "GOFLAGS="+strings.TrimSpace(v))
			continue
		}
		out = append(out, e)
	}
	return append(out, "GOTOOLCHAIN=go1.27.0", "GOEXPERIMENT=simd")
}

func attestFinalWipe(listing string, minBytes int) error {
	lines := asmLines(listing)
	lastMac := -1
	for i, instr := range lines {
		if destIsReg(instr, "BX") {
			lastMac = i
		}
	}
	if lastMac < 0 {
		return fmt.Errorf("aucun stockage vers mac (BX)")
	}
	stores := zeroStores(lines[lastMac+1:], "AX")
	if !prefixCovered(stores, minBytes) {
		return fmt.Errorf("après le dernier stockage vers mac : couverture nulle de AX [0,%d) incomplète (stores=%v)", minBytes, stores)
	}
	return nil
}

func attestStackWipeAfterLive(listing string, minBytes int) error {
	lines := asmLines(listing)
	lastLive := -1
	for i, instr := range lines {
		op, src, dest, ok := parseStore(instr)
		if !ok {
			continue
		}
		_ = op
		_ = dest
		if !isZeroSrc(src) {
			lastLive = i
		}
	}
	var rest []string
	if lastLive >= 0 {
		rest = lines[lastLive+1:]
	} else {
		rest = lines
	}
	stores := zeroStores(rest, "SP")
	if maxRun(stores) < minBytes {
		return fmt.Errorf("aucune plage nulle de %d octets vers SP après le dernier stockage vivant (stores=%v)", minBytes, stores)
	}
	return nil
}

func asmLines(listing string) []string {
	var out []string
	for _, line := range strings.Split(listing, "\n") {
		if instr := asmInstruction(line); instr != "" {
			out = append(out, instr)
		}
	}
	return out
}

func asmInstruction(line string) string {
	var last string
	for _, p := range strings.Split(line, "\t") {
		p = strings.TrimSpace(p)
		if p != "" {
			last = p
		}
	}
	if last == "" || strings.HasPrefix(last, "TEXT ") {
		return ""
	}
	if isHexBlob(last) {
		return ""
	}
	return last
}

func isHexBlob(s string) bool {
	if len(s) < 4 || len(s)%2 != 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func parseStore(instr string) (op, src, dest string, ok bool) {
	m := reStore.FindStringSubmatch(instr)
	if m == nil {
		return "", "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), strings.TrimSpace(m[3]), true
}

func destIsReg(instr, reg string) bool {
	_, _, dest, ok := parseStore(instr)
	if !ok {
		return false
	}
	return strings.Contains(dest, "("+reg+")")
}

type span struct{ lo, hi int }

func zeroStores(lines []string, reg string) []span {
	var out []span
	for _, instr := range lines {
		op, src, dest, ok := parseStore(instr)
		if !ok || !isZeroSrc(src) {
			continue
		}
		m := reDispReg.FindStringSubmatch(dest)
		if m == nil || m[2] != reg {
			continue
		}
		off, err := parseDisp(m[1])
		if err != nil {
			continue
		}
		w := storeWidth(op, src)
		if w <= 0 {
			continue
		}
		out = append(out, span{off, off + w})
	}
	return mergeSpans(out)
}

func isZeroSrc(src string) bool {
	s := strings.TrimSpace(strings.ToUpper(src))
	switch s {
	case "X15", "Y15", "Z15":
		return true
	}
	if strings.HasPrefix(s, "$") {
		n, err := strconv.ParseInt(s[1:], 0, 64)
		return err == nil && n == 0
	}
	return false
}

func storeWidth(op, src string) int {
	u := strings.ToUpper(op)
	srcU := strings.ToUpper(src)
	switch {
	case strings.Contains(u, "MOVUPS") || strings.Contains(u, "MOVAPS") || strings.Contains(u, "MOVDQU") || strings.Contains(u, "MOVDQA"):
		if strings.HasPrefix(srcU, "Z") {
			return 64
		}
		if strings.HasPrefix(srcU, "Y") {
			return 32
		}
		return 16
	case strings.HasSuffix(u, "Q"):
		return 8
	case strings.HasSuffix(u, "L"):
		return 4
	case strings.HasSuffix(u, "W"):
		return 2
	case strings.HasSuffix(u, "B"):
		return 1
	default:
		return 0
	}
}

func parseDisp(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 0, 64)
	return int(n), err
}

func mergeSpans(in []span) []span {
	if len(in) == 0 {
		return nil
	}
	s := append([]span(nil), in...)
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j].lo < s[i].lo {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	out := []span{s[0]}
	for _, x := range s[1:] {
		last := &out[len(out)-1]
		if x.lo <= last.hi {
			if x.hi > last.hi {
				last.hi = x.hi
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

func prefixCovered(s []span, n int) bool {
	pos := 0
	for _, x := range s {
		if x.lo > pos {
			return false
		}
		if x.hi > pos {
			pos = x.hi
		}
		if pos >= n {
			return true
		}
	}
	return pos >= n
}

func maxRun(s []span) int {
	best := 0
	for _, x := range s {
		if d := x.hi - x.lo; d > best {
			best = d
		}
	}
	return best
}
