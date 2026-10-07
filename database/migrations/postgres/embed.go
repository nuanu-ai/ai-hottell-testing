// Package postgres embeds the PostgreSQL migrations into the binaries that apply them.
package postgres

import "embed"

// FS holds the goose migrations of this directory. The pattern is "*" rather than "*.sql"
// because an embed pattern must match a file; goose ignores the files without a version prefix.
//
//go:embed *
var FS embed.FS
