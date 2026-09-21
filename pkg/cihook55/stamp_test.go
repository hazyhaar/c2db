package cihook55

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequireStamp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte("sgoiter-stamp: z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RequireStamp(t, p, "sgoiter-stamp")
}
