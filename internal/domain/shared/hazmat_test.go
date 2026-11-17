package shared_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

func TestParseHazmatClassAcceptsEveryClassAndDivision(t *testing.T) {
	valid := []string{
		"1", "1.1", "1.2", "1.3", "1.4", "1.5", "1.6", "2", "2.1", "2.2", "2.3", "3", "4", "4.1", "4.2", "4.3",
		"5", "5.1", "5.2", "6", "6.1", "6.2", "7", "8", "9",
	}
	for _, in := range valid {
		got, err := shared.ParseHazmatClass(in)
		if err != nil || got != shared.HazmatClass(in) || !got.Valid() {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if got, err := shared.ParseHazmatClass(""); err != nil || got != "" || got.Valid() {
		t.Errorf("empty means not declared: %q %v", got, err)
	}
}

func TestParseHazmatClassRefusesWhatIsNotAClassOrDivision(t *testing.T) {
	for _, bad := range []string{"0", "10", "1.0", "1.7", "2.4", "3.1", "4.4", "5.3", "6.3", "7.1", "8.1", "9.1", "1.4S", "1.4 ", " 3", "3 ", "a", "III", "class 3", "-1", "1,4", "１"} {
		_, err := shared.ParseHazmatClass(bad)
		if !errors.Is(err, shared.ErrInvalidHazmatClass) || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: err = %v, want ErrInvalidHazmatClass naming the value", bad, err)
		}
	}
}

func TestParseUNNumber(t *testing.T) {
	for _, ok := range []string{"UN0004", "UN1203", "UN3534", "UN0000", "UN9999"} {
		got, err := shared.ParseUNNumber(ok)
		if err != nil || got != shared.UNNumber(ok) {
			t.Errorf("%q: %q %v", ok, got, err)
		}
	}
	if got, err := shared.ParseUNNumber(""); err != nil || got != "" {
		t.Errorf("empty means none: %q %v", got, err)
	}
	for _, bad := range []string{"UN123", "UN12345", "un1203", "Un1203", "1203", "UN", "UN12A4", "UN 1203", " UN1203", "UN1203 ", "NA1203", "UN-1203", "UN１２０３", "UN+123"} {
		if _, err := shared.ParseUNNumber(bad); !errors.Is(err, shared.ErrInvalidUNNumber) || !strings.Contains(err.Error(), "UN and four digits") {
			t.Errorf("%q: err = %v, want ErrInvalidUNNumber", bad, err)
		}
	}
}

func TestSpotKind(t *testing.T) {
	for _, k := range []string{"PARKING", "HOLDING", "REEFER_PLUG", "HAZMAT", "STAGING_LANE"} {
		if got := shared.ParseSpotKind(k); got != shared.SpotKind(k) || !got.Known() {
			t.Errorf("%s: %q", k, got)
		}
	}
	for _, unknown := range []string{"", "parking", "COLD_STORE", " HAZMAT", "null"} {
		if got := shared.ParseSpotKind(unknown); got != shared.SpotKindUnknown || got.Known() {
			t.Errorf("%q must read as unknown, got %q", unknown, got)
		}
	}
}
