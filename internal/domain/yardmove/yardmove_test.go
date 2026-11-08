package yardmove_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
)

var t0 = time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

var (
	spot1 = shared.Location{Kind: shared.Spot, Code: "WH1-P1"}
	spot2 = shared.Location{Kind: shared.Spot, Code: "WH1-P2"}
	door1 = shared.Location{Kind: shared.Door, Code: "WH1-D1"}
)

func hostlerInput() yardmove.RequestInput {
	return yardmove.RequestInput{
		ID: "mv-1", SiteID: "WH1", TrailerID: "trl-1", AppointmentID: "appt-1", Performer: yardmove.Hostler,
		From: spot1, To: door1, Reason: yardmove.ToDoor, Priority: yardmove.Priority{Band: 3, DueBy: at(600)}, At: t0,
	}
}

func driverInput() yardmove.RequestInput {
	in := hostlerInput()
	in.Performer, in.From = yardmove.VisitingDriver, shared.Location{}
	return in
}

func request(t *testing.T, in yardmove.RequestInput) *yardmove.Move {
	t.Helper()
	m, err := yardmove.Request(in)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	return m
}

func claimed(t *testing.T) *yardmove.Move {
	t.Helper()
	m := request(t, hostlerInput())
	must(t, m.Claim("h-1", at(10), 5*time.Minute))
	m.PullEvents()
	return m
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

func only(t *testing.T, m *yardmove.Move) shared.Event {
	t.Helper()
	ev := m.PullEvents()
	if len(ev) != 1 {
		t.Fatalf("want 1 event, got %d", len(ev))
	}
	return ev[0]
}

func TestRequestHostlerMove(t *testing.T) {
	m := request(t, hostlerInput())
	want := yardmove.Snapshot{
		ID: "mv-1", SiteID: "WH1", TrailerID: "trl-1", AppointmentID: "appt-1", Performer: yardmove.Hostler, From: spot1, To: door1,
		Reason: yardmove.ToDoor, Priority: yardmove.Priority{Band: 3, DueBy: at(600)}, State: yardmove.Requested, RequestedAt: t0, Version: 1,
	}
	if got := m.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot\n got %+v\nwant %+v", got, want)
	}
	e := only(t, m)
	header(t, e, "YardMoveRequested", "mv-1", t0)
	eq(t, data(t, e), map[string]any{
		"move_id": "mv-1", "site_id": "WH1", "trailer_id": "trl-1", "performer": "HOSTLER", "reason": "TO_DOOR", "appointment_id": "appt-1",
		"from_location": map[string]any{"kind": "SPOT", "code": "WH1-P1"}, "to_location": map[string]any{"kind": "DOOR", "code": "WH1-D1"},
		"due_by": "2026-11-05T14:10:00Z", "version": float64(1),
	})
}

func TestMoveAccessors(t *testing.T) {
	m := request(t, hostlerInput())
	checks := map[string]bool{
		"id": m.ID() == "mv-1", "site": m.SiteID() == "WH1", "trailer": m.TrailerID() == "trl-1", "appointment": m.AppointmentID() == "appt-1",
		"performer": m.Performer() == yardmove.Hostler, "state": m.State() == yardmove.Requested, "from": m.From() == spot1, "to": m.To() == door1,
		"reason": m.Reason() == yardmove.ToDoor, "holder": m.HostlerRef() == "", "version": m.Version() == 1, "open": m.Open(), "actual": m.Actual().IsZero(),
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("accessor %s", name)
		}
	}
}

func TestRequestVisitingDriverMoveIsCreatedClaimed(t *testing.T) {
	in := driverInput()
	in.AppointmentID, in.Priority.DueBy = "", time.Time{}
	m := request(t, in)
	s := m.Snapshot()
	if s.State != yardmove.Claimed || s.HostlerRef != "" || !s.LeaseExpiresAt.IsZero() || !s.ClaimedAt.Equal(t0) || !s.From.IsZero() {
		t.Fatalf("snapshot %+v", s)
	}
	d := data(t, only(t, m))
	if d["from_location"] != nil || d["appointment_id"] != nil || d["due_by"] != nil || d["performer"] != "VISITING_DRIVER" {
		t.Fatalf("nullable fields are null: %v", d)
	}
	if m.LeaseExpired(at(1_000_000)) {
		t.Fatal("a visiting-driver move has no lease to expire")
	}
}

func TestRequestRefusals(t *testing.T) {
	long := strings.Repeat("x", 65)
	cases := []struct {
		name string
		mod  func(*yardmove.RequestInput)
	}{
		{"blank id", func(in *yardmove.RequestInput) { in.ID = "" }},
		{"long id", func(in *yardmove.RequestInput) { in.ID = long }},
		{"blank site", func(in *yardmove.RequestInput) { in.SiteID = " " }},
		{"blank trailer", func(in *yardmove.RequestInput) { in.TrailerID = "" }},
		{"long appointment", func(in *yardmove.RequestInput) { in.AppointmentID = long }},
		{"bad performer", func(in *yardmove.RequestInput) { in.Performer = "ROBOT" }},
		{"bad reason", func(in *yardmove.RequestInput) { in.Reason = "WANDER" }},
		{"zero destination", func(in *yardmove.RequestInput) { in.To = shared.Location{} }},
		{"destination of a bad kind", func(in *yardmove.RequestInput) { in.To = shared.Location{Kind: "GATE", Code: "g"} }},
		{"TO_DOOR to a spot", func(in *yardmove.RequestInput) { in.To = spot2 }},
		{"TO_SPOT to a door", func(in *yardmove.RequestInput) { in.Reason = yardmove.ToSpot }},
		{"half origin", func(in *yardmove.RequestInput) { in.From = shared.Location{Kind: shared.Spot} }},
		{"driver move from a place", func(in *yardmove.RequestInput) { *in = driverInput(); in.From = spot1 }},
		{"band 0", func(in *yardmove.RequestInput) { in.Priority.Band = 0 }},
		{"band 10", func(in *yardmove.RequestInput) { in.Priority.Band = 10 }},
		{"no time", func(in *yardmove.RequestInput) { in.At = time.Time{} }},
	}
	for _, c := range cases {
		in := hostlerInput()
		c.mod(&in)
		if _, err := yardmove.Request(in); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	for _, band := range []int{1, 9} {
		in := hostlerInput()
		in.Priority.Band = band
		if _, err := yardmove.Request(in); err != nil {
			t.Errorf("band %d is valid: %v", band, err)
		}
	}
	for _, r := range []yardmove.Reason{yardmove.FromDoor, yardmove.ToSpot, yardmove.Relocate, yardmove.ToHolding} {
		in := hostlerInput()
		in.Reason, in.To = r, spot2
		if _, err := yardmove.Request(in); err != nil {
			t.Errorf("reason %s to a spot is valid: %v", r, err)
		}
	}
	in := hostlerInput()
	in.From = shared.Location{}
	if _, err := yardmove.Request(in); err != nil {
		t.Errorf("a hostler move may start from an unplaced trailer: %v", err)
	}
}

func TestClaim(t *testing.T) {
	m := request(t, hostlerInput())
	m.PullEvents()
	must(t, m.Claim("h-1", at(10), 5*time.Minute))
	s := m.Snapshot()
	if s.State != yardmove.Claimed || s.HostlerRef != "h-1" || !s.ClaimedAt.Equal(at(10)) || !s.LeaseExpiresAt.Equal(at(310)) || s.Version != 2 {
		t.Fatalf("snapshot %+v", s)
	}
	e := only(t, m)
	d := data(t, e)
	if e.EventName() != "YardMoveClaimed" || d["hostler_ref"] != "h-1" || d["claimed_at"] != "2026-11-05T14:00:10Z" || d["version"] != float64(2) || d["move_id"] != "mv-1" || d["site_id"] != "WH1" || len(d) != 5 {
		t.Fatalf("data %v", d)
	}
}

func TestClaimRefusals(t *testing.T) {
	if err := claimed(t).Claim("h-2", at(11), time.Minute); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("claim twice: %v", err)
	}
	if err := request(t, driverInput()).Claim("h-1", at(1), time.Minute); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("a visiting-driver move is never pulled: %v", err)
	}
	m := request(t, hostlerInput())
	if err := m.Claim(" ", at(1), time.Minute); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("blank hostler: %v", err)
	}
	if err := m.Claim(strings.Repeat("h", 65), at(1), time.Minute); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("long hostler: %v", err)
	}
	if err := m.Claim("h-1", at(1), 0); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("zero lease: %v", err)
	}
	if m.State() != yardmove.Requested || m.Version() != 1 {
		t.Error("refusals change nothing")
	}
}

func TestExpireLease(t *testing.T) {
	m := claimed(t)
	if m.LeaseExpired(at(309)) || !m.LeaseExpired(at(310)) {
		t.Fatal("the lease runs until claimed_at + lease (inclusive end)")
	}
	if err := m.ExpireLease(at(309)); !errors.Is(err, yardmove.ErrLeaseNotExpired) {
		t.Fatalf("early: %v", err)
	}
	must(t, m.ExpireLease(at(310)))
	want := hostlerInput()
	snap := m.Snapshot()
	if snap.State != yardmove.Requested || snap.HostlerRef != "" || !snap.LeaseExpiresAt.IsZero() || !snap.ClaimedAt.IsZero() || snap.Version != 3 || snap.To != want.To {
		t.Fatalf("snapshot %+v", snap)
	}
	e := only(t, m)
	header(t, e, "YardMoveLeaseExpired", "mv-1", at(310))
	eq(t, data(t, e), map[string]any{"move_id": "mv-1", "site_id": "WH1", "hostler_ref": "h-1", "version": float64(3)})
	if m.LeaseExpired(at(99999)) {
		t.Fatal("a REQUESTED move holds no lease")
	}
	must(t, m.Claim("h-2", at(400), time.Minute))
}

func TestExpireLeaseRefusals(t *testing.T) {
	if err := request(t, hostlerInput()).ExpireLease(at(1)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("REQUESTED: %v", err)
	}
	if err := request(t, driverInput()).ExpireLease(at(100000)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("a visiting-driver move cannot expire: %v", err)
	}
}

func TestCompleteAtDestination(t *testing.T) {
	m := claimed(t)
	must(t, m.Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(130)}))
	s := m.Snapshot()
	if s.State != yardmove.Completed || s.Actual != door1 || !s.CompletedAt.Equal(at(130)) || !s.LeaseExpiresAt.IsZero() || s.Version != 3 || m.Actual() != door1 || m.Open() {
		t.Fatalf("snapshot %+v", s)
	}
	e := only(t, m)
	header(t, e, "YardMoveCompleted", "mv-1", at(130))
	eq(t, data(t, e), map[string]any{
		"move_id": "mv-1", "site_id": "WH1", "trailer_id": "trl-1", "performer": "HOSTLER", "hostler_ref": "h-1", "reason": "TO_DOOR",
		"from_location":   map[string]any{"kind": "SPOT", "code": "WH1-P1"},
		"to_location":     map[string]any{"kind": "DOOR", "code": "WH1-D1"},
		"actual_location": map[string]any{"kind": "DOOR", "code": "WH1-D1"},
		"completed_at":    "2026-11-05T14:02:10Z", "duration_seconds": float64(120), "version": float64(3),
	})
}

func TestCompleteElsewhereNeedsANote(t *testing.T) {
	m := claimed(t)
	other := shared.Location{Kind: shared.Door, Code: "WH1-D2"}
	if err := m.Complete(yardmove.CompleteInput{Hostler: "h-1", Actual: other, At: at(20)}); !errors.Is(err, yardmove.ErrUnexplainedDestination) {
		t.Fatalf("no note: %v", err)
	}
	if err := m.Complete(yardmove.CompleteInput{Hostler: "h-1", Actual: other, Note: "  ", At: at(20)}); !errors.Is(err, yardmove.ErrUnexplainedDestination) {
		t.Fatalf("blank note: %v", err)
	}
	if m.State() != yardmove.Claimed {
		t.Fatal("refusals change nothing")
	}
	must(t, m.Complete(yardmove.CompleteInput{Hostler: "h-1", Actual: other, Note: "D1 jammed", At: at(20)}))
	if m.Actual() != other {
		t.Fatal("actual recorded")
	}
}

func TestCompleteRefusals(t *testing.T) {
	if err := claimed(t).Complete(yardmove.CompleteInput{Hostler: "h-9", At: at(20)}); !errors.Is(err, yardmove.ErrNotLeaseHolder) {
		t.Errorf("not the holder: %v", err)
	}
	if err := claimed(t).Complete(yardmove.CompleteInput{At: at(20)}); !errors.Is(err, yardmove.ErrNotLeaseHolder) {
		t.Errorf("no holder named: %v", err)
	}
	if err := request(t, hostlerInput()).Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(20)}); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("REQUESTED: %v", err)
	}
	if err := claimed(t).Complete(yardmove.CompleteInput{Hostler: "h-1", Actual: shared.Location{Kind: "GATE", Code: "x"}, Note: "n", At: at(20)}); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("bad actual kind: %v", err)
	}
	if err := claimed(t).Complete(yardmove.CompleteInput{Hostler: "h-1", Actual: spot2, Note: strings.Repeat("n", 201), At: at(20)}); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("long note: %v", err)
	}
	done := claimed(t)
	must(t, done.Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(20)}))
	if err := done.Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(21)}); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("complete twice: %v", err)
	}
}

func TestCompleteVisitingDriverMoveByTheClerk(t *testing.T) {
	m := request(t, driverInput())
	m.PullEvents()
	must(t, m.Complete(yardmove.CompleteInput{Hostler: "ignored", At: at(95)}))
	d := data(t, only(t, m))
	if d["hostler_ref"] != nil || d["from_location"] != nil || d["duration_seconds"] != float64(95) || d["performer"] != "VISITING_DRIVER" {
		t.Fatalf("data %v", d)
	}
}

func TestCompleteClampsNegativeDuration(t *testing.T) {
	m := claimed(t)
	must(t, m.Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(5)}))
	if data(t, only(t, m))["duration_seconds"] != float64(0) {
		t.Fatal("duration never negative")
	}
}

func TestCancel(t *testing.T) {
	m := request(t, hostlerInput())
	m.PullEvents()
	must(t, m.Cancel("appointment cancelled", at(5)))
	if m.State() != yardmove.Cancelled || m.Open() || m.Version() != 2 {
		t.Fatal("cancelled")
	}
	e := only(t, m)
	d := data(t, e)
	if e.EventName() != "YardMoveCancelled" || d["reason"] != "appointment cancelled" || d["version"] != float64(2) || d["move_id"] != "mv-1" || d["site_id"] != "WH1" || len(d) != 4 {
		t.Fatalf("data %v", d)
	}
	c := claimed(t)
	must(t, c.Cancel("", at(50)))
	if d := data(t, only(t, c)); d["reason"] != "cancelled" {
		t.Fatalf("default reason: %v", d["reason"])
	}
	if !c.Snapshot().LeaseExpiresAt.IsZero() {
		t.Fatal("a cancelled move holds no lease")
	}
}

func TestCancelRefusals(t *testing.T) {
	if err := request(t, driverInput()).Cancel("x", at(1)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("a visiting-driver move cannot be cancelled: %v", err)
	}
	done := claimed(t)
	must(t, done.Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(20)}))
	if err := done.Cancel("x", at(21)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("completed: %v", err)
	}
	gone := request(t, hostlerInput())
	must(t, gone.Cancel("x", at(1)))
	if err := gone.Cancel("x", at(2)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("cancel twice: %v", err)
	}
	if err := request(t, hostlerInput()).Cancel(strings.Repeat("r", 201), at(1)); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("long reason: %v", err)
	}
}

func TestBandForMinutes(t *testing.T) {
	cases := map[int]int{-30: 1, 0: 1, 1: 2, 5: 2, 6: 3, 10: 3, 11: 4, 15: 4, 16: 5, 30: 5, 31: 6, 60: 6, 61: 7, 120: 7, 121: 8, 240: 8, 241: 9, 100000: 9}
	for min, want := range cases {
		if got := yardmove.BandForMinutes(min); got != want {
			t.Errorf("%d minutes: band %d, want %d", min, got, want)
		}
	}
}

func TestReasonValid(t *testing.T) {
	for _, r := range []yardmove.Reason{yardmove.ToDoor, yardmove.FromDoor, yardmove.ToSpot, yardmove.Relocate, yardmove.ToHolding} {
		if !r.Valid() {
			t.Errorf("%s", r)
		}
	}
	if yardmove.Reason("").Valid() || yardmove.Reason("X").Valid() {
		t.Error("unknown reason valid")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	done := claimed(t)
	must(t, done.Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(20)}))
	cancelled := request(t, hostlerInput())
	must(t, cancelled.Cancel("x", at(1)))
	for name, m := range map[string]*yardmove.Move{
		"requested": request(t, hostlerInput()), "claimed": claimed(t), "driver": request(t, driverInput()), "completed": done, "cancelled": cancelled,
	} {
		want := m.Snapshot()
		got, err := yardmove.Restore(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got.Snapshot(), want) || len(got.PullEvents()) != 0 {
			t.Errorf("%s: round trip differs", name)
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	req := func() yardmove.Snapshot { return request(t, hostlerInput()).Snapshot() }
	clm := func() yardmove.Snapshot { return claimed(t).Snapshot() }
	drv := func() yardmove.Snapshot { return request(t, driverInput()).Snapshot() }
	comp := func() yardmove.Snapshot {
		m := claimed(t)
		must(t, m.Complete(yardmove.CompleteInput{Hostler: "h-1", At: at(20)}))
		return m.Snapshot()
	}
	canc := func() yardmove.Snapshot {
		m := request(t, hostlerInput())
		must(t, m.Cancel("x", at(1)))
		return m.Snapshot()
	}
	mod := func(s yardmove.Snapshot, f func(*yardmove.Snapshot)) yardmove.Snapshot { f(&s); return s }
	cases := map[string]yardmove.Snapshot{
		"blank id":                       mod(req(), func(s *yardmove.Snapshot) { s.ID = "" }),
		"bad state":                      mod(req(), func(s *yardmove.Snapshot) { s.State = "LOST" }),
		"version 0":                      mod(req(), func(s *yardmove.Snapshot) { s.Version = 0 }),
		"requested with holder":          mod(req(), func(s *yardmove.Snapshot) { s.HostlerRef = "h" }),
		"requested with lease":           mod(req(), func(s *yardmove.Snapshot) { s.LeaseExpiresAt = at(5) }),
		"requested with claim time":      mod(req(), func(s *yardmove.Snapshot) { s.ClaimedAt = at(5) }),
		"requested driver move":          mod(drv(), func(s *yardmove.Snapshot) { s.State = yardmove.Requested; s.ClaimedAt = time.Time{} }),
		"claimed without claim time":     mod(clm(), func(s *yardmove.Snapshot) { s.ClaimedAt = time.Time{} }),
		"claimed hostler without holder": mod(clm(), func(s *yardmove.Snapshot) { s.HostlerRef = "" }),
		"claimed hostler without lease":  mod(clm(), func(s *yardmove.Snapshot) { s.LeaseExpiresAt = time.Time{} }),
		"claimed driver with holder":     mod(drv(), func(s *yardmove.Snapshot) { s.HostlerRef = "h" }),
		"claimed driver with lease":      mod(drv(), func(s *yardmove.Snapshot) { s.LeaseExpiresAt = at(5) }),
		"completed without time":         mod(comp(), func(s *yardmove.Snapshot) { s.CompletedAt = time.Time{} }),
		"completed without claim time":   mod(comp(), func(s *yardmove.Snapshot) { s.ClaimedAt = time.Time{} }),
		"completed without actual":       mod(comp(), func(s *yardmove.Snapshot) { s.Actual = shared.Location{} }),
		"completed with lease":           mod(comp(), func(s *yardmove.Snapshot) { s.LeaseExpiresAt = at(5) }),
		"cancelled with lease":           mod(canc(), func(s *yardmove.Snapshot) { s.LeaseExpiresAt = at(5) }),
	}
	for name, s := range cases {
		if _, err := yardmove.Restore(s); err == nil {
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

// header asserts the identity of an event.
func header(t *testing.T, e shared.Event, name, id string, when time.Time) {
	t.Helper()
	if e.EventName() != name || e.AggregateID() != id || !e.OccurredAt().Equal(when) {
		t.Fatalf("header %s/%s/%v, want %s/%s/%v", e.EventName(), e.AggregateID(), e.OccurredAt(), name, id, when)
	}
}
