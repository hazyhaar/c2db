// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"bytes"
	"crypto/mldsa"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"github.com/hazyhaar/c2db/pkg/c2poly1305"
)

func testKey() [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func writeMiniArchive(t *testing.T, path string, key [32]byte) {
	t.Helper()
	hdr := make([]byte, c2db.ArchHdrSize)
	binary.LittleEndian.PutUint32(hdr[0:], c2db.ArchMagic)
	binary.LittleEndian.PutUint32(hdr[4:], c2db.ArchFormatVer)
	binary.LittleEndian.PutUint16(hdr[38:], c2db.ArchSigSize)
	tagOff := c2db.ArchHdrSize - c2db.ArchTagSize
	c2poly1305.Crypto_poly1305(hdr[tagOff:], hdr[:tagOff], uint64(tagOff), key[:])
	if err := os.WriteFile(path, hdr, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestVerifyArchiveOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mini.arch")
	key := testKey()
	writeMiniArchive(t, path, key)
	if err := c2db.VerifyArchive(path, key); err != nil {
		t.Fatalf("VerifyArchive: %v", err)
	}
	var stderr bytes.Buffer
	code := run([]string{"-key", hex.EncodeToString(key[:]), "-in", path}, &stderr)
	if code != 0 {
		t.Fatalf("run exit %d stderr %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr non vide: %q", stderr.String())
	}
}

func TestVerifyReject(t *testing.T) {
	dir := t.TempDir()
	key := testKey()
	arch := filepath.Join(dir, "mini.arch")
	writeMiniArchive(t, arch, key)

	tampered := filepath.Join(dir, "tamper.arch")
	data, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(tampered, data, 0o600); err != nil {
		t.Fatalf("WriteFile tamper: %v", err)
	}

	snap := filepath.Join(dir, "mini.snap")
	var snapHdr [4]byte
	binary.LittleEndian.PutUint32(snapHdr[:], snapMagic)
	if err := os.WriteFile(snap, snapHdr[:], 0o600); err != nil {
		t.Fatalf("WriteFile snap: %v", err)
	}

	unknown := filepath.Join(dir, "unknown.bin")
	if err := os.WriteFile(unknown, []byte("XXXX"), 0o600); err != nil {
		t.Fatalf("WriteFile unknown: %v", err)
	}

	wrong := [32]byte{}
	cases := []struct {
		name string
		args []string
	}{
		{"usage", nil},
		{"bad-key", []string{"-key", "zz", "-in", arch}},
		{"wrong-key", []string{"-key", hex.EncodeToString(wrong[:]), "-in", arch}},
		{"tamper", []string{"-key", hex.EncodeToString(key[:]), "-in", tampered}},
		{"snap", []string{"-key", hex.EncodeToString(key[:]), "-in", snap}},
		{"unknown", []string{"-key", hex.EncodeToString(key[:]), "-in", unknown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := run(tc.args, &stderr)
			if code != 1 {
				t.Fatalf("exit %d, want 1", code)
			}
			line := stderr.String()
			if line == "" || strings.Count(line, "\n") != 1 || strings.HasSuffix(line, "\n\n") {
				t.Fatalf("stderr doit tenir en une ligne: %q", line)
			}
		})
	}
}

func testMLDSAKey(t *testing.T, fill byte) *mldsa.PrivateKey {
	t.Helper()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = fill
	}
	sk, err := mldsa.NewPrivateKey(mldsa.MLDSA44(), seed)
	if err != nil {
		t.Fatalf("NewPrivateKey: %v", err)
	}
	return sk
}

func writePubFile(t *testing.T, dir string, pub *mldsa.PublicKey) string {
	t.Helper()
	path := filepath.Join(dir, "pub.bin")
	if err := os.WriteFile(path, pub.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile pub: %v", err)
	}
	return path
}

func writeMiniArchiveSigned(t *testing.T, path string, key [32]byte, sk *mldsa.PrivateKey) {
	t.Helper()
	hdr := make([]byte, c2db.ArchHdrSize)
	binary.LittleEndian.PutUint32(hdr[0:], c2db.ArchMagic)
	binary.LittleEndian.PutUint32(hdr[4:], c2db.ArchFormatVer)
	binary.LittleEndian.PutUint16(hdr[36:], c2db.SigTypeMLDSA44)
	binary.LittleEndian.PutUint16(hdr[38:], uint16(mldsa.MLDSA44SignatureSize))
	tagOff := c2db.ArchHdrSize - c2db.ArchTagSize
	c2poly1305.Crypto_poly1305(hdr[tagOff:], hdr[:tagOff], uint64(tagOff), key[:])
	sig, err := sk.SignDeterministic(hdr, nil)
	if err != nil {
		t.Fatalf("SignDeterministic: %v", err)
	}
	if len(sig) != mldsa.MLDSA44SignatureSize {
		t.Fatalf("sig len %d", len(sig))
	}
	out := append(hdr, sig...)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestMLDSAVerifyCLI(t *testing.T) {
	dir := t.TempDir()
	key := testKey()
	sk := testMLDSAKey(t, 0x11)
	wrong := testMLDSAKey(t, 0x22)
	pubPath := writePubFile(t, dir, sk.PublicKey())
	wrongPath := filepath.Join(dir, "wrong.bin")
	if err := os.WriteFile(wrongPath, wrong.PublicKey().Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile wrong: %v", err)
	}

	arch := filepath.Join(dir, "signed.arch")
	writeMiniArchiveSigned(t, arch, key, sk)

	var stderr bytes.Buffer
	code := run([]string{"-key", hex.EncodeToString(key[:]), "-in", arch, "-pubkey", pubPath}, &stderr)
	if code != 0 {
		t.Fatalf("happy exit %d stderr %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("happy stderr %q", stderr.String())
	}

	stderr.Reset()
	code = run([]string{"-key", hex.EncodeToString(key[:]), "-in", arch}, &stderr)
	if code != 1 {
		t.Fatalf("need pub exit %d", code)
	}
	if !strings.Contains(stderr.String(), "public key required") {
		t.Fatalf("need pub stderr %q", stderr.String())
	}

	stderr.Reset()
	code = run([]string{"-key", hex.EncodeToString(key[:]), "-in", arch, "-pubkey", wrongPath}, &stderr)
	if code != 1 {
		t.Fatalf("wrong pub exit %d", code)
	}

	trunc := filepath.Join(dir, "trunc.arch")
	data, err := os.ReadFile(arch)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(trunc, data[:len(data)-1], 0o600); err != nil {
		t.Fatalf("WriteFile trunc: %v", err)
	}
	stderr.Reset()
	code = run([]string{"-key", hex.EncodeToString(key[:]), "-in", trunc, "-pubkey", pubPath}, &stderr)
	if code != 1 {
		t.Fatalf("trunc exit %d", code)
	}

	unsigned := filepath.Join(dir, "unsigned.arch")
	writeMiniArchive(t, unsigned, key)
	stderr.Reset()
	code = run([]string{"-key", hex.EncodeToString(key[:]), "-in", unsigned, "-pubkey", pubPath}, &stderr)
	if code != 1 {
		t.Fatalf("unsigned+pub exit %d", code)
	}
	if !strings.Contains(stderr.String(), "unsigned") {
		t.Fatalf("unsigned stderr %q", stderr.String())
	}
}

func TestVerifyBinaryExec(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "c2db-verify")

	buildCmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, string(out))
	}

	key := testKey()
	arch := filepath.Join(dir, "valid.arch")
	writeMiniArchive(t, arch, key)

	// Valid mini archive -> exit 0
	{
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bin, "-key", hex.EncodeToString(key[:]), "-in", arch)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("valid archive execution failed: %v, stderr: %q", err, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr non vide pour archive valide: %q", stderr.String())
		}
	}

	// Flip one byte -> exit 1, stderr nonempty
	{
		tampered := filepath.Join(dir, "tampered.arch")
		data, err := os.ReadFile(arch)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		data[len(data)-1] ^= 0xFF
		if err := os.WriteFile(tampered, data, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bin, "-key", hex.EncodeToString(key[:]), "-in", tampered)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err = cmd.Run()
		if err == nil {
			t.Fatalf("tampered archive succeeded, want exit 1")
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("expected *exec.ExitError, got: %T (%v)", err, err)
		}
		if exitErr.ExitCode() != 1 {
			t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
		}
		if strings.TrimSpace(stderr.String()) == "" {
			t.Fatalf("expected nonempty stderr, got empty")
		}
	}
}
