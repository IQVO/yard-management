package trailer

import (
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// SealState is the state of the seal on a trailer (ADR 0012). A seal number
// is present exactly when the state is not NONE.
type SealState string

// The seal states. INTACT is the original "present and unbroken, not yet
// verified"; the other states refine it and none replaces it.
const (
	// SealNone: no seal recorded.
	SealNone SealState = "NONE"
	// SealIntact: a seal is recorded and unbroken; nobody has compared it with
	// an expectation. Existing flows (check-in, record-seal) produce it.
	SealIntact SealState = "INTACT"
	// SealBroken: the seal was found broken; the trailer is held.
	SealBroken SealState = "BROKEN"
	// SealApplied: the seal was recorded as applied at the origin or dock.
	SealApplied SealState = "APPLIED"
	// SealVerified: checked at the gate against the expected number and equal.
	SealVerified SealState = "VERIFIED"
	// SealMismatch: checked at the gate against the expected number and
	// different (SealExceptionRaised was raised).
	SealMismatch SealState = "MISMATCH"
)

// Valid reports whether s is one of the six seal states.
func (s SealState) Valid() bool {
	switch s {
	case SealNone, SealIntact, SealBroken, SealApplied, SealVerified, SealMismatch:
		return true
	}
	return false
}

// unbroken reports whether a seal is present and not broken: the states in
// which the seal can still be verified or broken.
func (s SealState) unbroken() bool {
	return s == SealIntact || s == SealApplied || s == SealVerified || s == SealMismatch
}

// SealRecord is a seal read off a trailer (RecordSealWith).
type SealRecord struct {
	Number string
	// Class is the optional ISO 17712 class.
	Class shared.SealClass
	// State is SealIntact (the default) or SealApplied.
	State SealState
	// Minimum is the site policy floor ("" = none).
	Minimum shared.SealClass
}

// RecordSeal records the seal found on the trailer's doors as INTACT, with no
// class and no policy floor.
func (t *Trailer) RecordSeal(number string) error {
	return t.RecordSealWith(SealRecord{Number: number})
}

// RecordSealWith records a seal: INTACT (found on the doors) or APPLIED
// (put on at the origin or dock), with an optional class. A class below the
// site minimum is ErrSealClassTooLow, an unknown class ErrInvalidSealClass.
func (t *Trailer) RecordSealWith(r SealRecord) error {
	if t.state == Departed {
		return t.illegal("RecordSeal")
	}
	if err := shared.Require("seal number", r.Number, 64); err != nil {
		return err
	}
	if r.Class != "" && !r.Class.Valid() {
		return fmt.Errorf("%w: %q is not one of I, S or H", shared.ErrInvalidSealClass, r.Class)
	}
	switch r.State {
	case "":
		r.State = SealIntact
	case SealIntact, SealApplied:
	default:
		return shared.Invalid("a recorded seal is INTACT or APPLIED, not %s", r.State)
	}
	if err := shared.RequireSealClass(r.Class, r.Minimum); err != nil {
		return err
	}
	t.sealNumber, t.sealClass, t.sealState = r.Number, r.Class, r.State
	t.version++
	return nil
}

// BreakSeal marks the seal BROKEN and places a SEAL_BROKEN hold (Y-6); an
// existing hold is kept as it is. A trailer without a seal, or whose seal is
// already broken, has nothing to break.
func (t *Trailer) BreakSeal(at time.Time) error {
	if t.state == Departed || !t.sealState.unbroken() {
		return t.illegal("BreakSeal")
	}
	t.sealState = SealBroken
	t.version++
	if t.hold == "" {
		t.hold = HoldSealBroken
		t.emit(t.held(at))
	}
	return nil
}

// CheckSeal is SealCheckAtGate: it compares the recorded, unbroken seal with
// the appointment's expected seal. A match ends the seal VERIFIED. A
// difference ends it MISMATCH, raises SealExceptionRaised and reports true.
// No seal found, or no seal expected, changes nothing. It runs at the gate on
// a trailer that was not saved yet, so it does not advance the version.
func (t *Trailer) CheckSeal(expected string, at time.Time) bool {
	if expected == "" || (t.sealState != SealIntact && t.sealState != SealApplied) {
		return false
	}
	if t.sealNumber == expected {
		t.sealState = SealVerified
		return false
	}
	t.sealState = SealMismatch
	trailerID := t.id
	t.emit(SealExceptionRaised{
		Header: t.header("SealExceptionRaised", at), TrailerID: &trailerID, SiteID: t.site, AppointmentID: shared.Str(t.appointmentID),
		ExpectedSealNumber: expected, FoundSealNumber: t.sealNumber, RaisedAt: shared.Stamp(at),
	})
	return true
}

// SealState returns the state of the seal (NONE when there is none).
func (t *Trailer) SealState() SealState { return t.sealState }

// SealClass returns the ISO 17712 class of the seal ("" when none declared).
func (t *Trailer) SealClass() shared.SealClass { return t.sealClass }

// UnitScheme returns ISO6346 when the unit number was validated as an ISO 6346
// number and "" for a free-form one.
func (t *Trailer) UnitScheme() shared.UnitScheme { return t.unitScheme }

func (t *Trailer) sealStatePtr() *string {
	if t.sealState == SealNone {
		return nil
	}
	return shared.Str(string(t.sealState))
}

func (t *Trailer) sealClassPtr() *string { return shared.Str(string(t.sealClass)) }
