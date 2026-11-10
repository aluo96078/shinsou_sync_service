// Package social implements follows, blocks and sharing works between accounts on the same server
// (docs/spec/sharing.md).
//
// Lock order: a transaction that changes follows, blocks or shares first locks the user rows it
// involves, in id order. Accepting a share writes the library record through the sync service in
// its own transaction, never nested in one of these.
package social

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

// Limits from the API contract.
const (
	MaxMessageRunes = 500
	MaxQueryRunes   = 32
	MaxSearchLimit  = 50
)

// Receive policies.
const (
	ReceiveEveryone  = "everyone"
	ReceiveFollowing = "following"
	ReceiveNobody    = "nobody"
)

// Share states.
const (
	StatusPending   = "pending"
	StatusAccepted  = "accepted"
	StatusDismissed = "dismissed"
)

// serverDevice is the device part of the clocks on records the server writes.
const serverDevice = "server"

var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// UserSummary is another account as seen by the caller.
type UserSummary struct {
	ID         uuid.UUID `json:"id"`
	Username   string    `json:"username"`
	Following  bool      `json:"following"`
	FollowsYou bool      `json:"followsYou"`
	Blocked    bool      `json:"blocked"`
	CanShare   bool      `json:"canShare"`
	Since      *int64    `json:"since,omitempty"`
}

// UserPage is a page of accounts.
type UserPage struct {
	Users  []UserSummary `json:"users"`
	Total  int           `json:"total"`
	Offset int           `json:"offset"`
	Limit  int           `json:"limit"`
}

// Party is one side of a share.
type Party struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
}

// Share is a share as seen by one of its two sides.
type Share struct {
	ID          uuid.UUID    `json:"id"`
	Sender      Party        `json:"sender"`
	Recipient   Party        `json:"recipient"`
	Work        syncsvc.Work `json:"work"`
	Message     string       `json:"message"`
	Status      string       `json:"status"`
	CreatedAt   int64        `json:"createdAt"`
	RespondedAt *int64       `json:"respondedAt"`
	InLibrary   bool         `json:"inLibrary"`
}

// SharePage is a page of shares.
type SharePage struct {
	Shares  []Share `json:"shares"`
	Total   int     `json:"total"`
	Offset  int     `json:"offset"`
	Limit   int     `json:"limit"`
	Pending int     `json:"pending"`
}

// Export is the social part of an account export.
type Export struct {
	ReceiveFrom string  `json:"receiveFrom"`
	Following   []Party `json:"following"`
	Followers   []Party `json:"followers"`
	Blocks      []Party `json:"blocks"`
	Shares      []Share `json:"shares"`
}

// Service implements the social features.
type Service struct {
	pool         *pgxpool.Pool
	sync         *syncsvc.Service
	enabled      func() bool
	sharesPerDay int
}

// New creates the service. enabled reports whether sharing is switched on right now; sharesPerDay
// caps the shares one account creates in 24 hours (0 = unlimited).
func New(pool *pgxpool.Pool, sync *syncsvc.Service, enabled func() bool, sharesPerDay int) *Service {
	return &Service{pool: pool, sync: sync, enabled: enabled, sharesPerDay: sharesPerDay}
}

// Enabled reports whether sharing is switched on.
func (s *Service) Enabled() bool { return s.enabled() }

// ErrDisabled is the answer of every share endpoint while sharing is switched off.
func ErrDisabled() *apierr.Error {
	return apierr.New(http.StatusForbidden, "feature_disabled", "sharing is turned off on this server")
}

func errNoUser() *apierr.Error { return apierr.NotFound("no such user") }

func errNoShare() *apierr.Error { return apierr.NotFound("no such share") }

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// summaryCols describes user u relative to the caller ($1). accepts is whether u's preference
// lets the caller share with u; blocked_by is whether u blocks the caller.
const summaryCols = `u.id, u.username,
	exists(select 1 from follows f where f.follower_id = $1 and f.followee_id = u.id) as following,
	exists(select 1 from follows f where f.follower_id = u.id and f.followee_id = $1) as follows_you,
	exists(select 1 from blocks b where b.blocker_id = $1 and b.blocked_id = u.id) as blocked,
	exists(select 1 from blocks b where b.blocker_id = u.id and b.blocked_id = $1) as blocked_by,
	(u.share_policy = 'everyone' or (u.share_policy = 'following'
		and exists(select 1 from follows f where f.follower_id = u.id and f.followee_id = $1))) as accepts,
	u.status = 'active' as active`

func (s *Service) scanSummaries(rows pgx.Rows, withSince bool) ([]UserSummary, error) {
	enabled := s.enabled()
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (UserSummary, error) {
		var (
			u                          UserSummary
			blockedBy, accepts, active bool
			since                      *int64
		)
		dst := []any{&u.ID, &u.Username, &u.Following, &u.FollowsYou, &u.Blocked, &blockedBy, &accepts, &active}
		if withSince {
			dst = append(dst, &since)
		}
		if err := r.Scan(dst...); err != nil {
			return u, err
		}
		u.Since = since
		u.CanShare = enabled && active && accepts && !u.Blocked && !blockedBy
		return u, nil
	})
	if out == nil {
		out = []UserSummary{}
	}
	return out, err
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Search lists active accounts whose user name starts with q. The caller and accounts that block
// the caller are left out.
func (s *Service) Search(ctx context.Context, me uuid.UUID, q string, limit int) ([]UserSummary, error) {
	q = strings.TrimSpace(q)
	if n := utf8.RuneCountInString(q); n < 1 || n > MaxQueryRunes {
		return nil, apierr.BadRequest("q must be 1-" + strconv.Itoa(MaxQueryRunes) + " characters")
	}
	rows, err := s.pool.Query(ctx, `select `+summaryCols+` from users u
		where u.id <> $1 and u.status = 'active' and lower(u.username) like lower($2) || '%' escape '\'
			and not exists(select 1 from blocks b where b.blocker_id = u.id and b.blocked_id = $1)
		order by lower(u.username) limit $3`, me, escapeLike(q), limit)
	if err != nil {
		return nil, err
	}
	return s.scanSummaries(rows, false)
}

// relation selects one of the caller's lists.
type relation struct{ table, mine, other string }

var (
	relFollowing = relation{"follows", "follower_id", "followee_id"}
	relFollowers = relation{"follows", "followee_id", "follower_id"}
	relBlocks    = relation{"blocks", "blocker_id", "blocked_id"}
)

func (s *Service) list(ctx context.Context, me uuid.UUID, rel relation, offset, limit int) (*UserPage, error) {
	page := &UserPage{Offset: offset, Limit: limit}
	// The names in rel are constants from this file.
	if err := s.pool.QueryRow(ctx, `select count(*) from `+rel.table+` where `+rel.mine+` = $1`, me).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select `+summaryCols+`, (extract(epoch from x.created_at) * 1000)::bigint
		from `+rel.table+` x join users u on u.id = x.`+rel.other+`
		where x.`+rel.mine+` = $1 order by x.created_at desc, u.id offset $2 limit $3`, me, offset, limit)
	if err != nil {
		return nil, err
	}
	page.Users, err = s.scanSummaries(rows, true)
	return page, err
}

// Following lists the accounts the caller follows.
func (s *Service) Following(ctx context.Context, me uuid.UUID, offset, limit int) (*UserPage, error) {
	return s.list(ctx, me, relFollowing, offset, limit)
}

// Followers lists the accounts that follow the caller.
func (s *Service) Followers(ctx context.Context, me uuid.UUID, offset, limit int) (*UserPage, error) {
	return s.list(ctx, me, relFollowers, offset, limit)
}

// Blocks lists the accounts the caller blocks.
func (s *Service) Blocks(ctx context.Context, me uuid.UUID, offset, limit int) (*UserPage, error) {
	return s.list(ctx, me, relBlocks, offset, limit)
}

type lockedUser struct {
	active bool
	policy string
}

// lockUsers locks the given user rows in id order and returns the ones that exist.
func lockUsers(ctx context.Context, tx pgx.Tx, ids ...uuid.UUID) (map[uuid.UUID]lockedUser, error) {
	rows, err := tx.Query(ctx, `select id, status = 'active', share_policy from users where id = any($1) order by id for update`, ids)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]lockedUser{}
	var (
		id uuid.UUID
		u  lockedUser
	)
	_, err = pgx.ForEachRow(rows, []any{&id, &u.active, &u.policy}, func() error {
		out[id] = u
		return nil
	})
	return out, err
}

func exists(ctx context.Context, tx pgx.Tx, sql string, args ...any) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists(`+sql+`)`, args...).Scan(&ok)
	return ok, err
}

func blocks(ctx context.Context, tx pgx.Tx, blocker, blocked uuid.UUID) (bool, error) {
	return exists(ctx, tx, `select 1 from blocks where blocker_id = $1 and blocked_id = $2`, blocker, blocked)
}

func follows(ctx context.Context, tx pgx.Tx, follower, followee uuid.UUID) (bool, error) {
	return exists(ctx, tx, `select 1 from follows where follower_id = $1 and followee_id = $2`, follower, followee)
}

// Follow makes the caller follow target.
func (s *Service) Follow(ctx context.Context, me, target uuid.UUID) error {
	if me == target {
		return apierr.Validation("you cannot follow yourself")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		users, err := lockUsers(ctx, tx, me, target)
		if err != nil {
			return err
		}
		if t, ok := users[target]; !ok || !t.active {
			return errNoUser()
		}
		if hidden, err := blocks(ctx, tx, target, me); err != nil {
			return err
		} else if hidden {
			return errNoUser()
		}
		if mine, err := blocks(ctx, tx, me, target); err != nil {
			return err
		} else if mine {
			return apierr.New(http.StatusConflict, "user_blocked", "unblock this user first")
		}
		_, err = tx.Exec(ctx, `insert into follows (follower_id, followee_id) values ($1, $2) on conflict do nothing`, me, target)
		return err
	})
}

// Unfollow makes the caller stop following target.
func (s *Service) Unfollow(ctx context.Context, me, target uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `delete from follows where follower_id = $1 and followee_id = $2`, me, target)
	return err
}

// Block makes the caller block target: both stop following each other and target's open shares
// to the caller are dismissed.
func (s *Service) Block(ctx context.Context, me, target uuid.UUID) error {
	if me == target {
		return apierr.Validation("you cannot block yourself")
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		users, err := lockUsers(ctx, tx, me, target)
		if err != nil {
			return err
		}
		if _, ok := users[target]; !ok {
			return errNoUser()
		}
		if _, err := tx.Exec(ctx, `insert into blocks (blocker_id, blocked_id) values ($1, $2) on conflict do nothing`, me, target); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from follows where (follower_id = $1 and followee_id = $2) or (follower_id = $2 and followee_id = $1)`,
			me, target); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update shares set status = 'dismissed', responded_at = now()
			where sender_id = $2 and recipient_id = $1 and status = 'pending'`, me, target)
		return err
	})
}

// Unblock removes the caller's block on target.
func (s *Service) Unblock(ctx context.Context, me, target uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `delete from blocks where blocker_id = $1 and blocked_id = $2`, me, target)
	return err
}

// ReceiveFrom returns who may share with the caller.
func (s *Service) ReceiveFrom(ctx context.Context, me uuid.UUID) (string, error) {
	var p string
	err := s.pool.QueryRow(ctx, `select share_policy from users where id = $1`, me).Scan(&p)
	return p, err
}

// SetReceiveFrom changes who may share with the caller.
func (s *Service) SetReceiveFrom(ctx context.Context, me uuid.UUID, policy string) error {
	switch policy {
	case ReceiveEveryone, ReceiveFollowing, ReceiveNobody:
	default:
		return apierr.Validation("receiveFrom must be everyone, following or nobody")
	}
	_, err := s.pool.Exec(ctx, `update users set share_policy = $2 where id = $1`, me, policy)
	return err
}

// shareSelect reads shares as seen by the caller ($1).
const shareSelect = `select s.id, s.sender_id, su.username, s.recipient_id, ru.username, s.message, s.status,
	(extract(epoch from s.created_at) * 1000)::bigint, (extract(epoch from s.responded_at) * 1000)::bigint,
	w.hash, w.key_version, w.package_id, w.source_id, w.remote_id, w.title, w.author, w.cover_url,
	w.content_kind, w.language, w.source_name, w.repository_url,
	exists(select 1 from sync_records r where r.user_id = $1 and r.kind = 'library' and r.key = w.hash and not r.is_deleted)
from shares s
join users su on su.id = s.sender_id
join users ru on ru.id = s.recipient_id
join works w on w.id = s.work_id`

// scanShare reads one shareSelect row and hides from the sender what only the recipient may know:
// a dismissed share reads as pending.
func scanShare(row pgx.Row, viewer uuid.UUID) (Share, error) {
	var (
		sh Share
		kv int16
	)
	w := &sh.Work
	err := row.Scan(&sh.ID, &sh.Sender.ID, &sh.Sender.Username, &sh.Recipient.ID, &sh.Recipient.Username, &sh.Message, &sh.Status,
		&sh.CreatedAt, &sh.RespondedAt,
		&w.Hash, &kv, &w.PackageID, &w.SourceID, &w.RemoteID, &w.Title, &w.Author, &w.CoverURL,
		&w.ContentKind, &w.Language, &w.SourceName, &w.RepositoryURL, &sh.InLibrary)
	if err != nil {
		return sh, err
	}
	w.KeyVersion = int(kv)
	if viewer != sh.Recipient.ID && sh.Status != StatusAccepted {
		sh.Status, sh.RespondedAt = StatusPending, nil
	}
	return sh, nil
}

func getShare(ctx context.Context, q querier, viewer, id uuid.UUID) (*Share, error) {
	sh, err := scanShare(q.QueryRow(ctx, shareSelect+` where s.id = $2 and (s.sender_id = $1 or s.recipient_id = $1)`, viewer, id), viewer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoShare()
	}
	return &sh, err
}

// CreateShare shares the work with hash from the caller's library with recipient. created is
// false when the same share was open already and is returned as is.
func (s *Service) CreateShare(ctx context.Context, me, recipient uuid.UUID, hash, message string) (share *Share, created bool, err error) {
	message = strings.TrimSpace(message)
	if !hashRe.MatchString(hash) {
		return nil, false, apierr.Validation("work must be a work hash")
	}
	if utf8.RuneCountInString(message) > MaxMessageRunes {
		return nil, false, apierr.Validation("message must be at most " + strconv.Itoa(MaxMessageRunes) + " characters")
	}
	if strings.ContainsRune(message, 0) {
		return nil, false, apierr.Validation("message contains an invalid character")
	}
	if me == recipient {
		return nil, false, apierr.Validation("you cannot share with yourself")
	}
	var id uuid.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		users, err := lockUsers(ctx, tx, me, recipient)
		if err != nil {
			return err
		}
		to, ok := users[recipient]
		if !ok || !to.active {
			return errNoUser()
		}
		if mine, err := blocks(ctx, tx, me, recipient); err != nil {
			return err
		} else if mine {
			return apierr.New(http.StatusConflict, "user_blocked", "unblock this user first")
		}
		refused := to.policy == ReceiveNobody
		if !refused {
			if refused, err = blocks(ctx, tx, recipient, me); err != nil {
				return err
			}
		}
		if !refused && to.policy == ReceiveFollowing {
			followed, err := follows(ctx, tx, recipient, me)
			if err != nil {
				return err
			}
			refused = !followed
		}
		if refused {
			return apierr.New(http.StatusForbidden, "share_refused", "this user does not accept shares from you")
		}
		var workID int64
		err = tx.QueryRow(ctx, `select w.id from works w
			join sync_records r on r.user_id = $1 and r.kind = 'library' and r.key = w.hash and not r.is_deleted
			where w.hash = $2`, me, hash).Scan(&workID)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(http.StatusUnprocessableEntity, "unknown_work", "the work is not in your library")
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `select id from shares where sender_id = $1 and recipient_id = $2 and work_id = $3 and status = 'pending'`,
			me, recipient, workID).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if s.sharesPerDay > 0 {
			var (
				n      int
				oldest *time.Time
			)
			if err := tx.QueryRow(ctx, `select count(*), min(created_at) from shares
				where sender_id = $1 and created_at > now() - interval '24 hours'`, me).Scan(&n, &oldest); err != nil {
				return err
			}
			if n >= s.sharesPerDay {
				wait := 60
				if oldest != nil {
					wait = max(1, int(time.Until(oldest.Add(24*time.Hour)).Seconds())+1)
				}
				return &apierr.Error{Status: http.StatusTooManyRequests, Code: "rate_limited",
					Message: "too many shares today; try again later", RetryAfterSeconds: wait}
			}
		}
		created = true
		return tx.QueryRow(ctx, `insert into shares (sender_id, recipient_id, work_id, message) values ($1, $2, $3, $4) returning id`,
			me, recipient, workID, message).Scan(&id)
	})
	if err != nil {
		return nil, false, err
	}
	share, err = getShare(ctx, s.pool, me, id)
	return share, created, err
}

// PendingCount is the number of received shares waiting for an answer.
func (s *Service) PendingCount(ctx context.Context, me uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `select count(*) from shares where recipient_id = $1 and status = 'pending'`, me).Scan(&n)
	return n, err
}

// ListShares lists the caller's inbox (sent = false) or sent shares, newest first. status
// filters by state; empty means all.
func (s *Service) ListShares(ctx context.Context, me uuid.UUID, sent bool, status string, offset, limit int) (*SharePage, error) {
	where := ` where s.recipient_id = $1`
	if sent {
		where = ` where s.sender_id = $1`
	}
	if !slices.Contains([]string{"", StatusPending, StatusAccepted, StatusDismissed}, status) {
		return nil, apierr.BadRequest("status must be pending, accepted or dismissed")
	}
	switch {
	case status == "":
	case !sent:
		where += ` and s.status = '` + status + `'` // one of the three constants, checked above
	case status == StatusPending:
		where += ` and s.status in ('pending', 'dismissed')`
	case status == StatusAccepted:
		where += ` and s.status = 'accepted'`
	default:
		where += ` and false`
	}
	page := &SharePage{Offset: offset, Limit: limit, Shares: []Share{}}
	var err error
	if page.Pending, err = s.PendingCount(ctx, me); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `select count(*) from shares s`+where, me).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, shareSelect+where+` order by s.created_at desc, s.id offset $2 limit $3`, me, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		sh, err := scanShare(rows, me)
		if err != nil {
			return nil, err
		}
		page.Shares = append(page.Shares, sh)
	}
	return page, rows.Err()
}

// Accept adds the shared work to the caller's library and marks the share accepted. Only the
// recipient can accept; repeating it is harmless.
func (s *Service) Accept(ctx context.Context, me, id uuid.UUID) (*Share, error) {
	sh, err := getShare(ctx, s.pool, me, id)
	if err != nil {
		return nil, err
	}
	if sh.Recipient.ID != me {
		return nil, errNoShare()
	}
	if !sh.InLibrary {
		if err := s.addToLibrary(ctx, me, sh.Work.Hash); err != nil {
			return nil, err
		}
	}
	if _, err := s.pool.Exec(ctx, `update shares set status = 'accepted', responded_at = now()
		where id = $1 and recipient_id = $2 and status <> 'accepted'`, id, me); err != nil {
		return nil, err
	}
	return getShare(ctx, s.pool, me, id)
}

// addToLibrary writes a library record for the work through the ordinary push path, so the
// merge rules, the quota and the sequence numbers are the same as for a device.
func (s *Service) addToLibrary(ctx context.Context, me uuid.UUID, hash string) error {
	now := time.Now().UnixMilli()
	clock := strconv.FormatInt(now, 10) + ":0:" + serverDevice
	fields := map[string]json.RawMessage{
		"addedAt":      json.RawMessage(strconv.FormatInt(now, 10)),
		"categoryIds":  json.RawMessage(`[]`),
		"pinned":       json.RawMessage(`false`),
		"archived":     json.RawMessage(`false`),
		"updatePolicy": json.RawMessage(`"auto"`),
	}
	clocks := make(map[string]string, len(fields))
	for k := range fields {
		clocks[k] = clock
	}
	no, since := false, int64(0)
	resp, err := s.sync.Push(ctx, me, syncsvc.PushRequest{
		BatchID: uuid.NewString(),
		Since:   &since,
		Records: []syncsvc.PushRecord{{Kind: "library", Key: hash, Fields: fields, Clocks: clocks,
			Deleted: &syncsvc.PushDeleted{Value: &no, Clock: clock}}},
	})
	if err != nil {
		return err
	}
	if len(resp.Results) != 1 {
		return errors.New("social: unexpected push result")
	}
	switch r := resp.Results[0].Result; {
	case r == syncsvc.ResultQuotaExceeded:
		return apierr.New(http.StatusConflict, "quota_exceeded", "your account has no room for another library entry")
	case strings.HasPrefix(r, "rejected"):
		return errors.New("social: library record " + r)
	}
	return nil
}

// Dismiss marks an open share dismissed. Only the recipient can dismiss.
func (s *Service) Dismiss(ctx context.Context, me, id uuid.UUID) (*Share, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `select status from shares where id = $1 and recipient_id = $2 for update`, id, me).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoShare()
		}
		if err != nil {
			return err
		}
		switch status {
		case StatusAccepted:
			return apierr.New(http.StatusConflict, "conflict", "the share was accepted already")
		case StatusDismissed:
			return nil
		}
		_, err = tx.Exec(ctx, `update shares set status = 'dismissed', responded_at = now() where id = $1`, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return getShare(ctx, s.pool, me, id)
}

// Delete removes a share for both sides. Either side may delete.
func (s *Service) Delete(ctx context.Context, me, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `delete from shares where id = $1 and (sender_id = $2 or recipient_id = $2)`, id, me)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNoShare()
	}
	return nil
}

// ExportData reads the social part of an account export through q (the export's snapshot).
func ExportData(ctx context.Context, q querier, me uuid.UUID) (*Export, error) {
	out := &Export{Shares: []Share{}}
	if err := q.QueryRow(ctx, `select share_policy from users where id = $1`, me).Scan(&out.ReceiveFrom); err != nil {
		return nil, err
	}
	parties := func(rel relation) ([]Party, error) {
		rows, err := q.Query(ctx, `select u.id, u.username from `+rel.table+` x join users u on u.id = x.`+rel.other+`
			where x.`+rel.mine+` = $1 order by x.created_at, u.id`, me)
		if err != nil {
			return nil, err
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Party])
		if list == nil {
			list = []Party{}
		}
		return list, err
	}
	var err error
	if out.Following, err = parties(relFollowing); err != nil {
		return nil, err
	}
	if out.Followers, err = parties(relFollowers); err != nil {
		return nil, err
	}
	if out.Blocks, err = parties(relBlocks); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, shareSelect+` where s.sender_id = $1 or s.recipient_id = $1 order by s.created_at, s.id`, me)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		sh, err := scanShare(rows, me)
		if err != nil {
			return nil, err
		}
		out.Shares = append(out.Shares, sh)
	}
	return out, rows.Err()
}
