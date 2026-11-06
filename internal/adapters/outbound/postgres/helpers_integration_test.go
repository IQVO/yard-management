//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pendingEvents lists, in publish order, the event names of the outbox rows
// (the relay deletes a row once it is published).
func pendingEvents(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := pool.Query(ctx, `SELECT payload FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan outbox: %v", err)
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("outbox payload: %v", err)
		}
		names = append(names, env.Type)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}
