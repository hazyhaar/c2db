package c2uuidv7

import "time"

// ParseV7 accepts only the canonical 36-character UUIDv7 representation.
// Unlike Parse it rejects compact, URN, braced and other-version identifiers.
func ParseV7(s string) (UUID, error) {
	if len(s) != 36 {
		return UUID{}, ErrInvalidUUID
	}
	u, err := Parse(s)
	if err != nil || !u.IsV7() {
		return UUID{}, ErrInvalidUUID
	}
	return u, nil
}

func IsV7String(s string) bool {
	_, err := ParseV7(s)
	return err == nil
}

func ParseV7Time(s string) (time.Time, error) {
	u, err := ParseV7(s)
	if err != nil {
		return time.Time{}, err
	}
	return u.Time(), nil
}
