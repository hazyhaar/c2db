// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"sync"
)

type LockState int

const (
	LockNone LockState = iota
	LockShared
	LockReserved
	LockPending
	LockExclusive
)

var (
	ErrBusy         = errors.New("c2db: busy")
	ErrLockSequence = errors.New("c2db: lock sequence")
)

type FileLockRegistry struct {
	mu    sync.Mutex
	files map[string]*FileLockState
}

type FileLockState struct {
	mu      sync.Mutex
	holders map[uint64]LockState
}

func NewFileLockRegistry() *FileLockRegistry {
	return &FileLockRegistry{files: make(map[string]*FileLockState)}
}

func (r *FileLockRegistry) File(id string) *FileLockState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.files[id]
	if !ok {
		st = &FileLockState{holders: make(map[uint64]LockState)}
		r.files[id] = st
	}
	return st
}

func (s *FileLockState) Lock(connID uint64, target LockState) error {
	if target < LockNone || target > LockExclusive {
		return ErrLockSequence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.holders[connID]
	if target <= cur {
		return nil
	}
	switch target {
	case LockShared:
		if s.maxLock() >= LockPending {
			return ErrBusy
		}
	case LockReserved:
		if cur != LockShared {
			return ErrLockSequence
		}
		if s.maxOther(connID) >= LockReserved {
			return ErrBusy
		}
	case LockPending:
		if cur != LockReserved {
			return ErrLockSequence
		}
		if s.maxOther(connID) >= LockPending {
			return ErrBusy
		}
	case LockExclusive:
		if cur != LockShared && cur != LockReserved && cur != LockPending {
			return ErrLockSequence
		}
		if s.maxOther(connID) >= LockPending {
			return ErrBusy
		}
		if cur < LockPending {
			s.holders[connID] = LockPending
		}
		if s.hasOther(connID) {
			return ErrBusy
		}
		s.holders[connID] = LockExclusive
		return nil
	default:
		return ErrLockSequence
	}
	s.holders[connID] = target
	return nil
}

func (s *FileLockState) Unlock(connID uint64, target LockState) error {
	if target < LockNone || target > LockExclusive {
		return ErrLockSequence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.holders[connID]
	if target >= cur {
		return nil
	}
	if target == LockReserved {
		if s.maxOther(connID) >= LockReserved {
			return ErrBusy
		}
	}
	if target == LockNone {
		delete(s.holders, connID)
		return nil
	}
	s.holders[connID] = target
	return nil
}

func (s *FileLockState) CheckReservedLock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxLock() >= LockReserved
}

func (s *FileLockState) Level(connID uint64) LockState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holders[connID]
}

func (s *FileLockState) maxLock() LockState {
	var max LockState
	for _, st := range s.holders {
		if st > max {
			max = st
		}
	}
	return max
}

func (s *FileLockState) maxOther(except uint64) LockState {
	var max LockState
	for id, st := range s.holders {
		if id != except && st > max {
			max = st
		}
	}
	return max
}

func (s *FileLockState) hasOther(except uint64) bool {
	for id := range s.holders {
		if id != except {
			return true
		}
	}
	return false
}
