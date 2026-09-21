// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/hazyhaar/c2db/pkg/c2uuidv7"
	"golang.org/x/sys/unix"
)

func idHex(id c2uuidv7.UUID) string {
	return hex.EncodeToString(id[:])
}

func TestC2QLScanOK(t *testing.T) {
	raw := []byte(`{"cap":{"prefix":"ord","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":10,"proj":["id","status"]}}}`)
	if err := ValidateC2QL(raw); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

func TestC2QLRefus(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"limit", `{"cap":{"prefix":"ord","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","proj":["id"]}}}`, errQLLimitAbsente},
		{"proj", `{"cap":{"prefix":"ord","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":1}}}`, errQLProjAbsente},
		{"count", `{"cap":{"prefix":"ord","quota_bytes":65536,"quota_ops":8},"op":{"count":{"coll":"orders"}}}`, errQLLimitAbsente},
		{"quota", `{"cap":{"prefix":"ord","quota_bytes":100,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":10,"proj":["id"]}}}`, errQLQuota},
		{"prefix", `{"cap":{"prefix":"ord","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"other","id":"00000000000000000000000000000001"}}}`, errQLPrefix},
		{"join", `{"cap":{"prefix":"ord","quota_bytes":65536,"quota_ops":8},"op":{"join":{"on":"id"}}}`, errQLJoinSansMax},
		{"id", `{"cap":{"prefix":"ord","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"orders","id":"zz"}}}`, errQLID},
	}
	for _, tc := range cases {
		err := ValidateC2QL([]byte(tc.raw))
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v want %v", tc.name, err, tc.want)
		}
	}
}

func TestC2QLExecGetScanCas(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 1)
	defer func() { _ = s.Close() }()
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll: %v", err)
	}
	idA, err := s.Insert("orders", []byte(`{"status":"open"}`))
	if err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := s.Insert("orders", []byte(`{"status":"hold"}`)); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	ha := idHex(idA)
	got, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"orders","id":"`+ha+`"}}}`))
	if err != nil || len(got.Docs) != 1 || got.Docs[0].Meta.Hash == "" || got.Docs[0].Meta.ID != ha {
		t.Fatalf("get: %+v %v", got, err)
	}
	h := got.Docs[0].Meta.Hash
	scan, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":1,"proj":["status"]}}}`))
	if err != nil || scan.N != 1 || scan.NextFrom == "" {
		t.Fatalf("scan: %+v %v", scan, err)
	}
	cnt, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"count":{"coll":"orders","limit":8}}}`))
	if err != nil || cnt.N != 2 {
		t.Fatalf("count: %+v %v", cnt, err)
	}
	if _, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"`+ha+`","expect":"00","doc":{"status":"x"}}}}`)); !errors.Is(err, errQLExpect) {
		t.Fatalf("cas bad expect: %v", err)
	}
	cas, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"`+ha+`","expect":"`+h+`","doc":{"status":"done"}}}}`))
	if err != nil || cas.Docs[0].Meta.Hash == "" {
		t.Fatalf("cas: %+v %v", cas, err)
	}
}

func TestC2QLGetsJoinGroupNest(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 2)
	defer func() { _ = s.Close() }()
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll orders: %v", err)
	}
	if err := s.CreateCollection("ocust"); err != nil {
		t.Fatalf("coll ocust: %v", err)
	}
	cid, err := s.Insert("ocust", []byte(`{"name":"n"}`))
	if err != nil {
		t.Fatalf("insert c: %v", err)
	}
	ch := idHex(cid)
	id1, err := s.Insert("orders", []byte(`{"customer_id":"`+ch+`","status":"open","total":5}`))
	if err != nil {
		t.Fatalf("insert o1: %v", err)
	}
	id2, err := s.Insert("orders", []byte(`{"customer_id":"`+ch+`","status":"open","total":7}`))
	if err != nil {
		t.Fatalf("insert o2: %v", err)
	}
	gets, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"gets":{"coll":"orders","ids":["`+idHex(id1)+`","`+idHex(id2)+`"],"limit":8,"proj":["status"]}}}`))
	if err != nil || gets.N != 2 {
		t.Fatalf("gets: %+v %v", gets, err)
	}
	if _, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"join":{"on":"id","left_max":0,"right_max":1,"left":{"coll":"orders","limit":1,"proj":["status"]}}}}`)); !errors.Is(err, errQLJoinSansMax) {
		t.Fatalf("join sans max: %v", err)
	}
	join, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"join":{"on":"customer_id","left_max":8,"right_max":8,"left":{"coll":"orders","limit":8,"proj":["customer_id"]},"right":{"coll":"ocust","from_left":"customer_id","limit":8,"proj":["name"]}}}}`))
	if err != nil || join.N < 1 {
		t.Fatalf("join: %+v %v", join, err)
	}
	grp, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"group":{"by":["status"],"limit":8,"sum":"total","src":{"coll":"orders","limit":8,"proj":["status","total"]}}}}`))
	if err != nil || grp.N != 1 {
		t.Fatalf("group: %+v %v", grp, err)
	}
	if _, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"nest":{"depth":5,"src":{"scan":{"coll":"orders","limit":8,"proj":["status"]}},"then":{"group":{"by":["status"],"limit":8}}}}}`)); !errors.Is(err, errQLDepth) {
		t.Fatalf("nest 5: %v", err)
	}
	nest, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"nest":{"depth":2,"src":{"scan":{"coll":"orders","limit":8,"proj":["status","total"]}},"then":{"group":{"by":["status"],"limit":8,"sum":"total"}}}}}`))
	if err != nil || nest.N != 1 {
		t.Fatalf("nest scan→group: %+v %v", nest, err)
	}
}

func TestC2QLMutOps(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 3)
	defer func() { _ = s.Close() }()
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll: %v", err)
	}
	id, err := s.Insert("orders", []byte(`{"status":"open","n":1}`))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	hx := idHex(id)
	g, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"orders","id":"`+hx+`"}}}`))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	h := g.Docs[0].Meta.Hash
	out, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"`+hx+`","expect":"`+h+`","ops":[{"op":1,"f":"status","v":"done"},{"op":3,"f":"n","v":2}]}}}`))
	if err != nil {
		t.Fatalf("ops: %v", err)
	}
	if jsonField(out.Docs[0].Doc, "status") != "done" {
		t.Fatalf("status=%q", jsonField(out.Docs[0].Doc, "status"))
	}
	if jsonField(out.Docs[0].Doc, "n") != "3" {
		t.Fatalf("n=%s", jsonField(out.Docs[0].Doc, "n"))
	}
	if _, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"`+hx+`","expect":"`+out.Docs[0].Meta.Hash+`","ops":[{"op":99,"f":"x","v":1}]}}}`)); !errors.Is(err, errQLOpInconnue) {
		t.Fatalf("op 99: %v", err)
	}
	if _, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"cas":{"coll":"orders","id":"`+hx+`","expect":"`+out.Docs[0].Meta.Hash+`","ops":[{"op":1,"f":"","v":1}]}}}`)); !errors.Is(err, errQLChamp) {
		t.Fatalf("champ vide: %v", err)
	}
	recs, err := s.wal.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	sawMut := false
	for _, r := range recs {
		if r.Type == RecMut {
			sawMut = true
			break
		}
	}
	if !sawMut {
		t.Fatal("WAL sans RecMut")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2 := mustOpenShard(t, dir, key, 3)
	defer func() { _ = s2.Close() }()
	got, err := s2.GetDoc("orders", id)
	if err != nil || jsonField(got, "status") != "done" {
		t.Fatalf("redo mut: err=%v val=%s", err, got)
	}
}

func TestDBC2QLJoin(t *testing.T) {
	dir := t.TempDir()
	var mac [32]byte
	db, err := OpenDB(dir, mac)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.CreateCollection("orders"); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("coll orders: %v", err)
	}
	if err := db.CreateCollection("ocust"); err != nil {
		t.Fatalf("coll ocust: %v", err)
	}
	cid, err := db.Insert("ocust", []byte(`{"name":"n"}`))
	if err != nil {
		t.Fatalf("insert c: %v", err)
	}
	if _, err := db.Insert("orders", []byte(`{"customer_id":"`+idHex(cid)+`","total":4}`)); err != nil {
		t.Fatalf("insert o: %v", err)
	}
	res, err := db.ExecC2QL([]byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"join":{"on":"customer_id","left_max":8,"right_max":8,"left":{"coll":"orders","limit":8,"proj":["customer_id"]},"right":{"coll":"ocust","from_left":"customer_id","limit":8,"proj":["name"]}}}}`))
	if err != nil || res.N < 1 {
		t.Fatalf("db join: %+v %v shards=%d", res, err, len(db.shards))
	}
	lid, err := db.SealLot("cmd/lot/")
	if err != nil {
		t.Fatalf("SealLot: %v", err)
	}
	if KindOf(lid) != IDKindMap {
		t.Fatalf("lot kind=%d", KindOf(lid))
	}
	if n := len(db.LotShards()); n < 1 {
		t.Fatalf("LotShards=%d", n)
	}
}

func TestC2QLScanNextFromReopen(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 14)
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll: %v", err)
	}
	if _, err := s.Insert("orders", []byte(`{"n":1}`)); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if _, err := s.Insert("orders", []byte(`{"n":2}`)); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if _, err := s.Insert("orders", []byte(`{"n":3}`)); err != nil {
		t.Fatalf("insert c: %v", err)
	}
	scan, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":1,"proj":["n"]}}}`))
	if err != nil || scan.NextFrom == "" {
		t.Fatalf("scan: %+v %v", scan, err)
	}
	nf := scan.NextFrom
	first := scan.Docs[0].Meta.ID
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s = mustOpenShard(t, dir, key, 14)
	scan2, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":8,"proj":["n"],"from_id":"`+nf+`"}}}`))
	if err != nil || scan2.N < 1 {
		t.Fatalf("scan next_from réouvert: %+v %v", scan2, err)
	}
	if scan2.Docs[0].Meta.ID == first && nf > first {
		t.Fatalf("curseur ignoré: first=%s next_from=%s", scan2.Docs[0].Meta.ID, nf)
	}
	_ = s.Close()
}

func TestC2QLFilterAndAsOf(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 21)
	defer func() { _ = s.Close() }()
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll: %v", err)
	}
	idA, err := s.Insert("orders", []byte(`{"status":"open","n":1}`))
	if err != nil {
		t.Fatalf("insert a: %v", err)
	}
	snap := idHex(s.lastID)
	if _, err := s.Insert("orders", []byte(`{"status":"hold","n":9}`)); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	filt, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":8,"proj":["status"],"filter":{"eq":["status","open"]}}}}`))
	if err != nil || filt.N != 1 || jsonField(filt.Docs[0].Doc, "status") != "open" {
		t.Fatalf("filter eq: %+v %v", filt, err)
	}
	ge, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":8,"proj":["n"],"filter":{"ge":["n",9]}}}}`))
	if err != nil || ge.N != 1 {
		t.Fatalf("filter ge: %+v %v", ge, err)
	}
	and, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":8,"proj":["status"],"filter":{"and":[{"eq":["status","hold"]},{"ge":["n",1]}]}}}}`))
	if err != nil || and.N != 1 {
		t.Fatalf("filter and: %+v %v", and, err)
	}
	if err := s.Put(collectionKey("orders", idA[:]), []byte(`{"status":"done","n":1}`)); err != nil {
		t.Fatalf("put a2: %v", err)
	}
	old, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"as_of":"`+snap+`","op":{"get":{"coll":"orders","id":"`+idHex(idA)+`"}}}`))
	if err != nil || jsonField(old.Docs[0].Doc, "status") != "open" {
		t.Fatalf("as_of: %+v %v", old, err)
	}
	live, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"orders","id":"`+idHex(idA)+`"}}}`))
	if err != nil || jsonField(live.Docs[0].Doc, "status") != "done" {
		t.Fatalf("live: %+v %v", live, err)
	}
}

func TestC2QLDelOpsNestCasUUID(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 22)
	defer func() { _ = s.Close() }()
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll: %v", err)
	}
	idX, err := s.Insert("orders", []byte(`{"status":"open"}`))
	if err != nil {
		t.Fatalf("insert x: %v", err)
	}
	idY, err := s.Insert("orders", []byte(`{"status":"open"}`))
	if err != nil {
		t.Fatalf("insert y: %v", err)
	}
	hx, hy := idHex(idX), idHex(idY)
	from, to := hx, hy
	if from > to {
		from, to = to, from
	}
	rng, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":8,"proj":["status"],"from_id":"`+from+`","to_id":"`+to+`"}}}`))
	if err != nil || rng.N != 1 {
		t.Fatalf("from_id/to_id: %+v %v", rng, err)
	}
	g, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"get":{"coll":"orders","id":"`+hx+`"}}}`))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	h := g.Docs[0].Meta.Hash
	if _, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"del":{"coll":"orders","id":"`+hx+`","expect":"00"}}}`)); !errors.Is(err, errQLExpect) {
		t.Fatalf("del bad expect: %v", err)
	}
	del, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"del":{"coll":"orders","id":"`+hx+`","expect":"`+h+`"}}}`))
	if err != nil || del.N != 1 {
		t.Fatalf("del: %+v %v", del, err)
	}
	if _, err := s.GetDoc("orders", idX); !errors.Is(err, errNotFound) {
		t.Fatalf("get after del: %v", err)
	}
	if err := ValidateC2QL([]byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":1},"ops":[{"get":{"coll":"orders","id":"` + hy + `"}},{"count":{"coll":"orders","limit":8}}]}`)); !errors.Is(err, errQLQuota) {
		t.Fatalf("ops quota: %v", err)
	}
	suite, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":2},"ops":[{"get":{"coll":"orders","id":"`+hy+`"}},{"count":{"coll":"orders","limit":8}}]}`))
	if err != nil || len(suite.Docs) != 1 || suite.N < 1 {
		t.Fatalf("ops suite: %+v %v", suite, err)
	}
	nestCas, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"nest":{"depth":2,"src":{"scan":{"coll":"orders","limit":8,"proj":["status"]}},"then":{"cas":{"coll":"orders","id":"","expect":"","ops":[{"op":1,"f":"status","v":"hold"}]}}}}}`))
	if err != nil || nestCas.N != 1 || jsonField(nestCas.Docs[0].Doc, "status") != "hold" {
		t.Fatalf("nest cas: %+v %v", nestCas, err)
	}
	cb := collByte("orders")
	gotKind, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"scan":{"coll":"orders","limit":8,"proj":["status"],"kind":0,"coll_id":`+itoaU8(cb)+`}}}`))
	if err != nil || gotKind.N != 1 {
		t.Fatalf("kind put: %+v %v", gotKind, err)
	}
	ins, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"op":{"ins":{"coll":"orders","doc":{"status":"new"}}}}`))
	if err != nil || ins.N != 1 || ins.Docs[0].Meta.ID == "" {
		t.Fatalf("ins: %+v %v", ins, err)
	}
}

func TestDBInsertGetLikeSQLite(t *testing.T) {
	dir := t.TempDir()
	var mac [32]byte
	db, err := OpenDB(dir, mac)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.CreateCollection("orders"); err != nil {
		if errors.Is(err, unix.EINVAL) {
			t.Skip("O_DIRECT")
		}
		t.Fatalf("create: %v", err)
	}
	id, err := db.Insert("orders", []byte(`{"status":"open","total":5}`))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := db.GetDoc("orders", id)
	if err != nil || jsonField(got, "status") != "open" {
		t.Fatalf("get: err=%v val=%s", err, got)
	}
	if err := db.DeleteDoc("orders", id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.GetDoc("orders", id); !errors.Is(err, errNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestC2QLLotOneWALBlock(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	s := mustOpenShard(t, dir, key, 23)
	defer func() { _ = s.Close() }()
	if err := s.CreateCollection("orders"); err != nil {
		t.Fatalf("coll: %v", err)
	}
	n0 := s.wal.next
	res, err := ExecC2QL(s, []byte(`{"cap":{"prefix":"o","quota_bytes":65536,"quota_ops":8},"ops":[{"ins":{"coll":"orders","doc":{"n":1}}},{"ins":{"coll":"orders","doc":{"n":2}}}]}`))
	if err != nil || res.N != 2 {
		t.Fatalf("ops ins: %+v %v", res, err)
	}
	if d := s.wal.next - n0; d != 1 {
		t.Fatalf("blocs WAL lot=%d want 1", d)
	}
}

func itoaU8(n uint8) string {
	if n == 0 {
		return "0"
	}
	var b [3]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = '0' + n%10
		n /= 10
	}
	return string(b[i:])
}
