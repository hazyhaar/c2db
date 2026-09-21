// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"sync/atomic"
	"time"
)

type ProbeOp uint8

const (
	ProbeOpNone ProbeOp = iota
	ProbeOpPread
	ProbeOpPwrite
	ProbeOpFdatasync
	ProbeOpPoison
	ProbeOpPagerEvict
	ProbeOpPagerFlush
	ProbeOpBarrierOrder
	ProbeOpPageSeal
	ProbeOpPageSkip
	ProbeOpRecordSkip
	ProbeOpWALFull
	ProbeOpWALScanTip
	ProbeOpWriterLock
	ProbeOpPublish
	ProbeOpViewOpen
	ProbeOpViewClose
	ProbeOpCloseErr
	ProbeOpGoidCost
	ProbeOpWALRepair
)

type ProbeEvent struct {
	TimestampNano int64
	ShardID       uint16
	Op            ProbeOp
	LBA           uint64
	Gen           uint64
	Errno         int32
	DurationNs    int64
	Details       string
}

type ProbeSink interface {
	Emit(ev ProbeEvent)
}

var globalProbeSink atomic.Pointer[ProbeSink]

// SetProbeSink enregistre ou révoque le puits d'observabilité global.
// Passer nil désactive l'ensemble des sondes avec un surcoût nul (chargement atomique d'un pointeur nil).
func SetProbeSink(sink ProbeSink) {
	if sink == nil {
		globalProbeSink.Store(nil)
		return
	}
	globalProbeSink.Store(&sink)
}

func probeEmit(shardID uint16, op ProbeOp, lba uint64, gen uint64, errno int32, dur time.Duration, details string) {
	p := globalProbeSink.Load()
	if p != nil && *p != nil {
		ev := ProbeEvent{
			TimestampNano: time.Now().UnixNano(),
			ShardID:       shardID,
			Op:            op,
			LBA:           lba,
			Gen:           gen,
			Errno:         errno,
			DurationNs:    dur.Nanoseconds(),
			Details:       details,
		}
		(*p).Emit(ev)
	}
}
