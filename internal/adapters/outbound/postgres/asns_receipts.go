package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/yard-management/internal/application/views"
)

// Asns is the pgx-backed ports.Asns and ports.AsnFeed: the AsnContentView local
// copy of inbound-receiving.
type Asns struct{ pool *pgxpool.Pool }

// NewAsns constructs the ASN copy over pool.
func NewAsns(pool *pgxpool.Pool) *Asns { return &Asns{pool: pool} }

// FilterActive implements ports.Asns: the numbers that are not cancelled, in order.
func (a *Asns) FilterActive(ctx context.Context, numbers []string) ([]string, error) {
	rows, err := queryFor(ctx, a.pool).Query(ctx, `
		SELECT u.n FROM unnest($1::text[]) WITH ORDINALITY AS u(n, ord)
		WHERE NOT EXISTS (SELECT 1 FROM asns WHERE asn_number = u.n AND cancelled)
		ORDER BY u.ord`, strs(numbers))
	if err != nil {
		return nil, fmt.Errorf("filter active asns: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan asn: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Register implements ports.AsnFeed: a cancelled ASN stays cancelled.
func (a *Asns) Register(ctx context.Context, asnNumber string) error {
	if _, err := queryFor(ctx, a.pool).Exec(ctx, `INSERT INTO asns (asn_number) VALUES ($1) ON CONFLICT (asn_number) DO NOTHING`, asnNumber); err != nil {
		return fmt.Errorf("register asn: %w", err)
	}
	return nil
}

// Cancel implements ports.AsnFeed.
func (a *Asns) Cancel(ctx context.Context, asnNumber string) error {
	if _, err := queryFor(ctx, a.pool).Exec(ctx, `
		INSERT INTO asns (asn_number, cancelled) VALUES ($1, true)
		ON CONFLICT (asn_number) DO UPDATE SET cancelled = true`, asnNumber); err != nil {
		return fmt.Errorf("cancel asn: %w", err)
	}
	return nil
}

// Receipts is the pgx-backed ports.Receipts and ports.ReceiptFeed: the
// ReceiptAtDoorView local copy of inbound-receiving.
type Receipts struct{ pool *pgxpool.Pool }

// NewReceipts constructs the receipt copy over pool.
func NewReceipts(pool *pgxpool.Pool) *Receipts { return &Receipts{pool: pool} }

// Get implements ports.Receipts.
func (r *Receipts) Get(ctx context.Context, receiptID string) (views.Receipt, bool, error) {
	var (
		v      views.Receipt
		closed *time.Time
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT receipt_id, asn_number, appointment_id, door_code, closed_at FROM receipts WHERE receipt_id = $1`, receiptID).
		Scan(&v.ID, &v.ASNNumber, &v.AppointmentID, &v.DoorCode, &closed)
	if errors.Is(err, pgx.ErrNoRows) {
		return views.Receipt{}, false, nil
	}
	if err != nil {
		return views.Receipt{}, false, fmt.Errorf("get receipt: %w", err)
	}
	v.ClosedAt = timeOrZero(closed)
	return v, true, nil
}

// ClosedASNs implements ports.Receipts: the ASN numbers whose receipt of the
// appointment is closed, sorted.
func (r *Receipts) ClosedASNs(ctx context.Context, appointmentID string) ([]string, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT asn_number FROM receipts
		WHERE appointment_id = $1 AND appointment_id <> '' AND closed_at IS NOT NULL
		ORDER BY asn_number`, appointmentID)
	if err != nil {
		return nil, fmt.Errorf("list closed asns: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan asn: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Open implements ports.ReceiptFeed: a receipt already closed stays closed.
func (r *Receipts) Open(ctx context.Context, v views.Receipt) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO receipts (receipt_id, asn_number, appointment_id, door_code, closed_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (receipt_id) DO UPDATE
		SET asn_number = EXCLUDED.asn_number, appointment_id = EXCLUDED.appointment_id, door_code = EXCLUDED.door_code`,
		v.ID, v.ASNNumber, v.AppointmentID, v.DoorCode, nullTime(v.ClosedAt))
	if err != nil {
		return fmt.Errorf("open receipt: %w", err)
	}
	return nil
}

// Close implements ports.ReceiptFeed: closing an unseen receipt leaves a closed
// marker that a later Opened fact keeps.
func (r *Receipts) Close(ctx context.Context, receiptID, asnNumber string, closedAt time.Time) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO receipts (receipt_id, asn_number, closed_at) VALUES ($1, $2, $3)
		ON CONFLICT (receipt_id) DO UPDATE
		SET closed_at = EXCLUDED.closed_at,
		    asn_number = CASE WHEN receipts.asn_number = '' THEN EXCLUDED.asn_number ELSE receipts.asn_number END`,
		receiptID, asnNumber, closedAt.UTC())
	if err != nil {
		return fmt.Errorf("close receipt: %w", err)
	}
	return nil
}
