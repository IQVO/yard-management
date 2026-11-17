package shared

import (
	"errors"
	"fmt"
)

// Errors of the seal class rules (compare with errors.Is).
var (
	// ErrInvalidSealClass: the seal class is not I, S or H (422, slug
	// invalid-seal-class; ADR 0012).
	ErrInvalidSealClass = errors.New("invalid seal class")
	// ErrSealClassTooLow: the seal is of a lower class than the site policy
	// requires, or has no class at all (409, slug seal-class-too-low).
	ErrSealClassTooLow = errors.New("seal class is too low")
)

// SealClass is the ISO 17712 class of a mechanical seal: I indicative, S
// security, H high security. The empty class means none was declared.
type SealClass string

// The seal classes, from the weakest to the strongest.
const (
	SealClassIndicative   SealClass = "I"
	SealClassSecurity     SealClass = "S"
	SealClassHighSecurity SealClass = "H"
)

// Valid reports whether c is one of the three classes (the empty class is
// not a class).
func (c SealClass) Valid() bool { return c.rank() > 0 }

func (c SealClass) rank() int {
	switch c {
	case SealClassIndicative:
		return 1
	case SealClassSecurity:
		return 2
	case SealClassHighSecurity:
		return 3
	}
	return 0
}

// ParseSealClass accepts the empty string (no class declared) or one of I, S
// and H; anything else is ErrInvalidSealClass.
func ParseSealClass(s string) (SealClass, error) {
	c := SealClass(s)
	if c != "" && !c.Valid() {
		return "", fmt.Errorf("%w: %q is not one of I, S or H", ErrInvalidSealClass, s)
	}
	return c, nil
}

// Satisfies reports whether a seal of class c meets the minimum: no minimum
// is always met, a seal without a class never meets one, otherwise the class
// must be at least as strong (I below S below H).
func (c SealClass) Satisfies(minimum SealClass) bool {
	return minimum == "" || c.rank() >= minimum.rank() && c != ""
}

// RequireSealClass is nil when a seal of class c meets the minimum and
// ErrSealClassTooLow otherwise.
func RequireSealClass(c, minimum SealClass) error {
	if c.Satisfies(minimum) {
		return nil
	}
	if c == "" {
		return fmt.Errorf("%w: the site requires class %s and the seal has none", ErrSealClassTooLow, minimum)
	}
	return fmt.Errorf("%w: the site requires class %s and the seal is class %s", ErrSealClassTooLow, minimum, c)
}
