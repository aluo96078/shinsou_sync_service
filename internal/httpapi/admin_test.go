package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/httpapi"
	"github.com/aluo96078/shinsou_sync_service/internal/testutil"
)

type adminUser struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	Email         *string `json:"email"`
	EmailVerified bool    `json:"emailVerified"`
	Role          string  `json:"role"`
	Status        string  `json:"status"`
	LastSeenAt    *int64  `json:"lastSeenAt"`
	DeviceCount   int     `json:"deviceCount"`
	RecordCount   int     `json:"recordCount"`
	RecordQuota   *int    `json:"recordQuota"`
}

type userPage struct {
	Items  []adminUser `json:"items"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
	Limit  int         `json:"limit"`
}

func TestAdminEndpointsAreAdminOnly(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	user := e.register("alice")
	mod := e.register("mallory")
	if err := setRole(e, "mallory", "moderator"); err != nil {
		t.Fatal(err)
	}
	id := admin.User.ID
	endpoints := []struct{ method, path string }{
		{"GET", "/api/v1/admin/overview"},
		{"GET", "/api/v1/admin/users"},
		{"PATCH", "/api/v1/admin/users/" + id},
		{"DELETE", "/api/v1/admin/users/" + id},
		{"POST", "/api/v1/admin/users/" + id + "/sign-out"},
		{"GET", "/api/v1/admin/invites"},
		{"POST", "/api/v1/admin/invites"},
		{"DELETE", "/api/v1/admin/invites/" + id},
		{"GET", "/api/v1/admin/settings"},
		{"PATCH", "/api/v1/admin/settings"},
		{"GET", "/api/v1/admin/audit"},
	}
	for _, ep := range endpoints {
		e.expectErr(401, "unauthorized", ep.method, ep.path, "", map[string]any{"role": "user"})
		e.expectErr(403, "forbidden", ep.method, ep.path, user.AccessToken, map[string]any{"role": "user"})
		e.expectErr(403, "forbidden", ep.method, ep.path, mod.AccessToken, map[string]any{"role": "user"})
	}
	// The role is read from the database, not from the token: demotion bites at once.
	if err := setRole(e, "root", "user"); err != nil {
		t.Fatal(err)
	}
	e.expectErr(403, "forbidden", "GET", "/api/v1/admin/overview", admin.AccessToken, nil)
}

func setRole(e *env, username, role string) error {
	_, err := e.pool.Exec(context.Background(), `update users set role = $2 where username = $1`, username, role)
	return err
}

func TestAdminOverviewAndUsers(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	alice := e.register("alice")
	e.register("bob_x")
	e.register("bobxx")
	e.registerWith("carol", "carol@example.com")
	e.login("alice", "correct horse", "tablet")
	w := mkWork("1")
	push(e, alice.AccessToken, 0, []syncsvcWork{w}, recFor(w, alice.Device.SyncDeviceID)...)

	o := expect[struct {
		Version string `json:"version"`
		Users   struct {
			Total, Active, Suspended, Admins int
		} `json:"users"`
		Devices        int  `json:"devices"`
		Records        int  `json:"records"`
		Works          int  `json:"works"`
		EmailAvailable bool `json:"emailAvailable"`
	}](e, 200, "GET", "/api/v1/admin/overview", admin.AccessToken, nil)
	if o.Version != "test" || o.Users.Total != 5 || o.Users.Active != 5 || o.Users.Admins != 1 || o.Devices != 6 || o.Records != 2 || o.Works != 1 || o.EmailAvailable {
		t.Fatalf("overview = %+v", o)
	}

	page := expect[userPage](e, 200, "GET", "/api/v1/admin/users", admin.AccessToken, nil)
	if page.Total != 5 || len(page.Items) != 5 || page.Limit != 50 {
		t.Fatalf("users = %+v", page)
	}
	for _, u := range page.Items {
		if u.Username == "alice" && (u.DeviceCount != 2 || u.RecordCount != 2 || u.LastSeenAt == nil || u.RecordQuota != nil) {
			t.Fatalf("alice = %+v", u)
		}
	}
	// "_" matches literally, and the search covers the email.
	if got := expect[userPage](e, 200, "GET", "/api/v1/admin/users?q=bob_x", admin.AccessToken, nil); got.Total != 1 || got.Items[0].Username != "bob_x" {
		t.Fatalf("q=bob_x: %+v", got)
	}
	if got := expect[userPage](e, 200, "GET", "/api/v1/admin/users?q=%25", admin.AccessToken, nil); got.Total != 0 {
		t.Fatalf("q=%%: %+v", got)
	}
	if got := expect[userPage](e, 200, "GET", "/api/v1/admin/users?q=CAROL%40example", admin.AccessToken, nil); got.Total != 1 {
		t.Fatalf("email search: %+v", got)
	}
	if got := expect[userPage](e, 200, "GET", "/api/v1/admin/users?limit=2&offset=4", admin.AccessToken, nil); got.Total != 5 || len(got.Items) != 1 || got.Offset != 4 || got.Limit != 2 {
		t.Fatalf("paging: %+v", got)
	}
	e.expectErr(400, "bad_request", "GET", "/api/v1/admin/users?limit=500", admin.AccessToken, nil)
}

func recFor(w syncsvcWork, dev string) []syncsvcRecord {
	c := clock(nowMs(), 0, dev)
	return []syncsvcRecord{recRaw("library", w.Hash, map[string]string{"addedAt": "1"}, c), recRaw("history", w.Hash, map[string]string{"lastReadAt": "1"}, c)}
}

func TestAdminUpdateUser(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	alice := e.registerWith("alice", "alice@example.com")
	bob := e.register("bob")
	patch := func(id string, body map[string]any) (int, []byte) {
		return e.call("PATCH", "/api/v1/admin/users/"+id, admin.AccessToken, body)
	}

	u := expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+alice.User.ID, admin.AccessToken, map[string]any{"role": "moderator", "emailVerified": true, "recordQuota": 10})
	if u.Role != "moderator" || !u.EmailVerified || u.RecordQuota == nil || *u.RecordQuota != 10 {
		t.Fatalf("patched = %+v", u)
	}
	u = expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+alice.User.ID, admin.AccessToken, map[string]any{"recordQuota": nil})
	if u.RecordQuota != nil || u.Role != "moderator" {
		t.Fatalf("quota must reset to the instance default: %+v", u)
	}
	u = expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+alice.User.ID, admin.AccessToken, map[string]any{"recordQuota": 0})
	if u.RecordQuota == nil || *u.RecordQuota != 0 {
		t.Fatalf("0 means unlimited and is stored: %+v", u)
	}

	for name, body := range map[string]map[string]any{
		"empty":                  {},
		"bad role":               {"role": "owner"},
		"bad status":             {"status": "banned"},
		"negative quota":         {"recordQuota": -1},
		"verified without email": {"emailVerified": true},
	} {
		target := alice.User.ID
		if name == "verified without email" {
			target = bob.User.ID
		}
		if st, raw := patch(target, body); st != 422 {
			t.Errorf("%s: status %d %s", name, st, raw)
		}
	}
	if st, _ := patch(alice.User.ID, map[string]any{"role": 5}); st != 400 {
		t.Errorf("wrong type: %d", st)
	}
	e.expectErr(404, "not_found", "PATCH", "/api/v1/admin/users/00000000-0000-4000-8000-000000000000", admin.AccessToken, map[string]any{"role": "user"})
	e.expectErr(404, "not_found", "PATCH", "/api/v1/admin/users/not-a-uuid", admin.AccessToken, map[string]any{"role": "user"})

	// Suspending signs the user out everywhere and blocks login; reactivating allows it again.
	b2 := e.login("bob", "correct horse", "tablet")
	expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+bob.User.ID, admin.AccessToken, map[string]any{"status": "suspended"})
	for _, tok := range []string{bob.AccessToken, b2.AccessToken} {
		e.expectErr(401, "unauthorized", "GET", "/api/v1/me", tok, nil)
	}
	e.expectErr(401, "unauthorized", "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": b2.RefreshToken})
	e.expectErr(403, "forbidden", "POST", "/api/v1/auth/login", "", map[string]any{"username": "bob", "password": "correct horse", "device": deviceInfo("x")})
	expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+bob.User.ID, admin.AccessToken, map[string]any{"status": "active"})
	e.login("bob", "correct horse", "again")
}

func TestAdminSignOutAndDeleteUser(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	alice := e.register("alice")
	a2 := e.login("alice", "correct horse", "tablet")
	w := mkWork("1")
	push(e, alice.AccessToken, 0, []syncsvcWork{w}, recFor(w, alice.Device.SyncDeviceID)...)

	expect[map[string]any](e, 204, "POST", "/api/v1/admin/users/"+alice.User.ID+"/sign-out", admin.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", alice.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", a2.AccessToken, nil)
	e.login("alice", "correct horse", "back") // signing in again is allowed
	e.expectErr(404, "not_found", "POST", "/api/v1/admin/users/00000000-0000-4000-8000-000000000000/sign-out", admin.AccessToken, nil)

	expect[map[string]any](e, 204, "DELETE", "/api/v1/admin/users/"+alice.User.ID, admin.AccessToken, nil)
	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "correct horse", "device": deviceInfo("x")})
	for _, table := range []string{"devices", "sync_records", "refresh_tokens"} {
		var n int
		q := fmt.Sprintf(`select count(*) from %s`, table)
		if table == "sync_records" || table == "devices" {
			q += ` where user_id = '` + alice.User.ID + `'`
		}
		if err := e.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 && table != "refresh_tokens" {
			t.Errorf("%s still has %d rows of the deleted user", table, n)
		}
	}
	e.expectErr(404, "not_found", "DELETE", "/api/v1/admin/users/"+alice.User.ID, admin.AccessToken, nil)

	au := auditEntries(e, admin.AccessToken)
	for _, want := range []string{"user.sign_out", "user.delete"} {
		if !hasAction(au, want) {
			t.Errorf("audit lacks %s", want)
		}
	}
}

type auditEntry struct {
	ID         int64                          `json:"id"`
	Actor      *struct{ ID, Username string } `json:"actor"`
	Action     string                         `json:"action"`
	TargetType *string                        `json:"targetType"`
	TargetID   *string                        `json:"targetId"`
	Detail     map[string]any                 `json:"detail"`
	CreatedAt  int64                          `json:"createdAt"`
}

func auditEntries(e *env, token string) []auditEntry {
	e.t.Helper()
	page := expect[struct {
		Items []auditEntry `json:"items"`
		Total int          `json:"total"`
	}](e, 200, "GET", "/api/v1/admin/audit?limit=100", token, nil)
	return page.Items
}

func hasAction(list []auditEntry, action string) bool {
	for _, a := range list {
		if a.Action == action {
			return true
		}
	}
	return false
}

func TestLastAdminRule(t *testing.T) {
	e := newEnv(t)
	root := e.registerAdmin("root")
	second := e.register("second")
	e.promote("second")

	// A single administrator can neither demote, suspend nor delete themselves.
	solo := newEnv(t)
	soloRoot := solo.registerAdmin("root")
	for name, body := range map[string]map[string]any{"demote": {"role": "user"}, "moderator": {"role": "moderator"}, "suspend": {"status": "suspended"}} {
		solo.expectErr(409, "last_admin", "PATCH", "/api/v1/admin/users/"+soloRoot.User.ID, soloRoot.AccessToken, body)
		_ = name
	}
	solo.expectErr(409, "last_admin", "DELETE", "/api/v1/admin/users/"+soloRoot.User.ID, soloRoot.AccessToken, nil)
	solo.expectErr(409, "last_admin", "POST", "/api/v1/me/delete", soloRoot.AccessToken, map[string]any{"currentPassword": "correct horse"})
	// Harmless changes to the last admin are fine.
	expect[adminUser](solo, 200, "PATCH", "/api/v1/admin/users/"+soloRoot.User.ID, soloRoot.AccessToken, map[string]any{"role": "admin", "recordQuota": 5})

	// With a second active administrator the first may step down...
	expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+second.User.ID, root.AccessToken, map[string]any{"role": "user"})
	// ...and a suspended administrator does not count as active.
	e.promote("second")
	expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+second.User.ID, root.AccessToken, map[string]any{"status": "suspended"})
	e.expectErr(409, "last_admin", "PATCH", "/api/v1/admin/users/"+root.User.ID, root.AccessToken, map[string]any{"role": "user"})
	e.expectErr(409, "last_admin", "DELETE", "/api/v1/admin/users/"+root.User.ID, root.AccessToken, nil)
	// The suspended administrator can be deleted: it was not the one holding the instance up.
	expect[map[string]any](e, 204, "DELETE", "/api/v1/admin/users/"+second.User.ID, root.AccessToken, nil)
}

func TestLastAdminSurvivesConcurrentDemotions(t *testing.T) {
	for round := 0; round < 5; round++ {
		e := newEnv(t)
		a := e.registerAdmin("adm_a")
		b := e.register("adm_b")
		e.promote("adm_b")
		c := e.register("adm_c")
		e.promote("adm_c")

		// Every administrator tries to demote or suspend another one at the same time.
		type job struct {
			actor  session
			target session
			body   map[string]any
		}
		jobs := []job{
			{a, b, map[string]any{"role": "user"}}, {b, a, map[string]any{"role": "user"}},
			{c, a, map[string]any{"status": "suspended"}}, {a, c, map[string]any{"status": "suspended"}},
			{b, c, map[string]any{"role": "moderator"}}, {c, b, map[string]any{"role": "user"}},
		}
		var wg sync.WaitGroup
		results := make([]int, len(jobs))
		for i, j := range jobs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], _ = e.call("PATCH", "/api/v1/admin/users/"+j.target.User.ID, j.actor.AccessToken, j.body)
			}()
		}
		wg.Wait()
		var n int
		if err := e.pool.QueryRow(context.Background(), `select count(*) from users where role = 'admin' and status = 'active'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n < 1 {
			t.Fatalf("round %d: no active administrator left; results %v", round, results)
		}
		for _, st := range results {
			if st != 200 && st != 409 && st != 403 && st != 401 { // 401/403: the actor was removed by an earlier request
				t.Fatalf("round %d: unexpected status in %v", round, results)
			}
		}
	}
}

func TestLastAdminTwoAdminsMutualDemotion(t *testing.T) {
	for round := 0; round < 8; round++ {
		e := newEnv(t)
		a := e.registerAdmin("adm_a")
		b := e.register("adm_b")
		e.promote("adm_b")
		var wg sync.WaitGroup
		res := make([]int, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			res[0], _ = e.call("PATCH", "/api/v1/admin/users/"+b.User.ID, a.AccessToken, map[string]any{"role": "user"})
		}()
		go func() {
			defer wg.Done()
			res[1], _ = e.call("PATCH", "/api/v1/admin/users/"+a.User.ID, b.AccessToken, map[string]any{"role": "user"})
		}()
		wg.Wait()
		var n int
		if err := e.pool.QueryRow(context.Background(), `select count(*) from users where role = 'admin'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("round %d: %d administrators left (results %v), want exactly 1", round, n, res)
		}
		ok, conflict := 0, 0
		for _, st := range res {
			switch st {
			case 200:
				ok++
			case 409, 403: // 403: the demoted caller's second request already lost its role
				conflict++
			}
		}
		if ok != 1 || conflict != 1 {
			t.Fatalf("round %d: results %v, want one success and one refusal", round, res)
		}
	}
}

func TestAdminInvites(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Registration = config.RegistrationInvite })
	admin := e.registerAdmin("root")
	type inviteResp struct {
		Invite struct {
			ID        string  `json:"id"`
			MaxUses   int     `json:"maxUses"`
			Uses      int     `json:"uses"`
			ExpiresAt *int64  `json:"expiresAt"`
			CreatedBy *string `json:"createdBy"`
			Note      *string `json:"note"`
		} `json:"invite"`
		Code string `json:"code"`
	}
	c := expect[inviteResp](e, 201, "POST", "/api/v1/admin/invites", admin.AccessToken, map[string]any{"maxUses": 2, "expiresInHours": 24, "note": " for friends "})
	if c.Code == "" || c.Invite.MaxUses != 2 || c.Invite.ExpiresAt == nil || c.Invite.CreatedBy == nil || *c.Invite.CreatedBy != "root" || *c.Invite.Note != "for friends" {
		t.Fatalf("created = %+v", c)
	}
	def := expect[inviteResp](e, 201, "POST", "/api/v1/admin/invites", admin.AccessToken, map[string]any{})
	if def.Invite.MaxUses != 1 || def.Invite.ExpiresAt != nil || def.Invite.Note != nil {
		t.Fatalf("defaults = %+v", def)
	}
	nobody := expect[inviteResp](e, 201, "POST", "/api/v1/admin/invites", admin.AccessToken, nil)
	_ = nobody

	for name, body := range map[string]map[string]any{
		"zero uses": {"maxUses": 0}, "too many uses": {"maxUses": 1001},
		"zero hours": {"expiresInHours": 0}, "too many hours": {"expiresInHours": 9000}, "long note": {"note": strings.Repeat("x", 201)},
	} {
		if st, raw := e.call("POST", "/api/v1/admin/invites", admin.AccessToken, body); st != 422 {
			t.Errorf("%s: %d %s", name, st, raw)
		}
	}

	// The list never shows codes; it includes used-up invites, newest first.
	use := func(code, user string) int {
		st, _ := e.call("POST", "/api/v1/auth/register", "", map[string]any{"username": user, "password": "correct horse", "inviteCode": code, "device": deviceInfo("p")})
		return st
	}
	if a, b, d := use(c.Code, "usr_1"), use(c.Code, "usr_2"), use(c.Code, "usr_3"); a != 201 || b != 201 || d != 403 {
		t.Fatalf("an invite works exactly maxUses times: %d %d %d", a, b, d)
	}
	_, raw := e.call("GET", "/api/v1/admin/invites", admin.AccessToken, nil)
	if strings.Contains(string(raw), c.Code) || strings.Contains(string(raw), def.Code) {
		t.Fatalf("the list must not contain plain codes: %s", raw)
	}
	var list struct {
		Invites []struct {
			ID   string `json:"id"`
			Uses int    `json:"uses"`
		} `json:"invites"`
	}
	mustJSON(t, raw, &list)
	if len(list.Invites) != 3 || list.Invites[0].ID != nobody.Invite.ID || list.Invites[2].Uses != 2 {
		t.Fatalf("invites = %+v", list.Invites)
	}

	expect[map[string]any](e, 204, "DELETE", "/api/v1/admin/invites/"+def.Invite.ID, admin.AccessToken, nil)
	if use(def.Code, "usr_4") != 403 {
		t.Fatal("a revoked invite no longer works")
	}
	e.expectErr(404, "not_found", "DELETE", "/api/v1/admin/invites/"+def.Invite.ID, admin.AccessToken, nil)
	e.expectErr(404, "not_found", "DELETE", "/api/v1/admin/invites/garbage", admin.AccessToken, nil)

	// Audit: create and delete are recorded without the codes.
	au := auditEntries(e, admin.AccessToken)
	if !hasAction(au, "invite.create") || !hasAction(au, "invite.delete") {
		t.Fatalf("audit = %+v", au)
	}
	dump, _ := json.Marshal(au)
	if strings.Contains(string(dump), c.Code) || strings.Contains(string(dump), def.Code) || strings.Contains(string(dump), "correct horse") {
		t.Fatalf("the audit log leaks a secret: %s", dump)
	}
}

func TestAdminAuditLog(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	alice := e.register("alice")
	expect[adminUser](e, 200, "PATCH", "/api/v1/admin/users/"+alice.User.ID, admin.AccessToken, map[string]any{"role": "moderator"})
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"name": "My sync"})
	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/me/delete", alice.AccessToken, map[string]any{"currentPassword": "wrong"})
	expect[map[string]any](e, 204, "POST", "/api/v1/me/delete", alice.AccessToken, map[string]any{"currentPassword": "correct horse"})

	au := auditEntries(e, admin.AccessToken)
	if len(au) < 4 || au[0].ID < au[1].ID {
		t.Fatalf("newest first expected: %+v", au)
	}
	byAction := map[string]auditEntry{}
	for _, a := range au {
		byAction[a.Action] = a
	}
	upd := byAction["user.update"]
	if upd.Actor == nil || upd.Actor.Username != "root" || upd.TargetType == nil || *upd.TargetType != "user" || *upd.TargetID != alice.User.ID || upd.CreatedAt == 0 {
		t.Fatalf("user.update = %+v", upd)
	}
	if ch := upd.Detail["changes"].(map[string]any); ch["role"] != "moderator" {
		t.Fatalf("detail = %v", upd.Detail)
	}
	if byAction["settings.update"].Detail["changes"].(map[string]any)["name"] != "My sync" {
		t.Fatalf("settings.update = %+v", byAction["settings.update"])
	}
	del := byAction["account.delete"]
	if del.Detail["username"] != "alice" || del.Actor != nil {
		t.Fatalf("account.delete = %+v", del)
	}
	if byAction["user.admin_bootstrap"].Action == "" {
		t.Fatal("the bootstrap entry is listed too")
	}
	page := expect[struct {
		Items  []auditEntry `json:"items"`
		Total  int          `json:"total"`
		Offset int          `json:"offset"`
		Limit  int          `json:"limit"`
	}](e, 200, "GET", "/api/v1/admin/audit?limit=2&offset=1", admin.AccessToken, nil)
	if len(page.Items) != 2 || page.Total != len(au) || page.Offset != 1 || page.Limit != 2 {
		t.Fatalf("paging = %+v", page)
	}
}

type settingsResp struct {
	Values struct {
		Name              string  `json:"name"`
		Description       string  `json:"description"`
		Registration      string  `json:"registration"`
		EmailVerification string  `json:"emailVerification"`
		Sharing           string  `json:"sharing"`
		TermsURL          *string `json:"termsUrl"`
		PrivacyURL        *string `json:"privacyUrl"`
		Contact           *string `json:"contact"`
	} `json:"values"`
	Defaults struct {
		Name         string `json:"name"`
		Registration string `json:"registration"`
		Sharing      string `json:"sharing"`
	} `json:"defaults"`
	Overridden     []string `json:"overridden"`
	EmailAvailable bool     `json:"emailAvailable"`
}

func TestAdminSettingsOverrideAndReset(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.InstanceName = "Env name"; c.Contact = "env@example.com" })
	admin := e.registerAdmin("root")
	get := func() settingsResp {
		return expect[settingsResp](e, 200, "GET", "/api/v1/admin/settings", admin.AccessToken, nil)
	}

	s := get()
	if s.Values.Name != "Env name" || s.Values.Registration != "open" || len(s.Overridden) != 0 || s.EmailAvailable || *s.Values.Contact != "env@example.com" {
		t.Fatalf("initial settings = %+v", s)
	}
	// Registration is open by default in the test environment: close it and see it bite at once.
	expect[map[string]any](e, 201, "POST", "/api/v1/auth/register", "", map[string]any{"username": "early", "password": "correct horse", "device": deviceInfo("p")})
	s = expect[settingsResp](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{
		"registration": "closed", "name": "  Renamed  ", "description": "hello", "termsUrl": "https://example.com/terms", "contact": ""})
	if s.Values.Registration != "closed" || s.Values.Name != "Renamed" || s.Defaults.Registration != "open" || s.Defaults.Name != "Env name" ||
		*s.Values.TermsURL != "https://example.com/terms" || s.Values.Contact != nil || strings.Join(s.Overridden, ",") != "contact,description,name,registration,termsUrl" {
		t.Fatalf("patched = %+v", s)
	}
	e.expectErr(403, "registration_closed", "POST", "/api/v1/auth/register", "", map[string]any{"username": "late", "password": "correct horse", "device": deviceInfo("p")})
	e.expectErr(403, "registration_closed", "POST", "/api/v1/web/register", "", map[string]any{"username": "late", "password": "correct horse"})
	info := expect[map[string]any](e, 200, "GET", "/.well-known/shinsou", "", nil)
	if info["name"] != "Renamed" || info["registration"] != "closed" || info["description"] != "hello" || info["termsUrl"] != "https://example.com/terms" || info["contact"] != nil {
		t.Fatalf("instance info = %v", info)
	}

	// invite policy: invite required, then null resets the key to the environment default (open)
	expect[settingsResp](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"registration": "invite"})
	e.expectErr(403, "invite_required", "POST", "/api/v1/auth/register", "", map[string]any{"username": "late", "password": "correct horse", "device": deviceInfo("p")})
	s = expect[settingsResp](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"registration": nil, "name": nil, "contact": nil})
	if s.Values.Registration != "open" || s.Values.Name != "Env name" || *s.Values.Contact != "env@example.com" || strings.Join(s.Overridden, ",") != "description,termsUrl" {
		t.Fatalf("after reset = %+v", s)
	}
	expect[map[string]any](e, 201, "POST", "/api/v1/auth/register", "", map[string]any{"username": "late", "password": "correct horse", "device": deviceInfo("p")})

	// validation
	for name, body := range map[string]map[string]any{
		"empty":            {},
		"unknown key":      {"colour": "red"},
		"blank name":       {"name": "   "},
		"long name":        {"name": strings.Repeat("n", 81)},
		"long description": {"description": strings.Repeat("d", 501)},
		"bad registration": {"registration": "maybe"},
		"bad policy":       {"emailVerification": "sometimes"},
		"relative url":     {"termsUrl": "/terms"},
		"ftp url":          {"privacyUrl": "ftp://example.com/p"},
		"long url":         {"termsUrl": "https://example.com/" + strings.Repeat("a", 2048)},
		"long contact":     {"contact": strings.Repeat("c", 201)},
		"wrong type":       {"name": 5},
	} {
		if st, raw := e.call("PATCH", "/api/v1/admin/settings", admin.AccessToken, body); st != 422 {
			t.Errorf("%s: status %d %s", name, st, raw)
		}
	}
	// An empty URL clears a link set by the environment as well.
	s = expect[settingsResp](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"termsUrl": ""})
	if s.Values.TermsURL != nil {
		t.Fatalf("termsUrl = %v", s.Values.TermsURL)
	}
	// Requiring verification needs SMTP.
	e.expectErr(409, "email_unavailable", "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"emailVerification": "required"})
	s = expect[settingsResp](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"emailVerification": "optional"})
	if s.Values.EmailVerification != "optional" {
		t.Fatalf("emailVerification = %v", s.Values.EmailVerification)
	}
}

func TestAdminSettingsSurviveRestart(t *testing.T) {
	d := testutil.NewDB(t)
	cfg := config.Defaults()
	cfg.DatabaseURL, cfg.Registration, cfg.PublicURL = d.URL, config.RegistrationOpen, "https://sync.test"
	cfg.Argon2MemoryKiB, cfg.Argon2Time, cfg.AuthRatePerMinute = 8, 1, 0
	start := func() *env {
		srv, err := httpapi.New(context.Background(), cfg, d.Pool, slogTo(new(syncBuf)), "test")
		if err != nil {
			t.Fatal(err)
		}
		return &env{t: t, srv: httptestServer(t, srv), pool: d.Pool, cfg: cfg}
	}
	first := start()
	admin := first.registerAdmin("root")
	expect[map[string]any](first, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"name": "Persistent", "registration": "closed"})
	second := start()
	info := expect[map[string]any](second, 200, "GET", "/.well-known/shinsou", "", nil)
	if info["name"] != "Persistent" || info["registration"] != "closed" || info["setupPending"] != false {
		t.Fatalf("after restart: %v", info)
	}
}

func TestInstanceInfoReportsSetupPending(t *testing.T) {
	e := newEnv(t)
	info := expect[map[string]any](e, 200, "GET", "/.well-known/shinsou", "", nil)
	if info["setupPending"] != true || info["features"].(map[string]any)["email"] != false {
		t.Fatalf("fresh instance: %v", info)
	}
	e.registerAdmin("root")
	info = expect[map[string]any](e, 200, "GET", "/.well-known/shinsou", "", nil)
	if info["setupPending"] != false {
		t.Fatalf("after setup: %v", info)
	}
}
