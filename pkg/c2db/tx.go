// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
)

var (
	// ErrTxClosed signale qu'une opération est tentée sur une transaction déjà clôturée.
	ErrTxClosed = errors.New("c2db: transaction closed")
	// ErrPostCommit signale qu'une défaillance est survenue après l'engagement durable WAL.
	// La transaction est scellée de manière irrévocable dans le journal et sera restaurée au redémarrage.
	ErrPostCommit = errors.New("c2db: transaction durably committed to WAL, post-commit maintenance error")
)

// Tx représente un contexte de transaction explicite à zéro allocation de runtime.
type Tx struct {
	shard           *Shard
	id              c2uuidv7.UUID
	closed          atomic.Bool
	mutated         bool
	initialHeapRoot uint64
	initialHeapUsed uint64
	initialWALNext  uint64
}

// Begin ouvre une transaction explicite sur le Shard. Une vue en lecture seule
// refuse l'ouverture : les écritures de txPut/txDelete ciblent le tampon de
// travail dirty, absent de ce mode.
func (s *Shard) Begin() (*Tx, error) {
	if s != nil && s.readOnly {
		return nil, ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.lockWriter(); err != nil {
		return nil, err
	}
	if s.groupDepth > 0 {
		s.unlockWriter()
		return nil, ErrWriterBusy
	}
	s.enterGroup()
	id := c2uuidv7.New()
	var walNext uint64
	if s.wal != nil {
		walNext = s.wal.next
	}
	return &Tx{
		shard:           s,
		id:              id,
		initialHeapRoot: s.heapRoot,
		initialHeapUsed: s.heapUsed,
		initialWALNext:  walNext,
	}, nil
}

// ID retourne l'identifiant UUIDv7 unique de la transaction.
func (tx *Tx) ID() c2uuidv7.UUID {
	return tx.id
}

// Put insère ou met à jour une clé dans la transaction.
func (tx *Tx) Put(key, val []byte) error {
	if tx.closed.Load() {
		return ErrTxClosed
	}
	tx.mutated = true
	return tx.shard.txPut(tx.id, key, val)
}

// Delete supprime une clé dans la transaction.
func (tx *Tx) Delete(key []byte) error {
	if tx.closed.Load() {
		return ErrTxClosed
	}
	tx.mutated = true
	return tx.shard.txDelete(tx.id, key)
}

// Commit valide et persiste les modifications de la transaction.
// Respecte l'ordre canonique : émission du marqueur RecTxCommit, barrière de durabilité WAL, puis publication mémoire.
func (tx *Tx) Commit() error {
	if !tx.closed.CompareAndSwap(false, true) {
		return ErrTxClosed
	}
	defer tx.shard.unlockWriter()
	if tx.mutated {
		// Étape 0 : réservation formelle de capacité de publication AVANT tout engagement matériel
		if err := tx.shard.preparePublish(); err != nil {
			tx.shard.rollbackState(tx.initialHeapRoot, tx.initialHeapUsed, tx.initialWALNext)
			tx.shard.abortGroup()
			return err
		}

		// Étape 1 : émission du marqueur de scellement transactionnel RecTxCommit
		if tx.shard.wal != nil {
			commitRec := Record{ID: tx.id, Type: RecTxCommit}
			if err := tx.shard.wal.Append(commitRec); err != nil {
				tx.shard.rollbackState(tx.initialHeapRoot, tx.initialHeapUsed, tx.initialWALNext)
				tx.shard.abortGroup()
				return err
			}
			// Étape 2 : barrière matérielle de durabilité WAL (fdatasync)
			if err := tx.shard.wal.Flush(); err != nil {
				tx.shard.rollbackState(tx.initialHeapRoot, tx.initialHeapUsed, tx.initialWALNext)
				tx.shard.abortGroup()
				return err
			}
		}

		// POINT D'ENGAGEMENT DURABLE FRANCHI :
		// La transaction est désormais scellée de manière permanente et irrévocable dans le journal.
		// Étape 3 : publication en mémoire
		if err := tx.shard.publish(); err != nil {
			postErr := fmt.Errorf("%w: publish error: %v", ErrPostCommit, err)
			tx.shard.poison(postErr)
			_ = tx.shard.leaveGroup()
			return postErr
		}
		if err := tx.shard.leaveGroup(); err != nil {
			postErr := fmt.Errorf("%w: leaveGroup error: %v", ErrPostCommit, err)
			tx.shard.poison(postErr)
			return postErr
		}
	}
	return nil
}

// Rollback annule les modifications de la transaction.
func (tx *Tx) Rollback() error {
	if !tx.closed.CompareAndSwap(false, true) {
		return ErrTxClosed
	}
	defer tx.shard.unlockWriter()
	if tx.mutated {
		tx.shard.rollbackState(tx.initialHeapRoot, tx.initialHeapUsed, tx.initialWALNext)
	}
	tx.shard.abortGroup()
	return nil
}

// BeginShard ouvre une transaction explicite sur un shard spécifié.
func (db *DB) BeginShard(shardID uint16) (*Tx, error) {
	s, err := db.GetShard(shardID)
	if err != nil {
		return nil, err
	}
	return s.Begin()
}

// BeginKey ouvre une transaction explicite sur le shard correspondant à la clé routée.
func (db *DB) BeginKey(key []byte) (*Tx, error) {
	return db.BeginShard(Route(key))
}
