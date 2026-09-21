// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
	"golang.org/x/sys/unix"
)

const NumShards = 1024
const activeWords = NumShards / 64
const fdLimitTarget = 65536

// defaultCommitWindow est la borne de perte par défaut de CommitImmediate : une
// écriture isolée est pointée au plus tard une fenêtre après la première
// écriture non pointée du groupe.
const defaultCommitWindow = 2 * time.Millisecond

// CommitMode fixe le régime de durabilité locale d'une base.
type CommitMode uint8

const (
	// CommitImmediate pointe le journal par groupe de walCoalesceN écritures,
	// borné par la fenêtre par défaut. C'est le régime par défaut.
	CommitImmediate CommitMode = iota
	// CommitWindowed pointe le journal à l'échéance de Window depuis la
	// première écriture non pointée (et toujours au compteur walCoalesceN).
	CommitWindowed
	// CommitReplicated ne requiert pas la durabilité locale : la garantie
	// appartient à la réplication/quorum. Quand un ReplicationSink est branché
	// (SetReplicationSink), DB.Sync attend l'acquittement de quorum de la
	// dernière séquence publiée ; le fdatasync local n'est qu'une optimisation
	// de vitesse de reprise. Sans sink, DB.Sync se replie sur la barrière locale.
	CommitReplicated
)

// CommitPolicy fixe le contrat de durabilité locale de la base.
//
// Pour CommitImmediate et CommitWindowed, la garantie est LOCALE et sa fenêtre
// de perte est BORNÉE : une écriture non pointée devient rejouable après
// coupure dès que la fenêtre Window s'est écoulée depuis la première écriture
// non pointée du groupe, ou dès que walCoalesceN écritures ont été groupées,
// selon ce qui survient en premier. La fenêtre effective vaut Window pour
// CommitWindowed et defaultCommitWindow (2 ms) quand Window est nulle ; elle
// borne le délai d'ajout, pas le temps de fdatasync lui-même.
//
// Pour CommitReplicated, la garantie de durabilité appartient à la couche de
// réplication/quorum : le fsync local n'est qu'une optimisation de vitesse de
// reprise et le committer ne pointe plus selon la fenêtre. DB.Sync demeure la
// barrière explicite ; dès qu'un ReplicationSink est branché, cette barrière
// attend l'acquittement d'un quorum de réplicas et ne déclare l'écriture
// durable qu'à cette condition.
type CommitPolicy struct {
	Mode   CommitMode
	Window time.Duration
}

// WriteOption porte une intention de durabilité attachée à un appel d'écriture
// précis. Une écriture sans option retombe sur la politique d'ouverture de la
// base, si bien que l'ajout d'options ne rompt aucun appelant existant.
type WriteOption func(*writeOptions)

type writeOptions struct {
	policy CommitPolicy
	set    bool
}

// WithDurability attache à l'appel d'écriture la politique de durabilité p.
// Au niveau d'un Put, CommitImmediate force le pointage du shard avant le
// retour ; CommitWindowed(d) enregistre l'écriture auprès du committer central
// avec la fenêtre d, et non celle de l'ouverture ; CommitReplicated attend
// l'acquittement de quorum du ReplicationSink branché avant de rendre. Sans
// option, la politique d'ouverture s'applique inchangée.
func WithDurability(p CommitPolicy) WriteOption {
	return func(o *writeOptions) {
		o.policy = p
		o.set = true
	}
}

// resolveWriteOptions agrège les options d'un appel et borne la politique
// retenue. Le drapeau rendu est faux en l'absence d'intention explicite.
func resolveWriteOptions(opts []WriteOption) (CommitPolicy, bool) {
	var o writeOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if !o.set {
		return CommitPolicy{}, false
	}
	return normalizeCommitPolicy(o.policy), true
}

// commitCoordinator est le committer central unique d'une base. Une seule
// goroutine suit l'ensemble des shards ayant des écritures non pointées, et
// non une goroutine par shard (NumShards = 1024). Le pointage d'un shard prend
// son verrou écrivain, ce qui le sérialise avec les écritures en cours.
type commitCoordinator struct {
	window  time.Duration
	mu      sync.Mutex
	pending map[*Shard]time.Time
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newCommitCoordinator(window time.Duration) *commitCoordinator {
	if window <= 0 {
		window = defaultCommitWindow
	}
	c := &commitCoordinator{
		window:  window,
		pending: make(map[*Shard]time.Time),
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go c.run()
	return c
}

func (c *commitCoordinator) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// mark enregistre un shard non pointé avec la fenêtre par défaut du committer.
// L'échéance de la fenêtre court depuis la première écriture non pointée : une
// inscription sur un shard déjà en attente ne réarme pas la fenêtre au-delà de
// l'échéance acquise.
func (c *commitCoordinator) mark(s *Shard) {
	c.markWindow(s, c.window)
}

// markWindow enregistre un shard non pointé avec la fenêtre demandée par
// l'appel d'écriture. Une échéance plus rapprochée resserre la borne ; une
// échéance plus lointaine ne relâche jamais une échéance déjà acquise.
func (c *commitCoordinator) markWindow(s *Shard, window time.Duration) {
	if window <= 0 {
		window = c.window
	}
	deadline := time.Now().Add(window)
	c.mu.Lock()
	if prev, ok := c.pending[s]; !ok || deadline.Before(prev) {
		c.pending[s] = deadline
	}
	c.mu.Unlock()
	c.signal()
}

// unmark retire un shard de la file d'attente du committer après un pointage
// explicite par appel.
func (c *commitCoordinator) unmark(s *Shard) {
	c.mu.Lock()
	delete(c.pending, s)
	c.mu.Unlock()
}

// nextDeadline retourne le délai jusqu'à la plus proche échéance en attente.
func (c *commitCoordinator) nextDeadline() (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var earliest time.Time
	for _, dl := range c.pending {
		if earliest.IsZero() || dl.Before(earliest) {
			earliest = dl
		}
	}
	if earliest.IsZero() {
		return 0, false
	}
	wait := time.Until(earliest)
	if wait < 0 {
		wait = 0
	}
	return wait, true
}

func (c *commitCoordinator) run() {
	defer close(c.done)
	for {
		select {
		case <-c.stop:
			return
		case <-c.wake:
		}
		for {
			wait, ok := c.nextDeadline()
			if !ok {
				break
			}
			timer := time.NewTimer(wait)
			select {
			case <-c.stop:
				timer.Stop()
				return
			case <-timer.C:
			case <-c.wake:
				timer.Stop()
				continue
			}
			c.flushDue()
		}
	}
}

// flushDue pointe les shards dont l'échéance est atteinte. Un pointage en échec
// laisse le shard en attente avec une nouvelle échéance ; le verrou du journal
// sérialise le committer avec les écrivains sans voler leur verrou logique.
func (c *commitCoordinator) flushDue() {
	now := time.Now()
	c.mu.Lock()
	due := make([]*Shard, 0, len(c.pending))
	for s, dl := range c.pending {
		if !dl.After(now) {
			due = append(due, s)
		}
	}
	c.mu.Unlock()
	for _, s := range due {
		s.walMu.Lock()
		if s.wal == nil {
			c.mu.Lock()
			delete(c.pending, s)
			c.mu.Unlock()
			s.walMu.Unlock()
			continue
		}
		if s.unflushed > 0 {
			if err := s.flushWAL(); err != nil {
				c.mu.Lock()
				c.pending[s] = time.Now().Add(c.window)
				c.mu.Unlock()
				s.walMu.Unlock()
				continue
			}
		}
		c.mu.Lock()
		delete(c.pending, s)
		c.mu.Unlock()
		s.walMu.Unlock()
	}
}

// stopWait arrête la goroutine du committer et attend sa terminaison. Les
// écritures déjà non pointées sont pointées par la fermeture des shards.
func (c *commitCoordinator) stopWait() {
	if c == nil {
		return
	}
	c.once.Do(func() { close(c.stop) })
	<-c.done
}

type DB struct {
	mu           sync.RWMutex
	dir          string
	key          [32]byte
	shards       map[uint16]*Shard
	lotSeq       uint64
	lotGroup     bool
	busyTimeout  time.Duration
	activeShards [activeWords]uint64
	replHook     func(shard uint16, recType byte, key, val []byte)
	replSink     ReplicationSink
	eventSink    EventSink
	commitPolicy CommitPolicy
	shardOpts    []Option
	committer    *commitCoordinator
}

func Route(key []byte) uint16 {
	if bytes.HasPrefix(key, []byte("vfs:")) {
		if prefix := vfsRoutingPrefix(key); prefix != nil {
			sum := blake3archtsim.Sum256(prefix)
			return binary.BigEndian.Uint16(sum[:2]) >> 6
		}
	}
	sum := blake3archtsim.Sum256(key)
	return binary.BigEndian.Uint16(sum[:2]) >> 6
}

func vfsRoutingPrefix(key []byte) []byte {
	rest := key[4:] // après "vfs:"
	idx1 := bytes.IndexByte(rest, ':')
	if idx1 <= 0 {
		return nil
	}
	lt, ok := parseUintAscii(rest[:idx1])
	if !ok || lt < 0 {
		return nil
	}
	rest = rest[idx1+1:]
	if lt < 0 || lt >= len(rest) || rest[lt] != ':' {
		return nil
	}
	tenant := rest[:lt]
	rest = rest[lt+1:]

	idx2 := bytes.IndexByte(rest, ':')
	if idx2 <= 0 {
		return nil
	}
	lf, ok := parseUintAscii(rest[:idx2])
	if !ok || lf < 0 {
		return nil
	}
	rest = rest[idx2+1:]
	if lf < 0 || lf > len(rest) {
		return nil
	}
	baseFileID := rest[:lf]
	if bytes.HasSuffix(baseFileID, []byte("-journal")) {
		baseFileID = baseFileID[:len(baseFileID)-len("-journal")]
	} else if bytes.HasSuffix(baseFileID, []byte("-wal")) {
		baseFileID = baseFileID[:len(baseFileID)-len("-wal")]
	} else if bytes.HasSuffix(baseFileID, []byte("-shm")) {
		baseFileID = baseFileID[:len(baseFileID)-len("-shm")]
	}

	var buf [128]byte
	prefix := append(buf[:0], "vfs:"...)
	prefix = strconv.AppendInt(prefix, int64(len(tenant)), 10)
	prefix = append(prefix, ':')
	prefix = append(prefix, tenant...)
	prefix = append(prefix, ':')
	prefix = strconv.AppendInt(prefix, int64(len(baseFileID)), 10)
	prefix = append(prefix, ':')
	prefix = append(prefix, baseFileID...)
	return prefix
}

func parseUintAscii(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		if n > (math.MaxInt-9)/10 {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// OpenDB ouvre une base multi-shards. Les options acceptées sont celles
// d'OpenShard ; WithCommitPolicy y est lue pour créer le committer central
// unique et est propagée à chaque shard ouvert. La politique par défaut est
// CommitImmediate avec la fenêtre par défaut.
func OpenDB(dir string, key [32]byte, opts ...Option) (*DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	raiseFDLimit()
	var cfg shardConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	policy := normalizeCommitPolicy(cfg.commitPolicy)
	db := &DB{
		dir:          dir,
		key:          key,
		shards:       make(map[uint16]*Shard),
		commitPolicy: policy,
		shardOpts:    opts,
	}
	if policy.Mode != CommitReplicated {
		db.committer = newCommitCoordinator(policy.Window)
	}
	if err := db.scanActiveShards(); err != nil {
		db.committer.stopWait()
		return nil, err
	}
	return db, nil
}

// normalizeCommitPolicy borne la politique : mode inconnu ramené à
// CommitImmediate, fenêtre nulle des régimes locaux ramenée à la borne par
// défaut.
func normalizeCommitPolicy(p CommitPolicy) CommitPolicy {
	switch p.Mode {
	case CommitImmediate, CommitWindowed:
		if p.Window <= 0 {
			p.Window = defaultCommitWindow
		}
	case CommitReplicated:
		p.Window = 0
	default:
		p.Mode = CommitImmediate
		if p.Window <= 0 {
			p.Window = defaultCommitWindow
		}
	}
	return p
}

func raiseFDLimit() {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &rl); err != nil {
		return
	}
	if rl.Cur >= fdLimitTarget {
		return
	}
	want := uint64(fdLimitTarget)
	if rl.Max != unix.RLIM_INFINITY && want > rl.Max {
		want = rl.Max
	}
	if want <= rl.Cur {
		return
	}
	rl.Cur = want
	_ = unix.Setrlimit(unix.RLIMIT_NOFILE, &rl)
}

func (db *DB) scanActiveShards() error {
	entries, err := os.ReadDir(db.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || len(e.Name()) != 4 {
			continue
		}
		id, err := strconv.ParseUint(e.Name(), 16, 16)
		if err != nil || id >= NumShards {
			continue
		}
		if _, err := os.Stat(filepath.Join(db.dir, e.Name(), "data.img")); err != nil {
			continue
		}
		db.activeShards[id/64] |= 1 << (id % 64)
	}
	return nil
}

func (db *DB) GetShard(id uint16) (*Shard, error) {
	if db == nil {
		return nil, unix.EBADF
	}
	if id >= NumShards {
		return nil, fmt.Errorf("c2db: shard %d >= %d", id, NumShards)
	}

	db.mu.RLock()
	if db.shards == nil {
		db.mu.RUnlock()
		return nil, unix.EBADF
	}
	if s, ok := db.shards[id]; ok {
		lotGroup := db.lotGroup
		db.mu.RUnlock()
		if lotGroup && s.groupDepth == 0 {
			if err := s.lockWriter(); err != nil {
				return nil, err
			}
			s.enterGroup()
		}
		return s, nil
	}
	db.mu.RUnlock()

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.shards == nil {
		return nil, unix.EBADF
	}
	if s, ok := db.shards[id]; ok {
		if db.lotGroup && s.groupDepth == 0 {
			if err := s.lockWriter(); err != nil {
				return nil, err
			}
			s.enterGroup()
		}
		return s, nil
	}

	subdir := shardDir(db.dir, id)
	if err := os.MkdirAll(subdir, 0o700); err != nil {
		return nil, err
	}
	openOpts := db.shardOpts
	if db.committer != nil {
		openOpts = append(append([]Option(nil), db.shardOpts...), withExternalCommitter(db.committer))
	}
	s, err := OpenShard(subdir, db.key, id, openOpts...)
	if err != nil {
		return nil, err
	}
	s.SetBusyTimeout(db.busyTimeout)
	if db.replHook != nil {
		s.SetReplicationHook(db.replHook)
	}
	if db.replSink != nil {
		s.SetReplicationSink(db.replSink)
	}
	if db.eventSink != nil {
		s.SetEventSink(db.eventSink)
	}
	db.shards[id] = s
	db.activeShards[id/64] |= 1 << (id % 64)
	if db.lotGroup {
		if err := s.lockWriter(); err != nil {
			return nil, err
		}
		s.enterGroup()
	}
	return s, nil
}

func (db *DB) SetBusyTimeout(d time.Duration) {
	if db == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.busyTimeout = d
	for _, s := range db.shards {
		s.SetBusyTimeout(d)
	}
}

// ensureCommitter crée paresseusement le committer central d'une base ouverte
// sous CommitReplicated, où aucune goroutine de fenêtre n'est démarrée. Il
// n'est sollicité que par une écriture demandant explicitement une fenêtre par
// appel, ce qui préserve la discipline d'ouverture : sans une telle demande,
// aucun committer n'est créé sous CommitReplicated. Le committer est rattaché
// aux shards déjà ouverts et aux shards futurs, sous le verrou du journal de
// chacun, pour rester sûr vis-à-vis des écrivains concurrents.
func (db *DB) ensureCommitter() *commitCoordinator {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.shards == nil {
		return nil
	}
	if db.committer == nil {
		db.committer = newCommitCoordinator(defaultCommitWindow)
	}
	for _, s := range db.shards {
		s.walMu.Lock()
		s.committer = db.committer
		s.walMu.Unlock()
	}
	return db.committer
}

// SetReplicationHook configure le crochet de réplication pour tous les shards de la base.
func (db *DB) SetReplicationHook(hook func(shard uint16, recType byte, key, val []byte)) {
	if db == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.replHook = hook
	for _, s := range db.shards {
		s.SetReplicationHook(hook)
	}
}

// SetEventSink branche un puits d'événements sur tous les shards, présents et
// futurs, de la base. Le puits reste la propriété de l'appelant. Un puits nul
// désactive le flux.
func (db *DB) SetEventSink(sink EventSink) {
	if db == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.eventSink = sink
	for _, s := range db.shards {
		s.SetEventSink(sink)
	}
}

func (db *DB) Put(key, val []byte, opts ...WriteOption) error {
	if db == nil {
		return unix.EBADF
	}
	if policy, set := resolveWriteOptions(opts); set && policy.Mode == CommitWindowed {
		db.ensureCommitter()
	}
	s, err := db.GetShard(Route(key))
	if err != nil {
		return err
	}
	return s.Put(key, val, opts...)
}

// ErrCrossShardAtomicUnsupported est renvoyé par PutBatchAtomic quand le lot
// traverse plusieurs shards. L'atomicité inter-shards n'est pas garantie sans
// changer le schéma (inter_shard_tx: false).
var ErrCrossShardAtomicUnsupported = errors.New("c2db: putbatchatomic: lot traverse plusieurs shards, atomicité inter-shards non garantie")

// PutBatch ventile en parallèle sur les shards et committe chaque shard
// indépendamment. Ce chemin est scatter-gather non atomique : si un shard
// échoue, les shards déjà committés ne sont pas annulés. L'appelant qui
// exige l'atomicité doit utiliser PutBatchAtomic.
//
// L'option de durabilité porte sur le lot entier. Le chemin de lot pointe
// localement par construction (un seul pack WAL fermé par EndPack, puis
// vidage du pager) : CommitImmediate y est donc déjà satisfait, et
// CommitWindowed(d) l'est a fortiori, puisque le lot est durable avant
// l'échéance. CommitReplicated ajoute l'attente d'acquittement de quorum,
// seule condition qui déclare le lot durable sous ce régime.
func (db *DB) PutBatch(pairs [][2][]byte, opts ...WriteOption) error {
	if db == nil {
		return unix.EBADF
	}
	if len(pairs) == 0 {
		return nil
	}
	if policy, set := resolveWriteOptions(opts); set && policy.Mode == CommitWindowed {
		db.ensureCommitter()
	}

	firstID := Route(pairs[0][0])
	single := true
	for i := 1; i < len(pairs); i++ {
		if Route(pairs[i][0]) != firstID {
			single = false
			break
		}
	}
	if single {
		s, err := db.GetShard(firstID)
		if err != nil {
			return err
		}
		return s.PutBatch(pairs, opts...)
	}

	buckets := make(map[uint16][][2][]byte)
	for i := range pairs {
		id := Route(pairs[i][0])
		buckets[id] = append(buckets[id], pairs[i])
	}

	type shardJob struct {
		s     *Shard
		pairs [][2][]byte
	}
	jobs := make([]shardJob, 0, len(buckets))
	for id, shardPairs := range buckets {
		s, err := db.GetShard(id)
		if err != nil {
			return err
		}
		jobs = append(jobs, shardJob{s: s, pairs: shardPairs})
	}

	var (
		wg      sync.WaitGroup
		errOnce sync.Once
		retErr  error
	)
	wg.Add(len(jobs))
	for i := range jobs {
		job := jobs[i]
		go func() {
			defer wg.Done()
			if err := job.s.PutBatch(job.pairs, opts...); err != nil {
				errOnce.Do(func() { retErr = err })
			}
		}()
	}
	wg.Wait()
	return retErr
}

// PutBatchAtomic committe le lot de façon atomique par construction mono-WAL.
// Si toutes les clés vont vers le même shard, le lot est délégué à ce shard
// et committe de façon atomique. Si les clés traversent plusieurs shards,
// le lot est refusé avant toute écriture.
func (db *DB) PutBatchAtomic(pairs [][2][]byte) error {
	if db == nil {
		return unix.EBADF
	}
	if len(pairs) == 0 {
		return nil
	}

	firstID := Route(pairs[0][0])
	for i := 1; i < len(pairs); i++ {
		if Route(pairs[i][0]) != firstID {
			return ErrCrossShardAtomicUnsupported
		}
	}

	s, err := db.GetShard(firstID)
	if err != nil {
		return err
	}
	return s.PutBatch(pairs)
}

func (db *DB) Get(key []byte) ([]byte, error) {
	s, err := db.GetShard(Route(key))
	if err != nil {
		return nil, err
	}
	return s.Get(key)
}

func (db *DB) GetAsOf(key []byte, snap [16]byte) ([]byte, error) {
	s, err := db.GetShard(Route(key))
	if err != nil {
		return nil, err
	}
	return s.GetAsOf(key, snap)
}

func (db *DB) Delete(key []byte) error {
	s, err := db.GetShard(Route(key))
	if err != nil {
		return err
	}
	return s.Delete(key)
}

func (db *DB) CreateCollection(name string) error {
	s, err := db.GetShard(Route([]byte(name)))
	if err != nil {
		return err
	}
	return s.CreateCollection(name)
}

func (db *DB) Insert(name string, doc []byte) (c2uuidv7.UUID, error) {
	var zero c2uuidv7.UUID
	s, err := db.GetShard(Route([]byte(name)))
	if err != nil {
		return zero, err
	}
	return s.Insert(name, doc)
}

func (db *DB) GetDoc(name string, id c2uuidv7.UUID) ([]byte, error) {
	s, err := db.GetShard(ShardOf(id))
	if err != nil {
		return nil, err
	}
	return s.GetDoc(name, id)
}

func (db *DB) GetDocAsOf(name string, id c2uuidv7.UUID, snap c2uuidv7.UUID) ([]byte, error) {
	s, err := db.GetShard(ShardOf(id))
	if err != nil {
		return nil, err
	}
	return s.GetDocAsOf(name, id, snap)
}

func (db *DB) DeleteDoc(name string, id c2uuidv7.UUID) error {
	s, err := db.GetShard(ShardOf(id))
	if err != nil {
		return err
	}
	return s.DeleteDoc(name, id)
}

func (db *DB) ScanPrefix(prefix []byte, limit uint64) ([][]byte, error) {
	if db == nil {
		return nil, unix.EBADF
	}
	if limit == 0 {
		return nil, errQLLimitAbsente
	}

	db.mu.RLock()
	if db.shards == nil {
		db.mu.RUnlock()
		return nil, unix.EBADF
	}
	active := db.activeShards
	db.mu.RUnlock()

	var acc [][]byte
	for word, mask := range active {
		if mask == 0 {
			continue
		}
		for mask != 0 {
			id := uint16(word*64 + bits.TrailingZeros64(mask))
			mask &= mask - 1
			s, err := db.GetShard(id)
			if err != nil {
				return nil, err
			}
			keys, err := s.ScanPrefix(prefix)
			if err != nil {
				return nil, err
			}
			acc = append(acc, keys...)
		}
	}
	sort.Slice(acc, func(i, j int) bool { return bytes.Compare(acc[i], acc[j]) < 0 })
	if uint64(len(acc)) > limit {
		acc = acc[:limit]
	}
	return acc, nil
}

func (db *DB) SyncShard(id uint16) error {
	if db == nil {
		return unix.EBADF
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.shards == nil {
		return unix.EBADF
	}
	if s, ok := db.shards[id]; ok {
		return s.Checkpoint()
	}
	return nil
}

func (db *DB) SyncKey(key []byte) error {
	if db == nil {
		return unix.EBADF
	}
	return db.SyncShard(Route(key))
}

// Sync est la barrière de durabilité explicite de la base. Sous CommitImmediate
// et CommitWindowed, elle force le pointage de tous les shards ayant des
// écritures non pointées et attend leur fdatasync ; le verrou écrivain de
// chaque shard sérialise la barrière avec les écritures en cours, si bien
// qu'au retour toute écriture acquittée avant l'appel est rejouable.
//
// Sous CommitReplicated avec un ReplicationSink branché, la durabilité est
// acquittée par la réplication : Sync attend que le quorum ait acquitté la
// dernière séquence publiée par chaque shard, et ne déclare l'écriture durable
// qu'à cette condition. Le pointage local n'est alors qu'une optimisation de
// vitesse de reprise. Sans sink branché, Sync se replie sur la barrière locale.
// Un contexte annulé interrompt la barrière entre deux shards ou pendant
// l'attente d'acquittement.
func (db *DB) Sync(ctx context.Context) error {
	if db == nil {
		return unix.EBADF
	}
	if ctx == nil {
		ctx = context.Background()
	}
	db.mu.RLock()
	if db.shards == nil {
		db.mu.RUnlock()
		return unix.EBADF
	}
	shards := make([]*Shard, 0, len(db.shards))
	for _, s := range db.shards {
		shards = append(shards, s)
	}
	replicated := db.commitPolicy.Mode == CommitReplicated && db.replSink != nil
	sink := db.replSink
	db.mu.RUnlock()
	for _, s := range shards {
		if err := ctx.Err(); err != nil {
			return err
		}
		if replicated {
			if err := sink.WaitAck(ctx, s.LastReplSeq()); err != nil {
				return err
			}
		}
		if err := s.syncBarrier(); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Checkpoint() error {
	if db == nil {
		return unix.EBADF
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.shards == nil {
		return unix.EBADF
	}
	for _, s := range db.shards {
		if err := s.Checkpoint(); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Compact() error {
	if db == nil {
		return unix.EBADF
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.shards == nil {
		return unix.EBADF
	}
	for _, s := range db.shards {
		if err := s.Compact(); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Close() error {
	if db == nil {
		return unix.EBADF
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.shards == nil {
		return unix.EBADF
	}
	if db.committer != nil {
		db.committer.stopWait()
		db.committer = nil
	}
	var err error
	for id, s := range db.shards {
		if e := s.Close(); err == nil {
			err = e
		}
		delete(db.shards, id)
	}
	db.shards = nil
	return err
}

func shardDir(root string, id uint16) string {
	return filepath.Join(root, fmt.Sprintf("%04x", id))
}
