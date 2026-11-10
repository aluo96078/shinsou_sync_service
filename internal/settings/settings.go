// Package settings holds the instance settings an administrator can change at runtime. The
// environment provides the defaults; overrides live in instance_settings. The effective values are
// kept in memory (the server is a single process), loaded at start and refreshed after each change.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/config"
)

// Values are the effective settings. Empty URL and contact strings mean "none".
type Values struct {
	Name              string
	Description       string
	Registration      string
	EmailVerification string
	TermsURL          string
	PrivacyURL        string
	Contact           string
}

// keys maps the JSON name used by the API to the instance_settings key.
var keys = []struct{ api, db string }{
	{"name", "name"},
	{"description", "description"},
	{"registration", "registration"},
	{"emailVerification", "email_verification"},
	{"termsUrl", "terms_url"},
	{"privacyUrl", "privacy_url"},
	{"contact", "contact"},
}

func dbKey(api string) (string, bool) {
	for _, k := range keys {
		if k.api == api {
			return k.db, true
		}
	}
	return "", false
}

func (v *Values) field(db string) *string {
	switch db {
	case "name":
		return &v.Name
	case "description":
		return &v.Description
	case "registration":
		return &v.Registration
	case "email_verification":
		return &v.EmailVerification
	case "terms_url":
		return &v.TermsURL
	case "privacy_url":
		return &v.PrivacyURL
	case "contact":
		return &v.Contact
	}
	return nil
}

// Store is the in-memory view of the settings.
type Store struct {
	pool           *pgxpool.Pool
	emailAvailable bool
	defaults       Values

	patchMu sync.Mutex // serialises Patch end to end

	mu         sync.RWMutex
	effective  Values
	overridden []string
}

// New creates a store whose defaults come from cfg. Call Load before use.
func New(pool *pgxpool.Pool, cfg config.Config) *Store {
	d := Values{Name: cfg.InstanceName, Description: cfg.InstanceDescription, Registration: cfg.Registration,
		EmailVerification: cfg.EmailVerification, TermsURL: cfg.TermsURL, PrivacyURL: cfg.PrivacyURL, Contact: cfg.Contact}
	return &Store{pool: pool, emailAvailable: cfg.EmailAvailable(), defaults: d, effective: d}
}

// Get returns the effective values.
func (s *Store) Get() Values {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.effective
}

// Defaults returns the environment defaults.
func (s *Store) Defaults() Values { return s.defaults }

// Overridden lists the API names of the keys whose value comes from the database.
func (s *Store) Overridden() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.overridden)
}

// EmailAvailable reports whether the server can send mail.
func (s *Store) EmailAvailable() bool { return s.emailAvailable }

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Load reads the overrides from the database.
func (s *Store) Load(ctx context.Context) error {
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	return s.reload(ctx, s.pool)
}

func (s *Store) reload(ctx context.Context, q querier) error {
	rows, err := q.Query(ctx, `select key, value from instance_settings where key = any($1)`, dbKeys())
	if err != nil {
		return err
	}
	type kv struct {
		Key   string
		Value []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (kv, error) {
		var x kv
		return x, r.Scan(&x.Key, &x.Value)
	})
	if err != nil {
		return err
	}
	eff := s.defaults
	var over []string
	for _, x := range list {
		var str string
		if json.Unmarshal(x.Value, &str) != nil {
			continue // not a string: ignore a corrupt row rather than refusing to start
		}
		if f := eff.field(x.Key); f != nil {
			*f = str
			for _, k := range keys {
				if k.db == x.Key {
					over = append(over, k.api)
				}
			}
		}
	}
	slices.Sort(over)
	s.mu.Lock()
	s.effective, s.overridden = eff, over
	s.mu.Unlock()
	return nil
}

func dbKeys() []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.db
	}
	return out
}

// Patch applies a partial update: a string sets an override, null removes it. The change and its
// audit entry are written in one transaction; the in-memory values are refreshed afterwards.
func (s *Store) Patch(ctx context.Context, actor uuid.UUID, patch map[string]json.RawMessage) error {
	if len(patch) == 0 {
		return apierr.Validation("at least one setting is required")
	}
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	set := map[string]string{}
	reset := []string{}
	detail := map[string]any{}
	next := s.Get()
	for api, raw := range patch {
		db, ok := dbKey(api)
		if !ok {
			return apierr.Validation("unknown setting " + api)
		}
		if string(raw) == "null" {
			reset = append(reset, db)
			detail[api] = nil
			*next.field(db) = *s.defaults.field(db)
			continue
		}
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return apierr.Validation(api + " must be a string or null")
		}
		str = strings.TrimSpace(str)
		if err := validate(api, str); err != nil {
			return err
		}
		set[db] = str
		detail[api] = str
		*next.field(db) = str
	}
	if next.EmailVerification == config.EmailRequired && !s.emailAvailable {
		return apierr.New(409, "email_unavailable", "email verification cannot be required: the server has no SMTP configured")
	}
	detailJSON, _ := json.Marshal(map[string]any{"changes": detail})

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for db, v := range set {
			val, _ := json.Marshal(v)
			if _, err := tx.Exec(ctx, `insert into instance_settings (key, value) values ($1, $2)
				on conflict (key) do update set value = excluded.value, updated_at = now()`, db, val); err != nil {
				return err
			}
		}
		if len(reset) > 0 {
			if _, err := tx.Exec(ctx, `delete from instance_settings where key = any($1)`, reset); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `insert into audit_log (actor_id, action, target_type, detail) values ($1, 'settings.update', 'settings', $2)`,
			actor, detailJSON)
		return err
	})
	if err != nil {
		return err
	}
	return s.reload(ctx, s.pool)
}

func validate(api, v string) error {
	switch api {
	case "name":
		if n := utf8.RuneCountInString(v); n < 1 || n > 80 {
			return apierr.Validation("name must be 1-80 characters")
		}
	case "description":
		if utf8.RuneCountInString(v) > 500 {
			return apierr.Validation("description must be at most 500 characters")
		}
	case "registration":
		switch v {
		case config.RegistrationOpen, config.RegistrationInvite, config.RegistrationClosed:
		default:
			return apierr.Validation("registration must be open, invite or closed")
		}
	case "emailVerification":
		switch v {
		case config.EmailDisabled, config.EmailOptional, config.EmailRequired:
		default:
			return apierr.Validation("emailVerification must be disabled, optional or required")
		}
	case "termsUrl", "privacyUrl":
		if v == "" {
			return nil
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(v) > 2048 {
			return apierr.Validation(fmt.Sprintf("%s must be an absolute http(s) URL of at most 2048 characters", api))
		}
	case "contact":
		if utf8.RuneCountInString(v) > 200 {
			return apierr.Validation("contact must be at most 200 characters")
		}
	}
	return nil
}
