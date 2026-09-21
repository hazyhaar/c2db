// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/hazyhaar/c2db/pkg/blake3"
)

func TestRoute_VFSColocation(t *testing.T) {
	const tenant = "tenant_prod"
	const fileID = "test.db"
	const gen uint64 = 1

	want := Route(MetaKey(tenant, fileID))
	for blockNo := uint64(0); blockNo <= 1000; blockNo++ {
		got := Route(BlockKey(tenant, fileID, gen, blockNo))
		if got != want {
			t.Fatalf("blockNo=%d: shard %d, want %d", blockNo, got, want)
		}
	}

	for _, id := range []string{"test.db", "test.db-journal", "test.db-wal", "test.db-shm"} {
		if got := Route(MetaKey(tenant, id)); got != want {
			t.Fatalf("MetaKey %s: shard %d, want %d", id, got, want)
		}
		if got := Route(BlockKey(tenant, id, gen, 0)); got != want {
			t.Fatalf("BlockKey %s: shard %d, want %d", id, got, want)
		}
	}

	seen := make(map[uint16]struct{})
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("db%d.db", i)
		seen[Route(MetaKey(tenant, id))] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("8 fileID dbN.db: %d shard IDs, want at least 2", len(seen))
	}

	key := []byte("users:123")
	sum := blake3archtsim.Sum256(key)
	wantFull := binary.BigEndian.Uint16(sum[:2]) >> 6
	if got := Route(key); got != wantFull {
		t.Fatalf("clé hors VFS: shard %d, want %d", got, wantFull)
	}
}
