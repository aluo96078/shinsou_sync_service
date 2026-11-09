// Package db opens the PostgreSQL pool and runs the embedded migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/aluo96078/shinsou_sync_service/migrations"
)

// Open creates a connection pool. schema, when not empty, becomes the search_path of every
// connection; tests use it to isolate themselves inside a shared database.
func Open(ctx context.Context, url, schema string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if schema != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies every pending migration and returns how many ran.
func Migrate(ctx context.Context, url, schema string) (int, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return 0, fmt.Errorf("parse database url: %w", err)
	}
	if schema != "" {
		cfg.RuntimeParams["search_path"] = schema
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()
	return migrateDB(ctx, sqlDB)
}

func migrateDB(ctx context.Context, sqlDB *sql.DB) (int, error) {
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return 0, fmt.Errorf("goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return 0, fmt.Errorf("migrate: %w", err)
	}
	return len(results), nil
}
