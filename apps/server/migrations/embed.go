// Package migrations embeds the SliilS goose-format SQL migrations so they
// ship inside the compiled binary.
//
// Adding a new migration: create `YYYYMMDDHHMMSS_<snake_description>.sql`
// in this directory with `-- +goose Up` and `-- +goose Down` sections.
package migrations

import "embed"

// FS is the embedded migration bundle.
//
//go:embed *.sql
var FS embed.FS
