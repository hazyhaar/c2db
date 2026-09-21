// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"testing"
)

func TestIDRoundtrip(t *testing.T) {
	const tsNs = uint64(1_704_067_200_123_456_789)
	cases := []struct {
		shard   uint16
		counter uint64
	}{
		{0, 0},
		{1, 1},
		{511, 2},
		{1023, counterMask},
		{7, 1 << 39},
	}
	for _, tc := range cases {
		id, err := NewID(tsNs, tc.shard, tc.counter)
		if err != nil {
			t.Fatalf("NewID(shard=%d counter=%d): %v", tc.shard, tc.counter, err)
		}
		if got := ShardOf(id); got != tc.shard {
			t.Fatalf("ShardOf: got %d want %d", got, tc.shard)
		}
		if got := CounterOf(id); got != tc.counter {
			t.Fatalf("CounterOf: got %d want %d", got, tc.counter)
		}
	}
}

func TestIDMonotonicCounter(t *testing.T) {
	const tsNs = uint64(1_704_067_200_000_000_000)
	const shard uint16 = 42
	a, err := NewID(tsNs, shard, 1)
	if err != nil {
		t.Fatalf("NewID counter=1: %v", err)
	}
	b, err := NewID(tsNs, shard, 2)
	if err != nil {
		t.Fatalf("NewID counter=2: %v", err)
	}
	if bytes.Compare(a[:], b[:]) >= 0 {
		t.Fatalf("memcmp 16 o: counter 1 then 2 at equal ms not increasing\n a=%x\n b=%x", a, b)
	}
}

func TestIDRejectShard(t *testing.T) {
	_, err := NewID(0, 1024, 0)
	if err == nil {
		t.Fatal("NewID shard=1024: expected rejection")
	}
	_, err = NewID(0, 1025, 1)
	if err == nil {
		t.Fatal("NewID shard=1025: expected rejection")
	}
}

func TestIDKindColl(t *testing.T) {
	const tsNs = uint64(1_704_067_200_000_000_000)
	id, err := NewIDKind(tsNs, 3, 9, IDKindFreeze, 12)
	if err != nil {
		t.Fatalf("NewIDKind: %v", err)
	}
	if ShardOf(id) != 3 || CounterOf(id) != 9 || KindOf(id) != IDKindFreeze || CollOf(id) != 12 {
		t.Fatalf("roundtrip shard=%d ctr=%d kind=%d coll=%d", ShardOf(id), CounterOf(id), KindOf(id), CollOf(id))
	}
	put, err := NewID(tsNs, 3, 9)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if KindOf(put) != IDKindPut || CollOf(put) != 0 {
		t.Fatalf("NewID default kind=%d coll=%d", KindOf(put), CollOf(put))
	}
}
