// Package reviews implements reviews of works: one review per account and work, helpful votes,
// one level of replies, reports and the moderation tools (docs/spec/reviews.md).
//
// Lock order: a transaction that writes a review, a reply or a report first locks the writer's
// user row, which also serialises the per-day caps. Moderation locks the row it acts on.
package reviews

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/markdown"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

// Limits from the API contract.
const (
	MaxBodyRunes   = 5000
	MaxReplyRunes  = 2000
	MaxNoteRunes   = 500
	MaxReasonRunes = 500
)

// States of a review or a reply.
const (
	StatusVisible = "visible"
	StatusHidden  = "hidden"
)

// Sort orders of a work's reviews.
const (
	SortNewest  = "newest"
	SortHelpful = "helpful"
)

var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

var reportReasons = map[string]bool{"spam": true, "harassment": true, "spoiler": true, "illegal": true, "other": true}

// Party is an account named in a review, a reply or a report.
type Party struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
}

// Review is a review as seen by the caller.
type Review struct {
	ID           uuid.UUID     `json:"id"`
	Author       Party         `json:"author"`
	Rating       *int          `json:"rating"`
	Body         string        `json:"body"`
	BodyHTML     string        `json:"bodyHtml"`
	Spoiler      bool          `json:"spoiler"`
	Status       string        `json:"status"`
	HiddenReason *string       `json:"hiddenReason,omitempty"`
	Helpful      int           `json:"helpful"`
	VotedHelpful bool          `json:"votedHelpful"`
	ReplyCount   int           `json:"replyCount"`
	CreatedAt    int64         `json:"createdAt"`
	UpdatedAt    int64         `json:"updatedAt"`
	Edited       bool          `json:"edited"`
	Mine         bool          `json:"mine"`
	Work         *syncsvc.Work `json:"work,omitempty"`
}

// ReviewPage is a page of reviews.
type ReviewPage struct {
	Reviews []Review `json:"reviews"`
	Total   int      `json:"total"`
	Offset  int      `json:"offset"`
	Limit   int      `json:"limit"`
}

// Rating summarises the ratings of a work.
type Rating struct {
	Average *float64 `json:"average"`
	Count   int      `json:"count"`
}

// WorkPage is a work with its review summary.
type WorkPage struct {
	Work        syncsvc.Work `json:"work"`
	Rating      Rating       `json:"rating"`
	ReviewCount int          `json:"reviewCount"`
	MyReview    *Review      `json:"myReview"`
	InLibrary   bool         `json:"inLibrary"`
}

// Reply is a reply to a review as seen by the caller.
type Reply struct {
	ID           uuid.UUID `json:"id"`
	ReviewID     uuid.UUID `json:"reviewId"`
	Author       Party     `json:"author"`
	Body         string    `json:"body"`
	BodyHTML     string    `json:"bodyHtml"`
	Status       string    `json:"status"`
	HiddenReason *string   `json:"hiddenReason,omitempty"`
	CreatedAt    int64     `json:"createdAt"`
	Mine         bool      `json:"mine"`
}

// ReplyPage is a page of replies.
type ReplyPage struct {
	Replies []Reply `json:"replies"`
	Total   int     `json:"total"`
	Offset  int     `json:"offset"`
	Limit   int     `json:"limit"`
}

// Vote is the answer to a helpful vote.
type Vote struct {
	Helpful      int  `json:"helpful"`
	VotedHelpful bool `json:"votedHelpful"`
}

// Report is a report as seen by its reporter.
type Report struct {
	ID        uuid.UUID  `json:"id"`
	ReviewID  *uuid.UUID `json:"reviewId,omitempty"`
	ReplyID   *uuid.UUID `json:"replyId,omitempty"`
	Reason    string     `json:"reason"`
	Note      string     `json:"note"`
	Status    string     `json:"status"`
	CreatedAt int64      `json:"createdAt"`
}

// Limits are the posting caps (0 = unlimited).
type Limits struct {
	PostsPerDay           int
	ReportsPerDay         int
	NewAccountAge         time.Duration
	NewAccountPostsPerDay int
}

// Service implements reviews.
type Service struct {
	pool     *pgxpool.Pool
	enabled  func() bool
	visitors func() bool
	limits   Limits
}

// New creates the service. enabled reports whether reviews are switched on right now, visitors
// whether people who are not signed in may read them.
func New(pool *pgxpool.Pool, enabled, visitors func() bool, limits Limits) *Service {
	return &Service{pool: pool, enabled: enabled, visitors: visitors, limits: limits}
}

// Enabled reports whether reviews are switched on.
func (s *Service) Enabled() bool { return s.enabled() }

// VisitorBrowsing reports whether work pages and reviews can be read without signing in.
func (s *Service) VisitorBrowsing() bool { return s.enabled() && s.visitors() }

// ErrDisabled is the answer of every review endpoint while reviews are switched off.
func ErrDisabled() *apierr.Error {
	return apierr.New(http.StatusForbidden, "feature_disabled", "reviews are turned off on this server")
}

func errNoWork() *apierr.Error   { return apierr.NotFound("no such work") }
func errNoReview() *apierr.Error { return apierr.NotFound("no such review") }
func errNoReply() *apierr.Error  { return apierr.NotFound("no such reply") }
func errNoReport() *apierr.Error { return apierr.NotFound("no such report") }
func errNoUser() *apierr.Error   { return apierr.NotFound("no such user") }

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const workCols = `w.hash, w.key_version, w.package_id, w.source_id, w.remote_id, w.title, w.author, w.cover_url,
	w.content_kind, w.language, w.source_name, w.repository_url`

func workDst(w *syncsvc.Work, kv *int16) []any {
	return []any{&w.Hash, kv, &w.PackageID, &w.SourceID, &w.RemoteID, &w.Title, &w.Author, &w.CoverURL,
		&w.ContentKind, &w.Language, &w.SourceName, &w.RepositoryURL}
}

// notBlocked is true when the viewer ($1) does not block the account in col.
func notBlocked(col string) string {
	return `not exists(select 1 from blocks b where b.blocker_id = $1 and b.blocked_id = ` + col + `)`
}

// reviewSelect reads reviews as seen by the viewer ($1; the nil id for a visitor).
const reviewSelect = `select r.id, r.user_id, u.username, r.rating, r.body, r.spoiler, r.status, r.hidden_reason,
	(select count(*) from review_votes v where v.review_id = r.id),
	exists(select 1 from review_votes v where v.review_id = r.id and v.user_id = $1),
	(select count(*) from review_replies rp join users ru on ru.id = rp.user_id
		where rp.review_id = r.id and rp.status = 'visible' and ru.status = 'active'
			and not exists(select 1 from blocks b where b.blocker_id = $1 and b.blocked_id = rp.user_id)),
	(extract(epoch from r.created_at) * 1000)::bigint, (extract(epoch from r.updated_at) * 1000)::bigint,
	` + workCols + `
from reviews r
join users u on u.id = r.user_id
join works w on w.id = r.work_id`

// reviewShown is what the viewer ($1) may read: their own reviews, and visible reviews by active
// accounts they do not block.
var reviewShown = `(r.user_id = $1 or (r.status = 'visible' and u.status = 'active' and ` + notBlocked("r.user_id") + `))`

// reviewPublic is what counts towards a work's rating and review count.
const reviewPublic = `r.status = 'visible' and u.status = 'active'`

func scanReview(row pgx.Row, viewer uuid.UUID, mod, withWork bool) (Review, error) {
	var (
		rv     Review
		rating *int16
		kv     int16
		work   syncsvc.Work
	)
	dst := append([]any{&rv.ID, &rv.Author.ID, &rv.Author.Username, &rating, &rv.Body, &rv.Spoiler, &rv.Status, &rv.HiddenReason,
		&rv.Helpful, &rv.VotedHelpful, &rv.ReplyCount, &rv.CreatedAt, &rv.UpdatedAt}, workDst(&work, &kv)...)
	if err := row.Scan(dst...); err != nil {
		return rv, err
	}
	if rating != nil {
		n := int(*rating)
		rv.Rating = &n
	}
	rv.BodyHTML = markdown.HTML(rv.Body)
	rv.Edited = rv.UpdatedAt > rv.CreatedAt
	rv.Mine = viewer != uuid.Nil && viewer == rv.Author.ID
	if !rv.Mine && !mod {
		rv.HiddenReason = nil
	}
	if withWork {
		work.KeyVersion = int(kv)
		rv.Work = &work
	}
	return rv, nil
}

func collectReviews(rows pgx.Rows, viewer uuid.UUID, mod, withWork bool) ([]Review, error) {
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		rv, err := scanReview(rows, viewer, mod, withWork)
		if err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

// replySelect reads replies as seen by the viewer ($1).
const replySelect = `select rp.id, rp.review_id, rp.user_id, ru.username, rp.body, rp.status, rp.hidden_reason,
	(extract(epoch from rp.created_at) * 1000)::bigint
from review_replies rp
join users ru on ru.id = rp.user_id`

var replyShown = `(rp.user_id = $1 or (rp.status = 'visible' and ru.status = 'active' and ` + notBlocked("rp.user_id") + `))`

func scanReply(row pgx.Row, viewer uuid.UUID, mod bool) (Reply, error) {
	var rp Reply
	if err := row.Scan(&rp.ID, &rp.ReviewID, &rp.Author.ID, &rp.Author.Username, &rp.Body, &rp.Status, &rp.HiddenReason, &rp.CreatedAt); err != nil {
		return rp, err
	}
	rp.BodyHTML = markdown.HTML(rp.Body)
	rp.Mine = viewer != uuid.Nil && viewer == rp.Author.ID
	if !rp.Mine && !mod {
		rp.HiddenReason = nil
	}
	return rp, nil
}

func collectReplies(rows pgx.Rows, viewer uuid.UUID, mod bool) ([]Reply, error) {
	defer rows.Close()
	out := []Reply{}
	for rows.Next() {
		rp, err := scanReply(rows, viewer, mod)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	return out, rows.Err()
}

func checkHash(hash string) error {
	if !hashRe.MatchString(hash) {
		return errNoWork()
	}
	return nil
}

// cleanText trims text and checks its length.
func cleanText(what, text string, maxRunes int) (string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if utf8.RuneCountInString(text) > maxRunes {
		return "", apierr.Validation(what + " must be at most " + strconv.Itoa(maxRunes) + " characters")
	}
	if strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		return "", apierr.Validation(what + " contains an invalid character")
	}
	return text, nil
}

// Work returns the work with hash and its review summary as seen by viewer.
func (s *Service) Work(ctx context.Context, viewer uuid.UUID, hash string) (*WorkPage, error) {
	if err := checkHash(hash); err != nil {
		return nil, err
	}
	var (
		page WorkPage
		kv   int16
	)
	dst := append(workDst(&page.Work, &kv), &page.InLibrary, &page.Rating.Average, &page.Rating.Count, &page.ReviewCount)
	err := s.pool.QueryRow(ctx, `select `+workCols+`,
		exists(select 1 from sync_records sr where sr.user_id = $1 and sr.kind = 'library' and sr.key = w.hash and not sr.is_deleted),
		(select round(avg(r.rating), 2)::float8 from reviews r join users u on u.id = r.user_id where r.work_id = w.id and `+reviewPublic+`),
		(select count(r.rating) from reviews r join users u on u.id = r.user_id where r.work_id = w.id and `+reviewPublic+`),
		(select count(*) from reviews r join users u on u.id = r.user_id where r.work_id = w.id and `+reviewPublic+`)
		from works w where w.hash = $2`, viewer, hash).Scan(dst...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoWork()
	}
	if err != nil {
		return nil, err
	}
	page.Work.KeyVersion = int(kv)
	if viewer != uuid.Nil {
		rv, err := scanReview(s.pool.QueryRow(ctx, reviewSelect+` where w.hash = $2 and r.user_id = $1`, viewer, hash), viewer, false, false)
		switch {
		case err == nil:
			page.MyReview = &rv
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
	}
	return &page, nil
}

// List lists the reviews of the work with hash.
func (s *Service) List(ctx context.Context, viewer uuid.UUID, hash, sort string, offset, limit int) (*ReviewPage, error) {
	if err := checkHash(hash); err != nil {
		return nil, err
	}
	var order string
	switch sort {
	case "", SortNewest:
		order = `r.created_at desc, r.id`
	case SortHelpful:
		order = `(select count(*) from review_votes v where v.review_id = r.id) desc, r.created_at desc, r.id`
	default:
		return nil, apierr.BadRequest("sort must be newest or helpful")
	}
	page := &ReviewPage{Offset: offset, Limit: limit}
	var known bool
	err := s.pool.QueryRow(ctx, `select exists(select 1 from works where hash = $2),
		(select count(*) from reviews r join users u on u.id = r.user_id join works w on w.id = r.work_id
			where w.hash = $2 and `+reviewShown+`)`, viewer, hash).Scan(&known, &page.Total)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, errNoWork()
	}
	rows, err := s.pool.Query(ctx, reviewSelect+` where w.hash = $2 and `+reviewShown+` order by `+order+` offset $3 limit $4`,
		viewer, hash, offset, limit)
	if err != nil {
		return nil, err
	}
	page.Reviews, err = collectReviews(rows, viewer, false, false)
	return page, err
}

// Mine lists the caller's own reviews, newest first, each with its work.
func (s *Service) Mine(ctx context.Context, me uuid.UUID, offset, limit int) (*ReviewPage, error) {
	page := &ReviewPage{Offset: offset, Limit: limit}
	if err := s.pool.QueryRow(ctx, `select count(*) from reviews where user_id = $1`, me).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, reviewSelect+` where r.user_id = $1 order by r.updated_at desc, r.id offset $2 limit $3`, me, offset, limit)
	if err != nil {
		return nil, err
	}
	page.Reviews, err = collectReviews(rows, me, false, true)
	return page, err
}

func getReview(ctx context.Context, q querier, viewer, id uuid.UUID, mod bool) (*Review, error) {
	where := ` where r.id = $2 and ` + reviewShown
	if mod {
		where = ` where r.id = $2`
	}
	rv, err := scanReview(q.QueryRow(ctx, reviewSelect+where, viewer, id), viewer, mod, mod)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoReview()
	}
	return &rv, err
}

// lockWriter locks the caller's user row and returns the account's age.
func lockWriter(ctx context.Context, tx pgx.Tx, me uuid.UUID) (time.Duration, error) {
	var created time.Time
	err := tx.QueryRow(ctx, `select created_at from users where id = $1 and status = 'active' for update`, me).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, apierr.Unauthorized("this account is not active")
	}
	return time.Since(created), err
}

func tooMany(n, limit int, oldest *time.Time, what string) error {
	if limit <= 0 || n < limit {
		return nil
	}
	wait := 60
	if oldest != nil {
		wait = max(1, int(time.Until(oldest.Add(24*time.Hour)).Seconds())+1)
	}
	return &apierr.Error{Status: http.StatusTooManyRequests, Code: "rate_limited",
		Message: "too many " + what + " today; try again later", RetryAfterSeconds: wait}
}

// checkPosts enforces the cap on reviews and replies written in 24 hours. The caller holds the
// lock on the writer's user row.
func (s *Service) checkPosts(ctx context.Context, tx pgx.Tx, me uuid.UUID, age time.Duration) error {
	limit := s.limits.PostsPerDay
	if fresh := s.limits.NewAccountPostsPerDay; fresh > 0 && age < s.limits.NewAccountAge && (limit <= 0 || fresh < limit) {
		limit = fresh
	}
	if limit <= 0 {
		return nil
	}
	var (
		n      int
		oldest *time.Time
	)
	err := tx.QueryRow(ctx, `select count(*), min(c) from (
		select created_at as c from reviews where user_id = $1 and created_at > now() - interval '24 hours'
		union all
		select created_at from review_replies where user_id = $1 and created_at > now() - interval '24 hours') x`, me).Scan(&n, &oldest)
	if err != nil {
		return err
	}
	return tooMany(n, limit, oldest, "reviews and replies")
}

// Put writes the caller's review of the work with hash. A first review needs the work in the
// caller's library; created is false when an existing review was changed.
func (s *Service) Put(ctx context.Context, me uuid.UUID, hash string, rating *int, body string, spoiler bool) (rv *Review, created bool, err error) {
	if err := checkHash(hash); err != nil {
		return nil, false, err
	}
	if rating != nil && (*rating < 1 || *rating > 5) {
		return nil, false, apierr.Validation("rating must be from 1 to 5")
	}
	if body, err = cleanText("body", body, MaxBodyRunes); err != nil {
		return nil, false, err
	}
	if rating == nil && body == "" {
		return nil, false, apierr.Validation("a review needs a rating or a text")
	}
	var id uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		age, err := lockWriter(ctx, tx, me)
		if err != nil {
			return err
		}
		var (
			workID    int64
			inLibrary bool
		)
		err = tx.QueryRow(ctx, `select w.id, exists(select 1 from sync_records sr
				where sr.user_id = $1 and sr.kind = 'library' and sr.key = w.hash and not sr.is_deleted)
			from works w where w.hash = $2`, me, hash).Scan(&workID, &inLibrary)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoWork()
		}
		if err != nil {
			return err
		}
		// Only a real change moves updated_at, so saving the same text again does not mark the review as edited.
		err = tx.QueryRow(ctx, `update reviews set rating = $3, body = $4, spoiler = $5,
				updated_at = case when rating is not distinct from $3 and body = $4 and spoiler = $5 then updated_at else now() end
			where work_id = $1 and user_id = $2 returning id`, workID, me, rating, body, spoiler).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !inLibrary {
			return apierr.New(http.StatusUnprocessableEntity, "unknown_work", "the work is not in your library")
		}
		if err := s.checkPosts(ctx, tx, me, age); err != nil {
			return err
		}
		created = true
		return tx.QueryRow(ctx, `insert into reviews (work_id, user_id, rating, body, spoiler) values ($1, $2, $3, $4, $5) returning id`,
			workID, me, rating, body, spoiler).Scan(&id)
	})
	if err != nil {
		return nil, false, err
	}
	rv, err = getReview(ctx, s.pool, me, id, false)
	return rv, created, err
}

// Delete removes the caller's review of the work with hash, with its votes, replies and reports.
func (s *Service) Delete(ctx context.Context, me uuid.UUID, hash string) error {
	if err := checkHash(hash); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `delete from reviews r using works w where w.id = r.work_id and w.hash = $2 and r.user_id = $1`, me, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNoReview()
	}
	return nil
}

// target is a review or a reply somebody acts on.
type target struct {
	author uuid.UUID
	status string
}

// openReview returns the review when the viewer may act on it: it is visible, by an active
// account, and the viewer does not block its author.
func openReview(ctx context.Context, q querier, viewer, id uuid.UUID) (target, error) {
	var t target
	err := q.QueryRow(ctx, `select r.user_id, r.status from reviews r join users u on u.id = r.user_id
		where r.id = $2 and `+reviewPublic+` and `+notBlocked("r.user_id"), viewer, id).Scan(&t.author, &t.status)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, errNoReview()
	}
	return t, err
}

// SetHelpful sets or clears the caller's helpful vote on a review.
func (s *Service) SetHelpful(ctx context.Context, me, id uuid.UUID, on bool) (*Vote, error) {
	var vote Vote
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := openReview(ctx, tx, me, id)
		if err != nil {
			return err
		}
		if t.author == me {
			return apierr.Validation("you cannot vote on your own review")
		}
		if on {
			_, err = tx.Exec(ctx, `insert into review_votes (review_id, user_id) values ($1, $2) on conflict do nothing`, id, me)
		} else {
			_, err = tx.Exec(ctx, `delete from review_votes where review_id = $1 and user_id = $2`, id, me)
		}
		if err != nil {
			return err
		}
		vote.VotedHelpful = on
		return tx.QueryRow(ctx, `select count(*) from review_votes where review_id = $1`, id).Scan(&vote.Helpful)
	})
	if err != nil {
		return nil, err
	}
	return &vote, nil
}

// Replies lists the replies to a review, oldest first.
func (s *Service) Replies(ctx context.Context, viewer, id uuid.UUID, offset, limit int) (*ReplyPage, error) {
	if _, err := getReview(ctx, s.pool, viewer, id, false); err != nil {
		return nil, err
	}
	page := &ReplyPage{Offset: offset, Limit: limit}
	if err := s.pool.QueryRow(ctx, `select count(*) from review_replies rp join users ru on ru.id = rp.user_id
		where rp.review_id = $2 and `+replyShown, viewer, id).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, replySelect+` where rp.review_id = $2 and `+replyShown+` order by rp.created_at, rp.id offset $3 limit $4`,
		viewer, id, offset, limit)
	if err != nil {
		return nil, err
	}
	page.Replies, err = collectReplies(rows, viewer, false)
	return page, err
}

func getReply(ctx context.Context, q querier, viewer, id uuid.UUID, mod bool) (*Reply, error) {
	where := ` where rp.id = $2 and ` + replyShown
	if mod {
		where = ` where rp.id = $2 and $1::uuid is not null`
	}
	rp, err := scanReply(q.QueryRow(ctx, replySelect+where, viewer, id), viewer, mod)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoReply()
	}
	return &rp, err
}

// Reply adds the caller's reply to a review.
func (s *Service) Reply(ctx context.Context, me, id uuid.UUID, body string) (*Reply, error) {
	body, err := cleanText("body", body, MaxReplyRunes)
	if err != nil {
		return nil, err
	}
	if body == "" {
		return nil, apierr.Validation("a reply needs a text")
	}
	var replyID uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		age, err := lockWriter(ctx, tx, me)
		if err != nil {
			return err
		}
		t, err := openReview(ctx, tx, me, id)
		if err != nil {
			return err
		}
		var refused bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from blocks where blocker_id = $1 and blocked_id = $2)`, t.author, me).Scan(&refused); err != nil {
			return err
		}
		if refused {
			return apierr.Forbidden("you cannot reply to this review")
		}
		if err := s.checkPosts(ctx, tx, me, age); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `insert into review_replies (review_id, user_id, body) values ($1, $2, $3) returning id`, id, me, body).Scan(&replyID)
	})
	if err != nil {
		return nil, err
	}
	return getReply(ctx, s.pool, me, replyID, false)
}

// DeleteReply removes one of the caller's replies.
func (s *Service) DeleteReply(ctx context.Context, me, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `delete from review_replies where id = $1 and user_id = $2`, id, me)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNoReply()
	}
	return nil
}

const reportSelect = `select id, review_id, reply_id, reason, note, status, (extract(epoch from created_at) * 1000)::bigint from review_reports`

func scanReport(row pgx.Row) (Report, error) {
	var rp Report
	err := row.Scan(&rp.ID, &rp.ReviewID, &rp.ReplyID, &rp.Reason, &rp.Note, &rp.Status, &rp.CreatedAt)
	return rp, err
}

// Report reports a review (reply false) or a reply (reply true). created is false when the
// caller's open report on the same target is returned as is.
func (s *Service) Report(ctx context.Context, me, id uuid.UUID, reply bool, reason, note string) (report *Report, created bool, err error) {
	if !reportReasons[reason] {
		return nil, false, apierr.Validation("reason must be spam, harassment, spoiler, illegal or other")
	}
	if note, err = cleanText("note", note, MaxNoteRunes); err != nil {
		return nil, false, err
	}
	col := "review_id"
	if reply {
		col = "reply_id"
	}
	var out Report
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := lockWriter(ctx, tx, me); err != nil {
			return err
		}
		var author uuid.UUID
		if reply {
			var reviewID uuid.UUID
			err := tx.QueryRow(ctx, `select rp.user_id, rp.review_id from review_replies rp join users ru on ru.id = rp.user_id
				where rp.id = $2 and rp.status = 'visible' and ru.status = 'active' and `+notBlocked("rp.user_id"), me, id).Scan(&author, &reviewID)
			if errors.Is(err, pgx.ErrNoRows) {
				return errNoReply()
			}
			if err != nil {
				return err
			}
			if _, err := getReview(ctx, tx, me, reviewID, false); err != nil {
				return errNoReply()
			}
		} else {
			t, err := openReview(ctx, tx, me, id)
			if err != nil {
				return err
			}
			author = t.author
		}
		if author == me {
			return apierr.Validation("you cannot report your own content")
		}
		// col is one of two constants from this function.
		out, err = scanReport(tx.QueryRow(ctx, reportSelect+` where reporter_id = $1 and `+col+` = $2 and status = 'open'`, me, id))
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if s.limits.ReportsPerDay > 0 {
			var (
				n      int
				oldest *time.Time
			)
			if err := tx.QueryRow(ctx, `select count(*), min(created_at) from review_reports
				where reporter_id = $1 and created_at > now() - interval '24 hours'`, me).Scan(&n, &oldest); err != nil {
				return err
			}
			if err := tooMany(n, s.limits.ReportsPerDay, oldest, "reports"); err != nil {
				return err
			}
		}
		created = true
		var newID uuid.UUID
		if err := tx.QueryRow(ctx, `insert into review_reports (reporter_id, `+col+`, reason, note) values ($1, $2, $3, $4) returning id`,
			me, id, reason, note).Scan(&newID); err != nil {
			return err
		}
		out, err = scanReport(tx.QueryRow(ctx, reportSelect+` where id = $1`, newID))
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return &out, created, nil
}

// Export is the reviews part of an account export.
type Export struct {
	Reviews      []Review    `json:"reviews"`
	Replies      []Reply     `json:"replies"`
	HelpfulVotes []uuid.UUID `json:"helpfulVotes"`
	Reports      []Report    `json:"reports"`
}

// ExportData reads the reviews part of an account export through q (the export's snapshot).
func ExportData(ctx context.Context, q querier, me uuid.UUID) (*Export, error) {
	out := &Export{}
	rows, err := q.Query(ctx, reviewSelect+` where r.user_id = $1 order by r.created_at, r.id`, me)
	if err != nil {
		return nil, err
	}
	if out.Reviews, err = collectReviews(rows, me, false, true); err != nil {
		return nil, err
	}
	if rows, err = q.Query(ctx, replySelect+` where rp.user_id = $1 order by rp.created_at, rp.id`, me); err != nil {
		return nil, err
	}
	if out.Replies, err = collectReplies(rows, me, false); err != nil {
		return nil, err
	}
	if rows, err = q.Query(ctx, `select review_id from review_votes where user_id = $1 order by created_at, review_id`, me); err != nil {
		return nil, err
	}
	if out.HelpfulVotes, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
		return nil, err
	}
	if out.HelpfulVotes == nil {
		out.HelpfulVotes = []uuid.UUID{}
	}
	if rows, err = q.Query(ctx, reportSelect+` where reporter_id = $1 order by created_at, id`, me); err != nil {
		return nil, err
	}
	defer rows.Close()
	out.Reports = []Report{}
	for rows.Next() {
		rp, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out.Reports = append(out.Reports, rp)
	}
	return out, rows.Err()
}
