package c2db

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyFileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.key")
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	if err := WriteTestKey(path, key); err != nil {
		t.Fatalf("WriteTestKey: %v", err)
	}
	got, test, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if !test {
		t.Fatal("LoadKey: test != true")
	}
	if got != key {
		t.Fatalf("LoadKey: bits %x != %x", got, key)
	}
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %04o, want 0600", st.Mode().Perm())
	}
}

func TestKeyFileModeReject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loose.key")
	var raw [32]byte
	if err := os.WriteFile(path, raw[:], 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, _, err := LoadKey(path)
	if !errors.Is(err, errKeyMode) {
		t.Fatalf("LoadKey 0644: %v, want errKeyMode", err)
	}
}

func TestKeyFileRejectsDataDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.img"), nil, 0o600); err != nil {
		t.Fatalf("data.img: %v", err)
	}
	var key [32]byte
	path := filepath.Join(dir, "master.key")
	if err := WriteKey(path, key); !errors.Is(err, errKeyInDataDir) {
		t.Fatalf("WriteKey près de data.img: %v, want errKeyInDataDir", err)
	}
	if err := WriteTestKey(path, key); !errors.Is(err, errKeyInDataDir) {
		t.Fatalf("WriteTestKey près de data.img: %v, want errKeyInDataDir", err)
	}
}

func TestKeyFileRejectsTestdataWrite(t *testing.T) {
	var key [32]byte
	path := filepath.Join("testdata", "secret.key")
	if err := WriteKey(path, key); !errors.Is(err, errKeyInDataDir) {
		t.Fatalf("WriteKey testdata: %v, want errKeyInDataDir", err)
	}
	if err := WriteTestKey(path, key); !errors.Is(err, errKeyInDataDir) {
		t.Fatalf("WriteTestKey testdata: %v, want errKeyInDataDir", err)
	}
}

func TestNoUnmarkedPrivateKeysInTestdata(t *testing.T) {
	err := filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		sz := info.Size()
		if sz != 32 && sz != 40 {
			return nil
		}
		if sz == 32 {
			t.Errorf("fichier de 32 o interdit dans testdata: %s", path)
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if !bytes.Equal(b[:8], testKeyMagic) {
			t.Errorf("fichier de 40 o sans magique C2DBTEST: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk testdata: %v", err)
	}
}

func TestWriteKeyRoundtripOperational(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 7)
	}
	if err := WriteKey(path, key); err != nil {
		t.Fatalf("WriteKey: %v", err)
	}
	got, test, err := LoadKey(path)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if test {
		t.Fatal("LoadKey: test == true pour un fichier opérationnel")
	}
	if got != key {
		t.Fatalf("LoadKey: bits %x != %x", got, key)
	}
}
