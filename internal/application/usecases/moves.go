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

// RequestMoveInput is the body of RequestMove. A zero Reason is derived (A6):
// a door is TO_DOOR; a spot is FROM_DOOR from a door and RELOCATE otherwise.
type RequestMoveInput struct {
	TrailerID string
	To        shared.Location
	Reason    yardmove.Reason
	Band      int
	DueBy     time.Time
}

// RequestMove is the supervisor's move request: the destination is validated
// now (known active slot of the site, closure, flow, hold, free) and RESERVED
// by the open move; a trailer has at most one open move.
func (y *Yard) RequestMove(ctx context.Context, in RequestMoveInput) (views.Move, error) {
	var out views.Move
	err := y.atomically(ctx, func(ctx context.Context) error {
		tr, err := y.loadTrailer(ctx, in.TrailerID)
		if err != nil {
			return err
		}
		if err := y.requestable(ctx, tr, in.To); err != nil {
			return err
		}
		reason, band := in.Reason, in.Band
		if reason == "" {
			reason = deriveReason(tr, in.To)
		}
		if band == 0 {
			band = yardmove.DefaultBand
		}
		mv, err := y.newMove(ctx, tr, in.To, reason, yardmove.Priority{Band: band, DueBy: in.DueBy})
		if err != nil {
			return err
		}
		out = moveView(mv)
		return nil
	})
	return out, err
}

func deriveReason(tr *trailer.Trailer, to shared.Location) yardmove.Reason {
	switch {
	case to.Kind == shared.Door:
		return yardmove.ToDoor
	case tr.State() == trailer.Docked:
		return yardmove.FromDoor
	}
	return yardmove.Relocate
}

// requestable checks that tr may be sent to loc: it is on site, has no open
// move and the destination is a place it can take.
func (y *Yard) requestable(ctx context.Context, tr *trailer.Trailer, to shared.Location) error {
	if tr.State() == trailer.Departed {
		return fmt.Errorf("%w: RequestMove on a DEPARTED trailer", shared.ErrIllegalTransition)
	}
	if open, err := y.openMoveID(ctx, tr.ID()); err != nil {
		return err
	} else if open != "" {
		return fmt.Errorf("%w: %s", yardmove.ErrTrailerHasOpenMove, open)
	}
	if tr.Location() == to {
		return shared.Invalid("the trailer is already at %s", to.Code)
	}
	return y.canTake(ctx, tr, to, "")
}

// ClaimNextMove is pull dispatch: the system picks the REQUESTED hostler move
// of the site that comes first (priority band, due-by, request time) and
// leases it to the hostler for the site's lease. The trailer leaves its place
// in the same unit of work (StartMoving); leaving a door runs CallToDoor.
func (y *Yard) ClaimNextMove(ctx context.Context, siteID, hostlerRef string) (views.Move, error) {
	var out views.Move
	err := y.atomically(ctx, func(ctx context.Context) error {
		mv, err := y.claimNext(ctx, siteID, hostlerRef)
		if err != nil {
			return err
		}
		out = moveView(mv)
		return nil
	})
	return out, err
}

// claimNext picks, leases and starts the next move for the hostler.
func (y *Yard) claimNext(ctx context.Context, siteID, hostlerRef string) (*yardmove.Move, error) {
	if err := shared.Require("hostler reference", hostlerRef, 64); err != nil {
		return nil, err
	}
	switch held, err := y.d.Moves.FindClaimedByHostler(ctx, siteID, hostlerRef); {
	case err == nil:
		return nil, fmt.Errorf("%w: %s holds %s", yardmove.ErrHostlerBusy, hostlerRef, held.ID())
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	mv, err := y.d.Moves.ClaimNext(ctx, siteID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, ErrNoClaimableMove
	}
	if err != nil {
		return nil, err
	}
	pol, err := y.policyOf(ctx, siteID)
	if err != nil {
		return nil, err
	}
	now := y.now()
	if err := mv.Claim(hostlerRef, now, pol.Lease()); err != nil {
		return nil, err
	}
	if err := y.startMoving(ctx, mv, now); err != nil {
		return nil, err
	}
	return mv, y.saveMove(ctx, mv)
}

// startMoving takes the trailer of a freshly claimed move off its place.
func (y *Yard) startMoving(ctx context.Context, mv *yardmove.Move, now time.Time) error {
	tr, err := y.loadTrailer(ctx, mv.TrailerID())
	if err != nil || tr.State() == trailer.InMove {
		return err
	}
	if err := tr.StartMoving(now); err != nil {
		return err
	}
	return y.saveTrailer(ctx, tr)
}

func (y *Yard) saveMove(ctx context.Context, mv *yardmove.Move) error {
	if err := y.d.Moves.Save(ctx, mv); err != nil {
		return err
	}
	return y.publish(ctx, mv.PullEvents())
}

// CompleteMoveInput is the body of CompleteMove.
type CompleteMoveInput struct {
	MoveID     string
	HostlerRef string
	Actual     shared.Location
	Note       string
}

// CompleteMove finishes a CLAIMED move: the move and the trailer change in one
// transaction (ArriveAt at the actual location). Only the lease holder
// completes a hostler move; a gate clerk or dock operator completes a
// VISITING_DRIVER move. An occupied location is refused and the move stays
// CLAIMED.
func (y *Yard) CompleteMove(ctx context.Context, in CompleteMoveInput) (views.Move, error) {
	var out views.Move
	err := y.atomically(ctx, func(ctx context.Context) error {
		mv, err := y.d.Moves.FindByID(ctx, in.MoveID)
		if err != nil {
			return notFoundAs(err, ErrMoveNotFound)
		}
		now := y.now()
		if err := mv.Complete(yardmove.CompleteInput{Hostler: in.HostlerRef, Actual: in.Actual, Note: in.Note, At: now}); err != nil {
			return err
		}
		tr, err := y.loadTrailer(ctx, mv.TrailerID())
		if err != nil {
			return err
		}
		if err := y.arrive(ctx, tr, mv, now); err != nil {
			return err
		}
		if err := y.saveMove(ctx, mv); err != nil {
			return err
		}
		out = moveView(mv)
		return nil
	})
	return out, err
}

// arrive puts the trailer at the move's actual location after the placement
// rules and the reservation accept it.
func (y *Yard) arrive(ctx context.Context, tr *trailer.Trailer, mv *yardmove.Move, now time.Time) error {
	actual := mv.Actual()
	if err := y.d.Locks.Lock(ctx, actual.Code); err != nil {
		return err
	}
	p, err := y.placementFor(ctx, actual, now)
	if err != nil {
		return err
	}
	if actual != mv.To() {
		if other, err := y.d.Moves.FindOpenByDestination(ctx, actual); err == nil && other.ID() != mv.ID() {
			return fmt.Errorf("%w: %s is reserved by %s", yardmove.ErrDestinationReserved, actual.Code, other.ID())
		} else if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
	}
	if err := tr.ArriveAt(p, now); err != nil {
		return err
	}
	return y.saveTrailer(ctx, tr)
}

// CancelMove cancels an open move. Cancelling a CLAIMED hostler move returns
// the trailer to where it was (ReturnToOrigin) in the same unit of work.
func (y *Yard) CancelMove(ctx context.Context, moveID, reason string) (views.Move, error) {
	var out views.Move
	err := y.atomically(ctx, func(ctx context.Context) error {
		mv, err := y.d.Moves.FindByID(ctx, moveID)
		if err != nil {
			return notFoundAs(err, ErrMoveNotFound)
		}
		wasClaimed := mv.State() == yardmove.Claimed
		if err := mv.Cancel(reason, y.now()); err != nil {
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
		out = moveView(mv)
		return nil
	})
	return out, err
}

// returnTrailer puts the trailer of a hostler move that stops being CLAIMED
// back where it was. A trailer that cannot go back (its origin was taken, or
// it came from the gate) stays IN_MOVE for the supervisor to re-plan (A16).
func (y *Yard) returnTrailer(ctx context.Context, mv *yardmove.Move) error {
	tr, err := y.loadTrailer(ctx, mv.TrailerID())
	if err != nil {
		return err
	}
	origin := tr.Origin()
	if tr.State() != trailer.InMove || origin.IsZero() {
		return nil
	}
	if err := y.d.Locks.Lock(ctx, origin.Code); err != nil {
		return err
	}
	occupant := ""
	switch o, err := y.d.Trailers.FindAt(ctx, origin); {
	case err == nil:
		occupant = o.ID()
	case !errors.Is(err, ports.ErrNotFound):
		return err
	}
	switch err := tr.ReturnToOrigin(occupant, y.now()); {
	case err == nil:
		return y.saveTrailer(ctx, tr)
	case errors.Is(err, trailer.ErrLocationOccupied):
		return nil
	default:
		return err
	}
}

// SweepMoveLeases is the LeaseExpirySweep: every CLAIMED hostler move whose
// lease ran out returns to REQUESTED and its trailer to where it was. Each move
// is its own unit of work; a move that changed meanwhile is skipped.
func (y *Yard) SweepMoveLeases(ctx context.Context) (views.Sweep, error) {
	now := y.now()
	expired, err := y.d.Moves.ListExpiredClaims(ctx, now, 500)
	if err != nil {
		return views.Sweep{}, err
	}
	count := 0
	for _, candidate := range expired {
		id := candidate.ID()
		err := y.atomically(ctx, func(ctx context.Context) error {
			mv, err := y.d.Moves.FindByID(ctx, id)
			if err != nil {
				return err
			}
			if err := mv.ExpireLease(now); err != nil {
				return err
			}
			if err := y.returnTrailer(ctx, mv); err != nil {
				return err
			}
			return y.saveMove(ctx, mv)
		})
		switch {
		case err == nil:
			count++
		case errors.Is(err, ports.ErrConcurrentModification), errors.Is(err, shared.ErrIllegalTransition), errors.Is(err, yardmove.ErrLeaseNotExpired), errors.Is(err, ports.ErrNotFound):
		default:
			return views.Sweep{SweptAt: now, Count: count}, err
		}
	}
	return views.Sweep{SweptAt: now, Count: count}, nil
}
