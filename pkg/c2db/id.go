// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"fmt"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
)

const (
	shardBits   = 10
	kindBits    = 4
	collBits    = 8
	counterBits = 40
	maxShard    = 1 << shardBits
	maxKind     = 1 << kindBits
	maxColl     = 1 << collBits
	counterMask = (uint64(1) << counterBits) - 1
	kindShift   = 58
	collShift   = 50
	shardShift  = 40
)

const (
	IDKindPut    uint8 = 0
	IDKindDel    uint8 = 1
	IDKindTrace  uint8 = 2
	IDKindFreeze uint8 = 3
	IDKindMap    uint8 = 4
)

func NewID(tsNs uint64, shard uint16, counter uint64) (c2uuidv7.UUID, error) {
	return NewIDKind(tsNs, shard, counter, IDKindPut, 0)
}

func NewIDKind(tsNs uint64, shard uint16, counter uint64, kind uint8, coll uint8) (c2uuidv7.UUID, error) {
	if shard >= maxShard {
		return c2uuidv7.UUID{}, fmt.Errorf("c2db: shard %d >= %d", shard, maxShard)
	}
	if uint16(kind) >= maxKind {
		return c2uuidv7.UUID{}, fmt.Errorf("c2db: kind %d >= %d", kind, maxKind)
	}
	seqOrRand := (uint64(kind) << kindShift) | (uint64(coll) << collShift) | (uint64(shard) << shardShift) | (counter & counterMask)
	return c2uuidv7.Compose(tsNs, seqOrRand), nil
}

func ShardOf(id c2uuidv7.UUID) uint16 {
	return uint16((overlay62(id) >> shardShift) & (maxShard - 1))
}

func CounterOf(id c2uuidv7.UUID) uint64 {
	return overlay62(id) & counterMask
}

func KindOf(id c2uuidv7.UUID) uint8 {
	return uint8(overlay62(id) >> kindShift)
}

func CollOf(id c2uuidv7.UUID) uint8 {
	return uint8((overlay62(id) >> collShift) & (maxColl - 1))
}

func overlay62(id c2uuidv7.UUID) uint64 {
	return uint64(id[8]&0x3F)<<56 |
		uint64(id[9])<<48 |
		uint64(id[10])<<40 |
		uint64(id[11])<<32 |
		uint64(id[12])<<24 |
		uint64(id[13])<<16 |
		uint64(id[14])<<8 |
		uint64(id[15])
}
