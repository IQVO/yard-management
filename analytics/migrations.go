// Package analytics holds the ANALYTICAL schema of the read side (ADR 0006):
// the golang-migrate files under migrations/, embedded so cmd/yard-projector
// applies them without a migrations directory in the image (the same approach
// as the OLTP migrations in the top-level migrations package). Everything lives
// in the Postgres schema "analytics", written only by cmd/yard-projector under
// its own role and read (read-only) by cmd/yard-reports and, optionally, cmd/api.
package analytics

import "embed"

// Schema is the Postgres schema every analytical table lives in.
const Schema = "analytics"

// Migrations is the analytical schema: migrations/*.{up,down}.sql.
//
//go:embed migrations/*.sql
var Migrations embed.FS
