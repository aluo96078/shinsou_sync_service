// Package migrations embeds the SQL migrations applied by goose.
package migrations

import "embed"

// FS holds every *.sql migration.
//
//go:embed *.sql
var FS embed.FS
