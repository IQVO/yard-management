package trailer

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Errors of the dock safety interlock (ADR 0013; compare with errors.Is).
var (
	// ErrTrailerNotSecured: door work starts only on a secured trailer when
	// the site's policy asks for it, and a securing can only be released
	// when there is one (409, slug trailer-not-secured).
	ErrTrailerNotSecured = errors.New("trailer is not secured")
	// ErrTrailerStillSecured: a secured trailer is not moved away from its
	// door and does not depart before the securing is released (409, slug
	// trailer-still-secured).
	ErrTrailerStillSecured = errors.New("trailer is still secured")
)

// SecuringMethod says how a docked trailer is kept from moving: a vehicle
// restraint engaged on the rear impact guard, or the wheels chocked (OSHA 29
// CFR 1910.178(k)(1), ANSI MH30.3, EN 1398).
type SecuringMethod string

// The securing methods.
const (
	SecuredByRestraint  SecuringMethod = "RESTRAINT"
	SecuredByWheelChock SecuringMethod = "WHEEL_CHOCKS"
)

// Valid reports whether m is one of the two methods (the empty method, "not
// secured", is not a method).
func (m SecuringMethod) Valid() bool { return m == SecuredByRestraint || m == SecuredByWheelChock }

// Securing is a SecureTrailer command: how, and optionally who.
type Securing struct {
	Method SecuringMethod
	// PerformerRef is the optional reference of the person who secured the
	// trailer (a dock operator or hostler id).
	PerformerRef string
}

// DoorWorkRule is the site policy StartDoorWorkWith applies.
type DoorWorkRule struct {
	// RequireSecured refuses to start door work on a trailer that is not
	// secured (policy requireSecuringBeforeDoorWork).
	RequireSecured bool
}

// Secure records that the DOCKED trailer is secured by r.Method and raises
// TrailerSecured. It is idempotent for the same method: the trailer is left
// as it is, nothing is raised and changed is false. A different method
// while secured is an illegal transition (release first); a trailer that is
// not DOCKED cannot be secured.
func (t *Trailer) Secure(r Securing, at time.Time) (changed bool, err error) {
	if t.state != Docked {
		return false, t.illegal("Secure")
	}
	if !r.Method.Valid() {
		return false, shared.Invalid("securing method must be RESTRAINT or WHEEL_CHOCKS")
	}
	if err := shared.Check("performer reference", r.PerformerRef, 64); err != nil {
		return false, err
	}
	if t.securedBy != "" {
		if t.securedBy == r.Method {
			return false, nil
		}
		return false, fmt.Errorf("%w: Secure with %s while secured by %s; release the securing first", shared.ErrIllegalTransition, r.Method, t.securedBy)
	}
	t.securedBy, t.securedAt, t.securerRef = r.Method, at.UTC(), r.PerformerRef
	t.version++
	t.emit(TrailerSecured{
		Header: t.header("TrailerSecured", at), TrailerID: t.id, SiteID: t.site, DoorCode: t.loc.Code,
		Method: r.Method, SecuredAt: shared.Stamp(at), Version: t.version,
	})
	return true, nil
}

// ReleaseSecuring ends the securing and raises TrailerSecuringReleased. A
// trailer that is not secured has nothing to release (ErrTrailerNotSecured),
// and the securing is kept while door work is IN_PROGRESS (ErrDoorWorkOpen):
// loading or unloading never continues on a trailer released from its
// restraint.
func (t *Trailer) ReleaseSecuring(at time.Time) error {
	if t.securedBy == "" {
		return fmt.Errorf("%w: there is no securing to release", ErrTrailerNotSecured)
	}
	if t.work == WorkInProgress {
		return fmt.Errorf("%w: the securing stays while door work is in progress", ErrDoorWorkOpen)
	}
	t.securedBy, t.securedAt, t.securerRef = "", time.Time{}, ""
	t.version++
	t.emit(TrailerSecuringReleased{
		Header: t.header("TrailerSecuringReleased", at), TrailerID: t.id, SiteID: t.site, DoorCode: t.loc.Code,
		ReleasedAt: shared.Stamp(at), Version: t.version,
	})
	return nil
}

// checkReleasedFromDoor refuses to take a secured trailer away from its door.
func (t *Trailer) checkReleasedFromDoor() error {
	if t.state == Docked && t.securedBy != "" {
		return fmt.Errorf("%w: secured by %s at door %s", ErrTrailerStillSecured, t.securedBy, t.loc.Code)
	}
	return nil
}

// CanLeaveDoor reports ErrTrailerStillSecured while a DOCKED trailer is
// secured. The use case asks it when a move off a door is requested, so the
// supervisor hears the refusal at request time and not at the claim.
func (t *Trailer) CanLeaveDoor() error { return t.checkReleasedFromDoor() }

// Secured reports whether the trailer is secured now.
func (t *Trailer) Secured() bool { return t.securedBy != "" }

// SecuredBy returns the method the trailer is secured with ("" when it is
// not).
func (t *Trailer) SecuredBy() SecuringMethod { return t.securedBy }

// SecuredAt returns when the trailer was secured (zero when it is not).
func (t *Trailer) SecuredAt() time.Time { return t.securedAt }
