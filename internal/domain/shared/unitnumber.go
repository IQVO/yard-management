package shared

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidUnitNumber: an ISO 6346 unit number is malformed or its check
// digit is wrong (422, slug invalid-unit-number; ADR 0012).
var ErrInvalidUnitNumber = errors.New("invalid unit number")

// UnitScheme names the numbering scheme a unit number is validated against.
// The empty scheme is a free-form number: domestic road trailers keep any
// text the carrier paints on them (ADR 0012).
type UnitScheme string

// SchemeISO6346 is the ISO 6346 freight container identification.
const SchemeISO6346 UnitScheme = "ISO6346"

// Valid reports whether s is a scheme a caller may declare (the empty scheme
// means "not declared").
func (s UnitScheme) Valid() bool { return s == "" || s == SchemeISO6346 }

// UnitNumber is a unit number together with the scheme it was validated
// against. Build one with NewUnitNumber.
type UnitNumber struct {
	value  string
	scheme UnitScheme
}

// NewUnitNumber validates raw for a unit of the given kind. ISO 6346 applies
// when the kind is CONTAINER or when the caller declares SchemeISO6346; any
// other unit keeps a free-form number (not blank, free of NUL bytes, at most
// 64 characters). A declared scheme that is not known is ErrValidation; a
// number that fails ISO 6346 is ErrInvalidUnitNumber and names the check
// digit it expected.
func NewUnitNumber(raw string, kind TrailerKind, declared UnitScheme) (UnitNumber, error) {
	if err := Require("unit number", raw, 64); err != nil {
		return UnitNumber{}, err
	}
	if !declared.Valid() {
		return UnitNumber{}, Invalid("unit number scheme %q is not valid", declared)
	}
	if kind != KindContainer && declared != SchemeISO6346 {
		return UnitNumber{value: raw}, nil
	}
	if err := checkISO6346(raw); err != nil {
		return UnitNumber{}, err
	}
	return UnitNumber{value: raw, scheme: SchemeISO6346}, nil
}

// String returns the number as given.
func (u UnitNumber) String() string { return u.value }

// Scheme returns SchemeISO6346 when the number was validated as an ISO 6346
// container number and "" for a free-form number.
func (u UnitNumber) Scheme() UnitScheme { return u.scheme }

// ISO6346CheckDigit returns the check digit (0..9) of the first ten
// characters of an ISO 6346 number (owner code, category, serial). It
// reports false when body is not four upper-case letters (the fourth one
// U, J or Z) followed by six digits.
//
// Letters take the values 10, 12, 13 ... 38 (A = 10; every multiple of 11
// is skipped), digits their own value. Each value is multiplied by 2 to the
// power of its position (0..9), the products are summed, the sum modulo 11
// is the check digit and a remainder of 10 is written 0.
func ISO6346CheckDigit(body string) (int, bool) {
	if len(body) != 10 || !strings.ContainsRune("UJZ", rune(body[3])) {
		return 0, false
	}
	sum := 0
	for i := 0; i < 10; i++ {
		v, ok := iso6346Value(body[i], i < 4)
		if !ok {
			return 0, false
		}
		sum += v << i
	}
	return sum % 11 % 10, true
}

// iso6346Value is the numeric value of one character: letters in the owner
// part, digits in the serial part.
func iso6346Value(c byte, letter bool) (int, bool) {
	if !letter {
		if c < '0' || c > '9' {
			return 0, false
		}
		return int(c - '0'), true
	}
	if c < 'A' || c > 'Z' {
		return 0, false
	}
	return iso6346LetterValues[c-'A'], true
}

// iso6346LetterValues are the values of A to Z in an ISO 6346 number: from 10
// up, skipping the multiples of 11 (11, 22 and 33), so B = 12, L = 23, V = 34.
var iso6346LetterValues = [26]int{10, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 34, 35, 36, 37, 38}

func checkISO6346(raw string) error {
	const form = "ISO 6346 needs a 3-letter owner code, the category letter U, J or Z, 6 digits and a check digit"
	if len(raw) != 11 && len(raw) != 10 {
		return fmt.Errorf("%w: %q is not an ISO 6346 number: %s", ErrInvalidUnitNumber, raw, form)
	}
	want, ok := ISO6346CheckDigit(raw[:10])
	if !ok {
		return fmt.Errorf("%w: %q is not an ISO 6346 number: %s", ErrInvalidUnitNumber, raw, form)
	}
	if len(raw) == 10 {
		return fmt.Errorf("%w: %q has no check digit; the expected check digit is %d", ErrInvalidUnitNumber, raw, want)
	}
	if raw[10:] != strconv.Itoa(want) {
		return fmt.Errorf("%w: %q has check digit %c; the expected check digit is %d", ErrInvalidUnitNumber, raw, raw[10], want)
	}
	return nil
}
