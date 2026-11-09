// Package auth implements accounts, sessions and devices.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
)

var usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

var platforms = map[string]bool{"android": true, "ios": true, "desktop": true, "web": true}

// Wire types (api/openapi.yaml).
type (
	DeviceInfo struct {
		Name       string `json:"name"`
		Platform   string `json:"platform"`
		AppVersion string `json:"appVersion"`
	}
	User struct {
		ID            uuid.UUID `json:"id"`
		Username      string    `json:"username"`
		Handle        string    `json:"handle,omitempty"`
		Email         *string   `json:"email"`
		EmailVerified bool      `json:"emailVerified"`
		Role          string    `json:"role"`
		CreatedAt     int64     `json:"createdAt"`
	}
	Device struct {
		ID           uuid.UUID `json:"id"`
		SyncDeviceID string    `json:"syncDeviceId"`
		Name         string    `json:"name"`
		Platform     string    `json:"platform"`
		AppVersion   *string   `json:"appVersion"`
		CreatedAt    int64     `json:"createdAt"`
		LastSeenAt   int64     `json:"lastSeenAt"`
		Current      bool      `json:"current"`
	}
	Tokens struct {
		AccessToken          string `json:"accessToken"`
		AccessTokenExpiresAt int64  `json:"accessTokenExpiresAt"`
		RefreshToken         string `json:"refreshToken"`
	}
	Session struct {
		Tokens
		User   User   `json:"user"`
		Device Device `json:"device"`
	}
	RegisterInput struct {
		Username   string      `json:"username"`
		Password   string      `json:"password"`
		Email      *string     `json:"email"`
		InviteCode *string     `json:"inviteCode"`
		SetupCode  *string     `json:"setupCode"`
		Device     *DeviceInfo `json:"device"`
	}
	LoginInput struct {
		Username string      `json:"username"`
		Password string      `json:"password"`
		Device   *DeviceInfo `json:"device"`
	}
)

// Principal is an authenticated caller.
type Principal struct {
	UserID       uuid.UUID
	DeviceID     uuid.UUID
	SyncDeviceID string
	Role         string
	Expires      time.Time
}

// Service is the account service.
type Service struct {
	pool   *pgxpool.Pool
	cfg    config.Config
	hasher *Hasher
	tokens *Issuer
	now    func() time.Time
	host   string
}

// NewService creates the service.
func NewService(pool *pgxpool.Pool, cfg config.Config, hasher *Hasher, tokens *Issuer) *Service {
	s := &Service{pool: pool, cfg: cfg, hasher: hasher, tokens: tokens, now: time.Now}
	if u, err := url.Parse(cfg.PublicURL); err == nil {
		s.host = u.Host
	}
	return s
}

// Register creates an account and signs this device in.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*Session, error) {
	username := strings.TrimSpace(in.Username)
	if !usernameRe.MatchString(username) {
		return nil, apierr.Validation("username must be 3-32 characters of a-z, 0-9 and _")
	}
	if err := validatePassword(in.Password); err != nil {
		return nil, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	dev, err := validateDevice(in.Device)
	if err != nil {
		return nil, err
	}
	setup := deref(in.SetupCode) != ""
	invite := deref(in.InviteCode)
	if !setup {
		switch s.cfg.Registration {
		case config.RegistrationClosed:
			return nil, apierr.New(http.StatusForbidden, "registration_closed", "registration is closed on this server")
		case config.RegistrationInvite:
			if invite == "" {
				return nil, apierr.New(http.StatusForbidden, "invite_required", "an invite code is required to register")
			}
		}
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, err
	}

	var session *Session
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		role := "user"
		switch {
		case setup:
			ok, err := instance.ConsumeSetupCode(ctx, tx, *in.SetupCode)
			if err != nil {
				return err
			}
			if !ok {
				return apierr.Forbidden("invalid setup code")
			}
			role = "admin"
		case s.cfg.Registration == config.RegistrationInvite:
			ok, err := instance.ConsumeInvite(ctx, tx, invite)
			if err != nil {
				return err
			}
			if !ok {
				return apierr.New(http.StatusForbidden, "invite_required", "the invite code is invalid, expired or used up")
			}
		}

		user := User{ID: uuid.New(), Username: username, Email: email, Role: role}
		var created time.Time
		err := tx.QueryRow(ctx, `insert into users (id, username, email, password_hash, role)
			values ($1, $2, $3, $4, $5) returning created_at`, user.ID, username, email, hash, role).Scan(&created)
		if err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23505" {
				if strings.Contains(pg.ConstraintName, "email") {
					return apierr.New(http.StatusConflict, "email_taken", "this email address is already registered")
				}
				return apierr.New(http.StatusConflict, "username_taken", "this username is taken")
			}
			return err
		}
		user.CreatedAt = created.UnixMilli()
		user.Handle = s.handle(username)
		if role == "admin" {
			_, err = tx.Exec(ctx, `insert into audit_log (actor_id, action, target_type, target_id) values ($1, 'user.admin_bootstrap', 'user', $2)`, user.ID, user.ID.String())
			if err != nil {
				return err
			}
		}
		session, err = s.newSession(ctx, tx, user, dev)
		return err
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// Login verifies the password and creates a new device session.
func (s *Service) Login(ctx context.Context, in LoginInput) (*Session, error) {
	dev, err := validateDevice(in.Device)
	if err != nil {
		return nil, err
	}
	var (
		user    User
		hash    string
		status  string
		created time.Time
	)
	err = s.pool.QueryRow(ctx, `select id, username, email, email_verified, role, status, password_hash, created_at
		from users where lower(username) = lower($1)`, strings.TrimSpace(in.Username)).
		Scan(&user.ID, &user.Username, &user.Email, &user.EmailVerified, &user.Role, &status, &hash, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		s.hasher.DummyVerify(in.Password)
		return nil, errInvalidCredentials()
	}
	if err != nil {
		return nil, err
	}
	if !s.hasher.Verify(in.Password, hash) {
		return nil, errInvalidCredentials()
	}
	if status != "active" {
		return nil, apierr.Forbidden("this account is suspended")
	}
	user.CreatedAt = created.UnixMilli()
	user.Handle = s.handle(user.Username)
	var session *Session
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		session, err = s.newSession(ctx, tx, user, dev)
		return err
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func errInvalidCredentials() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, "invalid_credentials", "wrong username or password")
}

func (s *Service) newSession(ctx context.Context, tx pgx.Tx, user User, info DeviceInfo) (*Session, error) {
	dev, err := s.createDevice(ctx, tx, user.ID, info)
	if err != nil {
		return nil, err
	}
	tokens, err := s.issueTokens(ctx, tx, user.ID, dev.ID)
	if err != nil {
		return nil, err
	}
	return &Session{Tokens: tokens, User: user, Device: dev}, nil
}

func (s *Service) createDevice(ctx context.Context, tx pgx.Tx, userID uuid.UUID, info DeviceInfo) (Device, error) {
	for range 5 {
		var rnd [6]byte
		_, _ = rand.Read(rnd[:])
		d := Device{ID: uuid.New(), SyncDeviceID: info.Platform + "-" + hex.EncodeToString(rnd[:]), Name: info.Name,
			Platform: info.Platform, Current: true}
		if info.AppVersion != "" {
			v := info.AppVersion
			d.AppVersion = &v
		}
		var created time.Time
		err := tx.QueryRow(ctx, `insert into devices (id, user_id, sync_device_id, name, platform, app_version)
			values ($1, $2, $3, $4, $5, $6)
			on conflict (user_id, sync_device_id) do nothing returning created_at`,
			d.ID, userID, d.SyncDeviceID, d.Name, d.Platform, d.AppVersion).Scan(&created)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return Device{}, err
		}
		d.CreatedAt = created.UnixMilli()
		d.LastSeenAt = d.CreatedAt
		return d, nil
	}
	return Device{}, errors.New("could not allocate a sync device id")
}

func (s *Service) issueTokens(ctx context.Context, tx pgx.Tx, userID, deviceID uuid.UUID) (Tokens, error) {
	now := s.now()
	access, exp, err := s.tokens.Sign(userID, deviceID, now)
	if err != nil {
		return Tokens{}, err
	}
	refresh, hash := NewRefreshToken()
	if _, err := tx.Exec(ctx, `insert into refresh_tokens (token_hash, device_id, expires_at) values ($1, $2, $3)`,
		hash, deviceID, now.Add(s.cfg.RefreshTokenTTL)); err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, AccessTokenExpiresAt: exp.UnixMilli(), RefreshToken: refresh}, nil
}

// Refresh rotates a refresh token. Presenting an already rotated token revokes the device.
func (s *Service) Refresh(ctx context.Context, token string) (*Tokens, error) {
	hash := HashRefreshToken(token)
	var (
		result  *Tokens
		failure error
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			deviceID, userID uuid.UUID
			expires          time.Time
			rotated          *time.Time
			revoked          *time.Time
			status           string
		)
		err := tx.QueryRow(ctx, `select rt.device_id, d.user_id, rt.expires_at, rt.rotated_at, d.revoked_at, u.status
			from refresh_tokens rt join devices d on d.id = rt.device_id join users u on u.id = d.user_id
			where rt.token_hash = $1 for update of rt`, hash).Scan(&deviceID, &userID, &expires, &rotated, &revoked, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			failure = apierr.Unauthorized("invalid refresh token")
			return nil
		}
		if err != nil {
			return err
		}
		if revoked != nil || status != "active" {
			failure = apierr.Unauthorized("this device has been signed out")
			return nil
		}
		if rotated != nil {
			if err := revokeDevice(ctx, tx, deviceID); err != nil {
				return err
			}
			failure = apierr.New(http.StatusUnauthorized, "refresh_token_reused", "refresh token was already used; the device has been signed out")
			return nil // commit the revocation
		}
		if !expires.After(s.now()) {
			failure = apierr.Unauthorized("refresh token expired")
			return nil
		}
		if _, err := tx.Exec(ctx, `update refresh_tokens set rotated_at = now() where token_hash = $1`, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update devices set last_seen_at = now() where id = $1`, deviceID); err != nil {
			return err
		}
		t, err := s.issueTokens(ctx, tx, userID, deviceID)
		if err != nil {
			return err
		}
		result = &t
		return nil
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	return result, nil
}

func revokeDevice(ctx context.Context, tx pgx.Tx, deviceID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `update devices set revoked_at = now() where id = $1 and revoked_at is null`, deviceID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from refresh_tokens where device_id = $1 and rotated_at is null`, deviceID)
	return err
}

// Authenticate validates an access token and checks that its device is still signed in.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	userID, deviceID, exp, err := s.tokens.Parse(token, s.now())
	if err != nil {
		return nil, apierr.Unauthorized("invalid or expired access token")
	}
	p := &Principal{UserID: userID, DeviceID: deviceID, Expires: exp}
	var (
		status   string
		revoked  *time.Time
		lastSeen time.Time
	)
	err = s.pool.QueryRow(ctx, `select u.role, u.status, d.sync_device_id, d.revoked_at, d.last_seen_at
		from devices d join users u on u.id = d.user_id where d.id = $1 and d.user_id = $2`, deviceID, userID).
		Scan(&p.Role, &status, &p.SyncDeviceID, &revoked, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (revoked != nil || status != "active")) {
		return nil, apierr.Unauthorized("this device has been signed out")
	}
	if err != nil {
		return nil, err
	}
	if s.now().Sub(lastSeen) > time.Minute {
		_, _ = s.pool.Exec(ctx, `update devices set last_seen_at = now() where id = $1`, deviceID)
	}
	return p, nil
}

// Logout signs out the calling device.
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return revokeDevice(ctx, tx, p.DeviceID) })
}

// Me returns the caller's account.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*User, error) {
	var (
		u       User
		created time.Time
	)
	err := s.pool.QueryRow(ctx, `select id, username, email, email_verified, role, created_at from users where id = $1`, userID).
		Scan(&u.ID, &u.Username, &u.Email, &u.EmailVerified, &u.Role, &created)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = created.UnixMilli()
	u.Handle = s.handle(u.Username)
	return &u, nil
}

// ChangePassword updates the password and signs out every other device.
func (s *Service) ChangePassword(ctx context.Context, p *Principal, current, next string) error {
	if err := validatePassword(next); err != nil {
		return err
	}
	var hash string
	if err := s.pool.QueryRow(ctx, `select password_hash from users where id = $1`, p.UserID).Scan(&hash); err != nil {
		return err
	}
	if !s.hasher.Verify(current, hash) {
		return errInvalidCredentials()
	}
	newHash, err := s.hasher.Hash(next)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update users set password_hash = $2, password_changed_at = now() where id = $1`, p.UserID, newHash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update devices set revoked_at = now() where user_id = $1 and id <> $2 and revoked_at is null`, p.UserID, p.DeviceID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `delete from refresh_tokens where device_id in (select id from devices where user_id = $1 and id <> $2)`, p.UserID, p.DeviceID)
		return err
	})
}

// ListDevices returns the caller's signed-in devices.
func (s *Service) ListDevices(ctx context.Context, p *Principal) ([]Device, error) {
	rows, err := s.pool.Query(ctx, `select id, sync_device_id, name, platform, app_version, created_at, last_seen_at
		from devices where user_id = $1 and revoked_at is null order by created_at`, p.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows, p.DeviceID)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanDevice(row pgx.Row, current uuid.UUID) (Device, error) {
	var (
		d                 Device
		created, lastSeen time.Time
	)
	if err := row.Scan(&d.ID, &d.SyncDeviceID, &d.Name, &d.Platform, &d.AppVersion, &created, &lastSeen); err != nil {
		return Device{}, err
	}
	d.CreatedAt, d.LastSeenAt, d.Current = created.UnixMilli(), lastSeen.UnixMilli(), d.ID == current
	return d, nil
}

// RenameDevice renames one of the caller's devices.
func (s *Service) RenameDevice(ctx context.Context, p *Principal, id uuid.UUID, name string) (*Device, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return nil, apierr.Validation("device name must be 1-64 characters")
	}
	row := s.pool.QueryRow(ctx, `update devices set name = $3 where id = $1 and user_id = $2 and revoked_at is null
		returning id, sync_device_id, name, platform, app_version, created_at, last_seen_at`, id, p.UserID, name)
	d, err := scanDevice(row, p.DeviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.NotFound("device not found")
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// SignOutDevice signs out one of the caller's devices (possibly the current one).
func (s *Service) SignOutDevice(ctx context.Context, p *Principal, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var found uuid.UUID
		err := tx.QueryRow(ctx, `select id from devices where id = $1 and user_id = $2 and revoked_at is null for update`, id, p.UserID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.NotFound("device not found")
		}
		if err != nil {
			return err
		}
		return revokeDevice(ctx, tx, id)
	})
}

func (s *Service) handle(username string) string {
	if s.host == "" {
		return ""
	}
	return "@" + username + "@" + s.host
}

func validatePassword(p string) error {
	if n := utf8.RuneCountInString(p); n < 8 || n > 256 {
		return apierr.Validation("password must be 8-256 characters")
	}
	return nil
}

func normalizeEmail(e *string) (*string, error) {
	if e == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*e)
	if v == "" {
		return nil, nil
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v || len(v) > 254 {
		return nil, apierr.Validation("email address is not valid")
	}
	return &v, nil
}

func validateDevice(d *DeviceInfo) (DeviceInfo, error) {
	if d == nil {
		return DeviceInfo{}, apierr.Validation("device is required")
	}
	out := DeviceInfo{Name: strings.TrimSpace(d.Name), Platform: d.Platform, AppVersion: strings.TrimSpace(d.AppVersion)}
	if n := utf8.RuneCountInString(out.Name); n < 1 || n > 64 {
		return DeviceInfo{}, apierr.Validation("device name must be 1-64 characters")
	}
	if !platforms[out.Platform] {
		return DeviceInfo{}, apierr.Validation("device platform must be android, ios, desktop or web")
	}
	if utf8.RuneCountInString(out.AppVersion) > 64 {
		return DeviceInfo{}, apierr.Validation("appVersion is too long")
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DeviceActive reports whether a device is still signed in and its account is active. Streams
// call it periodically so a revoked device is disconnected.
func (s *Service) DeviceActive(ctx context.Context, userID, deviceID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `select exists (select 1 from devices d join users u on u.id = d.user_id
		where d.id = $1 and d.user_id = $2 and d.revoked_at is null and u.status = 'active')`, deviceID, userID).Scan(&ok)
	return ok, err
}
