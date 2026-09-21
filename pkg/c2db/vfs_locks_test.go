// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVFSLocksNominalSequence(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("nominal.db")
	const conn = uint64(1)

	assertLevel := func(want LockState) {
		t.Helper()
		if got := f.Level(conn); got != want {
			t.Fatalf("level=%d want %d", got, want)
		}
	}
	assertReserved := func(want bool) {
		t.Helper()
		if got := f.CheckReservedLock(); got != want {
			t.Fatalf("CheckReservedLock=%v want %v", got, want)
		}
	}

	assertLevel(LockNone)
	assertReserved(false)

	if err := f.Lock(conn, LockShared); err != nil {
		t.Fatalf("Lock SHARED: %v", err)
	}
	assertLevel(LockShared)
	assertReserved(false)

	if err := f.Lock(conn, LockReserved); err != nil {
		t.Fatalf("Lock RESERVED: %v", err)
	}
	assertLevel(LockReserved)
	assertReserved(true)

	if err := f.Lock(conn, LockPending); err != nil {
		t.Fatalf("Lock PENDING: %v", err)
	}
	assertLevel(LockPending)
	assertReserved(true)

	if err := f.Lock(conn, LockExclusive); err != nil {
		t.Fatalf("Lock EXCLUSIVE: %v", err)
	}
	assertLevel(LockExclusive)
	assertReserved(true)

	if err := f.Unlock(conn, LockShared); err != nil {
		t.Fatalf("Unlock SHARED: %v", err)
	}
	assertLevel(LockShared)
	assertReserved(false)

	if err := f.Unlock(conn, LockNone); err != nil {
		t.Fatalf("Unlock NONE: %v", err)
	}
	assertLevel(LockNone)
	assertReserved(false)
}

func TestVFSLocksRejectDoubleReserved(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("reserved.db")
	const a, b = uint64(1), uint64(2)

	if err := f.Lock(a, LockShared); err != nil {
		t.Fatalf("a SHARED: %v", err)
	}
	if err := f.Lock(b, LockShared); err != nil {
		t.Fatalf("b SHARED: %v", err)
	}
	if err := f.Lock(a, LockReserved); err != nil {
		t.Fatalf("a RESERVED: %v", err)
	}
	if f.CheckReservedLock() != true {
		t.Fatal("CheckReservedLock=false after RESERVED")
	}
	err := f.Lock(b, LockReserved)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("b RESERVED: %v want ErrBusy", err)
	}
	if f.Level(b) != LockShared {
		t.Fatalf("b level=%d want SHARED", f.Level(b))
	}
}

func TestVFSLocksRejectSharedWhilePending(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("pending.db")
	const writer, reader, late = uint64(1), uint64(2), uint64(3)

	if err := f.Lock(reader, LockShared); err != nil {
		t.Fatalf("reader SHARED: %v", err)
	}
	if err := f.Lock(writer, LockShared); err != nil {
		t.Fatalf("writer SHARED: %v", err)
	}
	if err := f.Lock(writer, LockReserved); err != nil {
		t.Fatalf("writer RESERVED: %v", err)
	}
	if err := f.Lock(late, LockShared); err != nil {
		t.Fatalf("late SHARED under RESERVED: %v", err)
	}
	if err := f.Unlock(late, LockNone); err != nil {
		t.Fatalf("late Unlock: %v", err)
	}
	if err := f.Lock(writer, LockPending); err != nil {
		t.Fatalf("writer PENDING: %v", err)
	}
	if f.CheckReservedLock() != true {
		t.Fatal("CheckReservedLock=false under PENDING")
	}
	err := f.Lock(late, LockShared)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("late SHARED under PENDING: %v want ErrBusy", err)
	}
	if f.Level(reader) != LockShared {
		t.Fatalf("existing reader dropped: level=%d", f.Level(reader))
	}
	err = f.Lock(writer, LockExclusive)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("EXCLUSIVE with reader: %v want ErrBusy", err)
	}
	if err := f.Unlock(reader, LockNone); err != nil {
		t.Fatalf("reader Unlock: %v", err)
	}
	if err := f.Lock(writer, LockExclusive); err != nil {
		t.Fatalf("EXCLUSIVE after reader left: %v", err)
	}
}

func TestVFSLocksIllegalSequence(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("seq.db")
	const conn = uint64(1)

	if err := f.Lock(conn, LockReserved); !errors.Is(err, ErrLockSequence) {
		t.Fatalf("NONE->RESERVED: %v want ErrLockSequence", err)
	}
	if err := f.Lock(conn, LockShared); err != nil {
		t.Fatalf("SHARED: %v", err)
	}
	if err := f.Lock(conn, LockPending); !errors.Is(err, ErrLockSequence) {
		t.Fatalf("SHARED->PENDING: %v want ErrLockSequence", err)
	}
	if err := f.Lock(conn, LockExclusive); err != nil {
		t.Fatalf("SHARED->EXCLUSIVE: %v", err)
	}
	if f.Level(conn) != LockExclusive {
		t.Fatalf("level=%d want EXCLUSIVE", f.Level(conn))
	}
}

func TestVFSLocksDirectSharedToExclusive(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("crash-recovery.db")
	const writer, reader, late = uint64(1), uint64(2), uint64(3)

	if err := f.Lock(writer, LockShared); err != nil {
		t.Fatalf("writer SHARED: %v", err)
	}
	if err := f.Lock(writer, LockExclusive); err != nil {
		t.Fatalf("sole SHARED->EXCLUSIVE: %v", err)
	}
	if f.Level(writer) != LockExclusive {
		t.Fatalf("writer level=%d want EXCLUSIVE", f.Level(writer))
	}
	if err := f.Unlock(writer, LockNone); err != nil {
		t.Fatalf("writer Unlock: %v", err)
	}

	if err := f.Lock(reader, LockShared); err != nil {
		t.Fatalf("reader SHARED: %v", err)
	}
	if err := f.Lock(writer, LockShared); err != nil {
		t.Fatalf("writer SHARED again: %v", err)
	}
	err := f.Lock(writer, LockExclusive)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("SHARED->EXCLUSIVE with reader: %v want ErrBusy", err)
	}
	if f.Level(writer) != LockPending {
		t.Fatalf("writer level=%d want PENDING", f.Level(writer))
	}
	err = f.Lock(late, LockShared)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("late SHARED under PENDING: %v want ErrBusy", err)
	}
	if err := f.Unlock(reader, LockNone); err != nil {
		t.Fatalf("reader Unlock: %v", err)
	}
	if err := f.Lock(writer, LockExclusive); err != nil {
		t.Fatalf("EXCLUSIVE after reader left: %v", err)
	}
	if f.Level(writer) != LockExclusive {
		t.Fatalf("writer level=%d want EXCLUSIVE", f.Level(writer))
	}
}

func TestVFSLocksDirectReservedToExclusive(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("commit.db")
	const writer, reader, late = uint64(1), uint64(2), uint64(3)

	if err := f.Lock(writer, LockShared); err != nil {
		t.Fatalf("writer SHARED: %v", err)
	}
	if err := f.Lock(writer, LockReserved); err != nil {
		t.Fatalf("writer RESERVED: %v", err)
	}
	if err := f.Lock(writer, LockExclusive); err != nil {
		t.Fatalf("sole RESERVED->EXCLUSIVE: %v", err)
	}
	if f.Level(writer) != LockExclusive {
		t.Fatalf("writer level=%d want EXCLUSIVE", f.Level(writer))
	}
	if err := f.Unlock(writer, LockNone); err != nil {
		t.Fatalf("writer Unlock: %v", err)
	}

	if err := f.Lock(reader, LockShared); err != nil {
		t.Fatalf("reader SHARED: %v", err)
	}
	if err := f.Lock(writer, LockShared); err != nil {
		t.Fatalf("writer SHARED again: %v", err)
	}
	if err := f.Lock(writer, LockReserved); err != nil {
		t.Fatalf("writer RESERVED again: %v", err)
	}
	err := f.Lock(writer, LockExclusive)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("RESERVED->EXCLUSIVE with reader: %v want ErrBusy", err)
	}
	if f.Level(writer) != LockPending {
		t.Fatalf("writer level=%d want PENDING", f.Level(writer))
	}
	err = f.Lock(late, LockShared)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("late SHARED under PENDING: %v want ErrBusy", err)
	}
	if err := f.Unlock(reader, LockNone); err != nil {
		t.Fatalf("reader Unlock: %v", err)
	}
	if err := f.Lock(writer, LockExclusive); err != nil {
		t.Fatalf("EXCLUSIVE after reader left: %v", err)
	}
	if f.Level(writer) != LockExclusive {
		t.Fatalf("writer level=%d want EXCLUSIVE", f.Level(writer))
	}
}

func TestVFSLocksIdempotence(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("idempotent.db")
	const conn = uint64(1)

	if err := f.Lock(conn, LockShared); err != nil {
		t.Fatalf("SHARED: %v", err)
	}
	if err := f.Lock(conn, LockShared); err != nil {
		t.Fatalf("Lock SHARED again: %v", err)
	}
	if err := f.Lock(conn, LockNone); err != nil {
		t.Fatalf("Lock NONE while SHARED: %v", err)
	}
	if f.Level(conn) != LockShared {
		t.Fatalf("level=%d want SHARED after Lock <= cur", f.Level(conn))
	}
	if err := f.Lock(conn, LockReserved); err != nil {
		t.Fatalf("RESERVED: %v", err)
	}
	if err := f.Lock(conn, LockShared); err != nil {
		t.Fatalf("Lock SHARED while RESERVED: %v", err)
	}
	if f.Level(conn) != LockReserved {
		t.Fatalf("level=%d want RESERVED after Lock <= cur", f.Level(conn))
	}
	if err := f.Unlock(conn, LockExclusive); err != nil {
		t.Fatalf("Unlock EXCLUSIVE while RESERVED: %v", err)
	}
	if f.Level(conn) != LockReserved {
		t.Fatalf("level=%d want RESERVED after Unlock >= cur", f.Level(conn))
	}
	if err := f.Unlock(conn, LockReserved); err != nil {
		t.Fatalf("Unlock RESERVED while RESERVED: %v", err)
	}
	if f.Level(conn) != LockReserved {
		t.Fatalf("level=%d want RESERVED after Unlock == cur", f.Level(conn))
	}
}

func TestVFSLocksTorture(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("torture.db")
	const n = 20
	const rounds = 200

	var (
		sharedN    atomic.Int32
		reservedN  atomic.Int32
		exclusiveN atomic.Int32
		failures   atomic.Int32
	)
	fail := func(msg string) {
		t.Error(msg)
		failures.Add(1)
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		connID := uint64(i + 1)
		writer := i%4 == 0
		go func() {
			defer wg.Done()
			for r := 0; r < rounds && failures.Load() == 0; r++ {
				if writer {
					if !tortureWriter(f, connID, &sharedN, &reservedN, &exclusiveN, fail) {
						return
					}
					continue
				}
				if !tortureReader(f, connID, &sharedN, &exclusiveN, fail) {
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: 20 goroutines did not finish")
	}
	if failures.Load() != 0 {
		t.Fatalf("exclusion failures: %d", failures.Load())
	}
}

func tortureReader(f *FileLockState, connID uint64, sharedN, exclusiveN *atomic.Int32, fail func(string)) bool {
	for {
		err := f.Lock(connID, LockShared)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrBusy) {
			fail("reader Lock SHARED: " + err.Error())
			return false
		}
		runtime.Gosched()
	}
	sharedN.Add(1)
	if exclusiveN.Load() != 0 {
		fail("SHARED granted while EXCLUSIVE held")
		sharedN.Add(-1)
		_ = f.Unlock(connID, LockNone)
		return false
	}
	sharedN.Add(-1)
	if err := f.Unlock(connID, LockNone); err != nil {
		fail("reader Unlock: " + err.Error())
		return false
	}
	return true
}

func tortureWriter(f *FileLockState, connID uint64, sharedN, reservedN, exclusiveN *atomic.Int32, fail func(string)) bool {
	for {
		err := f.Lock(connID, LockShared)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrBusy) {
			fail("writer Lock SHARED: " + err.Error())
			return false
		}
		runtime.Gosched()
	}
	err := f.Lock(connID, LockReserved)
	if errors.Is(err, ErrBusy) {
		if err := f.Unlock(connID, LockNone); err != nil {
			fail("writer drop SHARED: " + err.Error())
			return false
		}
		runtime.Gosched()
		return true
	}
	if err != nil {
		fail("writer Lock RESERVED: " + err.Error())
		return false
	}
	reservedN.Add(1)
	if reservedN.Load() != 1 {
		fail("two RESERVED holders")
		reservedN.Add(-1)
		_ = f.Unlock(connID, LockNone)
		return false
	}
	if err := f.Lock(connID, LockPending); err != nil {
		fail("writer Lock PENDING: " + err.Error())
		reservedN.Add(-1)
		_ = f.Unlock(connID, LockNone)
		return false
	}
	for {
		err := f.Lock(connID, LockExclusive)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrBusy) {
			fail("writer Lock EXCLUSIVE: " + err.Error())
			reservedN.Add(-1)
			_ = f.Unlock(connID, LockNone)
			return false
		}
		runtime.Gosched()
	}
	exclusiveN.Add(1)
	if exclusiveN.Load() != 1 {
		fail("two EXCLUSIVE holders")
		exclusiveN.Add(-1)
		reservedN.Add(-1)
		_ = f.Unlock(connID, LockNone)
		return false
	}
	if sharedN.Load() != 0 {
		fail("EXCLUSIVE granted while SHARED held")
		exclusiveN.Add(-1)
		reservedN.Add(-1)
		_ = f.Unlock(connID, LockNone)
		return false
	}
	exclusiveN.Add(-1)
	reservedN.Add(-1)
	if err := f.Unlock(connID, LockNone); err != nil {
		fail("writer Unlock: " + err.Error())
		return false
	}
	return true
}

func TestVFSLocks_DoublePendingRejection(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("double_pending.db")
	const c1, c2 = uint64(1), uint64(2)

	// Sous-cas décisif (C2 déjà Reserved, C1 tente Exclusive) :
	// 1. C2 : Shared puis Reserved. Réussit.
	if err := f.Lock(c2, LockShared); err != nil {
		t.Fatalf("c2 SHARED: %v", err)
	}
	if err := f.Lock(c2, LockReserved); err != nil {
		t.Fatalf("c2 RESERVED: %v", err)
	}

	// 2. C1 : Shared. Réussit (Reserved n'interdit pas les lecteurs).
	if err := f.Lock(c1, LockShared); err != nil {
		t.Fatalf("c1 SHARED: %v", err)
	}

	// 3. C1 : Exclusive → ErrBusy, Level(C1)==Pending (hasOther : C2 Reserved).
	if err := f.Lock(c1, LockExclusive); !errors.Is(err, ErrBusy) {
		t.Fatalf("c1 EXCLUSIVE: %v want ErrBusy", err)
	}
	if lvl := f.Level(c1); lvl != LockPending {
		t.Fatalf("c1 level=%d want PENDING", lvl)
	}

	// 4. C2 : Exclusive → ErrBusy ET Level(C2)==Reserved (PAS Pending : la garde maxOther a bloqué AVANT promotion).
	if err := f.Lock(c2, LockExclusive); !errors.Is(err, ErrBusy) {
		t.Fatalf("c2 EXCLUSIVE: %v want ErrBusy", err)
	}
	if lvl := f.Level(c2); lvl != LockReserved {
		t.Fatalf("c2 level=%d want RESERVED", lvl)
	}

	// 5. C2 : Pending → ErrBusy, Level(C2)==Reserved.
	if err := f.Lock(c2, LockPending); !errors.Is(err, ErrBusy) {
		t.Fatalf("c2 PENDING: %v want ErrBusy", err)
	}
	if lvl := f.Level(c2); lvl != LockReserved {
		t.Fatalf("c2 level=%d want RESERVED", lvl)
	}

	// Nettoyer (Unlock None C1 et C2), puis sous-cas Shared concurrent :
	if err := f.Unlock(c1, LockNone); err != nil {
		t.Fatalf("c1 unlock: %v", err)
	}
	if err := f.Unlock(c2, LockNone); err != nil {
		t.Fatalf("c2 unlock: %v", err)
	}

	const c3, c4 = uint64(3), uint64(4)

	// 1. C3 : Shared.
	if err := f.Lock(c3, LockShared); err != nil {
		t.Fatalf("c3 SHARED: %v", err)
	}

	// 2. C1 : Shared puis Reserved.
	if err := f.Lock(c1, LockShared); err != nil {
		t.Fatalf("c1 SHARED: %v", err)
	}
	if err := f.Lock(c1, LockReserved); err != nil {
		t.Fatalf("c1 RESERVED: %v", err)
	}

	// 3. C4 : Shared (avant Pending de C1, sinon Shared serait refusé).
	if err := f.Lock(c4, LockShared); err != nil {
		t.Fatalf("c4 SHARED: %v", err)
	}

	// 4. C1 : Exclusive → ErrBusy, C1=Pending.
	if err := f.Lock(c1, LockExclusive); !errors.Is(err, ErrBusy) {
		t.Fatalf("c1 EXCLUSIVE: %v want ErrBusy", err)
	}
	if lvl := f.Level(c1); lvl != LockPending {
		t.Fatalf("c1 level=%d want PENDING", lvl)
	}

	// 5. C4 : Exclusive → ErrBusy, Level(C4)==Shared (pas promu Pending).
	if err := f.Lock(c4, LockExclusive); !errors.Is(err, ErrBusy) {
		t.Fatalf("c4 EXCLUSIVE: %v want ErrBusy", err)
	}
	if lvl := f.Level(c4); lvl != LockShared {
		t.Fatalf("c4 level=%d want SHARED", lvl)
	}

	// 6. C4 : Pending → ErrLockSequence, Level(C4)==Shared.
	if err := f.Lock(c4, LockPending); !errors.Is(err, ErrLockSequence) {
		t.Fatalf("c4 PENDING: %v want ErrLockSequence", err)
	}
	if lvl := f.Level(c4); lvl != LockShared {
		t.Fatalf("c4 level=%d want SHARED", lvl)
	}
}

func TestVFSLocks_WriterStarvationPrevention(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("starvation.db")
	const c1, c3, c4 = uint64(1), uint64(3), uint64(4)

	// 1. C3 Shared.
	if err := f.Lock(c3, LockShared); err != nil {
		t.Fatalf("c3 SHARED: %v", err)
	}

	// 2. C1 Shared puis Reserved.
	if err := f.Lock(c1, LockShared); err != nil {
		t.Fatalf("c1 SHARED: %v", err)
	}
	if err := f.Lock(c1, LockReserved); err != nil {
		t.Fatalf("c1 RESERVED: %v", err)
	}

	// 3. C1 Exclusive → ErrBusy, C1=Pending.
	if err := f.Lock(c1, LockExclusive); !errors.Is(err, ErrBusy) {
		t.Fatalf("c1 EXCLUSIVE: %v want ErrBusy", err)
	}
	if lvl := f.Level(c1); lvl != LockPending {
		t.Fatalf("c1 level=%d want PENDING", lvl)
	}

	// 4. C4 Shared → ErrBusy.
	if err := f.Lock(c4, LockShared); !errors.Is(err, ErrBusy) {
		t.Fatalf("c4 SHARED: %v want ErrBusy", err)
	}

	// 5. C3 Unlock None.
	if err := f.Unlock(c3, LockNone); err != nil {
		t.Fatalf("c3 Unlock: %v", err)
	}

	// 6. C1 Exclusive → nil, Level==Exclusive.
	if err := f.Lock(c1, LockExclusive); err != nil {
		t.Fatalf("c1 EXCLUSIVE: %v want nil", err)
	}
	if lvl := f.Level(c1); lvl != LockExclusive {
		t.Fatalf("c1 level=%d want EXCLUSIVE", lvl)
	}
}

func TestVFSLocks_ExtremeConcurrentFuzz(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("fuzz.db")
	const n = 50
	const rounds = 50

	var failures atomic.Int32
	fail := func(msg string) {
		t.Error(msg)
		failures.Add(1)
	}

	checkInvariants := func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		exclusiveCount := 0
		pendingCount := 0
		for _, st := range f.holders {
			if st == LockExclusive {
				exclusiveCount++
			} else if st == LockPending {
				pendingCount++
			}
		}
		if exclusiveCount > 1 {
			fail(fmt.Sprintf("invariant violé: %d Exclusive holders", exclusiveCount))
		}
		if pendingCount > 1 {
			fail(fmt.Sprintf("invariant violé: %d Pending holders", pendingCount))
		}
		if exclusiveCount > 0 {
			for id, st := range f.holders {
				if st != LockExclusive && st > LockNone {
					fail(fmt.Sprintf("invariant violé: Exclusive tenu mais conn %d a niveau %d", id, st))
				}
			}
		}
		if pendingCount > 0 {
			for id, st := range f.holders {
				if st != LockPending && st >= LockPending {
					fail(fmt.Sprintf("invariant violé: Pending tenu mais conn %d a niveau %d", id, st))
				}
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		connID := uint64(i + 1)
		go func() {
			defer wg.Done()
			defer func() {
				_ = f.Unlock(connID, LockNone)
				checkInvariants()
			}()

			rng := rand.New(rand.NewSource(int64(connID)))
			for r := 0; r < rounds && failures.Load() == 0; r++ {
				action := rng.Intn(4)
				var err error
				switch action {
				case 0:
					err = f.Lock(connID, LockShared)
				case 1:
					err = f.Lock(connID, LockReserved)
				case 2:
					err = f.Lock(connID, LockExclusive)
				case 3:
					err = f.Unlock(connID, LockNone)
				}
				checkInvariants()

				if err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrLockSequence) {
					fail(fmt.Sprintf("conn %d action %d: erreur inattendue %v", connID, action, err))
					return
				}

				if errors.Is(err, ErrBusy) && f.Level(connID) == LockPending {
					for retries := 0; retries < 200 && f.Level(connID) == LockPending && failures.Load() == 0; retries++ {
						runtime.Gosched()
						if retries > 50 && rng.Intn(4) == 0 {
							uErr := f.Unlock(connID, LockNone)
							checkInvariants()
							if uErr != nil {
								fail(fmt.Sprintf("conn %d unlock: %v", connID, uErr))
							}
							break
						}
						lockErr := f.Lock(connID, LockExclusive)
						checkInvariants()
						if lockErr == nil {
							break
						}
						if !errors.Is(lockErr, ErrBusy) {
							fail(fmt.Sprintf("conn %d retry exclusive: %v", connID, lockErr))
							break
						}
					}
					if f.Level(connID) == LockPending {
						uErr := f.Unlock(connID, LockNone)
						checkInvariants()
						if uErr != nil {
							fail(fmt.Sprintf("conn %d final pending unlock: %v", connID, uErr))
						}
					}
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("deadlock: timeout 20s")
	}
	if failures.Load() != 0 {
		t.Fatalf("fuzz invariant failures: %d", failures.Load())
	}
}

func TestVFSLocks_RejectIllegalReservedDowngrade(t *testing.T) {
	reg := NewFileLockRegistry()
	f := reg.File("illegal_downgrade.db")
	const c1, c2 = uint64(1), uint64(2)

	// 1. c1 acquiert LockShared puis LockReserved
	if err := f.Lock(c1, LockShared); err != nil {
		t.Fatalf("c1 Lock SHARED: %v", err)
	}
	if err := f.Lock(c1, LockReserved); err != nil {
		t.Fatalf("c1 Lock RESERVED: %v", err)
	}
	if lvl := f.Level(c1); lvl != LockReserved {
		t.Fatalf("c1 level=%d want RESERVED", lvl)
	}

	// 2. c2 acquiert LockShared (autorisé concurremment avec LockReserved)
	if err := f.Lock(c2, LockShared); err != nil {
		t.Fatalf("c2 Lock SHARED: %v", err)
	}

	// 3. c2 tente LockExclusive -> ErrBusy, et c2 passe en LockPending
	err := f.Lock(c2, LockExclusive)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("c2 Lock EXCLUSIVE attendu ErrBusy, obtenu: %v", err)
	}
	if lvl := f.Level(c2); lvl != LockPending {
		t.Fatalf("c2 level=%d want PENDING", lvl)
	}

	// 4. c2 tente une rétrogradation vers LockReserved alors que c1 détient déjà LockReserved
	// Doit être formellement rejeté avec ErrBusy
	err = f.Unlock(c2, LockReserved)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("c2 Unlock RESERVED attendu ErrBusy, obtenu: %v", err)
	}
	if lvl := f.Level(c2); lvl != LockPending {
		t.Fatalf("c2 level=%d want PENDING (inchangé après rejet)", lvl)
	}

	// 5. Une rétrogradation vers LockShared doit quant à elle réussir
	if err := f.Unlock(c2, LockShared); err != nil {
		t.Fatalf("c2 Unlock SHARED: %v", err)
	}
	if lvl := f.Level(c2); lvl != LockShared {
		t.Fatalf("c2 level=%d want SHARED", lvl)
	}

	// 6. Test direct depuis LockExclusive (connexion seule acquiert Exclusive)
	if err := f.Unlock(c1, LockNone); err != nil {
		t.Fatalf("c1 Unlock NONE: %v", err)
	}
	if err := f.Unlock(c2, LockNone); err != nil {
		t.Fatalf("c2 Unlock NONE: %v", err)
	}

	// c1 prend Exclusive (seul)
	if err := f.Lock(c1, LockShared); err != nil {
		t.Fatalf("c1 Lock SHARED: %v", err)
	}
	if err := f.Lock(c1, LockExclusive); err != nil {
		t.Fatalf("c1 Lock EXCLUSIVE: %v", err)
	}
	// Rétrogradation légale de c1 vers LockReserved car aucune autre connexion ne détient LockReserved
	if err := f.Unlock(c1, LockReserved); err != nil {
		t.Fatalf("c1 Unlock RESERVED sans conflit attendu réussi, obtenu: %v", err)
	}
	if lvl := f.Level(c1); lvl != LockReserved {
		t.Fatalf("c1 level=%d want RESERVED", lvl)
	}
}
