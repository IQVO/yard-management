package inspection_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// same fails the test when got differs from want.
func same[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestOpenSnapshotsTheTemplateWithEveryItemPending(t *testing.T) {
	s := open(t, shared.GatePreDock).Snapshot()
	same(t, "id", s.ID, "insp-1")
	same(t, "site", s.SiteID, "WH1")
	same(t, "trailer", s.TrailerID, "trl-1")
	same(t, "visit", s.VisitID, "visit-1")
	same(t, "template", s.TemplateID, "yard-entry")
	same(t, "template version", s.TemplateVersion, 2)
	same(t, "gate", s.Gate, shared.GatePreDock)
	same(t, "status", s.Status, inspection.StatusOpen)
	same(t, "performer", s.PerformerRef, "emp-7")
	same(t, "version", s.Version, int64(1))
	same(t, "opened at", s.OpenedAt, at(1))
	same(t, "completed at is zero", s.CompletedAt.IsZero(), true)
	same(t, "grade", s.Grade, inspection.ConditionGrade(""))
	same(t, "item count", len(s.Items), 4)
}

func TestOpenCopiesEveryTemplateItemAsPendingAndEmpty(t *testing.T) {
	s := open(t, shared.GatePreDock).Snapshot()
	same(t, "item 2", s.Items[2].Code, "seal-intact")
	same(t, "item 2 hold", s.Items[2].HoldReason, trailer.HoldSealBroken)
	same(t, "item 1 allows n/a", s.Items[1].AllowNotApplicable, true)
	same(t, "item 3 required", s.Items[3].Required, false)
	for _, it := range s.Items {
		same(t, it.Code+" result", it.Result, inspection.ResultPending)
		same(t, it.Code+" note", it.Note, "")
		same(t, it.Code+" evidence is empty, not nil", it.EvidenceRefs != nil && len(it.EvidenceRefs) == 0, true)
	}
}

func TestOpenRaisesNothingAndExposesItsIdentity(t *testing.T) {
	i := open(t, shared.GatePreDock)
	same(t, "events", len(i.PullEvents()), 0)
	same(t, "id", i.ID(), "insp-1")
	same(t, "site", i.SiteID(), "WH1")
	same(t, "trailer", i.TrailerID(), "trl-1")
	same(t, "template", i.TemplateID(), "yard-entry")
	same(t, "gate", i.Gate(), shared.GatePreDock)
	same(t, "status", i.Status(), inspection.StatusOpen)
	same(t, "version", i.Version(), int64(1))
}

func TestOpenIsASnapshotOfTheTemplate(t *testing.T) {
	tpl := customTemplate(t)
	i, err := inspection.Open(inspection.OpenInput{ID: "insp-1", SiteID: "WH1", TrailerID: "trl-1", Template: tpl, Gate: shared.GateOnDemand, At: at(1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspection.Publish(inspection.PublishInput{ID: "yard-entry", Version: 3, Title: "Changed", At: at(5), Items: []inspection.TemplateItem{{Code: "only", Text: "Only.", Required: true}}}); err != nil {
		t.Fatal(err)
	}
	s := i.Snapshot()
	if s.TemplateVersion != 2 || len(s.Items) != 4 {
		t.Fatalf("a later version never rewrites the inspection: %+v", s)
	}
}

func TestOpenRefusals(t *testing.T) {
	good := func() inspection.OpenInput {
		return inspection.OpenInput{ID: "insp-1", SiteID: "WH1", TrailerID: "trl-1", Template: customTemplate(t), Gate: shared.GatePreLoad, At: at(1)}
	}
	long := strings.Repeat("x", 65)
	cases := map[string]func(*inspection.OpenInput){
		"no id":          func(in *inspection.OpenInput) { in.ID = "" },
		"no site":        func(in *inspection.OpenInput) { in.SiteID = " " },
		"no trailer":     func(in *inspection.OpenInput) { in.TrailerID = "" },
		"long id":        func(in *inspection.OpenInput) { in.ID = long },
		"long visit":     func(in *inspection.OpenInput) { in.VisitID = long },
		"long performer": func(in *inspection.OpenInput) { in.PerformerRef = long },
		"bad gate":       func(in *inspection.OpenInput) { in.Gate = "LATER" },
		"no gate":        func(in *inspection.OpenInput) { in.Gate = "" },
		"no time": func(in *inspection.OpenInput) {
			in.At = in.At.Truncate(0).AddDate(-5000, 0, 0).Truncate(0)
			in.At = zero()
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			in := good()
			mod(&in)
			if _, err := inspection.Open(in); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want validation, got %v", err)
			}
		})
	}
	in := good()
	in.Template = nil
	if _, err := inspection.Open(in); !errors.Is(err, inspection.ErrUnknownTemplate) {
		t.Fatalf("no template: %v", err)
	}
	in = good()
	in.ID, in.VisitID, in.PerformerRef = strings.Repeat("x", 64), strings.Repeat("v", 64), strings.Repeat("p", 64)
	if _, err := inspection.Open(in); err != nil {
		t.Fatalf("64 characters are allowed: %v", err)
	}
}

func TestRecordItemStoresResultNoteAndEvidence(t *testing.T) {
	i := open(t, shared.GatePreDock)
	err := i.RecordItem("lights", inspection.ItemRecord{Result: inspection.ResultFail, Note: "left tail light out", EvidenceRefs: []string{"photo:abc", "doc:42"}})
	if err != nil {
		t.Fatal(err)
	}
	s := i.Snapshot()
	it := s.Items[0]
	if it.Result != inspection.ResultFail || it.Note != "left tail light out" || len(it.EvidenceRefs) != 2 || it.EvidenceRefs[1] != "doc:42" || s.Version != 2 {
		t.Fatalf("item %+v version %d", it, s.Version)
	}
	// Recording again replaces the result, note and evidence (the inspector corrects a slip while OPEN).
	rec(t, i, "lights", inspection.ResultPass)
	it = i.Snapshot().Items[0]
	if it.Result != inspection.ResultPass || it.Note != "" || len(it.EvidenceRefs) != 0 || i.Version() != 3 {
		t.Fatalf("replaced item %+v", it)
	}
	// The snapshot does not alias the aggregate.
	in := []string{"x"}
	_ = i.RecordItem("paint", inspection.ItemRecord{Result: inspection.ResultPass, EvidenceRefs: in})
	in[0] = "mutated"
	snap := i.Snapshot()
	snap.Items[3].EvidenceRefs[0] = "mutated-too"
	if got := i.Snapshot().Items[3].EvidenceRefs[0]; got != "x" {
		t.Fatalf("evidence aliased: %q", got)
	}
}

func TestRecordItemRefusals(t *testing.T) {
	i := open(t, shared.GatePreDock)
	manyRefs := make([]string, 21)
	for n := range manyRefs {
		manyRefs[n] = "ref-" + string(rune('a'+n))
	}
	cases := []struct {
		name string
		code string
		rec  inspection.ItemRecord
		want error
	}{
		{"unknown item", "wipers", inspection.ItemRecord{Result: inspection.ResultPass}, inspection.ErrUnknownItem},
		{"not applicable not allowed", "lights", inspection.ItemRecord{Result: inspection.ResultNotApplicable}, inspection.ErrNotApplicableNotAllowed},
		{"pending is not a result", "lights", inspection.ItemRecord{Result: inspection.ResultPending}, shared.ErrValidation},
		{"made up result", "lights", inspection.ItemRecord{Result: "MAYBE"}, shared.ErrValidation},
		{"long note", "lights", inspection.ItemRecord{Result: inspection.ResultPass, Note: strings.Repeat("n", 501)}, shared.ErrValidation},
		{"too many refs", "lights", inspection.ItemRecord{Result: inspection.ResultPass, EvidenceRefs: manyRefs}, shared.ErrValidation},
		{"blank ref", "lights", inspection.ItemRecord{Result: inspection.ResultPass, EvidenceRefs: []string{" "}}, shared.ErrValidation},
		{"long ref", "lights", inspection.ItemRecord{Result: inspection.ResultPass, EvidenceRefs: []string{strings.Repeat("r", 257)}}, shared.ErrValidation},
		{"duplicate ref", "lights", inspection.ItemRecord{Result: inspection.ResultPass, EvidenceRefs: []string{"a", "a"}}, shared.ErrValidation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := i.Version()
			if err := i.RecordItem(c.code, c.rec); !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if i.Version() != before {
				t.Fatal("a refused record changes nothing")
			}
		})
	}
	// The limits themselves are fine, and NOT_APPLICABLE works where the template allows it.
	ok := []string{}
	for n := 0; n < 20; n++ {
		ok = append(ok, strings.Repeat("r", 256-1)+string(rune('a'+n)))
	}
	if err := i.RecordItem("lights", inspection.ItemRecord{Result: inspection.ResultPass, Note: strings.Repeat("n", 500), EvidenceRefs: ok}); err != nil {
		t.Fatalf("limits: %v", err)
	}
	if err := i.RecordItem("reefer-unit", inspection.ItemRecord{Result: inspection.ResultNotApplicable}); err != nil {
		t.Fatalf("not applicable where allowed: %v", err)
	}
}

func TestCompletePassed(t *testing.T) {
	i := open(t, shared.GatePreDock)
	allPass(t, i) // the optional item stays pending: a pass does not need it
	if err := i.Complete(inspection.CompleteInput{Grade: inspection.GradeClean, At: at(9), TrailerHoldable: true}); err != nil {
		t.Fatal(err)
	}
	s := i.Snapshot()
	if s.Status != inspection.StatusPassed || s.Grade != inspection.GradeClean || !s.CompletedAt.Equal(at(9)) || s.Version != 5 || i.HoldReason() != "" {
		t.Fatalf("snapshot %+v", s)
	}
	ev := i.PullEvents()
	if len(ev) != 1 || ev[0].EventName() != "InspectionCompleted" || ev[0].Entity() != "inspection" || ev[0].AggregateID() != "insp-1" || !ev[0].OccurredAt().Equal(at(9)) {
		t.Fatalf("a pass raises InspectionCompleted only: %+v", ev)
	}
	eq(t, data(t, ev[0]), map[string]any{
		"inspection_id": "insp-1", "site_id": "WH1", "trailer_id": "trl-1", "visit_id": "visit-1", "template_id": "yard-entry", "template_version": float64(2),
		"gate": "PRE_DOCK", "outcome": "PASSED", "condition_grade": "CLEAN", "completed_at": "2026-11-05T14:09:00Z", "failed_item_codes": []any{},
	})
	if len(i.PullEvents()) != 0 {
		t.Error("PullEvents clears")
	}
}

func TestCompleteFailedBecauseARequiredItemFailed(t *testing.T) {
	i := open(t, shared.GatePreLoad)
	rec(t, i, "lights", inspection.ResultPass)
	rec(t, i, "reefer-unit", inspection.ResultNotApplicable)
	if err := i.RecordItem("seal-intact", inspection.ItemRecord{Result: inspection.ResultFail, Note: "bolt cut"}); err != nil {
		t.Fatal(err)
	}
	rec(t, i, "paint", inspection.ResultFail) // an optional failure is recorded, it does not fail the inspection by itself
	if err := i.Complete(inspection.CompleteInput{At: at(9), TrailerHoldable: true}); err != nil {
		t.Fatal(err)
	}
	if i.Status() != inspection.StatusFailed || i.HoldReason() != trailer.HoldSealBroken {
		t.Fatalf("status %s reason %q", i.Status(), i.HoldReason())
	}
	ev := i.PullEvents()
	if len(ev) != 2 || ev[0].EventName() != "InspectionCompleted" || ev[1].EventName() != "InspectionFailed" {
		t.Fatalf("a failure raises both events: %+v", ev)
	}
	eq(t, data(t, ev[0]), map[string]any{
		"inspection_id": "insp-1", "site_id": "WH1", "trailer_id": "trl-1", "visit_id": "visit-1", "template_id": "yard-entry", "template_version": float64(2),
		"gate": "PRE_LOAD", "outcome": "FAILED", "condition_grade": nil, "completed_at": "2026-11-05T14:09:00Z", "failed_item_codes": []any{"seal-intact", "paint"},
	})
	eq(t, data(t, ev[1]), map[string]any{
		"inspection_id": "insp-1", "site_id": "WH1", "trailer_id": "trl-1", "template_id": "yard-entry", "gate": "PRE_LOAD",
		"failed_items": []any{
			map[string]any{"item_code": "seal-intact", "note": "bolt cut", "required": true},
			map[string]any{"item_code": "paint", "note": nil, "required": false},
		},
		"failed_at": "2026-11-05T14:09:00Z", "hold_placed": true,
	})
}

func TestAnOptionalFailureAloneDoesNotFailTheInspection(t *testing.T) {
	i := open(t, shared.GatePreDock)
	allPass(t, i)
	rec(t, i, "paint", inspection.ResultFail)
	if err := i.Complete(inspection.CompleteInput{At: at(9)}); err != nil {
		t.Fatal(err)
	}
	if i.Status() != inspection.StatusPassed || len(i.PullEvents()) != 1 || i.HoldReason() != "" {
		t.Fatalf("status %s", i.Status())
	}
}

func TestFailedEventSaysWhenNoHoldWasPlaced(t *testing.T) {
	i := open(t, shared.GatePreDock)
	rec(t, i, "lights", inspection.ResultFail)
	rec(t, i, "reefer-unit", inspection.ResultPass)
	rec(t, i, "seal-intact", inspection.ResultPass)
	if err := i.Complete(inspection.CompleteInput{At: at(9), TrailerHoldable: false}); err != nil {
		t.Fatal(err)
	}
	ev := i.PullEvents()
	if got := data(t, ev[1])["hold_placed"]; got != false {
		t.Fatalf("hold_placed %v", got)
	}
	if i.HoldReason() != trailer.HoldQuality {
		t.Fatalf("an item with no reason of its own holds for QUALITY, got %q", i.HoldReason())
	}
}

func TestHoldReasonIsTheMostSevereOfTheFailedRequiredItems(t *testing.T) {
	mk := func(reasons ...trailer.HoldReason) *inspection.Inspection {
		var items []inspection.TemplateItem
		for n, r := range reasons {
			items = append(items, inspection.TemplateItem{Code: "i" + string(rune('a'+n)), Text: "x", Required: true, HoldReason: r})
		}
		tpl, err := inspection.Publish(inspection.PublishInput{ID: "t", Version: 1, Title: "T", Items: items, At: at(0)})
		if err != nil {
			t.Fatal(err)
		}
		i, _ := inspection.Open(inspection.OpenInput{ID: "i", SiteID: "WH1", TrailerID: "trl-1", Template: tpl, Gate: shared.GateOnDemand, At: at(1)})
		for n := range reasons {
			rec(t, i, "i"+string(rune('a'+n)), inspection.ResultFail)
		}
		if err := i.Complete(inspection.CompleteInput{At: at(2)}); err != nil {
			t.Fatal(err)
		}
		return i
	}
	ladder := []trailer.HoldReason{trailer.HoldNotFound, trailer.HoldQuality, trailer.HoldDamaged, trailer.HoldSealBroken, trailer.HoldSecurity}
	for n := range ladder {
		// every reason beats the ones below it, whatever the order of the items
		got := mk(ladder[:n+1]...).HoldReason()
		rev := append([]trailer.HoldReason(nil), ladder[:n+1]...)
		for a, b := 0, len(rev)-1; a < b; a, b = a+1, b-1 {
			rev[a], rev[b] = rev[b], rev[a]
		}
		if got != ladder[n] || mk(rev...).HoldReason() != ladder[n] {
			t.Errorf("failed %v: reason %q, want %q", ladder[:n+1], got, ladder[n])
		}
	}
}

func TestCompleteRefusals(t *testing.T) {
	i := open(t, shared.GatePreDock)
	rec(t, i, "lights", inspection.ResultPass)
	rec(t, i, "reefer-unit", inspection.ResultNotApplicable)
	err := i.Complete(inspection.CompleteInput{At: at(9)})
	if !errors.Is(err, inspection.ErrRequiredItemPending) || !strings.Contains(err.Error(), "seal-intact") || strings.Contains(err.Error(), "paint") {
		t.Fatalf("a required item pending: %v", err)
	}
	rec(t, i, "seal-intact", inspection.ResultPass)
	if err := i.Complete(inspection.CompleteInput{Grade: "SPOTLESS", At: at(9)}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("grade: %v", err)
	}
	if err := i.Complete(inspection.CompleteInput{}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("no time: %v", err)
	}
	if i.Status() != inspection.StatusOpen || len(i.PullEvents()) != 0 {
		t.Fatal("refusals leave the inspection open and silent")
	}
	for _, g := range []inspection.ConditionGrade{inspection.GradeNone, inspection.GradeClean, inspection.GradeDirty, inspection.GradeOdour, inspection.GradeDamaged} {
		j := open(t, shared.GatePreDock)
		allPass(t, j)
		if err := j.Complete(inspection.CompleteInput{Grade: g, At: at(9)}); err != nil {
			t.Fatalf("grade %q: %v", g, err)
		}
	}
}

func TestCompletingIsFinal(t *testing.T) {
	i := open(t, shared.GatePreDock)
	allPass(t, i)
	if err := i.Complete(inspection.CompleteInput{At: at(9)}); err != nil {
		t.Fatal(err)
	}
	i.PullEvents()
	v := i.Version()
	if err := i.Complete(inspection.CompleteInput{At: at(10)}); !errors.Is(err, inspection.ErrInspectionClosed) {
		t.Fatalf("complete twice: %v", err)
	}
	if err := i.RecordItem("lights", inspection.ItemRecord{Result: inspection.ResultFail}); !errors.Is(err, inspection.ErrInspectionClosed) {
		t.Fatalf("record after completion: %v", err)
	}
	if i.Version() != v || len(i.PullEvents()) != 0 || i.Snapshot().Items[0].Result != inspection.ResultPass {
		t.Fatal("a closed inspection does not change")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	i := open(t, shared.GatePreLoad)
	rec(t, i, "lights", inspection.ResultFail)
	rec(t, i, "reefer-unit", inspection.ResultPass)
	rec(t, i, "seal-intact", inspection.ResultPass)
	for _, finish := range []bool{false, true} {
		if finish {
			if err := i.Complete(inspection.CompleteInput{Grade: inspection.GradeDirty, At: at(9)}); err != nil {
				t.Fatal(err)
			}
		}
		back, err := inspection.Restore(i.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		a, b := i.Snapshot(), back.Snapshot()
		if a.Status != b.Status || a.Version != b.Version || a.Grade != b.Grade || !a.CompletedAt.Equal(b.CompletedAt) || len(b.Items) != 4 || b.Items[0].Result != inspection.ResultFail {
			t.Fatalf("round trip finish=%v: %+v vs %+v", finish, a, b)
		}
		if back.HoldReason() != i.HoldReason() {
			t.Fatal("the hold reason survives a round trip")
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	failed := open(t, shared.GatePreLoad)
	rec(t, failed, "lights", inspection.ResultFail)
	rec(t, failed, "reefer-unit", inspection.ResultPass)
	rec(t, failed, "seal-intact", inspection.ResultPass)
	_ = failed.Complete(inspection.CompleteInput{At: at(9)})
	passed := open(t, shared.GatePreLoad)
	allPass(t, passed)
	_ = passed.Complete(inspection.CompleteInput{At: at(9)})

	cases := map[string]func(*inspection.Snapshot){
		"no id":                         func(s *inspection.Snapshot) { s.ID = "" },
		"bad template id":               func(s *inspection.Snapshot) { s.TemplateID = "Bad Id" },
		"zero version":                  func(s *inspection.Snapshot) { s.Version = 0 },
		"zero template version":         func(s *inspection.Snapshot) { s.TemplateVersion = 0 },
		"bad gate":                      func(s *inspection.Snapshot) { s.Gate = "SOMETIME" },
		"bad grade":                     func(s *inspection.Snapshot) { s.Grade = "SHINY" },
		"no items":                      func(s *inspection.Snapshot) { s.Items = nil },
		"no open time":                  func(s *inspection.Snapshot) { s.OpenedAt = zero() },
		"completed without a time":      func(s *inspection.Snapshot) { s.CompletedAt = zero() },
		"bad status":                    func(s *inspection.Snapshot) { s.Status = "DONE" },
		"bad result":                    func(s *inspection.Snapshot) { s.Items[0].Result = "SHRUG" },
		"passed with a required failed": func(s *inspection.Snapshot) { s.Items[0].Result = inspection.ResultFail },
		"passed with one pending":       func(s *inspection.Snapshot) { s.Items[0].Result = inspection.ResultPending },
		"failed with nothing failed":    func(s *inspection.Snapshot) { s.Status = inspection.StatusFailed },
		"open with a completion":        func(s *inspection.Snapshot) { s.Status = inspection.StatusOpen },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			s := passed.Snapshot()
			mod(&s)
			if _, err := inspection.Restore(s); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want validation, got %v", err)
			}
		})
	}
	s := failed.Snapshot()
	s.Items[0].Result = inspection.ResultPass
	if _, err := inspection.Restore(s); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("failed with nothing failed: %v", err)
	}
	if _, err := inspection.Restore(failed.Snapshot()); err != nil {
		t.Fatalf("the control row restores: %v", err)
	}
}
