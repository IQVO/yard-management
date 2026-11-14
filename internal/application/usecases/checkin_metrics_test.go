package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

// recordingCheckIns counts the ports.CheckInMetrics calls of a use case.
type recordingCheckIns struct {
	mu       sync.Mutex
	admitted int
	rejected []string
	refused  []string
}

var _ ports.CheckInMetrics = (*recordingCheckIns)(nil)

func (m *recordingCheckIns) CheckInAdmitted(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.admitted++
}

func (m *recordingCheckIns) CheckInRejected(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, reason)
}

func (m *recordingCheckIns) CheckInRefused(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refused = append(m.refused, reason)
}

// suspendCarrier folds a registered, then suspended, carrier into the local copy.
func suspendCarrier(t *testing.T, e *yardtest.Env, y *usecases.Yard, scac string) {
	t.Helper()
	must(t, y.ApplyCarrierRegistered(e.Ctx, "evt-carrier-1", views.Carrier{SCAC: scac, Name: "Acme Freight", Status: views.CarrierActive, Version: 1}, yardtest.T0))
	must(t, y.ApplyCarrierSuspended(e.Ctx, "evt-carrier-2", scac, "insurance lapsed", 2, yardtest.T0))
}

// metered is a Yard over e's store whose check-ins are counted by the returned recorder.
func metered(e *yardtest.Env) (*usecases.Yard, *recordingCheckIns) {
	m := &recordingCheckIns{}
	d := yardtest.Deps(e.Store)
	d.Metrics = m
	return usecases.New(d), m
}

func TestCheckInCountsAnAdmittedVisitOnce(t *testing.T) {
	e := yardEnv()
	y, m := metered(e)
	if _, err := y.CheckInVisit(e.Ctx, checkInput()); err != nil {
		t.Fatal(err)
	}
	if m.admitted != 1 || len(m.rejected) != 0 || len(m.refused) != 0 {
		t.Fatalf("admitted=%d rejected=%v refused=%v, want 1 / none / none", m.admitted, m.rejected, m.refused)
	}
}

// A seal mismatch under the REJECT policy answers no error but records a
// REJECTED visit: it is a rejection with its reason, not an admission.
func TestCheckInCountsASealMismatchRejectionWithItsReason(t *testing.T) {
	e := yardEnv()
	y, m := metered(e)
	_, err := y.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: yardpolicy.DeclareInput{LeaseSeconds: 300, SealMismatchAction: yardpolicy.Reject}})
	must(t, err)
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	in := checkInput(withAppointment("appt-1"), func(in *usecases.CheckInInput) { in.Trailers[0].SealNumber = "SL-9" })
	if _, err := y.CheckInVisit(e.Ctx, in); err != nil {
		t.Fatal(err)
	}
	if m.admitted != 0 || !reflect.DeepEqual(m.rejected, []string{"seal-mismatch"}) || len(m.refused) != 0 {
		t.Fatalf("admitted=%d rejected=%v refused=%v, want 0 / [seal-mismatch] / none", m.admitted, m.rejected, m.refused)
	}
}

// A suspended carrier under the REJECT policy records the REJECTED visit and
// answers ErrCarrierSuspended: still a rejection, counted once.
func TestCheckInCountsASuspendedCarrierRejection(t *testing.T) {
	e := yardEnv()
	y, m := metered(e)
	_, err := y.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: yardpolicy.DeclareInput{LeaseSeconds: 300, SealMismatchAction: yardpolicy.Flag, SuspendedCarrierAction: yardpolicy.CarrierReject}})
	must(t, err)
	suspendCarrier(t, e, y, "ACME")
	_, err = y.CheckInVisit(e.Ctx, checkInput())
	is(t, err, yardvisit.ErrCarrierSuspended)
	if m.admitted != 0 || !reflect.DeepEqual(m.rejected, []string{"carrier-suspended"}) || len(m.refused) != 0 {
		t.Fatalf("admitted=%d rejected=%v refused=%v, want 0 / [carrier-suspended] / none", m.admitted, m.rejected, m.refused)
	}
}

// The reason of a refusal is the problem slug the REST adapter answers with.
func TestCheckInCountsEachRefusalOnceWithItsProblemSlug(t *testing.T) {
	cases := []struct {
		name string
		prep func(*testing.T, *yardtest.Env, *usecases.Yard)
		mod  func(*usecases.CheckInInput)
		want string
	}{
		{"appointment required", func(t *testing.T, e *yardtest.Env, y *usecases.Yard) { requireAppointments(t, e) }, nil, "appointment-required"},
		{"unknown appointment", nil, withAppointment("appt-x"), "unknown-appointment"},
		{"open visit of the plate", func(t *testing.T, _ *yardtest.Env, y *usecases.Yard) {
			_, err := y.CheckInVisit(context.Background(), checkInput())
			must(t, err)
		}, nil, "visit-already-open"},
		{"unit already on site", func(t *testing.T, _ *yardtest.Env, y *usecases.Yard) {
			_, err := y.CheckInVisit(context.Background(), checkInput())
			must(t, err)
		}, func(in *usecases.CheckInInput) { in.TractorPlate = "OTHER" }, "trailer-already-on-site"},
		{"invalid carrier", nil, func(in *usecases.CheckInInput) { in.Carrier = " " }, "invalid-carrier"},
		{"bad trailer kind", nil, func(in *usecases.CheckInInput) { in.Trailers[0].Kind = "BOX" }, "validation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := yardEnv()
			y, m := metered(e)
			if c.prep != nil {
				c.prep(t, e, y)
			}
			*m = recordingCheckIns{} // the preparation's own check-in is not under test
			in := checkInput()
			if c.mod != nil {
				c.mod(&in)
			}
			if _, err := y.CheckInVisit(e.Ctx, in); err == nil {
				t.Fatal("the check-in must be refused")
			}
			if m.admitted != 0 || len(m.rejected) != 0 || !reflect.DeepEqual(m.refused, []string{c.want}) {
				t.Fatalf("admitted=%d rejected=%v refused=%v, want 0 / none / [%s]", m.admitted, m.rejected, m.refused, c.want)
			}
		})
	}
}

func TestCheckInWithNoFreeSpotIsRefused(t *testing.T) {
	e := yardtest.New()
	e.Site("WH1", nil, map[string]string{"WH1-D1": "Both"})
	y, m := metered(e)
	_, err := y.CheckInVisit(e.Ctx, checkInput())
	is(t, err, usecases.ErrNoFreeSpot)
	if !reflect.DeepEqual(m.refused, []string{"no-free-spot"}) || m.admitted != 0 || len(m.rejected) != 0 {
		t.Fatalf("admitted=%d rejected=%v refused=%v, want 0 / none / [no-free-spot]", m.admitted, m.rejected, m.refused)
	}
}

// An error no problem slug names is an internal error: the service is at fault.
func TestCheckInCountsAnUnclassifiedFailureAsInternalError(t *testing.T) {
	e := yardEnv()
	m := &recordingCheckIns{}
	d := yardtest.Deps(e.Store)
	d.Metrics = m
	d.Visits = failingVisits{VisitRepo: d.Visits}
	if _, err := usecases.New(d).CheckInVisit(e.Ctx, checkInput()); err == nil {
		t.Fatal("the check-in must fail")
	}
	if !reflect.DeepEqual(m.refused, []string{"internal-error"}) {
		t.Fatalf("refused = %v, want [internal-error]", m.refused)
	}
}

type failingVisits struct{ ports.VisitRepo }

func (failingVisits) FindOpenByPlate(context.Context, string, string) (*yardvisit.Visit, error) {
	return nil, errors.New("connection reset")
}

// The slug of every refusal the check-in names is the slug of the REST problem
// of the same error; the http package asserts the same list against its catalogue.
func TestCheckInRefusalReasonsAreKebabSlugs(t *testing.T) {
	for _, err := range usecases.CheckInRefusalErrors() {
		got := usecases.CheckInRefusalReason(err)
		if got == "" || got == "internal-error" {
			t.Errorf("%v has no problem slug: %q", err, got)
		}
	}
	for err, want := range map[error]string{
		yardvisit.ErrVisitAlreadyOpen: "visit-already-open", trailer.ErrTrailerAlreadyOnSite: "trailer-already-on-site",
		shared.ErrValidation: "validation", usecases.ErrNoFreeSpot: "no-free-spot",
	} {
		if got := usecases.CheckInRefusalReason(err); got != want {
			t.Errorf("reason of %v = %q, want %q", err, got, want)
		}
	}
	if got := usecases.CheckInRefusalReason(errors.New("boom")); got != "internal-error" {
		t.Errorf("an unknown error is %q, want internal-error", got)
	}
}
