package httpapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

type reviewView struct {
	ID           string        `json:"id"`
	Author       shareParty    `json:"author"`
	Rating       *int          `json:"rating"`
	Body         string        `json:"body"`
	BodyHTML     string        `json:"bodyHtml"`
	Spoiler      bool          `json:"spoiler"`
	Status       string        `json:"status"`
	HiddenReason *string       `json:"hiddenReason"`
	Helpful      int           `json:"helpful"`
	VotedHelpful bool          `json:"votedHelpful"`
	ReplyCount   int           `json:"replyCount"`
	CreatedAt    int64         `json:"createdAt"`
	UpdatedAt    int64         `json:"updatedAt"`
	Edited       bool          `json:"edited"`
	Mine         bool          `json:"mine"`
	Work         *syncsvc.Work `json:"work"`
}

type reviewPage struct {
	Reviews []reviewView `json:"reviews"`
	Total   int          `json:"total"`
}

type workPage struct {
	Work   syncsvc.Work `json:"work"`
	Rating struct {
		Average *float64 `json:"average"`
		Count   int      `json:"count"`
	} `json:"rating"`
	ReviewCount int         `json:"reviewCount"`
	MyReview    *reviewView `json:"myReview"`
	InLibrary   bool        `json:"inLibrary"`
}

type replyView struct {
	ID           string     `json:"id"`
	ReviewID     string     `json:"reviewId"`
	Author       shareParty `json:"author"`
	Body         string     `json:"body"`
	BodyHTML     string     `json:"bodyHtml"`
	Status       string     `json:"status"`
	HiddenReason *string    `json:"hiddenReason"`
	Mine         bool       `json:"mine"`
}

type replyPage struct {
	Replies []replyView `json:"replies"`
	Total   int         `json:"total"`
}

type reportView struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Status string `json:"status"`
}

type modReport struct {
	ID         string      `json:"id"`
	Reporter   shareParty  `json:"reporter"`
	TargetType string      `json:"targetType"`
	Reason     string      `json:"reason"`
	Status     string      `json:"status"`
	Resolution *string     `json:"resolution"`
	ResolvedBy *shareParty `json:"resolvedBy"`
	Review     *reviewView `json:"review"`
	Reply      *replyView  `json:"reply"`
}

type modReportPage struct {
	Reports []modReport `json:"reports"`
	Total   int         `json:"total"`
	Open    int         `json:"open"`
}

// noNewAccountCap lifts the cap on accounts that were just registered, which every test account is.
func noNewAccountCap(c *config.Config) { c.NewAccountPostsPerDay = 0 }

func (e *env) review(s session, w syncsvc.Work, rating int, body string) reviewView {
	e.t.Helper()
	return expect[reviewView](e, 201, "PUT", "/api/v1/works/"+w.Hash+"/review", s.AccessToken, map[string]any{"rating": rating, "body": body})
}

func (e *env) reviewsOf(token string, w syncsvc.Work, query string) reviewPage {
	e.t.Helper()
	return expect[reviewPage](e, 200, "GET", "/api/v1/works/"+w.Hash+"/reviews"+query, token, nil)
}

func (e *env) workPage(token string, w syncsvc.Work) workPage {
	e.t.Helper()
	return expect[workPage](e, 200, "GET", "/api/v1/works/"+w.Hash, token, nil)
}

func authors(p reviewPage) string {
	out := []string{}
	for _, r := range p.Reviews {
		out = append(out, r.Author.Username)
	}
	return strings.Join(out, ",")
}

func TestReviewWriteEditDelete(t *testing.T) {
	e := newEnv(t, noNewAccountCap)
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(alice, "w1")
	path := "/api/v1/works/" + w.Hash + "/review"

	// Validation.
	e.expectErr(422, "validation_failed", "PUT", path, alice.AccessToken, map[string]any{"body": "  "})
	e.expectErr(422, "validation_failed", "PUT", path, alice.AccessToken, map[string]any{"rating": 6})
	e.expectErr(422, "validation_failed", "PUT", path, alice.AccessToken, map[string]any{"rating": 0, "body": "x"})
	e.expectErr(422, "validation_failed", "PUT", path, alice.AccessToken, map[string]any{"body": strings.Repeat("字", 5001)})
	e.expectErr(404, "not_found", "PUT", "/api/v1/works/nope/review", alice.AccessToken, map[string]any{"rating": 3})
	e.expectErr(404, "not_found", "PUT", "/api/v1/works/"+strings.Repeat("0", 64)+"/review", alice.AccessToken, map[string]any{"rating": 3})
	// A first review needs the work in the library.
	e.expectErr(422, "unknown_work", "PUT", path, bob.AccessToken, map[string]any{"rating": 3})
	e.expectErr(401, "unauthorized", "PUT", path, "", map[string]any{"rating": 3})

	rv := e.review(alice, w, 4, "**Great** <b>read</b>\n\n- one")
	if rv.Rating == nil || *rv.Rating != 4 || !rv.Mine || rv.Edited || rv.Status != "visible" || rv.Author.Username != "alice" {
		t.Fatalf("review = %+v", rv)
	}
	if rv.BodyHTML != "<p><strong>Great</strong> &lt;b&gt;read&lt;/b&gt;</p><ul><li>one</li></ul>" {
		t.Fatalf("bodyHtml = %q", rv.BodyHTML)
	}

	// Saving the same thing again is not an edit; a change is, and it keeps the id.
	same := expect[reviewView](e, 200, "PUT", path, alice.AccessToken, map[string]any{"rating": 4, "body": "**Great** <b>read</b>\n\n- one"})
	if same.Edited || same.ID != rv.ID {
		t.Fatalf("unchanged save = %+v", same)
	}
	edited := expect[reviewView](e, 200, "PUT", path, alice.AccessToken, map[string]any{"body": "Changed my mind", "spoiler": true})
	if edited.ID != rv.ID || edited.Rating != nil || !edited.Spoiler || !edited.Edited || edited.Body != "Changed my mind" {
		t.Fatalf("edited = %+v", edited)
	}

	// The work page and the lists.
	page := e.workPage(alice.AccessToken, w)
	if page.Work.Hash != w.Hash || !page.InLibrary || page.MyReview == nil || page.MyReview.ID != rv.ID || page.ReviewCount != 1 ||
		page.Rating.Count != 0 || page.Rating.Average != nil {
		t.Fatalf("work page = %+v", page)
	}
	if other := e.workPage(bob.AccessToken, w); other.InLibrary || other.MyReview != nil || other.ReviewCount != 1 {
		t.Fatalf("bob's work page = %+v", other)
	}
	mine := expect[reviewPage](e, 200, "GET", "/api/v1/me/reviews", alice.AccessToken, nil)
	if mine.Total != 1 || mine.Reviews[0].Work == nil || mine.Reviews[0].Work.Hash != w.Hash {
		t.Fatalf("my reviews = %+v", mine)
	}
	if list := e.reviewsOf(bob.AccessToken, w, ""); list.Total != 1 || list.Reviews[0].Mine || list.Reviews[0].Work != nil {
		t.Fatalf("list = %+v", list)
	}

	// An existing review can still be edited after the work left the library.
	push(e, alice.AccessToken, 0, nil, tombstone("library", w.Hash, clock(nowMs()+1000, 0, alice.Device.SyncDeviceID)))
	expect[reviewView](e, 200, "PUT", path, alice.AccessToken, map[string]any{"rating": 2})

	e.do(204, "DELETE", path, alice.AccessToken)
	e.expectErr(404, "not_found", "DELETE", path, alice.AccessToken, nil)
	if page := e.workPage(alice.AccessToken, w); page.MyReview != nil || page.ReviewCount != 0 {
		t.Fatalf("after delete = %+v", page)
	}
	e.expectErr(404, "not_found", "GET", "/api/v1/works/"+strings.Repeat("0", 64), alice.AccessToken, nil)
}

func TestReviewRatingAndSort(t *testing.T) {
	e := newEnv(t, noNewAccountCap)
	alice, bob, carol, dave := e.register("alice"), e.register("bob"), e.register("carol"), e.register("dave")
	var w syncsvc.Work
	for _, s := range []session{alice, bob, carol} {
		w = e.addToLibrary(s, "w1")
	}
	ra := e.review(alice, w, 5, "first")
	rb := e.review(bob, w, 2, "second")
	expect[reviewView](e, 201, "PUT", "/api/v1/works/"+w.Hash+"/review", carol.AccessToken, map[string]any{"body": "no rating"})

	page := e.workPage(dave.AccessToken, w)
	if page.Rating.Count != 2 || page.Rating.Average == nil || *page.Rating.Average != 3.5 || page.ReviewCount != 3 {
		t.Fatalf("summary = %+v", page)
	}
	if got := authors(e.reviewsOf(dave.AccessToken, w, "")); got != "carol,bob,alice" {
		t.Fatalf("newest = %s", got)
	}

	// Helpful votes.
	e.expectErr(422, "validation_failed", "PUT", "/api/v1/reviews/"+ra.ID+"/helpful", alice.AccessToken, nil)
	type vote struct {
		Helpful      int  `json:"helpful"`
		VotedHelpful bool `json:"votedHelpful"`
	}
	for _, s := range []session{bob, dave, dave} {
		expect[vote](e, 200, "PUT", "/api/v1/reviews/"+ra.ID+"/helpful", s.AccessToken, nil)
	}
	if v := expect[vote](e, 200, "PUT", "/api/v1/reviews/"+rb.ID+"/helpful", dave.AccessToken, nil); v.Helpful != 1 || !v.VotedHelpful {
		t.Fatalf("vote = %+v", v)
	}
	list := e.reviewsOf(dave.AccessToken, w, "?sort=helpful")
	if got := authors(list); got != "alice,bob,carol" || list.Reviews[0].Helpful != 2 || !list.Reviews[0].VotedHelpful {
		t.Fatalf("helpful = %s %+v", got, list.Reviews[0])
	}
	if v := expect[vote](e, 200, "DELETE", "/api/v1/reviews/"+ra.ID+"/helpful", dave.AccessToken, nil); v.Helpful != 1 || v.VotedHelpful {
		t.Fatalf("unvote = %+v", v)
	}
	e.expectErr(400, "bad_request", "GET", "/api/v1/works/"+w.Hash+"/reviews?sort=best", dave.AccessToken, nil)
	if p := e.reviewsOf(dave.AccessToken, w, "?limit=1&offset=1"); p.Total != 3 || len(p.Reviews) != 1 || p.Reviews[0].Author.Username != "bob" {
		t.Fatalf("paging = %+v", p)
	}
	e.expectErr(404, "not_found", "PUT", "/api/v1/reviews/00000000-0000-0000-0000-000000000000/helpful", dave.AccessToken, nil)
}

func TestReviewReplies(t *testing.T) {
	e := newEnv(t, noNewAccountCap)
	alice, bob, carol := e.register("alice"), e.register("bob"), e.register("carol")
	w := e.addToLibrary(alice, "w1")
	rv := e.review(alice, w, 4, "text")
	replies := "/api/v1/reviews/" + rv.ID + "/replies"

	e.expectErr(422, "validation_failed", "POST", replies, bob.AccessToken, map[string]any{"body": " "})
	e.expectErr(422, "validation_failed", "POST", replies, bob.AccessToken, map[string]any{"body": strings.Repeat("x", 2001)})
	first := expect[replyView](e, 201, "POST", replies, bob.AccessToken, map[string]any{"body": "*agreed*"})
	if first.BodyHTML != "<p><em>agreed</em></p>" || !first.Mine || first.ReviewID != rv.ID {
		t.Fatalf("reply = %+v", first)
	}
	expect[replyView](e, 201, "POST", replies, alice.AccessToken, map[string]any{"body": "thanks"})
	second := expect[replyView](e, 201, "POST", replies, carol.AccessToken, map[string]any{"body": "me too"})

	page := expect[replyPage](e, 200, "GET", replies, alice.AccessToken, nil)
	if page.Total != 3 || page.Replies[0].Author.Username != "bob" || page.Replies[2].Author.Username != "carol" {
		t.Fatalf("replies = %+v", page)
	}
	if got := e.reviewsOf(bob.AccessToken, w, "").Reviews[0].ReplyCount; got != 3 {
		t.Fatalf("replyCount = %d", got)
	}

	// Only the author deletes a reply.
	e.expectErr(404, "not_found", "DELETE", "/api/v1/replies/"+second.ID, bob.AccessToken, nil)
	e.do(204, "DELETE", "/api/v1/replies/"+second.ID, carol.AccessToken)

	// Someone the review's author blocks cannot reply; someone who blocks the author does not see the review.
	e.do(204, "PUT", "/api/v1/me/blocks/"+bob.User.ID, alice.AccessToken)
	e.expectErr(403, "forbidden", "POST", replies, bob.AccessToken, map[string]any{"body": "again"})
	e.do(204, "PUT", "/api/v1/me/blocks/"+alice.User.ID, carol.AccessToken)
	e.expectErr(404, "not_found", "POST", replies, carol.AccessToken, map[string]any{"body": "again"})
	e.expectErr(404, "not_found", "GET", replies, carol.AccessToken, nil)
	if list := e.reviewsOf(carol.AccessToken, w, ""); list.Total != 0 {
		t.Fatalf("carol sees %+v", list)
	}
	// Alice does not see the replies of the account she blocks.
	if page := expect[replyPage](e, 200, "GET", replies, alice.AccessToken, nil); page.Total != 1 || page.Replies[0].Author.Username != "alice" {
		t.Fatalf("alice sees %+v", page)
	}
	if got := e.reviewsOf(alice.AccessToken, w, "").Reviews[0].ReplyCount; got != 1 {
		t.Fatalf("replyCount for alice = %d", got)
	}

	// Deleting the review takes the replies with it.
	e.do(204, "DELETE", "/api/v1/works/"+w.Hash+"/review", alice.AccessToken)
	e.expectErr(404, "not_found", "DELETE", "/api/v1/replies/"+first.ID, bob.AccessToken, nil)
}

func TestReviewsSwitchAndVisitors(t *testing.T) {
	e := newEnv(t, noNewAccountCap)
	admin := e.registerAdmin("root")
	alice := e.register("alice")
	w := e.addToLibrary(alice, "w1")
	rv := e.review(alice, w, 4, "text")
	type features struct {
		Features struct {
			Reviews         bool `json:"reviews"`
			VisitorBrowsing bool `json:"visitorBrowsing"`
		} `json:"features"`
	}
	if f := expect[features](e, 200, "GET", "/.well-known/shinsou", "", nil); !f.Features.Reviews || f.Features.VisitorBrowsing {
		t.Fatalf("features = %+v", f)
	}

	// Visitors are refused by default.
	e.expectErr(401, "unauthorized", "GET", "/api/v1/works/"+w.Hash, "", nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/works/"+w.Hash+"/reviews", "", nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/reviews/"+rv.ID+"/replies", "", nil)

	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"visitorBrowsing": "enabled"})
	if f := expect[features](e, 200, "GET", "/.well-known/shinsou", "", nil); !f.Features.VisitorBrowsing {
		t.Fatalf("features = %+v", f)
	}
	page := e.workPage("", w)
	if page.ReviewCount != 1 || page.MyReview != nil || page.InLibrary {
		t.Fatalf("visitor page = %+v", page)
	}
	if list := e.reviewsOf("", w, ""); list.Total != 1 || list.Reviews[0].Mine || list.Reviews[0].VotedHelpful {
		t.Fatalf("visitor list = %+v", list)
	}
	expect[replyPage](e, 200, "GET", "/api/v1/reviews/"+rv.ID+"/replies", "", nil)
	// Reading is all a visitor gets, and a bad token is not a visitor.
	e.expectErr(401, "unauthorized", "PUT", "/api/v1/reviews/"+rv.ID+"/helpful", "", nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me/reviews", "", nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/works/"+w.Hash, "not-a-token", nil)

	// Switching reviews off closes everything, visitors included.
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"reviews": "disabled"})
	if f := expect[features](e, 200, "GET", "/.well-known/shinsou", "", nil); f.Features.Reviews || f.Features.VisitorBrowsing {
		t.Fatalf("features = %+v", f)
	}
	e.expectErr(403, "feature_disabled", "GET", "/api/v1/works/"+w.Hash, "", nil)
	e.expectErr(403, "feature_disabled", "GET", "/api/v1/works/"+w.Hash, alice.AccessToken, nil)
	e.expectErr(403, "feature_disabled", "PUT", "/api/v1/works/"+w.Hash+"/review", alice.AccessToken, map[string]any{"rating": 1})
	e.expectErr(403, "feature_disabled", "GET", "/api/v1/me/reviews", alice.AccessToken, nil)
	e.expectErr(422, "validation_failed", "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"reviews": "maybe"})
	// Moderation still works.
	expect[modReportPage](e, 200, "GET", "/api/v1/mod/reports", admin.AccessToken, nil)
}

func TestReviewsNeedVerifiedEmail(t *testing.T) {
	e := newEnv(t, withMail, withVerification(config.EmailRequired), noNewAccountCap)
	alice := e.registerWith("alice", "alice@example.com")
	e.expectErr(403, "email_unverified", "GET", "/api/v1/me/reviews", alice.AccessToken, nil)
	e.expectErr(403, "email_unverified", "GET", "/api/v1/works/"+strings.Repeat("0", 64), alice.AccessToken, nil)
}

func TestReviewPostingCaps(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.ReviewsPerDay, c.NewAccountPostsPerDay, c.ReportsPerDay = 3, 2, 1 })
	alice, bob := e.register("alice"), e.register("bob")
	w1, w2, w3 := e.addToLibrary(alice, "w1"), e.addToLibrary(alice, "w2"), e.addToLibrary(alice, "w3")
	e.addToLibrary(bob, "w1")
	e.addToLibrary(bob, "w2")
	r1 := e.review(bob, w1, 3, "x")
	r2 := e.review(bob, w2, 3, "y")

	// A new account gets the lower cap; reviews and replies count together, edits do not.
	e.review(alice, w1, 3, "a")
	expect[replyView](e, 201, "POST", "/api/v1/reviews/"+r1.ID+"/replies", alice.AccessToken, map[string]any{"body": "b"})
	got := e.expectErr(429, "rate_limited", "PUT", "/api/v1/works/"+w2.Hash+"/review", alice.AccessToken, map[string]any{"rating": 1})
	if got.Error.RetryAfterSeconds < 1 {
		t.Fatalf("retry after = %d", got.Error.RetryAfterSeconds)
	}
	e.expectErr(429, "rate_limited", "POST", "/api/v1/reviews/"+r1.ID+"/replies", alice.AccessToken, map[string]any{"body": "c"})
	expect[reviewView](e, 200, "PUT", "/api/v1/works/"+w1.Hash+"/review", alice.AccessToken, map[string]any{"rating": 5})

	// An older account gets the ordinary cap.
	if _, err := e.pool.Exec(context.Background(), `update users set created_at = now() - interval '30 days' where username = 'alice'`); err != nil {
		t.Fatal(err)
	}
	e.review(alice, w2, 1, "")
	e.expectErr(429, "rate_limited", "PUT", "/api/v1/works/"+w3.Hash+"/review", alice.AccessToken, map[string]any{"rating": 1})

	// Reports have their own cap; repeating an open report is not a new one.
	rep := expect[reportView](e, 201, "POST", "/api/v1/reviews/"+r1.ID+"/report", alice.AccessToken, map[string]any{"reason": "spam"})
	if again := expect[reportView](e, 200, "POST", "/api/v1/reviews/"+r1.ID+"/report", alice.AccessToken, map[string]any{"reason": "other"}); again.ID != rep.ID || again.Reason != "spam" {
		t.Fatalf("repeated report = %+v", again)
	}
	e.expectErr(429, "rate_limited", "POST", "/api/v1/reviews/"+r2.ID+"/report", alice.AccessToken, map[string]any{"reason": "spam"})
}

func TestReviewReportsAndModeration(t *testing.T) {
	e := newEnv(t, noNewAccountCap)
	admin := e.registerAdmin("root")
	alice, bob, carol, mod := e.register("alice"), e.register("bob"), e.register("carol"), e.register("maud")
	if err := setRole(e, "maud", "moderator"); err != nil {
		t.Fatal(err)
	}
	w := e.addToLibrary(alice, "w1")
	e.addToLibrary(bob, "w1")
	ra := e.review(alice, w, 5, "buy pills")
	rb := e.review(bob, w, 1, "fine")
	reply := expect[replyView](e, 201, "POST", "/api/v1/reviews/"+rb.ID+"/replies", alice.AccessToken, map[string]any{"body": "rude words"})

	// Reporting.
	e.expectErr(422, "validation_failed", "POST", "/api/v1/reviews/"+ra.ID+"/report", bob.AccessToken, map[string]any{"reason": "boring"})
	e.expectErr(422, "validation_failed", "POST", "/api/v1/reviews/"+ra.ID+"/report", alice.AccessToken, map[string]any{"reason": "spam"})
	e.expectErr(422, "validation_failed", "POST", "/api/v1/reviews/"+ra.ID+"/report", bob.AccessToken, map[string]any{"reason": "spam", "note": strings.Repeat("x", 501)})
	e.expectErr(404, "not_found", "POST", "/api/v1/replies/"+ra.ID+"/report", bob.AccessToken, map[string]any{"reason": "spam"})
	expect[reportView](e, 201, "POST", "/api/v1/reviews/"+ra.ID+"/report", bob.AccessToken, map[string]any{"reason": "spam", "note": "ad"})
	expect[reportView](e, 201, "POST", "/api/v1/reviews/"+ra.ID+"/report", carol.AccessToken, map[string]any{"reason": "other"})
	replyReport := expect[reportView](e, 201, "POST", "/api/v1/replies/"+reply.ID+"/report", bob.AccessToken, map[string]any{"reason": "harassment"})

	// The moderation endpoints are for moderators and administrators.
	e.expectErr(403, "forbidden", "GET", "/api/v1/mod/reports", alice.AccessToken, nil)
	e.expectErr(403, "forbidden", "POST", "/api/v1/mod/reviews/"+ra.ID+"/hide", alice.AccessToken, map[string]any{"reason": "x"})
	e.expectErr(401, "unauthorized", "GET", "/api/v1/mod/reports", "", nil)
	e.expectErr(400, "bad_request", "GET", "/api/v1/mod/reports?status=all", mod.AccessToken, nil)

	open := expect[modReportPage](e, 200, "GET", "/api/v1/mod/reports", mod.AccessToken, nil)
	if open.Total != 3 || open.Open != 3 || open.Reports[0].Reporter.Username != "bob" || open.Reports[0].TargetType != "review" ||
		open.Reports[0].Review == nil || open.Reports[0].Review.ID != ra.ID || open.Reports[0].Review.Work == nil {
		t.Fatalf("open reports = %+v", open)
	}
	if last := open.Reports[2]; last.TargetType != "reply" || last.Reply == nil || last.Reply.ID != reply.ID || last.Review == nil || last.Review.ID != rb.ID {
		t.Fatalf("reply report = %+v", last)
	}

	// Hiding through a report closes every open report on the review.
	e.expectErr(422, "validation_failed", "POST", "/api/v1/mod/reports/"+open.Reports[0].ID+"/resolve", mod.AccessToken, map[string]any{"action": "ban"})
	res := expect[map[string]string](e, 200, "POST", "/api/v1/mod/reports/"+open.Reports[0].ID+"/resolve", mod.AccessToken, map[string]any{"action": "hide"})
	if res["resolution"] != "hidden" {
		t.Fatalf("resolve = %v", res)
	}
	e.expectErr(409, "already_resolved", "POST", "/api/v1/mod/reports/"+open.Reports[1].ID+"/resolve", mod.AccessToken, map[string]any{"action": "dismiss"})
	resolved := expect[modReportPage](e, 200, "GET", "/api/v1/mod/reports?status=resolved", mod.AccessToken, nil)
	if resolved.Total != 2 || resolved.Open != 1 || resolved.Reports[0].Resolution == nil || *resolved.Reports[0].Resolution != "hidden" ||
		resolved.Reports[0].ResolvedBy == nil || resolved.Reports[0].ResolvedBy.Username != "maud" {
		t.Fatalf("resolved = %+v", resolved)
	}

	// A hidden review is gone for everyone but its author, and no longer counts.
	if list := e.reviewsOf(carol.AccessToken, w, ""); authors(list) != "bob" {
		t.Fatalf("carol sees %s", authors(list))
	}
	if page := e.workPage(carol.AccessToken, w); page.ReviewCount != 1 || page.Rating.Count != 1 || *page.Rating.Average != 1 {
		t.Fatalf("summary = %+v", page)
	}
	e.expectErr(404, "not_found", "PUT", "/api/v1/reviews/"+ra.ID+"/helpful", carol.AccessToken, nil)
	e.expectErr(404, "not_found", "POST", "/api/v1/reviews/"+ra.ID+"/replies", carol.AccessToken, map[string]any{"body": "x"})
	own := e.workPage(alice.AccessToken, w).MyReview
	if own == nil || own.Status != "hidden" || own.HiddenReason == nil || *own.HiddenReason != "reported: spam" {
		t.Fatalf("alice's own = %+v", own)
	}
	if list := e.reviewsOf(alice.AccessToken, w, ""); list.Total != 2 {
		t.Fatalf("alice sees %d", list.Total)
	}

	// The hidden list, and unhiding.
	hidden := expect[reviewPage](e, 200, "GET", "/api/v1/mod/reviews", mod.AccessToken, nil)
	if hidden.Total != 1 || hidden.Reviews[0].ID != ra.ID || hidden.Reviews[0].HiddenReason == nil || hidden.Reviews[0].Work == nil {
		t.Fatalf("hidden = %+v", hidden)
	}
	back := expect[reviewView](e, 200, "POST", "/api/v1/mod/reviews/"+ra.ID+"/unhide", mod.AccessToken, nil)
	if back.Status != "visible" || back.HiddenReason != nil {
		t.Fatalf("unhidden = %+v", back)
	}
	if list := e.reviewsOf(carol.AccessToken, w, ""); list.Total != 2 {
		t.Fatalf("after unhide carol sees %d", list.Total)
	}

	// Hiding directly needs a reason; replies work the same way.
	e.expectErr(422, "validation_failed", "POST", "/api/v1/mod/replies/"+reply.ID+"/hide", mod.AccessToken, map[string]any{"reason": " "})
	hr := expect[replyView](e, 200, "POST", "/api/v1/mod/replies/"+reply.ID+"/hide", mod.AccessToken, map[string]any{"reason": "insult"})
	if hr.Status != "hidden" || hr.HiddenReason == nil || *hr.HiddenReason != "insult" {
		t.Fatalf("hidden reply = %+v", hr)
	}
	e.expectErr(409, "already_resolved", "POST", "/api/v1/mod/reports/"+replyReport.ID+"/resolve", mod.AccessToken, map[string]any{"action": "dismiss"})
	if page := expect[replyPage](e, 200, "GET", "/api/v1/reviews/"+rb.ID+"/replies", bob.AccessToken, nil); page.Total != 0 {
		t.Fatalf("bob sees %+v", page)
	}
	if page := expect[replyPage](e, 200, "GET", "/api/v1/reviews/"+rb.ID+"/replies", alice.AccessToken, nil); page.Total != 1 || page.Replies[0].HiddenReason == nil {
		t.Fatalf("alice sees %+v", page)
	}
	if list := expect[replyPage](e, 200, "GET", "/api/v1/mod/replies", mod.AccessToken, nil); list.Total != 1 {
		t.Fatalf("hidden replies = %+v", list)
	}

	// Dismissing and deleting.
	rep := expect[reportView](e, 201, "POST", "/api/v1/reviews/"+ra.ID+"/report", carol.AccessToken, map[string]any{"reason": "spoiler"})
	if res := expect[map[string]string](e, 200, "POST", "/api/v1/mod/reports/"+rep.ID+"/resolve", admin.AccessToken, map[string]any{"action": "dismiss"}); res["resolution"] != "dismissed" {
		t.Fatalf("dismiss = %v", res)
	}
	rep = expect[reportView](e, 201, "POST", "/api/v1/reviews/"+ra.ID+"/report", carol.AccessToken, map[string]any{"reason": "illegal"})
	if res := expect[map[string]string](e, 200, "POST", "/api/v1/mod/reports/"+rep.ID+"/resolve", mod.AccessToken, map[string]any{"action": "delete"}); res["resolution"] != "deleted" {
		t.Fatalf("delete = %v", res)
	}
	e.expectErr(404, "not_found", "POST", "/api/v1/mod/reports/"+rep.ID+"/resolve", mod.AccessToken, map[string]any{"action": "dismiss"})
	if e.workPage(alice.AccessToken, w).MyReview != nil {
		t.Fatal("the review survived deletion")
	}
	e.do(204, "DELETE", "/api/v1/mod/replies/"+reply.ID, mod.AccessToken)
	e.expectErr(404, "not_found", "DELETE", "/api/v1/mod/replies/"+reply.ID, mod.AccessToken, nil)

	// Every action is in the audit log.
	log := auditEntries(e, admin.AccessToken)
	for _, action := range []string{"review.hide", "review.unhide", "reply.hide", "report.dismiss", "review.delete", "reply.delete"} {
		if !hasAction(log, action) {
			t.Errorf("audit log has no %s entry", action)
		}
	}
}

func TestModeratorSuspension(t *testing.T) {
	e := newEnv(t, noNewAccountCap)
	admin := e.registerAdmin("root")
	alice, bob, mod, mod2 := e.register("alice"), e.register("bob"), e.register("maud"), e.register("mona")
	for _, name := range []string{"maud", "mona"} {
		if err := setRole(e, name, "moderator"); err != nil {
			t.Fatal(err)
		}
	}
	w := e.addToLibrary(alice, "w1")
	e.addToLibrary(bob, "w1")
	e.review(alice, w, 5, "text")
	rb := e.review(bob, w, 3, "text")
	expect[replyView](e, 201, "POST", "/api/v1/reviews/"+rb.ID+"/replies", alice.AccessToken, map[string]any{"body": "hello"})
	suspend := func(id string) string { return "/api/v1/mod/users/" + id + "/suspend" }

	e.expectErr(403, "forbidden", "POST", suspend(bob.User.ID), alice.AccessToken, map[string]any{"reason": "x"})
	e.expectErr(422, "validation_failed", "POST", suspend(alice.User.ID), mod.AccessToken, nil)
	// A moderator may only act on ordinary accounts; an administrator on moderators as well.
	e.expectErr(403, "forbidden", "POST", suspend(mod2.User.ID), mod.AccessToken, map[string]any{"reason": "x"})
	e.expectErr(403, "forbidden", "POST", suspend(admin.User.ID), mod.AccessToken, map[string]any{"reason": "x"})
	e.expectErr(403, "forbidden", "POST", suspend(admin.User.ID), admin.AccessToken, map[string]any{"reason": "x"})
	e.expectErr(404, "not_found", "POST", suspend("00000000-0000-0000-0000-000000000000"), mod.AccessToken, map[string]any{"reason": "x"})

	if st, raw := e.call("POST", suspend(alice.User.ID), mod.AccessToken, map[string]any{"reason": "spam"}); st != 204 {
		t.Fatalf("suspend = %d %s", st, raw)
	}
	// The account is signed out everywhere and what it wrote is hidden.
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", alice.AccessToken, nil)
	if list := e.reviewsOf(bob.AccessToken, w, ""); authors(list) != "bob" || list.Reviews[0].ReplyCount != 0 {
		t.Fatalf("after suspension = %+v", list)
	}
	if page := e.workPage(bob.AccessToken, w); page.ReviewCount != 1 || page.Rating.Count != 1 {
		t.Fatalf("summary = %+v", page)
	}

	e.do(204, "POST", "/api/v1/mod/users/"+alice.User.ID+"/unsuspend", mod.AccessToken)
	if list := e.reviewsOf(bob.AccessToken, w, ""); list.Total != 2 {
		t.Fatalf("after reinstating = %+v", list)
	}
	if st, raw := e.call("POST", suspend(mod2.User.ID), admin.AccessToken, map[string]any{"reason": "abuse"}); st != 204 {
		t.Fatalf("admin suspends moderator = %d %s", st, raw)
	}
	log := auditEntries(e, admin.AccessToken)
	if !hasAction(log, "user.suspend") || !hasAction(log, "user.unsuspend") {
		t.Fatalf("audit log = %+v", log)
	}
}

func TestReviewsInExportAndAccountDeletion(t *testing.T) {
	e := newEnv(t, noNewAccountCap)
	alice, bob := e.register("alice"), e.register("bob")
	w := e.addToLibrary(alice, "w1")
	e.addToLibrary(bob, "w1")
	ra := e.review(alice, w, 5, "mine")
	rb := e.review(bob, w, 2, "his")
	expect[replyView](e, 201, "POST", "/api/v1/reviews/"+rb.ID+"/replies", alice.AccessToken, map[string]any{"body": "reply"})
	e.do(200, "PUT", "/api/v1/reviews/"+rb.ID+"/helpful", alice.AccessToken)
	expect[reportView](e, 201, "POST", "/api/v1/reviews/"+rb.ID+"/report", alice.AccessToken, map[string]any{"reason": "spoiler"})

	export := expect[struct {
		Reviews struct {
			Reviews      []reviewView `json:"reviews"`
			Replies      []replyView  `json:"replies"`
			HelpfulVotes []string     `json:"helpfulVotes"`
			Reports      []reportView `json:"reports"`
		} `json:"reviews"`
	}](e, 200, "GET", "/api/v1/me/export", alice.AccessToken, nil).Reviews
	if len(export.Reviews) != 1 || export.Reviews[0].ID != ra.ID || export.Reviews[0].Work == nil || len(export.Replies) != 1 ||
		len(export.HelpfulVotes) != 1 || export.HelpfulVotes[0] != rb.ID || len(export.Reports) != 1 {
		t.Fatalf("export = %+v", export)
	}

	// Deleting the account takes its reviews, replies, votes and reports with it.
	if st, raw := e.call("POST", "/api/v1/me/delete", alice.AccessToken, map[string]any{"currentPassword": "correct horse"}); st/100 != 2 {
		t.Fatalf("delete account = %d %s", st, raw)
	}
	list := e.reviewsOf(bob.AccessToken, w, "")
	if list.Total != 1 || list.Reviews[0].Helpful != 0 || list.Reviews[0].ReplyCount != 0 {
		t.Fatalf("after account deletion = %+v", list)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from review_reports`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("reports left = %d %v", n, err)
	}
}
