package shared_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

func TestRequireAndCheck(t *testing.T) {
	cases := []struct {
		name string
		v    string
		max  int
		want bool
	}{
		{"ok", "abc", 5, true},
		{"exactly max", "abcde", 5, true},
		{"too long", "abcdef", 5, false},
		{"blank", "  \t", 5, false},
		{"empty", "", 5, false},
		{"nul", "a\x00b", 5, false},
		{"no max", strings.Repeat("x", 500), 0, true},
		{"multibyte counts runes", "ééééé", 5, true},
	}
	for _, c := range cases {
		err := shared.Require("field", c.v, c.max)
		if (err == nil) != c.want {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.want)
		}
		if err != nil && !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v is not ErrValidation", c.name, err)
		}
	}
	if err := shared.Check("f", "", 3); err != nil {
		t.Errorf("Check accepts empty: %v", err)
	}
	if err := shared.Check("f", "abcd", 3); err == nil {
		t.Error("Check must enforce max")
	}
}

func TestIsBlank(t *testing.T) {
	if !shared.IsBlank("") || !shared.IsBlank(" \n") || shared.IsBlank(" a ") {
		t.Fatal("IsBlank wrong")
	}
}

func TestValidateSCAC(t *testing.T) {
	for _, ok := range []string{"", "AB", "ABC", "ABCD", "AA", "ZZ", "AZ"} {
		if err := shared.ValidateSCAC(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"A", "ABCDE", "ab", "A1", "AB ", "ÀB"} {
		if err := shared.ValidateSCAC(bad); !errors.Is(err, shared.ErrInvalidCarrier) {
			t.Errorf("%q: want ErrInvalidCarrier, got %v", bad, err)
		}
	}
}

func TestEnumerations(t *testing.T) {
	for _, k := range shared.TrailerKinds {
		if !k.Valid() {
			t.Errorf("%s must be valid", k)
		}
	}
	if shared.TrailerKind("BOX").Valid() || shared.TrailerKind("").Valid() {
		t.Error("unknown kind valid")
	}
	for _, l := range []shared.LoadState{shared.LoadEmpty, shared.LoadLoaded, shared.LoadPartial} {
		if !l.Valid() {
			t.Errorf("%s", l)
		}
	}
	if shared.LoadState("FULL").Valid() {
		t.Error("unknown load valid")
	}
	for _, p := range []shared.Purpose{shared.PurposeDrop, shared.PurposeLiveUnload, shared.PurposeLiveLoad, shared.PurposePickup, shared.PurposeBobtail} {
		if !p.Valid() {
			t.Errorf("%s", p)
		}
	}
	if shared.Purpose("VISIT").Valid() {
		t.Error("unknown purpose valid")
	}
	out := map[shared.Purpose]bool{shared.PurposeDrop: false, shared.PurposeLiveUnload: false, shared.PurposeLiveLoad: true, shared.PurposePickup: true, shared.PurposeBobtail: false}
	for p, want := range out {
		if p.Outbound() != want {
			t.Errorf("%s outbound = %v", p, !want)
		}
	}
}

func TestLocation(t *testing.T) {
	l, err := shared.NewLocation(shared.Door, "WH1-D1")
	if err != nil || l.Kind != shared.Door || l.Code != "WH1-D1" || l.IsZero() || l.Ptr() == nil {
		t.Fatalf("door: %+v %v", l, err)
	}
	if _, err := shared.NewLocation("GATE", "x"); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("bad kind: %v", err)
	}
	if _, err := shared.NewLocation(shared.Spot, " "); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("blank code: %v", err)
	}
	var zero shared.Location
	if !zero.IsZero() || zero.Ptr() != nil {
		t.Error("zero location")
	}
	if (shared.Location{Kind: shared.Spot}).IsZero() || (shared.Location{Code: "x"}).IsZero() {
		t.Error("half-filled location is not zero")
	}
}

func TestHeaderAndHelpers(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 0, 0, 0, time.FixedZone("x", -3*3600))
	h := shared.NewHeader("trailer", "TrailerParked", "trl-1", at)
	if h.EventName() != "TrailerParked" || h.Entity() != "trailer" || h.AggregateID() != "trl-1" || !h.OccurredAt().Equal(at) || h.OccurredAt().Location() != time.UTC {
		t.Fatalf("header: %+v", h)
	}
	if shared.Stamp(at) != "2026-11-05T17:00:00Z" {
		t.Errorf("stamp %s", shared.Stamp(at))
	}
	if shared.StampPtr(time.Time{}) != nil || *shared.StampPtr(at) != "2026-11-05T17:00:00Z" {
		t.Error("StampPtr")
	}
	if shared.Str("") != nil || *shared.Str("a") != "a" {
		t.Error("Str")
	}
	if got := shared.Strings(nil); got == nil || len(got) != 0 {
		t.Error("Strings(nil) must be empty non-nil")
	}
	in := []string{"a"}
	out := shared.Strings(in)
	out[0] = "b"
	if in[0] != "a" {
		t.Error("Strings must copy")
	}
}
