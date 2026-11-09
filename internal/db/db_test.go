package db_test

import (
	"context"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/db"
	"github.com/aluo96078/shinsou_sync_service/internal/testutil"
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
