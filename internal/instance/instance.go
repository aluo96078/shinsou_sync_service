// Package instance manages instance-level state kept in the database: the JWT signing key, the
// one-time administrator setup code, invites and role changes.
package instance

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	keySigningKey    = "jwt_signing_key"
	keySetupCodeHash = "setup_code_hash"
	keyCSRFKey       = "web_csrf_key"
)

// CSRFKey returns the 32-byte key behind the web CSRF tokens, generating and storing it on first
// use (first insert wins, like SigningKey).
func CSRFKey(ctx context.Context, pool *pgxpool.Pool) ([]byte, error) {
	for range 3 {
		var raw []byte
		err := pool.QueryRow(ctx, `select value from instance_settings where key = $1`, keyCSRFKey).Scan(&raw)
		if err == nil {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, fmt.Errorf("decode csrf key: %w", err)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil || len(b) != 32 {
				return nil, errors.New("stored csrf key is corrupt")
			}
			return b, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		val, _ := json.Marshal(base64.StdEncoding.EncodeToString(b))
		if _, err := pool.Exec(ctx, `insert into instance_settings (key, value) values ($1, $2) on conflict (key) do nothing`, keyCSRFKey, val); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("could not establish a csrf key")
}

// SigningKey returns the EdDSA key, generating and storing it on first use. Safe to call from
// several processes at once: the first insert wins and everyone reads the stored value.
func SigningKey(ctx context.Context, pool *pgxpool.Pool) (ed25519.PrivateKey, error) {
	for range 3 {
		var raw []byte
		err := pool.QueryRow(ctx, `select value from instance_settings where key = $1`, keySigningKey).Scan(&raw)
		if err == nil {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, fmt.Errorf("decode signing key: %w", err)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil || len(b) != ed25519.PrivateKeySize {
				return nil, errors.New("stored signing key is corrupt")
			}
			return ed25519.PrivateKey(b), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		val, _ := json.Marshal(base64.StdEncoding.EncodeToString(priv))
		if _, err := pool.Exec(ctx, `insert into instance_settings (key, value) values ($1, $2) on conflict (key) do nothing`, keySigningKey, val); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("could not establish a signing key")
}

// GenerateCode returns a human-typable random code such as ABCD-EFGH-IJKL-MNOP.
func GenerateCode() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// NormalizeCode makes user input comparable: upper case, without dashes and spaces.
func NormalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", " ", "").Replace(s)
}

// HashCode is the stored form of an invite or setup code.
func HashCode(code string) []byte {
	sum := sha256.Sum256([]byte(NormalizeCode(code)))
	return sum[:]
}

// HasAdmin reports whether any administrator exists.
func HasAdmin(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, `select exists (select 1 from users where role = 'admin')`).Scan(&ok)
	return ok, err
}

// EnsureSetupCode creates the one-time administrator setup code when the instance has no
// administrator and no code yet. The plain code is only logged, never stored.
func EnsureSetupCode(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	has, err := HasAdmin(ctx, pool)
	if err != nil || has {
		return err
	}
	var exists bool
	if err := pool.QueryRow(ctx, `select exists (select 1 from instance_settings where key = $1)`, keySetupCodeHash).Scan(&exists); err != nil || exists {
		return err
	}
	code := GenerateCode()
	tag, err := storeSetupCode(ctx, pool, code, false)
	if err != nil {
		return err
	}
	if tag {
		log.Warn("no administrator exists yet; register with this one-time setup code to create it (shown only once; use `shinsou-server admin reset-setup-code` to replace it)", "setup_code", code)
	}
	return nil
}

// ErrAdminExists is returned when a setup code is requested but an administrator already exists.
var ErrAdminExists = errors.New("an administrator already exists; use `admin set-role` instead")

// ResetSetupCode replaces the setup code and returns the new plain code.
func ResetSetupCode(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	has, err := HasAdmin(ctx, pool)
	if err != nil {
		return "", err
	}
	if has {
		return "", ErrAdminExists
	}
	code := GenerateCode()
	if _, err := storeSetupCode(ctx, pool, code, true); err != nil {
		return "", err
	}
	return code, nil
}

func storeSetupCode(ctx context.Context, pool *pgxpool.Pool, code string, replace bool) (bool, error) {
	val, _ := json.Marshal(hex.EncodeToString(HashCode(code)))
	q := `insert into instance_settings (key, value) values ($1, $2) on conflict (key) do nothing`
	if replace {
		q = `insert into instance_settings (key, value) values ($1, $2) on conflict (key) do update set value = excluded.value, updated_at = now()`
	}
	tag, err := pool.Exec(ctx, q, keySetupCodeHash, val)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ConsumeSetupCode atomically verifies and removes the setup code inside tx. It reports whether
// the code was valid; a valid code can be used exactly once.
func ConsumeSetupCode(ctx context.Context, tx pgx.Tx, code string) (bool, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `select value from instance_settings where key = $1 for update`, keySetupCodeHash).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var stored string
	if err := json.Unmarshal(raw, &stored); err != nil {
		return false, err
	}
	want, err := hex.DecodeString(stored)
	if err != nil || subtle.ConstantTimeCompare(want, HashCode(code)) != 1 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `delete from instance_settings where key = $1`, keySetupCodeHash); err != nil {
		return false, err
	}
	return true, nil
}

// CreateInvite stores a new invite and returns its plain code.
func CreateInvite(ctx context.Context, pool *pgxpool.Pool, maxUses int, ttl time.Duration) (string, error) {
	if maxUses < 1 {
		return "", errors.New("max uses must be at least 1")
	}
	code := GenerateCode()
	var expires any
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	if _, err := pool.Exec(ctx, `insert into invites (code_hash, max_uses, expires_at) values ($1, $2, $3)`, HashCode(code), maxUses, expires); err != nil {
		return "", err
	}
	_, _ = pool.Exec(ctx, `insert into audit_log (action, target_type, detail) values ('invite.create', 'invite', $1)`,
		fmt.Sprintf(`{"maxUses":%d}`, maxUses))
	return code, nil
}

// ConsumeInvite uses one invite slot inside tx and reports whether the code was usable.
func ConsumeInvite(ctx context.Context, tx pgx.Tx, code string) (bool, error) {
	tag, err := tx.Exec(ctx, `update invites set uses = uses + 1
		where code_hash = $1 and uses < max_uses and (expires_at is null or expires_at > now())`, HashCode(code))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ErrUserNotFound is returned by SetRole for an unknown username.
var ErrUserNotFound = errors.New("user not found")

// SetRole changes a user's role.
func SetRole(ctx context.Context, pool *pgxpool.Pool, username, role string) error {
	switch role {
	case "user", "moderator", "admin":
	default:
		return fmt.Errorf("role must be user, moderator or admin, got %q", role)
	}
	var id string
	err := pool.QueryRow(ctx, `update users set role = $2 where lower(username) = lower($1) returning id::text`, username, role).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUserNotFound
	}
	if err != nil {
		return err
	}
	_, _ = pool.Exec(ctx, `insert into audit_log (action, target_type, target_id, detail) values ('user.set_role', 'user', $1, $2)`,
		id, fmt.Sprintf(`{"role":%q}`, role))
	if role == "admin" {
		_, _ = pool.Exec(ctx, `delete from instance_settings where key = $1`, keySetupCodeHash)
	}
	return nil
}
