package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/httpapi"
	"github.com/aluo96078/shinsou_sync_service/internal/jobs"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
	"github.com/aluo96078/shinsou_sync_service/internal/testutil"
	"github.com/aluo96078/shinsou_sync_service/internal/workkey"
)

type env struct {
	t    *testing.T
	srv  *httptest.Server
	pool *pgxpool.Pool
	cfg  config.Config
	logs *syncBuf
	jobs *jobs.Runner
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newEnv starts a full server on an isolated schema. Hashing is cheap and rate limits are off
// unless a test turns them on through mutate.
func newEnv(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	d := testutil.NewDB(t)
	cfg := config.Defaults()
	cfg.DatabaseURL = d.URL
	cfg.Registration = config.RegistrationOpen
	cfg.Argon2MemoryKiB, cfg.Argon2Time = 8, 1
	cfg.AuthRatePerMinute, cfg.PushRatePerMinute = 0, 0
	cfg.PublicURL = "https://sync.test"
	for _, m := range mutate {
		m(&cfg)
	}
	logs := &syncBuf{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	srv, err := httpapi.New(context.Background(), cfg, d.Pool, log, "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	return &env{t: t, srv: ts, pool: d.Pool, cfg: cfg, logs: logs,
		jobs: jobs.New(d.Pool, jobs.Options{TombstoneRetention: 0, BatchRetention: 0}, log)}
}

type apiError struct {
	Error struct {
		Code              string `json:"code"`
		Message           string `json:"message"`
		RetryAfterSeconds int    `json:"retryAfterSeconds"`
	} `json:"error"`
}

// call performs a request and returns the status and body.
func (e *env) call(method, path, token string, body any, headers ...string) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// expect calls and decodes the body into T, failing unless the status matches.
func expect[T any](e *env, want int, method, path, token string, body any, headers ...string) T {
	e.t.Helper()
	status, raw := e.call(method, path, token, body, headers...)
	if status != want {
		e.t.Fatalf("%s %s: status %d, want %d; body %s", method, path, status, want, raw)
	}
	var out T
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			e.t.Fatalf("%s %s: decode: %v; body %s", method, path, err, raw)
		}
	}
	return out
}

// expectErr calls and asserts the status and error code.
func (e *env) expectErr(want int, code, method, path, token string, body any, headers ...string) apiError {
	e.t.Helper()
	status, raw := e.call(method, path, token, body, headers...)
	var ae apiError
	_ = json.Unmarshal(raw, &ae)
	if status != want || ae.Error.Code != code {
		e.t.Fatalf("%s %s: got %d %q (%s), want %d %q", method, path, status, ae.Error.Code, raw, want, code)
	}
	return ae
}

type session struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"accessTokenExpiresAt"`
	User         struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
		Handle   string `json:"handle"`
	} `json:"user"`
	Device struct {
		ID           string `json:"id"`
		SyncDeviceID string `json:"syncDeviceId"`
		Current      bool   `json:"current"`
	} `json:"device"`
}

func deviceInfo(name string) map[string]any {
	return map[string]any{"name": name, "platform": "android", "appVersion": "1.0.0"}
}

func (e *env) register(username string) session {
	e.t.Helper()
	return expect[session](e, 201, "POST", "/api/v1/auth/register", "", map[string]any{
		"username": username, "password": "correct horse", "device": deviceInfo("phone"),
	})
}

func (e *env) login(username, password, device string) session {
	e.t.Helper()
	return expect[session](e, 200, "POST", "/api/v1/auth/login", "", map[string]any{
		"username": username, "password": password, "device": deviceInfo(device),
	})
}

// --- sync helpers ---

func mkWork(remoteID string) syncsvc.Work {
	w := workkey.Work{KeyVersion: 1, PackageID: "zh.wenku8", SourceID: "zh.wenku8", RemoteID: remoteID}
	h, err := w.Hash()
	if err != nil {
		panic(err)
	}
	return syncsvc.Work{KeyVersion: 1, PackageID: w.PackageID, SourceID: w.SourceID, RemoteID: remoteID,
		Hash: h, Title: "Title " + remoteID, ContentKind: "novel"}
}

func unitKey(w syncsvc.Work, unit string) string {
	h, err := workkey.Work{KeyVersion: 1, PackageID: w.PackageID, SourceID: w.SourceID, RemoteID: w.RemoteID}.UnitHash(unit)
	if err != nil {
		panic(err)
	}
	return h
}

func clock(ms int64, counter int, device string) string {
	return fmt.Sprintf("%d:%d:%s", ms, counter, device)
}

func nowMs() int64 { return time.Now().UnixMilli() }

func rec(kind, key string, field string, value any, clk string) syncsvc.PushRecord {
	raw, _ := json.Marshal(value)
	return syncsvc.PushRecord{Kind: kind, Key: key,
		Fields: map[string]json.RawMessage{field: raw}, Clocks: map[string]string{field: clk}}
}

func tombstone(kind, key, clk string) syncsvc.PushRecord {
	v := true
	return syncsvc.PushRecord{Kind: kind, Key: key, Deleted: &syncsvc.PushDeleted{Value: &v, Clock: clk}}
}

var batchCounter struct {
	sync.Mutex
	n int
}

func newBatchID() string {
	batchCounter.Lock()
	defer batchCounter.Unlock()
	batchCounter.n++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", batchCounter.n)
}

func push(e *env, token string, since int64, works []syncsvc.Work, records ...syncsvc.PushRecord) syncsvc.PushResponse {
	e.t.Helper()
	return expect[syncsvc.PushResponse](e, 200, "POST", "/api/v1/sync/push", token, pushBody(since, works, records, newBatchID()))
}

func pushBody(since int64, works []syncsvc.Work, records []syncsvc.PushRecord, batchID string) syncsvc.PushRequest {
	return syncsvc.PushRequest{BatchID: batchID, Since: &since, Works: works, Records: records}
}

func pull(e *env, token string, since int64) syncsvc.ChangesResponse {
	e.t.Helper()
	return expect[syncsvc.ChangesResponse](e, 200, "GET", fmt.Sprintf("/api/v1/sync/changes?since=%d", since), token, nil)
}

func wsURL(e *env, path string) string { return "ws" + strings.TrimPrefix(e.srv.URL, "http") + path }

func slogTo(w io.Writer) *slog.Logger { return slog.New(slog.NewJSONHandler(w, nil)) }

func newRunner(e *env, retentionSecs int) *jobs.Runner {
	return jobs.New(e.pool, jobs.Options{TombstoneRetention: time.Duration(retentionSecs) * time.Second,
		BatchRetention: 24 * time.Hour}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
}
