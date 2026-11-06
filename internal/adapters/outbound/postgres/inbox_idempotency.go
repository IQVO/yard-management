package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/views"
)

// ProcessedEvents is the pgx-backed ports.ProcessedEvents: the inbound
// consumers' dedupe guard over the processed_events table.
type ProcessedEvents struct {
	pool *pgxpool.Pool
}

// NewProcessedEvents constructs a ProcessedEvents over pool.
func NewProcessedEvents(pool *pgxpool.Pool) *ProcessedEvents { return &ProcessedEvents{pool: pool} }

// Claim records eventID as processed, returning false when an earlier call
// already recorded it. Called inside a ports.UnitOfWork (the consumers' case)
// the INSERT joins the SAME transaction as the fact's effects, so a rollback
// un-claims it and a redelivery is processed, not skipped. A concurrent claim
// of the same id blocks on the primary key until the other transaction ends.
func (r *ProcessedEvents) Claim(ctx context.Context, eventID string) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO processed_events (event_id) VALUES ($1)
		ON CONFLICT (event_id) DO NOTHING
	`, eventID)
	if err != nil {
		return false, fmt.Errorf("claim processed event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// IdempotencyStore is the pgx-backed ports.IdempotencyStore over the
// idempotency_keys table. Every statement runs on the transaction carried by
// ctx when one is open and on the pool otherwise.
type IdempotencyStore struct {
	pool *pgxpool.Pool
}

// NewIdempotencyStore constructs an IdempotencyStore over pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

// Lookup implements ports.IdempotencyStore.
func (s *IdempotencyStore) Lookup(ctx context.Context, key string) (views.IdempotencyRecord, bool, error) {
	rec := views.IdempotencyRecord{Key: key}
	err := queryFor(ctx, s.pool).QueryRow(ctx, `
		SELECT fingerprint, status, content_type, body, created_at
		FROM idempotency_keys WHERE key = $1
	`, key).Scan(&rec.Fingerprint, &rec.Status, &rec.ContentType, &rec.Body, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return views.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return views.IdempotencyRecord{}, false, fmt.Errorf("lookup idempotency key: %w", err)
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, true, nil
}

// Save implements ports.IdempotencyStore: the first writer of a key wins and a
// later Save of the same key changes nothing. A concurrent Save of the same
// key blocks on the primary key until the other transaction ends, so a
// post-save re-read sees the committed winner.
func (s *IdempotencyStore) Save(ctx context.Context, rec views.IdempotencyRecord) error {
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	_, err := queryFor(ctx, s.pool).Exec(ctx, `
		INSERT INTO idempotency_keys (key, fingerprint, status, content_type, body, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO NOTHING
	`, rec.Key, rec.Fingerprint, rec.Status, rec.ContentType, body, rec.CreatedAt.UTC())
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}
