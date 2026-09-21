package cihook55

import (
	"bytes"
	"os"
	"testing"
)

func RequireStamp(t *testing.T, path, needle string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(needle)) {
		t.Fatalf("tampon %q absent de %s", needle, path)
	}
}
