// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"testing"
)

func TestOverflow_RoundtripAndStreaming(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{42, 43, 44}
	s := mustOpenShard(t, dir, key, 0, WithHeapPages(8192)) // 128 Mo
	defer s.Close()

	// 1. Génération de payloads de différentes tailles
	sizes := []int{
		100,             // Inline (< 2048)
		2048,            // Frontière Inline
		2049,            // Overflow mono-page
		16320,           // Pleine capacité 1ère page d'overflow
		16321,           // Débordement sur 2 pages
		65536,           // 4 pages d'overflow
		250000,          // ~15 pages d'overflow
		1024 * 1024 * 2, // 2 Mo (~126 pages d'overflow)
	}

	payloads := make(map[string][]byte)
	for idx, sz := range sizes {
		data := make([]byte, sz)
		_, err := rand.Read(data)
		if err != nil {
			t.Fatalf("rand.Read: %v", err)
		}
		k := fmt.Sprintf("key_%02d_%07d", idx, sz)
		payloads[k] = data

		if err := s.Put([]byte(k), data); err != nil {
			t.Fatalf("Put %s (size %d): %v", k, sz, err)
		}
	}

	// 2. Relecture unitaire Get
	for k, want := range payloads {
		got, err := s.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get %s: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Get %s mismatch: len got=%d want=%d", k, len(got), len(want))
		}
	}

	// 3. Parcours Cursor et vérification de ValueReader (Streaming)
	c, err := s.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	defer c.Close()

	count := 0
	for k, v, ok := c.First(); ok; k, v, ok = c.Next() {
		sk := string(k)
		want, exists := payloads[sk]
		if !exists {
			t.Fatalf("Cursor found unknown key: %s", sk)
		}
		if !bytes.Equal(v, want) {
			t.Fatalf("Cursor.Next value mismatch for %s: got=%d want=%d", sk, len(v), len(want))
		}

		// Test du streaming ValueReader
		reader, err := c.ValueReader()
		if err != nil {
			t.Fatalf("ValueReader for %s: %v", sk, err)
		}
		streamed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("io.ReadAll from ValueReader %s: %v", sk, err)
		}
		if !bytes.Equal(streamed, want) {
			t.Fatalf("ValueReader stream mismatch for %s: got=%d want=%d", sk, len(streamed), len(want))
		}

		count++
	}

	if count != len(payloads) {
		t.Fatalf("Cursor walked %d keys, want %d", count, len(payloads))
	}

	// 4. Test de RepackHeap avec pages d'overflow
	if err := s.RepackHeap(); err != nil {
		t.Fatalf("RepackHeap: %v", err)
	}

	// Vérification post-repack
	for k, want := range payloads {
		got, err := s.Get([]byte(k))
		if err != nil {
			t.Fatalf("Post-repack Get %s: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Post-repack Get %s mismatch: len got=%d want=%d", k, len(got), len(want))
		}
	}
}

func TestOverflow_DiscriminantAndCollision(t *testing.T) {
	dir := t.TempDir()
	key := [32]byte{1, 2, 3}
	s := mustOpenShard(t, dir, key, 0, WithHeapPages(4096))
	defer s.Close()

	// 1. Valeur de 13 octets commençant par 0x01 (tentative de collision avec descripteur d'overflow)
	collisionVal := make([]byte, 13)
	collisionVal[0] = 0x01
	for i := 1; i < 13; i++ {
		collisionVal[i] = byte(i * 7)
	}

	if err := s.Put([]byte("key:collision13"), collisionVal); err != nil {
		t.Fatalf("Put collision13: %v", err)
	}

	got, err := s.Get([]byte("key:collision13"))
	if err != nil {
		t.Fatalf("Get collision13: %v", err)
	}
	if !bytes.Equal(got, collisionVal) {
		t.Fatalf("Collision13 corrompue : got %x, want %x", got, collisionVal)
	}

	// 2. Valeur de 17 octets commençant par "C2OFL" (ancien format overflow, doit rester inline sans altération)
	c2oflVal := []byte("C2OFL123456789012")
	if err := s.Put([]byte("key:c2ofl"), c2oflVal); err != nil {
		t.Fatalf("Put c2ofl: %v", err)
	}
	gotC2, err := s.Get([]byte("key:c2ofl"))
	if err != nil {
		t.Fatalf("Get c2ofl: %v", err)
	}
	if !bytes.Equal(gotC2, c2oflVal) {
		t.Fatalf("C2OFL corrompu : got %q, want %q", gotC2, c2oflVal)
	}

	// 3. Valeurs inline de 0 octet et 2048 octets (frontière)
	emptyVal := []byte{}
	if err := s.Put([]byte("key:empty"), emptyVal); err != nil {
		t.Fatalf("Put empty: %v", err)
	}
	gotEmpty, err := s.Get([]byte("key:empty"))
	if err != nil {
		t.Fatalf("Get empty: %v", err)
	}
	if len(gotEmpty) != 0 {
		t.Fatalf("Empty val got len %d", len(gotEmpty))
	}

	// 4. Curseur et ValueLen
	cur, err := s.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	defer cur.Close()
	found := false
	for _, _, ok := cur.First(); ok; _, _, ok = cur.Next() {
		if string(cur.Key()) == "key:collision13" {
			found = true
			if cur.ValueLen() != 13 {
				t.Fatalf("ValueLen collision13: got %d, want 13", cur.ValueLen())
			}
			if cur.IsOverflow() {
				t.Fatalf("collision13 ne doit PAS être marquée comme overflow")
			}
			if !bytes.Equal(cur.Value(), collisionVal) {
				t.Fatalf("cur.Value collision13: got %x, want %x", cur.Value(), collisionVal)
			}
		}
	}
	if !found {
		t.Fatalf("key:collision13 non trouvée par le curseur")
	}
}
