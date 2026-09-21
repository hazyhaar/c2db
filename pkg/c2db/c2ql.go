// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hazyhaar/c2db/pkg/blake3"
	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
)

const (
	c2qlMaxDocBytes = 4096
	c2qlMaxProj     = 64
)

var (
	errQLLimitAbsente = errors.New("c2ql: limit_absente")
	errQLProjAbsente  = errors.New("c2ql: proj_absente")
	errQLQuota        = errors.New("c2ql: quota")
	errQLPrefix       = errors.New("c2ql: cap_hors_prefixe")
	errQLOp           = errors.New("c2ql: operation_interdite")
	errQLCap          = errors.New("c2ql: cap")
	errQLExpect       = errors.New("c2ql: expect_differe")
	errQLCursor       = errors.New("c2ql: cursor_invalide")
	errQLJoinSansMax  = errors.New("c2ql: join_sans_max")
	errQLJoinSansOn   = errors.New("c2ql: join_sans_on")
	errQLDepth        = errors.New("c2ql: depth_depassee")
	errQLOpInconnue   = errors.New("c2ql: op_inconnue")
	errQLChamp        = errors.New("c2ql: champ_inconnu")
	errQLID           = errors.New("c2ql: id_invalide")
)

const (
	opSetField    = 1
	opSetIfAbsent = 2
	opIncrU64     = 3
	opClearField  = 8
)

type C2QLDoc struct {
	Doc  json.RawMessage `json:"doc"`
	Meta C2QLMeta        `json:"meta"`
}

type C2QLMeta struct {
	Hash string `json:"hash"`
	ID   string `json:"id"`
}

type C2QLResult struct {
	Docs     []C2QLDoc `json:"docs"`
	NextFrom string    `json:"next_from,omitempty"`
	N        uint64    `json:"n,omitempty"`
}

type qlCap struct {
	Prefix     string `json:"prefix"`
	QuotaBytes uint64 `json:"quota_bytes"`
	QuotaOps   uint64 `json:"quota_ops"`
}

type qlScan struct {
	Coll   string          `json:"coll"`
	Limit  uint64          `json:"limit"`
	Proj   []string        `json:"proj"`
	Filter json.RawMessage `json:"filter"`
	Kind   *uint8          `json:"kind"`
	CollID *uint8          `json:"coll_id"`
	FromID string          `json:"from_id"`
	ToID   string          `json:"to_id"`
}

type qlFilter struct {
	Eq  []json.RawMessage `json:"eq"`
	Ge  []json.RawMessage `json:"ge"`
	And []qlFilter        `json:"and"`
}

type qlDel struct {
	Coll   string `json:"coll"`
	ID     string `json:"id"`
	Expect string `json:"expect"`
}

type qlGet struct {
	Coll string `json:"coll"`
	ID   string `json:"id"`
}

type qlCount struct {
	Coll   string `json:"coll"`
	Limit  uint64 `json:"limit"`
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
}

type qlMutOp struct {
	Op uint64          `json:"op"`
	F  string          `json:"f"`
	V  json.RawMessage `json:"v"`
}

type qlCas struct {
	Coll   string          `json:"coll"`
	ID     string          `json:"id"`
	Expect string          `json:"expect"`
	Doc    json.RawMessage `json:"doc"`
	Ops    []qlMutOp       `json:"ops"`
}

type qlIns struct {
	Coll string          `json:"coll"`
	Doc  json.RawMessage `json:"doc"`
}

type qlGets struct {
	Coll  string   `json:"coll"`
	IDs   []string `json:"ids"`
	Limit uint64   `json:"limit"`
	Proj  []string `json:"proj"`
}

type qlJoinRight struct {
	Coll     string   `json:"coll"`
	FromLeft string   `json:"from_left"`
	Limit    uint64   `json:"limit"`
	Proj     []string `json:"proj"`
}

type qlJoin struct {
	On       string       `json:"on"`
	LeftMax  uint64       `json:"left_max"`
	RightMax uint64       `json:"right_max"`
	Left     *qlScan      `json:"left"`
	Right    *qlJoinRight `json:"right"`
}

type qlGroup struct {
	By    []string `json:"by"`
	Limit uint64   `json:"limit"`
	Sum   string   `json:"sum"`
	Src   *qlScan  `json:"src"`
}

type qlNest struct {
	Depth uint64 `json:"depth"`
	Src   *qlOp  `json:"src"`
	Then  *qlOp  `json:"then"`
}

type qlOp struct {
	Get   *qlGet   `json:"get"`
	Gets  *qlGets  `json:"gets"`
	Scan  *qlScan  `json:"scan"`
	Count *qlCount `json:"count"`
	Cas   *qlCas   `json:"cas"`
	Del   *qlDel   `json:"del"`
	Ins   *qlIns   `json:"ins"`
	Join  *qlJoin  `json:"join"`
	Group *qlGroup `json:"group"`
	Nest  *qlNest  `json:"nest"`
}

type qlLot struct {
	Cap  qlCap  `json:"cap"`
	AsOf string `json:"as_of"`
	Op   qlOp   `json:"op"`
	Ops  []qlOp `json:"ops"`
}

type kvFns struct {
	get  func(coll string, id c2uuidv7.UUID) ([]byte, error)
	scan func(coll string, limit uint64) ([][]byte, error)
	put  func(coll string, id c2uuidv7.UUID, v []byte) error
	ins  func(coll string, v []byte) (c2uuidv7.UUID, error)
	mut  func(coll string, id c2uuidv7.UUID, doc []byte, ops []qlMutOp) error
	del  func(coll string, id c2uuidv7.UUID) error
}

func lotOps(lot qlLot) []qlOp {
	if len(lot.Ops) > 0 {
		return lot.Ops
	}
	if opVerbs(lot.Op) == 0 {
		return nil
	}
	return []qlOp{lot.Op}
}

func opVerbs(op qlOp) int {
	n := 0
	if op.Get != nil {
		n++
	}
	if op.Gets != nil {
		n++
	}
	if op.Scan != nil {
		n++
	}
	if op.Count != nil {
		n++
	}
	if op.Cas != nil {
		n++
	}
	if op.Del != nil {
		n++
	}
	if op.Ins != nil {
		n++
	}
	if op.Join != nil {
		n++
	}
	if op.Group != nil {
		n++
	}
	if op.Nest != nil {
		n++
	}
	return n
}

func capColl(cap qlCap, coll string) error {
	if coll == "" || !strings.HasPrefix(coll, cap.Prefix) {
		return errQLPrefix
	}
	return nil
}

func ValidateC2QL(raw []byte) error {
	var lot qlLot
	if err := json.Unmarshal(raw, &lot); err != nil {
		return errQLOp
	}
	if lot.Cap.Prefix == "" || lot.Cap.QuotaBytes == 0 || lot.Cap.QuotaOps == 0 {
		return errQLCap
	}
	if lot.AsOf != "" {
		if _, err := parseUUIDHex(lot.AsOf); err != nil {
			return err
		}
	}
	ops := lotOps(lot)
	if len(ops) == 0 {
		return errQLOp
	}
	if uint64(len(ops)) > lot.Cap.QuotaOps {
		return errQLQuota
	}
	for _, op := range ops {
		if err := validateOp(lot.Cap, op, 0, false); err != nil {
			return err
		}
	}
	return nil
}

func validateOp(cap qlCap, op qlOp, depth uint64, nested bool) error {
	if opVerbs(op) != 1 {
		return errQLOp
	}
	if op.Get != nil {
		if err := capColl(cap, op.Get.Coll); err != nil {
			return err
		}
		if _, err := parseUUIDHex(op.Get.ID); err != nil {
			return err
		}
	}
	if op.Scan != nil {
		if err := validateScan(cap, op.Scan); err != nil {
			return err
		}
	}
	if op.Count != nil {
		if op.Count.Limit == 0 {
			return errQLLimitAbsente
		}
		if err := capColl(cap, op.Count.Coll); err != nil {
			return err
		}
		if err := validateOptionalID(op.Count.FromID); err != nil {
			return err
		}
		if err := validateOptionalID(op.Count.ToID); err != nil {
			return err
		}
	}
	if op.Cas != nil {
		if err := capColl(cap, op.Cas.Coll); err != nil {
			return err
		}
		if op.Cas.ID == "" {
			if !nested {
				return errQLID
			}
		} else if _, err := parseUUIDHex(op.Cas.ID); err != nil {
			return err
		}
	}
	if op.Del != nil {
		if err := capColl(cap, op.Del.Coll); err != nil {
			return err
		}
		if _, err := parseUUIDHex(op.Del.ID); err != nil {
			return err
		}
	}
	if op.Ins != nil {
		if err := capColl(cap, op.Ins.Coll); err != nil {
			return err
		}
		if len(op.Ins.Doc) == 0 {
			return errQLOp
		}
	}
	if op.Gets != nil {
		if op.Gets.Limit == 0 {
			return errQLLimitAbsente
		}
		if err := capColl(cap, op.Gets.Coll); err != nil {
			return err
		}
		if uint64(len(op.Gets.IDs)) > op.Gets.Limit {
			return errQLQuota
		}
		for _, id := range op.Gets.IDs {
			if _, err := parseUUIDHex(id); err != nil {
				return err
			}
		}
	}
	if op.Join != nil {
		if op.Join.On == "" {
			return errQLJoinSansOn
		}
		if op.Join.LeftMax == 0 || op.Join.RightMax == 0 {
			return errQLJoinSansMax
		}
		if op.Join.Left == nil {
			return errQLLimitAbsente
		}
		if err := validateScan(cap, op.Join.Left); err != nil {
			return err
		}
		if op.Join.Right != nil {
			if err := capColl(cap, op.Join.Right.Coll); err != nil {
				return err
			}
		}
	}
	if op.Group != nil {
		if op.Group.Limit == 0 {
			return errQLLimitAbsente
		}
		if op.Group.Src == nil {
			if !nested {
				return errQLLimitAbsente
			}
		} else if err := validateScan(cap, op.Group.Src); err != nil {
			return err
		}
	}
	if op.Nest != nil {
		if depth+1 > 4 || op.Nest.Depth > 4 || op.Nest.Depth == 0 {
			return errQLDepth
		}
		if op.Nest.Src == nil || op.Nest.Then == nil {
			return errQLOp
		}
		if err := validateOp(cap, *op.Nest.Src, depth+1, true); err != nil {
			return err
		}
		if err := validateOp(cap, *op.Nest.Then, depth+1, true); err != nil {
			return err
		}
	}
	return nil
}

func validateScan(cap qlCap, sc *qlScan) error {
	if sc.Limit == 0 {
		return errQLLimitAbsente
	}
	if len(sc.Proj) == 0 || len(sc.Proj) > c2qlMaxProj {
		return errQLProjAbsente
	}
	if err := capColl(cap, sc.Coll); err != nil {
		return err
	}
	need := sc.Limit * c2qlMaxDocBytes
	if need > cap.QuotaBytes {
		return errQLQuota
	}
	if err := validateOptionalID(sc.FromID); err != nil {
		return err
	}
	if err := validateOptionalID(sc.ToID); err != nil {
		return err
	}
	return nil
}

func validateOptionalID(s string) error {
	if s == "" {
		return nil
	}
	_, err := parseUUIDHex(s)
	return err
}

func (db *DB) ExecC2QL(raw []byte) (*C2QLResult, error) {
	if err := ValidateC2QL(raw); err != nil {
		return nil, err
	}
	var lot qlLot
	if err := json.Unmarshal(raw, &lot); err != nil {
		return nil, errQLOp
	}
	kv, err := db.kvFor(lot.AsOf)
	if err != nil {
		return nil, err
	}
	db.mu.Lock()
	db.lotGroup = true
	shards := make([]*Shard, 0, len(db.shards))
	for _, sh := range db.shards {
		shards = append(shards, sh)
	}
	db.mu.Unlock()
	for _, sh := range shards {
		if e := sh.lockWriter(); e != nil {
			db.mu.Lock()
			db.lotGroup = false
			db.mu.Unlock()
			return nil, e
		}
		sh.enterGroup()
	}
	res, err := execLotKV(lot, kv)
	var gerr error
	for _, sh := range shards {
		if e := sh.leaveGroup(); gerr == nil {
			gerr = e
		}
		sh.unlockWriter()
	}
	db.mu.Lock()
	db.lotGroup = false
	db.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if gerr != nil {
		return nil, gerr
	}
	return res, nil
}

func (db *DB) kvFor(asOf string) (kvFns, error) {
	var snap c2uuidv7.UUID
	if asOf != "" {
		var err error
		snap, err = parseUUIDHex(asOf)
		if err != nil {
			return kvFns{}, err
		}
	}
	return kvFns{
		get: func(coll string, id c2uuidv7.UUID) ([]byte, error) {
			if asOf != "" {
				return db.GetDocAsOf(coll, id, snap)
			}
			return db.GetDoc(coll, id)
		},
		scan: func(coll string, limit uint64) ([][]byte, error) {
			return db.ScanPrefix(collectionPrefix(coll), limit)
		},
		put: func(coll string, id c2uuidv7.UUID, v []byte) error {
			s, err := db.GetShard(ShardOf(id))
			if err != nil {
				return err
			}
			return s.Put(collectionKey(coll, id[:]), v)
		},
		ins: func(coll string, v []byte) (c2uuidv7.UUID, error) {
			return db.Insert(coll, v)
		},
		mut: func(coll string, id c2uuidv7.UUID, doc []byte, ops []qlMutOp) error {
			s, err := db.GetShard(ShardOf(id))
			if err != nil {
				return err
			}
			return s.commitMut(collectionKey(coll, id[:]), doc, ops)
		},
		del: func(coll string, id c2uuidv7.UUID) error {
			return db.DeleteDoc(coll, id)
		},
	}, nil
}

func (db *DB) LotShards() []uint16 {
	db.mu.RLock()
	defer db.mu.RUnlock()
	ids := make([]uint16, 0, len(db.shards))
	for id := range db.shards {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (db *DB) SealLot(prefix string) (c2uuidv7.UUID, error) {
	var zero c2uuidv7.UUID
	id, err := NewIDKind(uint64(time.Now().UnixNano()), 0, db.lotSeq, IDKindMap, 0)
	if err != nil {
		return zero, err
	}
	db.lotSeq++
	body, err := json.Marshal(db.LotShards())
	if err != nil {
		return zero, err
	}
	k := make([]byte, len(prefix)+16)
	copy(k, prefix)
	copy(k[len(prefix):], id[:])
	if err := db.Put(k, body); err != nil {
		return zero, err
	}
	return id, nil
}

func ExecC2QL(s *Shard, raw []byte) (*C2QLResult, error) {
	if err := ValidateC2QL(raw); err != nil {
		return nil, err
	}
	var lot qlLot
	if err := json.Unmarshal(raw, &lot); err != nil {
		return nil, errQLOp
	}
	kv, err := s.kvFor(lot.AsOf)
	if err != nil {
		return nil, err
	}
	if err := s.lockWriter(); err != nil {
		return nil, err
	}
	defer s.unlockWriter()
	s.enterGroup()
	res, err := execLotKV(lot, kv)
	gerr := s.leaveGroup()
	if err != nil {
		return nil, err
	}
	if gerr != nil {
		return nil, gerr
	}
	return res, nil
}

func (s *Shard) kvFor(asOf string) (kvFns, error) {
	var snap c2uuidv7.UUID
	if asOf != "" {
		var err error
		snap, err = parseUUIDHex(asOf)
		if err != nil {
			return kvFns{}, err
		}
	}
	return kvFns{
		get: func(coll string, id c2uuidv7.UUID) ([]byte, error) {
			if asOf != "" {
				return s.GetDocAsOf(coll, id, snap)
			}
			return s.GetDoc(coll, id)
		},
		scan: func(coll string, limit uint64) ([][]byte, error) {
			keys, err := s.ScanPrefix(collectionPrefix(coll))
			if err != nil {
				return nil, err
			}
			if uint64(len(keys)) > limit {
				keys = keys[:limit]
			}
			return keys, nil
		},
		put: func(coll string, id c2uuidv7.UUID, v []byte) error {
			return s.Put(collectionKey(coll, id[:]), v)
		},
		ins: func(coll string, v []byte) (c2uuidv7.UUID, error) {
			return s.Insert(coll, v)
		},
		mut: func(coll string, id c2uuidv7.UUID, doc []byte, ops []qlMutOp) error {
			return s.commitMut(collectionKey(coll, id[:]), doc, ops)
		},
		del: func(coll string, id c2uuidv7.UUID) error {
			return s.DeleteDoc(coll, id)
		},
	}, nil
}

func execLotKV(lot qlLot, kv kvFns) (*C2QLResult, error) {
	ops := lotOps(lot)
	out := &C2QLResult{Docs: []C2QLDoc{}}
	for _, op := range ops {
		one, err := execOp(op, kv)
		if err != nil {
			return nil, err
		}
		out.Docs = append(out.Docs, one.Docs...)
		out.N += one.N
		if one.NextFrom != "" {
			out.NextFrom = one.NextFrom
		}
	}
	if out.N == 0 {
		out.N = uint64(len(out.Docs))
	}
	return out, nil
}

func execOp(op qlOp, kv kvFns) (*C2QLResult, error) {
	switch {
	case op.Get != nil:
		return execGetKV(op.Get, kv)
	case op.Scan != nil:
		return execScanKV(op.Scan, kv)
	case op.Count != nil:
		return execCountKV(op.Count, kv)
	case op.Cas != nil:
		return execCasKV(op.Cas, kv)
	case op.Del != nil:
		return execDelKV(op.Del, kv)
	case op.Ins != nil:
		return execInsKV(op.Ins, kv)
	case op.Gets != nil:
		return execGetsKV(op.Gets, kv)
	case op.Join != nil:
		return execJoinKV(op.Join, kv)
	case op.Group != nil:
		return execGroupKV(op.Group, kv)
	case op.Nest != nil:
		return execNestKV(op.Nest, kv)
	default:
		return nil, errQLOp
	}
}

func docHash(val []byte) string {
	h := blake3archtsim.Sum256(val)
	return hex.EncodeToString(h[:])
}

func metaID(id c2uuidv7.UUID, val []byte) C2QLMeta {
	return C2QLMeta{Hash: docHash(val), ID: hex.EncodeToString(id[:])}
}

func projectJSON(val []byte, proj []string) json.RawMessage {
	if len(proj) == 0 {
		return append(json.RawMessage(nil), val...)
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(val, &m) != nil {
		return append(json.RawMessage(nil), val...)
	}
	out := make(map[string]json.RawMessage, len(proj))
	for _, p := range proj {
		if v, ok := m[p]; ok {
			out[p] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func execGetKV(g *qlGet, kv kvFns) (*C2QLResult, error) {
	id, err := parseUUIDHex(g.ID)
	if err != nil {
		return nil, err
	}
	val, err := kv.get(g.Coll, id)
	if err != nil {
		return nil, err
	}
	return &C2QLResult{Docs: []C2QLDoc{{
		Doc:  append(json.RawMessage(nil), val...),
		Meta: metaID(id, val),
	}}}, nil
}

func scanFetchN(sc *qlScan) uint64 {
	n := sc.Limit + 1
	if len(sc.Filter) > 0 || sc.Kind != nil || sc.CollID != nil || sc.FromID != "" || sc.ToID != "" {
		if sc.Limit > (1<<20)/32 {
			n = 1 << 20
		} else {
			n = sc.Limit*32 + 1
		}
	}
	return n
}

func execScanKV(sc *qlScan, kv kvFns) (*C2QLResult, error) {
	lim := sc.Limit
	keys, err := kv.scan(sc.Coll, scanFetchN(sc))
	if err != nil {
		return nil, err
	}
	res := &C2QLResult{Docs: make([]C2QLDoc, 0, lim)}
	for _, k := range keys {
		id, ok := idFromCollKey(sc.Coll, k)
		if !ok {
			continue
		}
		if !matchIDPred(id, sc) {
			continue
		}
		val, err := kv.get(sc.Coll, id)
		if err != nil {
			return nil, err
		}
		if !matchFilter(val, sc.Filter) {
			continue
		}
		if uint64(len(res.Docs)) >= lim {
			res.NextFrom = hex.EncodeToString(id[:])
			break
		}
		res.Docs = append(res.Docs, C2QLDoc{
			Doc:  projectJSON(val, sc.Proj),
			Meta: metaID(id, val),
		})
	}
	res.N = uint64(len(res.Docs))
	return res, nil
}

func execCountKV(c *qlCount, kv kvFns) (*C2QLResult, error) {
	keys, err := kv.scan(c.Coll, c.Limit)
	if err != nil {
		return nil, err
	}
	n := uint64(0)
	for _, k := range keys {
		id, ok := idFromCollKey(c.Coll, k)
		if !ok {
			continue
		}
		if c.FromID != "" {
			lo, err := parseUUIDHex(c.FromID)
			if err != nil || bytes.Compare(id[:], lo[:]) < 0 {
				continue
			}
		}
		if c.ToID != "" {
			hi, err := parseUUIDHex(c.ToID)
			if err != nil || bytes.Compare(id[:], hi[:]) >= 0 {
				continue
			}
		}
		n++
	}
	if n > c.Limit {
		n = c.Limit
	}
	return &C2QLResult{N: n}, nil
}

func execCasKV(c *qlCas, kv kvFns) (*C2QLResult, error) {
	id, err := parseUUIDHex(c.ID)
	if err != nil {
		return nil, err
	}
	cur, err := kv.get(c.Coll, id)
	if err != nil && !errors.Is(err, errNotFound) {
		return nil, err
	}
	got := ""
	if err == nil {
		got = docHash(cur)
	}
	if got != c.Expect {
		return nil, errQLExpect
	}
	next := c.Doc
	if len(c.Ops) > 0 {
		var err2 error
		next, err2 = applyMutOps(cur, c.Ops)
		if err2 != nil {
			return nil, err2
		}
	}
	if len(next) == 0 {
		return nil, errQLOp
	}
	if len(c.Ops) > 0 && kv.mut != nil {
		if err := kv.mut(c.Coll, id, next, c.Ops); err != nil {
			return nil, err
		}
	} else if err := kv.put(c.Coll, id, next); err != nil {
		return nil, err
	}
	return &C2QLResult{Docs: []C2QLDoc{{
		Doc:  next,
		Meta: metaID(id, next),
	}}}, nil
}

func execDelKV(d *qlDel, kv kvFns) (*C2QLResult, error) {
	id, err := parseUUIDHex(d.ID)
	if err != nil {
		return nil, err
	}
	cur, err := kv.get(d.Coll, id)
	if err != nil && !errors.Is(err, errNotFound) {
		return nil, err
	}
	got := ""
	if err == nil {
		got = docHash(cur)
	}
	if got != d.Expect {
		return nil, errQLExpect
	}
	if err == nil {
		if err := kv.del(d.Coll, id); err != nil {
			return nil, err
		}
	}
	return &C2QLResult{Docs: []C2QLDoc{{Meta: C2QLMeta{ID: hex.EncodeToString(id[:])}}}, N: 1}, nil
}

func execInsKV(in *qlIns, kv kvFns) (*C2QLResult, error) {
	id, err := kv.ins(in.Coll, in.Doc)
	if err != nil {
		return nil, err
	}
	return &C2QLResult{Docs: []C2QLDoc{{
		Doc:  append(json.RawMessage(nil), in.Doc...),
		Meta: metaID(id, in.Doc),
	}}, N: 1}, nil
}

func applyMutOps(cur []byte, ops []qlMutOp) (json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if len(cur) > 0 {
		if err := json.Unmarshal(cur, &m); err != nil {
			return nil, errQLOp
		}
	}
	for _, op := range ops {
		if op.F == "" {
			return nil, errQLChamp
		}
		switch op.Op {
		case opSetField:
			m[op.F] = append(json.RawMessage(nil), op.V...)
		case opSetIfAbsent:
			if _, ok := m[op.F]; !ok {
				m[op.F] = append(json.RawMessage(nil), op.V...)
			}
		case opIncrU64:
			n := uint64(0)
			if raw, ok := m[op.F]; ok {
				n, _ = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
			}
			add, _ := strconv.ParseUint(strings.TrimSpace(string(op.V)), 10, 64)
			m[op.F] = json.RawMessage(strconv.FormatUint(n+add, 10))
		case opClearField:
			delete(m, op.F)
		default:
			return nil, errQLOpInconnue
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, errQLOp
	}
	return b, nil
}

func jsonField(doc []byte, field string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(doc, &m) != nil {
		return ""
	}
	raw, ok := m[field]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func jsonScalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func matchFilter(val []byte, raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var f qlFilter
	if json.Unmarshal(raw, &f) != nil {
		return false
	}
	return evalFilter(val, f)
}

func evalFilter(val []byte, f qlFilter) bool {
	if len(f.And) > 0 {
		for _, a := range f.And {
			if !evalFilter(val, a) {
				return false
			}
		}
	}
	if len(f.Eq) == 2 {
		field := jsonScalar(f.Eq[0])
		want := jsonScalar(f.Eq[1])
		if jsonField(val, field) != want {
			return false
		}
	}
	if len(f.Ge) == 2 {
		field := jsonScalar(f.Ge[0])
		got := jsonField(val, field)
		want := jsonScalar(f.Ge[1])
		gi, ge := strconv.ParseInt(got, 10, 64)
		wi, we := strconv.ParseInt(want, 10, 64)
		if ge == nil && we == nil {
			if gi < wi {
				return false
			}
		} else if got < want {
			return false
		}
	}
	return true
}

func parseUUIDHex(s string) (c2uuidv7.UUID, error) {
	var id c2uuidv7.UUID
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return id, errQLID
	}
	copy(id[:], b)
	return id, nil
}

func matchIDPred(id c2uuidv7.UUID, sc *qlScan) bool {
	if sc.Kind != nil && KindOf(id) != *sc.Kind {
		return false
	}
	if sc.CollID != nil && CollOf(id) != *sc.CollID {
		return false
	}
	if sc.FromID != "" {
		lo, err := parseUUIDHex(sc.FromID)
		if err != nil || bytes.Compare(id[:], lo[:]) < 0 {
			return false
		}
	}
	if sc.ToID != "" {
		hi, err := parseUUIDHex(sc.ToID)
		if err != nil || bytes.Compare(id[:], hi[:]) >= 0 {
			return false
		}
	}
	return true
}

func execGetsKV(g *qlGets, kv kvFns) (*C2QLResult, error) {
	res := &C2QLResult{Docs: make([]C2QLDoc, 0, len(g.IDs))}
	for i, raw := range g.IDs {
		if uint64(i) >= g.Limit {
			break
		}
		id, err := parseUUIDHex(raw)
		if err != nil {
			return nil, err
		}
		val, err := kv.get(g.Coll, id)
		if err != nil {
			return nil, err
		}
		res.Docs = append(res.Docs, C2QLDoc{
			Doc:  projectJSON(val, g.Proj),
			Meta: metaID(id, val),
		})
	}
	res.N = uint64(len(res.Docs))
	return res, nil
}

func execJoinKV(j *qlJoin, kv kvFns) (*C2QLResult, error) {
	leftScan := *j.Left
	if leftScan.Limit > j.LeftMax {
		leftScan.Limit = j.LeftMax
	}
	left, err := execScanKV(&leftScan, kv)
	if err != nil {
		return nil, err
	}
	res := &C2QLResult{Docs: make([]C2QLDoc, 0, len(left.Docs))}
	nRight := uint64(0)
	rightColl := ""
	if j.Right != nil {
		rightColl = j.Right.Coll
	}
	for _, d := range left.Docs {
		if nRight >= j.RightMax {
			break
		}
		rk := jsonField(d.Doc, j.On)
		if rk == "" && j.Right != nil && j.Right.FromLeft != "" {
			rk = jsonField(d.Doc, j.Right.FromLeft)
		}
		if rk == "" {
			continue
		}
		rid, err := parseUUIDHex(rk)
		if err != nil {
			continue
		}
		val, err := kv.get(rightColl, rid)
		if err != nil {
			continue
		}
		nRight++
		proj := j.Left.Proj
		if j.Right != nil && len(j.Right.Proj) > 0 {
			proj = j.Right.Proj
		}
		res.Docs = append(res.Docs, C2QLDoc{
			Doc:  projectJSON(val, proj),
			Meta: metaID(rid, val),
		})
	}
	res.N = uint64(len(res.Docs))
	return res, nil
}

func execGroupKV(g *qlGroup, kv kvFns) (*C2QLResult, error) {
	var src *C2QLResult
	var err error
	if g.Src != nil {
		src, err = execScanKV(g.Src, kv)
		if err != nil {
			return nil, err
		}
	} else {
		src = &C2QLResult{}
	}
	return execGroupFromDocs(g, src.Docs), nil
}

func execGroupFromDocs(g *qlGroup, docs []C2QLDoc) *C2QLResult {
	type acc struct {
		n uint64
		s uint64
	}
	by := ""
	if len(g.By) > 0 {
		by = g.By[0]
	}
	m := map[string]*acc{}
	order := []string{}
	for _, d := range docs {
		k := jsonField(d.Doc, by)
		a, ok := m[k]
		if !ok {
			if uint64(len(order)) >= g.Limit {
				continue
			}
			a = &acc{}
			m[k] = a
			order = append(order, k)
		}
		a.n++
		if g.Sum != "" {
			v := jsonField(d.Doc, g.Sum)
			u, _ := strconv.ParseUint(v, 10, 64)
			a.s += u
		}
	}
	res := &C2QLResult{Docs: make([]C2QLDoc, 0, len(order))}
	for _, k := range order {
		a := m[k]
		body, _ := json.Marshal(map[string]uint64{"n": a.n, "s": a.s})
		res.Docs = append(res.Docs, C2QLDoc{Doc: body, Meta: C2QLMeta{Hash: docHash(body)}})
	}
	res.N = uint64(len(res.Docs))
	return res
}

func execNestKV(n *qlNest, kv kvFns) (*C2QLResult, error) {
	src, err := execOp(*n.Src, kv)
	if err != nil {
		return nil, err
	}
	then := n.Then
	if then.Group != nil {
		return execGroupFromDocs(then.Group, src.Docs), nil
	}
	if then.Cas != nil {
		res := &C2QLResult{Docs: make([]C2QLDoc, 0, len(src.Docs))}
		for _, d := range src.Docs {
			cas := *then.Cas
			cas.ID = d.Meta.ID
			cas.Expect = d.Meta.Hash
			one, err := execCasKV(&cas, kv)
			if err != nil {
				return nil, err
			}
			res.Docs = append(res.Docs, one.Docs...)
		}
		res.N = uint64(len(res.Docs))
		return res, nil
	}
	return execOp(*then, kv)
}
