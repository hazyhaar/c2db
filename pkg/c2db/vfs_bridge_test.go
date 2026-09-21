// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"errors"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

func TestVFSBusyOrMapsHeapFull(t *testing.T) {
	if got := vfsBusyOr(ErrHeapFull, 778); got != sqlite3.SQLITE_BUSY {
		t.Fatalf("vfsBusyOr(ErrHeapFull): got %d want SQLITE_BUSY=%d", got, sqlite3.SQLITE_BUSY)
	}
	if got := vfsBusyOr(errInsert, 778); got != sqlite3.SQLITE_BUSY {
		t.Fatalf("vfsBusyOr(errInsert): got %d want SQLITE_BUSY", got)
	}
	if got := vfsBusyOr(ErrWriterBusy, 778); got != sqlite3.SQLITE_BUSY {
		t.Fatalf("vfsBusyOr(ErrWriterBusy): got %d want SQLITE_BUSY", got)
	}
	if got := vfsBusyOr(ErrViewHeld, 778); got != sqlite3.SQLITE_BUSY {
		t.Fatalf("vfsBusyOr(ErrViewHeld): got %d want SQLITE_BUSY", got)
	}
	if got := vfsBusyOr(ErrBusy, 778); got != sqlite3.SQLITE_BUSY {
		t.Fatalf("vfsBusyOr(ErrBusy): got %d want SQLITE_BUSY", got)
	}
	other := errors.New("c2db: unrelated")
	if got := vfsBusyOr(other, 778); got != 778 {
		t.Fatalf("vfsBusyOr unmapped: got %d want 778", got)
	}
	if isVFSBusy(ErrHeapFull) != true {
		t.Fatalf("isVFSBusy(ErrHeapFull)=false")
	}
	if isVFSBusy(errInsert) != true {
		t.Fatalf("isVFSBusy(errInsert)=false")
	}
}
