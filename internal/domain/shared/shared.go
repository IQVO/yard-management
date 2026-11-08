// Package shared holds the tiny value rules every domain package of this
// context agrees on: the typed errors of the model that no single aggregate
// owns, validated Locations, the TrailerKind / LoadState / Purpose
// enumerations, SCAC codes and the domain Event envelope (ADR 0002). It
// imports nothing but the standard library.
package shared

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Errors every aggregate shares (compare with errors.Is).
var (
	// ErrValidation: a value or reference rule failed (422 validation).
	ErrValidation = errors.New("validation failed")
	// ErrIllegalTransition: the state machine has no such transition (409).
	ErrIllegalTransition = errors.New("illegal transition")
	// ErrInvalidCarrier: the carrier name or SCAC is malformed (422).
	ErrInvalidCarrier = errors.New("invalid carrier")
	// ErrUnknownLocation: the location is not a known, active Yard or Dock
	// slot of the site (422).
	ErrUnknownLocation = errors.New("unknown location")
)

// Invalid wraps ErrValidation with what failed.
func Invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// IsBlank reports whether s is empty or only white space.
func IsBlank(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsSpace(r) }) < 0
}

// Require returns ErrValidation when v is blank, longer than max (when max
// is positive) or holds a NUL byte, which Postgres rejects.
func Require(name, v string, max int) error {
	if IsBlank(v) {
		return Invalid("%s must not be blank", name)
	}
	return Check(name, v, max)
}

// Check is Require without the blank rule: an empty optional value passes.
func Check(name, v string, max int) error {
	if strings.ContainsRune(v, 0) {
		return Invalid("%s must not contain a NUL byte", name)
	}
	if max > 0 && len([]rune(v)) > max {
		return Invalid("%s must be at most %d characters", name, max)
	}
	return nil
}

// ValidateSCAC accepts the empty string (no scac) or 2..4 upper-case letters.
func ValidateSCAC(s string) error {
	if s == "" {
		return nil
	}
	if len(s) < 2 || len(s) > 4 {
		return fmt.Errorf("%w: scac must be 2..4 upper-case letters", ErrInvalidCarrier)
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return fmt.Errorf("%w: scac must be 2..4 upper-case letters", ErrInvalidCarrier)
		}
	}
	return nil
}

// TrailerKind is the kind of a towable unit.
type TrailerKind string

// The trailer kinds.
const (
	KindDryVan    TrailerKind = "DRY_VAN"
	KindReefer    TrailerKind = "REEFER"
	KindFlatbed   TrailerKind = "FLATBED"
	KindContainer TrailerKind = "CONTAINER"
)

// TrailerKinds are every valid kind, in order.
var TrailerKinds = []TrailerKind{KindDryVan, KindReefer, KindFlatbed, KindContainer}

// Valid reports whether k is one of the four kinds.
func (k TrailerKind) Valid() bool {
	switch k {
	case KindDryVan, KindReefer, KindFlatbed, KindContainer:
		return true
	}
	return false
}

// LoadState says whether a trailer holds goods.
type LoadState string

// The load states.
const (
	LoadEmpty   LoadState = "EMPTY"
	LoadLoaded  LoadState = "LOADED"
	LoadPartial LoadState = "PARTIAL"
)

// Valid reports whether l is a load state.
func (l LoadState) Valid() bool { return l == LoadEmpty || l == LoadLoaded || l == LoadPartial }

// Purpose is why a visit came.
type Purpose string

// The visit purposes.
const (
	PurposeDrop       Purpose = "DROP"
	PurposeLiveUnload Purpose = "LIVE_UNLOAD"
	PurposeLiveLoad   Purpose = "LIVE_LOAD"
	PurposePickup     Purpose = "PICKUP"
	PurposeBobtail    Purpose = "BOBTAIL"
)

// Valid reports whether p is a purpose.
func (p Purpose) Valid() bool {
	switch p {
	case PurposeDrop, PurposeLiveUnload, PurposeLiveLoad, PurposePickup, PurposeBobtail:
		return true
	}
	return false
}

// Outbound reports whether the purpose takes goods out (LIVE_LOAD, PICKUP).
func (p Purpose) Outbound() bool { return p == PurposeLiveLoad || p == PurposePickup }

// LocationKind is SPOT (a Yard slot) or DOOR (a Dock slot).
type LocationKind string

// The location kinds.
const (
	Spot LocationKind = "SPOT"
	Door LocationKind = "DOOR"
)

// Location is a place a trailer can be: a spot or a door, by locationCode.
type Location struct {
	Kind LocationKind `json:"kind"`
	Code string       `json:"code"`
}

// NewLocation validates and returns a Location.
func NewLocation(kind LocationKind, code string) (Location, error) {
	if kind != Spot && kind != Door {
		return Location{}, Invalid("location kind must be SPOT or DOOR")
	}
	if err := Require("location code", code, 128); err != nil {
		return Location{}, err
	}
	return Location{Kind: kind, Code: code}, nil
}

// IsZero reports whether the Location holds nothing.
func (l Location) IsZero() bool { return l.Kind == "" && l.Code == "" }

// Ptr returns nil for a zero Location, a pointer to it otherwise (nullable
// event fields).
func (l Location) Ptr() *Location {
	if l.IsZero() {
		return nil
	}
	return &l
}
