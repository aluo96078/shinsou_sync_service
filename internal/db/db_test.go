package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/aluo96078/shinsou_sync_service/internal/db"
	"github.com/aluo96078/shinsou_sync_service/internal/testutil"
	"github.com/aluo96078/shinsou_sync_service/migrations"
)

func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	n, err := db.Migrate(ctx, d.URL, d.Schema)
	if err != nil || n != 0 {
		t.Fatalf("second migrate = %d, %v; want 0, nil", n, err)
	}
	var count int
	if err := d.Pool.QueryRow(ctx, `select count(*) from information_schema.tables where table_schema = $1 and table_name = 'sync_records'`, d.Schema).Scan(&count); err != nil || count != 1 {
		t.Fatalf("sync_records missing: %d %v", count, err)
	}
}

// TestPackageKindMigrationUpDown checks that 00005 admits package records and that rolling it
// back deletes them and restores the original seven kinds.
func TestPackageKindMigrationUpDown(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	user := "00000000-0000-4000-8000-000000000001"
	if _, err := d.Pool.Exec(ctx, `insert into users (id, username, password_hash) values ($1, 'pkg', 'x')`, user); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, key string, seq int) error {
		_, err := d.Pool.Exec(ctx, `insert into sync_records (user_id, kind, key, seq) values ($1, $2, $3, $4)`, user, kind, key, seq)
		return err
	}
	if err := insert("package", "com.example.manhuagui", 1); err != nil {
		t.Fatalf("package record rejected after up: %v", err)
	}
	if err := insert("setting", "reader.fontSize", 2); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgx.ParseConfig(d.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = d.Schema
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 4); err != nil {
		t.Fatalf("down to 4: %v", err)
	}
	var n int
	if err := d.Pool.QueryRow(ctx, `select count(*) from sync_records where kind = 'package'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("package records after down = %d, %v; want 0", n, err)
	}
	if err := d.Pool.QueryRow(ctx, `select count(*) from sync_records`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("records after down = %d, %v; want the setting kept", n, err)
	}
	if err := insert("package", "com.example.manhuagui", 3); err == nil {
		t.Fatal("package record accepted after down")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if err := insert("package", "com.example.manhuagui", 4); err != nil {
		t.Fatalf("package record rejected after up again: %v", err)
	}
}
