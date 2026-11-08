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

// Appointments is the pgx-backed ports.Appointments (read side) and
// ports.AppointmentFeed (write side): the AppointmentView local copy of
// dock-scheduling (ADR 0003).
type Appointments struct{ pool *pgxpool.Pool }

// NewAppointments constructs the appointment copy over pool.
func NewAppointments(pool *pgxpool.Pool) *Appointments { return &Appointments{pool: pool} }

const appointmentColumns = `appointment_id, site_id, direction, door_code, carrier_scac, window_start, window_end,
	asn_numbers, shipment_refs, expected_seal_number, arrival_tolerance_minutes, status, version`

func scanAppointment(row pgx.Row) (views.Appointment, error) {
	var (
		a          views.Appointment
		start, end *time.Time
	)
	err := row.Scan(&a.ID, &a.SiteID, &a.Direction, &a.DoorCode, &a.CarrierSCAC, &start, &end,
		&a.ASNNumbers, &a.ShipmentRefs, &a.ExpectedSeal, &a.ToleranceMinutes, &a.Status, &a.Version)
	a.WindowStart, a.WindowEnd = timeOrZero(start), timeOrZero(end)
	return a, err
}

// Get implements ports.Appointments.
func (a *Appointments) Get(ctx context.Context, id string) (views.Appointment, bool, error) {
	return getAppointment(ctx, queryFor(ctx, a.pool), id, "")
}

func getAppointment(ctx context.Context, q querier, id, lock string) (views.Appointment, bool, error) {
	v, err := scanAppointment(q.QueryRow(ctx, `SELECT `+appointmentColumns+` FROM appointments WHERE appointment_id = $1`+lock, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return views.Appointment{}, false, nil
	}
	if err != nil {
		return views.Appointment{}, false, fmt.Errorf("get appointment: %w", err)
	}
	return v, true, nil
}

// ListByDoor implements ports.Appointments: the appointments of the door in
// the given statuses by window start.
func (a *Appointments) ListByDoor(ctx context.Context, doorCode string, statuses ...string) ([]views.Appointment, error) {
	rows, err := queryFor(ctx, a.pool).Query(ctx, `
		SELECT `+appointmentColumns+` FROM appointments
		WHERE door_code = $1 AND status = ANY($2)
		ORDER BY window_start NULLS FIRST, appointment_id`, doorCode, statuses)
	if err != nil {
		return nil, fmt.Errorf("list appointments of door: %w", err)
	}
	defer rows.Close()
	var out []views.Appointment
	for rows.Next() {
		v, err := scanAppointment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan appointment: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func terminalStatus(status string) bool {
	return status == views.ApptCancelled || status == views.ApptNoShow
}

// mergeAppointment overlays the non-zero fields of in on cur; a terminal status
// is kept and the local CHECKED_IN / USED statuses survive a reschedule.
func mergeAppointment(cur, in views.Appointment) views.Appointment {
	out := cur
	out.Version = in.Version
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&out.SiteID, in.SiteID)
	set(&out.Direction, in.Direction)
	set(&out.DoorCode, in.DoorCode)
	set(&out.CarrierSCAC, in.CarrierSCAC)
	set(&out.ExpectedSeal, in.ExpectedSeal)
	if !in.WindowStart.IsZero() {
		out.WindowStart, out.WindowEnd = in.WindowStart, in.WindowEnd
	}
	if in.ASNNumbers != nil {
		out.ASNNumbers = in.ASNNumbers
	}
	if in.ShipmentRefs != nil {
		out.ShipmentRefs = in.ShipmentRefs
	}
	if in.ToleranceMinutes > 0 {
		out.ToleranceMinutes = in.ToleranceMinutes
	}
	if terminalStatus(in.Status) || (in.Status != "" && !terminalStatus(cur.Status) && in.Status != views.ApptBooked) {
		out.Status = in.Status
	}
	return out
}

// Apply implements ports.AppointmentFeed: an unknown appointment is inserted
// from the event (BOOKED when it names no status); for a known one only a
// NEWER version takes effect, under a row lock so two concurrent facts apply
// one after the other.
func (a *Appointments) Apply(ctx context.Context, in views.Appointment) error {
	return inTx(ctx, a.pool, func(q querier) error {
		for range 3 {
			done, err := applyOnce(ctx, q, in)
			if err != nil || done {
				return err
			}
			// another writer inserted the row between our read and our write: read it again
		}
		return fmt.Errorf("apply appointment %s: lost the insert race repeatedly", in.ID)
	})
}

// applyOnce reads the row under a lock and inserts or merges; done=false means
// the insert lost a race and the caller reads again.
func applyOnce(ctx context.Context, q querier, in views.Appointment) (done bool, err error) {
	cur, ok, err := getAppointment(ctx, q, in.ID, " FOR UPDATE")
	if err != nil {
		return false, err
	}
	if !ok {
		return insertAppointment(ctx, q, in)
	}
	if in.Version <= cur.Version {
		return true, nil
	}
	_, err = q.Exec(ctx, `
		UPDATE appointments SET site_id = $2, direction = $3, door_code = $4, carrier_scac = $5, window_start = $6,
			window_end = $7, asn_numbers = $8, shipment_refs = $9, expected_seal_number = $10,
			arrival_tolerance_minutes = $11, status = $12, version = $13
		WHERE appointment_id = $1`, appointmentArgs(mergeAppointment(cur, in))...)
	if err != nil {
		return false, fmt.Errorf("update appointment: %w", err)
	}
	return true, nil
}

func insertAppointment(ctx context.Context, q querier, in views.Appointment) (bool, error) {
	if in.Status == "" {
		in.Status = views.ApptBooked
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO appointments (`+appointmentColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (appointment_id) DO NOTHING`, appointmentArgs(in)...)
	if err != nil {
		return false, fmt.Errorf("insert appointment: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func appointmentArgs(a views.Appointment) []any {
	return []any{
		a.ID, a.SiteID, a.Direction, a.DoorCode, a.CarrierSCAC, nullTime(a.WindowStart), nullTime(a.WindowEnd),
		strs(a.ASNNumbers), strs(a.ShipmentRefs), a.ExpectedSeal, a.ToleranceMinutes, a.Status, a.Version,
	}
}

// MarkStatus implements ports.AppointmentFeed: it sets a local status
// (CHECKED_IN, USED) of a BOOKED or CHECKED_IN appointment; ok=false when the
// appointment is unknown or already terminal.
func (a *Appointments) MarkStatus(ctx context.Context, id, status string) (bool, error) {
	tag, err := queryFor(ctx, a.pool).Exec(ctx, `
		UPDATE appointments SET status = $2 WHERE appointment_id = $1 AND status NOT IN ('CANCELLED', 'NO_SHOW')`, id, status)
	if err != nil {
		return false, fmt.Errorf("mark appointment: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Closures is the pgx-backed ports.Closures and ports.ClosureFeed: the
// DoorClosureView local copy.
type Closures struct{ pool *pgxpool.Pool }

// NewClosures constructs the closure copy over pool.
func NewClosures(pool *pgxpool.Pool) *Closures { return &Closures{pool: pool} }

// Closed implements ports.Closures: the door is under a not-lifted closure at
// the instant.
func (c *Closures) Closed(ctx context.Context, doorCode string, at time.Time) (bool, error) {
	var closed bool
	err := queryFor(ctx, c.pool).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM door_closures
		               WHERE door_code = $1 AND NOT lifted AND closed_from <= $2 AND $2 < closed_to)`, doorCode, at.UTC()).Scan(&closed)
	if err != nil {
		return false, fmt.Errorf("check door closure: %w", err)
	}
	return closed, nil
}

// Schedule implements ports.ClosureFeed: the closure is (re)recorded and keeps
// a lifted marker it already had.
func (c *Closures) Schedule(ctx context.Context, v views.Closure) error {
	_, err := queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO door_closures (closure_id, site_id, door_code, closed_from, closed_to, lifted)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (closure_id) DO UPDATE
		SET site_id = EXCLUDED.site_id, door_code = EXCLUDED.door_code, closed_from = EXCLUDED.closed_from, closed_to = EXCLUDED.closed_to`,
		v.ID, v.SiteID, v.DoorCode, nullTime(v.From), nullTime(v.To), v.Lifted)
	if err != nil {
		return fmt.Errorf("schedule closure: %w", err)
	}
	return nil
}

// Lift implements ports.ClosureFeed: lifting an unseen closure leaves a lifted
// marker, so the Scheduled fact that arrives later does not close the door.
func (c *Closures) Lift(ctx context.Context, closureID string) error {
	_, err := queryFor(ctx, c.pool).Exec(ctx, `
		INSERT INTO door_closures (closure_id, lifted) VALUES ($1, true)
		ON CONFLICT (closure_id) DO UPDATE SET lifted = true`, closureID)
	if err != nil {
		return fmt.Errorf("lift closure: %w", err)
	}
	return nil
}
