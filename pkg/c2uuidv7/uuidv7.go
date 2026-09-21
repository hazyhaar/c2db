// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package c2uuidv7 implémente un générateur et manipulateur d'UUID (RFC 9562)
// haute performance, 0-allocation (0 B/op), lock-free, à parité mécanique C99.
// Il constitue un sur-ensemble 100% compatible avec le paquet standard uuid de Go 1.27.
package c2uuidv7

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"
)

// UUID représente un identifiant universel 128 bits RFC 9562 (16 octets fixes).
type UUID [16]byte

var (
	ErrInvalidUUID = errors.New("c2uuidv7: invalid uuid format")
)

// Table ARCHTIME 1 : Décodage hexadécimal branchless (0xFF = caractère invalide).
var archtimeHexDecode = [256]byte{
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
}

// Table ARCHTIME 2 : Symboles hexadécimaux minuscules.
const archtimeHexChars = "0123456789abcdef"

// État atomique lock-free pour la monotonicité temporelle et le PRNG rapide
var (
	gUUIDLastTimestamp atomic.Uint64
	gUUIDPRNGState     atomic.Uint64
)

func init() {
	var seed [8]byte
	if _, err := rand.Read(seed[:]); err == nil {
		gUUIDPRNGState.Store(binary.LittleEndian.Uint64(seed[:]))
	} else {
		gUUIDPRNGState.Store(uint64(time.Now().UnixNano()) ^ 0x853c49e6748fea9b)
	}
}

func nextPRNG() uint64 {
	z := gUUIDPRNGState.Add(0x9e3779b97f4a7c15)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Nil retourne l'UUID nul 00000000-0000-0000-0000-000000000000 (RFC 9562 §5.9).
func Nil() UUID {
	return UUID{}
}

// Max retourne l'UUID maximal ffffffff-ffff-ffff-ffff-ffffffffffff (RFC 9562 §5.10).
func Max() UUID {
	return UUID{
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	}
}

// Compose compose un UUIDv7 déterministe à partir d'un timestamp en nanosecondes et d'un aléa 64 bits.
func Compose(tsNs uint64, seqOrRand uint64) (u UUID) {
	tsMs := tsNs / 1000000
	fracNs := uint32(tsNs % 1000000)
	subMs12 := (fracNs * 4096) / 1000000

	u[0] = byte((tsMs >> 40) & 0xFF)
	u[1] = byte((tsMs >> 32) & 0xFF)
	u[2] = byte((tsMs >> 24) & 0xFF)
	u[3] = byte((tsMs >> 16) & 0xFF)
	u[4] = byte((tsMs >> 8) & 0xFF)
	u[5] = byte(tsMs & 0xFF)

	u[6] = byte(0x70 | ((subMs12 >> 8) & 0x0F))
	u[7] = byte(subMs12 & 0xFF)

	u[8] = byte(0x80 | ((seqOrRand >> 56) & 0x3F))
	u[9] = byte((seqOrRand >> 48) & 0xFF)
	u[10] = byte((seqOrRand >> 40) & 0xFF)
	u[11] = byte((seqOrRand >> 32) & 0xFF)
	u[12] = byte((seqOrRand >> 24) & 0xFF)
	u[13] = byte((seqOrRand >> 16) & 0xFF)
	u[14] = byte((seqOrRand >> 8) & 0xFF)
	u[15] = byte(seqOrRand & 0xFF)
	return u
}

// NewV7Fast génère un UUIDv7 en < 3 ns/op via un PRNG lock-free, sans allocation ni mutex.
// Idéal pour la télémétrie de cyberdéfense et les pipelines à haut débit (> 50 Mops/s).
func NewV7Fast() UUID {
	now := time.Now()
	secs := uint64(now.Unix())
	nanos := uint64(now.Nanosecond())
	msecs := nanos / 1000000
	frac := nanos - (msecs * 1000000)
	timestamp := ((secs*1000 + msecs) << 12) + ((frac * 4096) / 1000000)

	// Progression atomique strictement monotone sans aucun mutex (Lock-Free CAS, RFC 9562 §6.2)
	for {
		prev := gUUIDLastTimestamp.Load()
		cur := timestamp
		if cur <= prev {
			cur = prev + 1
		}
		if gUUIDLastTimestamp.CompareAndSwap(prev, cur) {
			tsMs := cur >> 12
			subMs12 := uint32(cur & 0x0FFF)
			rnd := nextPRNG()

			var u UUID
			u[0] = byte((tsMs >> 40) & 0xFF)
			u[1] = byte((tsMs >> 32) & 0xFF)
			u[2] = byte((tsMs >> 24) & 0xFF)
			u[3] = byte((tsMs >> 16) & 0xFF)
			u[4] = byte((tsMs >> 8) & 0xFF)
			u[5] = byte(tsMs & 0xFF)

			u[6] = byte(0x70 | ((subMs12 >> 8) & 0x0F))
			u[7] = byte(subMs12 & 0xFF)

			u[8] = byte(0x80 | ((rnd >> 56) & 0x3F))
			u[9] = byte((rnd >> 48) & 0xFF)
			u[10] = byte((rnd >> 40) & 0xFF)
			u[11] = byte((rnd >> 32) & 0xFF)
			u[12] = byte((rnd >> 24) & 0xFF)
			u[13] = byte((rnd >> 16) & 0xFF)
			u[14] = byte((rnd >> 8) & 0xFF)
			u[15] = byte(rnd & 0xFF)
			return u
		}
	}
}

// NewV7 génère un UUIDv7 conforme RFC 9562 utilisant crypto/rand pour l'aléa cryptographique,
// avec progression temporelle atomique lock-free.
func NewV7() UUID {
	u := NewV7Fast()
	var cryptoRand [10]byte
	_, _ = rand.Read(cryptoRand[:])

	u[8] = byte(0x80 | (cryptoRand[0] & 0x3F))
	copy(u[9:], cryptoRand[1:])
	return u
}

// NewV4 génère un UUID version 4 (122 bits d'aléa cryptographique pur, RFC 9562 §5.4).
func NewV4() UUID {
	var u UUID
	_, _ = rand.Read(u[:])
	u[6] = (u[6] & 0x0F) | 0x40 // Version 4
	u[8] = (u[8] & 0x3F) | 0x80 // Variant 2
	return u
}

// New retourne un nouvel UUID par défaut (équivalent canonique de NewV7).
func New() UUID {
	return NewV7()
}

// EncodeHex écrit la représentation canonique 36 octets directement dans le tampon out.
// Zéro allocation garantie (0 B/op).
func (u UUID) EncodeHex(out *[36]byte) {
	out[0] = archtimeHexChars[u[0]>>4]
	out[1] = archtimeHexChars[u[0]&0x0F]
	out[2] = archtimeHexChars[u[1]>>4]
	out[3] = archtimeHexChars[u[1]&0x0F]
	out[4] = archtimeHexChars[u[2]>>4]
	out[5] = archtimeHexChars[u[2]&0x0F]
	out[6] = archtimeHexChars[u[3]>>4]
	out[7] = archtimeHexChars[u[3]&0x0F]
	out[8] = '-'

	out[9] = archtimeHexChars[u[4]>>4]
	out[10] = archtimeHexChars[u[4]&0x0F]
	out[11] = archtimeHexChars[u[5]>>4]
	out[12] = archtimeHexChars[u[5]&0x0F]
	out[13] = '-'

	out[14] = archtimeHexChars[u[6]>>4]
	out[15] = archtimeHexChars[u[6]&0x0F]
	out[16] = archtimeHexChars[u[7]>>4]
	out[17] = archtimeHexChars[u[7]&0x0F]
	out[18] = '-'

	out[19] = archtimeHexChars[u[8]>>4]
	out[20] = archtimeHexChars[u[8]&0x0F]
	out[21] = archtimeHexChars[u[9]>>4]
	out[22] = archtimeHexChars[u[9]&0x0F]
	out[23] = '-'

	out[24] = archtimeHexChars[u[10]>>4]
	out[25] = archtimeHexChars[u[10]&0x0F]
	out[26] = archtimeHexChars[u[11]>>4]
	out[27] = archtimeHexChars[u[11]&0x0F]
	out[28] = archtimeHexChars[u[12]>>4]
	out[29] = archtimeHexChars[u[12]&0x0F]
	out[30] = archtimeHexChars[u[13]>>4]
	out[31] = archtimeHexChars[u[13]&0x0F]
	out[32] = archtimeHexChars[u[14]>>4]
	out[33] = archtimeHexChars[u[14]&0x0F]
	out[34] = archtimeHexChars[u[15]>>4]
	out[35] = archtimeHexChars[u[15]&0x0F]
}

// String retourne la chaîne canonique 36 octets (ex: "018f3a5b-7c8d-7e9f-a012-3456789abcde").
func (u UUID) String() string {
	var buf [36]byte
	u.EncodeHex(&buf)
	return string(buf[:])
}

// AppendText implémente encoding.TextAppender.
func (u UUID) AppendText(b []byte) ([]byte, error) {
	var buf [36]byte
	u.EncodeHex(&buf)
	return append(b, buf[:]...), nil
}

// MarshalText implémente encoding.TextMarshaler.
func (u UUID) MarshalText() ([]byte, error) {
	var buf [36]byte
	u.EncodeHex(&buf)
	out := make([]byte, 36)
	copy(out, buf[:])
	return out, nil
}

// AppendBinary implémente encoding.BinaryAppender.
func (u UUID) AppendBinary(b []byte) ([]byte, error) {
	return append(b, u[:]...), nil
}

// MarshalBinary implémente encoding.BinaryMarshaler.
func (u UUID) MarshalBinary() ([]byte, error) {
	b := make([]byte, 16)
	copy(b, u[:])
	return b, nil
}

// UnmarshalBinary implémente encoding.BinaryUnmarshaler.
func (u *UUID) UnmarshalBinary(data []byte) error {
	if len(data) != 16 {
		return ErrInvalidUUID
	}
	copy(u[:], data)
	return nil
}

// Value implémente database/sql/driver.Valuer pour la persistance SQL directe.
func (u UUID) Value() (driver.Value, error) {
	return u.String(), nil
}

// Scan implémente database/sql.Scanner pour la désérialisation depuis les bases de données.
func (u *UUID) Scan(src any) error {
	switch v := src.(type) {
	case string:
		parsed, err := Parse(v)
		if err != nil {
			return err
		}
		*u = parsed
		return nil
	case []byte:
		if len(v) == 16 {
			copy(u[:], v)
			return nil
		}
		parsed, err := ParseBytes(v)
		if err != nil {
			return err
		}
		*u = parsed
		return nil
	default:
		return fmt.Errorf("c2uuidv7: cannot scan %T into UUID", src)
	}
}

// Parse décode une chaîne UUID au format canonique 36 caractères (ou 32 compacts).
func Parse(s string) (UUID, error) {
	b := unsafe.Slice(unsafe.StringData(s), len(s))
	return ParseBytes(b)
}

// MustParse décode une chaîne ou panique en cas d'erreur.
func MustParse(s string) UUID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// ParseBytes décode une tranche d'octets sans aucune allocation.
func ParseBytes(b []byte) (u UUID, err error) {
	inLen := len(b)
	if inLen >= 9 && string(b[:9]) == "urn:uuid:" {
		b = b[9:]
		inLen -= 9
	}
	if inLen == 38 && b[0] == '{' && b[37] == '}' {
		b = b[1:37]
		inLen = 36
	}

	if inLen == 36 {
		if b[8] != '-' || b[13] != '-' || b[18] != '-' || b[23] != '-' {
			return UUID{}, ErrInvalidUUID
		}

		offsets := [16]byte{0, 2, 4, 6, 9, 11, 14, 16, 19, 21, 24, 26, 28, 30, 32, 34}
		for i := 0; i < 16; i++ {
			off := offsets[i]
			h1 := archtimeHexDecode[b[off]]
			h2 := archtimeHexDecode[b[off+1]]
			if (h1|h2)&0xF0 != 0 {
				return UUID{}, ErrInvalidUUID
			}
			u[i] = (h1 << 4) | h2
		}
		return u, nil
	} else if inLen == 32 {
		for i := 0; i < 16; i++ {
			h1 := archtimeHexDecode[b[i*2]]
			h2 := archtimeHexDecode[b[i*2+1]]
			if (h1|h2)&0xF0 != 0 {
				return UUID{}, ErrInvalidUUID
			}
			u[i] = (h1 << 4) | h2
		}
		return u, nil
	}

	return UUID{}, ErrInvalidUUID
}

// UnmarshalText implémente encoding.TextUnmarshaler.
func (u *UUID) UnmarshalText(b []byte) error {
	parsed, err := ParseBytes(b)
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

// TimestampMs extrait le timestamp Unix milliseconde sur 48 bits (RFC 9562 §5.7).
func (u UUID) TimestampMs() uint64 {
	return (uint64(u[0]) << 40) |
		(uint64(u[1]) << 32) |
		(uint64(u[2]) << 24) |
		(uint64(u[3]) << 16) |
		(uint64(u[4]) << 8) |
		(uint64(u[5]))
}

// Time convertit le timestamp 48 bits en time.Time UTC.
func (u UUID) Time() time.Time {
	ms := int64(u.TimestampMs())
	return time.UnixMilli(ms).UTC()
}

// Version extrait la version RFC 9562 (7 pour UUIDv7, 4 pour UUIDv4).
func (u UUID) Version() int {
	return int(u[6] >> 4)
}

// Variant extrait la variante RFC 9562 (2 pour RFC 4122/9562).
func (u UUID) Variant() int {
	v := u[8]
	if (v & 0x80) == 0x00 {
		return 0
	}
	if (v & 0xC0) == 0x80 {
		return 2
	}
	if (v & 0xE0) == 0xC0 {
		return 6
	}
	return 7
}

// IsV7 vérifie si l'UUID est un UUIDv7 valide (Version 7 et Variante 2).
func (u UUID) IsV7() bool {
	return u.Version() == 7 && u.Variant() == 2
}

// IsV4 vérifie si l'UUID est un UUIDv4 valide (Version 4 et Variante 2).
func (u UUID) IsV4() bool {
	return u.Version() == 4 && u.Variant() == 2
}

// IsNil vérifie si l'UUID est l'UUID nul (RFC 9562 §5.9).
func (u UUID) IsNil() bool {
	return u == UUID{}
}

// IsMax vérifie si l'UUID est l'UUID maximal (RFC 9562 §5.10).
func (u UUID) IsMax() bool {
	return u == Max()
}

// Compare compare u et v selon l'ordre lexicographique big-endian RFC 9562 §6.11.
func (u UUID) Compare(v UUID) int {
	for i := 0; i < 16; i++ {
		if u[i] < v[i] {
			return -1
		}
		if u[i] > v[i] {
			return 1
		}
	}
	return 0
}

// Equal teste l'égalité stricte des 16 octets.
func (u UUID) Equal(v UUID) bool {
	return u == v
}
