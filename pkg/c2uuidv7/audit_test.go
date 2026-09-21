// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2uuidv7

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestAudit_RFC9562_BitExactness vérifie la conformité bit par bit de chaque champ RFC 9562 §5.7.
func TestAudit_RFC9562_BitExactness(t *testing.T) {
	// Cas 1 : Timestamp fixe, nanosecondes connues, aléa connu
	// tsNs = 1714567890123456789 ns -> 1714567890123 ms + 456789 ns
	// 1714567890123 = 0x018F33DE44CB
	// frac = 456789 ns -> (456789 * 4096) / 1000000 = 1871 = 0x074F (12 bits)
	// seqOrRand = 0x0123456789ABCDEF
	tsNs := uint64(1714567890123456789)
	randVal := uint64(0x0123456789ABCDEF)

	u := Compose(tsNs, randVal)

	// 1. unix_ts_ms (48 bits) : octets 0..5
	expectedTsMs := tsNs / 1000000
	gotTsMs := u.TimestampMs()
	if gotTsMs != expectedTsMs {
		t.Fatalf("unix_ts_ms mismatch: got %d (0x%X), want %d (0x%X)", gotTsMs, gotTsMs, expectedTsMs, expectedTsMs)
	}
	if u[0] != 0x01 || u[1] != 0x8F || u[2] != 0x34 || u[3] != 0x35 || u[4] != 0xC4 || u[5] != 0xCB {
		t.Fatalf("bytes 0..5 mismatch: %x", u[0:6])
	}

	// 2. ver (4 bits) : octet 6 bits 7..4 = 0x7
	ver := u.Version()
	if ver != 7 {
		t.Fatalf("ver mismatch: got %d, want 7", ver)
	}
	if (u[6] >> 4) != 0x7 {
		t.Fatalf("ver high nibble of byte 6 mismatch: got 0x%X", u[6]>>4)
	}

	// 3. rand_a (12 bits) : octet 6 bits 3..0 (4 bits) + octet 7 (8 bits)
	// subMs12 attendu = 1871 = 0x074F -> high 4 bits = 0x7, low 8 bits = 0x4F
	expectedRandA := uint16(1871)
	gotRandA := (uint16(u[6]&0x0F) << 8) | uint16(u[7])
	if gotRandA != expectedRandA {
		t.Fatalf("rand_a (sub-ms fraction) mismatch: got 0x%03X (%d), want 0x%03X (%d)", gotRandA, gotRandA, expectedRandA, expectedRandA)
	}

	// 4. var (2 bits) : octet 8 bits 7..6 = 0b10 (0x80)
	variant := u.Variant()
	if variant != 2 {
		t.Fatalf("var mismatch: got %d, want 2", variant)
	}
	if (u[8] & 0xC0) != 0x80 {
		t.Fatalf("var bits 7..6 of byte 8 mismatch: got 0x%02X, want 0x80", u[8]&0xC0)
	}

	// 5. rand_b (62 bits) : octet 8 bits 5..0 (6 bits) + octets 9..15 (56 bits)
	// seqOrRand = 0x0123456789ABCDEF
	// seqOrRand >> 56 = 0x01 -> & 0x3F = 0x01 -> u[8] = 0x80 | 0x01 = 0x81
	if u[8] != 0x81 {
		t.Fatalf("byte 8 mismatch: got 0x%02X, want 0x81", u[8])
	}
	expectedTail := []byte{0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}
	if !bytes.Equal(u[9:16], expectedTail) {
		t.Fatalf("bytes 9..15 mismatch: got %x, want %x", u[9:16], expectedTail)
	}

	// 6. Validité globale
	if !u.IsV7() {
		t.Fatalf("IsV7() should return true for compliant UUIDv7")
	}

	// 7. Time() conversion
	tm := u.Time()
	if tm.UnixMilli() != int64(expectedTsMs) {
		t.Fatalf("Time().UnixMilli() mismatch: got %d, want %d", tm.UnixMilli(), expectedTsMs)
	}
	if tm.Location() != time.UTC {
		t.Fatalf("Time() location should be UTC")
	}
}

// TestAudit_Parser_EdgeCases teste le comportement sur toutes les variations et cas d'erreur.
func TestAudit_Parser_EdgeCases(t *testing.T) {
	canonical := "018f3a5b-7c8d-7e9f-a012-3456789abcde"
	uExpected, err := Parse(canonical)
	if err != nil {
		t.Fatalf("Failed to parse canonical: %v", err)
	}

	// 1. Cas valides : Majuscules, Minuscules, Mixtes
	validCases := []string{
		"018f3a5b-7c8d-7e9f-a012-3456789abcde",
		"018F3A5B-7C8D-7E9F-A012-3456789ABCDE",
		"018f3A5B-7c8D-7e9F-a012-3456789aBcDe",
		"018f3a5b7c8d7e9fa0123456789abcde", // compact 32 hex
		"018F3A5B7C8D7E9FA0123456789ABCDE", // compact 32 hex uppercase
		"018f3A5B7c8D7e9Fa0123456789aBcDe", // compact 32 hex mixed
		"urn:uuid:018f3a5b-7c8d-7e9f-a012-3456789abcde",
		"urn:uuid:018F3A5B-7C8D-7E9F-A012-3456789ABCDE",
		"{018f3a5b-7c8d-7e9f-a012-3456789abcde}",
		"{018F3A5B-7C8D-7E9F-A012-3456789ABCDE}",
	}

	for _, tc := range validCases {
		u, err := Parse(tc)
		if err != nil {
			t.Errorf("Parse(%q) returned error: %v", tc, err)
		}
		if u != uExpected {
			t.Errorf("Parse(%q) returned %v, want %v", tc, u, uExpected)
		}

		uBytes, err := ParseBytes([]byte(tc))
		if err != nil {
			t.Errorf("ParseBytes(%q) returned error: %v", tc, err)
		}
		if uBytes != uExpected {
			t.Errorf("ParseBytes(%q) returned %v, want %v", tc, uBytes, uExpected)
		}

		var uUnm UUID
		err = uUnm.UnmarshalText([]byte(tc))
		if err != nil {
			t.Errorf("UnmarshalText(%q) returned error: %v", tc, err)
		}
		if uUnm != uExpected {
			t.Errorf("UnmarshalText(%q) returned %v, want %v", tc, uUnm, uExpected)
		}
	}

	// 2. Cas invalides : Doivent TOUS renvoyer ErrInvalidUUID sans paniquer
	invalidCases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"too short 1", "0"},
		{"too short 8", "018f3a5b"},
		{"too short 16", "018f3a5b-7c8d-7e"},
		{"too short 31", "018f3a5b7c8d7e9fa0123456789abcd"},
		{"too short 35", "018f3a5b-7c8d-7e9f-a012-3456789abcd"},
		{"too long 37", "018f3a5b-7c8d-7e9f-a012-3456789abcdef"},
		{"too long 33 compact", "018f3a5b7c8d7e9fa0123456789abcdef0"},
		{"bad separator 1", "018f3a5b_7c8d-7e9f-a012-3456789abcde"},
		{"bad separator 2", "018f3a5b:7c8d:7e9f:a012:3456789abcde"},
		{"bad separator space", "018f3a5b 7c8d 7e9f a012 3456789abcde"},
		{"bad separator slash", "018f3a5b/7c8d/7e9f/a012/3456789abcde"},
		{"hyphens in wrong pos", "018f3a5b7c-8d7e-9fa0-1234-56789abcde"},
		{"invalid hex g", "018f3a5b-7c8d-7e9f-a012-3456789abcdg"},
		{"invalid hex z", "018f3a5b-7c8d-7e9f-a012-3456789abcdz"},
		{"invalid hex G", "018f3a5b-7c8d-7e9f-a012-3456789abcdG"},
		{"invalid hex punctuation @", "018f3a5b-7c8d-7e9f-a012-3456789abcd@"},
		{"invalid hex punctuation !", "018f3a5b-7c8d-7e9f-a012-3456789abcd!"},
		{"control char null", "018f3a5b-7c8d-7e9f-a012-3456789abcd\x00"},
		{"control char tab", "018f3a5b-7c8d-7e9f-a012-3456789abcd\t"},
		{"control char newline", "018f3a5b-7c8d-7e9f-a012-3456789abcd\n"},
		{"DEL char 0x7F", "018f3a5b-7c8d-7e9f-a012-3456789abcd\x7F"},
		{"non-ASCII byte 0x80", "018f3a5b-7c8d-7e9f-a012-3456789abcd\x80"},
		{"non-ASCII byte 0xFF", "018f3a5b-7c8d-7e9f-a012-3456789abcd\xFF"},
		{"UTF-8 multibyte e-accent", "018f3a5b-7c8d-7e9f-a012-3456789abcdé"},
		{"unmatched open brace", "{018f3a5b-7c8d-7e9f-a012-3456789abcde"},
		{"unmatched close brace", "018f3a5b-7c8d-7e9f-a012-3456789abcde}"},
		{"braces with 32 compact", "{018f3a5b7c8d7e9fa0123456789abcde}"},
		{"URN uppercase prefix", "URN:UUID:018f3a5b-7c8d-7e9f-a012-3456789abcde"},
		{"URN truncated prefix", "urn:uuid018f3a5b-7c8d-7e9f-a012-3456789abcde"},
	}

	for _, tc := range invalidCases {
		_, err := Parse(tc.input)
		if err == nil {
			t.Errorf("Parse(%s: %q) expected error, got nil", tc.name, tc.input)
		}
		_, errBytes := ParseBytes([]byte(tc.input))
		if errBytes == nil {
			t.Errorf("ParseBytes(%s: %q) expected error, got nil", tc.name, tc.input)
		}
	}
}

// TestAudit_ZeroAllocations exhaustif sur toutes les voies de parsing et d'encodage.
func TestAudit_ZeroAllocations(t *testing.T) {
	canonicalStr := "018f3a5b-7c8d-7e9f-a012-3456789abcde"
	canonicalBytes := []byte("018f3a5b-7c8d-7e9f-a012-3456789abcde")
	compactBytes := []byte("018f3a5b7c8d7e9fa0123456789abcde")
	urnBytes := []byte("urn:uuid:018f3a5b-7c8d-7e9f-a012-3456789abcde")
	braceBytes := []byte("{018f3a5b-7c8d-7e9f-a012-3456789abcde}")

	// 1. Parse(string) -> 0 allocs
	allocsParseStr := testing.AllocsPerRun(1000, func() {
		_, _ = Parse(canonicalStr)
	})
	if allocsParseStr != 0 {
		t.Errorf("Parse(string) allocs = %.2f, want 0", allocsParseStr)
	}

	// 2. ParseBytes(canonical) -> 0 allocs
	allocsParseBytes := testing.AllocsPerRun(1000, func() {
		_, _ = ParseBytes(canonicalBytes)
	})
	if allocsParseBytes != 0 {
		t.Errorf("ParseBytes(canonical) allocs = %.2f, want 0", allocsParseBytes)
	}

	// 3. ParseBytes(compact 32) -> 0 allocs
	allocsParseCompact := testing.AllocsPerRun(1000, func() {
		_, _ = ParseBytes(compactBytes)
	})
	if allocsParseCompact != 0 {
		t.Errorf("ParseBytes(compact 32) allocs = %.2f, want 0", allocsParseCompact)
	}

	// 4. ParseBytes(urn) -> 0 allocs
	allocsParseURN := testing.AllocsPerRun(1000, func() {
		_, _ = ParseBytes(urnBytes)
	})
	if allocsParseURN != 0 {
		t.Errorf("ParseBytes(urn) allocs = %.2f, want 0", allocsParseURN)
	}

	// 5. ParseBytes(braces) -> 0 allocs
	allocsParseBraces := testing.AllocsPerRun(1000, func() {
		_, _ = ParseBytes(braceBytes)
	})
	if allocsParseBraces != 0 {
		t.Errorf("ParseBytes(braces) allocs = %.2f, want 0", allocsParseBraces)
	}

	// 6. EncodeHex -> 0 allocs
	u := NewV7Fast()
	var outBuf [36]byte
	allocsEncodeHex := testing.AllocsPerRun(1000, func() {
		u.EncodeHex(&outBuf)
	})
	if allocsEncodeHex != 0 {
		t.Errorf("EncodeHex allocs = %.2f, want 0", allocsEncodeHex)
	}

	// 7. UnmarshalText -> 0 allocs
	var uTarget UUID
	allocsUnmarshal := testing.AllocsPerRun(1000, func() {
		_ = uTarget.UnmarshalText(canonicalBytes)
	})
	if allocsUnmarshal != 0 {
		t.Errorf("UnmarshalText allocs = %.2f, want 0", allocsUnmarshal)
	}

	// 8. AppendText dans buffer pré-alloué -> 0 allocs
	var appBuf [64]byte
	allocsAppend := testing.AllocsPerRun(1000, func() {
		_, _ = u.AppendText(appBuf[:0])
	})
	if allocsAppend != 0 {
		t.Errorf("AppendText (preallocated) allocs = %.2f, want 0", allocsAppend)
	}
}

// TestAudit_CompareLexicographical vérifie la conformité RFC 9562 §6.11.
func TestAudit_CompareLexicographical(t *testing.T) {
	u1 := Compose(1000000, 10)
	u2 := Compose(1000000, 20)
	u3 := Compose(2000000, 5)

	if u1.Compare(u1) != 0 {
		t.Errorf("u1.Compare(u1) != 0")
	}
	if u1.Compare(u2) >= 0 {
		t.Errorf("u1.Compare(u2) should be < 0")
	}
	if u2.Compare(u1) <= 0 {
		t.Errorf("u2.Compare(u1) should be > 0")
	}
	if u1.Compare(u3) >= 0 {
		t.Errorf("u1.Compare(u3) should be < 0")
	}
	if u3.Compare(u2) <= 0 {
		t.Errorf("u3.Compare(u2) should be > 0")
	}
	if !u1.Equal(u1) {
		t.Errorf("u1.Equal(u1) should be true")
	}
	if u1.Equal(u2) {
		t.Errorf("u1.Equal(u2) should be false")
	}
}

// TestAudit_HexTableExhaustive valide tous les 256 octets possibles dans la table ARCHTIME.
func TestAudit_HexTableExhaustive(t *testing.T) {
	validHex := "0123456789abcdefABCDEF"
	for b := 0; b < 256; b++ {
		char := byte(b)
		decoded := archtimeHexDecode[char]
		if strings.ContainsRune(validHex, rune(char)) {
			// Doit être entre 0x00 et 0x0F
			if decoded > 0x0F {
				t.Errorf("archtimeHexDecode[%d / %q] = 0x%02X, expected valid hex nibble", b, char, decoded)
			}
			expectedNibble, err := hex.DecodeString(string([]byte{char, '0'}))
			if err != nil {
				t.Fatalf("hex.DecodeString failed on %c: %v", char, err)
			}
			if decoded != (expectedNibble[0] >> 4) {
				t.Errorf("archtimeHexDecode[%c] = %d, expected %d", char, decoded, expectedNibble[0]>>4)
			}
		} else {
			// Doit être 0xFF
			if decoded != 0xFF {
				t.Errorf("archtimeHexDecode[%d / %q] = 0x%02X, expected 0xFF (invalid)", b, char, decoded)
			}
		}
	}
}
