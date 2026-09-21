// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type scanGoldenBlock struct {
	Prefix []byte
	Keys   []string
}

type scanGoldenJSONBlock struct {
	Prefix string   `json:"prefix"`
	Keys   []string `json:"keys"`
}

func isAllHexDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func parseScanGoldenTxt(path string) ([]scanGoldenBlock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var blocks []scanGoldenBlock
	var cur *scanGoldenBlock
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "PREFIX ") {
			if cur != nil {
				blocks = append(blocks, *cur)
			}
			token := strings.TrimSpace(strings.TrimPrefix(line, "PREFIX "))
			var prefix []byte
			if strings.HasPrefix(token, "0x") || strings.HasPrefix(token, "0X") {
				hx, err := hex.DecodeString(token[2:])
				if err != nil {
					return nil, fmt.Errorf("invalid hex prefix %q: %w", token, err)
				}
				prefix = hx
			} else if strings.HasPrefix(token, "hex:") {
				hx, err := hex.DecodeString(token[4:])
				if err != nil {
					return nil, fmt.Errorf("invalid hex prefix %q: %w", token, err)
				}
				prefix = hx
			} else if len(token)%2 == 0 && isAllHexDigits(token) {
				hx, err := hex.DecodeString(token)
				if err != nil {
					return nil, fmt.Errorf("invalid hex prefix %q: %w", token, err)
				}
				prefix = hx
			} else {
				prefix = []byte(token)
			}
			cur = &scanGoldenBlock{
				Prefix: prefix,
				Keys:   make([]string, 0),
			}
		} else if strings.HasPrefix(line, "KEY ") {
			if cur == nil {
				return nil, fmt.Errorf("KEY without PREFIX: %q", line)
			}
			key := strings.TrimSpace(strings.TrimPrefix(line, "KEY "))
			cur.Keys = append(cur.Keys, key)
		} else if line == "END" {
			if cur != nil {
				blocks = append(blocks, *cur)
				cur = nil
			}
		} else {
			return nil, fmt.Errorf("unrecognized line in %s: %q", path, line)
		}
	}
	if cur != nil {
		blocks = append(blocks, *cur)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return blocks, nil
}

func TestScanGolden(t *testing.T) {
	goldenTxtPath := filepath.Join("testdata", "scan_golden.txt")
	blocks, err := parseScanGoldenTxt(goldenTxtPath)
	if err != nil {
		t.Fatalf("parseScanGoldenTxt(%s): %v", goldenTxtPath, err)
	}
	if len(blocks) == 0 {
		t.Fatalf("no blocks found in %s", goldenTxtPath)
	}

	goldenJSONPath := filepath.Join("testdata", "scan_golden.json")
	if jsonData, err := os.ReadFile(goldenJSONPath); err == nil {
		var jsonBlocks []scanGoldenJSONBlock
		if err := json.Unmarshal(jsonData, &jsonBlocks); err != nil {
			t.Fatalf("unmarshal %s: %v", goldenJSONPath, err)
		}
		if len(jsonBlocks) != len(blocks) {
			t.Fatalf("json blocks len=%d != txt blocks len=%d", len(jsonBlocks), len(blocks))
		}
		for i, jb := range jsonBlocks {
			if len(jb.Keys) != len(blocks[i].Keys) {
				t.Fatalf("block %d keys count mismatch: json=%d txt=%d", i, len(jb.Keys), len(blocks[i].Keys))
			}
			for kIdx := range jb.Keys {
				if jb.Keys[kIdx] != blocks[i].Keys[kIdx] {
					t.Fatalf("block %d key %d mismatch: json=%q txt=%q", i, kIdx, jb.Keys[kIdx], blocks[i].Keys[kIdx])
				}
			}
		}
	}

	dir := t.TempDir()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 17)
	}
	const shardID uint16 = 3

	s := mustOpenShard(t, dir, key, shardID)
	assertShardODirect(t, s)

	val := make([]byte, 1200)

	// Insertion de clés périphériques sans préfixe "gld"
	for i := 0; i < 5; i++ {
		k := []byte(fmt.Sprintf("abc_%04d", i))
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put abc_%04d: %v", i, err)
		}
	}

	// Insertion des 30 clés avec préfixe "gld" provoquant l'éclatement des pages (used >= 5)
	for i := 0; i < 30; i++ {
		k := []byte(fmt.Sprintf("gld_%04d", i))
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put gld_%04d (used=%d): %v", i, s.heapUsed, err)
		}
	}

	// Insertion d'autres clés périphériques
	for i := 0; i < 5; i++ {
		k := []byte(fmt.Sprintf("zzz_%04d", i))
		if err := s.Put(k, val); err != nil {
			t.Fatalf("Put zzz_%04d: %v", i, err)
		}
	}

	if s.heapUsed < 5 {
		t.Fatalf("s.heapUsed = %d, attendu >= 5 pour valider le débordement multi-pages", s.heapUsed)
	}

	// Validation de chaque bloc du fichier golden
	for bIdx, b := range blocks {
		got, err := s.ScanPrefix(b.Prefix)
		if err != nil {
			t.Fatalf("block %d ScanPrefix(%q): %v", bIdx, b.Prefix, err)
		}
		if len(got) != len(b.Keys) {
			t.Fatalf("block %d ScanPrefix(%q): got %d keys, want %d keys (heapUsed=%d)",
				bIdx, b.Prefix, len(got), len(b.Keys), s.heapUsed)
		}
		for i := range b.Keys {
			if string(got[i]) != b.Keys[i] {
				t.Fatalf("block %d key %d mismatch: got %q want %q", bIdx, i, string(got[i]), b.Keys[i])
			}
		}
	}

	// Validation également via View (mmap isolé)
	view, err := s.View()
	if err != nil {
		t.Fatalf("s.View: %v", err)
	}
	for bIdx, b := range blocks {
		gotV, err := view.ScanPrefix(b.Prefix)
		if err != nil {
			t.Fatalf("view block %d ScanPrefix(%q): %v", bIdx, b.Prefix, err)
		}
		if len(gotV) != len(b.Keys) {
			t.Fatalf("view block %d ScanPrefix(%q): got %d keys, want %d keys",
				bIdx, b.Prefix, len(gotV), len(b.Keys))
		}
		for i := range b.Keys {
			if string(gotV[i]) != b.Keys[i] {
				t.Fatalf("view block %d key %d mismatch: got %q want %q", bIdx, i, string(gotV[i]), b.Keys[i])
			}
		}
	}
	if err := view.Close(); err != nil {
		t.Fatalf("view.Close: %v", err)
	}

	// Fermeture propre
	if err := s.Close(); err != nil {
		t.Fatalf("s.Close: %v", err)
	}
}
