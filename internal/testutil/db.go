// Package testutil holds helpers shared by integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/db"
)

// DB is an isolated, fully migrated schema inside the shared test database.
type DB struct {
	Pool   *pgxpool.Pool
	URL    string
	Schema string
}

// NewDB creates a fresh schema, migrates it and drops it when the test ends. The test is skipped
// when SHINSOU_TEST_DATABASE_URL is not set.
func NewDB(t testing.TB) *DB {
	t.Helper()
	url := os.Getenv("SHINSOU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SHINSOU_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	var b [6]byte
	_, _ = rand.Read(b[:])
	schema := "t_" + hex.EncodeToString(b[:])

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, `create schema `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	_ = admin.Close(ctx)

	if _, err := db.Migrate(ctx, url, schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, url, schema)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(ctx, url)
		if err != nil {
			return
		}
		defer c.Close(ctx)
		_, _ = c.Exec(ctx, `drop schema `+schema+` cascade`)
	})
	return &DB{Pool: pool, URL: url, Schema: schema}
}
