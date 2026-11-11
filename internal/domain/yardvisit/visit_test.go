package yardvisit_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

var t0 = time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func trailerRef() yardvisit.TrailerRef {
	return yardvisit.TrailerRef{ID: "trl-1", UnitNumber: "TR-1", SCAC: "ACME", Kind: shared.KindDryVan, Load: shared.LoadLoaded}
}

func checkIn() yardvisit.CheckInInput {
	return yardvisit.CheckInInput{
		ID: "visit-1", SiteID: "WH1", Carrier: "Acme Freight", SCAC: "ACME", TractorPlate: "ABC1D23", DriverRef: "drv-7",
		Purpose: shared.PurposeLiveUnload, AppointmentID: "appt-1", Trailers: []yardvisit.TrailerRef{trailerRef()},
		Gate: yardvisit.Gate{AppointmentKnown: true, AppointmentBooked: true}, At: t0,
	}
}

func open(t *testing.T, mod ...func(*yardvisit.CheckInInput)) *yardvisit.Visit {
	t.Helper()
	in := checkIn()
	for _, m := range mod {
		m(&in)
	}
	v, err := yardvisit.CheckIn(in)
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	return v
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func data(t *testing.T, e shared.Event) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func only(t *testing.T, v *yardvisit.Visit) shared.Event {
	t.Helper()
	ev := v.PullEvents()
	if len(ev) != 1 {
		t.Fatalf("want 1 event, got %d", len(ev))
	}
	return ev[0]
}

func TestCheckIn(t *testing.T) {
	v := open(t)
	want := yardvisit.Snapshot{
		ID: "visit-1", SiteID: "WH1", Carrier: "Acme Freight", SCAC: "ACME", Plate: "ABC1D23", DriverRef: "drv-7", Purpose: shared.PurposeLiveUnload,
		AppointmentID: "appt-1", State: yardvisit.CheckedIn, TrailersIn: []string{"trl-1"}, TrailersOut: []string{}, ArrivedAt: t0, Version: 1,
	}
	if got := v.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot\n got %+v\nwant %+v", got, want)
	}
	e := only(t, v)
	if e.EventName() != "VisitCheckedIn" || e.Entity() != "yardvisit" || e.AggregateID() != "visit-1" || !e.OccurredAt().Equal(t0) {
		t.Fatalf("header %+v", e)
	}
	eq(t, data(t, e), map[string]any{
		"visit_id": "visit-1", "site_id": "WH1", "carrier": "Acme Freight", "carrier_scac": "ACME", "purpose": "LIVE_UNLOAD", "appointment_id": "appt-1",
		"trailers":   []any{map[string]any{"trailer_id": "trl-1", "unit_number": "TR-1", "carrier_scac": "ACME", "kind": "DRY_VAN", "load_state": "LOADED", "unit_number_scheme": nil, "seal_class": nil, "hazmat_class": nil, "un_number": nil, "requires_temperature_control": false}},
		"arrived_at": "2026-11-05T14:00:00Z", "version": float64(1),
	})
}

func TestVisitAccessors(t *testing.T) {
	v := open(t)
	if v.ID() != "visit-1" || v.SiteID() != "WH1" || v.State() != yardvisit.CheckedIn || v.Purpose() != shared.PurposeLiveUnload || v.AppointmentID() != "appt-1" || v.Version() != 1 {
		t.Fatal("accessors")
	}
}

func TestCheckInWalkInHasNulls(t *testing.T) {
	v := open(t, func(in *yardvisit.CheckInInput) {
		in.AppointmentID, in.SCAC, in.DriverRef = "", "", ""
		in.Trailers[0].SCAC = ""
		in.Gate = yardvisit.Gate{}
	})
	d := data(t, only(t, v))
	if d["appointment_id"] != nil || d["carrier_scac"] != nil || d["trailers"].([]any)[0].(map[string]any)["carrier_scac"] != nil {
		t.Fatalf("nullable fields are null: %v", d)
	}
}

func TestCheckInAppointmentRules(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*yardvisit.CheckInInput)
		err  error
		kept string
	}{
		{"booked appointment kept", nil, nil, "appt-1"},
		{"unknown appointment", func(in *yardvisit.CheckInInput) { in.Gate.AppointmentKnown, in.Gate.AppointmentBooked = false, false }, yardvisit.ErrUnknownAppointment, ""},
		{"unknown appointment even when the site requires none", func(in *yardvisit.CheckInInput) { in.Gate.AppointmentKnown = false }, yardvisit.ErrUnknownAppointment, ""},
		{"appointment must cover exactly one trailer: two", func(in *yardvisit.CheckInInput) {
			second := trailerRef()
			second.ID, second.UnitNumber = "trl-2", "TR-2"
			in.Trailers = append(in.Trailers, second)
		}, yardvisit.ErrUnknownAppointment, ""},
		{"appointment must cover exactly one trailer: none", func(in *yardvisit.CheckInInput) { in.Purpose, in.Trailers = shared.PurposePickup, nil }, yardvisit.ErrUnknownAppointment, ""},
		{"required and booked", func(in *yardvisit.CheckInInput) { in.Gate.RequireAppointment = true }, nil, "appt-1"},
		{"required and none named", func(in *yardvisit.CheckInInput) { in.Gate.RequireAppointment, in.AppointmentID = true, "" }, yardvisit.ErrAppointmentRequired, ""},
		{"required and not booked", func(in *yardvisit.CheckInInput) { in.Gate.RequireAppointment, in.Gate.AppointmentBooked = true, false }, yardvisit.ErrAppointmentRequired, ""},
		{"not required and not booked is a walk-in", func(in *yardvisit.CheckInInput) { in.Gate.AppointmentBooked = false }, nil, ""},
		{"not required and none named", func(in *yardvisit.CheckInInput) { in.AppointmentID = "" }, nil, ""},
	}
	for _, c := range cases {
		in := checkIn()
		if c.mod != nil {
			c.mod(&in)
		}
		v, err := yardvisit.CheckIn(in)
		if c.err != nil {
			if !errors.Is(err, c.err) || v != nil {
				t.Errorf("%s: err = %v", c.name, err)
			}
			continue
		}
		if err != nil || v.AppointmentID() != c.kept {
			t.Errorf("%s: err = %v kept = %q", c.name, err, v.AppointmentID())
		}
	}
}

func TestCheckInValidation(t *testing.T) {
	long := strings.Repeat("x", 101)
	cases := []struct {
		name string
		mod  func(*yardvisit.CheckInInput)
		err  error
	}{
		{"blank id", func(in *yardvisit.CheckInInput) { in.ID = "" }, shared.ErrValidation},
		{"blank site", func(in *yardvisit.CheckInInput) { in.SiteID = " " }, shared.ErrValidation},
		{"blank plate", func(in *yardvisit.CheckInInput) { in.TractorPlate = "" }, shared.ErrValidation},
		{"long plate", func(in *yardvisit.CheckInInput) { in.TractorPlate = strings.Repeat("P", 33) }, shared.ErrValidation},
		{"long driver", func(in *yardvisit.CheckInInput) { in.DriverRef = strings.Repeat("d", 65) }, shared.ErrValidation},
		{"blank carrier", func(in *yardvisit.CheckInInput) { in.Carrier = " " }, shared.ErrInvalidCarrier},
		{"long carrier", func(in *yardvisit.CheckInInput) { in.Carrier = long }, shared.ErrInvalidCarrier},
		{"nul carrier", func(in *yardvisit.CheckInInput) { in.Carrier = "a\x00" }, shared.ErrValidation},
		{"bad scac", func(in *yardvisit.CheckInInput) { in.SCAC = "ac" }, shared.ErrInvalidCarrier},
		{"no time", func(in *yardvisit.CheckInInput) { in.At = time.Time{} }, shared.ErrValidation},
		{"bad purpose", func(in *yardvisit.CheckInInput) { in.Purpose = "VISIT" }, shared.ErrValidation},
		{"bobtail with a trailer", func(in *yardvisit.CheckInInput) { in.Purpose, in.AppointmentID = shared.PurposeBobtail, "" }, shared.ErrValidation},
		{"drop without trailer", func(in *yardvisit.CheckInInput) {
			in.Purpose, in.AppointmentID, in.Trailers = shared.PurposeDrop, "", nil
		}, shared.ErrValidation},
		{"live load without trailer", func(in *yardvisit.CheckInInput) {
			in.Purpose, in.AppointmentID, in.Trailers = shared.PurposeLiveLoad, "", nil
		}, shared.ErrValidation},
		{"blank trailer id", func(in *yardvisit.CheckInInput) { in.Trailers[0].ID = "" }, shared.ErrValidation},
		{"blank unit number", func(in *yardvisit.CheckInInput) { in.Trailers[0].UnitNumber = "" }, shared.ErrValidation},
		{"bad trailer scac", func(in *yardvisit.CheckInInput) { in.Trailers[0].SCAC = "x" }, shared.ErrInvalidCarrier},
		{"bad trailer kind", func(in *yardvisit.CheckInInput) { in.Trailers[0].Kind = "BOX" }, shared.ErrValidation},
		{"bad trailer load", func(in *yardvisit.CheckInInput) { in.Trailers[0].Load = "FULL" }, shared.ErrValidation},
		{"duplicate trailer", func(in *yardvisit.CheckInInput) {
			in.AppointmentID = ""
			in.Trailers = append(in.Trailers, in.Trailers[0])
		}, shared.ErrValidation},
	}
	for _, c := range cases {
		in := checkIn()
		c.mod(&in)
		if _, err := yardvisit.CheckIn(in); !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.err)
		}
	}
	for _, p := range []shared.Purpose{shared.PurposePickup, shared.PurposeBobtail} {
		in := checkIn()
		in.Purpose, in.AppointmentID, in.Trailers = p, "", nil
		if _, err := yardvisit.CheckIn(in); err != nil {
			t.Errorf("%s needs no trailer: %v", p, err)
		}
	}
	in := checkIn()
	in.Carrier = strings.Repeat("c", 100)
	if _, err := yardvisit.CheckIn(in); err != nil {
		t.Errorf("a 100-character carrier is valid: %v", err)
	}
}

func reject() yardvisit.RejectInput {
	return yardvisit.RejectInput{ID: "visit-2", SiteID: "WH1", Carrier: "Acme", SCAC: "ACME", TractorPlate: "XYZ", AppointmentID: "appt-9", Reason: yardvisit.ReasonSealMismatch, At: t0}
}

func TestRejectAtGate(t *testing.T) {
	v, err := yardvisit.RejectAtGate(reject())
	must(t, err)
	want := yardvisit.Snapshot{
		ID: "visit-2", SiteID: "WH1", Carrier: "Acme", SCAC: "ACME", Plate: "XYZ", AppointmentID: "appt-9", State: yardvisit.Rejected,
		Reason: yardvisit.ReasonSealMismatch, TrailersIn: []string{}, TrailersOut: []string{}, ArrivedAt: t0, Version: 1,
	}
	if got := v.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot\n got %+v\nwant %+v", got, want)
	}
	e := only(t, v)
	if e.EventName() != "VisitRejected" {
		t.Fatalf("event %s", e.EventName())
	}
	eq(t, data(t, e), map[string]any{
		"visit_id": "visit-2", "site_id": "WH1", "carrier_scac": "ACME", "appointment_id": "appt-9", "reason": "SEAL_MISMATCH", "rejected_at": "2026-11-05T14:00:00Z",
	})
}

func TestRejectAtGateNullsAndRefusals(t *testing.T) {
	in := reject()
	in.SCAC, in.AppointmentID = "", ""
	v, err := yardvisit.RejectAtGate(in)
	must(t, err)
	if d := data(t, only(t, v)); d["carrier_scac"] != nil || d["appointment_id"] != nil {
		t.Fatalf("nulls: %v", d)
	}
	for name, mod := range map[string]func(*yardvisit.RejectInput){
		"bad reason":       func(in *yardvisit.RejectInput) { in.Reason = "BORED" },
		"blank plate":      func(in *yardvisit.RejectInput) { in.TractorPlate = "" },
		"long appointment": func(in *yardvisit.RejectInput) { in.AppointmentID = strings.Repeat("a", 65) },
		"blank carrier":    func(in *yardvisit.RejectInput) { in.Carrier = "" },
	} {
		bad := reject()
		mod(&bad)
		if _, err := yardvisit.RejectAtGate(bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, r := range []yardvisit.RejectionReason{yardvisit.ReasonAppointmentRequired, yardvisit.ReasonUnknownAppointment, yardvisit.ReasonSealMismatch, yardvisit.ReasonTrailerAlreadyOnSite, yardvisit.ReasonSiteRequest} {
		if !r.Valid() {
			t.Errorf("%s", r)
		}
	}
	if yardvisit.RejectionReason("").Valid() {
		t.Error("empty reason")
	}
}

func TestCheckOut(t *testing.T) {
	v := open(t)
	v.PullEvents()
	must(t, v.CheckOut([]yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true}}, at(98)))
	s := v.Snapshot()
	if s.State != yardvisit.CheckedOut || s.Version != 2 || !reflect.DeepEqual(s.TrailersOut, []string{"trl-1"}) || !s.DepartedAt.Equal(at(98)) {
		t.Fatalf("snapshot %+v", s)
	}
	e := only(t, v)
	d := data(t, e)
	if e.EventName() != "VisitCheckedOut" || d["on_site_minutes"] != float64(98) || d["version"] != float64(2) || d["departed_at"] != "2026-11-05T15:38:00Z" ||
		!reflect.DeepEqual(d["trailers_out"], []any{"trl-1"}) || d["visit_id"] != "visit-1" || d["site_id"] != "WH1" || len(d) != 6 {
		t.Fatalf("data %v", d)
	}
}

func TestCheckOutDropLeavesNothing(t *testing.T) {
	v := open(t)
	v.PullEvents()
	must(t, v.CheckOut(nil, at(5)))
	d := data(t, only(t, v))
	if l, ok := d["trailers_out"].([]any); !ok || len(l) != 0 {
		t.Fatalf("trailers_out must be []: %v", d["trailers_out"])
	}
}

func TestCheckOutRefusals(t *testing.T) {
	released := yardvisit.Departure{TrailerID: "trl-1", OnSite: true, Released: true}
	for name, tc := range map[string]struct {
		purpose shared.Purpose
		out     []yardvisit.Departure
		err     error
	}{
		"not on site":                 {shared.PurposeLiveUnload, []yardvisit.Departure{{TrailerID: "trl-1", Released: true}}, yardvisit.ErrTrailerNotReleased},
		"not released":                {shared.PurposeLiveUnload, []yardvisit.Departure{{TrailerID: "trl-1", OnSite: true}}, yardvisit.ErrTrailerNotReleased},
		"loaded outbound no seal":     {shared.PurposeLiveLoad, []yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true, Loaded: true}}, yardvisit.ErrSealRequired},
		"loaded pickup no seal":       {shared.PurposePickup, []yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true, Loaded: true}}, yardvisit.ErrSealRequired},
		"blank trailer":               {shared.PurposeLiveUnload, []yardvisit.Departure{{TrailerID: " ", OnSite: true, Released: true}}, shared.ErrValidation},
		"same trailer twice":          {shared.PurposeLiveUnload, []yardvisit.Departure{released, released}, shared.ErrValidation},
		"released one then a bad one": {shared.PurposeLiveUnload, []yardvisit.Departure{released, {TrailerID: "trl-2", OnSite: true}}, yardvisit.ErrTrailerNotReleased},
	} {
		v := open(t, func(in *yardvisit.CheckInInput) { in.Purpose, in.AppointmentID = tc.purpose, "" })
		if err := v.CheckOut(tc.out, at(5)); !errors.Is(err, tc.err) {
			t.Errorf("%s: err = %v", name, err)
		}
		if v.State() != yardvisit.CheckedIn || v.Version() != 1 {
			t.Errorf("%s: a refused check-out changes nothing", name)
		}
	}
	// sealed or empty or inbound trailers leave without further ado.
	for name, tc := range map[string]struct {
		purpose shared.Purpose
		d       yardvisit.Departure
	}{
		"sealed outbound": {shared.PurposeLiveLoad, yardvisit.Departure{TrailerID: "trl-1", OnSite: true, Released: true, Loaded: true, SealNumber: "S"}},
		"empty outbound":  {shared.PurposeLiveLoad, yardvisit.Departure{TrailerID: "trl-1", OnSite: true, Released: true}},
		"loaded inbound":  {shared.PurposeLiveUnload, yardvisit.Departure{TrailerID: "trl-1", OnSite: true, Released: true, Loaded: true}},
		"loaded drop":     {shared.PurposeDrop, yardvisit.Departure{TrailerID: "trl-1", OnSite: true, Released: true, Loaded: true}},
	} {
		v := open(t, func(in *yardvisit.CheckInInput) { in.Purpose, in.AppointmentID = tc.purpose, "" })
		if err := v.CheckOut([]yardvisit.Departure{tc.d}, at(5)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCheckOutIllegalStates(t *testing.T) {
	v := open(t)
	must(t, v.CheckOut(nil, at(5)))
	if err := v.CheckOut(nil, at(6)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("twice: %v", err)
	}
	r, err := yardvisit.RejectAtGate(reject())
	must(t, err)
	if err := r.CheckOut(nil, at(6)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("rejected: %v", err)
	}
}

func TestCheckOutClampsNegativeStay(t *testing.T) {
	v := open(t)
	v.PullEvents()
	must(t, v.CheckOut(nil, at(-10)))
	if data(t, only(t, v))["on_site_minutes"] != float64(0) {
		t.Fatal("never negative")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	out := open(t)
	must(t, out.CheckOut([]yardvisit.Departure{{TrailerID: "trl-1", OnSite: true, Released: true}}, at(9)))
	rej, err := yardvisit.RejectAtGate(reject())
	must(t, err)
	for name, v := range map[string]*yardvisit.Visit{"in": open(t), "out": out, "rejected": rej} {
		want := v.Snapshot()
		got, err := yardvisit.Restore(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got.Snapshot(), want) || len(got.PullEvents()) != 0 {
			t.Errorf("%s: round trip differs", name)
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	in := func() yardvisit.Snapshot { return open(t).Snapshot() }
	rj := func() yardvisit.Snapshot {
		v, _ := yardvisit.RejectAtGate(reject())
		return v.Snapshot()
	}
	co := func() yardvisit.Snapshot {
		v := open(t)
		must(t, v.CheckOut(nil, at(5)))
		return v.Snapshot()
	}
	mod := func(s yardvisit.Snapshot, f func(*yardvisit.Snapshot)) yardvisit.Snapshot { f(&s); return s }
	for name, s := range map[string]yardvisit.Snapshot{
		"blank id":                 mod(in(), func(s *yardvisit.Snapshot) { s.ID = "" }),
		"bad state":                mod(in(), func(s *yardvisit.Snapshot) { s.State = "LOST" }),
		"version 0":                mod(in(), func(s *yardvisit.Snapshot) { s.Version = 0 }),
		"checked in no purpose":    mod(in(), func(s *yardvisit.Snapshot) { s.Purpose = "" }),
		"checked in with reason":   mod(in(), func(s *yardvisit.Snapshot) { s.Reason = yardvisit.ReasonSiteRequest }),
		"checked in departed":      mod(in(), func(s *yardvisit.Snapshot) { s.DepartedAt = at(1) }),
		"checked in with out":      mod(in(), func(s *yardvisit.Snapshot) { s.TrailersOut = []string{"x"} }),
		"checked out no purpose":   mod(co(), func(s *yardvisit.Snapshot) { s.Purpose = "" }),
		"checked out with reason":  mod(co(), func(s *yardvisit.Snapshot) { s.Reason = yardvisit.ReasonSiteRequest }),
		"checked out not departed": mod(co(), func(s *yardvisit.Snapshot) { s.DepartedAt = time.Time{} }),
		"rejected no reason":       mod(rj(), func(s *yardvisit.Snapshot) { s.Reason = "" }),
		"rejected with purpose":    mod(rj(), func(s *yardvisit.Snapshot) { s.Purpose = shared.PurposeDrop }),
		"rejected departed":        mod(rj(), func(s *yardvisit.Snapshot) { s.DepartedAt = at(1) }),
		"rejected with trailers":   mod(rj(), func(s *yardvisit.Snapshot) { s.TrailersIn = []string{"x"} }),
		"rejected with out":        mod(rj(), func(s *yardvisit.Snapshot) { s.TrailersOut = []string{"x"} }),
	} {
		if _, err := yardvisit.Restore(s); err == nil {
			t.Errorf("%s: Restore accepted a corrupt snapshot", name)
		}
	}
}

// eq compares an event's data with the exact expected map (key set included).
func eq(t *testing.T, got, want map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("data differs:\n got %v\nwant %v", got, want)
	}
}
