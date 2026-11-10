package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

func TestPushPullRoundTrip(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	w := mkWork("1973")
	w.Author = ptr("Author")
	c1 := clock(nowMs(), 0, s.Device.SyncDeviceID)

	resp := push(e, s.AccessToken, 0, []syncsvc.Work{w},
		rec("library", w.Hash, "pinned", true, c1),
		rec("setting", "reader.fontSize", "value", 18, c1))
	if resp.Seq != 2 || len(resp.Results) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	for i, r := range resp.Results {
		if r.Result != "applied" || r.Record == nil || r.Record.Seq != int64(i+1) {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	if got := resp.Results[0].Record; got.Work == nil || got.Work.Title != "Title 1973" || got.Work.Hash != w.Hash {
		t.Fatalf("library record should carry its work: %+v", got)
	}
	if resp.Results[1].Record.Work != nil {
		t.Fatal("setting must not carry a work")
	}

	ch := pull(e, s.AccessToken, 0)
	if len(ch.Records) != 2 || ch.HasMore || ch.NextSince != 2 {
		t.Fatalf("changes = %+v", ch)
	}
	lib := ch.Records[0]
	if lib.Kind != "library" || lib.Key != w.Hash || string(lib.Fields["pinned"]) != "true" || lib.Clocks["pinned"] != c1 ||
		lib.Work == nil || lib.Work.Author == nil || *lib.Work.Author != "Author" || lib.Work.ContentKind != "novel" {
		t.Fatalf("library record = %+v", lib)
	}
	if again := pull(e, s.AccessToken, 2); len(again.Records) != 0 || again.NextSince != 2 || again.HasMore {
		t.Fatalf("nothing should follow seq 2: %+v", again)
	}

	// Pagination.
	page := expect[syncsvc.ChangesResponse](e, 200, "GET", "/api/v1/sync/changes?since=0&limit=1", s.AccessToken, nil)
	if len(page.Records) != 1 || !page.HasMore || page.NextSince != 1 {
		t.Fatalf("page 1 = %+v", page)
	}
	e.expectErr(400, "bad_request", "GET", "/api/v1/sync/changes", s.AccessToken, nil)
	e.expectErr(400, "bad_request", "GET", "/api/v1/sync/changes?since=-1", s.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/sync/changes?since=0", "", nil)
	e.expectErr(401, "unauthorized", "POST", "/api/v1/sync/push", "", pushBody(0, nil, nil, newBatchID()))

	// A record reset by the server keeps a stable seq when nothing changes.
	same := push(e, s.AccessToken, 2, nil, rec("library", w.Hash, "pinned", true, c1))
	if same.Results[0].Result != "unchanged" || same.Results[0].Record.Seq != 1 || same.Seq != 2 {
		t.Fatalf("unchanged resend = %+v", same)
	}
}

func TestSecondDeviceConverges(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice")
	b := e.login("alice", "correct horse", "tablet")
	w := mkWork("42")
	now := nowMs()

	push(e, a.AccessToken, 0, []syncsvc.Work{w}, rec("library", w.Hash, "pinned", true, clock(now, 0, a.Device.SyncDeviceID)))

	// Device B pulls everything, then edits a different field and a conflicting one.
	got := pull(e, b.AccessToken, 0)
	if len(got.Records) != 1 {
		t.Fatalf("B should see A's record: %+v", got)
	}
	r := push(e, b.AccessToken, got.NextSince, nil,
		rec("library", w.Hash, "archived", true, clock(now+10, 0, b.Device.SyncDeviceID)))
	if r.Results[0].Result != "applied" {
		t.Fatal(r.Results[0].Result)
	}
	// B also writes pinned=false later than A's pinned=true; A writes an older value that must lose.
	push(e, b.AccessToken, r.Seq, nil, rec("library", w.Hash, "pinned", false, clock(now+20, 0, b.Device.SyncDeviceID)))
	old := push(e, a.AccessToken, 0, nil, rec("library", w.Hash, "pinned", true, clock(now+5, 0, a.Device.SyncDeviceID)))
	if old.Results[0].Result != "unchanged" {
		t.Fatalf("stale write must not win: %+v", old.Results[0])
	}

	ca, cb := pull(e, a.AccessToken, 0), pull(e, b.AccessToken, 0)
	ja, _ := json.Marshal(ca.Records)
	jb, _ := json.Marshal(cb.Records)
	if !bytes.Equal(ja, jb) || len(ca.Records) != 1 {
		t.Fatalf("devices diverged:\nA=%s\nB=%s", ja, jb)
	}
	f := ca.Records[0].Fields
	if string(f["pinned"]) != "false" || string(f["archived"]) != "true" {
		t.Fatalf("merged fields = %v", f)
	}

	// Another account never sees this data.
	c := e.register("carol")
	if other := pull(e, c.AccessToken, 0); len(other.Records) != 0 {
		t.Fatalf("data leaked across accounts: %+v", other)
	}
}

func TestDeleteThenReAddDropsOldFields(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	w := mkWork("7")
	now := nowMs()
	dev := s.Device.SyncDeviceID
	push(e, s.AccessToken, 0, []syncsvc.Work{w}, rec("library", w.Hash, "pinned", true, clock(now, 0, dev)))
	push(e, s.AccessToken, 0, nil, tombstone("library", w.Hash, clock(now+10, 0, dev)))
	back := syncsvc.PushRecord{Kind: "library", Key: w.Hash,
		Fields:  map[string]json.RawMessage{"addedAt": json.RawMessage("123")},
		Clocks:  map[string]string{"addedAt": clock(now+20, 0, dev)},
		Deleted: &syncsvc.PushDeleted{Value: ptr(false), Clock: clock(now+20, 0, dev)}}
	r := push(e, s.AccessToken, 0, nil, back).Results[0].Record
	if _, ok := r.Fields["pinned"]; ok || string(r.Fields["addedAt"]) != "123" || r.Deleted == nil || r.Deleted.Value ||
		r.Cleared != clock(now+10, 0, dev) {
		t.Fatalf("old fields must not resurrect: %+v", r)
	}
}

func TestBatchIdempotency(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	w := mkWork("1")
	batch := newBatchID()
	body := pushBody(0, []syncsvc.Work{w}, []syncsvc.PushRecord{rec("library", w.Hash, "pinned", true, clock(nowMs(), 0, s.Device.SyncDeviceID))}, batch)
	first := expect[syncsvc.PushResponse](e, 200, "POST", "/api/v1/sync/push", s.AccessToken, body)
	second := expect[syncsvc.PushResponse](e, 200, "POST", "/api/v1/sync/push", s.AccessToken, body)
	ja, _ := json.Marshal(first)
	jb, _ := json.Marshal(second)
	if !bytes.Equal(ja, jb) || first.Seq != 1 {
		t.Fatalf("replay differs:\n%s\n%s", ja, jb)
	}
	// Same batch id with different content still returns the stored response.
	body.Records = []syncsvc.PushRecord{rec("setting", "x", "value", 1, clock(nowMs(), 0, "d"))}
	third := expect[syncsvc.PushResponse](e, 200, "POST", "/api/v1/sync/push", s.AccessToken, body)
	if third.Seq != 1 || len(third.Results) != 1 || third.Results[0].Kind != "library" {
		t.Fatalf("batch id must be idempotent: %+v", third)
	}
	if n := len(pull(e, s.AccessToken, 0).Records); n != 1 {
		t.Fatalf("replay merged again: %d records", n)
	}
	// The same id used by another account is independent.
	o := e.register("bob")
	other := expect[syncsvc.PushResponse](e, 200, "POST", "/api/v1/sync/push", o.AccessToken, body)
	if other.Results[0].Kind != "setting" {
		t.Fatalf("batch ids are per user: %+v", other)
	}
	// After the retention window the batch is merged afresh.
	if n, err := e.jobs.GCBatches(context.Background()); err != nil || n < 2 {
		t.Fatalf("GCBatches = %d, %v", n, err)
	}
}

func TestClockSkewRejected(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	future := clock(nowMs()+10*60*1000, 0, s.Device.SyncDeviceID)
	r := push(e, s.AccessToken, 0, nil, rec("setting", "a", "value", 1, future))
	if r.Results[0].Result != "rejected:clock_skew" || r.Results[0].Record != nil || r.Seq != 0 {
		t.Fatalf("future clock must be rejected: %+v", r)
	}
	ok := clock(nowMs()+4*60*1000, 0, s.Device.SyncDeviceID)
	if r := push(e, s.AccessToken, 0, nil, rec("setting", "a", "value", 1, ok)); r.Results[0].Result != "applied" {
		t.Fatalf("4 minutes ahead is within tolerance: %+v", r)
	}
	// Other validation outcomes are per record and do not fail the batch.
	bad := push(e, s.AccessToken, 0, nil,
		syncsvc.PushRecord{Kind: "setting", Key: "b", Fields: map[string]json.RawMessage{"value": json.RawMessage("1")}},
		rec("setting", "c", "value", 1, "not-a-clock"),
		rec("setting", "d", "Bad_Field", 1, clock(nowMs(), 0, "d")),
		syncsvc.PushRecord{Kind: "setting", Key: "e"},
		rec("nonsense", "f", "value", 1, clock(nowMs(), 0, "d")),
		rec("setting", "g", "value", 1, clock(nowMs(), 0, "d")))
	want := []string{"rejected:missing_clock", "rejected:invalid_clock", "rejected:invalid_record", "rejected:invalid_record", "rejected:invalid_record", "applied"}
	for i, r := range bad.Results {
		if r.Result != want[i] {
			t.Errorf("result %d = %s, want %s", i, r.Result, want[i])
		}
	}
}

func TestWorkValidation(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	w := mkWork("100")
	c := clock(nowMs(), 0, s.Device.SyncDeviceID)

	// Library record for a work the server has never seen.
	r := push(e, s.AccessToken, 0, nil, rec("library", w.Hash, "pinned", true, c))
	if r.Results[0].Result != "rejected:unknown_work" {
		t.Fatalf("want unknown_work, got %+v", r.Results[0])
	}
	// Wrong hash.
	bad := w
	bad.Hash = strings.Repeat("a", 64)
	e.expectErr(422, "work_hash_mismatch", "POST", "/api/v1/sync/push", s.AccessToken,
		pushBody(0, []syncsvc.Work{bad}, nil, newBatchID()))
	// Invalid key components.
	blank := mkWork("x")
	blank.RemoteID = "   "
	e.expectErr(422, "invalid_work_key", "POST", "/api/v1/sync/push", s.AccessToken,
		pushBody(0, []syncsvc.Work{blank}, nil, newBatchID()))
	ctl := mkWork("x")
	ctl.RemoteID = "a\x01b"
	e.expectErr(422, "invalid_work_key", "POST", "/api/v1/sync/push", s.AccessToken,
		pushBody(0, []syncsvc.Work{ctl}, nil, newBatchID()))
	v2 := mkWork("x")
	v2.KeyVersion = 2
	e.expectErr(422, "invalid_work_key", "POST", "/api/v1/sync/push", s.AccessToken,
		pushBody(0, []syncsvc.Work{v2}, nil, newBatchID()))
	// Descriptor fields.
	for name, mut := range map[string]func(*syncsvc.Work){
		"empty title": func(w *syncsvc.Work) { w.Title = "" },
		"bad kind":    func(w *syncsvc.Work) { w.ContentKind = "video" },
		"http cover":  func(w *syncsvc.Work) { w.CoverURL = ptr("http://example.com/c.jpg") },
		"NUL title":   func(w *syncsvc.Work) { w.Title = "a\x00b" },
	} {
		x := mkWork("200")
		mut(&x)
		if st, _ := e.call("POST", "/api/v1/sync/push", s.AccessToken, pushBody(0, []syncsvc.Work{x}, nil, newBatchID())); st != 422 {
			t.Errorf("%s: status %d, want 422", name, st)
		}
	}
	// Nothing from the failed attempts was stored.
	if pull(e, s.AccessToken, 0).NextSince != 0 {
		t.Fatal("rejected batches must not create records")
	}

	// Once a work is known, later batches may reference it without resending the descriptor.
	push(e, s.AccessToken, 0, []syncsvc.Work{w}, rec("library", w.Hash, "pinned", true, c))
	r = push(e, s.AccessToken, 0, nil, rec("progress", w.Hash, "progression", 0.5, c))
	if r.Results[0].Result != "applied" || r.Results[0].Record.Work == nil || r.Results[0].Record.Work.Title != "Title 100" {
		t.Fatalf("known work should resolve: %+v", r.Results[0])
	}
	// The latest submitted descriptor wins.
	w2 := w
	w2.Title = "Renamed"
	push(e, s.AccessToken, 0, []syncsvc.Work{w2})
	if got := pull(e, s.AccessToken, 0).Records[0].Work.Title; got != "Renamed" {
		t.Fatalf("title = %q", got)
	}
}

func TestUnitKeyRecomputation(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	w := mkWork("500")
	c := clock(nowMs(), 0, s.Device.SyncDeviceID)
	good := rec("unit", unitKey(w, "ch-1"), "read", true, c)
	good.Work, good.UnitRemoteID = w.Hash, ptr("ch-1")

	wrongKey := rec("unit", unitKey(w, "ch-1"), "read", true, c)
	wrongKey.Work, wrongKey.UnitRemoteID = w.Hash, ptr("ch-2") // key does not belong to the unit id

	noUnit := rec("unit", unitKey(w, "ch-3"), "read", true, c)
	noUnit.Work = w.Hash

	noWork := rec("unit", unitKey(w, "ch-4"), "read", true, c)
	noWork.UnitRemoteID = ptr("ch-4")

	unknown := rec("unit", unitKey(mkWork("999"), "ch-1"), "read", true, c)
	unknown.Work, unknown.UnitRemoteID = mkWork("999").Hash, ptr("ch-1")

	blank := rec("unit", "whatever", "read", true, c)
	blank.Work, blank.UnitRemoteID = w.Hash, ptr("  ")

	r := push(e, s.AccessToken, 0, []syncsvc.Work{w}, good, wrongKey, noUnit, noWork, unknown, blank)
	want := []string{"applied", "rejected:invalid_key", "rejected:invalid_key", "rejected:invalid_key", "rejected:unknown_work", "rejected:invalid_key"}
	for i, res := range r.Results {
		if res.Result != want[i] {
			t.Errorf("result %d = %s, want %s", i, res.Result, want[i])
		}
	}
	rc := r.Results[0].Record
	if rc.UnitRemoteID != "ch-1" || rc.Work == nil || rc.Work.Hash != w.Hash {
		t.Fatalf("unit record = %+v", rc)
	}
	ch := pull(e, s.AccessToken, 0)
	if len(ch.Records) != 1 || ch.Records[0].UnitRemoteID != "ch-1" || ch.Records[0].Work == nil || ch.Records[0].Work.RemoteID != "500" {
		t.Fatalf("pulled unit = %+v", ch.Records)
	}
	// Referencing the work from an earlier batch works too.
	u2 := rec("unit", unitKey(w, "ch-9"), "bookmarked", true, c)
	u2.Work, u2.UnitRemoteID = w.Hash, ptr("ch-9")
	if r := push(e, s.AccessToken, 0, nil, u2); r.Results[0].Result != "applied" {
		t.Fatalf("%+v", r.Results[0])
	}
}

func TestKeyFormats(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	w := mkWork("1")
	c := clock(nowMs(), 0, s.Device.SyncDeviceID)
	uuid := "123e4567-e89b-42d3-a456-426614174000"
	cases := []struct {
		kind, key string
		ok        bool
	}{
		{"library", w.Hash, true},
		{"library", "local:" + uuid, true},
		{"progress", "local:" + uuid, true},
		{"history", "local:" + uuid, true},
		{"library", "local:" + strings.ToUpper(uuid), false},
		{"library", "local:not-a-uuid", false},
		{"library", strings.ToUpper(w.Hash), false},
		{"library", "short", false},
		{"category", "default", true},
		{"category", uuid, true},
		{"category", "Default", false},
		{"category", "local:" + uuid, false},
		{"repository", "https://repo.example.com/index.json", true},
		{"repository", "", false},
		{"repository", strings.Repeat("a", 2049), false},
		{"setting", "reader.fontSize", true},
		{"setting", "Reader", false},
		{"setting", "a b", false},
		{"setting", strings.Repeat("a", 129), false},
	}
	for _, tc := range cases {
		r := push(e, s.AccessToken, 0, []syncsvc.Work{w}, rec(tc.kind, tc.key, "value", 1, c))
		got := r.Results[0].Result
		if tc.ok && got != "applied" || !tc.ok && got != "rejected:invalid_key" {
			t.Errorf("%s %q: %s (ok=%v)", tc.kind, tc.key, got, tc.ok)
		}
	}
}

func TestPayloadLimits(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	c := clock(nowMs(), 0, "d")
	many := make([]syncsvc.PushRecord, 501)
	for i := range many {
		many[i] = rec("setting", fmt.Sprintf("k%d", i), "value", i, c)
	}
	e.expectErr(413, "payload_too_large", "POST", "/api/v1/sync/push", s.AccessToken, pushBody(0, nil, many, newBatchID()))
	if r := push(e, s.AccessToken, 0, nil, many[:500]...); r.Seq != 500 {
		t.Fatalf("500 records must be accepted, seq=%d", r.Seq)
	}

	big := strings.Repeat("x", 1<<20)
	e.expectErr(413, "payload_too_large", "POST", "/api/v1/sync/push", s.AccessToken,
		pushBody(0, nil, []syncsvc.PushRecord{rec("setting", "big", "value", big, c)}, newBatchID()))

	// A single record's fields are capped at 16 KiB.
	r := push(e, s.AccessToken, 0, nil, rec("setting", "huge", "value", strings.Repeat("y", 20*1024), c))
	if r.Results[0].Result != "rejected:invalid_record" {
		t.Fatalf("oversized fields: %+v", r.Results[0])
	}
	e.expectErr(422, "validation_failed", "POST", "/api/v1/sync/push", s.AccessToken, map[string]any{"batchId": "nope", "since": 0, "records": []any{}})
	e.expectErr(422, "validation_failed", "POST", "/api/v1/sync/push", s.AccessToken, map[string]any{"batchId": newBatchID(), "records": []any{}})

	// Changes pages never exceed 500 even when more is requested.
	ch := expect[syncsvc.ChangesResponse](e, 200, "GET", "/api/v1/sync/changes?since=0&limit=9999", s.AccessToken, nil)
	if len(ch.Records) != 500 || ch.HasMore {
		t.Fatalf("limit clamp: %d records, hasMore=%v", len(ch.Records), ch.HasMore)
	}
}

func TestJSONThatPostgresCannotStoreIsRejectedPerRecord(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	c := clock(nowMs(), 0, "d")
	mk := func(key, raw string) syncsvc.PushRecord {
		return syncsvc.PushRecord{Kind: "setting", Key: key, Fields: map[string]json.RawMessage{"value": json.RawMessage(raw)},
			Clocks: map[string]string{"value": c}}
	}
	r := push(e, s.AccessToken, 0, nil,
		mk("nul", `"a\u0000b"`), mk("lone", `"\ud800"`), mk("lone2", `"\udc00x"`), mk("pair", `"😀"`), mk("ok", `{"a":[1,"é\n"]}`))
	want := []string{"rejected:invalid_record", "rejected:invalid_record", "rejected:invalid_record", "applied", "applied"}
	for i, res := range r.Results {
		if res.Result != want[i] {
			t.Errorf("result %d = %s, want %s", i, res.Result, want[i])
		}
	}
}

func TestRecordQuota(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RecordQuota = 2 })
	s := e.register("alice")
	c := clock(nowMs(), 0, "d")
	r := push(e, s.AccessToken, 0, nil, rec("setting", "a", "value", 1, c), rec("setting", "b", "value", 1, c), rec("setting", "c", "value", 1, c))
	want := []string{"applied", "applied", "rejected:quota_exceeded"}
	for i, res := range r.Results {
		if res.Result != want[i] {
			t.Errorf("result %d = %s, want %s", i, res.Result, want[i])
		}
	}
	// Existing records can still be updated, and the quota is per user.
	if r := push(e, s.AccessToken, 0, nil, rec("setting", "a", "value", 2, clock(nowMs()+1, 0, "d"))); r.Results[0].Result != "applied" {
		t.Fatalf("update at quota: %+v", r.Results[0])
	}
	o := e.register("bob")
	if r := push(e, o.AccessToken, 0, nil, rec("setting", "a", "value", 1, c)); r.Results[0].Result != "applied" {
		t.Fatalf("other user: %+v", r.Results[0])
	}
	// A per-user override beats the default.
	if _, err := e.pool.Exec(context.Background(), `update users set record_quota = 3 where username = 'alice'`); err != nil {
		t.Fatal(err)
	}
	if r := push(e, s.AccessToken, 0, nil, rec("setting", "c", "value", 1, c)); r.Results[0].Result != "applied" {
		t.Fatalf("override: %+v", r.Results[0])
	}
}

func TestTombstoneGCForcesResync(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	ctx := context.Background()
	dev := s.Device.SyncDeviceID
	now := nowMs()
	w := mkWork("1")
	push(e, s.AccessToken, 0, []syncsvc.Work{w}, rec("library", w.Hash, "pinned", true, clock(now, 0, dev))) // seq 1
	push(e, s.AccessToken, 0, nil, rec("setting", "keep", "value", 1, clock(now, 1, dev)))                   // seq 2
	push(e, s.AccessToken, 0, nil, tombstone("library", w.Hash, clock(now+10, 0, dev)))                      // seq 3
	push(e, s.AccessToken, 0, nil, rec("setting", "later", "value", 1, clock(now, 2, dev)))                  // seq 4

	if n, err := e.jobs.GCTombstones(ctx); err != nil || n != 1 {
		t.Fatalf("GCTombstones = %d, %v", n, err)
	}
	var floor int64
	if err := e.pool.QueryRow(ctx, `select sync_floor_seq from users where username = 'alice'`).Scan(&floor); err != nil || floor != 3 {
		t.Fatalf("floor = %d, %v", floor, err)
	}

	e.expectErr(410, "resync_required", "GET", "/api/v1/sync/changes?since=1", s.AccessToken, nil)
	e.expectErr(410, "resync_required", "GET", "/api/v1/sync/changes?since=2", s.AccessToken, nil)
	e.expectErr(410, "resync_required", "POST", "/api/v1/sync/push", s.AccessToken,
		pushBody(2, nil, []syncsvc.PushRecord{rec("setting", "x", "value", 1, clock(now, 3, dev))}, newBatchID()))
	// A rejected push must not have merged anything.
	if got := pull(e, s.AccessToken, 3); len(got.Records) != 1 || got.Records[0].Key != "later" {
		t.Fatalf("changes since floor = %+v", got)
	}

	// Cursor == floor is fine, and since=0 is always a valid full pull (the recovery path).
	full := pull(e, s.AccessToken, 0)
	if len(full.Records) != 2 || full.Records[0].Key != "keep" || full.Records[1].Key != "later" {
		t.Fatalf("full pull = %+v", full)
	}
	if r := push(e, s.AccessToken, full.NextSince, nil, rec("setting", "x", "value", 1, clock(now, 3, dev))); r.Results[0].Result != "applied" {
		t.Fatalf("push after resync: %+v", r.Results[0])
	}
	// Seqs keep increasing past the removed ones.
	if got := pull(e, s.AccessToken, 4); len(got.Records) != 1 || got.Records[0].Seq != 5 {
		t.Fatalf("seq continuity: %+v", got)
	}
	// Live records and young tombstones are untouched.
	if n, _ := e.jobs.GCTombstones(ctx); n != 0 {
		t.Fatalf("nothing left to collect, got %d", n)
	}
}

func TestBatchReplayAfterGCReturnsStoredResponse(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	ctx := context.Background()
	dev := s.Device.SyncDeviceID
	now := nowMs()
	w := mkWork("1")
	push(e, s.AccessToken, 0, []syncsvc.Work{w}, rec("library", w.Hash, "pinned", true, clock(now, 0, dev))) // seq 1

	// The response to this batch is lost on the way back; the client will retry it verbatim.
	body := pushBody(1, nil, []syncsvc.PushRecord{rec("setting", "keep", "value", 1, clock(now, 1, dev))}, newBatchID())
	first := expect[syncsvc.PushResponse](e, 200, "POST", "/api/v1/sync/push", s.AccessToken, body) // seq 2

	push(e, s.AccessToken, 2, nil, tombstone("library", w.Hash, clock(now+10, 0, dev))) // seq 3
	if n, err := e.jobs.GCTombstones(ctx); err != nil || n != 1 {
		t.Fatalf("GCTombstones = %d, %v", n, err)
	}

	// The floor is now 3, above the retried batch's cursor, but the batch was already merged.
	second := expect[syncsvc.PushResponse](e, 200, "POST", "/api/v1/sync/push", s.AccessToken, body)
	ja, _ := json.Marshal(first)
	jb, _ := json.Marshal(second)
	if !bytes.Equal(ja, jb) {
		t.Fatalf("replay differs:\n%s\n%s", ja, jb)
	}
	// A new batch with the same stale cursor is still refused, and nothing was merged twice.
	e.expectErr(410, "resync_required", "POST", "/api/v1/sync/push", s.AccessToken,
		pushBody(1, nil, []syncsvc.PushRecord{rec("setting", "x", "value", 1, clock(now, 2, dev))}, newBatchID()))
	if full := pull(e, s.AccessToken, 0); len(full.Records) != 1 || full.Records[0].Key != "keep" || full.Records[0].Seq != 2 {
		t.Fatalf("full pull = %+v", full)
	}
}

func TestTombstoneRetentionRespected(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	ctx := context.Background()
	dev := s.Device.SyncDeviceID
	push(e, s.AccessToken, 0, nil, tombstone("setting", "old", clock(nowMs(), 0, dev)), tombstone("setting", "fresh", clock(nowMs(), 1, dev)))
	if _, err := e.pool.Exec(ctx, `update sync_records set updated_at = now() - interval '91 days' where key = 'old'`); err != nil {
		t.Fatal(err)
	}
	runner := newRunner(e, 90*24*3600)
	if n, err := runner.GCTombstones(ctx); err != nil || n != 1 {
		t.Fatalf("GCTombstones = %d, %v", n, err)
	}
	var left int
	_ = e.pool.QueryRow(ctx, `select count(*) from sync_records`).Scan(&left)
	if left != 1 {
		t.Fatalf("fresh tombstone must remain, %d rows left", left)
	}
}

func TestExpiredRefreshTokensAreCollected(t *testing.T) {
	e := newEnv(t)
	e.register("alice")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `update refresh_tokens set expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if n, err := e.jobs.GCRefreshTokens(ctx); err != nil || n != 1 {
		t.Fatalf("GCRefreshTokens = %d, %v", n, err)
	}
}

// TestConcurrentPushesAreCommitOrdered hammers one account from two goroutines while a third
// pulls continuously. If a larger seq ever became visible before a smaller one, the puller's
// cursor would jump past the smaller record and it would never see it.
func TestConcurrentPushesAreCommitOrdered(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	const workers, batches, perBatch = 2, 15, 7

	var wg sync.WaitGroup
	done := make(chan struct{})
	pulled := make(chan map[string]int, 1)

	go func() { // puller
		seen := map[string]int{}
		cursor := int64(0)
		finalPass := false
		for {
			select {
			case <-done:
				finalPass = true
			default:
			}
			ch := pull(e, s.AccessToken, cursor)
			last := cursor
			for _, r := range ch.Records {
				if r.Seq <= last {
					t.Errorf("seq not strictly increasing: %d after %d", r.Seq, last)
				}
				last = r.Seq
				seen[r.Key]++
			}
			cursor = ch.NextSince
			if finalPass && !ch.HasMore {
				pulled <- seen
				return
			}
		}
	}()

	var failures sync.Map
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dev := fmt.Sprintf("w%d", w)
			for b := range batches {
				recs := make([]syncsvc.PushRecord, perBatch)
				for i := range recs {
					recs[i] = rec("setting", fmt.Sprintf("w%d.b%d.i%d", w, b, i), "value", i, clock(nowMs(), b*perBatch+i, dev))
				}
				// Every batch also touches one shared record so the workers contend on the same row.
				recs = append(recs, rec("setting", "shared", "value", w, clock(nowMs(), b, dev)))
				status, raw := e.call("POST", "/api/v1/sync/push", s.AccessToken, pushBody(0, nil, recs, newBatchID()))
				if status != 200 {
					failures.Store(fmt.Sprintf("%d/%d", w, b), fmt.Sprintf("%d %s", status, raw))
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	seen := <-pulled
	failures.Range(func(k, v any) bool { t.Errorf("push %v failed: %v", k, v); return true })

	for w := range workers {
		for b := range batches {
			for i := range perBatch {
				key := fmt.Sprintf("w%d.b%d.i%d", w, b, i)
				if seen[key] != 1 {
					t.Errorf("puller saw %s %d times, want exactly once", key, seen[key])
				}
			}
		}
	}
	if seen["shared"] < 1 {
		t.Error("puller never saw the shared record")
	}
	var distinct, rows int64
	_ = e.pool.QueryRow(context.Background(), `select count(*), count(distinct seq) from sync_records`).Scan(&rows, &distinct)
	if rows != workers*batches*perBatch+1 || distinct != rows {
		t.Fatalf("rows=%d distinct seqs=%d", rows, distinct)
	}
}

// TestSeqIsGapFreeWithoutOverwrites checks the allocation itself: with only distinct keys the
// seqs of the stored rows are exactly 1..N.
func TestSeqIsGapFreeWithoutOverwrites(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range 10 {
				recs := []syncsvc.PushRecord{
					rec("setting", fmt.Sprintf("a.%d.%d", w, b), "value", 1, clock(nowMs(), 0, "d")),
					rec("setting", fmt.Sprintf("b.%d.%d", w, b), "value", 1, clock(nowMs(), 0, "d")),
				}
				status, raw := e.call("POST", "/api/v1/sync/push", s.AccessToken, pushBody(0, nil, recs, newBatchID()))
				if status != 200 {
					t.Errorf("push: %d %s", status, raw)
				}
			}
		}()
	}
	wg.Wait()
	var n, minSeq, maxSeq int64
	if err := e.pool.QueryRow(context.Background(), `select count(*), min(seq), max(seq) from sync_records`).Scan(&n, &minSeq, &maxSeq); err != nil {
		t.Fatal(err)
	}
	if n != 80 || minSeq != 1 || maxSeq != 80 {
		t.Fatalf("seqs not gap-free: n=%d min=%d max=%d", n, minSeq, maxSeq)
	}
}

func TestDuplicateKeyInOneBatchUsesOneSeq(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	now := nowMs()
	r := push(e, s.AccessToken, 0, nil,
		rec("setting", "a", "value", 1, clock(now, 0, "d")),
		rec("setting", "a", "value", 2, clock(now, 1, "d")),
		rec("setting", "b", "value", 1, clock(now, 2, "d")))
	if r.Seq != 2 || r.Results[0].Record.Seq != 1 || r.Results[1].Record.Seq != 1 || r.Results[2].Record.Seq != 2 {
		t.Fatalf("resp = %+v", r)
	}
	if got := pull(e, s.AccessToken, 0); len(got.Records) != 2 || string(got.Records[0].Fields["value"]) != "2" {
		t.Fatalf("changes = %+v", got)
	}
}

func ptr[T any](v T) *T { return &v }
