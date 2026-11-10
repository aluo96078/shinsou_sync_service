// Package library serves the read-only library view built from synced records
// (docs/spec/records.md, docs/spec/web.md section 5).
//
// Record fields are untyped user JSON, so every field is read through a guard that checks
// jsonb_typeof first; a field of the wrong type behaves as if it were missing and never raises.
package library

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

// MaxUnits caps the chapter list of one entry.
const MaxUnits = 5000

// Item is one library entry (LibraryItem in the API).
type Item struct {
	Key                 string   `json:"key"`
	Title               string   `json:"title"`
	Author              *string  `json:"author"`
	ContentKind         *string  `json:"contentKind"`
	CoverURL            *string  `json:"coverUrl"`
	SourceName          *string  `json:"sourceName"`
	Language            *string  `json:"language"`
	Local               bool     `json:"local"`
	AddedAt             *int64   `json:"addedAt"`
	CategoryIDs         []string `json:"categoryIds"`
	Pinned              bool     `json:"pinned"`
	Archived            bool     `json:"archived"`
	TrackedUnits        int      `json:"trackedUnits"`
	ReadUnits           int      `json:"readUnits"`
	Progression         *float64 `json:"progression"`
	CurrentUnitRemoteID *string  `json:"currentUnitRemoteId"`
	LastReadAt          *int64   `json:"lastReadAt"`
	UpdatedAt           int64    `json:"updatedAt"`
}

// Page is a page of items.
type Page struct {
	Items  []Item `json:"items"`
	Total  int    `json:"total"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// Unit is the state of one chapter.
type Unit struct {
	UnitRemoteID string `json:"unitRemoteId"`
	Read         bool   `json:"read"`
	Bookmarked   bool   `json:"bookmarked"`
	LastPageIdx  *int64 `json:"lastPageIndex"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// Detail is one entry with its chapters.
type Detail struct {
	Item
	Work  *syncsvc.Work `json:"work"`
	Units []Unit        `json:"units"`
}

// Category is a library category.
type Category struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Order int64  `json:"order"`
}

// Query selects entries.
type Query struct {
	Q        string
	Category string
	Archived string // exclude (default), include, only
	Sort     string // recent (default), title, added
	Offset   int
	Limit    int
}

// Service reads the library.
type Service struct{ pool *pgxpool.Pool }

// New creates the service.
func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Guarded readers over a jsonb column. col and name are constants from this file, never user input.

// jText is the string value of fields.name, or null when it is missing or not a string.
func jText(col, name string) string {
	return fmt.Sprintf(`(case when jsonb_typeof(%[1]s->'%[2]s') = 'string' then %[1]s->>'%[2]s' end)`, col, name)
}

// jBool is true only for the JSON value true.
func jBool(col, name string) string {
	return fmt.Sprintf(`coalesce(%[1]s->'%[2]s' = 'true'::jsonb, false)`, col, name)
}

// jInt is the integer value of a numeric field. Out-of-range numbers read as missing and
// fractions are floored.
func jInt(col, name string) string {
	return fmt.Sprintf(`(case when jsonb_typeof(%[1]s->'%[2]s') = 'number' then
		(case when (%[1]s->>'%[2]s')::numeric between -9007199254740991 and 9007199254740991
			then floor((%[1]s->>'%[2]s')::numeric)::bigint end) end)`, col, name)
}

// jFrac is a numeric field clamped to [0, 1].
func jFrac(col, name string) string {
	return fmt.Sprintf(`(case when jsonb_typeof(%[1]s->'%[2]s') = 'number' then
		(case when (%[1]s->>'%[2]s')::numeric between -1e15 and 1e15
			then greatest(0::float8, least(1::float8, (%[1]s->>'%[2]s')::numeric::float8)) end) end)`, col, name)
}

// jStrings are the string elements of an array field; other elements and non-arrays are dropped.
func jStrings(col, name string) string {
	return fmt.Sprintf(`array(select e #>> '{}' from jsonb_array_elements(
		case when jsonb_typeof(%[1]s->'%[2]s') = 'array' then %[1]s->'%[2]s' else '[]'::jsonb end) e
		where jsonb_typeof(e) = 'string')`, col, name)
}

// base selects every live library record with the columns the filters and the sort need. The
// chapter counts are added later, for the visible page only.
const baseSelect = `select r.key, r.work_id,
	coalesce(` + `{customTitle}` + `, w.title, ` + `{title}` + `, '') as title,
	coalesce(w.author, ` + `{author}` + `) as author,
	coalesce(w.content_kind, case when ` + `{kind}` + ` in ('manga', 'novel') then ` + `{kind}` + ` end) as content_kind,
	w.cover_url, w.source_name, w.language,
	position('local:' in r.key) = 1 as is_local,
	` + `{addedAt}` + ` as added_at,
	` + `{categories}` + ` as category_ids,
	` + `{pinned}` + ` as pinned,
	` + `{archived}` + ` as archived,
	` + `{progression}` + ` as progression,
	` + `{currentUnit}` + ` as current_unit,
	` + `{lastReadAt}` + ` as last_read_at,
	(extract(epoch from r.updated_at) * 1000)::bigint as updated_ms
from sync_records r
left join works w on w.id = r.work_id
left join sync_records pr on pr.user_id = r.user_id and pr.kind = 'progress' and pr.key = r.key and not pr.is_deleted
left join sync_records hs on hs.user_id = r.user_id and hs.kind = 'history' and hs.key = r.key and not hs.is_deleted
where r.user_id = $1 and r.kind = 'library' and not r.is_deleted`

func baseSQL() string {
	rep := strings.NewReplacer(
		"{customTitle}", `(case when `+jText("r.fields", "customTitle")+` <> '' then `+jText("r.fields", "customTitle")+` end)`,
		"{title}", jText("r.fields", "title"),
		"{author}", jText("r.fields", "author"),
		"{kind}", jText("r.fields", "contentKind"),
		"{addedAt}", jInt("r.fields", "addedAt"),
		"{categories}", jStrings("r.fields", "categoryIds"),
		"{pinned}", jBool("r.fields", "pinned"),
		"{archived}", jBool("r.fields", "archived"),
		"{progression}", jFrac("pr.fields", "progression"),
		"{currentUnit}", jText("pr.fields", "unitRemoteId"),
		"{lastReadAt}", jInt("hs.fields", "lastReadAt"),
	)
	return rep.Replace(baseSelect)
}

// escapeLike makes s match literally inside an ILIKE pattern with backslash as the escape.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (q Query) where() (string, []any) {
	var conds []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)+1) } // $1 is the user
	if q.Q != "" {
		p := arg("%" + escapeLike(q.Q) + "%")
		conds = append(conds, fmt.Sprintf(`(b.title ilike %[1]s escape '\' or coalesce(b.author, '') ilike %[1]s escape '\')`, p))
	}
	switch q.Category {
	case "":
	case "default":
		conds = append(conds, `cardinality(b.category_ids) = 0`)
	default:
		conds = append(conds, arg(q.Category)+` = any(b.category_ids)`)
	}
	switch q.Archived {
	case "include":
	case "only":
		conds = append(conds, `b.archived`)
	default:
		conds = append(conds, `not b.archived`)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " where " + strings.Join(conds, " and "), args
}

func (q Query) orderBy() string {
	switch q.Sort {
	case "title":
		return ` order by lower(b.title), b.title, b.key`
	case "added":
		return ` order by b.added_at desc nulls last, b.key`
	default:
		return ` order by coalesce(b.last_read_at, b.added_at, b.updated_ms) desc, b.key`
	}
}

const itemColumns = `b.key, b.title, b.author, b.content_kind, b.cover_url, b.source_name, b.language, b.is_local,
	b.added_at, b.category_ids, b.pinned, b.archived, coalesce(u.tracked, 0), coalesce(u.read_count, 0),
	b.progression, b.current_unit, b.last_read_at, b.updated_ms`

// unitCounts counts chapter states of the work; local entries have none.
const unitCounts = ` left join lateral (
	select count(*)::int as tracked, (count(*) filter (where x.fields->'read' = 'true'::jsonb))::int as read_count
	from sync_records x where x.user_id = $1 and x.kind = 'unit' and x.work_id = b.work_id and not x.is_deleted) u on b.work_id is not null`

func scanItem(row pgx.Row) (Item, error) {
	var it Item
	err := row.Scan(&it.Key, &it.Title, &it.Author, &it.ContentKind, &it.CoverURL, &it.SourceName, &it.Language, &it.Local,
		&it.AddedAt, &it.CategoryIDs, &it.Pinned, &it.Archived, &it.TrackedUnits, &it.ReadUnits,
		&it.Progression, &it.CurrentUnitRemoteID, &it.LastReadAt, &it.UpdatedAt)
	if it.CategoryIDs == nil {
		it.CategoryIDs = []string{}
	}
	return it, err
}

// List returns one page of entries.
func (s *Service) List(ctx context.Context, userID uuid.UUID, q Query) (*Page, error) {
	where, args := q.where()
	base := baseSQL()
	page := &Page{Items: []Item{}, Offset: q.Offset, Limit: q.Limit}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	all := append([]any{userID}, args...)
	if err := tx.QueryRow(ctx, `with b as (`+base+`) select count(*) from b`+where, all...).Scan(&page.Total); err != nil {
		return nil, err
	}
	n := len(all)
	sql := `with b as (` + base + `), pg as (select * from b` + where + q.orderBy() +
		fmt.Sprintf(` limit $%d offset $%d`, n+1, n+2) + `)
		select ` + itemColumns + ` from pg as b` + unitCounts + q.orderBy()
	rows, err := tx.Query(ctx, sql, append(all, q.Limit, q.Offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, it)
	}
	return page, rows.Err()
}

// Get returns one entry with its chapters, or a 404.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, key string) (*Detail, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	it, err := scanItem(tx.QueryRow(ctx, `with b as (`+baseSQL()+` and r.key = $2) select `+itemColumns+` from b`+unitCounts, userID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.NotFound("library entry not found")
	}
	if err != nil {
		return nil, err
	}
	d := &Detail{Item: it, Units: []Unit{}}

	var (
		hash, pkg, src, rid, ttl, ck       string
		kv                                 int16
		author, cover, lang, srcName, repo *string
	)
	err = tx.QueryRow(ctx, `select w.hash, w.key_version, w.package_id, w.source_id, w.remote_id, w.title, w.author, w.cover_url,
			w.content_kind, w.language, w.source_name, w.repository_url
		from sync_records r join works w on w.id = r.work_id where r.user_id = $1 and r.kind = 'library' and r.key = $2`, userID, key).
		Scan(&hash, &kv, &pkg, &src, &rid, &ttl, &author, &cover, &ck, &lang, &srcName, &repo)
	switch {
	case err == nil:
		d.Work = &syncsvc.Work{KeyVersion: int(kv), PackageID: pkg, SourceID: src, RemoteID: rid, Hash: hash, Title: ttl,
			Author: author, CoverURL: cover, ContentKind: ck, Language: lang, SourceName: srcName, RepositoryURL: repo}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return nil, err
	}

	if d.Work != nil {
		rows, err := tx.Query(ctx, `select coalesce(x.unit_remote_id, ''), `+jBool("x.fields", "read")+`, `+jBool("x.fields", "bookmarked")+`,
				`+jInt("x.fields", "lastPageIndex")+`, (extract(epoch from x.updated_at) * 1000)::bigint
			from sync_records x
			where x.user_id = $1 and x.kind = 'unit' and not x.is_deleted
				and x.work_id = (select work_id from sync_records where user_id = $1 and kind = 'library' and key = $2)
			order by x.updated_at desc, x.key limit $3`, userID, key, MaxUnits)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var u Unit
			if err := rows.Scan(&u.UnitRemoteID, &u.Read, &u.Bookmarked, &u.LastPageIdx, &u.UpdatedAt); err != nil {
				return nil, err
			}
			d.Units = append(d.Units, u)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Categories lists the live category records ordered by `order`, then name.
func (s *Service) Categories(ctx context.Context, userID uuid.UUID) ([]Category, error) {
	rows, err := s.pool.Query(ctx, `select key, coalesce(`+jText("fields", "name")+`, '') as name, coalesce(`+jInt("fields", "order")+`, 0) as ord
		from sync_records where user_id = $1 and kind = 'category' and not is_deleted order by ord, name, key`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Key, &c.Name, &c.Order); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
