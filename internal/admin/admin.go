// Package admin implements the administration endpoints: overview, users, invites and the audit log.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
)

// Service is the administration service.
type Service struct {
	pool           *pgxpool.Pool
	version        string
	emailAvailable bool
}

// New creates the service.
func New(pool *pgxpool.Pool, version string, emailAvailable bool) *Service {
	return &Service{pool: pool, version: version, emailAvailable: emailAvailable}
}

// Overview is AdminOverview in the API.
type Overview struct {
	Version string `json:"version"`
	Users   struct {
		Total     int `json:"total"`
		Active    int `json:"active"`
		Suspended int `json:"suspended"`
		Admins    int `json:"admins"`
	} `json:"users"`
	Devices        int  `json:"devices"`
	Records        int  `json:"records"`
	Works          int  `json:"works"`
	EmailAvailable bool `json:"emailAvailable"`
}

// Overview counts what the instance holds.
func (s *Service) Overview(ctx context.Context) (*Overview, error) {
	o := &Overview{Version: s.version, EmailAvailable: s.emailAvailable}
	err := s.pool.QueryRow(ctx, `select
			(select count(*) from users), (select count(*) from users where status = 'active'),
			(select count(*) from users where status = 'suspended'), (select count(*) from users where role = 'admin'),
			(select count(*) from devices where revoked_at is null), (select count(*) from sync_records), (select count(*) from works)`).
		Scan(&o.Users.Total, &o.Users.Active, &o.Users.Suspended, &o.Users.Admins, &o.Devices, &o.Records, &o.Works)
	return o, err
}

// User is AdminUser in the API.
type User struct {
	ID            uuid.UUID `json:"id"`
	Username      string    `json:"username"`
	Email         *string   `json:"email"`
	EmailVerified bool      `json:"emailVerified"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	CreatedAt     int64     `json:"createdAt"`
	LastSeenAt    *int64    `json:"lastSeenAt"`
	DeviceCount   int       `json:"deviceCount"`
	RecordCount   int       `json:"recordCount"`
	RecordQuota   *int      `json:"recordQuota"`
}

// UserPage is a page of users.
type UserPage struct {
	Items  []User `json:"items"`
	Total  int    `json:"total"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

const userColumns = `u.id, u.username, u.email, u.email_verified, u.role, u.status,
	(extract(epoch from u.created_at) * 1000)::bigint,
	(select (extract(epoch from max(d.last_seen_at)) * 1000)::bigint from devices d where d.user_id = u.id),
	(select count(*)::int from devices d where d.user_id = u.id and d.revoked_at is null),
	(select count(*)::int from sync_records r where r.user_id = u.id), u.record_quota`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.EmailVerified, &u.Role, &u.Status, &u.CreatedAt, &u.LastSeenAt,
		&u.DeviceCount, &u.RecordCount, &u.RecordQuota)
	return u, err
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListUsers returns users newest first, optionally filtered by username or email.
func (s *Service) ListUsers(ctx context.Context, q string, offset, limit int) (*UserPage, error) {
	page := &UserPage{Items: []User{}, Offset: offset, Limit: limit}
	where, args := "", []any{}
	if q != "" {
		where = ` where u.username ilike $1 escape '\' or coalesce(u.email, '') ilike $1 escape '\'`
		args = append(args, "%"+escapeLike(q)+"%")
	}
	if err := s.pool.QueryRow(ctx, `select count(*) from users u`+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	n := len(args)
	rows, err := s.pool.Query(ctx, `select `+userColumns+` from users u`+where+
		` order by u.created_at desc, u.id limit $`+itoa(n+1)+` offset $`+itoa(n+2), append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, u)
	}
	return page, rows.Err()
}

func itoa(n int) string { return strconv.Itoa(n) }

// UserPatch is AdminUserPatch. RecordQuotaSet distinguishes "absent" from an explicit null.
type UserPatch struct {
	Role           *string
	Status         *string
	RecordQuotaSet bool
	RecordQuota    *int
	EmailVerified  *bool
}

// UnmarshalJSON records which keys were present.
func (p *UserPatch) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for k := range raw {
		switch k {
		case "role", "status", "recordQuota", "emailVerified":
		default:
			return errors.New("unknown field " + k)
		}
	}
	if v, ok := raw["role"]; ok {
		if err := json.Unmarshal(v, &p.Role); err != nil {
			return err
		}
	}
	if v, ok := raw["status"]; ok {
		if err := json.Unmarshal(v, &p.Status); err != nil {
			return err
		}
	}
	if v, ok := raw["recordQuota"]; ok {
		p.RecordQuotaSet = true
		if err := json.Unmarshal(v, &p.RecordQuota); err != nil {
			return err
		}
	}
	if v, ok := raw["emailVerified"]; ok {
		if err := json.Unmarshal(v, &p.EmailVerified); err != nil {
			return err
		}
	}
	return nil
}

// UpdateUser applies a patch. Removing the last active administrator (by demotion or suspension)
// is refused with last_admin; the check runs under the administrator row locks.
func (s *Service) UpdateUser(ctx context.Context, actor, target uuid.UUID, p UserPatch) (*User, error) {
	if p.Role == nil && p.Status == nil && !p.RecordQuotaSet && p.EmailVerified == nil {
		return nil, apierr.Validation("at least one field is required")
	}
	if p.Role != nil && *p.Role != "user" && *p.Role != "moderator" && *p.Role != "admin" {
		return nil, apierr.Validation("role must be user, moderator or admin")
	}
	if p.Status != nil && *p.Status != "active" && *p.Status != "suspended" {
		return nil, apierr.Validation("status must be active or suspended")
	}
	if p.RecordQuota != nil && *p.RecordQuota < 0 {
		return nil, apierr.Validation("recordQuota must be 0 or more")
	}
	var out User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		admins, err := auth.LockActiveAdmins(ctx, tx)
		if err != nil {
			return err
		}
		var (
			role, status string
			email        *string
		)
		err = tx.QueryRow(ctx, `select role, status, email from users where id = $1 for update`, target).Scan(&role, &status, &email)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.NotFound("user not found")
		}
		if err != nil {
			return err
		}
		newRole, newStatus := role, status
		changes := map[string]any{}
		if p.Role != nil {
			newRole = *p.Role
			changes["role"] = newRole
		}
		if p.Status != nil {
			newStatus = *p.Status
			changes["status"] = newStatus
		}
		if p.RecordQuotaSet {
			changes["recordQuota"] = p.RecordQuota
		}
		if p.EmailVerified != nil {
			if *p.EmailVerified && email == nil {
				return apierr.Validation("a user without an email address cannot be marked verified")
			}
			changes["emailVerified"] = *p.EmailVerified
		}
		wasAdmin := role == "admin" && status == "active"
		if wasAdmin && !(newRole == "admin" && newStatus == "active") && auth.OtherActiveAdmins(admins, target) == 0 {
			return auth.ErrLastAdmin()
		}
		if _, err := tx.Exec(ctx, `update users set role = $2, status = $3 where id = $1`, target, newRole, newStatus); err != nil {
			return err
		}
		if p.RecordQuotaSet {
			if _, err := tx.Exec(ctx, `update users set record_quota = $2 where id = $1`, target, p.RecordQuota); err != nil {
				return err
			}
		}
		if p.EmailVerified != nil {
			if _, err := tx.Exec(ctx, `update users set email_verified = $2 where id = $1`, target, *p.EmailVerified); err != nil {
				return err
			}
		}
		if newStatus == "suspended" && status != "suspended" {
			if err := auth.RevokeUserDevices(ctx, tx, target); err != nil {
				return err
			}
		}
		if err := audit(ctx, tx, actor, "user.update", "user", target.String(), map[string]any{"changes": changes}); err != nil {
			return err
		}
		out, err = scanUser(tx.QueryRow(ctx, `select `+userColumns+` from users u where u.id = $1`, target))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUser permanently deletes an account. The last active administrator is refused.
func (s *Service) DeleteUser(ctx context.Context, actor, target uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		admins, err := auth.LockActiveAdmins(ctx, tx)
		if err != nil {
			return err
		}
		var role, status, username string
		err = tx.QueryRow(ctx, `select role, status, username from users where id = $1 for update`, target).Scan(&role, &status, &username)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.NotFound("user not found")
		}
		if err != nil {
			return err
		}
		if role == "admin" && status == "active" && auth.OtherActiveAdmins(admins, target) == 0 {
			return auth.ErrLastAdmin()
		}
		// Audit first: when an administrator deletes themselves the actor reference is set to null
		// by the foreign key instead of the insert failing.
		if err := audit(ctx, tx, actor, "user.delete", "user", target.String(), map[string]any{"username": username}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `delete from users where id = $1`, target)
		return err
	})
}

// SignOutUser signs the user out on every device.
func (s *Service) SignOutUser(ctx context.Context, actor, target uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var username string
		err := tx.QueryRow(ctx, `select username from users where id = $1 for update`, target).Scan(&username)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.NotFound("user not found")
		}
		if err != nil {
			return err
		}
		if err := auth.RevokeUserDevices(ctx, tx, target); err != nil {
			return err
		}
		return audit(ctx, tx, actor, "user.sign_out", "user", target.String(), map[string]any{"username": username})
	})
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

// Invite is Invite in the API.
type Invite struct {
	ID        uuid.UUID `json:"id"`
	MaxUses   int       `json:"maxUses"`
	Uses      int       `json:"uses"`
	ExpiresAt *int64    `json:"expiresAt"`
	CreatedAt int64     `json:"createdAt"`
	CreatedBy *string   `json:"createdBy"`
	Note      *string   `json:"note"`
}

const inviteColumns = `i.id, i.max_uses, i.uses, (extract(epoch from i.expires_at) * 1000)::bigint,
	(extract(epoch from i.created_at) * 1000)::bigint, u.username, i.note`

func scanInvite(row pgx.Row) (Invite, error) {
	var i Invite
	err := row.Scan(&i.ID, &i.MaxUses, &i.Uses, &i.ExpiresAt, &i.CreatedAt, &i.CreatedBy, &i.Note)
	return i, err
}

// ListInvites returns every invite, newest first, including used-up and expired ones.
func (s *Service) ListInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `select `+inviteColumns+` from invites i left join users u on u.id = i.created_by order by i.created_at desc, i.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// InviteInput is the body of the create call.
type InviteInput struct {
	MaxUses        *int    `json:"maxUses"`
	ExpiresInHours *int    `json:"expiresInHours"`
	Note           *string `json:"note"`
}

// CreateInvite stores an invite and returns it with its plain code, which is never stored.
func (s *Service) CreateInvite(ctx context.Context, actor uuid.UUID, in InviteInput) (*Invite, string, error) {
	maxUses := 1
	if in.MaxUses != nil {
		maxUses = *in.MaxUses
	}
	if maxUses < 1 || maxUses > 1000 {
		return nil, "", apierr.Validation("maxUses must be 1-1000")
	}
	var expires any
	if in.ExpiresInHours != nil {
		if *in.ExpiresInHours < 1 || *in.ExpiresInHours > 8760 {
			return nil, "", apierr.Validation("expiresInHours must be 1-8760")
		}
		expires = time.Now().Add(time.Duration(*in.ExpiresInHours) * time.Hour)
	}
	var note *string
	if in.Note != nil {
		n := strings.TrimSpace(*in.Note)
		if utf8.RuneCountInString(n) > 200 {
			return nil, "", apierr.Validation("note must be at most 200 characters")
		}
		if n != "" {
			note = &n
		}
	}
	code := instance.GenerateCode()
	var inv Invite
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `insert into invites (code_hash, created_by, max_uses, expires_at, note)
			values ($1, $2, $3, $4, $5) returning id`, instance.HashCode(code), actor, maxUses, expires, note).Scan(&id); err != nil {
			return err
		}
		detail := map[string]any{"inviteId": id, "maxUses": maxUses, "expiresInHours": in.ExpiresInHours}
		if err := audit(ctx, tx, actor, "invite.create", "invite", id.String(), detail); err != nil {
			return err
		}
		var err error
		inv, err = scanInvite(tx.QueryRow(ctx, `select `+inviteColumns+` from invites i left join users u on u.id = i.created_by where i.id = $1`, id))
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return &inv, code, nil
}

// DeleteInvite revokes an invite. Accounts created with it are unaffected.
func (s *Service) DeleteInvite(ctx context.Context, actor, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from invites where id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apierr.NotFound("invite not found")
		}
		return audit(ctx, tx, actor, "invite.delete", "invite", id.String(), map[string]any{"inviteId": id})
	})
}

// AuditEntry is AuditEntry in the API.
type AuditEntry struct {
	ID         int64           `json:"id"`
	Actor      *AuditActor     `json:"actor"`
	Action     string          `json:"action"`
	TargetType *string         `json:"targetType"`
	TargetID   *string         `json:"targetId"`
	Detail     json.RawMessage `json:"detail"`
	CreatedAt  int64           `json:"createdAt"`
}

// AuditActor is the user behind an entry.
type AuditActor struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
}

// AuditPage is a page of the audit log.
type AuditPage struct {
	Items  []AuditEntry `json:"items"`
	Total  int          `json:"total"`
	Offset int          `json:"offset"`
	Limit  int          `json:"limit"`
}

// Audit lists the audit log, newest first.
func (s *Service) Audit(ctx context.Context, offset, limit int) (*AuditPage, error) {
	page := &AuditPage{Items: []AuditEntry{}, Offset: offset, Limit: limit}
	if err := s.pool.QueryRow(ctx, `select count(*) from audit_log`).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select a.id, a.actor_id, u.username, a.action, a.target_type, a.target_id, a.detail,
			(extract(epoch from a.created_at) * 1000)::bigint
		from audit_log a left join users u on u.id = a.actor_id order by a.id desc limit $1 offset $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			e        AuditEntry
			actorID  *uuid.UUID
			username *string
			detail   []byte
		)
		if err := rows.Scan(&e.ID, &actorID, &username, &e.Action, &e.TargetType, &e.TargetID, &detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		if actorID != nil && username != nil {
			e.Actor = &AuditActor{ID: *actorID, Username: *username}
		}
		e.Detail = json.RawMessage("null")
		if detail != nil {
			e.Detail = detail
		}
		page.Items = append(page.Items, e)
	}
	return page, rows.Err()
}
