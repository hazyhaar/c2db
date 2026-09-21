// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"errors"
	"strings"
	"time"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
)

const CollectionSep byte = 0x1F

var (
	catalogPrefix            = []byte{0x00, 'C', CollectionSep}
	ErrInvalidCollectionName = errors.New("c2db: invalid collection name")
	ErrEmptyCollectionKey    = errors.New("c2db: empty collection key")
	ErrNoCollection          = errors.New("c2db: collection does not exist")
	ErrCollectionExists      = errors.New("c2db: collection already exists")
)

func collectionPrefix(name string) []byte {
	p := make([]byte, len(name)+1)
	copy(p, name)
	p[len(name)] = CollectionSep
	return p
}

func collectionKey(name string, key []byte) []byte {
	k := make([]byte, len(name)+1+len(key))
	copy(k, name)
	k[len(name)] = CollectionSep
	copy(k[len(name)+1:], key)
	return k
}

func catalogKey(name string) []byte {
	k := make([]byte, len(catalogPrefix)+len(name))
	copy(k, catalogPrefix)
	copy(k[len(catalogPrefix):], name)
	return k
}

func validCollectionName(name string) bool {
	if name == "" {
		return false
	}
	if strings.IndexByte(name, CollectionSep) >= 0 {
		return false
	}
	if strings.IndexByte(name, 0) >= 0 {
		return false
	}
	return true
}

func StripCollectionPrefix(name string, rawKey []byte) ([]byte, bool) {
	prefix := collectionPrefix(name)
	if bytes.HasPrefix(rawKey, prefix) {
		return rawKey[len(prefix):], true
	}
	return nil, false
}

func (s *Shard) collectionExists(name string) (bool, error) {
	_, err := s.Get(catalogKey(name))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	return false, err
}

func (s *Shard) CreateCollection(name string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !validCollectionName(name) {
		return ErrInvalidCollectionName
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return err
	}
	if ok {
		return ErrCollectionExists
	}
	return s.Put(catalogKey(name), []byte{1})
}

func (s *Shard) ListCollections() ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	raw, err := s.ScanPrefix(catalogPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, k := range raw {
		if !bytes.HasPrefix(k, catalogPrefix) {
			continue
		}
		out = append(out, string(k[len(catalogPrefix):]))
	}
	return out, nil
}

func (s *Shard) PutIn(name string, key, val []byte) error {
	if !validCollectionName(name) {
		return ErrInvalidCollectionName
	}
	if len(key) == 0 {
		return ErrEmptyCollectionKey
	}
	if err := s.ready(); err != nil {
		return err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoCollection
	}
	return s.Put(collectionKey(name, key), val)
}

func (s *Shard) GetIn(name string, key []byte) ([]byte, error) {
	if !validCollectionName(name) {
		return nil, ErrInvalidCollectionName
	}
	if len(key) == 0 {
		return nil, ErrEmptyCollectionKey
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoCollection
	}
	return s.Get(collectionKey(name, key))
}

// GetFrom returns the value for a key from a collection.
// This is the counterpart to PutIn for read validation.
func (s *Shard) GetFrom(name string, key []byte) ([]byte, error) {
	return s.GetIn(name, key)
}

func (s *Shard) Scan(name string) ([][]byte, error) {
	if !validCollectionName(name) {
		return nil, ErrInvalidCollectionName
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoCollection
	}
	return s.ScanPrefix(collectionPrefix(name))
}

func (s *Shard) DropCollection(name string) error {
	if !validCollectionName(name) {
		return ErrInvalidCollectionName
	}
	if err := s.ready(); err != nil {
		return err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoCollection
	}
	keys, err := s.ScanPrefix(collectionPrefix(name))
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := s.Delete(k); err != nil {
			return err
		}
	}
	return s.Delete(catalogKey(name))
}

func collByte(name string) uint8 {
	sum := blake3archtsim.Sum256([]byte(name))
	return sum[0]
}

func idFromCollKey(name string, raw []byte) (c2uuidv7.UUID, bool) {
	suf, ok := StripCollectionPrefix(name, raw)
	if !ok || len(suf) != 16 {
		return c2uuidv7.UUID{}, false
	}
	var id c2uuidv7.UUID
	copy(id[:], suf)
	return id, true
}

func (s *Shard) Insert(name string, doc []byte) (c2uuidv7.UUID, error) {
	var zero c2uuidv7.UUID
	if !validCollectionName(name) {
		return zero, ErrInvalidCollectionName
	}
	if len(doc) == 0 {
		return zero, errQLOp
	}
	if err := s.ready(); err != nil {
		return zero, err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return zero, err
	}
	if !ok {
		return zero, ErrNoCollection
	}
	id, err := NewIDKind(uint64(time.Now().UnixNano()), s.id, s.counter, IDKindPut, collByte(name))
	if err != nil {
		return zero, err
	}
	if err := s.Put(collectionKey(name, id[:]), doc); err != nil {
		return zero, err
	}
	return id, nil
}

func (s *Shard) GetDoc(name string, id c2uuidv7.UUID) ([]byte, error) {
	if !validCollectionName(name) {
		return nil, ErrInvalidCollectionName
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoCollection
	}
	return s.Get(collectionKey(name, id[:]))
}

func (s *Shard) GetDocAsOf(name string, id c2uuidv7.UUID, snap c2uuidv7.UUID) ([]byte, error) {
	if !validCollectionName(name) {
		return nil, ErrInvalidCollectionName
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoCollection
	}
	return s.GetAsOf(collectionKey(name, id[:]), [16]byte(snap))
}

func (s *Shard) DeleteDoc(name string, id c2uuidv7.UUID) error {
	if !validCollectionName(name) {
		return ErrInvalidCollectionName
	}
	if err := s.ready(); err != nil {
		return err
	}
	ok, err := s.collectionExists(name)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoCollection
	}
	return s.Delete(collectionKey(name, id[:]))
}
