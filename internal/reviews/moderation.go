package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
)

// Moderation locks the review or reply it acts on first and its reports after that, on every path.

// Roles that matter to moderation.
const (
	roleUser      = "user"
	roleModerator = "moderator"
	roleAdmin     = "admin"
)

// CanModerate reports whether an account with role may use the moderation tools.
func CanModerate(role string) bool { return role == roleModerator || role == roleAdmin }

// Resolutions of a report.
const (
	ActionDismiss = "dismiss"
	ActionHide    = "hide"
	ActionDelete  = "delete"
)

// ModReport is a report as seen by a moderator. Review is the reported review, or the review the
// reported reply belongs to.
type ModReport struct {
	ID         uuid.UUID `json:"id"`
	Reporter   Party     `json:"reporter"`
	TargetType string    `json:"targetType"`
	Reason     string    `json:"reason"`
	Note       string    `json:"note"`
	Status     string    `json:"status"`
	Resolution *string   `json:"resolution"`
	ResolvedBy *Party    `json:"resolvedBy"`
	ResolvedAt *int64    `json:"resolvedAt"`
	CreatedAt  int64     `json:"createdAt"`
	Review     *Review   `json:"review"`
	Reply      *Reply    `json:"reply,omitempty"`
}

// ModReportPage is a page of reports. Open is the number of reports waiting for an answer.
type ModReportPage struct {
	Reports []ModReport `json:"reports"`
	Total   int         `json:"total"`
	Offset  int         `json:"offset"`
	Limit   int         `json:"limit"`
	Open    int         `json:"open"`
}

func audit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action, targetType, targetID string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into audit_log (actor_id, action, target_type, target_id, detail) values ($1, $2, $3, $4, $5)`,
		actor, action, targetType, targetID, raw)
	return err
}

// Reports lists reports with the given status: open ones oldest first, resolved ones latest first.
func (s *Service) Reports(ctx context.Context, actor uuid.UUID, status string, offset, limit int) (*ModReportPage, error) {
	var order string
	switch status {
	case "", "open":
		status, order = "open", `x.created_at, x.id`
	case "resolved":
		order = `x.resolved_at desc, x.id`
	default:
		return nil, apierr.BadRequest("status must be open or resolved")
	}
	page := &ModReportPage{Offset: offset, Limit: limit, Reports: []ModReport{}}
	err := s.pool.QueryRow(ctx, `select count(*) filter (where status = $1), count(*) filter (where status = 'open') from review_reports`,
		status).Scan(&page.Total, &page.Open)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select x.id, x.reporter_id, ru.username, x.review_id, x.reply_id, x.reason, x.note, x.status, x.resolution,
			x.resolved_by, mu.username, (extract(epoch from x.resolved_at) * 1000)::bigint, (extract(epoch from x.created_at) * 1000)::bigint,
			coalesce(x.review_id, (select rp.review_id from review_replies rp where rp.id = x.reply_id))
		from review_reports x
		join users ru on ru.id = x.reporter_id
		left join users mu on mu.id = x.resolved_by
		where x.status = $1 order by `+order+` offset $2 limit $3`, status, offset, limit)
	if err != nil {
		return nil, err
	}
	var (
		rp        ModReport
		reviewID  *uuid.UUID
		replyID   *uuid.UUID
		modID     *uuid.UUID
		modName   *string
		parent    uuid.UUID
		parents   []uuid.UUID
		reviewIDs []uuid.UUID
		replyIDs  []uuid.UUID
		replyOf   = map[uuid.UUID]uuid.UUID{} // report -> reply
	)
	_, err = pgx.ForEachRow(rows, []any{&rp.ID, &rp.Reporter.ID, &rp.Reporter.Username, &reviewID, &replyID, &rp.Reason, &rp.Note, &rp.Status,
		&rp.Resolution, &modID, &modName, &rp.ResolvedAt, &rp.CreatedAt, &parent}, func() error {
		item := rp
		item.TargetType = "review"
		if replyID != nil {
			item.TargetType = "reply"
			replyOf[item.ID] = *replyID
			replyIDs = append(replyIDs, *replyID)
		}
		if modID != nil && modName != nil {
			item.ResolvedBy = &Party{ID: *modID, Username: *modName}
		}
		parents = append(parents, parent)
		reviewIDs = append(reviewIDs, parent)
		page.Reports = append(page.Reports, item)
		return nil
	})
	if err != nil || len(page.Reports) == 0 {
		return page, err
	}
	rrows, err := s.pool.Query(ctx, reviewSelect+` where r.id = any($2)`, actor, reviewIDs)
	if err != nil {
		return nil, err
	}
	reviews, err := collectReviews(rrows, actor, true, true)
	if err != nil {
		return nil, err
	}
	var replies []Reply
	if len(replyIDs) > 0 {
		prows, err := s.pool.Query(ctx, replySelect+` where rp.id = any($2) and $1::uuid is not null`, actor, replyIDs)
		if err != nil {
			return nil, err
		}
		if replies, err = collectReplies(prows, actor, true); err != nil {
			return nil, err
		}
	}
	for i := range page.Reports {
		item := &page.Reports[i]
		if j := slices.IndexFunc(reviews, func(r Review) bool { return r.ID == parents[i] }); j >= 0 {
			item.Review = &reviews[j]
		}
		if id, ok := replyOf[item.ID]; ok {
			if j := slices.IndexFunc(replies, func(r Reply) bool { return r.ID == id }); j >= 0 {
				item.Reply = &replies[j]
			}
		}
	}
	return page, nil
}

// ModReviews lists reviews with the given status (hidden by default), each with its work.
func (s *Service) ModReviews(ctx context.Context, actor uuid.UUID, status string, offset, limit int) (*ReviewPage, error) {
	status, err := modStatus(status)
	if err != nil {
		return nil, err
	}
	page := &ReviewPage{Offset: offset, Limit: limit}
	if err := s.pool.QueryRow(ctx, `select count(*) from reviews where status = $1`, status).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, reviewSelect+` where r.status = $2 order by coalesce(r.hidden_at, r.created_at) desc, r.id offset $3 limit $4`,
		actor, status, offset, limit)
	if err != nil {
		return nil, err
	}
	page.Reviews, err = collectReviews(rows, actor, true, true)
	return page, err
}

// ModReplies lists replies with the given status (hidden by default).
func (s *Service) ModReplies(ctx context.Context, actor uuid.UUID, status string, offset, limit int) (*ReplyPage, error) {
	status, err := modStatus(status)
	if err != nil {
		return nil, err
	}
	page := &ReplyPage{Offset: offset, Limit: limit}
	if err := s.pool.QueryRow(ctx, `select count(*) from review_replies where status = $1`, status).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, replySelect+` where rp.status = $2 and $1::uuid is not null
		order by coalesce(rp.hidden_at, rp.created_at) desc, rp.id offset $3 limit $4`, actor, status, offset, limit)
	if err != nil {
		return nil, err
	}
	page.Replies, err = collectReplies(rows, actor, true)
	return page, err
}

func modStatus(status string) (string, error) {
	switch status {
	case "", StatusHidden:
		return StatusHidden, nil
	case StatusVisible:
		return StatusVisible, nil
	}
	return "", apierr.BadRequest("status must be hidden or visible")
}

// kind names the two things moderation acts on. The strings are constants used to build SQL.
type kind struct{ name, table, reportCol string }

var (
	kindReview = kind{"review", "reviews", "review_id"}
	kindReply  = kind{"reply", "review_replies", "reply_id"}
)

func kindOf(reply bool) kind {
	if reply {
		return kindReply
	}
	return kindReview
}

func (k kind) missing() *apierr.Error {
	if k == kindReply {
		return errNoReply()
	}
	return errNoReview()
}

// apply hides, unhides or deletes a review or a reply inside tx, closes its open reports and
// writes the audit entry. It returns the ids of the reports it closed.
func apply(ctx context.Context, tx pgx.Tx, actor uuid.UUID, k kind, id uuid.UUID, action, reason string) ([]uuid.UUID, error) {
	var (
		author  uuid.UUID
		status  string
		excerpt string
	)
	err := tx.QueryRow(ctx, `select user_id, status, left(body, 200) from `+k.table+` where id = $1 for update`, id).Scan(&author, &status, &excerpt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, k.missing()
	}
	if err != nil {
		return nil, err
	}
	detail := map[string]any{"author": author}
	if reason != "" {
		detail["reason"] = reason
	}
	var resolution string
	switch action {
	case "hide":
		resolution = "hidden"
		_, err = tx.Exec(ctx, `update `+k.table+` set status = 'hidden', hidden_reason = $2, hidden_at = now() where id = $1`, id, reason)
	case "unhide":
		_, err = tx.Exec(ctx, `update `+k.table+` set status = 'visible', hidden_reason = null, hidden_at = null where id = $1`, id)
	case "dismiss":
		resolution = "dismissed"
	case "delete":
		resolution = "deleted"
		detail["excerpt"] = excerpt
	}
	if err != nil {
		return nil, err
	}
	var closed []uuid.UUID
	if resolution != "" {
		rows, err := tx.Query(ctx, `update review_reports set status = 'resolved', resolution = $2, resolved_by = $3, resolved_at = now()
			where `+k.reportCol+` = $1 and status = 'open' returning id`, id, resolution, actor)
		if err != nil {
			return nil, err
		}
		if closed, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
			return nil, err
		}
		detail["reports"] = len(closed)
	}
	if action == "delete" {
		// The reports go with their target; the audit entry is what stays.
		if _, err := tx.Exec(ctx, `delete from `+k.table+` where id = $1`, id); err != nil {
			return nil, err
		}
	}
	if action == "dismiss" {
		return closed, audit(ctx, tx, actor, "report.dismiss", k.name, id.String(), detail)
	}
	return closed, audit(ctx, tx, actor, k.name+"."+action, k.name, id.String(), detail)
}

func cleanReason(reason string, required bool) (string, error) {
	reason, err := cleanText("reason", reason, MaxReasonRunes)
	if err != nil {
		return "", err
	}
	if required && reason == "" {
		return "", apierr.Validation("a reason is required")
	}
	return reason, nil
}

// Hide hides a review or a reply from everyone but its author and closes its open reports.
func (s *Service) Hide(ctx context.Context, actor, id uuid.UUID, reply bool, reason string) error {
	reason, err := cleanReason(reason, true)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := apply(ctx, tx, actor, kindOf(reply), id, "hide", reason)
		return err
	})
}

// Unhide makes a hidden review or reply visible again.
func (s *Service) Unhide(ctx context.Context, actor, id uuid.UUID, reply bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := apply(ctx, tx, actor, kindOf(reply), id, "unhide", "")
		return err
	})
}

// Remove deletes a review or a reply as a moderator.
func (s *Service) Remove(ctx context.Context, actor, id uuid.UUID, reply bool, reason string) error {
	reason, err := cleanReason(reason, false)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := apply(ctx, tx, actor, kindOf(reply), id, "delete", reason)
		return err
	})
}

// ModReview returns a review as a moderator sees it.
func (s *Service) ModReview(ctx context.Context, actor, id uuid.UUID) (*Review, error) {
	return getReview(ctx, s.pool, actor, id, true)
}

// ModReply returns a reply as a moderator sees it.
func (s *Service) ModReply(ctx context.Context, actor, id uuid.UUID) (*Reply, error) {
	return getReply(ctx, s.pool, actor, id, true)
}

// Resolve answers a report and every other open report on the same review or reply: dismiss
// leaves the content alone, hide hides it, delete removes it. It returns the resolution.
func (s *Service) Resolve(ctx context.Context, actor, id uuid.UUID, action string) (string, error) {
	if action != ActionDismiss && action != ActionHide && action != ActionDelete {
		return "", apierr.Validation("action must be dismiss, hide or delete")
	}
	var resolution string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			reviewID, replyID *uuid.UUID
			reason            string
		)
		err := tx.QueryRow(ctx, `select review_id, reply_id, reason from review_reports where id = $1`, id).Scan(&reviewID, &replyID, &reason)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoReport()
		}
		if err != nil {
			return err
		}
		k, target := kindReview, reviewID
		if replyID != nil {
			k, target = kindReply, replyID
		}
		why := ""
		if action == ActionHide {
			why = "reported: " + reason
		}
		closed, err := apply(ctx, tx, actor, k, *target, action, why)
		if err != nil {
			var e *apierr.Error
			if errors.As(err, &e) && e.Status == http.StatusNotFound {
				return errNoReport()
			}
			return err
		}
		if !slices.Contains(closed, id) {
			return apierr.New(http.StatusConflict, "already_resolved", "this report has been answered already")
		}
		resolution = map[string]string{ActionDismiss: "dismissed", ActionHide: "hidden", ActionDelete: "deleted"}[action]
		return nil
	})
	return resolution, err
}

// SetSuspended suspends or reinstates an account from the moderation tools. A moderator may act
// on ordinary accounts, an administrator on moderators as well; administrators are managed from
// the administration pages.
func (s *Service) SetSuspended(ctx context.Context, actor uuid.UUID, actorRole string, target uuid.UUID, suspended bool, reason string) error {
	reason, err := cleanReason(reason, suspended)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var role, status string
		err := tx.QueryRow(ctx, `select role, status from users where id = $1 for update`, target).Scan(&role, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoUser()
		}
		if err != nil {
			return err
		}
		if role != roleUser && (actorRole != roleAdmin || role != roleModerator) {
			return apierr.Forbidden("you cannot suspend this account")
		}
		want, action := "active", "user.unsuspend"
		if suspended {
			want, action = "suspended", "user.suspend"
		}
		if status == want {
			return nil
		}
		if _, err := tx.Exec(ctx, `update users set status = $2 where id = $1`, target, want); err != nil {
			return err
		}
		if suspended {
			if err := auth.RevokeUserDevices(ctx, tx, target); err != nil {
				return err
			}
		}
		detail := map[string]any{"via": "moderation"}
		if reason != "" {
			detail["reason"] = reason
		}
		return audit(ctx, tx, actor, action, "user", target.String(), detail)
	})
}
