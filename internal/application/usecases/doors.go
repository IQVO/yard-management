package usecases

import (
	"context"
	"errors"
	"fmt"
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

// StartDoorWork begins door work on a DOCKED trailer. When the site's policy
// requireSecuringBeforeDoorWork is on, an unsecured trailer is refused
// (ErrTrailerNotSecured, ADR 0013); with it off the behaviour is unchanged.
// Loading a trailer for which the policy requires a PRE_LOAD inspection is
// refused until it has passed one (ErrInspectionRequired, ErrInspectionFailed,
// ADR 0015).
func (y *Yard) StartDoorWork(ctx context.Context, trailerID string) (views.Trailer, error) {
	return y.mutateTrailerWith(ctx, trailerID, func(ctx context.Context, tr *trailer.Trailer) error {
		pol, err := y.policyOf(ctx, tr.SiteID())
		if err != nil {
			return err
		}
		if err := tr.StartDoorWorkWith(trailer.DoorWorkRule{RequireSecured: pol.RequireSecuringBeforeDoorWork()}, y.now()); err != nil {
			return err
		}
		return y.judgeInspections(ctx, tr, pol, shared.GatePreLoad) // after the other refusals; loading only (ADR 0015)
	})
}

// SecureTrailer records that a DOCKED trailer is secured by a vehicle
// restraint or wheel chocks (ADR 0013). The same method again changes nothing;
// a trailer with an open move cannot be secured (the move would take it off the
// door).
func (y *Yard) SecureTrailer(ctx context.Context, trailerID, method, performerRef string) (views.Trailer, error) {
	return y.mutateTrailerWith(ctx, trailerID, func(ctx context.Context, tr *trailer.Trailer) error {
		if open, err := y.openMoveID(ctx, tr.ID()); err != nil {
			return err
		} else if open != "" {
			return fmt.Errorf("%w: %s must end before the trailer is secured", yardmove.ErrTrailerHasOpenMove, open)
		}
		changed, err := tr.Secure(trailer.Securing{Method: trailer.SecuringMethod(method), PerformerRef: performerRef}, y.now())
		if err == nil && !changed {
			return errUnchanged
		}
		return err
	})
}

// ClassifyTrailer sets what a trailer carries: the US DOT hazard class (1 to 9
// or a division such as 1.4), the UN number (UN and four digits) and whether
// it needs temperature control. It replaces the classification: a field left
// out is cleared, and the same facts again change nothing. An unknown class is
// ErrInvalidHazmatClass, a malformed UN number ErrInvalidUNNumber (ADR 0014).
// Classifying a parked trailer does not move it; the placement rules judge a
// placement.
func (y *Yard) ClassifyTrailer(ctx context.Context, trailerID, hazmatClass, unNumber string, requiresTemperatureControl bool) (views.Trailer, error) {
	c, err := trailer.NewClassification(hazmatClass, unNumber, requiresTemperatureControl)
	if err != nil {
		return views.Trailer{}, err
	}
	return y.mutateTrailerWith(ctx, trailerID, func(_ context.Context, tr *trailer.Trailer) error {
		changed, err := tr.Classify(c)
		if err == nil && !changed {
			return errUnchanged
		}
		return err
	})
}

// ReleaseTrailerSecuring ends the securing (ADR 0013); it is required before a
// trailer leaves its door. A DROP trailer whose door work is done then gets its
// FROM_DOOR move, which ClearDoorAfterWork withheld while it was secured.
func (y *Yard) ReleaseTrailerSecuring(ctx context.Context, trailerID string) (views.Trailer, error) {
	return y.mutateTrailerThen(ctx, trailerID, func(tr *trailer.Trailer) error { return tr.ReleaseSecuring(y.now()) }, y.clearDoorAfterWork)
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
// the policy stands down (the supervisor requests the move later), and so it
// does while the trailer is still secured (ADR 0013).
func (y *Yard) clearDoorAfterWork(ctx context.Context, tr *trailer.Trailer) error {
	if tr.Purpose() != shared.PurposeDrop || tr.State() != trailer.Docked || tr.DoorWork() != trailer.WorkDone || tr.Secured() {
		return nil // a secured trailer is not moved off its door: ReleaseTrailerSecuring asks again
	}
	if open, err := y.openMoveID(ctx, tr.ID()); err != nil || open != "" {
		return err
	}
	pol, err := y.policyOf(ctx, tr.SiteID())
	if err != nil {
		return err
	}
	spot, err := y.firstFreeSpot(ctx, tr, nil, pol.ParkingZoneIDs())
	// No spot to send it to is not a failure of the door work that just completed: the supervisor places the
	// trailer by hand (also when its hazard class has no spot at this site, ADR 0014).
	if errors.Is(err, ErrNoFreeSpot) || errors.Is(err, trailer.ErrPlacementRuleViolated) {
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

// RecordSeal records a seal number as INTACT (found on the doors) or APPLIED
// (put on at the origin or dock), with an optional ISO 17712 class, or marks
// the seal BROKEN (which places a SEAL_BROKEN hold). A class below the site's
// minimum seal class is refused (ErrSealClassTooLow); an unknown class is
// ErrInvalidSealClass.
func (y *Yard) RecordSeal(ctx context.Context, trailerID, state, number, class string) (views.Trailer, error) {
	cls, err := shared.ParseSealClass(class)
	if err != nil {
		return views.Trailer{}, err
	}
	return y.mutateTrailerWith(ctx, trailerID, func(ctx context.Context, tr *trailer.Trailer) error {
		switch state {
		case "INTACT", "APPLIED":
			pol, err := y.policyOf(ctx, tr.SiteID())
			if err != nil {
				return err
			}
			return tr.RecordSealWith(trailer.SealRecord{Number: number, Class: cls, State: trailer.SealState(state), Minimum: pol.MinimumSealClass()})
		case "BROKEN":
			if cls != "" {
				return shared.Invalid("a seal class goes with an INTACT or APPLIED seal, not a BROKEN one")
			}
			return tr.BreakSeal(y.now())
		}
		return shared.Invalid("seal state must be INTACT, APPLIED or BROKEN")
	})
}

// errUnchanged is what a command returns when it is a legitimate no-op (an
// idempotent repeat): nothing is saved or published and the view is answered.
var errUnchanged = errors.New("usecases: command changed nothing")

// mutateTrailer loads a trailer, applies one command, saves it and answers
// its view; the follow-up policies of door work run in the same unit of work.
func (y *Yard) mutateTrailer(ctx context.Context, id string, fn func(*trailer.Trailer) error) (views.Trailer, error) {
	return y.mutateTrailerThen(ctx, id, fn, nil)
}

func (y *Yard) mutateTrailerThen(ctx context.Context, id string, fn func(*trailer.Trailer) error, after func(context.Context, *trailer.Trailer) error) (views.Trailer, error) {
	return y.mutateTrailerWithThen(ctx, id, func(_ context.Context, tr *trailer.Trailer) error { return fn(tr) }, after)
}

// mutateTrailerWith is mutateTrailer for a command that needs the context of
// the unit of work (to read a policy, say).
func (y *Yard) mutateTrailerWith(ctx context.Context, id string, fn func(context.Context, *trailer.Trailer) error) (views.Trailer, error) {
	return y.mutateTrailerWithThen(ctx, id, fn, nil)
}

func (y *Yard) mutateTrailerWithThen(ctx context.Context, id string, fn func(context.Context, *trailer.Trailer) error, after func(context.Context, *trailer.Trailer) error) (views.Trailer, error) {
	var out views.Trailer
	err := y.atomically(ctx, func(ctx context.Context) error {
		tr, err := y.loadTrailer(ctx, id)
		if err != nil {
			return err
		}
		unchanged := false
		if err := fn(ctx, tr); errors.Is(err, errUnchanged) {
			unchanged = true
		} else if err != nil {
			return err
		}
		if !unchanged {
			if err := y.saveTrailer(ctx, tr); err != nil {
				return err
			}
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
