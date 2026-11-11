package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
)

// consume runs one consumed CloudEvent's effect in a unit of work that first
// claims the event id (the inbox): a duplicate is skipped, a rollback leaves
// the claim undone so the redelivery is processed.
func (y *Yard) consume(ctx context.Context, eventID string, fn func(ctx context.Context) error) error {
	return y.atomically(ctx, func(ctx context.Context) error {
		first, err := y.d.Processed.Claim(ctx, eventID)
		if err != nil || !first {
			return err
		}
		return fn(ctx)
	})
}

// ApplyZone folds ZoneRegistered into the location copy.
func (y *Yard) ApplyZone(ctx context.Context, eventID, zoneID, siteCode string) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error { return y.d.LocationFeed.ApplyZone(ctx, zoneID, siteCode) })
}

// ApplySlot folds LocationSlotRegistered into the location copy. Role Yard is
// a spot and role Dock a door; every other role is not the yard's business.
// spotKind is the facility-layout spotKind of a Yard slot (PARKING, HOLDING,
// REEFER_PLUG, HAZMAT, STAGING_LANE); an absent or unknown value is stored as
// "no kind", so a legacy spot is judged as it always was (ADR 0014).
func (y *Yard) ApplySlot(ctx context.Context, eventID, code, zoneID, role, dockFlow, spotKind string) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error {
		switch role {
		case "Yard":
			return y.d.LocationFeed.ApplySlot(ctx, code, zoneID, shared.Spot, "", shared.ParseSpotKind(spotKind))
		case "Dock":
			return y.d.LocationFeed.ApplySlot(ctx, code, zoneID, shared.Door, dockFlow, shared.SpotKindUnknown)
		}
		return nil
	})
}

// DecommissionSlot folds LocationSlotDecommissioned.
func (y *Yard) DecommissionSlot(ctx context.Context, eventID, code string) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error { return y.d.LocationFeed.Decommission(ctx, code) })
}

// AppointmentFact is a dock-scheduling appointment event, decoded. Status is
// BOOKED, CANCELLED, NO_SHOW or "" (a reschedule); PreviousDoorCode is set by
// a reschedule.
type AppointmentFact struct {
	Appointment      views.Appointment
	PreviousDoorCode string
}

// ApplyAppointment folds Booked / Rescheduled / Cancelled / NoShow into the
// appointment copy, then runs NoShowFreesHolding when the appointment was
// cancelled, missed, or moved to another door.
func (y *Yard) ApplyAppointment(ctx context.Context, eventID string, f AppointmentFact) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error {
		if err := y.d.ApptFeed.Apply(ctx, f.Appointment); err != nil {
			return err
		}
		a := f.Appointment
		moved := f.PreviousDoorCode != "" && f.PreviousDoorCode != a.DoorCode
		if a.Status == views.ApptCancelled || a.Status == views.ApptNoShow || moved {
			return y.freeHolding(ctx, a.ID)
		}
		return nil
	})
}

// freeHolding is policy NoShowFreesHolding: the open hostler TO_DOOR moves of
// the appointment are cancelled (a CLAIMED one returns its trailer). A trailer
// already on site stays where it is; the supervisor decides.
func (y *Yard) freeHolding(ctx context.Context, appointmentID string) error {
	moves, err := y.d.Moves.ListOpenByAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	for _, mv := range moves {
		if mv.Performer() != yardmove.Hostler || mv.Reason() != yardmove.ToDoor {
			continue
		}
		wasClaimed := mv.State() == yardmove.Claimed
		if err := mv.Cancel("appointment cancelled, missed or moved", y.now()); err != nil {
			return err
		}
		if wasClaimed {
			if err := y.returnTrailer(ctx, mv); err != nil {
				return err
			}
		}
		if err := y.saveMove(ctx, mv); err != nil {
			return err
		}
	}
	return nil
}

// ScheduleClosure folds DoorClosureScheduled.
func (y *Yard) ScheduleClosure(ctx context.Context, eventID string, c views.Closure) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error { return y.d.ClosureFeed.Schedule(ctx, c) })
}

// LiftClosure folds DoorClosureLifted.
func (y *Yard) LiftClosure(ctx context.Context, eventID, closureID string) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error { return y.d.ClosureFeed.Lift(ctx, closureID) })
}

// RegisterAsn folds ASNRegistered.
func (y *Yard) RegisterAsn(ctx context.Context, eventID, asnNumber string) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error { return y.d.AsnFeed.Register(ctx, asnNumber) })
}

// CancelAsn folds ASNCancelled.
func (y *Yard) CancelAsn(ctx context.Context, eventID, asnNumber string) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error { return y.d.AsnFeed.Cancel(ctx, asnNumber) })
}

// OpenReceipt folds ReceiptOpened into the receipt copy.
func (y *Yard) OpenReceipt(ctx context.Context, eventID string, r views.Receipt) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error { return y.d.ReceiptFeed.Open(ctx, r) })
}

// CloseReceipt folds ReceiptClosed into the receipt copy, then runs
// AutoCompleteUnload for the appointment the receipt was opened on.
func (y *Yard) CloseReceipt(ctx context.Context, eventID, receiptID, asnNumber string, closedAt time.Time) error {
	return y.consume(ctx, eventID, func(ctx context.Context) error {
		if err := y.d.ReceiptFeed.Close(ctx, receiptID, asnNumber, closedAt); err != nil {
			return err
		}
		r, ok, err := y.d.Receipts.Get(ctx, receiptID)
		if err != nil || !ok || r.AppointmentID == "" {
			return err
		}
		return y.autoCompleteUnload(ctx, r.AppointmentID)
	})
}

// autoCompleteUnload is policy AutoCompleteUnload (Y-7): when the trailer of
// the appointment is DOCKED and EVERY ASN on it has a closed receipt, its door
// work completes (WAITING or IN_PROGRESS to DONE) and it is EMPTY. A held
// trailer or one already done is left alone.
func (y *Yard) autoCompleteUnload(ctx context.Context, appointmentID string) error {
	tr, err := y.d.Trailers.FindByAppointment(ctx, appointmentID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	asns := tr.ASNNumbers()
	if tr.State() != trailer.Docked || tr.DoorWork() == trailer.WorkDone || len(asns) == 0 {
		return nil
	}
	closed, err := y.d.Receipts.ClosedASNs(ctx, appointmentID)
	if err != nil || !containsAll(closed, asns) {
		return err
	}
	now := y.now()
	if tr.DoorWork() == trailer.WorkWaiting {
		if err := tr.StartDoorWork(now); err != nil {
			return ignoreHold(err)
		}
		// One save per accepted change: the version guard persists exactly one
		// transition at a time.
		if err := y.saveTrailer(ctx, tr); err != nil {
			return err
		}
	}
	if err := tr.CompleteDoorWork(shared.LoadEmpty, now); err != nil {
		return err
	}
	if err := y.saveTrailer(ctx, tr); err != nil {
		return err
	}
	return y.clearDoorAfterWork(ctx, tr)
}

func ignoreHold(err error) error {
	if errors.Is(err, trailer.ErrTrailerOnHold) {
		return nil
	}
	return err
}

func containsAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
