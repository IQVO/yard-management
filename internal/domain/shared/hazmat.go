package shared

import (
	"errors"
	"fmt"
)

// Errors of the contents classification (compare with errors.Is).
var (
	// ErrInvalidHazmatClass: the hazard class is not a US DOT class or
	// division (422, slug invalid-hazmat-class; ADR 0014).
	ErrInvalidHazmatClass = errors.New("invalid hazmat class")
	// ErrInvalidUNNumber: the UN number is not "UN" and four digits (422,
	// slug invalid-un-number; ADR 0014).
	ErrInvalidUNNumber = errors.New("invalid UN number")
)

// HazmatClass is the US DOT hazard class of what a trailer carries (49 CFR
// 172.101, 173.2): the class "1" to "9", or a division of a class that has
// them (1.1 to 1.6, 2.1 to 2.3, 4.1 to 4.3, 5.1 and 5.2, 6.1 and 6.2). The
// empty class means the contents are not declared hazardous.
type HazmatClass string

// hazmatClasses is every accepted designation: the nine classes and the
// divisions of classes 1, 2, 4, 5 and 6. Class 3, 7, 8 and 9 have none.
var hazmatClasses = map[HazmatClass]bool{
	"1": true, "1.1": true, "1.2": true, "1.3": true, "1.4": true, "1.5": true, "1.6": true,
	"2": true, "2.1": true, "2.2": true, "2.3": true,
	"3": true,
	"4": true, "4.1": true, "4.2": true, "4.3": true,
	"5": true, "5.1": true, "5.2": true,
	"6": true, "6.1": true, "6.2": true,
	"7": true, "8": true, "9": true,
}

// Valid reports whether c is a hazard class or division (the empty class is
// not one).
func (c HazmatClass) Valid() bool { return hazmatClasses[c] }

// ParseHazmatClass accepts the empty string (not hazardous) or a class or
// division exactly as written ("3", "1.4"); anything else, including a
// padded, a lower-case or a compatibility-group form ("1.4S"), is
// ErrInvalidHazmatClass. The value is not normalised.
func ParseHazmatClass(s string) (HazmatClass, error) {
	c := HazmatClass(s)
	if c != "" && !c.Valid() {
		return "", fmt.Errorf("%w: %q is not a US DOT hazard class (1 to 9) or division (such as 1.4, 2.1, 4.3, 6.1)", ErrInvalidHazmatClass, s)
	}
	return c, nil
}

// UNNumber is the four-digit United Nations number of a hazardous material,
// written "UN" and four digits ("UN1203"). The empty number means none was
// declared.
type UNNumber string

// ParseUNNumber accepts the empty string or "UN" followed by exactly four
// digits; anything else, including lower case and a bare number, is
// ErrInvalidUNNumber. The value is not normalised, so the number stored is
// the one the shipper sent.
func ParseUNNumber(s string) (UNNumber, error) {
	if s == "" {
		return "", nil
	}
	if len(s) != 6 || s[:2] != "UN" {
		return "", fmt.Errorf("%w: %q is not UN and four digits (for example UN1203)", ErrInvalidUNNumber, s)
	}
	for _, r := range s[2:] {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("%w: %q is not UN and four digits (for example UN1203)", ErrInvalidUNNumber, s)
		}
	}
	return UNNumber(s), nil
}
