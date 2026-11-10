// Package jobs runs the periodic maintenance tasks.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options configure the retention windows. Zero durations are valid and mean "everything
// older than now", which tests rely on.
type Options struct {
	Interval           time.Duration
	TombstoneRetention time.Duration
	BatchRetention     time.Duration
	// EmailTokenRetention is how long an expired mail token is kept (7 days in production).
	EmailTokenRetention time.Duration
}

// Runner executes the jobs.
type Runner struct {
	pool *pgxpool.Pool
	opts Options
	log  *slog.Logger
}

// New creates a Runner.
func New(pool *pgxpool.Pool, opts Options, log *slog.Logger) *Runner {
	return &Runner{pool: pool, opts: opts, log: log}
}

// Run blocks until ctx is cancelled, running every job once per interval.
func (r *Runner) Run(ctx context.Context) {
	if r.opts.Interval <= 0 {
		return
	}
	t := time.NewTicker(r.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce executes every job and logs failures.
func (r *Runner) RunOnce(ctx context.Context) {
	if n, err := r.GCTombstones(ctx); err != nil {
		r.log.Error("tombstone gc failed", "err", err)
	} else if n > 0 {
		r.log.Info("tombstone gc", "removed", n)
	}
	if n, err := r.GCBatches(ctx); err != nil {
		r.log.Error("batch gc failed", "err", err)
	} else if n > 0 {
		r.log.Info("batch gc", "removed", n)
	}
	if n, err := r.GCRefreshTokens(ctx); err != nil {
		r.log.Error("refresh token gc failed", "err", err)
	} else if n > 0 {
		r.log.Info("refresh token gc", "removed", n)
	}
	if n, err := r.GCEmailTokens(ctx); err != nil {
		r.log.Error("email token gc failed", "err", err)
	} else if n > 0 {
		r.log.Info("email token gc", "removed", n)
	}
}

func seconds(d time.Duration) float64 { return d.Seconds() }

// GCTombstones permanently removes deleted records unchanged for the retention period and
// raises each affected user's sync_floor_seq to the largest removed seq. Each user is handled in
// its own transaction that first takes the user's row lock, the same order pushes use.
func (r *Runner) GCTombstones(ctx context.Context) (int64, error) {
	rows, err := r.pool.Query(ctx, `select distinct user_id from sync_records
		where is_deleted and updated_at < now() - make_interval(secs => $1)`, seconds(r.opts.TombstoneRetention))
	if err != nil {
		return 0, err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	var total int64
	for _, u := range users {
		var removed int64
		err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `select 1 from users where id = $1 for update`, u); err != nil {
				return err
			}
			var maxSeq *int64
			err := tx.QueryRow(ctx, `with gone as (
					delete from sync_records where user_id = $1 and is_deleted
						and updated_at < now() - make_interval(secs => $2)
					returning seq)
				select max(seq), count(*) from gone`, u, seconds(r.opts.TombstoneRetention)).Scan(&maxSeq, &removed)
			if err != nil {
				return err
			}
			if maxSeq != nil {
				_, err = tx.Exec(ctx, `update users set sync_floor_seq = greatest(sync_floor_seq, $2) where id = $1`, u, *maxSeq)
			}
			return err
		})
		if err != nil {
			return total, err
		}
		total += removed
	}
	return total, nil
}

// GCBatches removes idempotency records older than the retention period.
func (r *Runner) GCBatches(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `delete from sync_batches where created_at < now() - make_interval(secs => $1)`,
		seconds(r.opts.BatchRetention))
	return tag.RowsAffected(), err
}

// GCRefreshTokens removes expired refresh tokens.
func (r *Runner) GCRefreshTokens(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `delete from refresh_tokens where expires_at < now()`)
	return tag.RowsAffected(), err
}

// GCEmailTokens removes mail tokens that expired more than the retention period ago.
func (r *Runner) GCEmailTokens(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `delete from email_tokens where expires_at < now() - make_interval(secs => $1)`,
		seconds(r.opts.EmailTokenRetention))
	return tag.RowsAffected(), err
}
