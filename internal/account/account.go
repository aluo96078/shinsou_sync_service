// Package account reports on and exports what the server stores for one account.
package account

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/lww"
	"github.com/aluo96078/shinsou_sync_service/internal/social"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

// Kinds are the record kinds, in the order the usage report lists them.
var Kinds = []string{"library", "unit", "progress", "history", "category", "repository", "setting"}

// Usage is the usage report (Usage in the API).
type Usage struct {
	Total  int            `json:"total"`
	Quota  *int           `json:"quota"`
	ByKind map[string]int `json:"byKind"`
}

// Service implements the account reports.
type Service struct {
	pool         *pgxpool.Pool
	auth         *auth.Service
	defaultQuota int
}

// New creates the service. defaultQuota is the instance-wide record limit (0 = unlimited).
func New(pool *pgxpool.Pool, a *auth.Service, defaultQuota int) *Service {
	return &Service{pool: pool, auth: a, defaultQuota: defaultQuota}
}

// Usage counts the account's records.
func (s *Service) Usage(ctx context.Context, userID uuid.UUID) (*Usage, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	u := &Usage{ByKind: map[string]int{}}
	for _, k := range Kinds {
		u.ByKind[k] = 0
	}
	var personal *int
	if err := tx.QueryRow(ctx, `select record_quota from users where id = $1`, userID).Scan(&personal); err != nil {
		return nil, err
	}
	limit := s.defaultQuota
	if personal != nil {
		limit = *personal
	}
	if limit > 0 {
		u.Quota = &limit
	}
	rows, err := tx.Query(ctx, `select kind, count(*)::int, (count(*) filter (where not is_deleted))::int
		from sync_records where user_id = $1 group by kind`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind        string
			all, living int
		)
		if err := rows.Scan(&kind, &all, &living); err != nil {
			return nil, err
		}
		u.Total += all
		u.ByKind[kind] = living
	}
	return u, rows.Err()
}

// ExportMeta is the instance information written into an export.
type ExportMeta struct {
	Name string
	URL  string
}

type exportRecord struct {
	syncsvc.SyncRecord
	UpdatedAt int64 `json:"updatedAt"`
}

// Export streams the whole account as one JSON document. begin is called once, after the small
// parts have been read and before the first byte is written; it returns the destination (the
// handler sets its headers there). Records and works are read in one read-only repeatable-read
// transaction and written as they arrive, so memory use does not grow with the account.
func (s *Service) Export(ctx context.Context, p *auth.Principal, meta ExportMeta, begin func(username string) io.Writer) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	user, err := s.auth.Me(ctx, p.UserID)
	if err != nil {
		return err
	}
	devices, err := s.auth.ListDevices(ctx, p)
	if err != nil {
		return err
	}
	socialData, err := social.ExportData(ctx, tx, p.UserID)
	if err != nil {
		return err
	}
	// Open the first result set before the response starts, so a database error is still an
	// ordinary error response.
	works, err := tx.Query(ctx, `select w.hash, w.key_version, w.package_id, w.source_id, w.remote_id, w.title, w.author, w.cover_url,
			w.content_kind, w.language, w.source_name, w.repository_url
		from works w where w.id in (select work_id from sync_records where user_id = $1 and work_id is not null) order by w.id`, p.UserID)
	if err != nil {
		return err
	}
	defer works.Close()

	out := bufio.NewWriterSize(begin(user.Username), 32<<10)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	write := func(v any) error { return enc.Encode(v) }
	raw := func(str string) { _, _ = out.WriteString(str) }

	var instMeta = struct {
		Name string  `json:"name"`
		URL  *string `json:"url"`
	}{Name: meta.Name}
	if meta.URL != "" {
		instMeta.URL = &meta.URL
	}
	raw(`{"exportedAt":`)
	if err := write(time.Now().UnixMilli()); err != nil {
		return err
	}
	raw(`,"instance":`)
	if err := write(instMeta); err != nil {
		return err
	}
	raw(`,"user":`)
	if err := write(user); err != nil {
		return err
	}
	raw(`,"devices":`)
	if err := write(devices); err != nil {
		return err
	}
	raw(`,"social":`)
	if err := write(socialData); err != nil {
		return err
	}
	raw(`,"works":[`)
	first := true
	for works.Next() {
		var (
			w                                      syncsvc.Work
			kv                                     int16
			author, cover, lang, srcName, repoName *string
		)
		if err := works.Scan(&w.Hash, &kv, &w.PackageID, &w.SourceID, &w.RemoteID, &w.Title, &author, &cover,
			&w.ContentKind, &lang, &srcName, &repoName); err != nil {
			return err
		}
		w.KeyVersion, w.Author, w.CoverURL, w.Language, w.SourceName, w.RepositoryURL = int(kv), author, cover, lang, srcName, repoName
		if !first {
			raw(",")
		}
		first = false
		if err := write(w); err != nil {
			return err
		}
	}
	if err := works.Err(); err != nil {
		return err
	}
	works.Close()

	raw(`],"records":[`)
	rows, err := tx.Query(ctx, `select r.kind, r.key, r.seq, r.unit_remote_id, r.fields, r.clocks, r.deleted, r.cleared,
			(extract(epoch from r.updated_at) * 1000)::bigint,
			w.hash, w.key_version, w.package_id, w.source_id, w.remote_id, w.title, w.author, w.cover_url,
			w.content_kind, w.language, w.source_name, w.repository_url
		from sync_records r left join works w on w.id = r.work_id where r.user_id = $1 order by r.seq`, p.UserID)
	if err != nil {
		return err
	}
	defer rows.Close()
	first = true
	for rows.Next() {
		var (
			rec                     exportRecord
			unit, cleared           *string
			fields, clocks, deleted []byte
			hash, pkg, src, rid     *string
			ttl, ck                 *string
			kv                      *int16
			author, cover, lang     *string
			srcName, repo           *string
		)
		if err := rows.Scan(&rec.Kind, &rec.Key, &rec.Seq, &unit, &fields, &clocks, &deleted, &cleared, &rec.UpdatedAt,
			&hash, &kv, &pkg, &src, &rid, &ttl, &author, &cover, &ck, &lang, &srcName, &repo); err != nil {
			return err
		}
		rec.Fields, rec.Clocks = map[string]json.RawMessage{}, map[string]string{}
		if err := json.Unmarshal(fields, &rec.Fields); err != nil {
			return err
		}
		if err := json.Unmarshal(clocks, &rec.Clocks); err != nil {
			return err
		}
		if deleted != nil {
			var d lww.Register
			if err := json.Unmarshal(deleted, &d); err != nil {
				return err
			}
			rec.Deleted = &d
		}
		if cleared != nil {
			rec.Cleared = *cleared
		}
		if unit != nil {
			rec.UnitRemoteID = *unit
		}
		if hash != nil {
			rec.Work = &syncsvc.Work{KeyVersion: int(*kv), PackageID: *pkg, SourceID: *src, RemoteID: *rid, Hash: *hash,
				Title: *ttl, Author: author, CoverURL: cover, ContentKind: *ck, Language: lang, SourceName: srcName, RepositoryURL: repo}
		}
		if !first {
			raw(",")
		}
		first = false
		if err := write(rec); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	raw(`]}`)
	raw("\n")
	return out.Flush()
}
