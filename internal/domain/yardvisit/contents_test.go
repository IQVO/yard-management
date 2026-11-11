package yardvisit_test

import (
	"errors"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

func TestCheckInContentsRules(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*yardvisit.TrailerRef)
		want error
	}{
		{"unknown hazard class", func(r *yardvisit.TrailerRef) { r.HazmatClass = "1.7" }, shared.ErrInvalidHazmatClass},
		{"compatibility group", func(r *yardvisit.TrailerRef) { r.HazmatClass = "1.4S" }, shared.ErrInvalidHazmatClass},
		{"malformed UN number", func(r *yardvisit.TrailerRef) { r.HazmatClass, r.UNNumber = "3", "UN12" }, shared.ErrInvalidUNNumber},
		{"UN number without a class", func(r *yardvisit.TrailerRef) { r.UNNumber = "UN1203" }, shared.ErrValidation},
	}
	for _, c := range cases {
		in := checkIn()
		withTrailer(c.mod)(&in)
		if _, err := yardvisit.CheckIn(in); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestCheckInEventCarriesTheContents(t *testing.T) {
	v := open(t, withTrailer(func(r *yardvisit.TrailerRef) {
		r.HazmatClass, r.UNNumber, r.RequiresTemperatureControl = "3", "UN1203", true
	}))
	tr := data(t, only(t, v))["trailers"].([]any)[0].(map[string]any)
	if tr["hazmat_class"] != "3" || tr["un_number"] != "UN1203" || tr["requires_temperature_control"] != true {
		t.Fatalf("trailer %v", tr)
	}
	plain := open(t)
	tr = data(t, only(t, plain))["trailers"].([]any)[0].(map[string]any)
	if tr["hazmat_class"] != nil || tr["un_number"] != nil || tr["requires_temperature_control"] != false {
		t.Fatalf("a trailer that declares nothing: %v", tr)
	}
}
