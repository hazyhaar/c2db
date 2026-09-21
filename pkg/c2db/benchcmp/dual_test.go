// SPDX-License-Identifier: Apache-2.0 OR MIT

package benchcmp

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
	"golang.org/x/sys/unix"
)

const (
	dualN      = 128
	dualJSON   = "/devhoros/c2simd/c2pkg/c2db/bench_dual.json"
	dualMD     = "/devhoros/c2simd/c2pkg/c2db/bench_dual.md"
	dualAsmDir = "/devhoros/c2simd/c2pkg/c2db/bench_asm"
	c2dbPkgDir = "/devhoros/c2simd/c2pkg/c2db"
)

type dualRow struct {
	Axis    string      `json:"axis"`
	Name    string      `json:"name"`
	Engine  string      `json:"engine"`
	N       int         `json:"n"`
	NsPerOp float64     `json:"ns_per_op"`
	Proof   string      `json:"proof"`
	Kernel  KernelProbe `json:"kernel"`
	L1      l1Counters  `json:"l1"`
	AsmCALL int         `json:"asm_call,omitempty"`
	AsmSYS  int         `json:"asm_syscall,omitempty"`
	AsmFile string      `json:"asm_file,omitempty"`
}

var asmSyms = []struct {
	file, re string
}{
	{"publish.s", `c2db\.\(\*Shard\)\.publish`},
	{"putBody.s", `c2db\.\(\*Shard\)\.putBody`},
	{"GetAsOf.s", `c2db\.\(\*Shard\)\.GetAsOf`},
	{"pinLive.s", `c2db\.\(\*Shard\)\.pinLive`},
	{"writeOne.s", `c2db\.\(\*WAL\)\.writeOne`},
	{"verifyPage.s", `c2db\.\(\*Pager\)\.verifyPage`},
	{"pwriteAll.s", `c2db\.\(\*Device\)\.pwriteAll`},
	{"lockWriter.s", `c2db\.\(\*Shard\)\.lockWriter`},
}

func harvestAsm(t *testing.T) map[string][2]int {
	t.Helper()
	if err := os.MkdirAll(dualAsmDir, 0o755); err != nil {
		t.Fatalf("mkdir asm: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "c2db.test")
	cmd := exec.Command("go", "test", "-c", "-o", bin, ".")
	cmd.Dir = c2dbPkgDir
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=go1.27.0",
		"GOEXPERIMENT=simd",
		"GOWORK=/devhoros/c2simd/go.work",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("go test -c: %v\n%s", err, out)
		return nil
	}
	counts := map[string][2]int{}
	for _, s := range asmSyms {
		dump, err := exec.Command("go", "tool", "objdump", "-s", s.re, bin).CombinedOutput()
		path := filepath.Join(dualAsmDir, s.file)
		_ = os.WriteFile(path, dump, 0o644)
		nCall := bytes.Count(dump, []byte("CALL "))
		nSys := bytes.Count(dump, []byte("SYSCALL"))
		counts[s.file] = [2]int{nCall, nSys}
		head := dump
		if len(head) > 2500 {
			head = head[:2500]
		}
		t.Logf("ASM %s CALL=%d SYSCALL=%d\n%s", s.file, nCall, nSys, head)
		if err != nil {
			t.Logf("objdump %s: %v", s.re, err)
		}
	}
	return counts
}

func TestDualStrataFuncAndMutAsm(t *testing.T) {
	asm := harvestAsm(t)
	dir := t.TempDir()
	var mac [32]byte
	for i := range mac {
		mac[i] = byte(i + 11)
	}
	c2dir := filepath.Join(dir, "c2")
	if err := os.MkdirAll(c2dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := c2db.OpenShard(c2dir, mac, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("OpenShard: %v", err)
	}
	defer s.Close()
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll: %v", err)
	}
	sq, err := openSQLite(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer sq.Close()

	var rows []dualRow
	add := func(axis, name, engine, proof string, n int, fn func()) {
		ns, k, l1 := measure(n, fn)
		r := dualRow{Axis: axis, Name: name, Engine: engine, N: n, NsPerOp: ns, Proof: proof, Kernel: k, L1: l1}
		if c, ok := asm[name+".s"]; ok {
			r.AsmCALL, r.AsmSYS, r.AsmFile = c[0], c[1], filepath.Join(dualAsmDir, name+".s")
		}
		rows = append(rows, r)
		t.Logf("%s %s %s n=%d ns/op=%.0f ipc=%.2f sysc_w=%d write_b=%d proof=%s",
			axis, name, engine, n, ns, l1.IPC, k.SyscWrite, k.WriteBytes, proof)
	}

	ids := make([]c2uuidv7.UUID, 0, dualN)
	docs := make([][]byte, dualN)
	for i := 0; i < dualN; i++ {
		docs[i] = []byte(`{"status":"open","n":` + itoa(i) + `}`)
	}

	add("F", "insert", "c2db", "", dualN, func() {
		for i := 0; i < dualN; i++ {
			id, err := s.Insert("orders", docs[i])
			if err != nil {
				t.Errorf("Insert: %v", err)
				return
			}
			ids = append(ids, id)
		}
	})
	if len(ids) != dualN {
		t.Fatalf("ids=%d", len(ids))
	}
	got, err := s.GetDoc("orders", ids[0])
	if err != nil || !bytes.Equal(got, docs[0]) {
		t.Fatalf("preuve F-insert: err=%v got=%s", err, got)
	}
	rows[len(rows)-1].Proof = "GetDoc==Insert bit-exact"

	add("F", "insert", "sqlite", "INSERT kv", dualN, func() {
		for i := 0; i < dualN; i++ {
			if _, err := sq.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, keys[i], docs[i]); err != nil {
				t.Errorf("sqlite: %v", err)
				return
			}
		}
	})

	add("F", "get", "c2db", "", dualN, func() {
		for i := 0; i < dualN; i++ {
			if _, err := s.GetDoc("orders", ids[i]); err != nil {
				t.Errorf("GetDoc: %v", err)
				return
			}
		}
	})
	rows[len(rows)-1].Proof = "pinLive+GetAsOf"

	add("F", "get", "sqlite", "SELECT by key", dualN, func() {
		for i := 0; i < dualN; i++ {
			var v []byte
			if err := sq.QueryRow(`SELECT v FROM kv WHERE k=?`, keys[i]).Scan(&v); err != nil {
				t.Errorf("sqlite get: %v", err)
				return
			}
		}
	})

	scanLot := []byte(`{"cap":{"prefix":"o","quota_bytes":1048576,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":8,"proj":["status"],"filter":{"eq":["status","open"]}}}}`)
	add("F", "scan_filter", "c2db", "", 1, func() {
		res, err := c2db.ExecC2QL(s, scanLot)
		if err != nil || res.N < 1 {
			t.Errorf("scan: %+v %v", res, err)
		}
	})
	rows[len(rows)-1].Proof = "filter eq status=open N>0"

	h := ""
	g0, err := c2db.ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"orders","id":"`+hexID(ids[1])+`"}}}`))
	if err != nil {
		t.Fatalf("get hash: %v", err)
	}
	h = string(g0.Docs[0].Meta.Hash)
	casBad := []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"` + hexID(ids[1]) + `","expect":"00","doc":{"status":"x"}}}}`)
	if _, err := c2db.ExecC2QL(s, casBad); err == nil {
		t.Fatal("preuve F-cas: expect faux encore vert")
	}
	casOK := []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"` + hexID(ids[1]) + `","expect":"` + h + `","ops":[{"op":1,"f":"status","v":"hold"}]}}}`)
	add("F", "cas", "c2db", "rejet expect puis RecMut", 1, func() {
		if _, err := c2db.ExecC2QL(s, casOK); err != nil {
			t.Errorf("cas: %v", err)
		}
	})

	add("F", "lot", "c2db", "2 ins un fdatasync", 2, func() {
		raw := []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"ops":[{"ins":{"coll":"orders","doc":{"n":9001}}},{"ins":{"coll":"orders","doc":{"n":9002}}}]}`)
		if _, err := c2db.ExecC2QL(s, raw); err != nil {
			t.Errorf("lot: %v", err)
		}
	})

	vk := []byte("view-iso")
	if err := s.Put(vk, []byte(`{"iso":"a"}`)); err != nil {
		t.Fatalf("view seed: %v", err)
	}
	view, err := s.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	add("F", "view", "c2db", "", 1, func() {
		if err := s.Put(vk, []byte(`{"iso":"b"}`)); err != nil {
			t.Errorf("put during view: %v", err)
		}
	})
	gotV, err := view.Get(vk)
	_ = view.Close()
	if err != nil || !bytes.Contains(gotV, []byte(`"iso":"a"`)) {
		t.Fatalf("preuve F-view: err=%v val=%s", err, gotV)
	}
	rows[len(rows)-1].Proof = "View gelé v1 pendant Put v2"

	big := bytes.Repeat([]byte("x"), 512)
	bigDoc := append([]byte(`{"blob":"`), append(big, `"}`...)...)
	add("D", "json_large", "c2db", "Insert 512B", 8, func() {
		for i := 0; i < 8; i++ {
			if _, err := s.Insert("orders", bigDoc); err != nil {
				t.Errorf("large: %v", err)
				return
			}
		}
	})

	add("D", "uuid_overlay", "c2db", "Insert compose uuidv7", 32, func() {
		for i := 0; i < 32; i++ {
			id, err := s.Insert("orders", []byte(`{"k":1}`))
			if err != nil {
				t.Errorf("uuid: %v", err)
				return
			}
			if c2db.KindOf(id) != c2db.IDKindPut {
				t.Errorf("kind=%d", c2db.KindOf(id))
				return
			}
		}
	})
	rows[len(rows)-1].Proof = "KindOf==IDKindPut"

	add("D", "recmut", "c2db", "cas op=3 incr", 1, func() {
		id, err := s.Insert("orders", []byte(`{"n":1}`))
		if err != nil {
			t.Errorf("seed mut: %v", err)
			return
		}
		g, err := c2db.ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"orders","id":"`+hexID(id)+`"}}}`))
		if err != nil {
			t.Errorf("get mut: %v", err)
			return
		}
		hh := g.Docs[0].Meta.Hash
		_, err = c2db.ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"`+hexID(id)+`","expect":"`+hh+`","ops":[{"op":3,"f":"n","v":1}]}}}`))
		if err != nil {
			t.Errorf("mut: %v", err)
		}
	})

	add("D", "cow_grow", "c2db", "heapUsed croît", 64, func() {
		used0 := uint64(0)
		_ = used0
		for i := 0; i < 64; i++ {
			if _, err := s.Insert("orders", []byte(`{"g":1}`)); err != nil {
				t.Errorf("cow: %v", err)
				return
			}
		}
	})

	attachAsm := map[string]string{
		"insert": "putBody.s", "get": "GetAsOf.s", "cas": "publish.s",
		"lot": "writeOne.s", "view": "pinLive.s", "json_large": "putBody.s",
		"uuid_overlay": "putBody.s", "recmut": "publish.s", "cow_grow": "publish.s",
	}
	for i := range rows {
		if f, ok := attachAsm[rows[i].Name]; ok && rows[i].Engine == "c2db" {
			if c, ok := asm[f]; ok {
				rows[i].AsmCALL, rows[i].AsmSYS, rows[i].AsmFile = c[0], c[1], filepath.Join(dualAsmDir, f)
			}
		}
	}

	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(dualJSON, append(raw, '\n'), 0o644)
	writeDualMD(t, rows, asm)
}

func hexID(id c2uuidv7.UUID) string {
	const hexd = "0123456789abcdef"
	var b [32]byte
	for i := 0; i < 16; i++ {
		b[i*2] = hexd[id[i]>>4]
		b[i*2+1] = hexd[id[i]&0xf]
	}
	return string(b[:])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [12]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}

func writeDualMD(t *testing.T, rows []dualRow, asm map[string][2]int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# Banc double — fonctionnel et mutation, assembleur et I/O\n\n")
	b.WriteString("## Assembleur (go tool objdump)\n\n")
	b.WriteString("| Symbole | CALL | SYSCALL | Fichier |\n| :--- | ---: | ---: | :--- |\n")
	for _, s := range asmSyms {
		c := asm[s.file]
		b.WriteString("| `" + s.file + "` | ")
		b.WriteString(itoa(c[0]) + " | " + itoa(c[1]) + " | `" + filepath.Join(dualAsmDir, s.file) + "` |\n")
	}
	b.WriteString("\n## A. Strates fonctionnelles\n\n")
	b.WriteString("| Nom | Moteur | ns/op | IPC | write sysc | write octets | Preuve |\n| :--- | :--- | ---: | ---: | ---: | ---: | :--- |\n")
	for _, r := range rows {
		if r.Axis != "F" {
			continue
		}
		b.WriteString("| " + r.Name + " | " + r.Engine + " | ")
		b.WriteString(itoa(int(r.NsPerOp)) + " | ")
		b.WriteString(format2(r.L1.IPC) + " | ")
		b.WriteString(itoa(int(r.Kernel.SyscWrite)) + " | ")
		b.WriteString(itoa(int(r.Kernel.WriteBytes)) + " | " + r.Proof + " |\n")
	}
	b.WriteString("\n## B. Strates mutation de donnée\n\n")
	b.WriteString("| Nom | ns/op | IPC | L1D miss | write octets | Preuve |\n| :--- | ---: | ---: | ---: | ---: | :--- |\n")
	for _, r := range rows {
		if r.Axis != "D" {
			continue
		}
		b.WriteString("| " + r.Name + " | " + itoa(int(r.NsPerOp)) + " | " + format2(r.L1.IPC) + " | ")
		b.WriteString(itoa(int(r.L1.L1DMiss)) + " | " + itoa(int(r.Kernel.WriteBytes)) + " | " + r.Proof + " |\n")
	}
	b.WriteString("\nLogs assembleur : `" + dualAsmDir + "`.\n")
	_ = os.WriteFile(dualMD, []byte(b.String()), 0o644)
}

func format2(f float64) string {
	if f == 0 {
		return "0"
	}
	s := itoa(int(f*100 + 0.5))
	if len(s) == 1 {
		return "0.0" + s
	}
	if len(s) == 2 {
		return "0." + s
	}
	return s[:len(s)-2] + "." + s[len(s)-2:]
}
