package yardvisit_test

import (
	"errors"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

func withTrailer(mod func(*yardvisit.TrailerRef)) func(*yardvisit.CheckInInput) {
	return func(in *yardvisit.CheckInInput) {
		tr := in.Trailers[0]
		mod(&tr)
		in.Trailers = []yardvisit.TrailerRef{tr}
	}
}

func TestCheckInUnitNumberRules(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*yardvisit.TrailerRef)
		want error
	}{
		{"container with a bad check digit", func(r *yardvisit.TrailerRef) { r.Kind, r.UnitNumber = shared.KindContainer, "CSQU3054380" }, shared.ErrInvalidUnitNumber},
		{"container with a free-form number", func(r *yardvisit.TrailerRef) { r.Kind = shared.KindContainer }, shared.ErrInvalidUnitNumber},
		{"declared ISO6346 on a dry van", func(r *yardvisit.TrailerRef) { r.UnitScheme = shared.SchemeISO6346 }, shared.ErrInvalidUnitNumber},
		{"unknown scheme", func(r *yardvisit.TrailerRef) { r.UnitScheme = "GS1" }, shared.ErrValidation},
		{"unknown seal class", func(r *yardvisit.TrailerRef) { r.SealClass = "X" }, shared.ErrInvalidSealClass},
	}
	for _, c := range cases {
		in := checkIn()
		withTrailer(c.mod)(&in)
		if _, err := yardvisit.CheckIn(in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestCheckInEventCarriesSchemeAndClass(t *testing.T) {
	v := open(t,
		withTrailer(func(r *yardvisit.TrailerRef) {
			r.Kind, r.UnitNumber, r.SealClass = shared.KindContainer, "CSQU3054383", shared.SealClassHighSecurity
		}))
	tr := data(t, only(t, v))["trailers"].([]any)[0].(map[string]any)
	if tr["unit_number_scheme"] != "ISO6346" || tr["seal_class"] != "H" || tr["unit_number"] != "CSQU3054383" {
		t.Fatalf("container trailer %v", tr)
	}
	declared := open(t, withTrailer(func(r *yardvisit.TrailerRef) { r.UnitNumber, r.UnitScheme = "MSCU0000007", shared.SchemeISO6346 }))
	if tr := data(t, only(t, declared))["trailers"].([]any)[0].(map[string]any); tr["unit_number_scheme"] != "ISO6346" || tr["seal_class"] != nil {
		t.Fatalf("declared ISO trailer %v", tr)
	}
	free := open(t, withTrailer(func(r *yardvisit.TrailerRef) { r.UnitNumber = "any free-form 7" }))
	if tr := data(t, only(t, free))["trailers"].([]any)[0].(map[string]any); tr["unit_number_scheme"] != nil || tr["seal_class"] != nil {
		t.Fatalf("free-form trailer %v", tr)
	}
}
