package c2db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const fixturesDir = "testdata/fixtures"

func compileFixtures(t *testing.T, extraFlags ...string) string {
	t.Helper()

	sources, err := filepath.Glob(filepath.Join(fixturesDir, "*.go"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("aucune fixture trouvée sous %s : %v", fixturesDir, err)
	}
	sort.Strings(sources)

	args := append([]string{"tool", "compile", "-p", "csgfixtures", "-o", os.DevNull}, extraFlags...)
	args = append(args, sources...)

	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.0")
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "Found Is") {
		t.Fatalf("compilation des fixtures échouée : %v\n%s", err, out)
	}
	return string(out)
}

func compilePackage(t *testing.T, extraFlags ...string) string {
	t.Helper()

	args := append([]string{"build", "-o", os.DevNull, "-gcflags=" + strings.Join(extraFlags, " ")}, ".")
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.0", "GOEXPERIMENT=simd")
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "TEXT") {
		t.Fatalf("compilation du paquet c2db : %v\n%s", err, out)
	}
	return string(out)
}

var jumpSymRe = regexp.MustCompile(`csgfixtures\.(\w+)\.jump\d+`)

func TestC2_JumpTable(t *testing.T) {
	asm := compileFixtures(t, "-S")

	withTable := map[string]bool{}
	for _, m := range jumpSymRe.FindAllStringSubmatch(asm, -1) {
		withTable[m[1]] = true
	}

	cases := []struct {
		fn     string
		want   bool
		reason string
	}{
		{"SwitchDense", true, "16 cas contigus : au-dessus de minCases=8, densité 1"},
		{"SwitchHoles", true, "16 cas sur 0..60, densité 1/3,8 : sous minDensity=4, les trous sont tolérés"},
		{"SwitchSparse", false, "16 cas sur 0..300, densité 1/18 : au-delà de minDensity"},
		{"SwitchTooFew", false, "7 cas contigus : sous minCases=8"},
	}

	for _, c := range cases {
		if got := withTable[c.fn]; got != c.want {
			t.Errorf("C2 %s : table de saut = %v, attendu %v (%s)", c.fn, got, c.want, c.reason)
		}
	}
}

func recTypeConsts(t *testing.T) map[string]int64 {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "wal.go", nil, 0)
	if err != nil {
		t.Fatalf("lecture de wal.go : %v", err)
	}

	out := map[string]int64{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		var iotaVal int64
		inRec := false
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			if vs.Type != nil {
				id, ok := vs.Type.(*ast.Ident)
				inRec = ok && id.Name == "RecType"
			}
			if !inRec {
				continue
			}
			for i, name := range vs.Names {
				val := iotaVal
				if i < len(vs.Values) {
					switch v := vs.Values[i].(type) {
					case *ast.BasicLit:
						n, err := strconv.ParseInt(v.Value, 0, 64)
						if err != nil {
							t.Fatalf("RecType %s : littéral illisible %q", name.Name, v.Value)
						}
						val = n
						iotaVal = n
					case *ast.Ident:
						if v.Name == "iota" {
							val = 0
							iotaVal = 0
						}
					}
				}
				out[name.Name] = val
				iotaVal++
			}
		}
	}
	return out
}

var walJumpRe = regexp.MustCompile(`c2db\.Db_wal_(?:pack|type_ok)\.jump\d+`)

func TestC2_WALTypeSwitchGetsJumpTable(t *testing.T) {
	vals := recTypeConsts(t)
	if len(vals) < 8 {
		t.Errorf("C2 RecType : %d cas, minCases=8 — l'énumération est trop courte pour une table de saut", len(vals))
	}
	if vals["RecPut"] != 1 || vals["RecDel"] != 2 {
		t.Errorf("C2 RecType : Put=%d Del=%d, attendu 1 et 2 (Append existant)", vals["RecPut"], vals["RecDel"])
	}
	if vals["RecInvalid"] != 0 {
		t.Errorf("C2 RecType : RecInvalid=%d, attendu 0", vals["RecInvalid"])
	}
	if vals["RecMigrate"] != 7 {
		t.Errorf("C2 RecType : RecMigrate=%d, attendu 7", vals["RecMigrate"])
	}
	seen := map[int64]bool{}
	for _, v := range vals {
		seen[v] = true
	}
	for i := int64(0); i <= 7; i++ {
		if !seen[i] {
			t.Errorf("C2 RecType : valeur %d absente (0 invalid … 7 migrate)", i)
		}
	}

	asm := compilePackage(t, "-S")
	if !walJumpRe.MatchString(asm) {
		t.Error("C2 : le commutateur de types WAL ne reçoit PAS de table de saut. " +
			"Db_wal_type_ok a perdu sa densité, ou le nombre de cas est passé sous minCases=8.")
	}
}

func TestCrc32c_zeroalloc(t *testing.T) {
	page := make([]byte, 16384)
	allocs := testing.AllocsPerRun(1000, func() {
		_ = Db_page_crc32c(page, 16384)
	})
	if allocs != 0 {
		t.Fatalf("Db_page_crc32c allocs/op = %.2f, want 0", allocs)
	}
}

func BenchmarkCrc32c_zeroalloc(b *testing.B) {
	page := make([]byte, 16384)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Db_page_crc32c(page, 16384)
	}
}
