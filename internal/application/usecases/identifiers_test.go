package usecases_test

import (
	"testing"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

func container(unit string) func(*usecases.CheckInInput) {
	return func(in *usecases.CheckInInput) { in.Trailers[0].UnitNumber, in.Trailers[0].Kind = unit, "CONTAINER" }
}

func sealedWith(number, class string) func(*usecases.CheckInInput) {
	return func(in *usecases.CheckInInput) { in.Trailers[0].SealNumber, in.Trailers[0].SealClass = number, class }
}

func TestCheckInRefusesAContainerWithABadCheckDigit(t *testing.T) {
	e := yardEnv()
	_, err := e.Yard.CheckInVisit(e.Ctx, checkInput(container("CSQU3054380")))
	is(t, err, shared.ErrInvalidUnitNumber)
	if len(e.Store.EventNames()) != 0 {
		t.Fatalf("a refused check-in persists nothing: %v", e.Store.EventNames())
	}
	if _, err := e.Yard.CheckInVisit(e.Ctx, checkInput(container("CSQU3054380"))); err == nil {
		t.Fatal("still refused")
	}
}

func TestCheckInAcceptsAValidContainerAndShowsTheScheme(t *testing.T) {
	e := yardEnv()
	ci := checkIn(t, e, container("CSQU3054383"))
	tr := ci.Trailers[0]
	if tr.UnitNumber != "CSQU3054383" || tr.UnitNumberScheme == nil || *tr.UnitNumberScheme != "ISO6346" || tr.Kind != shared.KindContainer {
		t.Fatalf("trailer %+v", tr)
	}
}

func TestCheckInKeepsFreeFormNumbersOnRoadTrailers(t *testing.T) {
	for _, kind := range []string{"DRY_VAN", "REEFER", "FLATBED"} {
		e := yardEnv()
		ci := checkIn(t, e, func(in *usecases.CheckInInput) { in.Trailers[0].UnitNumber, in.Trailers[0].Kind = "tr 1042/b", kind })
		if got := ci.Trailers[0]; got.UnitNumber != "tr 1042/b" || got.UnitNumberScheme != nil {
			t.Errorf("%s: %+v", kind, got)
		}
	}
}

func TestCheckInDeclaredISO6346AppliesToARoadTrailer(t *testing.T) {
	e := yardEnv()
	_, err := e.Yard.CheckInVisit(e.Ctx, checkInput(func(in *usecases.CheckInInput) { in.Trailers[0].UnitNumberScheme = "ISO6346" }))
	is(t, err, shared.ErrInvalidUnitNumber)
	ci := checkIn(t, e, func(in *usecases.CheckInInput) {
		in.Trailers[0].UnitNumber, in.Trailers[0].UnitNumberScheme = "MSCU0000007", "ISO6346"
	})
	if s := ci.Trailers[0].UnitNumberScheme; s == nil || *s != "ISO6346" {
		t.Fatalf("scheme %v", s)
	}
	_, err = e.Yard.CheckInVisit(e.Ctx, checkInput(func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumberScheme = "OTHER", "GS1" }))
	is(t, err, shared.ErrValidation)
}

func TestCheckInSealClass(t *testing.T) {
	e := yardEnv()
	ci := checkIn(t, e, sealedWith("SL-1", "S"))
	if c := ci.Trailers[0].Seal.Class; c == nil || *c != "S" || ci.Trailers[0].Seal.State != "INTACT" {
		t.Fatalf("seal %+v", ci.Trailers[0].Seal)
	}
	_, err := e.Yard.CheckInVisit(e.Ctx, checkInput(sealedWith("SL-2", "X"), func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumber = "OTHER", "T2" }))
	is(t, err, shared.ErrInvalidSealClass)
	_, err = e.Yard.CheckInVisit(e.Ctx, checkInput(sealedWith("", "S"), func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumber = "OTHER", "T3" }))
	is(t, err, shared.ErrValidation)
}

func TestMinimumSealClassRefusesALowerClassAtCheckIn(t *testing.T) {
	e := yardEnv()
	declare(t, e, func(in *yardpolicy.DeclareInput) { in.MinimumSealClass = shared.SealClassHighSecurity })
	for name, mod := range map[string]func(*usecases.CheckInInput){"class I": sealedWith("SL-1", "I"), "class S": sealedWith("SL-1", "S"), "no class": sealedWith("SL-1", "")} {
		_, err := e.Yard.CheckInVisit(e.Ctx, checkInput(mod))
		if !isErr(err, shared.ErrSealClassTooLow) {
			t.Errorf("%s: err = %v, want ErrSealClassTooLow", name, err)
		}
	}
	if len(e.Store.EventNames()) != 0 {
		t.Fatalf("a refused check-in persists nothing: %v", e.Store.EventNames())
	}
	ci := checkIn(t, e, sealedWith("SL-1", "H"))
	if ci.Trailers[0].Seal.Class == nil || *ci.Trailers[0].Seal.Class != "H" {
		t.Fatalf("class H meets a minimum of H: %+v", ci.Trailers[0].Seal)
	}
	other := checkInput(func(in *usecases.CheckInInput) { in.TractorPlate, in.Trailers[0].UnitNumber = "PLATE2", "T2" })
	if _, err := e.Yard.CheckInVisit(e.Ctx, other); err != nil {
		t.Fatalf("a trailer with no seal at all is not judged by the class rule: %v", err)
	}
}

func TestMinimumSealClassAppliesToRecordSeal(t *testing.T) {
	e := yardEnv()
	_, tr := arrived(t, e)
	declare(t, e, func(in *yardpolicy.DeclareInput) { in.MinimumSealClass = shared.SealClassSecurity })
	_, err := e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "INTACT", "SL-5", "I")
	is(t, err, shared.ErrSealClassTooLow)
	_, err = e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "INTACT", "SL-5", "")
	is(t, err, shared.ErrSealClassTooLow)
	_, err = e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "INTACT", "SL-5", "Q")
	is(t, err, shared.ErrInvalidSealClass)
	got, err := e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "APPLIED", "SL-5", "H")
	must(t, err)
	if got.Seal.State != "APPLIED" || got.Seal.Class == nil || *got.Seal.Class != "H" || got.Seal.Number == nil || *got.Seal.Number != "SL-5" {
		t.Fatalf("seal %+v", got.Seal)
	}
	_, err = e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "BROKEN", "", "H")
	is(t, err, shared.ErrValidation)
	_, err = e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "VERIFIED", "SL-5", "")
	is(t, err, shared.ErrValidation)
	broken, err := e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "BROKEN", "", "")
	must(t, err)
	if broken.Seal.State != "BROKEN" || broken.Hold == nil {
		t.Fatalf("an APPLIED seal can break: %+v", broken)
	}
}

func TestWithoutAPolicyTheSealClassIsOptional(t *testing.T) {
	e := yardEnv()
	_, tr := arrived(t, e)
	got, err := e.Yard.RecordSeal(e.Ctx, tr.TrailerID, "INTACT", "SL-5", "")
	must(t, err)
	if got.Seal.State != "INTACT" || got.Seal.Class != nil {
		t.Fatalf("existing request, existing answer: %+v", got.Seal)
	}
	p, err := e.Yard.GetYardPolicy(e.Ctx, "WH1")
	must(t, err)
	if p.MinimumSealClass != nil {
		t.Fatalf("default policy: %v", *p.MinimumSealClass)
	}
}

func TestGateVerificationEndsVerifiedOrMismatch(t *testing.T) {
	e := yardEnv()
	e.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci := checkIn(t, e, withAppointment("appt-1"), sealedWith("SL-1", ""))
	if ci.Trailers[0].Seal.State != "VERIFIED" {
		t.Fatalf("a matching seal is VERIFIED: %+v", ci.Trailers[0].Seal)
	}
	e2 := yardEnv()
	e2.Book(appointment("appt-1", "WH1-D1", yardtest.T0))
	ci2 := checkIn(t, e2, withAppointment("appt-1"), sealedWith("SL-9", ""))
	if ci2.Trailers[0].Seal.State != "MISMATCH" || !contains(e2.Store.EventNames(), "SealExceptionRaised") {
		t.Fatalf("a different seal is a MISMATCH and still raises the exception: %+v", ci2.Trailers[0].Seal)
	}
	e3 := yardEnv()
	ci3 := checkIn(t, e3, sealedWith("SL-9", ""))
	if ci3.Trailers[0].Seal.State != "INTACT" {
		t.Fatalf("with nothing to compare the seal stays INTACT: %+v", ci3.Trailers[0].Seal)
	}
	broken, err := e.Yard.RecordSeal(e.Ctx, ci.Trailers[0].TrailerID, "BROKEN", "", "")
	must(t, err)
	if broken.Seal.State != "BROKEN" || broken.Hold == nil || *broken.Hold != "SEAL_BROKEN" {
		t.Fatalf("a VERIFIED seal can break: %+v", broken)
	}
}
