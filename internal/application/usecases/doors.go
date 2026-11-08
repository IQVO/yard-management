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

// loadTrailer finds a trailer or answers ErrTrailerNotFound.
func (y *Yard) loadTrailer(ctx context.Context, id string) (*trailer.Trailer, error) {
	tr, err := y.d.Trailers.FindByID(ctx, id)
	return tr, notFoundAs(err, ErrTrailerNotFound)
}

// openMoveID returns the id of the trailer's open move ("" when none).
func (y *Yard) openMoveID(ctx context.Context, trailerID string) (string, error) {
	mv, err := y.d.Moves.FindOpenByTrailer(ctx, trailerID)
	switch {
	case err == nil:
		return mv.ID(), nil
	case errors.Is(err, ports.ErrNotFound):
		return "", nil
	}
	return "", err
}

// saveTrailer persists the trailer, hands its events to the outbox and runs
// the reactions to TrailerUndocked: the appointment is used (X4) and the door
// it left calls the next trailer in (CallToDoor).
func (y *Yard) saveTrailer(ctx context.Context, tr *trailer.Trailer) error {
	if err := y.d.Trailers.Save(ctx, tr); err != nil {
		return err
	}
	events := tr.PullEvents()
	if err := y.publish(ctx, events); err != nil {
		return err
	}
	for _, e := range events {
		if u, ok := e.(trailer.TrailerUndocked); ok {
			if err := y.afterUndock(ctx, tr, u); err != nil {
				return err
			}
		}
	}
	return nil
}

func (y *Yard) afterUndock(ctx context.Context, tr *trailer.Trailer, u trailer.TrailerUndocked) error {
	if id := tr.AppointmentID(); id != "" {
		if _, err := y.markAppointment(ctx, id, views.ApptUsed); err != nil {
			return err
		}
	}
	return y.callToDoor(ctx, tr.SiteID(), u.DoorCode)
}

// callToDoor is policy CallToDoor: when a door frees up, the next BOOKED or
// CHECKED_IN appointment of that door whose trailer is PARKED gets a HOSTLER
// move TO_DOOR, due at the window start with the band of the minutes left.
func (y *Yard) callToDoor(ctx context.Context, siteID, doorCode string) error {
	now := y.now()
	appts, err := y.d.Appointments.ListByDoor(ctx, doorCode, views.ApptBooked, views.ApptCheckedIn)
	if err != nil {
		return err
	}
	door := shared.Location{Kind: shared.Door, Code: doorCode}
	for _, a := range appts {
		tr, err := y.d.Trailers.FindByAppointment(ctx, a.ID)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && (tr.State() != trailer.Parked || tr.SiteID() != siteID)) {
			continue
		}
		if err != nil {
			return err
		}
		if open, err := y.openMoveID(ctx, tr.ID()); err != nil || open != "" {
			if err != nil {
				return err
			}
			continue
		}
		if err := y.canTake(ctx, tr, door, ""); err != nil {
			if isPlacementRefusal(err) {
				continue
			}
			return err
		}
		band := yardmove.BandForMinutes(int(a.WindowStart.Sub(now) / time.Minute))
		return y.requestHostlerMove(ctx, tr, door, yardmove.ToDoor, yardmove.Priority{Band: band, DueBy: a.WindowStart})
	}
	return nil
}

// requestHostlerMove creates and saves a REQUESTED hostler move for the
// trailer (the checks are the caller's) and hands its events to the outbox.
func (y *Yard) requestHostlerMove(ctx context.Context, tr *trailer.Trailer, to shared.Location, reason yardmove.Reason, prio yardmove.Priority) error {
	_, err := y.newMove(ctx, tr, to, reason, prio)
	return err
}

func (y *Yard) newMove(ctx context.Context, tr *trailer.Trailer, to shared.Location, reason yardmove.Reason, prio yardmove.Priority) (*yardmove.Move, error) {
	mv, err := yardmove.Request(yardmove.RequestInput{
		ID: y.d.IDs.New("mv"), SiteID: tr.SiteID(), TrailerID: tr.ID(), AppointmentID: tr.AppointmentID(), Performer: yardmove.Hostler,
		From: tr.Location(), To: to, Reason: reason, Priority: prio, At: y.now(),
	})
	if err != nil {
		return nil, err
	}
	if err := y.d.Moves.Save(ctx, mv); err != nil {
		return nil, err
	}
	return mv, y.publish(ctx, mv.PullEvents())
}

// StartDoorWork begins door work on a DOCKED trailer.
func (y *Yard) StartDoorWork(ctx context.Context, trailerID string) (views.Trailer, error) {
	return y.mutateTrailer(ctx, trailerID, func(tr *trailer.Trailer) error { return tr.StartDoorWork(y.now()) })
}

// CompleteDoorWork ends door work, recording the resulting load state (empty
// when left blank: an unload). ClearDoorAfterWork then queues a FROM_DOOR move
// for a DROP trailer; a live trailer stays until its visit checks out.
func (y *Yard) CompleteDoorWork(ctx context.Context, trailerID string, load shared.LoadState) (views.Trailer, error) {
	if load == "" {
		load = shared.LoadEmpty
	}
	return y.mutateTrailerThen(ctx, trailerID, func(tr *trailer.Trailer) error { return tr.CompleteDoorWork(load, y.now()) }, y.clearDoorAfterWork)
}

// clearDoorAfterWork is policy ClearDoorAfterWork: a DROP trailer whose door
// work is done gets a HOSTLER move FROM_DOOR to a free spot. With no free spot
// the policy stands down (the supervisor requests the move later).
func (y *Yard) clearDoorAfterWork(ctx context.Context, tr *trailer.Trailer) error {
	if tr.Purpose() != shared.PurposeDrop || tr.State() != trailer.Docked || tr.DoorWork() != trailer.WorkDone {
		return nil
	}
	if open, err := y.openMoveID(ctx, tr.ID()); err != nil || open != "" {
		return err
	}
	pol, err := y.policyOf(ctx, tr.SiteID())
	if err != nil {
		return err
	}
	spot, err := y.firstFreeSpot(ctx, tr, nil, pol.ParkingZoneIDs())
	if errors.Is(err, ErrNoFreeSpot) {
		return nil
	}
	if err != nil {
		return err
	}
	return y.requestHostlerMove(ctx, tr, spot, yardmove.FromDoor, yardmove.Priority{Band: yardmove.DefaultBand})
}

// PlaceHold puts a hold on a trailer.
func (y *Yard) PlaceHold(ctx context.Context, trailerID string, reason trailer.HoldReason) (views.Trailer, error) {
	return y.mutateTrailer(ctx, trailerID, func(tr *trailer.Trailer) error { return tr.PlaceHold(reason, y.now()) })
}

// ReleaseHold removes the hold of a trailer.
func (y *Yard) ReleaseHold(ctx context.Context, trailerID string) (views.Trailer, error) {
	return y.mutateTrailer(ctx, trailerID, func(tr *trailer.Trailer) error { return tr.ReleaseHold(y.now()) })
}

// RecordSeal records an INTACT seal number, or marks the seal BROKEN (which
// places a SEAL_BROKEN hold).
func (y *Yard) RecordSeal(ctx context.Context, trailerID, state, number string) (views.Trailer, error) {
	return y.mutateTrailer(ctx, trailerID, func(tr *trailer.Trailer) error {
		switch state {
		case "INTACT":
			return tr.RecordSeal(number)
		case "BROKEN":
			return tr.BreakSeal(y.now())
		}
		return shared.Invalid("seal state must be INTACT or BROKEN")
	})
}

// mutateTrailer loads a trailer, applies one command, saves it and answers
// its view; the follow-up policies of door work run in the same unit of work.
func (y *Yard) mutateTrailer(ctx context.Context, id string, fn func(*trailer.Trailer) error) (views.Trailer, error) {
	return y.mutateTrailerThen(ctx, id, fn, nil)
}

func (y *Yard) mutateTrailerThen(ctx context.Context, id string, fn func(*trailer.Trailer) error, after func(context.Context, *trailer.Trailer) error) (views.Trailer, error) {
	var out views.Trailer
	err := y.atomically(ctx, func(ctx context.Context) error {
		tr, err := y.loadTrailer(ctx, id)
		if err != nil {
			return err
		}
		if err := fn(tr); err != nil {
			return err
		}
		if err := y.saveTrailer(ctx, tr); err != nil {
			return err
		}
		if after != nil {
			if err := after(ctx, tr); err != nil {
				return err
			}
		}
		open, err := y.openMoveID(ctx, id)
		out = trailerView(tr, open)
		return err
	})
	return out, err
}
