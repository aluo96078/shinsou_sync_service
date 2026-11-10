package httpapi_test

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

type userSummary struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Following  bool   `json:"following"`
	FollowsYou bool   `json:"followsYou"`
	Blocked    bool   `json:"blocked"`
	CanShare   bool   `json:"canShare"`
	Since      *int64 `json:"since"`
}

type socialUserPage struct {
	Users  []userSummary `json:"users"`
	Total  int           `json:"total"`
	Offset int           `json:"offset"`
	Limit  int           `json:"limit"`
}

type shareParty struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type shareView struct {
	ID          string       `json:"id"`
	Sender      shareParty   `json:"sender"`
	Recipient   shareParty   `json:"recipient"`
	Work        syncsvc.Work `json:"work"`
	Message     string       `json:"message"`
	Status      string       `json:"status"`
	CreatedAt   int64        `json:"createdAt"`
	RespondedAt *int64       `json:"respondedAt"`
	InLibrary   bool         `json:"inLibrary"`
}

type sharePage struct {
	Shares  []shareView `json:"shares"`
	Total   int         `json:"total"`
	Offset  int         `json:"offset"`
	Limit   int         `json:"limit"`
	Pending int         `json:"pending"`
}

func (e *env) search(token, q string) []userSummary {
	e.t.Helper()
	return expect[struct {
		Users []userSummary `json:"users"`
	}](e, 200, "GET", "/api/v1/users?q="+url.QueryEscape(q), token, nil).Users
}

func names(us []userSummary) string {
	out := []string{}
	for _, u := range us {
		out = append(out, u.Username)
	}
	return strings.Join(out, ",")
}

// addToLibrary puts a work into the account's library the way a device does.
func (e *env) addToLibrary(s session, remoteID string) syncsvc.Work {
	e.t.Helper()
	w := mkWork(remoteID)
	push(e, s.AccessToken, 0, []syncsvc.Work{w}, recRaw("library", w.Hash, map[string]string{"addedAt": "1"}, clock(nowMs(), 0, s.Device.SyncDeviceID)))
	return w
}

func (e *env) share(from session, to session, w syncsvc.Work, message string) shareView {
	e.t.Helper()
	return expect[shareView](e, 201, "POST", "/api/v1/shares", from.AccessToken,
		map[string]any{"recipientId": to.User.ID, "work": w.Hash, "message": message})
}

func (e *env) do(status int, method, path, token string) {
	e.t.Helper()
	if st, raw := e.call(method, path, token, nil); st != status {
		e.t.Fatalf("%s %s = %d %s, want %d", method, path, st, raw, status)
	}
}

func TestUserSearch(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	alice := e.register("alice")
	e.register("alan")
	al := e.register("albert")
	bob := e.register("bob")
	e.register("a_b")

	if got := names(e.search(alice.AccessToken, "AL")); got != "alan,albert" {
		t.Fatalf("search AL = %s", got)
	}
	if got := names(e.search(alice.AccessToken, " b ")); got != "bob" {
		t.Fatalf("search b = %s", got)
	}
	// LIKE wildcards are literal.
	if got := names(e.search(alice.AccessToken, "a_")); got != "a_b" {
		t.Fatalf("search a_ = %s", got)
	}
	if got := names(e.search(alice.AccessToken, "%")); got != "" {
		t.Fatalf("search %% = %s", got)
	}
	if us := expect[struct {
		Users []userSummary `json:"users"`
	}](e, 200, "GET", "/api/v1/users?q=a&limit=2", bob.AccessToken, nil).Users; len(us) != 2 {
		t.Fatalf("limit ignored: %+v", us)
	}
	for _, q := range []string{"", "?q=", "?q=%20", "?q=" + strings.Repeat("a", 33), "?q=a&limit=0", "?q=a&limit=51"} {
		e.expectErr(400, "bad_request", "GET", "/api/v1/users"+q, alice.AccessToken, nil)
	}
	e.expectErr(401, "unauthorized", "GET", "/api/v1/users?q=a", "", nil)

	// A suspended account and one that blocks the caller are not listed.
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/users/"+al.User.ID, admin.AccessToken, map[string]any{"status": "suspended"})
	e.do(204, "PUT", "/api/v1/me/blocks/"+alice.User.ID, bob.AccessToken)
	if got := names(e.search(alice.AccessToken, "al")); got != "alan" {
		t.Fatalf("search al after suspension = %s", got)
	}
	if got := names(e.search(alice.AccessToken, "bo")); got != "" {
		t.Fatalf("blocker listed: %s", got)
	}
	// The blocker still finds the blocked account, marked as such.
	us := e.search(bob.AccessToken, "alice")
	if len(us) != 1 || !us[0].Blocked || us[0].CanShare {
		t.Fatalf("blocked account as seen by blocker = %+v", us)
	}
}

func TestSocialRateLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SocialRatePerMinute, c.SocialRateBurst = 1, 2 })
	alice, bob := e.register("alice"), e.register("bob")
	e.search(alice.AccessToken, "b")
	e.search(alice.AccessToken, "b")
	if got := e.expectErr(429, "rate_limited", "GET", "/api/v1/users?q=b", alice.AccessToken, nil); got.Error.RetryAfterSeconds < 1 {
		t.Fatalf("no retry hint: %+v", got)
	}
	// The budget is per account, and share creation draws from the same one.
	e.search(bob.AccessToken, "a")
	e.expectErr(429, "rate_limited", "POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": bob.User.ID, "work": mkWork("1").Hash})
}

func TestFollow(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")

	e.do(204, "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	e.do(204, "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	e.do(204, "PUT", "/api/v1/me/following/"+carol.User.ID, alice.AccessToken)
	e.do(204, "PUT", "/api/v1/me/following/"+alice.User.ID, bob.AccessToken)

	following := expect[socialUserPage](e, 200, "GET", "/api/v1/me/following", alice.AccessToken, nil)
	if following.Total != 2 || names(following.Users) != "carol,bob" || following.Users[0].Since == nil {
		t.Fatalf("following = %+v", following)
	}
	if b := following.Users[1]; !b.Following || !b.FollowsYou || b.Blocked || !b.CanShare {
		t.Fatalf("bob as seen by alice = %+v", b)
	}
	if c := following.Users[0]; !c.Following || c.FollowsYou {
		t.Fatalf("carol as seen by alice = %+v", c)
	}
	followers := expect[socialUserPage](e, 200, "GET", "/api/v1/me/followers", bob.AccessToken, nil)
	if followers.Total != 1 || names(followers.Users) != "alice" || !followers.Users[0].Following {
		t.Fatalf("followers = %+v", followers)
	}
	page := expect[socialUserPage](e, 200, "GET", "/api/v1/me/following?offset=1&limit=1", alice.AccessToken, nil)
	if page.Total != 2 || names(page.Users) != "bob" || page.Offset != 1 || page.Limit != 1 {
		t.Fatalf("page = %+v", page)
	}

	e.expectErr(422, "validation_failed", "PUT", "/api/v1/me/following/"+alice.User.ID, alice.AccessToken, nil)
	e.expectErr(404, "not_found", "PUT", "/api/v1/me/following/00000000-0000-4000-8000-000000000000", alice.AccessToken, nil)
	e.expectErr(404, "not_found", "PUT", "/api/v1/me/following/nope", alice.AccessToken, nil)

	e.do(204, "DELETE", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	e.do(204, "DELETE", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	if p := expect[socialUserPage](e, 200, "GET", "/api/v1/me/following", alice.AccessToken, nil); names(p.Users) != "carol" {
		t.Fatalf("after unfollow = %+v", p)
	}
}

func TestBlock(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(bob, "1")
	w2 := e.addToLibrary(alice, "2")

	e.do(204, "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	e.do(204, "PUT", "/api/v1/me/following/"+alice.User.ID, bob.AccessToken)
	open := e.share(bob, alice, w, "read this")

	e.do(204, "PUT", "/api/v1/me/blocks/"+bob.User.ID, alice.AccessToken)
	e.do(204, "PUT", "/api/v1/me/blocks/"+bob.User.ID, alice.AccessToken)

	// Both follows are gone, the open share is dismissed.
	for _, s := range []session{alice, bob} {
		if p := expect[socialUserPage](e, 200, "GET", "/api/v1/me/following", s.AccessToken, nil); p.Total != 0 {
			t.Fatalf("follow survived the block: %+v", p)
		}
	}
	inbox := expect[sharePage](e, 200, "GET", "/api/v1/shares", alice.AccessToken, nil)
	if inbox.Pending != 0 || len(inbox.Shares) != 1 || inbox.Shares[0].ID != open.ID || inbox.Shares[0].Status != "dismissed" {
		t.Fatalf("inbox after block = %+v", inbox)
	}
	blocks := expect[socialUserPage](e, 200, "GET", "/api/v1/me/blocks", alice.AccessToken, nil)
	if blocks.Total != 1 || names(blocks.Users) != "bob" || !blocks.Users[0].Blocked || blocks.Users[0].CanShare {
		t.Fatalf("blocks = %+v", blocks)
	}

	// The blocked account cannot follow or share; it learns no more than "refused" / "no such user".
	e.expectErr(404, "not_found", "PUT", "/api/v1/me/following/"+alice.User.ID, bob.AccessToken, nil)
	e.expectErr(403, "share_refused", "POST", "/api/v1/shares", bob.AccessToken, map[string]any{"recipientId": alice.User.ID, "work": w.Hash})
	// The blocker has to unblock first.
	e.expectErr(409, "user_blocked", "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken, nil)
	e.expectErr(409, "user_blocked", "POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": bob.User.ID, "work": w2.Hash})

	e.expectErr(422, "validation_failed", "PUT", "/api/v1/me/blocks/"+alice.User.ID, alice.AccessToken, nil)
	e.expectErr(404, "not_found", "PUT", "/api/v1/me/blocks/00000000-0000-4000-8000-000000000000", alice.AccessToken, nil)

	e.do(204, "DELETE", "/api/v1/me/blocks/"+bob.User.ID, alice.AccessToken)
	e.do(204, "DELETE", "/api/v1/me/blocks/"+bob.User.ID, alice.AccessToken)
	e.share(bob, alice, w, "")
	e.do(204, "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
}

func TestSharingPreference(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(bob, "1")
	body := map[string]any{"recipientId": alice.User.ID, "work": w.Hash}
	type prefs struct {
		ReceiveFrom string `json:"receiveFrom"`
	}

	if p := expect[prefs](e, 200, "GET", "/api/v1/me/sharing", alice.AccessToken, nil); p.ReceiveFrom != "everyone" {
		t.Fatalf("default = %+v", p)
	}
	if p := expect[prefs](e, 200, "PUT", "/api/v1/me/sharing", alice.AccessToken, prefs{"following"}); p.ReceiveFrom != "following" {
		t.Fatalf("put = %+v", p)
	}
	if us := e.search(bob.AccessToken, "alice"); len(us) != 1 || us[0].CanShare {
		t.Fatalf("canShare before follow = %+v", us)
	}
	e.expectErr(403, "share_refused", "POST", "/api/v1/shares", bob.AccessToken, body)
	// It is the recipient following the sender that counts.
	e.do(204, "PUT", "/api/v1/me/following/"+alice.User.ID, bob.AccessToken)
	e.expectErr(403, "share_refused", "POST", "/api/v1/shares", bob.AccessToken, body)
	e.do(204, "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	if us := e.search(bob.AccessToken, "alice"); !us[0].CanShare {
		t.Fatalf("canShare after follow = %+v", us)
	}
	first := expect[shareView](e, 201, "POST", "/api/v1/shares", bob.AccessToken, body)

	expect[prefs](e, 200, "PUT", "/api/v1/me/sharing", alice.AccessToken, prefs{"nobody"})
	// What was received already stays; nothing new comes in.
	e.do(204, "DELETE", "/api/v1/shares/"+first.ID, alice.AccessToken)
	e.expectErr(403, "share_refused", "POST", "/api/v1/shares", bob.AccessToken, body)
	if p := expect[prefs](e, 200, "GET", "/api/v1/me/sharing", alice.AccessToken, nil); p.ReceiveFrom != "nobody" {
		t.Fatalf("get = %+v", p)
	}
	e.expectErr(422, "validation_failed", "PUT", "/api/v1/me/sharing", alice.AccessToken, prefs{"friends"})
	e.expectErr(422, "validation_failed", "PUT", "/api/v1/me/sharing", alice.AccessToken, map[string]any{})
}

func TestShareLifecycle(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")
	w := mkWork("1")
	w.SourceName, w.RepositoryURL = ptr("Wenku8"), ptr("https://example.org/repo.json")
	push(e, alice.AccessToken, 0, []syncsvc.Work{w}, recRaw("library", w.Hash, map[string]string{"addedAt": "1"}, clock(nowMs(), 0, alice.Device.SyncDeviceID)))
	req := func(to session, hash, msg string) map[string]any {
		return map[string]any{"recipientId": to.User.ID, "work": hash, "message": msg}
	}

	// Input and preconditions.
	e.expectErr(422, "validation_failed", "POST", "/api/v1/shares", alice.AccessToken, req(bob, "abc", ""))
	e.expectErr(422, "validation_failed", "POST", "/api/v1/shares", alice.AccessToken, req(bob, w.Hash, strings.Repeat("字", 501)))
	e.expectErr(422, "validation_failed", "POST", "/api/v1/shares", alice.AccessToken, req(alice, w.Hash, ""))
	e.expectErr(422, "validation_failed", "POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": "x", "work": w.Hash})
	e.expectErr(404, "not_found", "POST", "/api/v1/shares", alice.AccessToken,
		map[string]any{"recipientId": "00000000-0000-4000-8000-000000000000", "work": w.Hash})
	e.expectErr(422, "unknown_work", "POST", "/api/v1/shares", alice.AccessToken, req(bob, mkWork("404").Hash, ""))
	// Bob knows the hash but the work is not in his library.
	e.expectErr(422, "unknown_work", "POST", "/api/v1/shares", bob.AccessToken, req(alice, w.Hash, ""))

	sh := expect[shareView](e, 201, "POST", "/api/v1/shares", alice.AccessToken, req(bob, w.Hash, "  "+strings.Repeat("字", 500)+" "))
	if sh.Sender.Username != "alice" || sh.Recipient.Username != "bob" || sh.Status != "pending" || sh.RespondedAt != nil ||
		sh.Message != strings.Repeat("字", 500) || !sh.InLibrary || sh.Work.Hash != w.Hash || sh.Work.Title != w.Title ||
		sh.Work.SourceName == nil || *sh.Work.SourceName != "Wenku8" || sh.Work.RepositoryURL == nil || sh.CreatedAt == 0 {
		t.Fatalf("created share = %+v", sh)
	}
	// The same open share is returned, not duplicated.
	again := expect[shareView](e, 200, "POST", "/api/v1/shares", alice.AccessToken, req(bob, w.Hash, "other words"))
	if again.ID != sh.ID || again.Message != sh.Message {
		t.Fatalf("duplicate = %+v", again)
	}

	inbox := expect[sharePage](e, 200, "GET", "/api/v1/shares", bob.AccessToken, nil)
	if inbox.Total != 1 || inbox.Pending != 1 || len(inbox.Shares) != 1 || inbox.Shares[0].ID != sh.ID || inbox.Shares[0].InLibrary {
		t.Fatalf("inbox = %+v", inbox)
	}
	if n := expect[map[string]int](e, 200, "GET", "/api/v1/shares/summary", bob.AccessToken, nil)["pending"]; n != 1 {
		t.Fatalf("summary = %d", n)
	}
	sent := expect[sharePage](e, 200, "GET", "/api/v1/shares?box=sent", alice.AccessToken, nil)
	if sent.Total != 1 || sent.Pending != 0 || sent.Shares[0].ID != sh.ID {
		t.Fatalf("sent = %+v", sent)
	}
	if p := expect[sharePage](e, 200, "GET", "/api/v1/shares?box=sent", bob.AccessToken, nil); p.Total != 0 || len(p.Shares) != 0 {
		t.Fatalf("bob sent = %+v", p)
	}
	e.expectErr(400, "bad_request", "GET", "/api/v1/shares?box=all", bob.AccessToken, nil)
	e.expectErr(400, "bad_request", "GET", "/api/v1/shares?status=open", bob.AccessToken, nil)

	// Only the two sides see it; only the recipient answers.
	for _, path := range []string{"/accept", "/dismiss"} {
		e.expectErr(404, "not_found", "POST", "/api/v1/shares/"+sh.ID+path, carol.AccessToken, nil)
		e.expectErr(404, "not_found", "POST", "/api/v1/shares/"+sh.ID+path, alice.AccessToken, nil)
	}
	e.expectErr(404, "not_found", "DELETE", "/api/v1/shares/"+sh.ID, carol.AccessToken, nil)
	e.expectErr(404, "not_found", "POST", "/api/v1/shares/nope/accept", bob.AccessToken, nil)

	// Dismissing is invisible to the sender.
	d := expect[shareView](e, 200, "POST", "/api/v1/shares/"+sh.ID+"/dismiss", bob.AccessToken, nil)
	if d.Status != "dismissed" || d.RespondedAt == nil {
		t.Fatalf("dismissed = %+v", d)
	}
	expect[shareView](e, 200, "POST", "/api/v1/shares/"+sh.ID+"/dismiss", bob.AccessToken, nil)
	sent = expect[sharePage](e, 200, "GET", "/api/v1/shares?box=sent&status=pending", alice.AccessToken, nil)
	if sent.Total != 1 || sent.Shares[0].Status != "pending" || sent.Shares[0].RespondedAt != nil {
		t.Fatalf("sender sees the dismissal: %+v", sent)
	}
	if p := expect[sharePage](e, 200, "GET", "/api/v1/shares?box=sent&status=dismissed", alice.AccessToken, nil); p.Total != 0 || len(p.Shares) != 0 {
		t.Fatalf("sender can filter by dismissed: %+v", p)
	}
	if p := expect[sharePage](e, 200, "GET", "/api/v1/shares?status=dismissed", bob.AccessToken, nil); p.Total != 1 || p.Pending != 0 {
		t.Fatalf("recipient filter = %+v", p)
	}
	// A dismissed share no longer stands in the way of sending it again.
	second := e.share(alice, bob, w, "again")
	if second.ID == sh.ID {
		t.Fatal("a dismissed share was reused")
	}

	// Accepting (also after dismissing) puts the work into the recipient's library.
	before := pull(e, bob.AccessToken, 0)
	a := expect[shareView](e, 200, "POST", "/api/v1/shares/"+sh.ID+"/accept", bob.AccessToken, nil)
	if a.Status != "accepted" || a.RespondedAt == nil || !a.InLibrary {
		t.Fatalf("accepted = %+v", a)
	}
	lib := e.library(bob.AccessToken, url.Values{})
	if lib.Total != 1 || lib.Items[0].Key != w.Hash || lib.Items[0].Title != w.Title || lib.Items[0].AddedAt == nil || lib.Items[0].Pinned {
		t.Fatalf("library after accept = %+v", lib)
	}
	after := pull(e, bob.AccessToken, before.NextSince)
	if len(after.Records) != 1 || after.Records[0].Kind != "library" || after.Records[0].Key != w.Hash || after.Records[0].Work == nil || after.Records[0].Work.Title != w.Title ||
		string(after.Records[0].Fields["updatePolicy"]) != `"auto"` || !strings.HasSuffix(after.Records[0].Clocks["addedAt"], ":0:server") {
		t.Fatalf("changes after accept = %+v", after)
	}
	// Repeating is harmless and writes nothing new.
	expect[shareView](e, 200, "POST", "/api/v1/shares/"+sh.ID+"/accept", bob.AccessToken, nil)
	expect[shareView](e, 200, "POST", "/api/v1/shares/"+second.ID+"/accept", bob.AccessToken, nil)
	if again := pull(e, bob.AccessToken, after.NextSince); len(again.Records) != 0 {
		t.Fatalf("repeated accept wrote records: %+v", again.Records)
	}
	e.expectErr(409, "conflict", "POST", "/api/v1/shares/"+sh.ID+"/dismiss", bob.AccessToken, nil)
	sent = expect[sharePage](e, 200, "GET", "/api/v1/shares?box=sent&status=accepted", alice.AccessToken, nil)
	if sent.Total != 2 || sent.Shares[0].Status != "accepted" || sent.Shares[0].RespondedAt == nil {
		t.Fatalf("sender view after accept = %+v", sent)
	}

	// Either side removes it for both.
	e.do(204, "DELETE", "/api/v1/shares/"+sh.ID, alice.AccessToken)
	e.do(204, "DELETE", "/api/v1/shares/"+second.ID, bob.AccessToken)
	e.expectErr(404, "not_found", "DELETE", "/api/v1/shares/"+sh.ID, bob.AccessToken, nil)
	if p := expect[sharePage](e, 200, "GET", "/api/v1/shares", bob.AccessToken, nil); p.Total != 0 {
		t.Fatalf("inbox after delete = %+v", p)
	}
	// The library entry stays.
	if lib := e.library(bob.AccessToken, url.Values{}); lib.Total != 1 {
		t.Fatalf("library after delete = %+v", lib)
	}
}

func TestShareToSuspendedAccount(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(alice, "1")
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/users/"+bob.User.ID, admin.AccessToken, map[string]any{"status": "suspended"})
	e.expectErr(404, "not_found", "POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": bob.User.ID, "work": w.Hash})
	e.expectErr(404, "not_found", "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken, nil)
}

func TestShareDailyCap(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SharesPerDay = 2 })
	alice, bob := e.register("alice"), e.register("bob")
	w1, w2, w3 := e.addToLibrary(alice, "1"), e.addToLibrary(alice, "2"), e.addToLibrary(alice, "3")
	e.share(alice, bob, w1, "")
	e.share(alice, bob, w2, "")
	got := e.expectErr(429, "rate_limited", "POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": bob.User.ID, "work": w3.Hash})
	if got.Error.RetryAfterSeconds < 23*3600 || got.Error.RetryAfterSeconds > 24*3600+1 {
		t.Fatalf("retry hint = %+v", got)
	}
	// Asking again for an open share is not a new share.
	expect[shareView](e, 200, "POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": bob.User.ID, "work": w1.Hash})
}

func TestAcceptRespectsQuota(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RecordQuota = 1 })
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(alice, "1")
	e.addToLibrary(bob, "2")
	sh := e.share(alice, bob, w, "")
	e.expectErr(409, "quota_exceeded", "POST", "/api/v1/shares/"+sh.ID+"/accept", bob.AccessToken, nil)
	if p := expect[sharePage](e, 200, "GET", "/api/v1/shares", bob.AccessToken, nil); p.Pending != 1 || p.Shares[0].Status != "pending" || p.Shares[0].InLibrary {
		t.Fatalf("share after refused accept = %+v", p)
	}
}

func TestSharingSwitch(t *testing.T) {
	e := newEnv(t)
	admin := e.registerAdmin("root")
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(alice, "1")
	sh := e.share(alice, bob, w, "")

	type inst struct {
		Features struct {
			Sharing bool `json:"sharing"`
		} `json:"features"`
	}
	if !expect[inst](e, 200, "GET", "/.well-known/shinsou", "", nil).Features.Sharing {
		t.Fatal("sharing should be on by default")
	}
	e.expectErr(422, "validation_failed", "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"sharing": "maybe"})
	got := expect[settingsResp](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"sharing": "disabled"})
	if got.Values.Sharing != "disabled" || got.Defaults.Sharing != "enabled" {
		t.Fatalf("settings = %+v", got)
	}
	if expect[inst](e, 200, "GET", "/.well-known/shinsou", "", nil).Features.Sharing {
		t.Fatal("instance still advertises sharing")
	}

	for _, c := range [][2]string{
		{"GET", "/api/v1/shares"}, {"POST", "/api/v1/shares"}, {"GET", "/api/v1/shares/summary"},
		{"DELETE", "/api/v1/shares/" + sh.ID}, {"POST", "/api/v1/shares/" + sh.ID + "/accept"}, {"POST", "/api/v1/shares/" + sh.ID + "/dismiss"},
	} {
		e.expectErr(403, "feature_disabled", c[0], c[1], bob.AccessToken, nil)
	}
	// The rest keeps working; nobody can be shared with.
	if us := e.search(alice.AccessToken, "bob"); len(us) != 1 || us[0].CanShare {
		t.Fatalf("search while off = %+v", us)
	}
	e.do(204, "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	e.do(204, "PUT", "/api/v1/me/blocks/"+alice.User.ID, bob.AccessToken)
	e.do(204, "DELETE", "/api/v1/me/blocks/"+alice.User.ID, bob.AccessToken)
	e.do(200, "GET", "/api/v1/me/sharing", bob.AccessToken)

	// Turning it back on brings back what was there. The block dismissed the open share.
	expect[settingsResp](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"sharing": nil})
	if p := expect[sharePage](e, 200, "GET", "/api/v1/shares", bob.AccessToken, nil); p.Total != 1 || p.Shares[0].Status != "dismissed" {
		t.Fatalf("inbox after switching back on = %+v", p)
	}
}

func TestSharingStartsDisabledFromConfig(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Sharing = config.SharingDisabled })
	alice := e.register("alice")
	e.expectErr(403, "feature_disabled", "GET", "/api/v1/shares", alice.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/shares", "", nil)
}

func TestSocialNeedsVerifiedEmail(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	alice := e.registerWith("alice", "alice@example.org")
	for _, c := range [][2]string{
		{"GET", "/api/v1/users?q=a"}, {"GET", "/api/v1/me/following"}, {"GET", "/api/v1/me/sharing"},
		{"GET", "/api/v1/shares"}, {"GET", "/api/v1/shares/summary"},
	} {
		e.expectErr(403, "email_unverified", c[0], c[1], alice.AccessToken, nil)
	}
}

func TestSocialExportAndAccountDeletion(t *testing.T) {
	e := newEnv(t)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")
	w := e.addToLibrary(alice, "1")
	wb := e.addToLibrary(bob, "2")
	e.do(204, "PUT", "/api/v1/me/following/"+bob.User.ID, alice.AccessToken)
	e.do(204, "PUT", "/api/v1/me/following/"+alice.User.ID, bob.AccessToken)
	e.do(204, "PUT", "/api/v1/me/blocks/"+carol.User.ID, alice.AccessToken)
	e.share(alice, bob, w, "out")
	in := e.share(bob, alice, wb, "in")
	expect[shareView](e, 200, "POST", "/api/v1/shares/"+in.ID+"/dismiss", alice.AccessToken, nil)
	expect[map[string]string](e, 200, "PUT", "/api/v1/me/sharing", alice.AccessToken, map[string]string{"receiveFrom": "following"})

	doc := expect[struct {
		Social struct {
			ReceiveFrom string       `json:"receiveFrom"`
			Following   []shareParty `json:"following"`
			Followers   []shareParty `json:"followers"`
			Blocks      []shareParty `json:"blocks"`
			Shares      []shareView  `json:"shares"`
		} `json:"social"`
	}](e, 200, "GET", "/api/v1/me/export", alice.AccessToken, nil)
	s := doc.Social
	if s.ReceiveFrom != "following" || len(s.Following) != 1 || s.Following[0].Username != "bob" || len(s.Followers) != 1 ||
		len(s.Blocks) != 1 || s.Blocks[0].Username != "carol" || len(s.Shares) != 2 ||
		s.Shares[0].Message != "out" || s.Shares[1].Status != "dismissed" {
		t.Fatalf("export social = %+v", s)
	}
	// Bob's export does not give away that Alice dismissed his share.
	bobDoc := expect[struct {
		Social struct {
			Shares []shareView `json:"shares"`
		} `json:"social"`
	}](e, 200, "GET", "/api/v1/me/export", bob.AccessToken, nil)
	if len(bobDoc.Social.Shares) != 2 || bobDoc.Social.Shares[1].Status != "pending" || bobDoc.Social.Shares[1].RespondedAt != nil {
		t.Fatalf("bob's export = %+v", bobDoc.Social.Shares)
	}

	// Deleting the account takes its follows, blocks and shares along.
	if st, raw := e.call("POST", "/api/v1/me/delete", alice.AccessToken, map[string]any{"currentPassword": "correct horse"}); st != 204 {
		t.Fatalf("delete account = %d %s", st, raw)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`select (select count(*) from follows) + (select count(*) from blocks) + (select count(*) from shares)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left after deletion: %d (%v)", n, err)
	}
	if p := expect[sharePage](e, 200, "GET", "/api/v1/shares", bob.AccessToken, nil); p.Total != 0 {
		t.Fatalf("bob's inbox = %+v", p)
	}
}

// Opposite-direction social changes between the same two accounts must neither deadlock nor
// leave a duplicate open share.
func TestConcurrentSocialChanges(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.register("alice"), e.register("bob")
	wa, wb := e.addToLibrary(alice, "1"), e.addToLibrary(bob, "2")
	type step struct {
		method, path, token string
		body                any
	}
	steps := []step{
		{"PUT", "/api/v1/me/following/" + bob.User.ID, alice.AccessToken, nil},
		{"PUT", "/api/v1/me/following/" + alice.User.ID, bob.AccessToken, nil},
		{"PUT", "/api/v1/me/blocks/" + bob.User.ID, alice.AccessToken, nil},
		{"DELETE", "/api/v1/me/blocks/" + bob.User.ID, alice.AccessToken, nil},
		{"PUT", "/api/v1/me/blocks/" + alice.User.ID, bob.AccessToken, nil},
		{"DELETE", "/api/v1/me/blocks/" + alice.User.ID, bob.AccessToken, nil},
		{"POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": bob.User.ID, "work": wa.Hash}},
		{"POST", "/api/v1/shares", alice.AccessToken, map[string]any{"recipientId": bob.User.ID, "work": wa.Hash}},
		{"POST", "/api/v1/shares", bob.AccessToken, map[string]any{"recipientId": alice.User.ID, "work": wb.Hash}},
		{"POST", "/api/v1/shares", bob.AccessToken, map[string]any{"recipientId": alice.User.ID, "work": wb.Hash}},
	}
	for round := 0; round < 5; round++ {
		var wg sync.WaitGroup
		statuses := make([]int, len(steps))
		for i, s := range steps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				statuses[i], _ = e.call(s.method, s.path, s.token, s.body)
			}()
		}
		wg.Wait()
		for i, st := range statuses {
			if st >= 500 {
				t.Fatalf("round %d: %s %s = %d", round, steps[i].method, steps[i].path, st)
			}
		}
	}
	var dup int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from (select 1 from shares where status = 'pending'
		group by sender_id, recipient_id, work_id having count(*) > 1) d`).Scan(&dup); err != nil || dup != 0 {
		t.Fatalf("duplicate open shares: %d (%v)", dup, err)
	}
}

// Two devices accepting the same share at once add the work once.
func TestConcurrentAccept(t *testing.T) {
	e := newEnv(t)
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(alice, "1")
	sh := e.share(alice, bob, w, "")
	var wg sync.WaitGroup
	statuses := make([]int, 4)
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i], _ = e.call("POST", "/api/v1/shares/"+sh.ID+"/accept", bob.AccessToken, nil)
		}()
	}
	wg.Wait()
	for _, st := range statuses {
		if st != 200 {
			t.Fatalf("accept statuses = %v", statuses)
		}
	}
	if lib := e.library(bob.AccessToken, url.Values{}); lib.Total != 1 {
		t.Fatalf("library = %+v", lib)
	}
}
