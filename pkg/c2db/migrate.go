// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"golang.org/x/sys/unix"
)

var (
	errMigrateSame     = errors.New("c2db: migrate from == to")
	errMigrateOccupied = errors.New("c2db: migrate destination occupied")
)

func Migrate(db *DB, from, to uint16, key [32]byte) error {
	if db == nil {
		return unix.EBADF
	}
	if from == to {
		return errMigrateSame
	}
	if from >= NumShards {
		return fmt.Errorf("c2db: shard %d >= %d", from, NumShards)
	}
	if to >= NumShards {
		return fmt.Errorf("c2db: shard %d >= %d", to, NumShards)
	}
	db.mu.RLock()
	if db.shards == nil {
		db.mu.RUnlock()
		return unix.EBADF
	}
	if _, ok := db.shards[to]; ok {
		db.mu.RUnlock()
		return errMigrateOccupied
	}
	db.mu.RUnlock()
	toDir := shardDir(db.dir, to)
	if err := checkDestFree(toDir); err != nil {
		return err
	}
	src, err := db.GetShard(from)
	if err != nil {
		return err
	}
	if err := src.flushPages(); err != nil {
		return err
	}
	if err := src.data.Flush(); err != nil {
		return err
	}
	if err := os.MkdirAll(toDir, 0o700); err != nil {
		return err
	}
	dataPath := filepath.Join(toDir, "data.img")
	walPath := filepath.Join(toDir, "wal.img")
	if err := copyImage(src.data, dataPath); err != nil {
		return err
	}
	if err := copyImage(src.wal.dev, walPath); err != nil {
		_ = os.Remove(dataPath)
		return err
	}
	if err := appendTopoTransition(db.dir, key, from, to); err != nil {
		_ = os.Remove(dataPath)
		_ = os.Remove(walPath)
		return err
	}
	return src.Close()
}

func checkDestFree(dir string) error {
	p := filepath.Join(dir, "data.img")
	st, err := os.Stat(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Size() > 0 {
		return errMigrateOccupied
	}
	return os.Remove(p)
}

func copyImage(src *Device, destPath string) error {
	dst, err := Create(destPath, src.size)
	if err != nil {
		return err
	}
	err = copyLBAs(src, dst)
	if err2 := dst.Close(); err == nil {
		err = err2
	}
	if err != nil {
		_ = os.Remove(destPath)
	}
	return err
}

func copyLBAs(src, dst *Device) error {
	if src.size != dst.size {
		return fmt.Errorf("c2db: migrate size mismatch %d != %d", src.size, dst.size)
	}
	nLBA := src.size / LBASize
	buf, err := mmapAnon(int(LBASize))
	if err != nil {
		return err
	}
	defer func() { _ = unix.Munmap(buf) }()
	for lba := uint64(0); lba < nLBA; lba++ {
		if err := src.Read(lba, buf); err != nil {
			return err
		}
		if err := dst.Write(lba, buf); err != nil {
			return err
		}
	}
	return dst.Flush()
}

func appendTopoTransition(dir string, key [32]byte, from, to uint16) error {
	path := filepath.Join(dir, "topo.img")
	j, err := openOrCreateTopo(path, shardBytes, key)
	if err != nil {
		return err
	}
	id, err := NewID(uint64(time.Now().UnixNano()), from, 0)
	if err != nil {
		_ = j.Close()
		return err
	}
	a := Artifact{
		Kind: byte(RecTopoTransition),
		ID:   id,
		Ref:  topoTransitionRef(from, to),
	}
	if err := j.Append(a); err != nil {
		_ = j.Close()
		return err
	}
	if err := j.Flush(); err != nil {
		_ = j.Close()
		return err
	}
	return j.Close()
}

func topoTransitionRef(from, to uint16) [32]byte {
	var pair [4]byte
	binary.LittleEndian.PutUint16(pair[0:2], from)
	binary.LittleEndian.PutUint16(pair[2:4], to)
	return blake3archtsim.Sum256(pair[:])
}
