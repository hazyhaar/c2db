// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"context"
	"errors"
)

// ErrReplicationQuorum signale qu'une barrière de durabilité répliquée n'a pas
// obtenu l'acquittement d'un quorum de réplicas avant l'annulation du contexte.
var ErrReplicationQuorum = errors.New("c2db: replication quorum not reached")

// ReplicationSink porte la durabilité sous CommitReplicated. Le moteur publie
// chaque enregistrement WAL et mémorise la séquence de réplication attribuée ;
// DB.Sync attend ensuite l'acquittement de quorum de cette séquence. Le contrat
// vit dans c2db, mais l'implémentation réelle réside dans la couche de
// réplication (c2repl), ce qui évite toute dépendance circulaire c2db -> c2repl.
//
// Un ReplicationSink branché ne remplace pas la barrière locale : le pointage
// fdatasync du journal demeure une optimisation de vitesse de reprise. Il
// ajoute la condition d'acquittement qui, seule, déclare l'écriture durable.
type ReplicationSink interface {
	// Publish diffuse un enregistrement WAL et retourne sa séquence de
	// réplication monotone.
	Publish(shard uint16, recType byte, key, val []byte) uint64
	// WaitAck bloque jusqu'à l'acquittement de quorum de seq, ou jusqu'à
	// l'annulation du contexte.
	WaitAck(ctx context.Context, seq uint64) error
}

// SetReplicationSink branche le dispositif de durabilité répliquée sur tous les
// shards, présents et futurs, de la base. Sous CommitReplicated, DB.Sync attend
// alors l'acquittement de quorum au lieu du seul pointage local. Un sink nul
// rétablit le repli local.
func (db *DB) SetReplicationSink(sink ReplicationSink) {
	if db == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.replSink = sink
	for _, s := range db.shards {
		s.SetReplicationSink(sink)
	}
}

// SetReplicationSink branche le dispositif de durabilité répliquée sur le shard.
func (s *Shard) SetReplicationSink(sink ReplicationSink) {
	if s == nil {
		return
	}
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	s.replSink = sink
}

// LastReplSeq retourne la plus haute séquence de réplication publiée par ce
// shard, ou zéro en l'absence de dispositif branché.
func (s *Shard) LastReplSeq() uint64 {
	if s == nil {
		return 0
	}
	return s.lastReplSeq.Load()
}

// ReplicationSink retourne le dispositif de durabilité répliquée branché sur le
// shard, ou nil. L'accès est protégé par le verrou des crochets, ce qui le rend
// sûr vis-à-vis de SetReplicationSink.
func (s *Shard) ReplicationSink() ReplicationSink {
	if s == nil {
		return nil
	}
	s.hookMu.RLock()
	defer s.hookMu.RUnlock()
	return s.replSink
}

// replicate publie un enregistrement WAL vers le dispositif de réplication
// quand un sink est branché, et mémorise la séquence retournée ; à défaut, il
// se replie sur le crochet de diffusion historique sans acquittement.
func (s *Shard) replicate(shard uint16, recType byte, key, val []byte) {
	s.hookMu.RLock()
	sink := s.replSink
	hook := s.replHook
	s.hookMu.RUnlock()
	if sink != nil {
		seq := sink.Publish(shard, recType, key, val)
		for {
			cur := s.lastReplSeq.Load()
			if seq <= cur || s.lastReplSeq.CompareAndSwap(cur, seq) {
				break
			}
		}
		return
	}
	if hook != nil {
		hook(shard, recType, key, val)
	}
}
