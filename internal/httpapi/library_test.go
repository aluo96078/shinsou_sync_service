package httpapi_test

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

// recRaw builds a record from raw JSON field values so tests can push any shape.
func recRaw(kind, key string, fields map[string]string, clk string) syncsvc.PushRecord {
	r := syncsvc.PushRecord{Kind: kind, Key: key, Fields: map[string]json.RawMessage{}, Clocks: map[string]string{}}
	for k, v := range fields {
		r.Fields[k] = json.RawMessage(v)
		r.Clocks[k] = clk
	}
	return r
}

func unitRec(w syncsvc.Work, unit string, fields map[string]string, clk string) syncsvc.PushRecord {
	r := recRaw("unit", unitKey(w, unit), fields, clk)
	r.Work, r.UnitRemoteID = w.Hash, ptr(unit)
	return r
}

func workWith(id, title string, author *string, kind string) syncsvc.Work {
	w := mkWork(id)
	w.Title, w.Author, w.ContentKind = title, author, kind
	return w
}

type libItem struct {
	Key                 string   `json:"key"`
	Title               string   `json:"title"`
	Author              *string  `json:"author"`
	ContentKind         *string  `json:"contentKind"`
	Local               bool     `json:"local"`
	AddedAt             *int64   `json:"addedAt"`
	CategoryIDs         []string `json:"categoryIds"`
	Pinned              bool     `json:"pinned"`
	Archived            bool     `json:"archived"`
	TrackedUnits        int      `json:"trackedUnits"`
	ReadUnits           int      `json:"readUnits"`
	Progression         *float64 `json:"progression"`
	CurrentUnitRemoteID *string  `json:"currentUnitRemoteId"`
	LastReadAt          *int64   `json:"lastReadAt"`
	UpdatedAt           int64    `json:"updatedAt"`
}

type libPage struct {
	Items  []libItem `json:"items"`
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
	Limit  int       `json:"limit"`
}

func (e *env) library(token string, query url.Values) libPage {
	e.t.Helper()
	return expect[libPage](e, 200, "GET", "/api/v1/library?"+query.Encode(), token, nil)
}

func keysOf(p libPage) []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.Key)
	}
	return out
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLibraryView(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	clk := clock(nowMs(), 0, s.Device.SyncDeviceID)
	w1 := workWith("1", "Alpha", ptr("Ann"), "novel")
	w2 := workWith("2", "Beta", ptr("Bob"), "manga")
	w3 := workWith("3", "Gamma", nil, "manga")
	l1, l2, l3, l4 := "local:00000000-0000-4000-8000-000000000001", "local:00000000-0000-4000-8000-000000000002",
		"local:00000000-0000-4000-8000-000000000003", "local:00000000-0000-4000-8000-000000000004"
	recs := []syncsvc.PushRecord{
		recRaw("library", w1.Hash, map[string]string{"addedAt": "1000", "categoryIds": `["00000000-0000-4000-8000-0000000000c1"]`, "pinned": "true"}, clk),
		recRaw("library", w2.Hash, map[string]string{"addedAt": "3000", "archived": "true", "customTitle": `"Zeta custom"`}, clk),
		recRaw("library", w3.Hash, map[string]string{"addedAt": "2000", "categoryIds": `[]`}, clk),
		recRaw("library", l1, map[string]string{"addedAt": "500", "title": `"100% sure"`, "author": `"Zed"`, "contentKind": `"novel"`}, clk),
		recRaw("library", l2, map[string]string{"addedAt": "400", "title": `"1000 sure"`}, clk),
		recRaw("library", l3, map[string]string{"addedAt": "300", "title": `"a_c"`}, clk),
		recRaw("library", l4, map[string]string{"addedAt": "200", "title": `"abc"`}, clk),
		recRaw("progress", w1.Hash, map[string]string{"progression": "0.5", "unitRemoteId": `"ch-2"`}, clk),
		recRaw("history", w1.Hash, map[string]string{"lastReadAt": "9000"}, clk),
		recRaw("history", w3.Hash, map[string]string{"lastReadAt": "100"}, clk),
		unitRec(w1, "ch-1", map[string]string{"read": "true"}, clk),
		unitRec(w1, "ch-2", map[string]string{"read": "false", "bookmarked": "true", "lastPageIndex": "7"}, clk),
		unitRec(w1, "ch-3", map[string]string{"read": "true"}, clk),
		unitRec(w1, "ch-4", map[string]string{"read": "true"}, clk),
		recRaw("category", "00000000-0000-4000-8000-0000000000c1", map[string]string{"name": `"Reading"`, "order": "2"}, clk),
		recRaw("category", "00000000-0000-4000-8000-0000000000c2", map[string]string{"name": `"A shelf"`, "order": "2"}, clk),
		recRaw("category", "00000000-0000-4000-8000-0000000000c3", map[string]string{"name": `"First"`, "order": "-1"}, clk),
		recRaw("category", "00000000-0000-4000-8000-0000000000c4", map[string]string{"name": `"Gone"`, "order": "0"}, clk),
	}
	push(e, s.AccessToken, 0, []syncsvc.Work{w1, w2, w3}, recs...)
	// A removed chapter and a removed category must not show up.
	ch4 := tombstone("unit", unitKey(w1, "ch-4"), clock(nowMs()+10, 0, s.Device.SyncDeviceID))
	ch4.Work, ch4.UnitRemoteID = w1.Hash, ptr("ch-4")
	push(e, s.AccessToken, 0, nil, ch4, tombstone("category", "00000000-0000-4000-8000-0000000000c4", clock(nowMs()+10, 0, s.Device.SyncDeviceID)))

	q := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}

	// Default: archived excluded, most recent first (lastReadAt, else addedAt).
	all := e.library(s.AccessToken, q())
	if all.Total != 6 || all.Limit != 50 || all.Offset != 0 {
		t.Fatalf("unexpected page: %+v", all)
	}
	want := []string{w1.Hash, l1, l2, l3, l4, w3.Hash} // 9000, 500, 400, 300, 200, 100
	if !sameList(keysOf(all), want) {
		t.Fatalf("recent order = %v, want %v", keysOf(all), want)
	}
	it := all.Items[0]
	if it.Title != "Alpha" || *it.Author != "Ann" || *it.ContentKind != "novel" || it.Local || it.TrackedUnits != 3 || it.ReadUnits != 2 ||
		*it.Progression != 0.5 || *it.CurrentUnitRemoteID != "ch-2" || *it.LastReadAt != 9000 || !it.Pinned ||
		len(it.CategoryIDs) != 1 || it.CategoryIDs[0] != "00000000-0000-4000-8000-0000000000c1" || *it.AddedAt != 1000 || it.UpdatedAt == 0 {
		t.Fatalf("first item = %+v", it)
	}
	loc := all.Items[1]
	if !loc.Local || loc.Title != "100% sure" || *loc.Author != "Zed" || loc.TrackedUnits != 0 || loc.Progression != nil || loc.LastReadAt != nil {
		t.Fatalf("local item = %+v", loc)
	}

	// archived modes; a custom title wins over the work title
	if got := e.library(s.AccessToken, q("archived", "include")); got.Total != 7 {
		t.Fatalf("include: %d", got.Total)
	}
	only := e.library(s.AccessToken, q("archived", "only"))
	if only.Total != 1 || only.Items[0].Title != "Zeta custom" || !only.Items[0].Archived {
		t.Fatalf("only: %+v", only)
	}

	// search is case-insensitive, covers the author, and treats % and _ literally
	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"ALPHA", []string{w1.Hash}},
		{"ann", []string{w1.Hash}},
		{"100%", []string{l1}},
		{"%", []string{l1}},
		{"a_c", []string{l3}},
		{"_", []string{l3}},
		{`\`, []string{}},
		{"nothing like this", []string{}},
	} {
		got := e.library(s.AccessToken, q("q", tc.q))
		if !sameList(keysOf(got), tc.want) || got.Total != len(tc.want) {
			t.Errorf("q=%q: got %v (total %d), want %v", tc.q, keysOf(got), got.Total, tc.want)
		}
	}

	// categories: a key, and "default" for entries without any
	if got := e.library(s.AccessToken, q("category", "00000000-0000-4000-8000-0000000000c1")); !sameList(keysOf(got), []string{w1.Hash}) {
		t.Fatalf("category c1: %v", keysOf(got))
	}
	if got := e.library(s.AccessToken, q("category", "default")); !sameList(keysOf(got), []string{l1, l2, l3, l4, w3.Hash}) {
		t.Fatalf("category default: %v", keysOf(got))
	}
	if got := e.library(s.AccessToken, q("category", "missing")); got.Total != 0 {
		t.Fatalf("unknown category: %+v", got)
	}

	// sorting
	// Digits sort before letters in every collation; the order inside a group depends on the
	// database collation, so only the groups are asserted.
	byTitle := keysOf(e.library(s.AccessToken, q("sort", "title")))
	grp := func(keys ...string) map[string]bool {
		m := map[string]bool{}
		for _, k := range keys {
			m[k] = true
		}
		return m
	}
	if len(byTitle) != 6 || !grp(l1, l2)[byTitle[0]] || !grp(l1, l2)[byTitle[1]] || !grp(l3, l4)[byTitle[2]] || !grp(l3, l4)[byTitle[3]] ||
		byTitle[4] != w1.Hash || byTitle[5] != w3.Hash {
		t.Fatalf("title order = %v", byTitle)
	}
	if got := e.library(s.AccessToken, q("sort", "added")); !sameList(keysOf(got), []string{w3.Hash, w1.Hash, l1, l2, l3, l4}) {
		t.Fatalf("added order = %v", keysOf(got))
	}

	// paging is stable and total ignores the window
	p1 := e.library(s.AccessToken, q("limit", "2"))
	p2 := e.library(s.AccessToken, q("limit", "2", "offset", "2"))
	p9 := e.library(s.AccessToken, q("limit", "2", "offset", "50"))
	if p1.Total != 6 || p2.Total != 6 || p9.Total != 6 || len(p9.Items) != 0 || !sameList(append(keysOf(p1), keysOf(p2)...), want[:4]) {
		t.Fatalf("paging: %v %v %+v", keysOf(p1), keysOf(p2), p9)
	}
	for _, bad := range []string{"limit=0", "limit=101", "limit=x", "offset=-1", "sort=sideways", "archived=maybe"} {
		e.expectErr(400, "bad_request", "GET", "/api/v1/library?"+bad, s.AccessToken, nil)
	}

	// detail
	d := expect[struct {
		libItem
		Work  *syncsvc.Work `json:"work"`
		Units []struct {
			UnitRemoteID string `json:"unitRemoteId"`
			Read         bool   `json:"read"`
			Bookmarked   bool   `json:"bookmarked"`
			LastPageIdx  *int64 `json:"lastPageIndex"`
		} `json:"units"`
	}](e, 200, "GET", "/api/v1/library/"+w1.Hash, s.AccessToken, nil)
	if d.Title != "Alpha" || d.Work == nil || d.Work.Hash != w1.Hash || len(d.Units) != 3 || d.TrackedUnits != 3 || d.ReadUnits != 2 {
		t.Fatalf("detail = %+v", d)
	}
	found := false
	for _, u := range d.Units {
		if u.UnitRemoteID == "ch-2" {
			found = true
			if u.Read || !u.Bookmarked || u.LastPageIdx == nil || *u.LastPageIdx != 7 {
				t.Fatalf("ch-2 = %+v", u)
			}
		}
		if u.UnitRemoteID == "ch-4" {
			t.Fatal("deleted chapters are not listed")
		}
	}
	if !found {
		t.Fatal("ch-2 missing")
	}
	ld := expect[map[string]any](e, 200, "GET", "/api/v1/library/"+l1, s.AccessToken, nil)
	if ld["work"] != nil || len(ld["units"].([]any)) != 0 || ld["local"] != true {
		t.Fatalf("local detail = %v", ld)
	}
	e.expectErr(404, "not_found", "GET", "/api/v1/library/"+mkWork("404").Hash, s.AccessToken, nil)
	push(e, s.AccessToken, 0, nil, tombstone("library", w3.Hash, clock(nowMs()+20, 0, s.Device.SyncDeviceID)))
	e.expectErr(404, "not_found", "GET", "/api/v1/library/"+w3.Hash, s.AccessToken, nil)
	if got := e.library(s.AccessToken, q()); got.Total != 5 {
		t.Fatalf("removed entries are not listed: %d", got.Total)
	}

	// categories ordered by order then name, tombstones skipped
	cats := expect[struct {
		Categories []struct {
			Key   string `json:"key"`
			Name  string `json:"name"`
			Order int64  `json:"order"`
		} `json:"categories"`
	}](e, 200, "GET", "/api/v1/categories", s.AccessToken, nil)
	keys := []string{}
	for _, c := range cats.Categories {
		keys = append(keys, c.Key)
	}
	if !sameList(keys, []string{"00000000-0000-4000-8000-0000000000c3", "00000000-0000-4000-8000-0000000000c2", "00000000-0000-4000-8000-0000000000c1"}) || cats.Categories[1].Name != "A shelf" {
		t.Fatalf("categories = %+v", cats.Categories)
	}

	// another account sees nothing of this
	b := e.register("bob")
	if got := e.library(b.AccessToken, q()); got.Total != 0 {
		t.Fatalf("isolation: %+v", got)
	}
	e.expectErr(404, "not_found", "GET", "/api/v1/library/"+w1.Hash, b.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/library", "", nil)
}

func TestLibraryToleratesGarbageFields(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	clk := clock(nowMs(), 0, s.Device.SyncDeviceID)
	w := workWith("1", "Real title", ptr("Real author"), "novel")
	local := "local:00000000-0000-4000-8000-0000000000aa"
	garbage := map[string]string{
		"customTitle": "123", "addedAt": `"tomorrow"`, "categoryIds": `"x"`, "pinned": `"yes"`, "archived": `[true]`,
	}
	recs := []syncsvc.PushRecord{
		recRaw("library", w.Hash, garbage, clk),
		recRaw("progress", w.Hash, map[string]string{"progression": `"half"`, "unitRemoteId": "5", "locator": `"x"`}, clk),
		recRaw("history", w.Hash, map[string]string{"lastReadAt": `{}`, "unitRemoteId": "[]"}, clk),
		unitRec(w, "ch-1", map[string]string{"read": `"true"`, "bookmarked": "1", "lastPageIndex": `"x"`}, clk),
		unitRec(w, "ch-2", map[string]string{"read": "true", "lastPageIndex": "1e300"}, clk),
		unitRec(w, "ch-3", map[string]string{"read": "null", "lastPageIndex": "2.9"}, clk),
		recRaw("library", local, map[string]string{"title": "5", "author": `["a"]`, "contentKind": `"comic"`, "addedAt": "1e300",
			"categoryIds": `["ok", 1, null, {"a": 1}, ["n"], true]`, "pinned": "null", "archived": "0"}, clk),
		recRaw("progress", local, map[string]string{"progression": "1e300"}, clk),
		recRaw("history", local, map[string]string{"lastReadAt": "12345678901234567890123"}, clk),
		recRaw("category", "00000000-0000-4000-8000-0000000000b1", map[string]string{"name": "5", "order": `"x"`}, clk),
		recRaw("category", "00000000-0000-4000-8000-0000000000b2", map[string]string{"name": `{}`, "order": "1e300"}, clk),
		recRaw("category", "00000000-0000-4000-8000-0000000000b3", map[string]string{"name": `"fine"`, "order": "3.7"}, clk),
	}
	for _, r := range push(e, s.AccessToken, 0, []syncsvc.Work{w}, recs...).Results {
		if r.Result != "applied" {
			t.Fatalf("setup push: %+v", r)
		}
	}
	// Pushed progression above 1 is stored as sent; the view clamps it.
	push(e, s.AccessToken, 0, nil, recRaw("progress", "local:00000000-0000-4000-8000-0000000000bb", map[string]string{"progression": "7"}, clk),
		recRaw("library", "local:00000000-0000-4000-8000-0000000000bb", map[string]string{"title": `"Clamped"`}, clk))

	for _, archived := range []string{"exclude", "include", "only"} {
		for _, sort := range []string{"recent", "title", "added"} {
			e.library(s.AccessToken, url.Values{"archived": {archived}, "sort": {sort}, "q": {"x"}, "category": {"ok"}})
		}
	}
	all := e.library(s.AccessToken, url.Values{"archived": {"include"}})
	if all.Total != 3 {
		t.Fatalf("total = %d", all.Total)
	}
	by := map[string]libItem{}
	for _, it := range all.Items {
		by[it.Key] = it
	}
	r := by[w.Hash]
	if r.Title != "Real title" || r.AddedAt != nil || len(r.CategoryIDs) != 0 || r.Pinned || r.Archived || r.Progression != nil ||
		r.CurrentUnitRemoteID != nil || r.LastReadAt != nil || r.ReadUnits != 1 || r.TrackedUnits != 3 {
		t.Fatalf("work entry = %+v", r)
	}
	l := by[local]
	if l.Title != "" || l.Author != nil || l.ContentKind != nil || l.AddedAt != nil || !sameList(l.CategoryIDs, []string{"ok"}) ||
		l.Pinned || l.Archived || l.Progression != nil || l.LastReadAt != nil {
		t.Fatalf("local entry = %+v", l)
	}
	if c := by["local:00000000-0000-4000-8000-0000000000bb"]; c.Progression == nil || *c.Progression != 1 {
		t.Fatalf("clamped = %+v", c)
	}
	if got := e.library(s.AccessToken, url.Values{"category": {"ok"}}); got.Total != 1 {
		t.Fatalf("category filter on mixed array: %+v", got)
	}
	if got := e.library(s.AccessToken, url.Values{"category": {"default"}}); got.Total != 2 {
		t.Fatalf("garbage categoryIds count as no category: %+v", got)
	}

	d := expect[struct {
		Units []struct {
			UnitRemoteID string `json:"unitRemoteId"`
			Read         bool   `json:"read"`
			Bookmarked   bool   `json:"bookmarked"`
			LastPageIdx  *int64 `json:"lastPageIndex"`
		} `json:"units"`
	}](e, 200, "GET", "/api/v1/library/"+w.Hash, s.AccessToken, nil)
	if len(d.Units) != 3 {
		t.Fatalf("units = %+v", d.Units)
	}
	for _, u := range d.Units {
		switch u.UnitRemoteID {
		case "ch-1":
			if u.Read || u.Bookmarked || u.LastPageIdx != nil {
				t.Errorf("ch-1 = %+v", u)
			}
		case "ch-2":
			if !u.Read || u.LastPageIdx != nil {
				t.Errorf("ch-2 = %+v", u)
			}
		case "ch-3":
			if u.Read || u.LastPageIdx == nil || *u.LastPageIdx != 2 {
				t.Errorf("ch-3 = %+v", u)
			}
		}
	}
	expect[map[string]any](e, 200, "GET", "/api/v1/library/"+local, s.AccessToken, nil)

	cats := expect[struct {
		Categories []struct {
			Key   string `json:"key"`
			Name  string `json:"name"`
			Order int64  `json:"order"`
		} `json:"categories"`
	}](e, 200, "GET", "/api/v1/categories", s.AccessToken, nil)
	if len(cats.Categories) != 3 {
		t.Fatalf("categories = %+v", cats.Categories)
	}
	for _, c := range cats.Categories {
		switch c.Key {
		case "00000000-0000-4000-8000-0000000000b1", "00000000-0000-4000-8000-0000000000b2":
			if c.Name != "" || c.Order != 0 {
				t.Errorf("%s = %+v", c.Key, c)
			}
		case "00000000-0000-4000-8000-0000000000b3":
			if c.Name != "fine" || c.Order != 3 {
				t.Errorf("b3 = %+v", c)
			}
		}
	}
}

// A descriptor pushed by a device that does not have the package installed carries no source
// data; it must not erase what another device already told the server. Descriptors can also be
// pushed on their own, without records.
func TestWorkDescriptorKeepsKnownSourceData(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	clk := clock(nowMs(), 0, s.Device.SyncDeviceID)
	w := workWith("1", "Alpha", nil, "novel")
	push(e, s.AccessToken, 0, []syncsvc.Work{w}, recRaw("library", w.Hash, map[string]string{"addedAt": "1000"}, clk))

	type detail struct {
		SourceName *string       `json:"sourceName"`
		Language   *string       `json:"language"`
		Work       *syncsvc.Work `json:"work"`
	}
	get := func() detail {
		return expect[detail](e, 200, "GET", "/api/v1/library/"+w.Hash, s.AccessToken, nil)
	}
	if d := get(); d.SourceName != nil || d.Work == nil || d.Work.PackageID != w.PackageID {
		t.Fatalf("before: %+v", d)
	}

	described := w
	described.SourceName, described.Language, described.RepositoryURL = ptr("Wenku8"), ptr("zh-Hant"), ptr("https://example.org/repo.json")
	push(e, s.AccessToken, 0, []syncsvc.Work{described})
	if d := get(); d.SourceName == nil || *d.SourceName != "Wenku8" || d.Language == nil || *d.Language != "zh-Hant" ||
		d.Work.RepositoryURL == nil || *d.Work.RepositoryURL != "https://example.org/repo.json" {
		t.Fatalf("after a descriptor-only push: %+v", d)
	}

	renamed := w
	renamed.Title = "Alpha 2"
	push(e, s.AccessToken, 0, []syncsvc.Work{renamed})
	d := get()
	if d.Work.Title != "Alpha 2" || d.SourceName == nil || *d.SourceName != "Wenku8" || d.Work.RepositoryURL == nil {
		t.Fatalf("after a push without source data: %+v", d)
	}

	// A record the server answers with carries the merged descriptor, not the pushed one.
	stale := clock(nowMs()-60_000, 0, s.Device.SyncDeviceID)
	res := push(e, s.AccessToken, 0, []syncsvc.Work{renamed}, recRaw("library", w.Hash, map[string]string{"addedAt": "1"}, stale))
	if len(res.Results) != 1 || res.Results[0].Record == nil || res.Results[0].Record.Work == nil {
		t.Fatalf("expected the server's record back: %+v", res.Results)
	}
	if got := res.Results[0].Record.Work; got.SourceName == nil || *got.SourceName != "Wenku8" ||
		got.Language == nil || got.RepositoryURL == nil {
		t.Fatalf("push result lost the stored source data: %+v", got)
	}
}
