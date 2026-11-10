package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/mail"
)

// Mail token purposes (the email_tokens.purpose values).
const (
	purposeVerify = "verify_email"
	purposeReset  = "reset_password"
)

// Token lifetimes and the per-account, per-purpose sending interval (docs/spec/web.md 2.2).
const (
	verifyTTL    = 48 * time.Hour
	resetTTL     = time.Hour
	mailCooldown = 60 * time.Second
)

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func hashMailToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// insertMailToken stores a new single-use token for (user, purpose) and returns the plain value.
func insertMailToken(ctx context.Context, q execer, userID uuid.UUID, purpose, email string, ttl time.Duration) (string, error) {
	var b [32]byte
	_, _ = rand.Read(b[:])
	token := base64.RawURLEncoding.EncodeToString(b[:])
	_, err := q.Exec(ctx, `insert into email_tokens (token_hash, user_id, purpose, email, expires_at)
		values ($1, $2, $3, $4, now() + make_interval(secs => $5))`,
		hashMailToken(token), userID, purpose, email, ttl.Seconds())
	return token, err
}

func errInvalidToken() *apierr.Error {
	return apierr.New(http.StatusBadRequest, "invalid_token", "the link is invalid, expired or already used")
}

func errEmailUnavailable() *apierr.Error {
	return apierr.New(http.StatusConflict, "email_unavailable", "this server cannot send email")
}

// cooldownLeft returns how long until another mail of this purpose may be sent to the user.
func cooldownLeft(ctx context.Context, tx pgx.Tx, userID uuid.UUID, purpose string) (time.Duration, error) {
	var left *float64
	err := tx.QueryRow(ctx, `select extract(epoch from (max(created_at) + make_interval(secs => $3)) - now())
		from email_tokens where user_id = $1 and purpose = $2`, userID, purpose, mailCooldown.Seconds()).Scan(&left)
	if err != nil || left == nil || *left <= 0 {
		return 0, err
	}
	return time.Duration(*left * float64(time.Second)), nil
}

func tooSoon(wait time.Duration) *apierr.Error {
	return &apierr.Error{Status: http.StatusTooManyRequests, Code: "rate_limited",
		Message: "a mail was sent a moment ago; try again later", RetryAfterSeconds: int(math.Ceil(wait.Seconds()))}
}

// lockTokenOwner locks the account a mail token belongs to and reports whether the token exists.
// Every mail flow takes the user row before any token row, so two of them (following a link while
// the address is being changed, two reset links at once) wait for each other instead of deadlocking.
func lockTokenOwner(ctx context.Context, tx pgx.Tx, token string) (bool, error) {
	var userID uuid.UUID
	err := tx.QueryRow(ctx, `select user_id from email_tokens where token_hash = $1`, hashMailToken(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx, `select id from users where id = $1 for update`, userID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// VerifyEmail confirms an address with the token from the verification mail.
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	if token == "" {
		return errInvalidToken()
	}
	var failure error
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			userID    uuid.UUID
			purpose   string
			tokEmail  string
			expired   bool
			used      bool
			userEmail *string
			verified  bool
		)
		if found, err := lockTokenOwner(ctx, tx, token); err != nil {
			return err
		} else if !found {
			failure = errInvalidToken()
			return nil
		}
		err := tx.QueryRow(ctx, `select t.user_id, t.purpose, t.email, t.expires_at <= now(), t.used_at is not null,
				u.email, u.email_verified
			from email_tokens t join users u on u.id = t.user_id
			where t.token_hash = $1 for update of t`, hashMailToken(token)).
			Scan(&userID, &purpose, &tokEmail, &expired, &used, &userEmail, &verified)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && purpose != purposeVerify) {
			failure = errInvalidToken()
			return nil
		}
		if err != nil {
			return err
		}
		matches := userEmail != nil && strings.EqualFold(*userEmail, tokEmail)
		if used {
			// Following the link twice is harmless when the address is verified already.
			if !(matches && verified) {
				failure = errInvalidToken()
			}
			return nil
		}
		if expired || !matches {
			failure = errInvalidToken()
			return nil
		}
		if _, err := tx.Exec(ctx, `update email_tokens set used_at = now() where token_hash = $1`, hashMailToken(token)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update users set email_verified = true where id = $1`, userID)
		return err
	})
	if err != nil {
		return err
	}
	return failure
}

// ResendVerification sends the verification mail again, at most once a minute.
func (s *Service) ResendVerification(ctx context.Context, p *Principal) error {
	var (
		token string
		email string
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			e        *string
			verified bool
		)
		if err := tx.QueryRow(ctx, `select email, email_verified from users where id = $1 for update`, p.UserID).Scan(&e, &verified); err != nil {
			return err
		}
		switch {
		case e == nil:
			return apierr.New(http.StatusUnprocessableEntity, "email_required", "this account has no email address")
		case verified:
			return apierr.New(http.StatusConflict, "conflict", "the email address is already verified")
		case !s.mailEnabled():
			return errEmailUnavailable()
		}
		wait, err := cooldownLeft(ctx, tx, p.UserID, purposeVerify)
		if err != nil {
			return err
		}
		if wait > 0 {
			return tooSoon(wait)
		}
		email = *e
		token, err = insertMailToken(ctx, tx, p.UserID, purposeVerify, email, verifyTTL)
		return err
	})
	if err != nil {
		return err
	}
	_, _, name := s.policy()
	s.mailer.Enqueue(mail.VerifyEmail(name, s.cfg.PublicURL, email, token))
	return nil
}

// ChangeEmail sets, replaces or removes (nil) the address. The new address starts unverified.
func (s *Service) ChangeEmail(ctx context.Context, p *Principal, email *string, password string) (*User, error) {
	next, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	var hash string
	if err := s.pool.QueryRow(ctx, `select password_hash from users where id = $1`, p.UserID).Scan(&hash); err != nil {
		return nil, err
	}
	if !s.hasher.Verify(password, hash) {
		return nil, errInvalidCredentials()
	}
	_, verification, name := s.policy()
	if next == nil && verification == config.EmailRequired {
		return nil, apierr.New(http.StatusUnprocessableEntity, "email_required", "an email address is required on this server")
	}
	var token string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var cur *string
		if err := tx.QueryRow(ctx, `select email from users where id = $1 for update`, p.UserID).Scan(&cur); err != nil {
			return err
		}
		if (cur == nil && next == nil) || (cur != nil && next != nil && strings.EqualFold(*cur, *next)) {
			return nil // nothing changes
		}
		sends := next != nil && verification != config.EmailDisabled && s.mailEnabled()
		if sends {
			// The one-mail-a-minute rule also covers this path, or it could be used to flood
			// arbitrary addresses with verification mail.
			wait, err := cooldownLeft(ctx, tx, p.UserID, purposeVerify)
			if err != nil {
				return err
			}
			if wait > 0 {
				return tooSoon(wait)
			}
		}
		if _, err := tx.Exec(ctx, `update users set email = $2, email_verified = false where id = $1`, p.UserID, next); err != nil {
			return err
		}
		// Links mailed to the old address stop working, the password reset ones included.
		if _, err := tx.Exec(ctx, `update email_tokens set used_at = now()
			where user_id = $1 and used_at is null`, p.UserID); err != nil {
			return err
		}
		if sends {
			token, err = insertMailToken(ctx, tx, p.UserID, purposeVerify, *next, verifyTTL)
		}
		return err
	})
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil, apierr.New(http.StatusConflict, "email_taken", "this email address is already registered")
		}
		return nil, err
	}
	if token != "" {
		s.mailer.Enqueue(mail.VerifyEmail(name, s.cfg.PublicURL, *next, token))
	}
	return s.Me(ctx, p.UserID)
}

// ForgotPassword mails a reset link when the address belongs to an account that may use it. The
// outcome is the same for every address: only a server without SMTP answers differently.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	if !s.mailEnabled() {
		return errEmailUnavailable()
	}
	addr, err := normalizeEmail(&email)
	if err != nil {
		return err
	}
	if addr == nil {
		return apierr.Validation("email address is not valid")
	}
	_, verification, name := s.policy()
	var token string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			id       uuid.UUID
			stored   string
			verified bool
			status   string
		)
		err := tx.QueryRow(ctx, `select id, email, email_verified, status from users where lower(email) = lower($1) for update`, *addr).
			Scan(&id, &stored, &verified, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != "active" || (!verified && verification != config.EmailDisabled) {
			return nil
		}
		wait, err := cooldownLeft(ctx, tx, id, purposeReset)
		if err != nil || wait > 0 {
			return err
		}
		token, err = insertMailToken(ctx, tx, id, purposeReset, stored, resetTTL)
		*addr = stored
		return err
	})
	if err != nil {
		return err
	}
	if token != "" {
		s.mailer.Enqueue(mail.ResetPassword(name, s.cfg.PublicURL, *addr, token))
	}
	return nil
}

// ResetPassword sets a new password with the token from the reset mail and signs out every device.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	if token == "" {
		return errInvalidToken()
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	var failure error
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			userID    uuid.UUID
			purpose   string
			tokEmail  string
			expired   bool
			used      bool
			userEmail *string
		)
		if found, err := lockTokenOwner(ctx, tx, token); err != nil {
			return err
		} else if !found {
			failure = errInvalidToken()
			return nil
		}
		err := tx.QueryRow(ctx, `select t.user_id, t.purpose, t.email, t.expires_at <= now(), t.used_at is not null, u.email
			from email_tokens t join users u on u.id = t.user_id
			where t.token_hash = $1 for update of t`, hashMailToken(token)).
			Scan(&userID, &purpose, &tokEmail, &expired, &used, &userEmail)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (purpose != purposeReset || used || expired)) {
			failure = errInvalidToken()
			return nil
		}
		if err != nil {
			return err
		}
		matches := userEmail != nil && strings.EqualFold(*userEmail, tokEmail)
		if _, err := tx.Exec(ctx, `update email_tokens set used_at = now()
			where user_id = $1 and purpose = $2 and used_at is null`, userID, purposeReset); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update users set password_hash = $2, password_changed_at = now(),
			email_verified = email_verified or $3 where id = $1`, userID, hash, matches); err != nil {
			return err
		}
		return RevokeUserDevices(ctx, tx, userID)
	})
	if err != nil {
		return err
	}
	return failure
}

// RevokeUserDevices signs the user out on every device and drops their refresh tokens.
func RevokeUserDevices(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `update devices set revoked_at = now() where user_id = $1 and revoked_at is null`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from refresh_tokens where device_id in (select id from devices where user_id = $1)`, userID)
	return err
}
