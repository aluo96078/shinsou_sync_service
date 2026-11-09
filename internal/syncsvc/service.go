package syncsvc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/lww"
	"github.com/aluo96078/shinsou_sync_service/internal/notify"
	"github.com/aluo96078/shinsou_sync_service/internal/workkey"
)

// Service merges pushed records and serves pulls.
type Service struct {
	pool         *pgxpool.Pool
	hub          *notify.Hub
	defaultQuota int
	now          func() time.Time
}

// New creates the sync service. defaultQuota is the per-user record limit (0 = unlimited).
func New(pool *pgxpool.Pool, hub *notify.Hub, defaultQuota int) *Service {
	return &Service{pool: pool, hub: hub, defaultQuota: defaultQuota, now: time.Now}
}

func errResync() *apierr.Error {
	return apierr.New(http.StatusGone, "resync_required", "the cursor is older than the retained history; pull again from since=0")
}

// work is a work row known during one push.
type work struct {
	id   int64
	desc Work
}

type stateKey struct{ kind, key string }

type entry struct {
	state  lww.State
	seq    int64
	exists bool
	dirty  bool
	work   *work
	unit   string
}

type prepared struct {
	rec    PushRecord
	key    string
	reject string
	work   *work
	unit   string
}

// Push merges a batch inside one transaction that holds the user's row lock, which makes the
// sequence numbers it hands out commit in order.
func (s *Service) Push(ctx context.Context, userID uuid.UUID, req PushRequest) (*PushResponse, error) {
	batchID, err := uuid.Parse(req.BatchID)
	if err != nil {
		return nil, apierr.Validation("batchId must be a UUID")
	}
	if req.Since == nil || *req.Since < 0 {
		return nil, apierr.Validation("since is required and must be >= 0")
	}
	if len(req.Records) > MaxRecordsPerBatch || len(req.Works) > MaxWorksPerBatch {
		return nil, apierr.New(http.StatusRequestEntityTooLarge, "payload_too_large", "at most 500 records and 500 works per batch")
	}
	works := map[string]Work{}
	for _, w := range req.Works {
		if e := validateWork(w); e != nil {
			return nil, e
		}
		works[w.Hash] = w
	}

	var (
		resp     *PushResponse
		startSeq int64
		endSeq   int64
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var floor int64
		var quota *int
		err := tx.QueryRow(ctx, `select sync_seq, sync_floor_seq, record_quota from users where id = $1 for update`, userID).
			Scan(&startSeq, &floor, &quota)
		if err != nil {
			return err
		}
		if *req.Since != 0 && *req.Since < floor {
			return errResync()
		}
		var stored []byte
		err = tx.QueryRow(ctx, `select response from sync_batches where user_id = $1 and batch_id = $2`, userID, batchID).Scan(&stored)
		if err == nil {
			resp = &PushResponse{}
			endSeq = startSeq // a replay changes nothing
			return json.Unmarshal(stored, resp)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		limit := s.defaultQuota
		if quota != nil {
			limit = *quota
		}
		resp, endSeq, err = s.merge(ctx, tx, userID, startSeq, limit, works, req.Records)
		if err != nil {
			return err
		}
		body, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into sync_batches (user_id, batch_id, response) values ($1, $2, $3)`, userID, batchID, body)
		return err
	})
	if err != nil {
		return nil, err
	}
	if endSeq > startSeq {
		s.hub.Publish(userID, endSeq)
	}
	return resp, nil
}

func (s *Service) merge(ctx context.Context, tx pgx.Tx, userID uuid.UUID, startSeq int64, quota int,
	batchWorks map[string]Work, records []PushRecord) (*PushResponse, int64, error) {

	known, err := s.loadWorks(ctx, tx, userID, batchWorks, records)
	if err != nil {
		return nil, 0, err
	}

	// Pass 1: validate keys and resolve works.
	prep := make([]prepared, len(records))
	for i, rec := range records {
		prep[i] = prepared{rec: rec, key: rec.Key}
		p := &prep[i]
		if !kinds[rec.Kind] {
			p.reject = string(lww.InvalidRecord)
			continue
		}
		switch {
		case rec.Kind == "unit":
			w, ok := known[rec.Work]
			if !hashRe.MatchString(rec.Work) || rec.UnitRemoteID == nil {
				p.reject = ResultInvalidKey
				continue
			}
			if !ok {
				p.reject = ResultUnknownWork
				continue
			}
			id := workkey.Work{KeyVersion: w.desc.KeyVersion, PackageID: w.desc.PackageID, SourceID: w.desc.SourceID, RemoteID: w.desc.RemoteID}
			unitHash, err := id.UnitHash(*rec.UnitRemoteID)
			if err != nil || unitHash != rec.Key {
				p.reject = ResultInvalidKey
				continue
			}
			p.work, p.unit = w, *rec.UnitRemoteID
		case !validKey(rec.Kind, rec.Key):
			p.reject = ResultInvalidKey
		case workKeyed(rec.Kind) && hashRe.MatchString(rec.Key):
			w, ok := known[rec.Key]
			if !ok {
				p.reject = ResultUnknownWork
				continue
			}
			p.work = w
		}
	}

	// Load the current state of every touched record in one query.
	var ks, kk []string
	seen := map[stateKey]bool{}
	for _, p := range prep {
		sk := stateKey{p.rec.Kind, p.key}
		if p.reject == "" && !seen[sk] {
			seen[sk] = true
			ks, kk = append(ks, p.rec.Kind), append(kk, p.key)
		}
	}
	states, err := loadStates(ctx, tx, userID, ks, kk)
	if err != nil {
		return nil, 0, err
	}

	var (
		seq        = startSeq
		count      = -1
		nowMillis  = s.now().UnixMilli()
		results    = make([]PushResult, len(prep))
		batch      = &pgx.Batch{}
		writeOrder []stateKey
	)
	for i, p := range prep {
		res := PushResult{Kind: p.rec.Kind, Key: p.rec.Key}
		if p.reject != "" {
			res.Result = p.reject
			results[i] = res
			continue
		}
		push, bad := toLWW(p.rec)
		if bad {
			res.Result = string(lww.InvalidRecord)
			results[i] = res
			continue
		}
		sk := stateKey{p.rec.Kind, p.key}
		e := states[sk]
		if e == nil {
			e = &entry{}
			states[sk] = e
		}
		var cur *lww.State
		if e.exists {
			cur = &e.state
		}
		next, r := lww.Merge(cur, push, nowMillis)
		if r.Rejected() {
			res.Result = string(r)
			results[i] = res
			continue
		}
		if r == lww.Applied {
			if !e.exists && quota > 0 {
				if count < 0 {
					if err := tx.QueryRow(ctx, `select count(*) from sync_records where user_id = $1`, userID).Scan(&count); err != nil {
						return nil, 0, err
					}
				}
				if count >= quota {
					res.Result = ResultQuotaExceeded
					results[i] = res
					continue
				}
			}
			if !e.exists {
				count++
			}
			if !e.dirty {
				seq++ // a record changed twice in one batch consumes a single seq
				e.seq = seq
			}
			e.state, e.exists = next, true
			e.work, e.unit = p.work, p.unit
			if !e.dirty {
				writeOrder = append(writeOrder, sk)
			}
			e.dirty = true
		}
		res.Result = string(r)
		res.Record = toRecord(sk, e, p.work, p.unit)
		results[i] = res
	}

	for _, sk := range writeOrder {
		e := states[sk]
		if err := queueUpsert(batch, userID, sk, e); err != nil {
			return nil, 0, err
		}
	}
	if batch.Len() > 0 {
		br := tx.SendBatch(ctx, batch)
		for range batch.Len() {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return nil, 0, err
			}
		}
		if err := br.Close(); err != nil {
			return nil, 0, err
		}
	}
	if seq != startSeq {
		if _, err := tx.Exec(ctx, `update users set sync_seq = $2 where id = $1`, userID, seq); err != nil {
			return nil, 0, err
		}
	}
	// Results for a record touched more than once in the batch must show the final seq of
	// the intermediate states they saw; that is already captured in each result.
	return &PushResponse{Results: results, Seq: seq}, seq, nil
}

func toLWW(rec PushRecord) (lww.Push, bool) {
	p := lww.Push{Clocks: rec.Clocks}
	if len(rec.Fields) > 0 {
		p.Fields = rec.Fields
		for _, v := range rec.Fields {
			if !jsonSafe(v) {
				return lww.Push{}, true
			}
		}
	}
	if rec.Deleted != nil {
		if rec.Deleted.Value == nil {
			return lww.Push{}, true
		}
		p.Deleted = &lww.Register{Value: *rec.Deleted.Value, Clock: rec.Deleted.Clock}
	}
	return p, false
}

func toRecord(sk stateKey, e *entry, w *work, unit string) *SyncRecord {
	r := &SyncRecord{Kind: sk.kind, Key: sk.key, Seq: e.seq, Fields: e.state.Fields, Clocks: e.state.Clocks,
		Deleted: e.state.Deleted, Cleared: e.state.Cleared, UnitRemoteID: unit}
	if r.Fields == nil {
		r.Fields = map[string]json.RawMessage{}
	}
	if r.Clocks == nil {
		r.Clocks = map[string]string{}
	}
	if w != nil {
		d := w.desc
		r.Work = &d
	}
	return r
}

func queueUpsert(b *pgx.Batch, userID uuid.UUID, sk stateKey, e *entry) error {
	fields, err := json.Marshal(e.state.Fields)
	if err != nil {
		return err
	}
	clocks, err := json.Marshal(e.state.Clocks)
	if err != nil {
		return err
	}
	var deleted any
	if e.state.Deleted != nil {
		d, err := json.Marshal(e.state.Deleted)
		if err != nil {
			return err
		}
		deleted = d
	}
	var cleared, unit any
	if e.state.Cleared != "" {
		cleared = e.state.Cleared
	}
	if e.unit != "" {
		unit = e.unit
	}
	var workID any
	if e.work != nil {
		workID = e.work.id
	}
	b.Queue(`insert into sync_records (user_id, kind, key, work_id, unit_remote_id, fields, clocks, deleted, cleared, seq, updated_at)
		values ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb, $9, $10, now())
		on conflict (user_id, kind, key) do update set
			work_id = excluded.work_id, unit_remote_id = excluded.unit_remote_id, fields = excluded.fields,
			clocks = excluded.clocks, deleted = excluded.deleted, cleared = excluded.cleared,
			seq = excluded.seq, updated_at = now()`,
		userID, sk.kind, sk.key, workID, unit, fields, clocks, deleted, cleared, e.seq)
	return nil
}

func loadStates(ctx context.Context, tx pgx.Tx, userID uuid.UUID, kinds, keys []string) (map[stateKey]*entry, error) {
	out := map[stateKey]*entry{}
	if len(kinds) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `select r.kind, r.key, r.fields, r.clocks, r.deleted, r.cleared, r.seq, r.unit_remote_id
		from sync_records r
		join unnest($2::text[], $3::text[]) as t(kind, key) on t.kind = r.kind and t.key = r.key
		where r.user_id = $1`, userID, kinds, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind, key       string
			fields, clocks  []byte
			deleted         []byte
			cleared, unitID *string
			seq             int64
		)
		if err := rows.Scan(&kind, &key, &fields, &clocks, &deleted, &cleared, &seq, &unitID); err != nil {
			return nil, err
		}
		st, err := decodeState(fields, clocks, deleted, cleared)
		if err != nil {
			return nil, err
		}
		e := &entry{state: st, seq: seq, exists: true}
		if unitID != nil {
			e.unit = *unitID
		}
		out[stateKey{kind, key}] = e
	}
	return out, rows.Err()
}

func decodeState(fields, clocks, deleted []byte, cleared *string) (lww.State, error) {
	st := lww.State{Fields: map[string]json.RawMessage{}, Clocks: map[string]string{}}
	if err := json.Unmarshal(fields, &st.Fields); err != nil {
		return st, err
	}
	if err := json.Unmarshal(clocks, &st.Clocks); err != nil {
		return st, err
	}
	if deleted != nil {
		var d lww.Register
		if err := json.Unmarshal(deleted, &d); err != nil {
			return st, err
		}
		st.Deleted = &d
	}
	if cleared != nil {
		st.Cleared = *cleared
	}
	return st, nil
}

// loadWorks upserts the batch's descriptors and looks up other works referenced by records.
func (s *Service) loadWorks(ctx context.Context, tx pgx.Tx, userID uuid.UUID, batch map[string]Work, records []PushRecord) (map[string]*work, error) {
	known := map[string]*work{}
	hashes := make([]string, 0, len(batch))
	for h := range batch {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes) // consistent lock order across concurrent pushes
	for _, h := range hashes {
		w := batch[h]
		var id int64
		err := tx.QueryRow(ctx, `insert into works (hash, key_version, package_id, source_id, remote_id, title, author, cover_url,
				content_kind, language, source_name, repository_url, updated_by)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			on conflict (hash) do update set title = excluded.title, author = excluded.author, cover_url = excluded.cover_url,
				content_kind = excluded.content_kind, language = excluded.language, source_name = excluded.source_name,
				repository_url = excluded.repository_url, updated_by = excluded.updated_by, updated_at = now()
			where (works.title, works.author, works.cover_url, works.content_kind, works.language, works.source_name, works.repository_url)
				is distinct from (excluded.title, excluded.author, excluded.cover_url, excluded.content_kind, excluded.language,
				excluded.source_name, excluded.repository_url)
			returning id`,
			w.Hash, w.KeyVersion, w.PackageID, w.SourceID, w.RemoteID, w.Title, w.Author, w.CoverURL,
			w.ContentKind, w.Language, w.SourceName, w.RepositoryURL, userID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `select id from works where hash = $1`, h).Scan(&id)
		}
		if err != nil {
			return nil, err
		}
		known[h] = &work{id: id, desc: w}
	}
	var missing []string
	seen := map[string]bool{}
	for _, r := range records {
		h := ""
		switch {
		case r.Kind == "unit":
			h = r.Work
		case workKeyed(r.Kind):
			h = r.Key
		}
		if hashRe.MatchString(h) && known[h] == nil && !seen[h] {
			seen[h] = true
			missing = append(missing, h)
		}
	}
	if len(missing) == 0 {
		return known, nil
	}
	rows, err := tx.Query(ctx, `select id, hash, key_version, package_id, source_id, remote_id, title, author, cover_url,
		content_kind, language, source_name, repository_url from works where hash = any($1)`, missing)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w work
		var kv int16
		if err := rows.Scan(&w.id, &w.desc.Hash, &kv, &w.desc.PackageID, &w.desc.SourceID, &w.desc.RemoteID, &w.desc.Title,
			&w.desc.Author, &w.desc.CoverURL, &w.desc.ContentKind, &w.desc.Language, &w.desc.SourceName, &w.desc.RepositoryURL); err != nil {
			return nil, err
		}
		w.desc.KeyVersion = int(kv)
		cp := w
		known[w.desc.Hash] = &cp
	}
	return known, rows.Err()
}

// Changes returns records with seq > since, ascending.
func (s *Service) Changes(ctx context.Context, userID uuid.UUID, since int64, limit int) (*ChangesResponse, error) {
	if limit < 1 || limit > MaxChangesLimit {
		limit = MaxChangesLimit
	}
	out := &ChangesResponse{Records: []SyncRecord{}, NextSince: since}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var floor int64
	if err := tx.QueryRow(ctx, `select sync_floor_seq from users where id = $1`, userID).Scan(&floor); err != nil {
		return nil, err
	}
	// since=0 is a full pull and is always safe; it is also how a client recovers from 410.
	if since != 0 && since < floor {
		return nil, errResync()
	}
	rows, err := tx.Query(ctx, `select r.kind, r.key, r.seq, r.unit_remote_id, r.fields, r.clocks, r.deleted, r.cleared,
			w.hash, w.key_version, w.package_id, w.source_id, w.remote_id, w.title, w.author, w.cover_url,
			w.content_kind, w.language, w.source_name, w.repository_url
		from sync_records r left join works w on w.id = r.work_id
		where r.user_id = $1 and r.seq > $2 order by r.seq limit $3`, userID, since, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			rec                      SyncRecord
			unit                     *string
			fields, clocks, deleted  []byte
			cleared                  *string
			hash, pkg, src, rid, ttl *string
			kv                       *int16
			author, cover, ck        *string
			lang, srcName, repo      *string
		)
		if err := rows.Scan(&rec.Kind, &rec.Key, &rec.Seq, &unit, &fields, &clocks, &deleted, &cleared,
			&hash, &kv, &pkg, &src, &rid, &ttl, &author, &cover, &ck, &lang, &srcName, &repo); err != nil {
			return nil, err
		}
		st, err := decodeState(fields, clocks, deleted, cleared)
		if err != nil {
			return nil, err
		}
		rec.Fields, rec.Clocks, rec.Deleted, rec.Cleared = st.Fields, st.Clocks, st.Deleted, st.Cleared
		if unit != nil {
			rec.UnitRemoteID = *unit
		}
		if hash != nil {
			rec.Work = &Work{KeyVersion: int(*kv), PackageID: *pkg, SourceID: *src, RemoteID: *rid, Hash: *hash, Title: *ttl,
				Author: author, CoverURL: cover, ContentKind: *ck, Language: lang, SourceName: srcName, RepositoryURL: repo}
		}
		if len(out.Records) == limit {
			out.HasMore = true
			break
		}
		out.Records = append(out.Records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if n := len(out.Records); n > 0 {
		out.NextSince = out.Records[n-1].Seq
	}
	return out, nil
}

// CurrentSeq returns the latest sequence number handed out for the user.
func (s *Service) CurrentSeq(ctx context.Context, userID uuid.UUID) (int64, error) {
	var seq int64
	err := s.pool.QueryRow(ctx, `select sync_seq from users where id = $1`, userID).Scan(&seq)
	return seq, err
}
