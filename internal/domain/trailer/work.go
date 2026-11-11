package trailer

import (
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// StartDoorWork begins unloading or loading on a DOCKED trailer (WAITING to
// IN_PROGRESS). A held trailer's door work is blocked until the release.
func (t *Trailer) StartDoorWork(at time.Time) error {
	if t.state != Docked || t.work != WorkWaiting {
		return t.illegal("StartDoorWork")
	}
	if t.hold != "" {
		return fmt.Errorf("%w: %s", ErrTrailerOnHold, t.hold)
	}
	t.work = WorkInProgress
	t.version++
	t.emit(DoorWorkStarted{
		Header: t.header("DoorWorkStarted", at), TrailerID: t.id, SiteID: t.site, DoorCode: t.loc.Code,
		AppointmentID: shared.Str(t.appointmentID), StartedAt: shared.Stamp(at), Version: t.version,
	})
	return nil
}

// CompleteDoorWork ends door work (IN_PROGRESS to DONE) and records the
// resulting load state (EMPTY after an unload).
func (t *Trailer) CompleteDoorWork(load shared.LoadState, at time.Time) error {
	if t.state != Docked || t.work != WorkInProgress {
		return t.illegal("CompleteDoorWork")
	}
	if !load.Valid() {
		return shared.Invalid("load state %q is not valid", load)
	}
	t.work, t.load = WorkDone, load
	t.version++
	t.emit(DoorWorkCompleted{
		Header: t.header("DoorWorkCompleted", at), TrailerID: t.id, SiteID: t.site, DoorCode: t.loc.Code,
		AppointmentID: shared.Str(t.appointmentID), CompletedAt: shared.Stamp(at), LoadState: load, Version: t.version,
	})
	return nil
}

// RecordLoadState records what the trailer holds now.
func (t *Trailer) RecordLoadState(load shared.LoadState) error {
	if t.state == Departed {
		return t.illegal("RecordLoadState")
	}
	if !load.Valid() {
		return shared.Invalid("load state %q is not valid", load)
	}
	t.load = load
	t.version++
	return nil
}

func (t *Trailer) held(at time.Time) TrailerHeld {
	return TrailerHeld{Header: t.header("TrailerHeld", at), TrailerID: t.id, SiteID: t.site, Reason: t.hold, Version: t.version}
}

// PlaceHold puts the trailer on hold. A DOCKED trailer may be held (its door
// work then waits for the release); a second hold is refused.
func (t *Trailer) PlaceHold(reason HoldReason, at time.Time) error {
	if t.state == Departed {
		return t.illegal("PlaceHold")
	}
	if !reason.Valid() {
		return shared.Invalid("hold reason %q is not valid", reason)
	}
	if t.hold != "" {
		return fmt.Errorf("%w: already held for %s", ErrTrailerOnHold, t.hold)
	}
	t.hold = reason
	t.version++
	t.emit(t.held(at))
	return nil
}

// ReleaseHold removes the hold.
func (t *Trailer) ReleaseHold(at time.Time) error {
	if t.hold == "" {
		return ErrTrailerNotOnHold
	}
	t.hold = ""
	t.version++
	t.emit(TrailerHoldReleased{Header: t.header("TrailerHoldReleased", at), TrailerID: t.id, SiteID: t.site, Version: t.version})
	return nil
}

// DwellMinutes is the time on site so far (arrival to departure once gone).
func (t *Trailer) DwellMinutes(now time.Time) int64 {
	end := now
	if t.state == Departed {
		end = t.departedAt
	}
	return max(int64(end.Sub(t.arrivedAt)/time.Minute), 0)
}

// SweepDwell is the Trailer side of DwellSweep: once the dwell reaches the
// threshold it raises TrailerDwellExceeded, once per trailer, and reports
// true. A departed trailer, a non-positive threshold or an already alerted
// trailer raises nothing.
func (t *Trailer) SweepDwell(now time.Time, thresholdMinutes int64) bool {
	if t.state == Departed || thresholdMinutes < 1 || !t.dwellAlertedAt.IsZero() {
		return false
	}
	dwell := t.DwellMinutes(now)
	if dwell < thresholdMinutes {
		return false
	}
	t.dwellAlertedAt = now.UTC()
	t.version++
	t.emit(TrailerDwellExceeded{
		Header: t.header("TrailerDwellExceeded", now), TrailerID: t.id, SiteID: t.site, Kind: t.kind,
		DwellMinutes: dwell, ThresholdMinutes: thresholdMinutes, LocationCode: shared.Str(t.loc.Code),
	})
	return true
}
