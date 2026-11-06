package yardcheck_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
)

var t0 = time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)

func open(t *testing.T) *yardcheck.Check {
	t.Helper()
	c, err := yardcheck.OpenYardCheck("yck-1", "WH1", t0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenYardCheck(t *testing.T) {
	c := open(t)
	if c.ID() != "yck-1" || c.SiteID() != "WH1" || c.State() != yardcheck.Open || c.Version() != 1 || len(c.PullEvents()) != 0 {
		t.Fatal("opened")
	}
	for name, f := range map[string]func() error{
		"blank id":   func() error { _, err := yardcheck.OpenYardCheck(" ", "WH1", t0); return err },
		"blank site": func() error { _, err := yardcheck.OpenYardCheck("y", "", t0); return err },
		"no time":    func() error { _, err := yardcheck.OpenYardCheck("y", "WH1", time.Time{}); return err },
		"long id":    func() error { _, err := yardcheck.OpenYardCheck(strings.Repeat("y", 65), "WH1", t0); return err },
	} {
		if err := f(); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRecordObservation(t *testing.T) {
	c := open(t)
	must(t, c.RecordObservation("P1", "TR-1"))
	must(t, c.RecordObservation("P2", ""))
	if c.Version() != 3 || len(c.Snapshot().Observations) != 2 {
		t.Fatal("recorded")
	}
	if err := c.RecordObservation("P1", "TR-9"); !errors.Is(err, yardcheck.ErrLocationAlreadyObserved) {
		t.Fatalf("twice: %v", err)
	}
	if err := c.RecordObservation(" ", ""); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("blank location: %v", err)
	}
	if err := c.RecordObservation("P3", strings.Repeat("u", 65)); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("long unit: %v", err)
	}
	if c.Version() != 3 {
		t.Fatal("refusals change nothing")
	}
}

func TestReconcile(t *testing.T) {
	exp := []yardcheck.Expected{
		{LocationCode: "P1", TrailerID: "trl-1", UnitNumber: "U1"},
		{LocationCode: "P2", TrailerID: "trl-2", UnitNumber: "U2"},
		{LocationCode: "P3", TrailerID: "trl-3", UnitNumber: "U3"},
		{LocationCode: "D1", TrailerID: "trl-4", UnitNumber: "U4"},
	}
	obs := func(pairs ...string) []yardcheck.Observation {
		var out []yardcheck.Observation
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, yardcheck.Observation{LocationCode: pairs[i], UnitNumber: pairs[i+1]})
		}
		return out
	}
	d := func(k yardcheck.DiscrepancyKind, loc, trl, unit string) yardcheck.Discrepancy {
		return yardcheck.Discrepancy{Kind: k, LocationCode: loc, TrailerID: trl, UnitNumber: unit}
	}
	cases := []struct {
		name string
		obs  []yardcheck.Observation
		exp  []yardcheck.Expected
		want []yardcheck.Discrepancy
	}{
		{"everything matches", obs("P1", "U1", "P2", "U2", "P3", "U3", "D1", "U4"), exp, nil},
		{"observed empty with a trailer recorded", obs("P1", "U1", "P2", "", "P3", "U3", "D1", "U4"), exp,
			[]yardcheck.Discrepancy{d(yardcheck.Missing, "P2", "trl-2", "U2")}},
		{"unobserved location with a trailer is missing", obs("P1", "U1"), exp,
			[]yardcheck.Discrepancy{d(yardcheck.Missing, "D1", "trl-4", "U4"), d(yardcheck.Missing, "P2", "trl-2", "U2"), d(yardcheck.Missing, "P3", "trl-3", "U3")}},
		{"unknown unit on an empty spot", obs("P9", "ZZ"), nil, []yardcheck.Discrepancy{d(yardcheck.Unexpected, "P9", "", "ZZ")}},
		{"empty observation of an empty spot is fine", obs("P9", ""), nil, nil},
		{"unit the model places elsewhere", obs("P1", "U2", "P2", ""), exp[:2],
			[]yardcheck.Discrepancy{d(yardcheck.WrongLocation, "P1", "trl-2", "U2"), d(yardcheck.Missing, "P1", "trl-1", "U1")}},
		{"a different unit makes the recorded trailer missing and the found one unexpected", obs("P1", "ZZ"), exp[:1],
			[]yardcheck.Discrepancy{d(yardcheck.Unexpected, "P1", "", "ZZ"), d(yardcheck.Missing, "P1", "trl-1", "U1")}},
		{"a trailer found elsewhere is not also missing at its old place", obs("P9", "U1"), exp[:1],
			[]yardcheck.Discrepancy{d(yardcheck.WrongLocation, "P9", "trl-1", "U1")}},
		{"nothing observed, nothing recorded", nil, nil, nil},
	}
	for _, c := range cases {
		got := yardcheck.Reconcile(c.obs, c.exp)
		if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestClose(t *testing.T) {
	c := open(t)
	must(t, c.RecordObservation("P1", ""))
	must(t, c.RecordObservation("P2", "ZZ"))
	must(t, c.Close([]yardcheck.Expected{{LocationCode: "P1", TrailerID: "trl-1", UnitNumber: "U1"}}, t0.Add(time.Hour)))
	s := c.Snapshot()
	if s.State != yardcheck.Closed || s.Version != 4 || !s.ClosedAt.Equal(t0.Add(time.Hour)) || len(s.Discrepancies) != 2 || c.State() != yardcheck.Closed {
		t.Fatalf("snapshot %+v", s)
	}
	ev := c.PullEvents()
	if len(ev) != 1 || ev[0].EventName() != "YardCheckClosed" || ev[0].Entity() != "yardcheck" || ev[0].AggregateID() != "yck-1" {
		t.Fatalf("events %+v", ev)
	}
	want := map[string]any{
		"yard_check_id": "yck-1", "site_id": "WH1", "closed_at": "2026-11-05T16:00:00Z",
		"discrepancies": []any{
			map[string]any{"kind": "UNEXPECTED", "location_code": "P2", "trailer_id": nil, "unit_number": "ZZ"},
			map[string]any{"kind": "MISSING", "location_code": "P1", "trailer_id": "trl-1", "unit_number": "U1"},
		},
	}
	if got := dataOf(t, ev[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("data differs:\n got %v\nwant %v", got, want)
	}
}

func TestCloseCleanAuditHasAnEmptyList(t *testing.T) {
	c := open(t)
	must(t, c.Close(nil, t0))
	raw, _ := json.Marshal(c.PullEvents()[0])
	var d map[string]any
	_ = json.Unmarshal(raw, &d)
	if l, ok := d["discrepancies"].([]any); !ok || len(l) != 0 {
		t.Fatalf("discrepancies must be []: %v", d["discrepancies"])
	}
}

func TestClosedCheckRefusesEverything(t *testing.T) {
	c := open(t)
	must(t, c.Close(nil, t0))
	if err := c.Close(nil, t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("close twice: %v", err)
	}
	if err := c.RecordObservation("P1", ""); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("observe after close: %v", err)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	closed := open(t)
	must(t, closed.RecordObservation("P1", "ZZ"))
	must(t, closed.Close(nil, t0.Add(time.Minute)))
	for name, c := range map[string]*yardcheck.Check{"open": open(t), "closed": closed} {
		want := c.Snapshot()
		got, err := yardcheck.Restore(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got.Snapshot(), want) || len(got.PullEvents()) != 0 {
			t.Errorf("%s: round trip differs:\n%+v\n%+v", name, got.Snapshot(), want)
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	op := func() yardcheck.Snapshot { return open(t).Snapshot() }
	cl := func() yardcheck.Snapshot {
		c := open(t)
		must(t, c.Close(nil, t0))
		return c.Snapshot()
	}
	mod := func(s yardcheck.Snapshot, f func(*yardcheck.Snapshot)) yardcheck.Snapshot { f(&s); return s }
	for name, s := range map[string]yardcheck.Snapshot{
		"blank id":                mod(op(), func(s *yardcheck.Snapshot) { s.ID = "" }),
		"version 0":               mod(op(), func(s *yardcheck.Snapshot) { s.Version = 0 }),
		"open but closed":         mod(op(), func(s *yardcheck.Snapshot) { s.ClosedAt = t0 }),
		"open with discrepancies": mod(op(), func(s *yardcheck.Snapshot) { s.Discrepancies = []yardcheck.Discrepancy{{Kind: yardcheck.Missing}} }),
		"closed without time":     mod(cl(), func(s *yardcheck.Snapshot) { s.ClosedAt = time.Time{} }),
		"bad state":               mod(op(), func(s *yardcheck.Snapshot) { s.State = "LOST" }),
		"blank observation":       mod(op(), func(s *yardcheck.Snapshot) { s.Observations = []yardcheck.Observation{{LocationCode: " "}} }),
		"duplicate observation": mod(op(), func(s *yardcheck.Snapshot) {
			s.Observations = []yardcheck.Observation{{LocationCode: "P1"}, {LocationCode: "P1"}}
		}),
		"bad discrepancy kind": mod(cl(), func(s *yardcheck.Snapshot) { s.Discrepancies = []yardcheck.Discrepancy{{Kind: "ODD"}} }),
	} {
		if _, err := yardcheck.Restore(s); err == nil {
			t.Errorf("%s: Restore accepted a corrupt snapshot", name)
		}
	}
}

func dataOf(t *testing.T, e shared.Event) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}
