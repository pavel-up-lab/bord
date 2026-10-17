// Package migrations embeds immutable, monotonically numbered SQL migrations.
package migrations

import "embed"

// Files contains NNNNNN_name.up.sql files. Applied migrations are never edited.
//
//go:embed *.up.sql
var Files embed.FS
