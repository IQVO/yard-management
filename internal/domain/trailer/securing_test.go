package trailer_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func secured(t *testing.T, m trailer.SecuringMethod) *trailer.Trailer {
	t.Helper()
	tr := docked(t)
	if changed, err := tr.Secure(trailer.Securing{Method: m, PerformerRef: "op-1"}, at(2)); err != nil || !changed {
		t.Fatalf("Secure: changed=%v err=%v", changed, err)
	}
	tr.PullEvents()
	return tr
}

func TestSecureRecordsMethodInstantAndPerformer(t *testing.T) {
	for _, m := range []trailer.SecuringMethod{trailer.SecuredByRestraint, trailer.SecuredByWheelChock} {
		tr := docked(t)
		changed, err := tr.Secure(trailer.Securing{Method: m, PerformerRef: "op-1"}, at(2))
		if err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v", m, changed, err)
		}
		if !tr.Secured() || tr.SecuredBy() != m || !tr.SecuredAt().Equal(at(2)) || tr.Version() != 3 {
			t.Fatalf("%s: secured=%v by=%s at=%v version=%d", m, tr.Secured(), tr.SecuredBy(), tr.SecuredAt(), tr.Version())
		}
		if sn := tr.Snapshot(); sn.SecuredBy != m || sn.SecurerRef != "op-1" || !sn.SecuredAt.Equal(at(2)) {
			t.Fatalf("snapshot %+v", sn)
		}
		e := only(t, tr)
		header(t, e, "TrailerSecured", at(2))
		eq(t, data(t, e), map[string]any{
			"trailer_id": "trl-1", "site_id": "WH1", "door_code": "WH1-D1", "method": string(m), "secured_at": "2026-11-05T14:02:00Z", "version": float64(3),
		})
	}
}

func TestSecureIsIdempotentForTheSameMethod(t *testing.T) {
	tr := secured(t, trailer.SecuredByRestraint)
	changed, err := tr.Secure(trailer.Securing{Method: trailer.SecuredByRestraint, PerformerRef: "someone-else"}, at(9))
	if err != nil || changed {
		t.Fatalf("repeat: changed=%v err=%v", changed, err)
	}
	if tr.Version() != 3 || len(tr.PullEvents()) != 0 || !tr.SecuredAt().Equal(at(2)) || tr.Snapshot().SecurerRef != "op-1" {
		t.Fatal("a repeat changes nothing: version, events, instant and performer stay")
	}
}

func TestSecureRefusals(t *testing.T) {
	other := secured(t, trailer.SecuredByRestraint)
	_, err := other.Secure(trailer.Securing{Method: trailer.SecuredByWheelChock}, at(3))
	if !errors.Is(err, shared.ErrIllegalTransition) || !strings.Contains(err.Error(), "release") {
		t.Errorf("another method while secured: %v", err)
	}
	for name, tr := range map[string]*trailer.Trailer{"in move": newTrailer(t), "parked": parked(t)} {
		if _, err := tr.Secure(trailer.Securing{Method: trailer.SecuredByRestraint}, at(3)); !errors.Is(err, shared.ErrIllegalTransition) {
			t.Errorf("%s: %v", name, err)
		}
	}
	dep := parked(t)
	must(t, dep.Depart("v", at(4)))
	if _, err := dep.Secure(trailer.Securing{Method: trailer.SecuredByRestraint}, at(5)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("departed: %v", err)
	}
	d := docked(t)
	for _, in := range []trailer.Securing{{Method: ""}, {Method: "LOCKS"}, {Method: "restraint"}, {Method: trailer.SecuredByRestraint, PerformerRef: strings.Repeat("x", 65)}, {Method: trailer.SecuredByRestraint, PerformerRef: "a\x00b"}} {
		if _, err := d.Secure(in, at(3)); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	if d.Secured() || d.Version() != 2 || len(d.PullEvents()) != 0 {
		t.Error("refusals change nothing")
	}
}

func TestStartDoorWorkUnderTheInterlock(t *testing.T) {
	rule := trailer.DoorWorkRule{RequireSecured: true}
	tr := docked(t)
	err := tr.StartDoorWorkWith(rule, at(2))
	if !errors.Is(err, trailer.ErrTrailerNotSecured) {
		t.Fatalf("unsecured under the policy: %v", err)
	}
	if tr.DoorWork() != trailer.WorkWaiting || tr.Version() != 2 || len(tr.PullEvents()) != 0 {
		t.Fatal("a refused start changes nothing")
	}
	// The policy off (and the legacy StartDoorWork) start without any securing.
	must(t, docked(t).StartDoorWorkWith(trailer.DoorWorkRule{}, at(2)))
	must(t, docked(t).StartDoorWork(at(2)))
	for _, m := range []trailer.SecuringMethod{trailer.SecuredByRestraint, trailer.SecuredByWheelChock} {
		must(t, secured(t, m).StartDoorWorkWith(rule, at(3)))
	}
}

func TestInterlockPrecedence(t *testing.T) {
	rule := trailer.DoorWorkRule{RequireSecured: true}
	held := docked(t)
	must(t, held.PlaceHold(trailer.HoldDamaged, at(2)))
	if err := held.StartDoorWorkWith(rule, at(3)); !errors.Is(err, trailer.ErrTrailerOnHold) {
		t.Errorf("hold is reported before the securing: %v", err)
	}
	if err := parked(t).StartDoorWorkWith(rule, at(3)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Errorf("a parked trailer is an illegal transition, not an unsecured one: %v", err)
	}
}

func TestReleaseSecuring(t *testing.T) {
	tr := secured(t, trailer.SecuredByWheelChock)
	must(t, tr.ReleaseSecuring(at(7)))
	if tr.Secured() || tr.SecuredBy() != "" || !tr.SecuredAt().IsZero() || tr.Version() != 4 || tr.Snapshot().SecurerRef != "" {
		t.Fatal("released")
	}
	e := only(t, tr)
	header(t, e, "TrailerSecuringReleased", at(7))
	eq(t, data(t, e), map[string]any{"trailer_id": "trl-1", "site_id": "WH1", "door_code": "WH1-D1", "released_at": "2026-11-05T14:07:00Z", "version": float64(4)})
	if err := tr.ReleaseSecuring(at(8)); !errors.Is(err, trailer.ErrTrailerNotSecured) {
		t.Errorf("nothing to release: %v", err)
	}
	if err := docked(t).ReleaseSecuring(at(8)); !errors.Is(err, trailer.ErrTrailerNotSecured) {
		t.Errorf("never secured: %v", err)
	}
}

func TestSecuringStaysWhileDoorWorkIsInProgress(t *testing.T) {
	tr := secured(t, trailer.SecuredByRestraint)
	must(t, tr.StartDoorWorkWith(trailer.DoorWorkRule{RequireSecured: true}, at(3)))
	tr.PullEvents()
	if err := tr.ReleaseSecuring(at(4)); !errors.Is(err, trailer.ErrDoorWorkOpen) || !tr.Secured() || tr.Version() != 4 {
		t.Fatalf("release in progress: %v secured=%v version=%d", err, tr.Secured(), tr.Version())
	}
	must(t, tr.CompleteDoorWork(shared.LoadEmpty, at(5)))
	must(t, tr.ReleaseSecuring(at(6)))
}

func TestASecuredTrailerDoesNotLeaveItsDoor(t *testing.T) {
	for name, leave := range map[string]func(*trailer.Trailer) error{
		"StartMoving": func(tr *trailer.Trailer) error { return tr.StartMoving(at(5)) },
		"Depart":      func(tr *trailer.Trailer) error { return tr.Depart("v", at(5)) },
		"CanLeaveDoor": func(tr *trailer.Trailer) error {
			return tr.CanLeaveDoor()
		},
	} {
		tr := secured(t, trailer.SecuredByRestraint)
		must(t, tr.StartDoorWork(at(3)))
		must(t, tr.CompleteDoorWork(shared.LoadEmpty, at(4)))
		tr.PullEvents()
		v := tr.Version()
		if err := leave(tr); !errors.Is(err, trailer.ErrTrailerStillSecured) || !strings.Contains(err.Error(), "WH1-D1") {
			t.Fatalf("%s: %v", name, err)
		}
		if tr.State() != trailer.Docked || tr.Version() != v || len(tr.PullEvents()) != 0 {
			t.Fatalf("%s: a refusal changes nothing", name)
		}
		must(t, tr.ReleaseSecuring(at(6)))
		must(t, leave(tr))
	}
}

func TestDoorWorkOpenIsReportedBeforeStillSecured(t *testing.T) {
	tr := secured(t, trailer.SecuredByRestraint)
	if err := tr.Depart("v", at(5)); !errors.Is(err, trailer.ErrDoorWorkOpen) {
		t.Fatalf("depart with open door work: %v", err)
	}
}

func TestAnUnsecuredTrailerLeavesAsBefore(t *testing.T) {
	d := docked(t)
	must(t, d.StartMoving(at(3)))
	must(t, parked(t).StartMoving(at(3)))
	must(t, worked(t).Depart("v", at(9)))
	must(t, parked(t).Depart("v", at(9)))
	if err := parked(t).CanLeaveDoor(); err != nil {
		t.Errorf("a parked trailer is never held by a securing: %v", err)
	}
}

func TestSecuringSurvivesSnapshotAndRestore(t *testing.T) {
	tr := secured(t, trailer.SecuredByWheelChock)
	got, err := trailer.Restore(tr.Snapshot())
	if err != nil || got.SecuredBy() != trailer.SecuredByWheelChock || !got.SecuredAt().Equal(at(2)) {
		t.Fatalf("restore: %v", err)
	}
	if _, err := trailer.Restore(docked(t).Snapshot()); err != nil {
		t.Fatalf("a row from before ADR 0013 loads: %v", err)
	}
}

func TestRestoreRejectsCorruptSecuring(t *testing.T) {
	sec := func() trailer.Snapshot { return secured(t, trailer.SecuredByRestraint).Snapshot() }
	cases := map[string]func(*trailer.Snapshot){
		"unknown method":             func(s *trailer.Snapshot) { s.SecuredBy = "LOCKS" },
		"method without an instant":  func(s *trailer.Snapshot) { s.SecuredAt = time.Time{} },
		"instant without a method":   func(s *trailer.Snapshot) { s.SecuredBy = "" },
		"performer without a method": func(s *trailer.Snapshot) { s.SecuredBy, s.SecuredAt = "", time.Time{} },
		"secured but parked": func(s *trailer.Snapshot) {
			*s = parked(t).Snapshot()
			s.SecuredBy, s.SecuredAt = trailer.SecuredByRestraint, at(2)
		},
	}
	for name, corrupt := range cases {
		s := sec()
		corrupt(&s)
		if _, err := trailer.Restore(s); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
