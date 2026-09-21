// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"

	"github.com/hazyhaar/c2db/pkg/c2poly1305"
	"golang.org/x/sys/unix"
)

const (
	topoKindOff = 0
	topoIDOff   = 1
	topoRefOff  = 17
	topoTagOff  = 4080
	topoTagSize = 16
)

type Artifact struct {
	Kind byte
	ID   [16]byte
	Ref  [32]byte
}

type Topo struct {
	dev   *Device
	key   [32]byte
	next  uint64
	block []byte
}

var errTopoBadTag = errors.New("c2db: topo poly1305 tag mismatch")

func openOrCreateTopo(path string, size uint64, key [32]byte) (*Topo, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return CreateTopo(path, size, key)
	}
	if err != nil {
		return nil, err
	}
	return OpenTopo(path, key)
}

func CreateTopo(path string, size uint64, key [32]byte) (*Topo, error) {
	dev, err := Create(path, size)
	if err != nil {
		return nil, err
	}
	j, err := newTopo(dev, key)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	return j, nil
}

func OpenTopo(path string, key [32]byte) (*Topo, error) {
	dev, err := Open(path)
	if err != nil {
		return nil, err
	}
	j, err := newTopo(dev, key)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	if err := j.scanTip(); err != nil {
		_ = j.Close()
		return nil, err
	}
	return j, nil
}

func newTopo(dev *Device, key [32]byte) (*Topo, error) {
	block, err := unix.Mmap(-1, 0, LBASize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	return &Topo{dev: dev, key: key, block: block}, nil
}

func (j *Topo) Append(a Artifact) error {
	if err := j.ready(); err != nil {
		return err
	}
	maxLBA := j.dev.size / LBASize
	if j.next >= maxLBA {
		return fmt.Errorf("c2db: topo full (lba %d)", j.next)
	}
	clear(j.block)
	j.block[topoKindOff] = a.Kind
	copy(j.block[topoIDOff:topoIDOff+16], a.ID[:])
	copy(j.block[topoRefOff:topoRefOff+32], a.Ref[:])
	sealTopo(j.block, j.key)
	if err := j.dev.Write(j.next, j.block); err != nil {
		return err
	}
	j.next++
	return nil
}

func (j *Topo) Replay() ([]Artifact, error) {
	if err := j.ready(); err != nil {
		return nil, err
	}
	out := make([]Artifact, 0, j.next)
	for lba := uint64(0); lba < j.next; lba++ {
		if err := j.dev.Read(lba, j.block); err != nil {
			return nil, err
		}
		if !verifyTopo(j.block, j.key) {
			return nil, errTopoBadTag
		}
		var a Artifact
		a.Kind = j.block[topoKindOff]
		copy(a.ID[:], j.block[topoIDOff:topoIDOff+16])
		copy(a.Ref[:], j.block[topoRefOff:topoRefOff+32])
		out = append(out, a)
	}
	return out, nil
}

func (j *Topo) Flush() error {
	if err := j.ready(); err != nil {
		return err
	}
	return j.dev.Flush()
}

func (j *Topo) Close() error {
	if err := j.ready(); err != nil {
		return err
	}
	err := j.dev.Close()
	j.dev = nil
	if j.block != nil {
		_ = unix.Munmap(j.block)
		j.block = nil
	}
	return err
}

func (j *Topo) ready() error {
	if j == nil || j.dev == nil {
		return unix.EBADF
	}
	return j.dev.checkReady()
}

func (j *Topo) scanTip() error {
	maxLBA := j.dev.size / LBASize
	j.next = 0
	for lba := uint64(0); lba < maxLBA; lba++ {
		if err := j.dev.Read(lba, j.block); err != nil {
			return err
		}
		if topoBlockEmpty(j.block) {
			break
		}
		j.next = lba + 1
	}
	return nil
}

func topoBlockEmpty(block []byte) bool {
	for i := range block {
		if block[i] != 0 {
			return false
		}
	}
	return true
}

func sealTopo(block []byte, key [32]byte) {
	c2poly1305.Crypto_poly1305(block[topoTagOff:LBASize], block[:topoTagOff], uint64(topoTagOff), key[:])
}

func verifyTopo(block []byte, key [32]byte) bool {
	var tag [topoTagSize]byte
	c2poly1305.Crypto_poly1305(tag[:], block[:topoTagOff], uint64(topoTagOff), key[:])
	return subtle.ConstantTimeCompare(tag[:], block[topoTagOff:LBASize]) == 1
}
