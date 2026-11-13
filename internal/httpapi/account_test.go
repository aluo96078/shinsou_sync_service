package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

type usage struct {
	Total  int            `json:"total"`
	Quota  *int           `json:"quota"`
	ByKind map[string]int `json:"byKind"`
}

func TestUsageAndQuotas(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	s := e.register("alice")

	u := expect[usage](e, 200, "GET", "/api/v1/me/usage", s.AccessToken, nil)
	if u.Total != 0 || u.Quota == nil || *u.Quota != 200000 || len(u.ByKind) != 8 {
		t.Fatalf("empty usage = %+v", u)
	}
	for _, k := range []string{"library", "unit", "progress", "history", "category", "repository", "setting", "package"} {
		if v, ok := u.ByKind[k]; !ok || v != 0 {
			t.Fatalf("kind %s missing or non-zero: %+v", k, u.ByKind)
		}
	}

	w := mkWork("1")
	clk := clock(nowMs(), 0, s.Device.SyncDeviceID)
	push(e, s.AccessToken, 0, []syncsvc.Work{w},
		recRaw("library", w.Hash, map[string]string{"addedAt": "1"}, clk),
		recRaw("history", w.Hash, map[string]string{"lastReadAt": "1"}, clk),
		unitRec(w, "ch-1", map[string]string{"read": "true"}, clk),
		recRaw("setting", "reader.fontSize", map[string]string{"value": "12"}, clk))
	push(e, s.AccessToken, 0, nil, tombstone("history", w.Hash, clock(nowMs()+5, 0, s.Device.SyncDeviceID)))
	u = expect[usage](e, 200, "GET", "/api/v1/me/usage", s.AccessToken, nil)
	if u.Total != 4 || u.ByKind["library"] != 1 || u.ByKind["history"] != 0 || u.ByKind["unit"] != 1 || u.ByKind["setting"] != 1 {
		t.Fatalf("usage = %+v (tombstones count in total, not in byKind)", u)
	}

	// A personal limit replaces the default; 0 means unlimited, reported as null.
	expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+s.User.ID, admin.AccessToken, map[string]any{"recordQuota": 4})
	u = expect[usage](e, 200, "GET", "/api/v1/me/usage", s.AccessToken, nil)
	if u.Quota == nil || *u.Quota != 4 {
		t.Fatalf("quota = %v", u.Quota)
	}
	r := push(e, s.AccessToken, 0, nil, recRaw("setting", "reader.other", map[string]string{"value": "1"}, clk))
	if r.Results[0].Result != "rejected:quota_exceeded" {
		t.Fatalf("the personal quota applies to sync: %+v", r.Results[0])
	}
	expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+s.User.ID, admin.AccessToken, map[string]any{"recordQuota": 0})
	u = expect[usage](e, 200, "GET", "/api/v1/me/usage", s.AccessToken, nil)
	if u.Quota != nil {
		t.Fatalf("unlimited must be null, got %d", *u.Quota)
	}
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me/usage", "", nil)
}

func TestExportContents(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	e.login("alice", "correct horse", "tablet")
	b := e.register("bob")
	w1, w2 := mkWork("1"), mkWork("2")
	clk := clock(nowMs(), 0, s.Device.SyncDeviceID)
	push(e, s.AccessToken, 0, []syncsvc.Work{w1, w2},
		recRaw("library", w1.Hash, map[string]string{"addedAt": "1", "title": `"ignored"`}, clk),
		unitRec(w1, "ch-1", map[string]string{"read": "true"}, clk),
		recRaw("library", w2.Hash, map[string]string{"addedAt": "2"}, clk),
		recRaw("setting", "reader.fontSize", map[string]string{"value": `{"nested":[1,2,{"x":"<b>&"}]}`}, clk))
	push(e, s.AccessToken, 0, nil, tombstone("library", w2.Hash, clock(nowMs()+5, 0, s.Device.SyncDeviceID)))
	push(e, b.AccessToken, 0, []syncsvc.Work{mkWork("9")}, recRaw("library", mkWork("9").Hash, map[string]string{"addedAt": "9"}, clock(nowMs(), 0, b.Device.SyncDeviceID)))

	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/me/export", nil)
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="shinsou-export-alice-`) || !strings.HasSuffix(cd, `.json"`) || len(cd) != len(`attachment; filename="shinsou-export-alice-20060102.json"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	var doc struct {
		ExportedAt int64 `json:"exportedAt"`
		Instance   struct {
			Name string  `json:"name"`
			URL  *string `json:"url"`
		} `json:"instance"`
		User struct {
			Username string `json:"username"`
			Email    any    `json:"email"`
		} `json:"user"`
		Devices []struct {
			Name    string `json:"name"`
			Current bool   `json:"current"`
		} `json:"devices"`
		Works   []syncsvc.Work `json:"works"`
		Records []struct {
			syncsvc.SyncRecord
			UpdatedAt int64 `json:"updatedAt"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.ExportedAt == 0 || doc.Instance.Name != "Shinsou Sync" || *doc.Instance.URL != "https://sync.test" || doc.User.Username != "alice" || doc.User.Email != nil {
		t.Fatalf("header part = %+v", doc)
	}
	if len(doc.Devices) != 2 {
		t.Fatalf("devices = %+v", doc.Devices)
	}
	if len(doc.Works) != 2 { // only the works alice's records refer to; bob's work is absent
		t.Fatalf("works = %+v", doc.Works)
	}
	if len(doc.Records) != 4 {
		t.Fatalf("records = %d, want 4 (tombstones included)", len(doc.Records))
	}
	seenTomb, seenUnit, seenSetting := false, false, false
	for _, r := range doc.Records {
		if r.UpdatedAt == 0 || r.Seq == 0 || len(r.Clocks) == 0 && r.Deleted == nil {
			t.Errorf("incomplete record: %+v", r)
		}
		switch {
		case r.Key == w2.Hash && r.Deleted != nil && r.Deleted.Value:
			seenTomb = true
		case r.Kind == "unit":
			seenUnit = r.UnitRemoteID == "ch-1" && r.Work != nil && r.Work.Hash == w1.Hash
		case r.Kind == "setting":
			seenSetting = strings.Contains(string(r.Fields["value"]), `"<b>&"`)
		}
	}
	if !seenTomb || !seenUnit || !seenSetting {
		t.Fatalf("tomb=%v unit=%v setting=%v", seenTomb, seenUnit, seenSetting)
	}
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me/export", "", nil)
}

func TestDeleteAccount(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	s := e.register("alice")
	s2 := e.login("alice", "correct horse", "tablet")
	w := mkWork("1")
	push(e, s.AccessToken, 0, []syncsvc.Work{w}, recFor(w, s.Device.SyncDeviceID)...)
	other := e.register("bob")
	push(e, other.AccessToken, 0, []syncsvc.Work{w}, recFor(w, other.Device.SyncDeviceID)...)

	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/me/delete", s.AccessToken, map[string]any{"currentPassword": "wrong one"})
	e.expectErr(401, "unauthorized", "POST", "/api/v1/me/delete", "", map[string]any{"currentPassword": "correct horse"})
	expect[map[string]any](e, 204, "POST", "/api/v1/me/delete", s.AccessToken, map[string]any{"currentPassword": "correct horse"})

	for _, tok := range []string{s.AccessToken, s2.AccessToken} {
		e.expectErr(401, "unauthorized", "GET", "/api/v1/me", tok, nil)
	}
	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "correct horse", "device": deviceInfo("x")})
	count := func(q string) int {
		var n int
		if err := e.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`select count(*) from users where username = 'alice'`) + count(`select count(*) from devices where user_id = '`+s.User.ID+`'`) +
		count(`select count(*) from sync_records where user_id = '`+s.User.ID+`'`) + count(`select count(*) from sync_batches where user_id = '`+s.User.ID+`'`); n != 0 {
		t.Fatalf("%d rows of the deleted account remain", n)
	}
	if n := count(`select count(*) from works`); n != 1 {
		t.Fatalf("shared work descriptions stay: %d", n)
	}
	if n := count(`select count(*) from sync_records where user_id = '` + other.User.ID + `'`); n != 2 {
		t.Fatalf("other accounts are untouched: %d", n)
	}
	au := auditEntries(e, admin.AccessToken)
	var found *auditEntry
	for i := range au {
		if au[i].Action == "account.delete" {
			found = &au[i]
		}
	}
	if found == nil || found.Detail["username"] != "alice" || found.TargetID == nil || *found.TargetID != s.User.ID {
		t.Fatalf("audit = %+v", au)
	}
	dump, _ := json.Marshal(au)
	if strings.Contains(string(dump), "correct horse") {
		t.Fatal("the audit log must not hold passwords")
	}
	// An administrator who is not the last one may delete their own account.
	e.promote("bob")
	expect[map[string]any](e, 204, "POST", "/api/v1/me/delete", other.AccessToken, map[string]any{"currentPassword": "correct horse"})
}
