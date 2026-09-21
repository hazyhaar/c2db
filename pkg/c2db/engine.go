// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
	"github.com/hazyhaar/c2db/pkg/cihook55"
	"golang.org/x/sys/unix"
)

const (
	pageN             = uint64(16384)
	defaultHeapPages  = uint64(4096)
	defaultShardBytes = 4096 * 16384
	heapPages         = defaultHeapPages // Rétrocompatibilité packages externes
	shardBytes        = defaultShardBytes
	walBytes          = 16 * 1024 * 1024
	heapRootOff       = 32
	heapUsedOff       = 40
	heapGenOff        = 48
	maxShardFile      = uint64(1) << 30
)

const (
	maxBTreeDepth = 4
	splitReserve  = 2*maxBTreeDepth + 1      // 9 pages de réserve
	heapWatermark = heapPages - splitReserve // 4096 - 9 = 4087 pages
)

// Racine et pages utilisées du tas : uint64 LE aux offsets 32 et 40 de l'image
// (en-tête page 0, après CRC@28, avant BODY@64). used >= 1, root < heapPages.

type Shard struct {
	data        *Device
	pager       *Pager
	wal         *WAL
	pub         []byte
	dirty       []byte
	heapRoot    uint64
	heapUsed    uint64
	heapPubUsed uint64
	groupDepth  int
	id          uint16
	counter     uint64
	walFlushes  uint64
	dataFlushes uint64
	// walCheckpoints et walCheckpointNanos mesurent la cadence et le coût
	// cumulé des pointages de journal déclenchés par le franchissement du
	// seuil de recyclage. Ils rendent la cadence observable sans modifier le
	// chemin chaud.
	walCheckpoints     uint64
	walCheckpointNanos uint64
	walCheckpointMaxNs uint64
	lastID             [16]byte
	hasLast            bool
	writeMu            sync.Mutex
	writeOwner         atomic.Uint64
	writeDepth         int
	// walMu sérialise l'accès au journal (append, pointage). Les écrivains le
	// prennent sous writeMu ; le committer central le prend seul pour pointer
	// sans jamais voler le verrou écrivain logique, ce qui évite de faire
	// échouer les écrivains à busyTimeout nul pendant un fdatasync.
	walMu       sync.Mutex
	busyTimeout time.Duration
	live        atomic.Pointer[liveHeap]
	holdHeaps   []*liveHeap
	spare       []byte
	heapGen     uint64
	unflushed   int
	// commitMode porte la politique de durabilité locale du shard et
	// committer, quand il est non nul, l'enregistre auprès du committer
	// central de la base dirigée par OpenDB. Un shard isolé ouvert par
	// OpenShard n'a pas de committer : syncWAL y pointe immédiatement, sauf
	// sous CommitReplicated où le pointage est laissé à Sync.
	commitMode CommitMode
	committer  *commitCoordinator
	// commitWindow borne la fenêtre de perte d'un shard isolé (sans committer
	// central) ; firstUnflushed date la première écriture non pointée.
	commitWindow   time.Duration
	firstUnflushed time.Time
	// writeDurPolicy et writeDurSet portent l'intention de durabilité de
	// l'appel d'écriture en cours. Ils ne sont valides que tant que le verrou
	// écrivain est tenu, ce qui les sérialise entre écrivains. writeDurSet à
	// faux retombe sur la politique d'ouverture.
	writeDurPolicy CommitPolicy
	writeDurSet    bool
	pagesSkipped   uint64
	strictOpen     bool
	readOnly       bool
	lockFd         int
	heapPages      uint64
	shardBytes     uint64
	heapWatermark  uint64
	dirtyMask      []uint64
	// Ensemble différentiel des pages divergentes entre dirty et pub.
	// dirtyMask déduplique (un bit par page), dirtyList ordonne la
	// publication en O(D) pages touchées sans balayer le tas, dirtyOverflow
	// bascule sur le balayage des mots du bitmap quand la liste sature.
	// dirtyList est préallouée à l'ouverture : zéro allocation au commit.
	dirtyList             []uint32
	dirtyListLen          int
	dirtyOverflow         bool
	poisonErr             atomic.Pointer[error]
	publishFaultInjection atomic.Bool

	// Observabilité du rejeu pour qualification formelle
	ReplayLastMutIdx int
	ReplayLastMutKey string
	ReplayLastMutErr error
	ReplayValStep    int
	ReplayValErr     error
	// ReplaySyncPagesCopied comptabilise les pages effectivement recopiées de
	// s.dirty vers s.pub par syncPubFromDirty pendant le rejeu. Il mesure le coût
	// du rejeu indépendamment du temps mural et sert d'oracle au banc de
	// non-régression : il doit croître avec les seules pages divergentes, jamais
	// avec la taille du tas.
	ReplaySyncPagesCopied uint64
	// replayMask accumule l'union des pages divergentes réellement copiées
	// pendant le rejeu. syncPubFromDirty remet à zéro l'ensemble différentiel par
	// enregistrement (dirtyMask/dirtyList), et cette union est reportée vers
	// dirtyMask juste avant la publication terminale, afin que publish()
	// transfère au pager exactement les pages reconstruites.
	replayMask []uint64

	// btDelCowPages cumule, à titre d'observabilité de qualification, le nombre
	// de pages copiées sur écriture par les suppressions B-tree appliquées. Sur
	// un arbre à plusieurs feuilles, la descente doit s'arrêter à la feuille
	// cible : le cumul croît linéairement avec le nombre de suppressions, non
	// avec le produit suppressions x feuilles. La mesure porte sur le masque
	// réellement retourné par le noyau, sans donnée de complaisance.
	btDelCowPages atomic.Uint64
	// replayDiscarded marque qu'un groupe transactionnel a été écarté faute
	// d'intégrité (validation ou application). Le rejeu reste « réussi » au sens
	// du retour nil en mode non strict, mais l'abandon ne doit pas être rendu
	// définitif par un pointage : une ouverture stricte ultérieure doit encore
	// pouvoir constater l'infraction.
	replayDiscarded bool

	hookMu   sync.RWMutex
	replHook func(shard uint16, recType byte, key, val []byte)
	// replSink, quand il est branché, porte la durabilité sous CommitReplicated :
	// chaque enregistrement WAL est publié au dispositif de réplication, qui
	// retourne sa séquence, et Sync attend l'acquittement de quorum de cette
	// séquence. Un sink branché prime sur le replHook de simple diffusion.
	replSink    ReplicationSink
	lastReplSeq atomic.Uint64

	contentionMu     sync.RWMutex
	onLockContention func()
	autoCompacting   bool
	retention        Retention
	// rebuildBudget plafonne le volume matérialisé par
	// rebuildAllVersionsLocked avant de céder la place au rebâtissage ancré
	// (aplatissement de l'historique). Nul applique defaultRebuildBudget.
	rebuildBudget uint64

	// eventMu protège le pointeur de puits d'événements. Le puits est
	// optionnel : tant qu'il est nul, aucune allocation ni écriture n'est
	// produite par les chemins d'écriture et de compaction.
	eventMu   sync.RWMutex
	eventSink EventSink
	// txlog est le journal détenu par le shard quand WithTxLog est fourni ;
	// il est fermé par Close. Un puits posé par SetEventSink reste la
	// propriété de l'appelant.
	txlog *TxLog
	// history est le journal de versions déportées, ouvert uniquement sous
	// RetentionArchiveSuperseded ; il est fermé par Close.
	history *History
}

const (
	walCoalesceN = 32
	maxHoldHeaps = 8 // Plafond strict des tampons anonymes MVCC conservés par shard (8 vues simultanées max)
)

type liveHeap struct {
	buf     []byte
	root    uint64
	snap    [16]byte
	hasSnap bool
	refs    atomic.Int32
}

type View struct {
	snap [16]byte
	heap []byte
	root uint64
	pin  *liveHeap
}

var (
	ErrReadOnly         = errors.New("c2db: shard ouvert en lecture seule")
	ErrRecoveryRequired = errors.New("c2db: reprise WAL requise sur shard en lecture seule")
	ErrWriterBusy       = errors.New("c2db: writer busy")
	// ErrHeapFull signale l'épuisement des pages disponibles dans le tas B-Tree.
	ErrHeapFull = errors.New("c2db: heap full")
	// ErrTreeFull signale que le noyau B-Tree n'a pas pu scinder un nœud
	// interne faute de page libre pour le frère ou la nouvelle racine. Il se
	// distingue d'errInsert (insertion refusée pour cause de validation ou de
	// format) et d'ErrHeapFull (tas au filigrane) : l'arbre a atteint sa
	// capacité structurelle et aucune compaction de feuille ne le débloque.
	ErrTreeFull              = errors.New("c2db: tree full")
	errInsert                = errors.New("c2db: insert rejected")
	ErrViewHeld              = errors.New("c2db: view held")
	errPageSeal              = errors.New("c2db: page seal")
	errOFD                   = errors.New("c2db: ofd lock")
	ErrNotFound              = errors.New("c2db: key not found")
	errNotFound              = ErrNotFound
	errPayload               = errors.New("c2db: wal payload truncated")
	ErrPayloadTooLarge       = errors.New("c2db: key or value exceeds maximum size")
	ErrShardCapacityMismatch = errors.New("c2db: shard capacity is fixed at creation; reopen with the same heap pages")
	ErrInvalidHeapPages      = errors.New("c2db: invalid heap pages: must be between 4096 and 67108864 (1 To) and multiple of 64")
)

const (
	minHeapPages uint64 = 4096     // 64 Mo (défaut minimal)
	maxHeapPages uint64 = 67108864 // 1 To (67 108 864 pages de 16 Ko)
)

// defaultRebuildBudget plafonne le volume matérialisé par
// rebuildAllVersionsLocked quand le champ rebuildBudget du shard est nul.
const defaultRebuildBudget = 256 << 20

type Option func(*shardConfig)

// Retention gouverne le sort de l'historique MVCC lors d'une compaction.
type Retention uint8

const (
	// RetentionKeepAll préserve toutes les versions : la compaction réinsère
	// l'historique complet et échoue ferme (ErrHeapFull) si le tas ne peut
	// l'accueillir, plutôt que de l'aplatir silencieusement. Régime explicite,
	// obtenu par WithHistoryRetention(RetentionKeepAll).
	RetentionKeepAll Retention = iota
	// RetentionPruneSuperseded ne conserve que la dernière version active par
	// clé ; la compaction aplatit l'historique pour libérer de la place. C'est
	// le défaut, conforme à l'historique du moteur ; l'élagage est désormais
	// tracé par un événement Prune.
	RetentionPruneSuperseded
	// RetentionArchiveSuperseded déporte vers le segment d'historique du shard
	// les versions supplantées AVANT d'aplatir le tas chaud. La place chaude est
	// libérée sans perte de version : les lectures as-of retombent sur le froid.
	RetentionArchiveSuperseded
)

type shardConfig struct {
	heapPages     uint64
	heapPagesSet  bool
	walBytes      uint64
	walBytesSet   bool
	strict        bool
	readOnly      bool
	retention     Retention
	txlogDir      string
	historyDir    string
	rebuildBudget uint64
	commitPolicy  CommitPolicy
	// externalCommitter branche le shard sur le committer central de la base.
	// Option interne : un OpenShard isolé reste sans committer et pointe par
	// syncWAL, ce qui évite une goroutine par shard.
	externalCommitter *commitCoordinator
}

// WithCommitPolicy fixe la politique de durabilité locale. Au niveau OpenDB,
// elle gouverne le committer central unique de la base et la fenêtre de perte
// bornée. Propagée à OpenShard, elle fixe le régime du shard isolé : sous
// CommitImmediate et CommitWindowed, syncWAL pointe immédiatement (fenêtre
// nulle, borne la plus stricte) ; sous CommitReplicated, le pointage local est
// laissé à DB.Sync.
func WithCommitPolicy(p CommitPolicy) Option {
	return func(c *shardConfig) {
		c.commitPolicy = p
	}
}

// withExternalCommitter rattache un shard au committer central d'une base. Il
// n'est pas exporté : un appelant d'OpenShard ne doit pas fabriquer un
// committer par shard.
func withExternalCommitter(c *commitCoordinator) Option {
	return func(cfg *shardConfig) {
		cfg.externalCommitter = c
	}
}

// WithHistoryRetention fixe le contrat de rétention d'historique du shard.
// En l'absence de cette option, le contrat est RetentionPruneSuperseded.
func WithHistoryRetention(r Retention) Option {
	return func(c *shardConfig) {
		c.retention = r
	}
}

// WithTxLog active, pour ce shard, l'émission des événements de transaction
// (mutations, compactions, élagages, refus) vers un journal append-only ouvert
// dans dir. Le journal est détenu et fermé par le shard. En l'absence de cette
// option et de SetEventSink, les chemins d'écriture ne paient aucun surcoût.
func WithTxLog(dir string) Option {
	return func(c *shardConfig) {
		c.txlogDir = dir
	}
}

// WithHistoryLog fixe le répertoire des segments d'historique recevant les
// versions déportées sous RetentionArchiveSuperseded. En l'absence de cette
// option, le répertoire par défaut est <dir du shard>/history. Le journal
// d'historique est détenu et fermé par le shard.
func WithHistoryLog(dir string) Option {
	return func(c *shardConfig) {
		c.historyDir = dir
	}
}

// withRebuildBudget fixe le plafond de matérialisation de
// rebuildAllVersionsLocked avant bascule vers le rebâtissage ancré. Destiné à
// l'épreuve des chemins de rejeu sous contrainte, il n'est pas exporté.
func withRebuildBudget(n uint64) Option {
	return func(c *shardConfig) {
		c.rebuildBudget = n
	}
}

// WithHeapPages configure le nombre de pages du tas B-Tree (16 Ko par page).
// Valeurs usuelles : 4096 (64 Mo, défaut), 16384 (256 Mo), 65536 (1 Go), 16777216 (256 Go), 67108864 (1 To).
//
// Contrat de capacité : la taille du tas est fixée à la création du shard et
// matérialisée par la taille de data.img. Rouvrir un shard existant avec un
// autre nombre de pages est refusé par ErrShardCapacityMismatch ; rouvrir sans
// cette option adopte la capacité du fichier. Le tas ne croît jamais en ligne :
// une capacité insuffisante se résout par une nouvelle partition, jamais par
// une extension non bornée du même fichier.
func WithHeapPages(pages uint64) Option {
	return func(c *shardConfig) {
		c.heapPages = pages
		c.heapPagesSet = true
	}
}

// WithWALBytes configure la taille allouée au journal WAL (wal.img).
// Doit être un multiple positif de LBASize (4096 octets). Défaut : 16 Mo (4096 LBAs).
func WithWALBytes(bytes uint64) Option {
	return func(c *shardConfig) {
		c.walBytes = bytes
		c.walBytesSet = true
	}
}

// WithStrict active le mode strict d'intégrité lors de l'ouverture du shard.
func WithStrict(strict bool) Option {
	return func(c *shardConfig) {
		c.strict = strict
	}
}

// WithReadOnly ouvre une vue en mémoire sans création ni écriture de fichiers.
// Les journaux annexes inscriptibles (WithTxLog, RetentionArchiveSuperseded)
// sont incompatibles avec ce mode et provoquent ErrReadOnly.
func WithReadOnly(ro bool) Option {
	return func(c *shardConfig) {
		c.readOnly = ro
	}
}

func OpenTenantShard(dir string, master [32]byte, tenant uint16, epoch c2uuidv7.UUID, opts ...Option) (*Shard, error) {
	return OpenShard(dir, DeriveTenantMAC(master, tenant, epoch), tenant, opts...)
}

func OpenShard(dir string, key [32]byte, shard uint16, opts ...Option) (*Shard, error) {
	return openShardInternal(dir, key, shard, opts...)
}

func OpenShardStrict(dir string, key [32]byte, shard uint16, opts ...Option) (*Shard, error) {
	return openShardInternal(dir, key, shard, append([]Option{WithStrict(true)}, opts...)...)
}

func openShardInternal(dir string, key [32]byte, shard uint16, opts ...Option) (*Shard, error) {
	if shard >= maxShard {
		return nil, fmt.Errorf("c2db: shard %d >= %d", shard, maxShard)
	}

	cfg := shardConfig{
		heapPages: defaultHeapPages,
		strict:    false,
		retention: RetentionPruneSuperseded,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.readOnly && (cfg.txlogDir != "" || cfg.retention == RetentionArchiveSuperseded) {
		return nil, ErrReadOnly
	}

	if cfg.heapPages < minHeapPages || cfg.heapPages > maxHeapPages || cfg.heapPages%64 != 0 {
		return nil, ErrInvalidHeapPages
	}

	dataPath := filepath.Join(dir, "data.img")
	targetShardBytes := cfg.heapPages * pageN

	// Contrôle de cohérence si data.img existe déjà
	if fi, err := os.Stat(dataPath); err == nil && fi.Size() > 0 {
		existingPages := uint64(fi.Size()) / pageN
		if existingPages*pageN != uint64(fi.Size()) || existingPages < minHeapPages || existingPages > maxHeapPages {
			return nil, fmt.Errorf("c2db: existing data.img has invalid size %d", fi.Size())
		}
		if cfg.heapPagesSet && cfg.heapPages != existingPages {
			return nil, fmt.Errorf("%w (file=%d pages, requested=%d pages)", ErrShardCapacityMismatch, existingPages, cfg.heapPages)
		}
		cfg.heapPages = existingPages
		targetShardBytes = cfg.heapPages * pageN
	}

	openDevice := openOrCreateDevice
	if cfg.readOnly {
		openDevice = openReadOnlyDevice
	}
	data, err := openDevice(dataPath, targetShardBytes)
	if err != nil {
		return nil, err
	}
	walPath := filepath.Join(dir, "wal.img")
	targetWALBytes := cfg.walBytes
	if targetWALBytes == 0 {
		targetWALBytes = walBytes
	}
	if targetWALBytes%LBASize != 0 {
		_ = data.Close()
		return nil, fmt.Errorf("c2db: wal bytes %d not a multiple of %d", targetWALBytes, LBASize)
	}
	if fi, err := os.Stat(walPath); err == nil && fi.Size() > 0 {
		existingWAL := uint64(fi.Size())
		if existingWAL%LBASize != 0 || existingWAL < LBASize {
			_ = data.Close()
			return nil, fmt.Errorf("c2db: existing wal.img has invalid size %d", fi.Size())
		}
		if cfg.walBytesSet && cfg.walBytes != existingWAL {
			_ = data.Close()
			return nil, fmt.Errorf("c2db: existing wal.img capacity %d does not match requested %d", fi.Size(), cfg.walBytes)
		}
		targetWALBytes = existingWAL
	}
	var w *WAL
	if cfg.readOnly {
		var dev *Device
		dev, err = openDevice(walPath, targetWALBytes)
		if err == nil {
			w, err = newWAL(dev, key, walPath)
			if err != nil {
				_ = dev.Close()
			}
		}
	} else {
		w, err = openOrCreateWAL(walPath, targetWALBytes, key)
	}
	if err != nil {
		_ = data.Close()
		return nil, err
	}
	pager, err := NewPager(data)
	if err != nil {
		_ = w.KillWithoutFlush()
		_ = data.Close()
		return nil, err
	}
	tagLBAs := tagHeaderLBAs + (cfg.heapPages+tagsPerLBA-1)/tagsPerLBA
	tagsBytes := uint64(tagLBAs) * LBASize
	tags, err := openDevice(filepath.Join(dir, "tags.img"), tagsBytes)
	if err != nil {
		_ = pager.CloseWithoutFlush()
		_ = w.KillWithoutFlush()
		_ = data.Close()
		return nil, err
	}
	if err := pager.SetSeal(key, tags, shard); err != nil {
		_ = tags.Close()
		_ = pager.CloseWithoutFlush()
		_ = w.KillWithoutFlush()
		_ = data.Close()
		return nil, err
	}
	pub, err := mmapAnon(int(targetShardBytes))
	if err != nil {
		_ = pager.CloseWithoutFlush()
		_ = w.KillWithoutFlush()
		_ = data.Close()
		return nil, err
	}
	// Une vue en lecture seule ne réserve pas le tampon de travail dirty : les
	// lecteurs épinglent exclusivement pub via pinLive(). Le tampon n'est alloué
	// paresseusement que si le rejeu du journal doit effectivement muter le tas
	// (ensureDirty, appelé depuis replay), puis libéré avec le shard à la
	// fermeture. Le régime nominal — journal déjà pointé — n'alloue ni ne copie
	// rien.
	var dirty []byte
	if !cfg.readOnly {
		if dirty, err = mmapAnon(int(targetShardBytes)); err != nil {
			_ = unix.Munmap(pub)
			_ = pager.CloseWithoutFlush()
			_ = w.KillWithoutFlush()
			_ = data.Close()
			return nil, err
		}
	}
	lockPath := filepath.Join(dir, ".lock")
	lockFlags := unix.O_RDWR | unix.O_CREAT | unix.O_CLOEXEC
	if cfg.readOnly {
		lockFlags = unix.O_RDONLY | unix.O_CLOEXEC
	}
	lockFd, err := unix.Open(lockPath, lockFlags, 0o600)
	if err != nil {
		unmapHeap(dirty)
		_ = unix.Munmap(pub)
		_ = pager.CloseWithoutFlush()
		_ = w.KillWithoutFlush()
		_ = data.Close()
		return nil, err
	}

	dirtyWords := cfg.heapPages / 64
	dirtyCap := cfg.heapPages
	if dirtyCap > 65536 {
		dirtyCap = 65536
	}
	s := &Shard{
		data:          data,
		pager:         pager,
		wal:           w,
		pub:           pub,
		dirty:         dirty,
		id:            shard,
		heapPages:     cfg.heapPages,
		shardBytes:    targetShardBytes,
		heapWatermark: cfg.heapPages - splitReserve,
		dirtyMask:     make([]uint64, dirtyWords),
		dirtyList:     make([]uint32, dirtyCap),
		heapUsed:      1,
		strictOpen:    cfg.strict,
		readOnly:      cfg.readOnly,
		lockFd:        lockFd,
		retention:     cfg.retention,
		rebuildBudget: cfg.rebuildBudget,
		commitMode:    cfg.commitPolicy.Mode,
		commitWindow:  normalizeCommitPolicy(cfg.commitPolicy).Window,
	}
	// Le committer est central au niveau de la base : une seule goroutine pour
	// NumShards shards. Un shard isolé, ouvert sans base, n'en reçoit pas ; sa
	// fenêtre est bornée paresseusement au prochain syncWAL, ce qui préserve la
	// discipline synctest des harnais.
	s.committer = cfg.externalCommitter
	// W3a : le verrou écrivain couvre toute la durée de l'ouverture, de la
	// lecture du tas au rejeu et à son pointage. Les ouvertures concurrentes du
	// même shard, y compris dans le même processus, entrent en conflit sur le
	// verrou OFD associé à `.lock`. Le relâchement est différé : sur un chemin
	// d'erreur, s.Close() ferme lockFd avant que unlockOFD ne s'exécute, ce qui
	// rend son appel inoffensif, tandis que writeMu est rendu exactement une fois.
	if err := s.lockWriter(); err != nil {
		_ = s.Close()
		return nil, err
	}
	defer s.unlockWriter()
	// La pointe du journal est lue sous le verrou partagé, jamais en course
	// avec un écrivain qui recycle le journal.
	if s.readOnly {
		if err := s.wal.scanTip(); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	if err := s.loadHeap(); err != nil {
		_ = s.Close()
		return nil, err
	}
	if pub[20] == 0 && s.readOnly {
		s.heapRoot = 0
		s.heapUsed = 0
	} else if pub[20] == 0 {
		st := Db_bt_leaf_init(pub, pageN)
		if st.Ok == 0 {
			_ = s.Close()
			return nil, errInsert
		}
		s.heapRoot = 0
		s.heapUsed = 1
		s.stampHeap(pub)
		copy(s.dirty[:pageN], pub[:pageN])
		if err := s.pager.PutPage(0, pub[:pageN]); err != nil {
			_ = s.Close()
			return nil, err
		}
		if _, err := pager.FlushDirty(); err != nil {
			_ = s.Close()
			return nil, err
		}
	} else {
		s.heapRoot = binary.LittleEndian.Uint64(pub[heapRootOff : heapRootOff+8])
		s.heapUsed = binary.LittleEndian.Uint64(pub[heapUsedOff : heapUsedOff+8])
		if s.heapUsed < 1 {
			s.heapUsed = 1
		}
		if s.heapRoot >= s.heapPages {
			s.heapRoot = 0
		}
	}
	s.syncDirtyFromPub()
	s.heapPubUsed = s.heapUsed
	s.installLive(s.pub)
	// Le segment d'historique est ouvert AVANT le rejeu : sous
	// ArchiveSuperseded, le filet de rejeu doit pouvoir déporter les versions
	// supplantées quand la reconstruction matérialisée dépasse son budget,
	// au lieu d'échouer fermé. Le répertoire par défaut vit sous le shard, à
	// côté de data.img et wal.img.
	if cfg.retention == RetentionArchiveSuperseded {
		hdir := cfg.historyDir
		if hdir == "" {
			hdir = filepath.Join(dir, "history")
		}
		hl, hlErr := OpenHistory(hdir, 0)
		if hlErr != nil {
			_ = s.Close()
			return nil, hlErr
		}
		s.history = hl
	}
	if err := s.replay(); err != nil {
		_ = s.Close()
		return nil, err
	}
	// W3b : sceller l'état reconstruit AVANT tout pointage. Le rejeu ne publie
	// pas vers le pager ; un pointage seul, suivi d'un recyclage, perdrait les
	// pages reconstruites. publish() transfère les pages divergentes au pager,
	// checkpointLocked() les fdatasync puis pose le marqueur, et
	// TruncateCheckpoint() recycle le préfixe intégralement appliqué. Le seuil
	// next > 1 évite de re-pointer un journal déjà réduit à son marqueur ;
	// replayDiscarded interdit de rendre définitif l'abandon d'un groupe
	// transactionnel que le mode non strict a écarté sans le corriger.
	if !s.readOnly && s.wal != nil && s.wal.next > 1 && !s.replayDiscarded {
		if err := s.publish(); err != nil {
			_ = s.Close()
			return nil, err
		}
		if err := s.checkpointLocked(); err != nil {
			_ = s.Close()
			return nil, err
		}
		if err := s.wal.TruncateCheckpoint(); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	// Le journal d'événements est branché APRÈS le rejeu : les compactions
	// déclenchées par la reprise ne doivent pas polluer le flux, qui ne
	// consigne que les décisions prises en régime nominal.
	if cfg.txlogDir != "" {
		tl, tlErr := OpenTxLog(cfg.txlogDir, 0)
		if tlErr != nil {
			_ = s.Close()
			return nil, tlErr
		}
		s.txlog = tl
		s.eventSink = tl
	}
	// En lecture seule, dirty n'existe que si le rejeu l'a alloué, et pub porte
	// déjà l'état final : aucune recopie intégrale n'est nécessaire. En régime
	// écrivain, cette recopie réaligne le tampon froid de travail sur l'état
	// publié avant la première mutation.
	if !s.readOnly {
		s.syncDirtyFull()
	}
	return s, nil
}

func (s *Shard) SetBusyTimeout(d time.Duration) {
	if s == nil {
		return
	}
	s.busyTimeout = d
}

func goid() uint64 {
	var buf [32]byte
	n := runtime.Stack(buf[:], false)
	b := buf[:n]
	if i := bytes.IndexByte(b, ' '); i >= 0 {
		b = b[i+1:]
	}
	if j := bytes.IndexByte(b, ' '); j >= 0 {
		b = b[:j]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}

func (s *Shard) lockWriter() error {
	id := goid()
	if s.writeOwner.Load() == id {
		s.writeDepth++
		return nil
	}
	if err := s.tryWriteMu(); err != nil {
		return err
	}
	if err := s.lockOFD(); err != nil {
		s.writeMu.Unlock()
		return err
	}
	s.walMu.Lock()
	s.writeOwner.Store(id)
	s.writeDepth = 1
	return nil
}

func (s *Shard) notifyContention() {
	s.contentionMu.RLock()
	cb := s.onLockContention
	s.contentionMu.RUnlock()
	if cb != nil {
		cb()
	}
}

func (s *Shard) tryWriteMu() error {
	if s.busyTimeout <= 0 {
		if !s.writeMu.TryLock() {
			s.notifyContention()
			return ErrWriterBusy
		}
		return nil
	}
	deadline := time.Now().Add(s.busyTimeout)
	delay := 50 * time.Microsecond
	for {
		if s.writeMu.TryLock() {
			return nil
		}
		s.notifyContention()
		if !time.Now().Before(deadline) {
			return ErrWriterBusy
		}
		time.Sleep(delay)
		delay *= 2
		if delay > time.Millisecond {
			delay = time.Millisecond
		}
	}
}

func (s *Shard) unlockWriter() {
	if s.writeDepth > 1 {
		s.writeDepth--
		return
	}
	s.writeDepth = 0
	s.writeOwner.Store(0)
	s.unlockOFD()
	s.walMu.Unlock()
	s.writeMu.Unlock()
}

func (s *Shard) lockOFD() error {
	if s == nil || s.lockFd < 0 {
		return nil
	}
	fl := unix.Flock_t{Type: unix.F_WRLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
	if s.readOnly {
		fl.Type = unix.F_RDLCK
	}
	if s.busyTimeout <= 0 {
		if err := unix.FcntlFlock(uintptr(s.lockFd), unix.F_OFD_SETLK, &fl); err != nil {
			if err == unix.EAGAIN || err == unix.EACCES {
				s.notifyContention()
				return ErrWriterBusy
			}
			if err == unix.EINVAL || err == unix.ENOSYS {
				if s.readOnly {
					return err
				}
				return nil
			}
			return err
		}
		return nil
	}
	deadline := time.Now().Add(s.busyTimeout)
	delay := 50 * time.Microsecond
	for {
		err := unix.FcntlFlock(uintptr(s.lockFd), unix.F_OFD_SETLK, &fl)
		if err == nil {
			return nil
		}
		if err == unix.EINVAL || err == unix.ENOSYS {
			if s.readOnly {
				return err
			}
			return nil
		}
		if err != unix.EAGAIN && err != unix.EACCES {
			return err
		}
		s.notifyContention()
		if !time.Now().Before(deadline) {
			return ErrWriterBusy
		}
		time.Sleep(delay)
		delay *= 2
		if delay > time.Millisecond {
			delay = time.Millisecond
		}
	}
}

// SetOnLockContention enregistre un rappel invoqué dès qu'une tentative
// d'acquisition du verrou d'écrivain (lockWriter) rencontre un conflit actif (EAGAIN/EACCES).
func (s *Shard) SetOnLockContention(cb func()) {
	if s == nil {
		return
	}
	s.contentionMu.Lock()
	defer s.contentionMu.Unlock()
	s.onLockContention = cb
}

func (s *Shard) unlockOFD() {
	if s != nil && s.lockFd >= 0 {
		fl := unix.Flock_t{Type: unix.F_UNLCK, Whence: int16(unix.SEEK_SET), Start: 0, Len: 1}
		_ = unix.FcntlFlock(uintptr(s.lockFd), unix.F_OFD_SETLK, &fl)
	}
}

func (s *Shard) Put(key, val []byte, opts ...WriteOption) error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	policy, set := resolveWriteOptions(opts)
	if set {
		s.writeDurPolicy = policy
		s.writeDurSet = true
		defer func() {
			s.writeDurSet = false
			s.writeDurPolicy = CommitPolicy{}
		}()
	}
	if err := s.retryAfterHeapFull(key, s.putBody(key, val), func() error {
		return s.putBody(key, val)
	}); err != nil {
		return err
	}
	return s.applyCallDurability(policy, set)
}

// applyCallDurability scelle l'intention de durabilité portée par l'appel.
// CommitImmediate force le pointage du shard si des écritures restent non
// pointées (le chemin syncWAL l'a déjà fait hors groupe) ; CommitReplicated
// attend l'acquittement de quorum du ReplicationSink branché, et se replie sur
// le pointage local sans dispositif de réplication, conformément au contrat.
func (s *Shard) applyCallDurability(policy CommitPolicy, set bool) error {
	if !set {
		return nil
	}
	switch policy.Mode {
	case CommitImmediate:
		if s.wal != nil && s.unflushed == 0 && s.wal.pendingN == 0 && s.wal.qCount == 0 {
			if s.committer != nil {
				s.committer.unmark(s)
			}
			return nil
		}
		if err := s.flushWAL(); err != nil {
			return err
		}
		if s.committer != nil {
			s.committer.unmark(s)
		}
		return nil
	case CommitReplicated:
		sink := s.ReplicationSink()
		if sink == nil {
			return s.flushWAL()
		}
		return sink.WaitAck(context.Background(), s.LastReplSeq())
	default:
		return nil
	}
}

func (s *Shard) putBody(key, val []byte) error {
	s.ensurePack()
	if err := s.preparePublish(); err != nil {
		return err
	}
	initialHeapRoot := s.heapRoot
	initialHeapUsed := s.heapUsed
	fail := func(err error) error {
		s.heapRoot = initialHeapRoot
		s.heapUsed = initialHeapUsed
		s.convergeDirtyFromPub()
		return err
	}
	cellVal, isOverflow, headPage, numPages, err := s.encodeValue(val)
	if err != nil {
		return fail(err)
	}
	if isOverflow {
		if s.pager != nil && int(numPages)+16 > len(s.pager.slots) {
			if err := s.pager.Reserve(int(numPages) + 16); err != nil {
				return fail(err)
			}
		}
		for p := headPage; p < headPage+numPages; p++ {
			off := p * pageN
			if err := s.pager.PutPage(p*pageLBAs, s.dirty[off:off+pageN]); err != nil {
				return fail(err)
			}
		}
		if _, err := s.pager.FlushDirty(); err != nil {
			return fail(err)
		}
	}
	rec, err := s.journalPut(key, cellVal)
	if err != nil {
		return fail(err)
	}
	if err := s.applyInsertVer(s.pub, key, rec.ID[:], cellVal); err != nil {
		return fail(err)
	}
	if err := s.publish(); err != nil {
		return err
	}
	s.replicate(s.id, byte(RecPut), key, val)
	s.emitEvent(EventMutation, key, rec.ID, nil, s.counter)
	return nil
}

// SetReplicationHook configure le crochet de réplication synchrone exécuté
// sous le verrou d'écrivain (lockWriter) immédiatement après chaque publication WAL locale.
func (s *Shard) SetReplicationHook(hook func(shard uint16, recType byte, key, val []byte)) {
	if s == nil {
		return
	}
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	s.replHook = hook
}

// SetEventSink branche un puits d'événements sur le shard. Le puits reste la
// propriété de l'appelant : Close ne le ferme pas. Un puits nul désactive le
// flux. L'appel est sûr vis-à-vis des écrivains concurrents.
func (s *Shard) SetEventSink(sink EventSink) {
	if s == nil {
		return
	}
	s.eventMu.Lock()
	s.eventSink = sink
	s.eventMu.Unlock()
}

func (s *Shard) hasEventSink() bool {
	if s == nil {
		return false
	}
	s.eventMu.RLock()
	ok := s.eventSink != nil
	s.eventMu.RUnlock()
	return ok
}

// emitEvent consigne un événement sans interpréter la charge utile. L'émission
// est au mieux : le journal d'événements est une trace secondaire, jamais la
// source de vérité, et un échec d'émission ne remet pas en cause une écriture
// déjà publiée.
func (s *Shard) emitEvent(kind EventKind, key []byte, version [16]byte, payload []byte, watermark uint64) {
	if s == nil {
		return
	}
	s.eventMu.RLock()
	sink := s.eventSink
	s.eventMu.RUnlock()
	if sink == nil {
		return
	}
	_ = sink.Emit(TxnEvent{
		Kind:      kind,
		Shard:     s.id,
		Timestamp: uint64(time.Now().UnixNano()),
		VersionID: version,
		Watermark: watermark,
		Key:       key,
		Payload:   payload,
	})
}

// compactionPayload encode la rétention appliquée et la phase (0 avant,
// 1 après) d'un événement de compaction. La frontière (watermark) de
// l'événement porte le compteur de mutations du shard au moment de la décision.
func compactionPayload(retention Retention, phase byte) []byte {
	return []byte{byte(retention), phase}
}

func (s *Shard) Delete(key []byte) error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	return s.retryAfterHeapFull(key, s.deleteBody(key), func() error {
		return s.deleteBody(key)
	})
}

func (s *Shard) deleteBody(key []byte) error {
	s.ensurePack()
	if len(key) == 0 {
		return errInsert
	}
	if err := s.preparePublish(); err != nil {
		return err
	}
	initialHeapRoot := s.heapRoot
	initialHeapUsed := s.heapUsed
	fail := func(err error) error {
		s.heapRoot = initialHeapRoot
		s.heapUsed = initialHeapUsed
		s.convergeDirtyFromPub()
		return err
	}
	rec, err := s.journalDel(key)
	if err != nil {
		return fail(err)
	}
	if err := s.applyDelete(s.pub, key); err != nil {
		return fail(err)
	}
	// Sous ArchiveSuperseded, la suppression physique du tas chaud laisse les
	// versions déjà déportées dans le segment froid. Sans une pierre tombale
	// datée de l'identifiant de suppression, GetAsOf ressusciterait la clé
	// depuis le froid. Le déport précède la publication ; le rejeu répare une
	// pierre manquante si la panne tombe entre les deux.
	if err := s.appendDeleteTombstone(key, rec.ID); err != nil {
		return fail(err)
	}
	if err := s.publish(); err != nil {
		return err
	}
	s.replicate(s.id, byte(RecDel), key, nil)
	s.emitEvent(EventMutation, key, rec.ID, nil, s.counter)
	return nil
}

// DeleteBatch journalise et applique N suppressions par tranches bornées.
// La sémantique par clé est celle de Delete : même journal, même application sur
// le tas, même pierre tombale versionnée, mêmes hooks et événements.
//
// Le lot est découpé en tranches de deleteBatchChunk clés ; chaque tranche
// exécute sa propre paire preparePublish/publish, ce qui vide périodiquement
// l'ensemble sale du B-tree et borne le coût de chaque vidage. Le lot vide rend
// nil et une clé vide est refusée comme dans le chemin unitaire (errInsert).
//
// Une erreur dans une tranche laisse les tranches précédentes publiées, comme le
// ferait un lot partiel ; le retour arrière et la reprise après compaction ne
// couvrent que la tranche en cours.
const deleteBatchChunk = 4096

func (s *Shard) DeleteBatch(keys [][]byte) error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	return s.deleteBatchLocked(keys)
}

// deleteBatchLocked borne le lot par tranche. La reprise après épuisement du tas
// est appliquée à la tranche courante seule : une tranche déjà publiée n'est
// jamais rejouée, ce qui évite de dupliquer ses pierres tombales et événements.
func (s *Shard) deleteBatchLocked(keys [][]byte) error {
	s.ensurePack()
	total := len(keys)
	for start := 0; start < total; start += deleteBatchChunk {
		end := min(start+deleteBatchChunk, total)
		chunk := keys[start:end]
		if err := s.retryAfterHeapFull(nil, s.deleteChunkLocked(chunk), func() error {
			return s.deleteChunkLocked(chunk)
		}); err != nil {
			return err
		}
		// Le journal est recyclé entre tranches : sans ce pointage, un lot de
		// plusieurs tranches remplirait le WAL avant la fin.
		if s.wal != nil && s.wal.NeedsRecycle() {
			if err := s.checkpointLocked(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Shard) deleteChunkLocked(keys [][]byte) error {
	if err := s.preparePublish(); err != nil {
		return err
	}
	initialHeapRoot := s.heapRoot
	initialHeapUsed := s.heapUsed
	fail := func(err error) error {
		s.heapRoot = initialHeapRoot
		s.heapUsed = initialHeapUsed
		s.convergeDirtyFromPub()
		return err
	}
	src := s.pub
	var recs []Record
	if s.hasEventSink() {
		recs = make([]Record, len(keys))
	}
	for i := range keys {
		if len(keys[i]) == 0 {
			return fail(errInsert)
		}
		rec, err := s.journalDel(keys[i])
		if err != nil {
			return fail(err)
		}
		if err := s.applyDelete(src, keys[i]); err != nil {
			return fail(err)
		}
		src = s.dirty
		if err := s.appendDeleteTombstone(keys[i], rec.ID); err != nil {
			return fail(err)
		}
		if recs != nil {
			recs[i] = rec
		}
	}
	if err := s.publish(); err != nil {
		return err
	}
	for i := range keys {
		s.replicate(s.id, byte(RecDel), keys[i], nil)
	}
	if recs != nil {
		for i := range keys {
			s.emitEvent(EventMutation, keys[i], recs[i].ID, nil, s.counter)
		}
	}
	return nil
}

// appendDeleteTombstone consigne dans le segment froid une version de pierre
// tombale datée de l'identifiant de suppression, sous ArchiveSuperseded. Elle
// masque les versions antérieures déjà déportées et empêche la résurrection
// de la clé par GetAsOf. L'ajout est idempotent : un rejeu qui retombe sur un
// enregistrement de suppression déjà déporté ne double pas la pierre.
func (s *Shard) appendDeleteTombstone(key []byte, id [16]byte) error {
	if s.history == nil || len(key) == 0 {
		return nil
	}
	if s.history.HasTombstone(key, id) {
		return nil
	}
	rec := HistoryRecord{Key: key, VersionID: id, Tombstone: true}
	if err := s.history.Append(rec); err != nil {
		return err
	}
	if err := s.history.Sync(); err != nil {
		return err
	}
	s.emitEvent(EventArchive, key, id, nil, s.counter)
	return nil
}

func (s *Shard) txPut(txID c2uuidv7.UUID, key, val []byte) error {
	return s.retryAfterHeapFull(key, s.txPutOnce(txID, key, val), func() error {
		return s.txPutOnce(txID, key, val)
	})
}

func (s *Shard) txPutOnce(txID c2uuidv7.UUID, key, val []byte) error {
	s.ensurePack()
	cellVal, isOverflow, headPage, numPages, err := s.encodeValue(val)
	if err != nil {
		return err
	}
	if isOverflow {
		if s.pager != nil && int(numPages)+16 > len(s.pager.slots) {
			if err := s.pager.Reserve(int(numPages) + 16); err != nil {
				return err
			}
		}
		for p := headPage; p < headPage+numPages; p++ {
			off := p * pageN
			if err := s.pager.PutPage(p*pageLBAs, s.dirty[off:off+pageN]); err != nil {
				return err
			}
		}
		if _, err := s.pager.FlushDirty(); err != nil {
			return err
		}
	}
	rec := Record{ID: txID, Type: RecPut, Payload: packTxKV(key, cellVal)}
	if err := s.wal.Append(rec); err != nil {
		return err
	}
	if err := s.syncWAL(); err != nil {
		return err
	}
	s.lastID = rec.ID
	s.hasLast = true
	src := s.dirty
	if uint64(len(src)) < s.shardBytes {
		src = s.pub
	}
	return s.applyInsertVer(src, key, rec.ID[:], cellVal)
}

func (s *Shard) txDelete(txID c2uuidv7.UUID, key []byte) error {
	return s.retryAfterHeapFull(key, s.txDeleteOnce(txID, key), func() error {
		return s.txDeleteOnce(txID, key)
	})
}

func (s *Shard) txDeleteOnce(txID c2uuidv7.UUID, key []byte) error {
	s.ensurePack()
	if len(key) == 0 {
		return errInsert
	}
	rec := Record{ID: txID, Type: RecDel, Payload: packTxKV(key, nil)}
	if err := s.wal.Append(rec); err != nil {
		return err
	}
	if err := s.syncWAL(); err != nil {
		return err
	}
	s.lastID = rec.ID
	s.hasLast = true
	src := s.dirty
	if uint64(len(src)) < s.shardBytes {
		src = s.pub
	}
	if err := s.applyDelete(src, key); err != nil {
		return err
	}
	return s.appendDeleteTombstone(key, rec.ID)
}

// PutBatch journalise N enregistrements sans Flush intermédiaire, puis un seul
// WAL.Flush (la barrière fdatasync du lot), applique N insertions sur dirty à
// partir de pub, puis matérialise les pages du tas sans barrière. Si une
// insertion échoue, le tas n'est pas écrit ; le WAL peut contenir des records
// extra que le redo appliquera au replay.
func (s *Shard) PutBatch(pairs [][2][]byte, opts ...WriteOption) error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if len(pairs) == 0 {
		return nil
	}
	policy, set := resolveWriteOptions(opts)
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	if set {
		s.writeDurPolicy = policy
		s.writeDurSet = true
		defer func() {
			s.writeDurSet = false
			s.writeDurPolicy = CommitPolicy{}
		}()
	}
	if err := s.retryAfterHeapFull(nil, s.putBatchLocked(pairs), func() error {
		return s.putBatchLocked(pairs)
	}); err != nil {
		return err
	}
	return s.applyCallDurability(policy, set)
}

func (s *Shard) putBatchLocked(pairs [][2][]byte) error {
	if err := s.preparePublish(); err != nil {
		return err
	}
	initialHeapRoot := s.heapRoot
	initialHeapUsed := s.heapUsed
	fail := func(err error) error {
		s.heapRoot = initialHeapRoot
		s.heapUsed = initialHeapUsed
		s.convergeDirtyFromPub()
		return err
	}

	cellVals := make([][]byte, len(pairs))
	hasOverflow := false
	for i := range pairs {
		if len(pairs[i][0]) > 0xFFFF {
			return fail(ErrPayloadTooLarge)
		}
		cellVal, isOverflow, headPage, numPages, err := s.encodeValue(pairs[i][1])
		if err != nil {
			return fail(err)
		}
		if isOverflow {
			hasOverflow = true
			for p := headPage; p < headPage+numPages; p++ {
				off := p * pageN
				if err := s.pager.PutPage(p*pageLBAs, s.dirty[off:off+pageN]); err != nil {
					return fail(err)
				}
			}
		}
		cellVals[i] = cellVal
	}
	if hasOverflow {
		if _, err := s.pager.FlushDirty(); err != nil {
			return fail(err)
		}
	}

	if s.wal != nil {
		need := len(s.wal.pending) + len(pairs)
		if cap(s.wal.pending) < need {
			grown := make([]Record, len(s.wal.pending), need)
			copy(grown, s.wal.pending)
			s.wal.pending = grown
		}
	}

	s.wal.BeginPack()
	now := uint64(time.Now().UnixNano())
	src := s.pub
	var lastID [16]byte
	var ids [][16]byte
	if s.hasEventSink() {
		ids = make([][16]byte, len(pairs))
	}
	for i := range pairs {
		id, err := NewID(now, s.id, s.counter)
		if err != nil {
			s.wal.DiscardPack()
			return fail(err)
		}
		s.counter++
		rec := Record{ID: id, Type: RecPut, Payload: packKV(pairs[i][0], cellVals[i])}
		if err := s.wal.Append(rec); err != nil {
			s.wal.DiscardPack()
			return fail(err)
		}
		if err := s.applyInsertVer(src, pairs[i][0], rec.ID[:], cellVals[i]); err != nil {
			s.wal.DiscardPack()
			return fail(err)
		}
		src = s.dirty
		lastID = rec.ID
		if ids != nil {
			ids[i] = rec.ID
		}
	}

	if err := s.wal.EndPack(); err != nil {
		return fail(err)
	}
	s.walFlushes++
	s.lastID = lastID
	s.hasLast = true

	if err := s.publish(); err != nil {
		return err
	}
	for i := range pairs {
		s.replicate(s.id, byte(RecPut), pairs[i][0], pairs[i][1])
	}
	if ids != nil {
		for i := range pairs {
			s.emitEvent(EventMutation, pairs[i][0], ids[i], nil, s.counter)
		}
	}
	// Matérialisation des pages du tas sur data.img sans barrière : le lot a
	// déjà scellé sa durabilité par l'unique fdatasync du journal (EndPack).
	// La barrière data n'a lieu qu'au pointage (checkpointLocked) ou à la
	// fermeture. Un lot n'émet donc qu'une barrière, jamais deux.
	if _, err := s.pager.FlushDirtyNoBarrier(); err != nil {
		return err
	}
	if s.wal != nil && s.wal.NeedsRecycle() {
		_ = s.checkpointLocked()
	}
	return nil
}

func (s *Shard) Get(key []byte) ([]byte, error) {
	var snap [16]byte
	for i := range snap {
		snap[i] = 0xFF
	}
	return s.GetAsOf(key, snap)
}

// GetAsOf résout la valeur visible d'une clé à un instantané donné. Le tas
// chaud est consulté d'abord ; si aucune version chaude ne satisfait
// l'instantané (clé absente du tas, ou toutes les versions postérieures), le
// segment d'historique est consulté quand il est ouvert
// (RetentionArchiveSuperseded). Le drapeau de pierre tombale du format est
// respecté : l'enregistrement rend la clé absente sans résurrection d'une
// version antérieure.
func (s *Shard) GetAsOf(key []byte, snap [16]byte) ([]byte, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	h := s.pinLive()
	if h == nil {
		return nil, unix.EBADF
	}
	got, err := getAsOfHeap(h.buf, h.root, key, snap)
	s.unpinLive(h)
	if err == nil {
		return got, nil
	}
	if !errors.Is(err, ErrNotFound) || s.history == nil {
		return nil, err
	}
	rec, found, herr := s.history.Resolve(key, snap)
	if herr != nil {
		return nil, herr
	}
	if !found || rec.Tombstone {
		return nil, errNotFound
	}
	return append([]byte(nil), rec.Value...), nil
}

func (s *Shard) View() (*View, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	h := s.pinLive()
	if h == nil {
		return nil, unix.EBADF
	}
	v := &View{heap: h.buf, root: h.root, pin: h}
	if h.hasSnap {
		v.snap = h.snap
	} else {
		for i := range v.snap {
			v.snap[i] = 0xFF
		}
	}
	probeEmit(s.id, ProbeOpViewOpen, 0, 0, 0, 0, "view opened")
	return v, nil
}

func (v *View) Get(key []byte) ([]byte, error) {
	if v == nil || v.heap == nil {
		return nil, unix.EBADF
	}
	return getAsOfHeap(v.heap, v.root, key, v.snap)
}

func (v *View) ScanPrefix(prefix []byte) ([][]byte, error) {
	if v == nil || v.heap == nil {
		return nil, unix.EBADF
	}
	return scanPrefixHeap(v.heap, v.root, prefix)
}

func (v *View) Close() error {
	if v == nil || v.heap == nil {
		return unix.EBADF
	}
	if v.pin != nil {
		v.pin.refs.Add(-1)
		v.pin = nil
	}
	v.heap = nil
	probeEmit(0, ProbeOpViewClose, 0, 0, 0, 0, "view closed")
	return nil
}

func (s *Shard) ScanPrefix(prefix []byte) ([][]byte, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	h := s.pinLive()
	if h == nil {
		return nil, unix.EBADF
	}
	defer s.unpinLive(h)
	return scanPrefixHeap(h.buf, h.root, prefix)
}

// InspectPageStats retourne des métriques physiques de disposition B-Tree :
// rootPage, isRootLeaf, cellCount, heapPagesAllocated, freeBytes.
func (s *Shard) InspectPageStats() (rootPage uint64, isRootLeaf bool, cellCount uint16, heapPages uint64, freeBytes uint16, err error) {
	if err := s.ready(); err != nil {
		return 0, false, 0, 0, 0, err
	}
	h := s.pinLive()
	if h == nil {
		return 0, false, 0, 0, 0, unix.EBADF
	}
	defer s.unpinLive(h)

	rootPage = h.root
	heapPages = s.heapPubUsed
	if heapPages == 0 {
		heapPages = s.heapUsed
	}
	if heapPages == 0 {
		heapPages = 1
	}
	if (rootPage+1)*pageN > uint64(len(h.buf)) {
		return rootPage, false, 0, heapPages, 0, errors.New("c2db: root page beyond heap")
	}
	page := h.buf[rootPage*pageN : (rootPage+1)*pageN]
	pageType := page[20]
	isRootLeaf = (pageType == 1)
	cellCount = binary.LittleEndian.Uint16(page[22:24])

	lowestCell := uint16(16384)
	for i := 0; i < int(cellCount); i++ {
		slotAddr := 64 + i*2
		if slotAddr+2 <= len(page) {
			cellOff := binary.LittleEndian.Uint16(page[slotAddr : slotAddr+2])
			if cellOff < lowestCell {
				lowestCell = cellOff
			}
		}
	}
	slotEnd := uint16(64 + cellCount*2)
	if lowestCell >= slotEnd {
		freeBytes = lowestCell - slotEnd
	}
	return rootPage, isRootLeaf, cellCount, heapPages, freeBytes, nil
}

func scanPrefixHeap(heap []byte, root uint64, prefix []byte) ([][]byte, error) {
	if len(heap) >= int(pageN) && heap[20] == 0 {
		return nil, nil
	}
	nbytes := uint64(len(heap))
	npages := nbytes / pageN
	// Passe 1 : dénombrement sans écriture. out=nil laisse le noyau compter
	// sans écrire ; outn=^uint64(0) neutralise son contrôle de capacité, qui
	// n'est pas bornant pour un dénombrement. Le noyau rend alors le compte
	// exact, seule information manquante à l'appelant.
	probe := Db_bt_scan_prefix_heap(heap, nbytes, npages, root, prefix, uint64(len(prefix)), nil, ^uint64(0))
	if probe.Ok == 0 {
		return nil, errInsert
	}
	if probe.Nfound == 0 {
		return nil, nil
	}
	// Passe 2 : remplissage sur un tampon dimensionné exactement au compte.
	packed := make([]byte, probe.Nfound*4)
	r := Db_bt_scan_prefix_heap(heap, nbytes, npages, root, prefix, uint64(len(prefix)), packed, uint64(len(packed)))
	if r.Ok == 0 || r.Nfound != probe.Nfound {
		return nil, errInsert
	}
	out := make([][]byte, 0, r.Nfound)
	var prev string
	for i := uint64(0); i < r.Nfound; i++ {
		woff := i * 4
		if woff+4 > uint64(len(packed)) {
			break
		}
		off := uint64(binary.LittleEndian.Uint32(packed[woff : woff+4]))
		if off+4 > uint64(len(heap)) {
			continue
		}
		klen := uint64(binary.LittleEndian.Uint16(heap[off : off+2]))
		if off+4+klen > uint64(len(heap)) {
			continue
		}
		k := append([]byte(nil), heap[off+4:off+4+klen]...)
		sk := string(k)
		if sk == prev {
			continue
		}
		prev = sk
		out = append(out, k)
	}
	return out, nil
}

func (s *Shard) Update(fn func(*Shard) error) error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	if err := fn(s); err != nil {
		return err
	}
	return s.flushPages()
}

func getRawAsOfHeap(heap []byte, root uint64, key []byte, snap [16]byte) ([]byte, error) {
	if len(heap) >= int(pageN) && heap[20] == 0 {
		return nil, errNotFound
	}
	nbytes := uint64(len(heap))
	npages := nbytes / pageN
	probe := Db_bt_get_as_of_heap(heap, nbytes, npages, root, key, uint64(len(key)), snap[:], nil, 0)
	if probe.Found == 0 {
		return nil, errNotFound
	}
	out := make([]byte, probe.Len_)
	got := Db_bt_get_as_of_heap(heap, nbytes, npages, root, key, uint64(len(key)), snap[:], out, probe.Len_)
	if got.Found == 0 {
		return nil, errNotFound
	}
	return out[:got.Len_], nil
}

func getAsOfHeap(heap []byte, root uint64, key []byte, snap [16]byte) ([]byte, error) {
	raw, err := getRawAsOfHeap(heap, root, key, snap)
	if err != nil {
		return nil, err
	}
	npages := uint64(len(heap)) / pageN
	return decodeValue(heap, npages, raw)
}

func (s *Shard) Close() error {
	if s != nil && s.readOnly {
		return s.KillWithoutFlush()
	}
	if s == nil || s.data == nil {
		return unix.EBADF
	}
	var err error
	if s.wal != nil {
		err = s.wal.Flush()
		s.unflushed = 0
	}
	if s.pager != nil {
		if err == nil {
			n, err2 := s.pager.FlushDirty()
			if err2 != nil {
				err = err2
			} else if n > 0 {
				s.dataFlushes++
			}
		}
		s.pager.release()
		s.pager = nil
	}
	if s.wal != nil {
		if err2 := s.wal.Close(); err == nil {
			err = err2
		}
		s.wal = nil
	}
	if err2 := s.data.Close(); err == nil {
		err = err2
	}
	s.data = nil
	if s.lockFd >= 0 {
		_ = unix.Close(s.lockFd)
		s.lockFd = -1
	}
	if s.txlog != nil {
		if err2 := s.txlog.Close(); err == nil {
			err = err2
		}
		s.txlog = nil
	}
	if s.history != nil {
		if err2 := s.history.Close(); err == nil {
			err = err2
		}
		s.history = nil
	}
	unmapHeaps(s)
	return err
}

func unmapHeaps(s *Shard) {
	if s == nil {
		return
	}
	unmapHeap(s.pub)
	if len(s.dirty) > 0 && (len(s.pub) == 0 || &s.dirty[0] != &s.pub[0]) {
		unmapHeap(s.dirty)
	}
	for _, h := range s.holdHeaps {
		if h == nil || len(h.buf) == 0 {
			continue
		}
		if len(s.pub) > 0 && &h.buf[0] == &s.pub[0] {
			continue
		}
		if len(s.dirty) > 0 && &h.buf[0] == &s.dirty[0] {
			continue
		}
		unmapHeap(h.buf)
	}
	if len(s.spare) > 0 && (len(s.pub) == 0 || &s.spare[0] != &s.pub[0]) && (len(s.dirty) == 0 || &s.spare[0] != &s.dirty[0]) {
		unmapHeap(s.spare)
	}
	s.live.Store(nil)
	s.pub, s.dirty, s.holdHeaps, s.spare = nil, nil, nil, nil
}

func unmapHeap(b []byte) {
	if len(b) == 0 {
		return
	}
	_ = unix.Munmap(b)
}

func (s *Shard) CloseWithoutFlush() error {
	if s != nil && s.readOnly {
		return s.KillWithoutFlush()
	}
	if s == nil || s.data == nil {
		return unix.EBADF
	}
	var err error
	if s.wal != nil {
		err = s.wal.flushPending()
	}
	if s.pager != nil {
		if err2 := s.pager.CloseWithoutFlush(); err == nil {
			err = err2
		}
		s.pager = nil
	}
	if s.wal != nil {
		if err2 := s.wal.Close(); err == nil {
			err = err2
		}
		s.wal = nil
	}
	if err2 := s.data.Close(); err == nil {
		err = err2
	}
	s.data = nil
	if s.lockFd >= 0 {
		_ = unix.Close(s.lockFd)
		s.lockFd = -1
	}
	if s.txlog != nil {
		if err2 := s.txlog.Close(); err == nil {
			err = err2
		}
		s.txlog = nil
	}
	if s.history != nil {
		if err2 := s.history.Close(); err == nil {
			err = err2
		}
		s.history = nil
	}
	unmapHeaps(s)
	return err
}

// KillWithoutFlush simule un arrêt brutal et non coopératif (SIGKILL / perte de tension) :
// fermeture brute de tous les descripteurs et démappage mémoire SANS vidange des écritures
// en attente (aucun appel à flushPending, flushQuantum ou fsync).
func (s *Shard) KillWithoutFlush() error {
	if s == nil || s.data == nil {
		return unix.EBADF
	}
	var err error
	if s.pager != nil {
		if err2 := s.pager.CloseWithoutFlush(); err == nil {
			err = err2
		}
		s.pager = nil
	}
	if s.wal != nil {
		if err2 := s.wal.KillWithoutFlush(); err == nil {
			err = err2
		}
		s.wal = nil
	}
	if err2 := s.data.Close(); err == nil {
		err = err2
	}
	s.data = nil
	if s.lockFd >= 0 {
		_ = unix.Close(s.lockFd)
		s.lockFd = -1
	}
	if s.txlog != nil {
		if err2 := s.txlog.Close(); err == nil {
			err = err2
		}
		s.txlog = nil
	}
	if s.history != nil {
		if err2 := s.history.Close(); err == nil {
			err = err2
		}
		s.history = nil
	}
	unmapHeaps(s)
	return err
}

func (s *Shard) poison(err error) {
	if err != nil {
		s.poisonErr.Store(&err)
	}
}

func (s *Shard) SetPublishFaultInjection(v bool) {
	s.publishFaultInjection.Store(v)
}

func (s *Shard) dirtyPagesCount() int {
	if s.dirtyOverflow {
		n := 0
		for _, w := range s.dirtyMask {
			n += bits.OnesCount64(w)
		}
		return n + 1 // +1 pour page 0
	}
	return s.dirtyListLen + 1 // +1 pour page 0
}

func (s *Shard) ready() error {
	if s == nil || s.data == nil || s.wal == nil || s.pager == nil {
		return unix.EBADF
	}
	if p := s.poisonErr.Load(); p != nil {
		return *p
	}
	return s.data.checkReady()
}

func (s *Shard) flushPages() error {
	if err := s.wal.Flush(); err != nil {
		return err
	}
	n, err := s.pager.FlushDirty()
	if n > 0 {
		s.dataFlushes++
	}
	return err
}

func (s *Shard) journalPut(key, val []byte) (Record, error) {
	if len(key) > 0xFFFF || len(val) > 0xFFFF {
		return Record{}, ErrPayloadTooLarge
	}
	id, err := NewID(uint64(time.Now().UnixNano()), s.id, s.counter)
	if err != nil {
		return Record{}, err
	}
	s.counter++
	rec := Record{ID: id, Type: RecPut, Payload: packKV(key, val)}
	if err := s.wal.Append(rec); err != nil {
		return Record{}, err
	}
	if err := s.syncWAL(); err != nil {
		return Record{}, err
	}
	s.lastID = rec.ID
	s.hasLast = true
	return rec, nil
}

func (s *Shard) journalDel(key []byte) (Record, error) {
	if len(key) > 0xFFFF {
		return Record{}, ErrPayloadTooLarge
	}
	id, err := NewID(uint64(time.Now().UnixNano()), s.id, s.counter)
	if err != nil {
		return Record{}, err
	}
	s.counter++
	rec := Record{ID: id, Type: RecDel, Payload: packKV(key, nil)}
	if err := s.wal.Append(rec); err != nil {
		return Record{}, err
	}
	if err := s.syncWAL(); err != nil {
		return Record{}, err
	}
	s.lastID = rec.ID
	s.hasLast = true
	return rec, nil
}

// Mut applique de manière atomique une séquence d'opérations de mutation (qlMutOp)
// sur le document identifié par key, calcule le document résultant via le moteur
// applicatif applyMutOps, enregistre l'événement RecMut dans le WAL et publie
// la nouvelle version.
func (s *Shard) Mut(key []byte, ops []qlMutOp) error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	s.ensurePack()

	var maxSnap [16]byte
	for i := range maxSnap {
		maxSnap[i] = 0xFF
	}
	cur, err := getAsOfHeap(s.pub, s.heapRoot, key, maxSnap)
	if err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	next, err := applyMutOps(cur, ops)
	if err != nil {
		return err
	}

	raw, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	if len(key) > 0xFFFF || len(raw) > 0xFFFF {
		return ErrPayloadTooLarge
	}
	id, err := NewID(uint64(time.Now().UnixNano()), s.id, s.counter)
	if err != nil {
		return err
	}
	s.counter++
	rec := Record{ID: id, Type: RecMut, Payload: packKV(key, raw)}
	if err := s.wal.Append(rec); err != nil {
		return err
	}
	if err := s.syncWAL(); err != nil {
		return err
	}
	s.lastID = rec.ID
	s.hasLast = true
	if err := s.applyInsertVer(s.pub, key, rec.ID[:], next); err != nil {
		return err
	}
	if err := s.publish(); err != nil {
		return err
	}
	s.emitEvent(EventMutation, key, rec.ID, nil, s.counter)
	return nil
}

func (s *Shard) commitMut(key, doc []byte, ops []qlMutOp) error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	s.ensurePack()
	raw, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	if len(key) > 0xFFFF || len(raw) > 0xFFFF {
		return ErrPayloadTooLarge
	}
	id, err := NewID(uint64(time.Now().UnixNano()), s.id, s.counter)
	if err != nil {
		return err
	}
	s.counter++
	rec := Record{ID: id, Type: RecMut, Payload: packKV(key, raw)}
	if err := s.wal.Append(rec); err != nil {
		return err
	}
	if err := s.syncWAL(); err != nil {
		return err
	}
	s.lastID = rec.ID
	s.hasLast = true
	if err := s.applyInsertVer(s.pub, key, rec.ID[:], doc); err != nil {
		return err
	}
	return s.publish()
}

var lastReplayShard atomic.Pointer[Shard]

func ActiveReplayShard() *Shard {
	return lastReplayShard.Load()
}

func (s *Shard) replay() error {
	lastReplayShard.Store(s)
	defer lastReplayShard.Store(nil)
	s.replayDiscarded = false
	// Le rejeu réaligne pub sur dirty à chaque enregistrement et remet à zéro
	// l'ensemble différentiel. L'union des pages divergentes est accumulée à
	// part pour la publication terminale.
	if len(s.replayMask) != len(s.dirtyMask) {
		s.replayMask = make([]uint64, len(s.dirtyMask))
	} else {
		for w := range s.replayMask {
			s.replayMask[w] = 0
		}
	}
	recs, resumeAt, err := s.wal.ReplayResumable()
	if err != nil {
		return err
	}
	if s.readOnly && s.pub[20] == 0 {
		for _, rec := range recs[resumeAt:] {
			if !walIsCheckpoint(rec.Type) {
				return ErrRecoveryRequired
			}
		}
	}
	// Une vue en lecture seule n'engage le tampon de travail que si le rejeu a
	// effectivement des enregistrements à appliquer après le dernier pointage
	// durable. Le tampon neuf est d'abord aligné sur pub, reproduisant
	// l'invariant du régime écrivain avant la première copie sur écriture du
	// noyau ; le régime nominal — journal déjà pointé — n'alloue rien.
	if s.readOnly && uint64(len(s.dirty)) < s.shardBytes && len(recs) > resumeAt {
		if err := s.ensureDirty(); err != nil {
			return err
		}
		s.syncDirtyFromPub()
	}

	// Recensement des transactions commitées (RecTxCommit)
	committed := make(map[[16]byte]bool)
	for i := range recs {
		if recs[i].Type == RecTxCommit {
			committed[recs[i].ID] = true
		}
	}

	// Pré-amorçage du compteur sur TOUT le journal, avant toute application : le
	// préfixe couvert par le dernier pointage porte le compteur, et il n'est pas
	// réappliqué sur le tas. Sans cet amorçage, un identifiant neuf pourrait
	// précéder un identifiant déjà durable.
	for i := range recs {
		if n := CounterOf(c2uuidv7.UUID(recs[i].ID)); n >= s.counter {
			s.counter = n + 1
		}
	}

	txPending := make(map[[16]byte][]Record)

	applyRecord := func(rec Record) error {
		n := CounterOf(c2uuidv7.UUID(rec.ID))
		if n >= s.counter {
			s.counter = n + 1
		}
		switch rec.Type {
		case RecPut:
			_, key, val, ok := unpackKV(rec.Payload)
			if !ok {
				return errPayload
			}
			// Idempotence stricte : une version d'identifiant supérieur ou égal
			// est déjà durable dans le tas rechargé depuis data.img. La
			// réinsérer, même à valeur différente, duplique l'historique et
			// gonfle le tas jusqu'au refus d'insertion (cause observée).
			if latestID, ok := s.replayGetLatestID(key); ok && bytes.Compare(latestID[:], rec.ID[:]) >= 0 {
				return nil
			}
			if isOfl, hPage, nPages := checkOverflowDesc(val); isOfl {
				tLen := binary.LittleEndian.Uint32(val[1:5])
				if !s.replayValidateOverflowChain(hPage, tLen) {
					if s.strictOpen {
						return fmt.Errorf("c2db: replay invalid overflow chain for key %q", key)
					}
					return nil
				}
				if s.heapUsed < hPage+nPages {
					s.heapUsed = hPage + nPages
				}
			}
			// R1 : le rejeu reproduit le repack du chemin d'écriture, ancré sur
			// l'état vivant et sans pointage de WAL.
			if err := s.appliquerAvecRepackRejeu(func() error {
				return s.applyInsertVer(s.pub, key, rec.ID[:], val)
			}); err != nil {
				return fmt.Errorf("applyRecord key %s (len %d): %w", string(key), len(val), err)
			}
			s.syncPubFromDirty()
		case RecDel:
			_, key, _, ok := unpackKV(rec.Payload)
			if !ok {
				return errPayload
			}
			// Idempotence stricte : la suppression c2db est dure (le noyau
			// retire les cellules, aucun tombstone). Une clé absente a donc déjà
			// subi sa suppression, ou n'a jamais existé ; la re-supprimer ne
			// ferait que recopier des pages. La pierre tombale froide est en
			// revanche (re)posée : une panne entre l'aplatissement chaud et son
			// écriture ne doit pas laisser le froid ressusciter la clé.
			if _, err := s.replayGet(key); errors.Is(err, errNotFound) {
				return s.appendDeleteTombstone(key, rec.ID)
			}
			if err := s.appliquerAvecRepackRejeu(func() error {
				return s.applyDelete(s.pub, key)
			}); err != nil {
				return err
			}
			s.syncPubFromDirty()
			return s.appendDeleteTombstone(key, rec.ID)
		case RecMut:
			_, key, raw, ok := unpackKV(rec.Payload)
			if !ok {
				return errPayload
			}
			var ops []qlMutOp
			if err := json.Unmarshal(raw, &ops); err != nil {
				return errPayload
			}
			// Idempotence stricte : ne pas recalculer ni réinsérer une mutation
			// dont la version est déjà présente.
			if latestID, ok := s.replayGetLatestID(key); ok && bytes.Compare(latestID[:], rec.ID[:]) >= 0 {
				return nil
			}
			var maxSnap [16]byte
			for i := range maxSnap {
				maxSnap[i] = 0xFF
			}
			cur, err := getAsOfHeap(s.pub, s.heapRoot, key, maxSnap)
			if err != nil && !errors.Is(err, errNotFound) {
				return err
			}
			next, err := applyMutOps(cur, ops)
			if err != nil {
				return err
			}
			if err := s.appliquerAvecRepackRejeu(func() error {
				return s.applyInsertVer(s.pub, key, rec.ID[:], next)
			}); err != nil {
				return err
			}
			s.syncPubFromDirty()
		}
		return nil
	}

	// Validation préalable d'un groupe de mutations pour garantir l'indivisibilité (Tout-ou-Rien)
	validateTxGroup := func(group []Record) bool {
		for recIdx, r := range group {
			s.ReplayValStep = recIdx
			s.ReplayValErr = nil
			switch r.Type {
			case RecPut:
				_, key, val, ok := unpackKV(r.Payload)
				if !ok || len(key) == 0 {
					s.ReplayValErr = fmt.Errorf("c2db: replay validation invalid kv for rec %d", recIdx)
					cihook55.Fire("replay-tx-validation-step-failed")
					return false
				}
				if isOfl, hPage, _ := checkOverflowDesc(val); isOfl {
					tLen := binary.LittleEndian.Uint32(val[1:5])
					if !s.replayValidateOverflowChain(hPage, tLen) {
						s.ReplayValErr = fmt.Errorf("c2db: replay validation invalid overflow chain at page %d", hPage)
						cihook55.Fire("replay-tx-validation-step-failed")
						return false
					}
					cihook55.Fire("replay-tx-overflow-loaded")
				}
			case RecDel:
				_, key, _, ok := unpackKV(r.Payload)
				if !ok || len(key) == 0 {
					s.ReplayValErr = fmt.Errorf("c2db: replay validation invalid del kv for rec %d", recIdx)
					cihook55.Fire("replay-tx-validation-step-failed")
					return false
				}
			case RecMut:
				_, key, raw, ok := unpackKV(r.Payload)
				if !ok || len(key) == 0 {
					s.ReplayValErr = fmt.Errorf("c2db: replay validation invalid mut kv for rec %d", recIdx)
					cihook55.Fire("replay-tx-validation-step-failed")
					return false
				}
				var ops []qlMutOp
				if err := json.Unmarshal(raw, &ops); err != nil {
					s.ReplayValErr = fmt.Errorf("c2db: replay validation json unmarshal rec %d: %w", recIdx, err)
					cihook55.Fire("replay-tx-validation-step-failed")
					return false
				}
			default:
				s.ReplayValErr = fmt.Errorf("c2db: replay validation unsupported type %d for rec %d", r.Type, recIdx)
				cihook55.Fire("replay-tx-validation-step-failed")
				return false
			}
		}
		return true
	}

	// applyTxGroup applique les mutations d'un groupe transactionnel dans s.dirty.
	// Le tampon source est recalculé à chaque enregistrement (s.dirty, repli s.pub).
	// La fonction ne publie ni ne pointe : s.pub demeure l'état pré-groupe tant que
	// l'appelant n'a pas appelé syncPubFromDirty().
	applyTxGroup := func(group []Record) error {
		for mutIdx, r := range group {
			s.ReplayLastMutIdx = mutIdx
			s.ReplayLastMutErr = nil
			switch r.Type {
			case RecPut:
				_, key, val, _ := unpackKV(r.Payload)
				s.ReplayLastMutKey = string(key)
				// Idempotence stricte, comme applyRecord : une version d'identifiant
				// supérieur ou égal est déjà durable dans le tas rechargé ; la
				// réinsérer duplique l'historique et gonfle le tas (cause observée
				// de perte de clés après rejeu d'un préfixe déjà pointé).
				if latestID, ok := s.replayGetLatestID(key); ok && bytes.Compare(latestID[:], r.ID[:]) >= 0 {
					continue
				}
				if isOfl, hPage, nPages := checkOverflowDesc(val); isOfl {
					tLen := binary.LittleEndian.Uint32(val[1:5])
					if !s.replayValidateOverflowChain(hPage, tLen) {
						s.ReplayLastMutErr = fmt.Errorf("c2db: replay invalid overflow chain for key %q", key)
						cihook55.Fire("replay-tx-mutation-failed")
						return s.ReplayLastMutErr
					}
					if s.heapUsed < hPage+nPages {
						s.heapUsed = hPage + nPages
					}
				}
				src := s.dirty
				if uint64(len(src)) < s.shardBytes {
					src = s.pub
				}
				if err := s.applyInsertVer(src, key, r.ID[:], val); err != nil {
					s.ReplayLastMutErr = err
					cihook55.Fire("replay-tx-mutation-failed")
					return err
				}
				cihook55.Fire("replay-tx-mutation-applied")
			case RecDel:
				_, key, _, _ := unpackKV(r.Payload)
				s.ReplayLastMutKey = string(key)
				if _, err := s.replayGet(key); errors.Is(err, errNotFound) {
					continue
				}
				src := s.dirty
				if uint64(len(src)) < s.shardBytes {
					src = s.pub
				}
				if err := s.applyDelete(src, key); err != nil {
					s.ReplayLastMutErr = err
					cihook55.Fire("replay-tx-mutation-failed")
					return err
				}
				cihook55.Fire("replay-tx-mutation-applied")
			case RecMut:
				_, key, raw, _ := unpackKV(r.Payload)
				s.ReplayLastMutKey = string(key)
				if latestID, ok := s.replayGetLatestID(key); ok && bytes.Compare(latestID[:], r.ID[:]) >= 0 {
					continue
				}
				var ops []qlMutOp
				if err := json.Unmarshal(raw, &ops); err != nil {
					s.ReplayLastMutErr = errPayload
					cihook55.Fire("replay-tx-mutation-failed")
					return errPayload
				}
				var maxSnap [16]byte
				for j := range maxSnap {
					maxSnap[j] = 0xFF
				}
				src := s.dirty
				if uint64(len(src)) < s.shardBytes {
					src = s.pub
				}
				cur, err := getAsOfHeap(src, s.heapRoot, key, maxSnap)
				if err != nil && !errors.Is(err, errNotFound) {
					s.ReplayLastMutErr = err
					cihook55.Fire("replay-tx-mutation-failed")
					return err
				}
				next, err := applyMutOps(cur, ops)
				if err != nil {
					s.ReplayLastMutErr = err
					cihook55.Fire("replay-tx-mutation-failed")
					return err
				}
				if err := s.applyInsertVer(src, key, r.ID[:], next); err != nil {
					s.ReplayLastMutErr = err
					cihook55.Fire("replay-tx-mutation-failed")
					return err
				}
				cihook55.Fire("replay-tx-mutation-applied")
			default:
				e := fmt.Errorf("c2db: replay unsupported tx record type %d", r.Type)
				s.ReplayLastMutErr = e
				cihook55.Fire("replay-tx-mutation-failed")
				return e
			}
		}
		return nil
	}

	// Seule la queue postérieure au dernier pointage est réappliquée : le
	// préfixe est déjà durable dans data.img, seuls le compteur et le dernier
	// identifiant devaient en être réamorcés (ci-dessus).
	for i := resumeAt; i < len(recs); i++ {
		rec := recs[i]
		if rec.Type == RecTxCommit {
			if pending, ok := txPending[rec.ID]; ok {
				// Indivisibilité Tout-ou-Rien : si une mutation du groupe est invalide, rejeter tout le groupe
				preGroupRoot := s.heapRoot
				preGroupUsed := s.heapUsed
				if !validateTxGroup(pending) {
					// Restauration intégrale immédiate des effets de la validation
					s.heapRoot = preGroupRoot
					s.heapUsed = preGroupUsed
					s.convergeDirtyFromPub()
					cihook55.Fire("replay-tx-validation-failed")
					s.replayDiscarded = true
					if s.strictOpen {
						return fmt.Errorf("c2db: replay tx %x validation failed", rec.ID)
					}
					delete(txPending, rec.ID)
					continue
				}
				// W4 : le repack est exécuté à la frontière du groupe, jamais en
				// plein groupe. Le rollback s'appuie sur l'invariant « s.pub n'est
				// pas modifié pendant la boucle » ; un rebuildAllVersionsLocked
				// interlacé publie, échange s.pub et recycle s.dirty, ce qui
				// invaliderait preGroupRoot et scinderait le groupe. Chaque
				// tentative prend son instantané juste avant elle ; un échec
				// d'espace déclenche un rollback complet, puis un repack sur
				// l'état stable pré-groupe, puis un nouvel essai.
				var groupErr error
				for attempt := 0; attempt < 2; attempt++ {
					preGroupRoot = s.heapRoot
					preGroupUsed = s.heapUsed
					groupErr = applyTxGroup(pending)
					if groupErr == nil {
						// Toutes les mutations du groupe ont réussi : publication atomique vers s.pub
						s.syncPubFromDirty()
						break
					}
					// Aucun repack n'a eu lieu depuis cet instantané : rollback exact.
					s.heapRoot = preGroupRoot
					s.heapUsed = preGroupUsed
					s.convergeDirtyFromPub()
					cihook55.Fire("replay-tx-rolled-back")
					if !errors.Is(groupErr, ErrHeapFull) && !errors.Is(groupErr, ErrTreeFull) && !errors.Is(groupErr, errInsert) {
						break
					}
					if attempt > 0 {
						break
					}
					// Le repack est préventif et hors de l'instantané de rollback :
					// il s'exécute après la restauration complète ci-dessus.
					if rebErr := s.rebuildAllVersionsLocked(); rebErr != nil {
						return rebErr
					}
				}
				if groupErr != nil {
					s.replayDiscarded = true
					if s.strictOpen {
						return fmt.Errorf("c2db: replay tx %x failed: %w", rec.ID, groupErr)
					}
				}
				delete(txPending, rec.ID)
			}
			continue
		}

		// Inspection du flag d'appartenance transactionnelle
		var isTransactional bool
		if rec.Type == RecPut || rec.Type == RecDel || rec.Type == RecMut {
			flag, _, _, ok := unpackKV(rec.Payload)
			if ok && flag == txFlagTransactional {
				isTransactional = true
			}
		}

		if isTransactional {
			// Mutation transactionnelle explicite : REQUIERT RecTxCommit !
			if committed[rec.ID] {
				txPending[rec.ID] = append(txPending[rec.ID], rec)
			}
			// Si committed[rec.ID] est faux, la mutation transactionnelle sans commit est
			// SYSTÉMATIQUEMENT REJETÉE, même si elle est la seule survivante du log !
			continue
		}

		// Mutation autonome (standalone Put / Delete) : application immédiate
		if err := applyRecord(rec); err != nil {
			return err
		}
	}
	// Le dernier identifiant publié est celui du dernier enregistrement PORTEUR
	// D'UN IDENTIFIANT RÉEL. Un enregistrement de pointage porte un identifiant
	// nul ; le prendre pour dernier identifiant publié rendrait hasLast vrai avec
	// un instantané nul, ce qui masquerait tout parcours de préfixe après
	// réouverture (les lectures directes par clé, elles, restent servies).
	if n := len(recs); n > 0 {
		var nul [16]byte
		for i := n - 1; i >= 0; i-- {
			if bytes.Equal(recs[i].ID[:], nul[:]) {
				continue
			}
			s.lastID = recs[i].ID
			s.hasLast = true
			break
		}
	}
	// La publication terminale s'appuie sur l'ensemble sale : celui-ci a été
	// remis à zéro par chaque syncPubFromDirty. L'union des pages divergentes
	// est reportée vers dirtyMask pour que publish() transfère au pager les pages
	// reconstruites.
	s.commitReplayDirty()
	s.heapPubUsed = s.heapUsed
	s.installLive(s.pub)
	return nil
}

func (s *Shard) replayGet(key []byte) ([]byte, error) {
	var snap [16]byte
	for i := range snap {
		snap[i] = 0xFF
	}
	return getRawAsOfHeap(s.pub, s.heapRoot, key, snap)
}

func (s *Shard) replayGetLatestID(key []byte) ([16]byte, bool) {
	var zero [16]byte
	if len(key) == 0 || len(key) > 0xFFFF {
		return zero, false
	}
	heap := s.pub
	nbytes := uint64(len(heap))
	npages := nbytes / pageN
	if npages == 0 || s.heapRoot >= npages {
		return zero, false
	}

	page := s.heapRoot
	walked := uint64(0)
	base := page * pageN
	typ := heap[base+cursorTypeOffset]
	for typ == cursorTypeInternal && walked < npages {
		chosen := bt_internal_child(heap, nbytes, base, key, uint64(len(key)))
		if chosen >= npages || chosen == page {
			return zero, false
		}
		page = chosen
		walked++
		base = page * pageN
		typ = heap[base+cursorTypeOffset]
	}
	if typ != cursorTypeLeaf {
		return zero, false
	}

	var latestID [16]byte
	var found bool
	for walked < npages {
		if page >= npages {
			break
		}
		base = page * pageN
		if heap[base+cursorTypeOffset] != cursorTypeLeaf {
			break
		}
		nslots := int(binary.LittleEndian.Uint16(heap[base+cursorNSlotsOffset:]))
		for i := 0; i < nslots; i++ {
			slotAddr := base + cursorBodyOffset + uint64(i)*cursorBTSlotSize
			if slotAddr+2 > nbytes {
				break
			}
			cellOff := uint64(binary.LittleEndian.Uint16(heap[slotAddr:]))
			cellBase := base + cellOff
			if cellBase+4 > nbytes {
				break
			}
			cklen := uint64(binary.LittleEndian.Uint16(heap[cellBase:]))
			cvlen := uint64(binary.LittleEndian.Uint16(heap[cellBase+2:]))
			if cellBase+4+cklen+cursorBTIDLen+cvlen > nbytes {
				break
			}
			ckey := heap[cellBase+4 : cellBase+4+cklen]
			cmp := bytes.Compare(ckey, key)
			if cmp == 0 {
				copy(latestID[:], heap[cellBase+4+cklen:cellBase+4+cklen+cursorBTIDLen])
				found = true
			} else if cmp > 0 {
				if found {
					return latestID, true
				}
				return zero, false
			}
		}
		if found {
			return latestID, true
		}
		next := binary.LittleEndian.Uint64(heap[base+cursorHLCOffset:])
		if next == 0 || next == page {
			break
		}
		page = next
		walked++
	}
	return latestID, found
}

func findTargetLeaf(heap []byte, root uint64, key []byte) (uint64, bool) {
	nbytes := uint64(len(heap))
	npages := nbytes / pageN
	if npages == 0 || root >= npages {
		return 0, false
	}
	page := root
	walked := uint64(0)
	for walked < npages {
		base := page * pageN
		if base+BT_TypeOffset >= nbytes {
			return 0, false
		}
		typ := heap[base+BT_TypeOffset]
		if typ == BT_TypeLeaf {
			return page, true
		}
		if typ != BT_TypeInternal {
			return 0, false
		}
		chosen := bt_internal_child(heap, nbytes, base, key, uint64(len(key)))
		if chosen >= npages || chosen == page {
			return 0, false
		}
		page = chosen
		walked++
	}
	return 0, false
}

func (s *Shard) ensurePack() {
	if s.wal != nil && s.wal.packDepth == 0 {
		s.wal.BeginPack()
	}
}

func (s *Shard) syncWAL() error {
	if s.groupDepth > 0 {
		return nil
	}
	if s.writeDurSet {
		switch s.writeDurPolicy.Mode {
		case CommitImmediate:
			// Durabilité locale requise par cet appel : pointage immédiat,
			// sans attendre ni compteur ni fenêtre.
			if s.unflushed == 0 {
				s.firstUnflushed = time.Now()
			}
			s.unflushed++
			return s.flushWAL()
		case CommitWindowed:
			window := s.writeDurPolicy.Window
			if window <= 0 {
				window = defaultCommitWindow
			}
			if s.unflushed == 0 {
				s.firstUnflushed = time.Now()
			}
			s.unflushed++
			if s.committer != nil {
				s.committer.markWindow(s, window)
				return nil
			}
			if time.Since(s.firstUnflushed) >= window {
				return s.flushWAL()
			}
			return nil
		case CommitReplicated:
			// Durabilité locale non requise : l'acquittement de quorum est
			// attendu par l'appelant après publication.
			if s.unflushed == 0 {
				s.firstUnflushed = time.Now()
			}
			s.unflushed++
			return nil
		}
	}
	if s.unflushed == 0 {
		s.firstUnflushed = time.Now()
	}
	s.unflushed++
	if s.unflushed >= walCoalesceN {
		return s.flushWAL()
	}
	if s.commitMode == CommitReplicated {
		// Durabilité locale non requise : DB.Sync est la barrière explicite.
		return nil
	}
	if s.committer != nil {
		// La borne par compteur n'est pas atteinte : le committer central
		// pointera à l'échéance de la fenêtre, y compris sous inactivité.
		s.committer.mark(s)
		return nil
	}
	// Shard isolé sans committer : la fenêtre est bornée paresseusement au
	// prochain syncWAL. Sous inactivité, le pointage relève de la base
	// (committer central) ou d'un appel explicite.
	if time.Since(s.firstUnflushed) >= s.commitWindow {
		return s.flushWAL()
	}
	return nil
}

// flushWAL pointe le journal du shard (écriture des tampons puis fdatasync) et
// remet à zéro le compteur d'écritures non pointées. L'appelant doit détenir le
// verrou écrivain du shard : le WAL n'est pas réentrant.
func (s *Shard) flushWAL() error {
	if s.wal == nil {
		return unix.EBADF
	}
	if err := s.wal.Flush(); err != nil {
		return err
	}
	s.unflushed = 0
	s.firstUnflushed = time.Time{}
	return nil
}

// syncBarrier force le pointage du shard et attend la fin de la durabilité.
// Utilisé par DB.Sync : le verrou du journal sérialise la barrière avec les
// écritures en cours et avec le committer central.
func (s *Shard) syncBarrier() error {
	if err := s.ready(); err != nil {
		return err
	}
	s.walMu.Lock()
	defer s.walMu.Unlock()
	if s.wal == nil {
		return unix.EBADF
	}
	if s.unflushed == 0 && s.wal.pendingN == 0 && s.wal.qCount == 0 {
		return nil
	}
	return s.flushWAL()
}

func (s *Shard) enterGroup() {
	if s.groupDepth == 0 {
		s.wal.BeginPack()
	}
	s.groupDepth++
}

func (s *Shard) leaveGroup() error {
	if s.groupDepth == 0 {
		return nil
	}
	s.groupDepth--
	if s.groupDepth > 0 {
		return nil
	}
	// Refermeture du pack sans barrière. La transaction a déjà vidé le journal
	// (fdatasync) avant publication ; SyncIfDirty constate alors que tout octet
	// est synchrone et n'émet rien. Pour un lot groupé qui n'a pas encore vidé
	// ses enregistrements (ExecC2QL, groupe de routes), la même barrière scelle
	// le journal une seule fois.
	if s.wal.packDepth > 0 {
		s.wal.EndPackNoFlush()
	}
	if err := s.wal.SyncIfDirty(); err != nil {
		return err
	}
	s.unflushed = 0
	// Matérialisation des pages du tas sans barrière : la durabilité du groupe
	// est déjà scellée par la fdatasync du journal, et le rejeu reconstruit le
	// tas depuis les enregistrements. La barrière data est différée au pointage
	// (checkpointLocked), à la compaction ou à la fermeture, exactement comme
	// pour un PutBatch. Sans cette déferral, un commit transactionnel paierait
	// deux barrières de plus (data et étiquettes) et les écrivains ne
	// soutiendraient pas la mesure sous lecture saturante.
	if _, err := s.pager.FlushDirtyNoBarrier(); err != nil {
		return err
	}
	return nil
}

func (s *Shard) abortGroup() {
	if s.groupDepth > 0 {
		s.groupDepth--
	}
	if s.wal != nil && s.wal.packDepth > 0 {
		s.wal.packDepth--
		s.wal.pending = s.wal.pending[:0]
		s.wal.pendingN = 0
	}
	s.unflushed = 0
}

func (s *Shard) rollbackState(initialHeapRoot, initialHeapUsed, initialWALNext uint64) {
	s.heapRoot = initialHeapRoot
	s.heapUsed = initialHeapUsed
	s.convergeDirtyFromPub()
	if s.wal != nil {
		s.wal.rollbackTo(initialWALNext)
	}
}

func (s *Shard) Checkpoint() error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if s.wal == nil {
		return unix.EBADF
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	return s.checkpointLocked()
}

func (s *Shard) checkpointLocked() error {
	if s.wal == nil {
		return nil
	}
	start := time.Now()
	defer func() {
		d := uint64(time.Since(start).Nanoseconds())
		s.walCheckpoints++
		s.walCheckpointNanos += d
		if d > s.walCheckpointMaxNs {
			s.walCheckpointMaxNs = d
		}
	}()
	if s.pager != nil {
		if _, err := s.pager.FlushDirty(); err != nil {
			return err
		}
	}
	if err := s.wal.Checkpoint(); err != nil {
		return err
	}
	if s.wal.NeedsRecycle() {
		return s.wal.TruncateCheckpoint()
	}
	return nil
}

// WALCheckpointStats retourne le nombre de pointages de journal effectués,
// leur coût cumulé en nanosecondes et le coût du plus long d'entre eux. Ces
// compteurs rendent la cadence de recyclage observable pour la métrologie et
// la qualification.
func (s *Shard) WALCheckpointStats() (count, totalNs, maxNs uint64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.walCheckpoints, s.walCheckpointNanos, s.walCheckpointMaxNs
}

func (s *Shard) Compact() error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if s.wal == nil {
		return unix.EBADF
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	return s.compactLocked()
}

func (s *Shard) compactLocked() error {
	if s.heapUsed > 0 {
		// Le repack manuel consulte la rétention du shard et borne son
		// exécution par deux événements Compaction portant la rétention et la
		// frontière (compteur de mutations), sans modifier la sémantique du
		// repack physique : aucun déport ni élagage n'est décidé ici.
		s.emitEvent(EventCompaction, nil, [16]byte{}, compactionPayload(s.retention, 0), s.counter)
		src := s.pub
		if uint64(len(src)) < s.shardBytes {
			src = s.dirty
		}
		scratch := make([]byte, pageN)
		for p := uint64(0); p < s.heapUsed && p < s.heapPages; p++ {
			pageOff := p * pageN
			pageBuf := src[pageOff : pageOff+pageN]
			if pageBuf[20] == 1 { // TYPE_LEAF
				res := C2db_slot_pack_compact(pageBuf, pageN, scratch, pageN, 16, 1)
				if res.Ok == 1 && res.Bytes_freed > 0 {
					_ = C2db_crc32c_fullpage_store(pageBuf, pageN)
					// La page est mutée sur place : sans ce répertoire, la
					// publication différentielle ne la persisterait jamais.
					s.markDirty(p)
				}
			}
		}
		if err := s.repackHeapInternal(); err != nil {
			return err
		}
		if _, err := s.pager.FlushDirty(); err != nil {
			return err
		}
		if err := s.wal.Checkpoint(); err != nil {
			return err
		}
		s.emitEvent(EventCompaction, nil, [16]byte{}, compactionPayload(s.retention, 1), s.counter)
	}
	return s.wal.Compact()
}

func (s *Shard) tryAutoCompact() (bool, error) {
	if s.autoCompacting || s.wal == nil {
		return false, nil
	}
	s.autoCompacting = true
	defer func() { s.autoCompacting = false }()
	s.emitEvent(EventCompaction, nil, [16]byte{}, compactionPayload(s.retention, 0), s.counter)
	var err error
	switch s.retention {
	case RetentionPruneSuperseded:
		err = s.rebuildLatestLocked()
	case RetentionArchiveSuperseded:
		err = s.archiveSupersededLocked()
	default:
		err = s.rebuildAllVersionsLocked()
	}
	if err != nil {
		return false, err
	}
	s.emitEvent(EventCompaction, nil, [16]byte{}, compactionPayload(s.retention, 1), s.counter)
	if s.pager != nil {
		if _, err := s.pager.FlushDirty(); err != nil {
			return false, err
		}
	}
	if err := s.wal.Checkpoint(); err != nil {
		return false, err
	}
	return true, nil
}

// archiveSupersededLocked déporte vers le segment d'historique toutes les
// versions supplantées du tas publié, puis aplatit le tas chaud en ne
// conservant que la dernière version active par clé. L'ordre est impératif :
// le déport (append + sync) précède l'aplatissement, sans quoi les versions
// déportées seraient perdues si l'écriture d'historique échouait. Pour chaque
// version déportée, un événement Archive est émis ; aucun Prune n'est émis,
// le déport n'étant pas une perte. Le rebâtissage réutilise le filet ordonné
// des feuilles : les versions d'une même clé y sont contiguës et croissantes
// par identifiant, le dernier slot du groupe est donc la version la plus
// récente.
func (s *Shard) archiveSupersededLocked() error {
	if s.history == nil {
		return errHistoryUnavailable
	}
	c := newCursor(s.pub, s.heapRoot, [16]byte{}, s, nil)
	var gKey []byte
	var gIDs, gRaw [][]byte
	var gVLen []uint16

	flushGroup := func() error {
		if len(gIDs) == 0 {
			return nil
		}
		last := len(gIDs) - 1
		limit := len(gIDs)
		if gVLen[last] > 0 {
			limit = last
		}
		for i := 0; i < limit; i++ {
			tomb := gVLen[i] == 0
			var user []byte
			if !tomb {
				v, decErr := decodeValue(s.pub, s.heapPages, gRaw[i])
				if decErr != nil {
					return decErr
				}
				user = v
			}
			rec := HistoryRecord{Key: gKey, Value: user, Tombstone: tomb}
			copy(rec.VersionID[:], gIDs[i])
			if appErr := s.history.Append(rec); appErr != nil {
				return appErr
			}
			s.emitEvent(EventArchive, gKey, rec.VersionID, nil, s.counter)
		}
		gKey = nil
		gIDs = gIDs[:0]
		gRaw = gRaw[:0]
		gVLen = gVLen[:0]
		return nil
	}

	for li := 0; li < len(c.leafPages); li++ {
		n := c.leafSlotCount(li)
		for si := 0; si < n; si++ {
			k, id, raw, _, ok := c.readSlotCell(li, si)
			if !ok || len(id) != BT_IDLen {
				return errInsert
			}
			if gKey == nil || !bytes.Equal(k, gKey) {
				if err := flushGroup(); err != nil {
					return err
				}
				gKey = append([]byte(nil), k...)
			}
			gIDs = append(gIDs, append([]byte(nil), id...))
			gRaw = append(gRaw, append([]byte(nil), raw...))
			gVLen = append(gVLen, uint16(len(raw)))
		}
	}
	if err := flushGroup(); err != nil {
		return err
	}
	// Le déport doit être durable avant l'aplatissement : sans ce sync, une
	// panne entre les deux laisserait les versions supplantées définitivement
	// absentes des deux côtés.
	if err := s.history.Sync(); err != nil {
		return err
	}
	return s.rebuildLatestLocked()
}

func (s *Shard) rebuildLatestLocked() error {
	// Le rebâtissage matérialise une version par clé dans des tranches Go. Sous
	// RetentionPruneSuperseded, il est borné par le budget de matérialisation :
	// au-delà, la reconstruction est refusée (ErrHeapFull) plutôt que de doubler
	// la mémoire du shard. Sous ArchiveSuperseded, cette fonction est la phase
	// d'aplatissement chaud du déport, déjà bornée par le nombre de clés
	// distinctes ; le budget de matérialisation plein ne s'y applique pas.
	budgetOctets := s.rebuildBudget
	if budgetOctets == 0 {
		budgetOctets = defaultRebuildBudget
	}
	bounded := s.retention == RetentionPruneSuperseded
	var total uint64
	var keys, vals, ids [][]byte
	c, err := s.Cursor()
	if err != nil {
		return err
	}
	k, v, ok := c.First()
	for ok {
		id := c.VersionID()
		if len(id) != BT_IDLen {
			_ = c.Close()
			return errInsert
		}
		if bounded && total+uint64(len(k))+uint64(len(v)) > budgetOctets {
			_ = c.Close()
			return ErrHeapFull
		}
		total += uint64(len(k)) + uint64(len(v))
		keys = append(keys, append([]byte(nil), k...))
		vals = append(vals, append([]byte(nil), v...))
		ids = append(ids, append([]byte(nil), id...))
		k, v, ok = c.Next()
	}
	if err := c.Close(); err != nil {
		return err
	}

	// Nombre de versions supersédées qui vont être abandonnées. Il est
	// compté avant la reconstruction, sur les cellules brutes des feuilles,
	// pour être consigné dans l'événement Prune.
	var pruned uint64
	if s.hasEventSink() && s.retention != RetentionArchiveSuperseded {
		if total := s.countAllVersionsLocked(); total > uint64(len(keys)) {
			pruned = total - uint64(len(keys))
		}
	}

	newBuf, err := mmapAnon(int(s.shardBytes))
	if err != nil {
		return err
	}
	st := Db_bt_leaf_init(newBuf, pageN)
	if st.Ok == 0 {
		_ = unix.Munmap(newBuf)
		return errInsert
	}

	savedRoot := s.heapRoot
	savedUsed := s.heapUsed
	savedDirty := s.dirty
	fail := func(err error) error {
		s.heapRoot = savedRoot
		s.heapUsed = savedUsed
		s.dirty = savedDirty
		_ = unix.Munmap(newBuf)
		s.convergeDirtyFromPub()
		return err
	}

	s.dirty = newBuf
	s.heapRoot = 0
	s.heapUsed = 1
	s.stampHeap(newBuf)
	s.markAllDirty()

	savedLast := s.lastID
	savedHasLast := s.hasLast
	src := s.dirty
	for i := range keys {
		cellVal, isOverflow, headPage, numPages, encErr := s.encodeValue(vals[i])
		if encErr != nil {
			return fail(encErr)
		}
		if isOverflow && s.pager != nil {
			for p := headPage; p < headPage+numPages; p++ {
				off := p * pageN
				if putErr := s.pager.PutPage(p*pageLBAs, s.dirty[off:off+pageN]); putErr != nil {
					return fail(putErr)
				}
			}
		}
		if insErr := s.applyInsertVer(src, keys[i], ids[i], cellVal); insErr != nil {
			return fail(insErr)
		}
		src = s.dirty
	}

	if savedHasLast {
		s.lastID = savedLast
		s.hasLast = true
	} else if len(ids) > 0 {
		copy(s.lastID[:], ids[0])
		s.hasLast = true
		for i := 1; i < len(ids); i++ {
			if bytes.Compare(ids[i], s.lastID[:]) > 0 {
				copy(s.lastID[:], ids[i])
			}
		}
	}

	if len(savedDirty) > 0 && (len(s.pub) == 0 || &savedDirty[0] != &s.pub[0]) {
		_ = unix.Munmap(savedDirty)
	}
	if err := s.publish(); err != nil {
		return err
	}
	if pruned > 0 {
		s.emitEvent(EventPrune, nil, [16]byte{}, binary.LittleEndian.AppendUint64(nil, pruned), s.counter)
	}
	return nil
}

// countAllVersionsLocked dénombre les versions présentes dans les feuilles du
// tas publié, sans matérialiser leur charge utile. Il n'est appelé que pour
// renseigner la traçabilité d'élagage, jamais sur le chemin nominal.
func (s *Shard) countAllVersionsLocked() uint64 {
	var total uint64
	c := newCursor(s.pub, s.heapRoot, [16]byte{}, s, nil)
	for li := 0; li < len(c.leafPages); li++ {
		n := c.leafSlotCount(li)
		for si := 0; si < n; si++ {
			if _, id, _, _, ok := c.readSlotCell(li, si); ok && len(id) == BT_IDLen {
				total++
			}
		}
	}
	return total
}

// rebuildAllVersionsLocked reconstruit le tas B-Tree à neuf en réinsérant
// TOUTES les versions stockées dans les feuilles, et non la seule version
// active par clé. C'est le substitut non destructeur de rebuildLatestLocked
// pour les chemins où l'historique as-of doit survivre (rejeu WAL, compaction
// sous pression d'écriture).
//
// L'énumération se fait sur les cellules brutes des pages feuilles
// (leafPages + readSlotCell) et non via Cursor.First/Next, qui ne restitue
// qu'une version par clé et aplatirait l'historique. Le repli intra-feuille
// de applyInsertVer (C2db_slot_pack_compact, drop_tombstones = 0) reste le
// filet local ; cette fonction est le filet global.
//
// L'état lu est celui de s.pub / s.heapRoot courants, sans épinglage d'un
// liveHeap : aucune racine périmée ne peut être reconstruite.
func (s *Shard) rebuildAllVersionsLocked() error {
	// Cette reconstruction publie vers le pager, contrairement au rejeu simple.
	if s.readOnly {
		return ErrRecoveryRequired
	}
	var keys, vals, ids [][]byte
	// Le rebâtissage matérialise toutes les versions ; sur un shard proche de la
	// saturation, cela doublerait la mémoire. Au-delà de ce budget, la
	// reconstruction cède la place au rebâtissage ancré, qui aplatit
	// l'historique mais reste borné.
	budgetOctets := s.rebuildBudget
	if budgetOctets == 0 {
		budgetOctets = defaultRebuildBudget
	}
	var total uint64
	c := newCursor(s.pub, s.heapRoot, [16]byte{}, s, nil)
	for li := 0; li < len(c.leafPages); li++ {
		n := c.leafSlotCount(li)
		for si := 0; si < n; si++ {
			k, id, raw, _, ok := c.readSlotCell(li, si)
			if !ok || len(id) != BT_IDLen {
				return errInsert
			}
			taille := uint64(len(raw))
			if isOfl, _, _ := checkOverflowDesc(raw); isOfl && len(raw) >= 5 {
				taille = uint64(binary.LittleEndian.Uint32(raw[1:5]))
			}
			if total+taille > budgetOctets {
				switch s.retention {
				case RetentionPruneSuperseded:
					s.installLive(s.pub)
					return s.rebuildLatestLocked()
				case RetentionArchiveSuperseded:
					// Le filet de rejeu ne doit pas échouer fermé sous
					// ArchiveSuperseded : le déport vers le segment
					// d'historique précède l'aplatissement du tas chaud, si
					// bien qu'aucune version n'est perdue. La garde de
					// réentrance est posée avant l'appel, comme dans
					// tryAutoCompact, pour interdire une reconstruction
					// imbriquée via allocOverflowPages.
					prevAuto := s.autoCompacting
					s.autoCompacting = true
					err := s.archiveSupersededLocked()
					s.autoCompacting = prevAuto
					return err
				default:
					return ErrHeapFull
				}
			}
			total += taille
			user, decErr := decodeValue(s.pub, s.heapPages, raw)
			if decErr != nil {
				return decErr
			}
			keys = append(keys, append([]byte(nil), k...))
			vals = append(vals, append([]byte(nil), user...))
			ids = append(ids, append([]byte(nil), id...))
		}
	}
	if len(keys) == 0 {
		return nil
	}

	// Garde de réentrance : encodeValue -> allocOverflowPages appelle
	// tryAutoCompact ; l'interdire pendant le rebâtissage évite une
	// reconstruction imbriquée sur un tampon en cours de bascule.
	prevAuto := s.autoCompacting
	s.autoCompacting = true
	defer func() { s.autoCompacting = prevAuto }()

	newBuf, err := mmapAnon(int(s.shardBytes))
	if err != nil {
		return err
	}
	st := Db_bt_leaf_init(newBuf, pageN)
	if st.Ok == 0 {
		_ = unix.Munmap(newBuf)
		return errInsert
	}

	savedRoot := s.heapRoot
	savedUsed := s.heapUsed
	savedDirty := s.dirty
	fail := func(err error) error {
		s.heapRoot = savedRoot
		s.heapUsed = savedUsed
		s.dirty = savedDirty
		_ = unix.Munmap(newBuf)
		s.convergeDirtyFromPub()
		return err
	}

	s.dirty = newBuf
	s.heapRoot = 0
	s.heapUsed = 1
	s.stampHeap(newBuf)
	s.markAllDirty()

	savedLast := s.lastID
	savedHasLast := s.hasLast
	src := s.dirty
	for i := range keys {
		cellVal, isOverflow, headPage, numPages, encErr := s.encodeValue(vals[i])
		if encErr != nil {
			return fail(encErr)
		}
		if isOverflow && s.pager != nil {
			for p := headPage; p < headPage+numPages; p++ {
				off := p * pageN
				if putErr := s.pager.PutPage(p*pageLBAs, s.dirty[off:off+pageN]); putErr != nil {
					return fail(putErr)
				}
			}
		}
		if insErr := s.applyInsertVer(src, keys[i], ids[i], cellVal); insErr != nil {
			return fail(insErr)
		}
		src = s.dirty
	}

	if savedHasLast {
		s.lastID = savedLast
		s.hasLast = true
	} else if len(ids) > 0 {
		copy(s.lastID[:], ids[0])
		s.hasLast = true
		for i := 1; i < len(ids); i++ {
			if bytes.Compare(ids[i], s.lastID[:]) > 0 {
				copy(s.lastID[:], ids[i])
			}
		}
	}

	if len(savedDirty) > 0 && (len(s.pub) == 0 || &savedDirty[0] != &s.pub[0]) {
		_ = unix.Munmap(savedDirty)
	}
	return s.publish()
}

func (s *Shard) retryAfterHeapFull(key []byte, err error, op func() error) error {
	// Seul l'épuisement réel du tas (ErrHeapFull) et l'échec structurel de
	// l'arbre (ErrTreeFull) autorisent la reconstruction bornée : la compaction
	// libère des pages ou rebalance l'arbre. errInsert (validation ou format) ne
	// la déclenche jamais.
	if !errors.Is(err, ErrHeapFull) && !errors.Is(err, ErrTreeFull) {
		return err
	}
	ok, compactErr := s.tryAutoCompact()
	if compactErr != nil {
		if errors.Is(compactErr, ErrHeapFull) {
			s.emitEvent(EventRefusal, key, [16]byte{}, nil, s.counter)
		}
		return compactErr
	}
	if !ok {
		if errors.Is(err, ErrHeapFull) {
			s.emitEvent(EventRefusal, key, [16]byte{}, nil, s.counter)
		}
		return err
	}
	ret := op()
	if errors.Is(ret, ErrHeapFull) {
		s.emitEvent(EventRefusal, key, [16]byte{}, nil, s.counter)
	}
	return ret
}

// appliquerAvecRepackRejeu est le filet de rejeu : il reproduit le repack du
// chemin d'écriture quand une opération de rejeu manque d'espace. Deux points
// durs le distinguent du filet d'écriture :
//
//   - il ancre la compaction sur l'état vivant du rejeu (s.pub, s.heapRoot) ;
//     la reconstruction lit directement s.pub/s.heapRoot et n'épingle aucun
//     liveHeap, donc aucune racine périmée ne peut être reconstruite ;
//   - il n'appelle aucun pointage ni troncature du WAL, car tronquer le journal
//     avant la fin du rejeu laisserait un tas partiel si la suite échouait.
func (s *Shard) appliquerAvecRepackRejeu(op func() error) error {
	err := op()
	if err == nil || (!errors.Is(err, ErrHeapFull) && !errors.Is(err, ErrTreeFull) && !errors.Is(err, errInsert)) {
		return err
	}
	if rebErr := s.rebuildAllVersionsLocked(); rebErr != nil {
		return rebErr
	}
	return op()
}

func (s *Shard) RepackHeap() error {
	if s != nil && s.readOnly {
		return ErrReadOnly
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.lockWriter(); err != nil {
		return err
	}
	defer s.unlockWriter()
	// Le repack manuel de pages est encadré par deux événements Compaction
	// portant la rétention et la frontière, sans changer la sémantique du
	// repack : seul le placement physique des pages est modifié.
	s.emitEvent(EventCompaction, nil, [16]byte{}, compactionPayload(s.retention, 0), s.counter)
	if err := s.repackHeapInternal(); err != nil {
		return err
	}
	if _, err := s.pager.FlushDirty(); err != nil {
		return err
	}
	if s.wal != nil {
		if err := s.wal.Checkpoint(); err != nil {
			return err
		}
	}
	s.emitEvent(EventCompaction, nil, [16]byte{}, compactionPayload(s.retention, 1), s.counter)
	return nil
}

func (s *Shard) markActivePages(mask []uint64) {
	if s.heapUsed == 0 || s.heapRoot >= s.heapPages {
		return
	}
	src := s.pub
	if uint64(len(src)) < s.shardBytes {
		src = s.dirty
	}
	if uint64(len(src)) < s.shardBytes {
		return
	}
	if len(mask) > 0 {
		mask[0] |= 1
	}

	stack := []uint64{s.heapRoot}
	for len(stack) > 0 {
		pg := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if pg >= s.heapPages || pg >= s.heapUsed {
			continue
		}
		word := pg / 64
		bit := pg % 64
		if uint64(len(mask)) > word && mask[word]&(1<<bit) != 0 && pg != s.heapRoot {
			continue
		}
		if uint64(len(mask)) > word {
			mask[word] |= (1 << bit)
		}

		off := pg * pageN
		pageBuf := src[off : off+pageN]
		typ := pageBuf[20]
		if typ == 2 { // TYPE_INTERNAL
			leftmost := binary.LittleEndian.Uint64(pageBuf[8:16])
			if leftmost < s.heapPages && leftmost > 0 {
				stack = append(stack, leftmost)
			}
			nslots := binary.LittleEndian.Uint16(pageBuf[22:24])
			for i := uint16(0); i < nslots; i++ {
				slotOff := 64 + i*2
				if int(slotOff+2) > len(pageBuf) {
					break
				}
				cellOff := binary.LittleEndian.Uint16(pageBuf[slotOff : slotOff+2])
				if int(cellOff+2) > len(pageBuf) {
					continue
				}
				cklen := binary.LittleEndian.Uint16(pageBuf[cellOff : cellOff+2])
				childOff := int(cellOff) + 4 + int(cklen)
				if childOff+8 <= len(pageBuf) {
					child := binary.LittleEndian.Uint64(pageBuf[childOff : childOff+8])
					if child < s.heapPages && child > 0 {
						stack = append(stack, child)
					}
				}
			}
		} else if typ == 1 { // TYPE_LEAF
			nextLeaf := binary.LittleEndian.Uint64(pageBuf[8:16])
			if nextLeaf < s.heapPages && nextLeaf > 0 {
				stack = append(stack, nextLeaf)
			}
			nslots := binary.LittleEndian.Uint16(pageBuf[22:24])
			for i := uint16(0); i < nslots; i++ {
				slotOff := 64 + i*2
				if int(slotOff+2) > len(pageBuf) {
					break
				}
				cellOff := binary.LittleEndian.Uint16(pageBuf[slotOff : slotOff+2])
				if int(cellOff+4) > len(pageBuf) {
					continue
				}
				cklen := binary.LittleEndian.Uint16(pageBuf[cellOff : cellOff+2])
				cvlen := binary.LittleEndian.Uint16(pageBuf[cellOff+2 : cellOff+4])
				valOff := int(cellOff) + 4 + int(cklen) + 16
				if cvlen == OverflowDescLen && valOff+OverflowDescLen <= len(pageBuf) {
					if pageBuf[valOff] == cellKindOverflow {
						tLen := binary.LittleEndian.Uint32(pageBuf[valOff+1 : valOff+5])
						hPage := binary.LittleEndian.Uint64(pageBuf[valOff+5 : valOff+13])
						maxPages := uint64((uint64(tLen) + OverflowChunkCapacity - 1) / OverflowChunkCapacity)
						curr := hPage
						walked := uint64(0)
						for curr != 0 && curr < s.heapPages && walked <= maxPages {
							word := curr / 64
							bit := curr % 64
							if uint64(len(mask)) > word {
								mask[word] |= (1 << bit)
							}
							nextOff := curr * pageN
							if nextOff+40 > uint64(len(src)) {
								break
							}
							nextP := binary.LittleEndian.Uint64(src[nextOff+32 : nextOff+40])
							walked++
							curr = nextP
						}
					}
				}
			}
		}
	}
}

func (s *Shard) repackHeapInternal() error {
	if s.heapUsed <= 1 {
		return nil
	}
	src := s.pub
	if uint64(len(src)) < s.shardBytes {
		src = s.dirty
	}
	if uint64(len(src)) < s.shardBytes {
		return nil
	}

	activeMask := make([]uint64, len(s.dirtyMask))
	s.markActivePages(activeMask)

	var activeCount uint64
	for w := range activeMask {
		activeCount += uint64(bits.OnesCount64(activeMask[w]))
	}
	if activeCount >= s.heapUsed {
		return nil
	}

	newBuf, err := mmapAnon(int(s.shardBytes))
	if err != nil {
		return err
	}

	mapping := make(map[uint64]uint64, activeCount)
	newIdx := uint64(0)
	if len(activeMask) > 0 && activeMask[0]&1 != 0 {
		mapping[0] = 0
		newIdx = 1
	}

	for w := range activeMask {
		word := activeMask[w]
		for word != 0 {
			bit := bits.TrailingZeros64(word)
			oldPg := uint64(w*64 + bit)
			if oldPg != 0 && oldPg < s.heapUsed && oldPg < s.heapPages {
				mapping[oldPg] = newIdx
				newIdx++
			}
			word &= word - 1
		}
	}

	for oldPg, dstPg := range mapping {
		srcOff := oldPg * pageN
		dstOff := dstPg * pageN
		copy(newBuf[dstOff:dstOff+pageN], src[srcOff:srcOff+pageN])
		pageBuf := newBuf[dstOff : dstOff+pageN]

		if pageBuf[20] == 2 { // TYPE_INTERNAL
			leftmost := binary.LittleEndian.Uint64(pageBuf[8:16])
			if newLeft, ok := mapping[leftmost]; ok {
				binary.LittleEndian.PutUint64(pageBuf[8:16], newLeft)
			}
			nslots := binary.LittleEndian.Uint16(pageBuf[22:24])
			for i := uint16(0); i < nslots; i++ {
				slotOff := 64 + i*2
				if int(slotOff+2) > len(pageBuf) {
					break
				}
				cellOff := binary.LittleEndian.Uint16(pageBuf[slotOff : slotOff+2])
				if int(cellOff+2) > len(pageBuf) {
					continue
				}
				cklen := binary.LittleEndian.Uint16(pageBuf[cellOff : cellOff+2])
				childOff := int(cellOff) + 4 + int(cklen)
				if childOff+8 <= len(pageBuf) {
					child := binary.LittleEndian.Uint64(pageBuf[childOff : childOff+8])
					if newChild, ok := mapping[child]; ok {
						binary.LittleEndian.PutUint64(pageBuf[childOff:childOff+8], newChild)
					}
				}
			}
			_ = C2db_crc32c_fullpage_store(pageBuf, pageN)
		} else if pageBuf[20] == 1 { // TYPE_LEAF
			nextLeaf := binary.LittleEndian.Uint64(pageBuf[8:16])
			if nextLeaf > 0 {
				if newNextLeaf, ok := mapping[nextLeaf]; ok {
					binary.LittleEndian.PutUint64(pageBuf[8:16], newNextLeaf)
				}
			}
			nslots := binary.LittleEndian.Uint16(pageBuf[22:24])
			for i := uint16(0); i < nslots; i++ {
				slotOff := 64 + i*2
				if int(slotOff+2) > len(pageBuf) {
					break
				}
				cellOff := binary.LittleEndian.Uint16(pageBuf[slotOff : slotOff+2])
				if int(cellOff+4) > len(pageBuf) {
					continue
				}
				cklen := binary.LittleEndian.Uint16(pageBuf[cellOff : cellOff+2])
				cvlen := binary.LittleEndian.Uint16(pageBuf[cellOff+2 : cellOff+4])
				valOff := int(cellOff) + 4 + int(cklen) + 16
				if cvlen == OverflowDescLen && valOff+OverflowDescLen <= len(pageBuf) {
					if pageBuf[valOff] == cellKindOverflow {
						hPage := binary.LittleEndian.Uint64(pageBuf[valOff+5 : valOff+13])
						if newHPage, ok := mapping[hPage]; ok {
							binary.LittleEndian.PutUint64(pageBuf[valOff+5:valOff+13], newHPage)
						}
					}
				}
			}
			_ = C2db_crc32c_fullpage_store(pageBuf, pageN)
		} else if pageBuf[20] == TypeOverflow { // TYPE_OVERFLOW
			nextPage := binary.LittleEndian.Uint64(pageBuf[32:40])
			if nextPage > 0 {
				if newNext, ok := mapping[nextPage]; ok {
					binary.LittleEndian.PutUint64(pageBuf[32:40], newNext)
				}
			}
			_ = C2db_crc32c_fullpage_store(pageBuf, pageN)
		}
	}

	newRoot := s.heapRoot
	if nr, ok := mapping[s.heapRoot]; ok {
		newRoot = nr
	}

	s.heapRoot = newRoot
	s.heapUsed = newIdx
	if s.heapUsed < 1 {
		s.heapUsed = 1
	}
	s.stampHeap(newBuf)
	_ = C2db_crc32c_fullpage_store(newBuf[:pageN], pageN)

	if len(s.dirty) > 0 && &s.dirty[0] != &s.pub[0] {
		_ = unix.Munmap(s.dirty)
	}
	s.dirty = newBuf

	s.markAllDirty()

	return s.publish()
}

// syncPubFromDirty recopie vers pub les seules pages divergentes répertoriées
// par markDirty (dirtyList en régime nominal, dirtyMask sous dirtyOverflow),
// puis remet ces marques à zéro. C'est l'exact symétrique de
// convergeDirtyFromPub. Coût O(pages touchées par l'enregistrement) et non
// O(heapUsed) : le rejeu d'un journal cesse d'être quadratique. La sémantique
// est inchangée, les pages non marquées étant déjà égales entre pub et dirty.
//
// L'union des pages réellement copiées est accumulée dans replayMask : la
// publication terminale du rejeu (publish) aura besoin de cet ensemble après la
// remise à zéro, sinon elle ne transférerait aucune page reconstruite au pager.
func (s *Shard) syncPubFromDirty() {
	if uint64(len(s.pub)) < s.shardBytes || uint64(len(s.dirty)) < s.shardBytes {
		return
	}
	if s.dirtyOverflow {
		for w := range s.dirtyMask {
			word := s.dirtyMask[w]
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				off := uint64(w*64+bit) * pageN
				if off+pageN <= uint64(len(s.pub)) && off+pageN <= uint64(len(s.dirty)) {
					copy(s.pub[off:off+pageN], s.dirty[off:off+pageN])
					if w < len(s.replayMask) {
						s.replayMask[w] |= uint64(1) << uint(bit)
					}
					s.ReplaySyncPagesCopied++
				}
				word &= word - 1
			}
			s.dirtyMask[w] = 0
		}
		s.dirtyOverflow = false
		s.dirtyListLen = 0
		return
	}
	for _, pg32 := range s.dirtyList[:s.dirtyListLen] {
		pg := uint64(pg32)
		s.dirtyMask[pg/64] &^= uint64(1) << (pg % 64)
		if off := pg * pageN; off+pageN <= uint64(len(s.pub)) && off+pageN <= uint64(len(s.dirty)) {
			copy(s.pub[off:off+pageN], s.dirty[off:off+pageN])
			if pg/64 < uint64(len(s.replayMask)) {
				s.replayMask[pg/64] |= uint64(1) << (pg % 64)
			}
			s.ReplaySyncPagesCopied++
		}
	}
	s.dirtyListLen = 0
}

// commitReplayDirty reporte l'union des pages divergentes accumulée pendant le
// rejeu (replayMask) vers l'ensemble de publication (dirtyMask), après que
// chaque syncPubFromDirty a remis à zéro l'ensemble différentiel. La publication
// terminale (publish) transfère ainsi au pager exactement les pages
// reconstruites, sans balayer le tas.
func (s *Shard) commitReplayDirty() {
	if s.dirtyListLen != 0 || s.dirtyOverflow {
		// Résidu non publié : le reporter d'abord vers pub, comme le ferait la
		// dernière synchronisation de l'enregistrement précédent.
		s.syncPubFromDirty()
	}
	var any bool
	for w := range s.replayMask {
		if s.replayMask[w] == 0 {
			continue
		}
		if w < len(s.dirtyMask) {
			s.dirtyMask[w] |= s.replayMask[w]
		}
		s.replayMask[w] = 0
		any = true
	}
	if any {
		s.dirtyOverflow = true
		s.dirtyListLen = 0
	}
}

// markDirty répertorie une page divergente du tampon dirty vis-à-vis de pub.
// O(1) et sans allocation : le bitmap déduplique, la liste dense préallouée
// à l'ouverture ordonne la publication. Quand la liste sature (rafale
// massive), le drapeau dirtyOverflow bascule la publication sur le balayage
// des mots du bitmap, sans jamais comparer le contenu des pages.
func (s *Shard) markDirty(pg uint64) {
	if pg >= s.heapPages {
		return
	}
	w := pg / 64
	if w >= uint64(len(s.dirtyMask)) {
		return
	}
	bit := uint64(1) << (pg % 64)
	if s.dirtyMask[w]&bit != 0 {
		return
	}
	s.dirtyMask[w] |= bit
	if s.dirtyListLen < len(s.dirtyList) {
		s.dirtyList[s.dirtyListLen] = uint32(pg)
		s.dirtyListLen++
	} else {
		s.dirtyOverflow = true
	}
}

// markAllDirty répertorie l'intégralité du tas utilisé (maintenance repack).
// La liste dense saturerait d'office : le repli bitmap est posé d'emblée et
// la liste est réarmée pour ne pas rejouer des entrées périmées.
func (s *Shard) markAllDirty() {
	used := s.heapUsed
	if used < 1 {
		used = 1
	}
	if used > s.heapPages {
		used = s.heapPages
	}
	full := used / 64
	for w := uint64(0); w < full && w < uint64(len(s.dirtyMask)); w++ {
		s.dirtyMask[w] = ^uint64(0)
	}
	if r := used % 64; r != 0 && full < uint64(len(s.dirtyMask)) {
		s.dirtyMask[full] |= ^uint64(0) >> (64 - r)
	}
	s.dirtyListLen = 0
	s.dirtyOverflow = true
}

// markWritePath répertorie le chemin racine→feuille que l'opération B-Tree va
// copier sur écriture, y compris les pages d'index au-delà de la page 63 que
// le masque de 64 bits retourné par le noyau transpilé ne peut pas désigner.
// Coût borné par la profondeur de l'arbre : la suppression s'arrête à la
// feuille cible depuis l'arrêt du parcours de chaîne dans db_bt_del_heap, il
// n'y a plus de chaîne de feuilles à répertorier. Zéro allocation : seules des
// têtes de pages sont lues, via les mêmes assistants que le noyau.
func (s *Shard) markWritePath(src []byte, key []byte) {
	if uint64(len(src)) < s.shardBytes || len(key) == 0 {
		return
	}
	nbytes := uint64(len(src))
	root := s.heapRoot
	if root >= s.heapPages || root >= s.heapUsed {
		return
	}
	s.markDirty(root)
	pg := root
	for d := 0; d <= maxBTreeDepth; d++ {
		base := pg * pageN
		if base+BT_TypeOffset+1 > nbytes {
			return
		}
		if src[base+BT_TypeOffset] != BT_TypeInternal {
			break
		}
		child := bt_internal_child(src, nbytes, base, key, uint64(len(key)))
		if child >= s.heapPages || child >= s.heapUsed || child == pg {
			return
		}
		s.markDirty(child)
		pg = child
	}
}

// convergeDirtyFromPub réaligne le tampon dirty sur pub pour les seules pages
// de l'ensemble dirty, puis réarme l'ensemble. O(D), zéro allocation.
// Chemin chaud : réutilisation de l'ancien tampon pub (aucun lecteur épinglé),
// restauration après échec, annulation de transaction. L'ensemble reflète
// exactement la divergence dirty/pub depuis la dernière convergence (invariant
// maintenu par markDirty) : un ensemble vide signifie qu'il n'y a rien à
// réaligner, sans repli coûteux.
func (s *Shard) convergeDirtyFromPub() {
	if uint64(len(s.pub)) < s.shardBytes || uint64(len(s.dirty)) < s.shardBytes {
		return
	}
	if s.dirtyOverflow {
		for w := range s.dirtyMask {
			word := s.dirtyMask[w]
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				off := uint64(w*64+bit) * pageN
				if off+pageN <= uint64(len(s.pub)) && off+pageN <= uint64(len(s.dirty)) {
					copy(s.dirty[off:off+pageN], s.pub[off:off+pageN])
				}
				word &= word - 1
			}
			s.dirtyMask[w] = 0
		}
		s.dirtyOverflow = false
		s.dirtyListLen = 0
		return
	}
	for _, pg32 := range s.dirtyList[:s.dirtyListLen] {
		pg := uint64(pg32)
		s.dirtyMask[pg/64] &^= uint64(1) << (pg % 64)
		if off := pg * pageN; off+pageN <= uint64(len(s.pub)) && off+pageN <= uint64(len(s.dirty)) {
			copy(s.dirty[off:off+pageN], s.pub[off:off+pageN])
		}
	}
	s.dirtyListLen = 0
}

func (s *Shard) recordDirtyPages(hst, st Db_bt_heap_st_t) {
	// L'en-tête de la page 0 (racine, used, génération) est réestampillée à
	// chaque adoption : elle diverge donc systématiquement.
	s.markDirty(0)
	s.markDirty(st.Root)
	s.markDirty(hst.Root)
	if len(s.dirtyMask) > 0 {
		pages := st.Pages
		for pages != 0 {
			b := bits.TrailingZeros64(pages)
			s.markDirty(uint64(b))
			pages &= pages - 1
		}
	}
	for p := hst.Used; p <= st.Used && p < s.heapPages; p++ {
		s.markDirty(p)
	}
}

func (s *Shard) syncDirtyFromPub() {
	used := s.heapUsed
	if used < 1 {
		used = 1
	}
	if used > s.heapPages {
		used = s.heapPages
	}
	if uint64(len(s.pub)) < used*pageN || uint64(len(s.dirty)) < used*pageN {
		return
	}

	var hasDirty bool
	for w := range s.dirtyMask {
		word := s.dirtyMask[w]
		if word != 0 {
			hasDirty = true
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				pageIdx := uint64(w*64 + bit)
				if pageIdx < used {
					off := pageIdx * pageN
					copy(s.dirty[off:off+pageN], s.pub[off:off+pageN])
				}
				word &= word - 1
			}
			s.dirtyMask[w] = 0
		}
	}

	if !hasDirty {
		for i := uint64(0); i < used; i++ {
			off := i * pageN
			copy(s.dirty[off:off+pageN], s.pub[off:off+pageN])
		}
	}
}

func (s *Shard) syncDirtyFull() {
	used := s.heapUsed
	if used < 1 {
		used = 1
	}
	if used > s.heapPages {
		used = s.heapPages
	}
	if uint64(len(s.pub)) < used*pageN || uint64(len(s.dirty)) < used*pageN {
		return
	}
	for i := uint64(0); i < used; i++ {
		off := i * pageN
		copy(s.dirty[off:off+pageN], s.pub[off:off+pageN])
	}
	for w := range s.dirtyMask {
		s.dirtyMask[w] = 0
	}
	s.dirtyListLen = 0
	s.dirtyOverflow = false
}

const (
	txFlagStandalone    byte = 0
	txFlagTransactional byte = 1
)

func packKV(key, val []byte) []byte {
	return packKVWithFlag(txFlagStandalone, key, val)
}

func packTxKV(key, val []byte) []byte {
	return packKVWithFlag(txFlagTransactional, key, val)
}

func packKVWithFlag(flag byte, key, val []byte) []byte {
	if len(key) > 0xFFFF || len(val) > 0xFFFF {
		panic("c2db: packKV payload exceeds 65535 bytes")
	}
	p := make([]byte, 1+4+len(key)+len(val))
	p[0] = flag
	binary.LittleEndian.PutUint16(p[1:3], uint16(len(key)))
	binary.LittleEndian.PutUint16(p[3:5], uint16(len(val)))
	copy(p[5:], key)
	copy(p[5+len(key):], val)
	return p
}

func unpackKV(p []byte) (flag byte, key, val []byte, ok bool) {
	if len(p) < 5 {
		if len(p) >= 4 {
			klen := int(binary.LittleEndian.Uint16(p[0:2]))
			vlen := int(binary.LittleEndian.Uint16(p[2:4]))
			if len(p)-4 >= klen+vlen {
				return txFlagStandalone, p[4 : 4+klen], p[4+klen : 4+klen+vlen], true
			}
		}
		return 0, nil, nil, false
	}
	flag = p[0]
	klen := int(binary.LittleEndian.Uint16(p[1:3]))
	vlen := int(binary.LittleEndian.Uint16(p[3:5]))
	rest := len(p) - 5
	if klen > rest || vlen > rest-klen {
		return 0, nil, nil, false
	}
	return flag, p[5 : 5+klen], p[5+klen : 5+klen+vlen], true
}

func openReadOnlyDevice(path string, size uint64) (*Device, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECT|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if st.Size <= 0 || uint64(st.Size) != size {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("c2db: file %s size %d, expected %d", path, st.Size, size)
	}
	return &Device{fd: fd, size: size}, nil
}

func openOrCreateDevice(path string, size uint64) (*Device, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return Create(path, size)
	}
	if err != nil {
		return nil, err
	}
	return Open(path)
}

func openOrCreateWAL(path string, size uint64, key [32]byte) (*WAL, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return CreateWAL(path, size, key)
	}
	if err != nil {
		return nil, err
	}
	return OpenWAL(path, key)
}

func mmapAnon(n int) ([]byte, error) {
	return unix.Mmap(-1, 0, n, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
}

// ensureDirty garantit la présence du tampon de travail dirty à sa taille
// nominale. En régime écrivain, le tampon est alloué à l'ouverture : l'appel
// est un simple contrôle de longueur. En lecture seule, il n'est alloué qu'au
// premier rejeu effectif, ce qui évite une réserve de 1 Gio et une recopie
// intégrale pour les vues dont le journal est déjà pointé.
func (s *Shard) ensureDirty() error {
	if uint64(len(s.dirty)) >= s.shardBytes {
		return nil
	}
	buf, err := mmapAnon(int(s.shardBytes))
	if err != nil {
		return err
	}
	s.dirty = buf
	return nil
}

func (s *Shard) applyDelete(src, key []byte) error {
	// Filet local : une invocation hors des chemins gardés ne doit jamais
	// déréférencer un tampon de travail absent.
	if err := s.ensureDirty(); err != nil {
		return err
	}
	// Lire les chaînes de débordement AVANT la suppression physique : le noyau
	// retire toutes les cellules de la feuille cible portant la clé, effaçant
	// leurs descripteurs. Les chaînes lues deviennent alors orphelines et sont
	// consignées mortes après le succès de la suppression.
	chains := s.collectOverflowChainsForDelete(src, key)
	hst := s.heapState()
	s.markWritePath(src, key)
	st := Db_bt_del_heap(src, s.dirty, s.shardBytes, s.heapPages, &hst, key, uint64(len(key)))
	if st.Ok != 0 {
		s.recordDirtyPages(hst, st)
		s.btDelCowPages.Add(uint64(bits.OnesCount64(st.Pages)))
		s.adoptHeap(st)
		s.retireDeletedOverflow(chains)
		return nil
	}
	s.convergeDirtyFromPub()
	if hst.Used >= s.heapPages || s.heapUsed >= s.heapPages || s.heapUsed >= s.heapWatermark {
		return ErrHeapFull
	}
	return errInsert
}

func (s *Shard) applyInsertVer(src, key, id, val []byte) error {
	// Filet local : une invocation hors des chemins gardés ne doit jamais
	// déréférencer un tampon de travail absent.
	if err := s.ensureDirty(); err != nil {
		return err
	}
	if len(key) == 0 || len(key) > 0xFFFF || len(val) > 0xFFFF {
		return errInsert
	}
	if s.heapUsed >= s.heapWatermark {
		var exists bool
		if h := s.pinLive(); h != nil {
			var snap [16]byte
			for i := range snap {
				snap[i] = 0xFF
			}
			probe := Db_bt_get_as_of_heap(h.buf, uint64(len(h.buf)), uint64(len(h.buf))/pageN, h.root, key, uint64(len(key)), snap[:], nil, 0)
			exists = probe.Found != 0
			s.unpinLive(h)
		}
		if !exists && src != nil {
			var snap [16]byte
			for i := range snap {
				snap[i] = 0xFF
			}
			probe := Db_bt_get_as_of_heap(src, s.shardBytes, s.heapPages, s.heapRoot, key, uint64(len(key)), snap[:], nil, 0)
			exists = probe.Found != 0
		}
		if !exists {
			return ErrHeapFull
		}
	}
	hst := s.heapState()
	s.markWritePath(src, key)
	// RetentionPruneSuperseded : la réécriture à valeur identique remplace la
	// cellule dans la feuille au lieu d'empiler une version. Les autres
	// rétentions (KeepAll, ArchiveSuperseded) conservent l'empilement intégral.
	prune := uint64(0)
	if s.retention == RetentionPruneSuperseded {
		prune = 1
	}
	st := Db_bt_insert_ver_heap_prune(src, s.dirty, s.shardBytes, s.heapPages, &hst, key, uint64(len(key)), id, val, uint64(len(val)), prune)
	if st.Ok != 0 {
		s.recordDirtyPages(hst, st)
		s.adoptHeap(st)
		return nil
	}
	if st.Full != 0 {
		// Le noyau n'a pas pu scinder un nœud interne faute de page libre.
		// La classification sépare l'épuisement réel du tas (ErrHeapFull, filet
		// et Refusal inchangés) de l'échec structurel « arbre plein »
		// (ErrTreeFull), distinct de errInsert et non rejoué par le filet.
		s.convergeDirtyFromPub()
		return s.classifyInsertFull(hst)
	}
	// Si l'insertion a échoué par fragmentation locale dans la feuille cible,
	// défragmenter EXCLUSIVEMENT la feuille cible en O(1) sans balayer tout le tas.
	// L'opération s'exécute avec drop_tombstones = 0 pour préserver strictement
	// l'intégrité de toutes les versions historiques visibles par requêtes as-of.
	if targetLeaf, ok := findTargetLeaf(s.dirty, s.heapRoot, key); ok && targetLeaf < s.heapPages {
		pageOff := targetLeaf * pageN
		if pageOff+pageN <= uint64(len(s.dirty)) {
			scratch := make([]byte, pageN)
			pageBuf := s.dirty[pageOff : pageOff+pageN]
			res := C2db_slot_pack_compact(pageBuf, pageN, scratch, pageN, BT_IDLen, 0)
			if res.Ok == 1 && res.Bytes_freed > 0 {
				_ = C2db_crc32c_fullpage_store(pageBuf, pageN)
				s.markDirty(targetLeaf)
				hst = s.heapState()
				st = Db_bt_insert_ver_heap_prune(src, s.dirty, s.shardBytes, s.heapPages, &hst, key, uint64(len(key)), id, val, uint64(len(val)), prune)
				if st.Ok != 0 {
					s.recordDirtyPages(hst, st)
					s.adoptHeap(st)
					return nil
				}
			}
		}
	}
	s.convergeDirtyFromPub()
	if hst.Used >= s.heapPages || s.heapUsed >= s.heapPages || s.heapUsed >= s.heapWatermark {
		return ErrHeapFull
	}
	// Échec structurel du noyau (scission profonde impossible alors que le tas a
	// de la place) : classé « arbre plein », distinct d'errInsert (validation ou
	// format). La reconstruction bornée le rebalance.
	return ErrTreeFull
}

func (s *Shard) heapState() Db_bt_heap_st_t {
	used := s.heapUsed
	if used < 1 {
		used = 1
	}
	return Db_bt_heap_st_t{Root: s.heapRoot, Used: used, Ok: 1}
}

// classifyInsertFull traduit l'état full du noyau (scission interne impossible
// faute de page) en erreur du moteur. Si le tas n'a plus de quoi loger la paire
// de pages d'une scission, l'échec est un épuisement du tas (ErrHeapFull, filet
// et Refusal inchangés) ; s'il reste de la place mais que la structure a refusé
// de croître, l'échec est un « arbre plein » structurel (ErrTreeFull), distinct
// de errInsert et non rejoué par le filet.
func (s *Shard) classifyInsertFull(hst Db_bt_heap_st_t) error {
	if hst.Used >= s.heapPages || s.heapUsed >= s.heapPages ||
		hst.Used+2 > s.heapPages || s.heapUsed+2 > s.heapPages ||
		s.heapUsed >= s.heapWatermark {
		return ErrHeapFull
	}
	return ErrTreeFull
}

func (s *Shard) stampHeap(buf []byte) {
	if len(buf) < heapGenOff+8 {
		return
	}
	binary.LittleEndian.PutUint64(buf[heapRootOff:heapRootOff+8], s.heapRoot)
	binary.LittleEndian.PutUint64(buf[heapUsedOff:heapUsedOff+8], s.heapUsed)
	binary.LittleEndian.PutUint64(buf[heapGenOff:heapGenOff+8], s.heapGen)
}

func (s *Shard) adoptHeap(st Db_bt_heap_st_t) {
	s.heapRoot = st.Root
	s.heapUsed = st.Used
	if s.heapUsed < 1 {
		s.heapUsed = 1
	}
	s.stampHeap(s.dirty)
}

func (s *Shard) SetStrictOpen(strict bool) {
	s.strictOpen = strict
}

func (s *Shard) PagesSkipped() uint64 {
	if s == nil {
		return 0
	}
	return s.pagesSkipped
}

func (s *Shard) loadHeap() error {
	pg, err := s.pager.GetPage(0)
	if err != nil && !errors.Is(err, errPageSeal) {
		return err
	}
	if err != nil {
		s.pagesSkipped++
		probeEmit(s.id, ProbeOpPageSkip, 0, 0, 0, 0, "page 0 seal mismatch: "+err.Error())
		if s.strictOpen {
			return fmt.Errorf("c2db: page 0 corrupted: %w", err)
		}
		if s.readOnly {
			if err := s.data.Read(0, s.pub[:pageN]); err != nil {
				return err
			}
			// Seule une page entièrement nulle représente un tas non initialisé.
			for _, b := range s.pub[:pageN] {
				if b != 0 {
					return ErrRecoveryRequired
				}
			}
		}
	} else {
		copy(s.pub[:pageN], pg)
	}
	used := uint64(1)
	if s.pub[20] != 0 {
		used = binary.LittleEndian.Uint64(s.pub[heapUsedOff : heapUsedOff+8])
		if used < 1 {
			used = 1
		}
		if used > s.heapPages {
			used = s.heapPages
		}
	}
	for i := uint64(1); i < used; i++ {
		pg, err = s.pager.GetPage(i * pageLBAs)
		if err != nil && !errors.Is(err, errPageSeal) {
			return err
		}
		if err != nil {
			s.pagesSkipped++
			probeEmit(s.id, ProbeOpPageSkip, i*pageLBAs, 0, 0, 0, fmt.Sprintf("page %d seal mismatch: %v", i, err))
			if s.strictOpen {
				return fmt.Errorf("c2db: page %d corrupted: %w", i, err)
			}
			if s.readOnly {
				return ErrRecoveryRequired
			}
			continue
		}
		off := i * pageN
		copy(s.pub[off:off+pageN], pg)
	}
	return nil
}

func (s *Shard) installLive(buf []byte) {
	h := &liveHeap{buf: buf, root: s.heapRoot, snap: s.lastID, hasSnap: s.hasLast}
	s.live.Store(h)
}

func (s *Shard) pinLive() *liveHeap {
	for {
		h := s.live.Load()
		if h == nil {
			return nil
		}
		h.refs.Add(1)
		if s.live.Load() == h {
			return h
		}
		h.refs.Add(-1)
	}
}

func (s *Shard) unpinLive(h *liveHeap) {
	if h != nil {
		h.refs.Add(-1)
	}
}

func (s *Shard) preparePublish() error {
	s.reapHolds()
	if len(s.holdHeaps) >= maxHoldHeaps {
		return ErrViewHeld
	}
	// Pré-allouer le tampon spare pour garantir qu'aucune allocation mmap
	// ne puisse échouer après le point d'engagement durable WAL.
	if len(s.spare) < int(s.shardBytes) {
		extra, err := mmapAnon(int(s.shardBytes))
		if err != nil {
			return err
		}
		if s.spare != nil {
			_ = unix.Munmap(s.spare)
		}
		s.spare = extra
	}

	// Réservation dimensionnée du cache de pages (pager) AVANT l'engagement durable WAL :
	// 1. Garantir que le pager dispose d'une capacité totale d'emplacements >= needed.
	//    Si needed > len(slots), allouer des slots supplémentaires via Reserve(needed) avant l'engagement.
	//    Si needed dépasse le plafond de sécurité absolu (65536 pages), refuser la transaction.
	// 2. S'assurer qu'assez de slots propres sont libres pour accueillir toutes les pages sales
	//    qui seront transférées lors de publish() (FlushDirty avant l'engagement si nécessaire).
	if s.pager != nil {
		needed := s.dirtyPagesCount() + 1 // +1 pour la racine page 0
		if needed > len(s.pager.slots) {
			if err := s.pager.Reserve(needed); err != nil {
				return err
			}
		}
		cleanSlots := len(s.pager.slots) - s.pager.NDirty()
		if needed > cleanSlots {
			if _, err := s.pager.FlushDirty(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Shard) publish() error {
	cihook55.Fire("before-publish")
	if s.publishFaultInjection.Load() {
		err := errors.New("c2db: simulated post-commit publish fault")
		s.poison(err)
		return err
	}
	s.reapHolds()
	curLive := s.live.Load()
	if curLive != nil && curLive.refs.Load() != 0 {
		for i := 0; i < 256 && len(s.holdHeaps) >= maxHoldHeaps; i++ {
			s.reapHolds()
			if len(s.holdHeaps) < maxHoldHeaps {
				break
			}
			runtime.Gosched()
		}
		if len(s.holdHeaps) >= maxHoldHeaps {
			probeEmit(s.id, ProbeOpViewOpen, 0, 0, 0, 0, fmt.Sprintf("holdHeaps ceiling reached (%d), rejecting publish", len(s.holdHeaps)))
			return ErrViewHeld
		}
	}

	used := s.heapUsed
	if used < 1 {
		used = 1
	}
	if used > s.heapPages {
		used = s.heapPages
	}
	from := s.dirty
	if uint64(len(from)) < s.shardBytes {
		from = s.pub
	}
	s.heapGen++
	s.stampHeap(from)
	_ = C2db_crc32c_fullpage_store(from[:pageN], pageN)
	s.markDirty(0)

	// Transfert des pages sales au pager.
	// Grâce à preparePublish(), cleanSlots >= needed est garanti lors des transactions :
	// aucun FlushDirty() n'est déclenché par alloc() dans le pager ici.
	if s.pager != nil {
		if s.dirtyOverflow {
			for w := range s.dirtyMask {
				word := s.dirtyMask[w]
				for word != 0 {
					bit := bits.TrailingZeros64(word)
					pg := uint64(w*64 + bit)
					if pg < used {
						off := pg * pageN
						if err := s.pager.PutPage(pg*pageLBAs, from[off:off+pageN]); err != nil {
							s.poison(err)
							return fmt.Errorf("%w: publish: %v", ErrPostCommit, err)
						}
					}
					word &= word - 1
				}
			}
		} else {
			for _, pg32 := range s.dirtyList[:s.dirtyListLen] {
				pg := uint64(pg32)
				if pg >= used {
					continue
				}
				off := pg * pageN
				if err := s.pager.PutPage(pg*pageLBAs, from[off:off+pageN]); err != nil {
					s.poison(err)
					return fmt.Errorf("%w: publish: %v", ErrPostCommit, err)
				}
			}
		}
	}

	newLive := &liveHeap{buf: from, root: s.heapRoot, snap: s.lastID, hasSnap: s.hasLast}
	old := s.live.Swap(newLive)
	oldPub := s.pub
	s.pub = from
	if old != nil && old.refs.Load() != 0 {
		for i := 0; i < 64 && old.refs.Load() != 0; i++ {
			runtime.Gosched()
		}
	}
	if old != nil && old.refs.Load() != 0 {
		// Lecteurs concurrents épinglant encore l'ancien liveHeap :
		// holdHeaps a la capacité garantie réservée par preparePublish, et s.spare est pré-alloué.
		if len(s.spare) >= int(s.shardBytes) {
			s.dirty = s.spare
			s.spare = nil
		} else {
			if err := s.takeSpareDirty(); err != nil {
				return err
			}
		}
		// Lecteurs épinglés : le tampon de rechange est froid, son contenu est
		// sans rapport avec pub ; la convergence intégrale reste nécessaire.
		s.syncDirtyFull()
		s.holdHeaps = append(s.holdHeaps, old)
	} else if old != nil {
		for old.refs.Load() != 0 {
			runtime.Gosched()
		}
		if len(old.buf) >= int(s.shardBytes) && &old.buf[0] != &from[0] {
			s.dirty = old.buf
		} else if len(oldPub) >= int(s.shardBytes) && &oldPub[0] != &from[0] {
			s.dirty = oldPub
		} else if len(s.spare) >= int(s.shardBytes) {
			s.dirty = s.spare
			s.spare = nil
		} else {
			if err := s.takeSpareDirty(); err != nil {
				return err
			}
		}
		s.convergeDirtyFromPub()
	} else {
		if len(s.spare) >= int(s.shardBytes) {
			s.dirty = s.spare
			s.spare = nil
		} else {
			if err := s.takeSpareDirty(); err != nil {
				return err
			}
		}
		s.convergeDirtyFromPub()
	}
	if len(s.dirty) < int(s.shardBytes) {
		if err := s.takeSpareDirty(); err != nil {
			return err
		}
		s.syncDirtyFull()
	}
	s.heapPubUsed = used
	cihook55.Fire("after-publish")
	return nil
}

func (s *Shard) takeSpareDirty() error {
	if len(s.spare) >= int(s.shardBytes) {
		s.dirty = s.spare
		s.spare = nil
		return nil
	}
	extra, err := mmapAnon(int(s.shardBytes))
	if err != nil {
		return err
	}
	s.dirty = extra
	return nil
}

func (s *Shard) reapHolds() {
	if len(s.holdHeaps) == 0 {
		return
	}
	keep := s.holdHeaps[:0]
	for _, h := range s.holdHeaps {
		if h.refs.Load() != 0 {
			keep = append(keep, h)
			continue
		}
		if len(s.spare) == 0 && len(h.buf) >= int(s.shardBytes) && (len(s.pub) == 0 || &h.buf[0] != &s.pub[0]) && (len(s.dirty) == 0 || &h.buf[0] != &s.dirty[0]) {
			s.spare = h.buf
			continue
		}
		if len(h.buf) >= int(s.shardBytes) && (len(s.pub) == 0 || &h.buf[0] != &s.pub[0]) && (len(s.dirty) == 0 || &h.buf[0] != &s.dirty[0]) && (len(s.spare) == 0 || &h.buf[0] != &s.spare[0]) {
			_ = unix.Munmap(h.buf)
		}
	}
	s.holdHeaps = keep
}

// HeapPages retourne la capacité allouée en nombre de pages (16 Ko par page).
func (s *Shard) HeapPages() uint64 {
	if s == nil {
		return 0
	}
	return s.heapPages
}

// ShardBytes retourne la capacité totale de données du Shard en octets.
func (s *Shard) ShardBytes() uint64 {
	if s == nil {
		return 0
	}
	return s.shardBytes
}
