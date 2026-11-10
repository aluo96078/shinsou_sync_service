package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
)

// ErrLastAdmin is returned when a change would leave the instance without an active administrator.
func ErrLastAdmin() *apierr.Error {
	return apierr.New(http.StatusConflict, "last_admin", "this would leave the instance without an active administrator")
}

// adminLockClass is the namespace of the transaction-level advisory lock that serialises every
// change able to remove an administrator.
const adminLockClass = 0x53484e53 // "SHNS"

// LockActiveAdmins locks every active administrator row, in id order, and returns the ids. Any
// change that could remove an administrator must call it first (before locking the target) and
// decide inside the same transaction, which makes concurrent demotions line up one behind the other.
//
// A transaction-level advisory lock is taken before the row locks. The rows alone are not enough:
// row locks are acquired against per-statement snapshots, and concurrent requests whose snapshots
// disagree about who is still an administrator can lock overlapping sets in different orders and
// deadlock. Serialising on the advisory lock first removes that, and the row locks still hold off
// everything else that writes to those users.
func LockActiveAdmins(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, 1)`, int32(adminLockClass)); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select id from users where role = 'admin' and status = 'active' order by id for update`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// OtherActiveAdmins counts the locked administrators other than target.
func OtherActiveAdmins(locked []uuid.UUID, target uuid.UUID) int {
	n := 0
	for _, id := range locked {
		if id != target {
			n++
		}
	}
	return n
}

// VerifyPassword checks the current password of a user.
func (s *Service) VerifyPassword(ctx context.Context, userID uuid.UUID, password string) error {
	var hash string
	if err := s.pool.QueryRow(ctx, `select password_hash from users where id = $1`, userID).Scan(&hash); err != nil {
		return err
	}
	if !s.hasher.Verify(password, hash) {
		return errInvalidCredentials()
	}
	return nil
}

// DeleteAccount permanently deletes the caller's account after checking the password. Devices,
// tokens and synced records go with it through the foreign keys.
func (s *Service) DeleteAccount(ctx context.Context, p *Principal, password string) error {
	if err := s.VerifyPassword(ctx, p.UserID, password); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		admins, err := LockActiveAdmins(ctx, tx)
		if err != nil {
			return err
		}
		var (
			username string
			role     string
			status   string
		)
		err = tx.QueryRow(ctx, `select username, role, status from users where id = $1 for update`, p.UserID).Scan(&username, &role, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.Unauthorized("this account no longer exists")
		}
		if err != nil {
			return err
		}
		if role == "admin" && status == "active" && OtherActiveAdmins(admins, p.UserID) == 0 {
			return ErrLastAdmin()
		}
		if _, err := tx.Exec(ctx, `delete from users where id = $1`, p.UserID); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]string{"username": username})
		_, err = tx.Exec(ctx, `insert into audit_log (action, target_type, target_id, detail) values ('account.delete', 'user', $1, $2)`,
			p.UserID.String(), detail)
		return err
	})
}

// LogoutRefreshToken signs out the device that owns the refresh token. An unknown token is not
// an error: the caller clears its cookies either way.
func (s *Service) LogoutRefreshToken(ctx context.Context, token string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var deviceID uuid.UUID
		err := tx.QueryRow(ctx, `select device_id from refresh_tokens where token_hash = $1`, HashRefreshToken(token)).Scan(&deviceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return revokeDevice(ctx, tx, deviceID)
	})
}
