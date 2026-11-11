package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// NewTemplate publishes a four-item custom template (ADR 0015).
func NewTemplate(t *testing.T, id string, version int) *inspection.Template {
	t.Helper()
	tpl, err := inspection.Publish(inspection.PublishInput{
		ID: id, Version: version, Title: "Yard entry check", At: T0,
		Items: []inspection.TemplateItem{
			{Code: "lights", Text: "Lights work.", Required: true},
			{Code: "reefer-unit", Text: "Reefer unit is sound.", Required: true, AllowNotApplicable: true},
			{Code: "seal-intact", Text: "Seal is intact.", Required: true, HoldReason: trailer.HoldSealBroken},
			{Code: "paint", Text: "Paint is fine.", Required: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

// NewInspection opens an inspection of a fresh custom template.
func NewInspection(t *testing.T, id, trailerID string, gate shared.Gate, offset time.Duration) *inspection.Inspection {
	t.Helper()
	i, err := inspection.Open(inspection.OpenInput{
		ID: id, SiteID: "WH1", TrailerID: trailerID, VisitID: "visit-1", Template: NewTemplate(t, "yard-entry", 1), Gate: gate,
		PerformerRef: "emp-7", At: T0.Add(offset),
	})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// recordAll records the three required items, saving after each (every record
// is one version of the aggregate), and fails the seal when failSeal is set.
func recordAll(t *testing.T, s Store, i *inspection.Inspection, failSeal bool) {
	t.Helper()
	for _, c := range []string{"lights", "reefer-unit", "seal-intact"} {
		r := inspection.ItemRecord{Result: inspection.ResultPass}
		if c == "seal-intact" && failSeal {
			r = inspection.ItemRecord{Result: inspection.ResultFail, Note: "bolt cut", EvidenceRefs: []string{"photo:1", "doc:2"}}
		}
		must(t, i.RecordItem(c, r))
		must(t, s.Inspections.Save(bg, i))
	}
}

// InspectionRepo runs the InspectionRepo contract.
func InspectionRepo(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown inspection is not found": func(t *testing.T, s Store) {
			_, err := s.Inspections.FindByID(bg, "insp-404")
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Inspections.FindOpen(bg, "trl-1", "yard-entry", shared.GatePreDock)
			wantErr(t, err, ports.ErrNotFound)
		},
		"save then find round trips an open, a passed and a failed inspection": func(t *testing.T, s Store) {
			i := NewInspection(t, "insp-1", "trl-1", shared.GatePreDock, 0)
			must(t, s.Inspections.Save(bg, i))
			check := func() {
				t.Helper()
				got, err := s.Inspections.FindByID(bg, "insp-1")
				must(t, err)
				if !reflect.DeepEqual(got.Snapshot(), i.Snapshot()) {
					t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), i.Snapshot())
				}
			}
			check()
			recordAll(t, s, i, true)
			check()
			must(t, i.Complete(inspection.CompleteInput{Grade: inspection.GradeDamaged, At: T0.Add(time.Hour)}))
			must(t, s.Inspections.Save(bg, i))
			check()
			if got := i.Snapshot(); got.Status != inspection.StatusFailed || got.Items[2].EvidenceRefs[1] != "doc:2" {
				t.Fatalf("snapshot %+v", got)
			}
		},
		"one open inspection per trailer, template and gate": func(t *testing.T, s Store) {
			first := NewInspection(t, "insp-1", "trl-1", shared.GatePreDock, 0)
			must(t, s.Inspections.Save(bg, first))
			wantErr(t, s.Inspections.Save(bg, NewInspection(t, "insp-2", "trl-1", shared.GatePreDock, time.Minute)), inspection.ErrInspectionAlreadyOpen)
			must(t, s.Inspections.Save(bg, NewInspection(t, "insp-3", "trl-1", shared.GatePreLoad, time.Minute)))
			must(t, s.Inspections.Save(bg, NewInspection(t, "insp-4", "trl-2", shared.GatePreDock, time.Minute)))
			got, err := s.Inspections.FindOpen(bg, "trl-1", "yard-entry", shared.GatePreDock)
			must(t, err)
			if got.ID() != "insp-1" {
				t.Fatalf("FindOpen = %s", got.ID())
			}
			recordAll(t, s, first, false)
			must(t, first.Complete(inspection.CompleteInput{At: T0.Add(time.Hour)}))
			must(t, s.Inspections.Save(bg, first))
			_, err = s.Inspections.FindOpen(bg, "trl-1", "yard-entry", shared.GatePreDock)
			wantErr(t, err, ports.ErrNotFound)
			must(t, s.Inspections.Save(bg, NewInspection(t, "insp-5", "trl-1", shared.GatePreDock, 2*time.Minute))) // completed: a new one may open
		},
		"the version guard refuses stale writers": func(t *testing.T, s Store) {
			i := NewInspection(t, "insp-1", "trl-1", shared.GatePreDock, 0)
			must(t, s.Inspections.Save(bg, i))
			wantErr(t, s.Inspections.Save(bg, i), ports.ErrConcurrentModification)
			one, err := s.Inspections.FindByID(bg, "insp-1")
			must(t, err)
			two, err := s.Inspections.FindByID(bg, "insp-1")
			must(t, err)
			must(t, one.RecordItem("lights", inspection.ItemRecord{Result: inspection.ResultPass}))
			must(t, two.RecordItem("lights", inspection.ItemRecord{Result: inspection.ResultFail}))
			must(t, s.Inspections.Save(bg, one))
			wantErr(t, s.Inspections.Save(bg, two), ports.ErrConcurrentModification)
		},
	})
}

// InspectionQueries runs the read side of the InspectionRepo contract.
func InspectionQueries(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"a trailer's inspections list newest first and page by keyset": func(t *testing.T, s Store) {
			for n, gate := range []shared.Gate{shared.GatePreDock, shared.GatePreLoad, shared.GatePreDeparture} {
				must(t, s.Inspections.Save(bg, NewInspection(t, "insp-"+string(rune('1'+n)), "trl-1", gate, time.Duration(n)*time.Minute)))
			}
			must(t, s.Inspections.Save(bg, NewInspection(t, "insp-9", "trl-2", shared.GatePreDock, 0)))
			first, err := s.Inspections.ListByTrailer(bg, "trl-1", nil, 2)
			must(t, err)
			if len(first) != 2 || first[0].ID() != "insp-3" || first[1].ID() != "insp-2" {
				t.Fatalf("first page %v", ids(first))
			}
			rest, err := s.Inspections.ListByTrailer(bg, "trl-1", &views.Key{At: T0.Add(time.Minute), ID: "insp-2"}, 5)
			must(t, err)
			if len(rest) != 1 || rest[0].ID() != "insp-1" {
				t.Fatalf("second page %v", ids(rest))
			}
		},
		"completed lists the finished inspections in completion order": func(t *testing.T, s Store) {
			a := NewInspection(t, "insp-a", "trl-1", shared.GatePreDock, 0)
			b := NewInspection(t, "insp-b", "trl-1", shared.GatePreLoad, 0)
			open := NewInspection(t, "insp-c", "trl-1", shared.GatePreDeparture, 0)
			for _, i := range []*inspection.Inspection{a, b, open} {
				must(t, s.Inspections.Save(bg, i))
			}
			recordAll(t, s, a, true)
			recordAll(t, s, b, false)
			must(t, b.Complete(inspection.CompleteInput{At: T0.Add(time.Minute)}))
			must(t, a.Complete(inspection.CompleteInput{At: T0.Add(2 * time.Minute)}))
			must(t, s.Inspections.Save(bg, a))
			must(t, s.Inspections.Save(bg, b))
			got, err := s.Inspections.Completed(bg, "trl-1")
			must(t, err)
			if len(got) != 2 || got[0].InspectionID != "insp-b" || got[0].Status != inspection.StatusPassed || got[1].InspectionID != "insp-a" || got[1].Status != inspection.StatusFailed || got[1].Gate != shared.GatePreDock {
				t.Fatalf("Completed = %+v", got)
			}
			none, err := s.Inspections.Completed(bg, "trl-2")
			must(t, err)
			if len(none) != 0 {
				t.Fatalf("Completed of another trailer = %+v", none)
			}
		},
	})
}

func ids(in []*inspection.Inspection) []string {
	out := make([]string, 0, len(in))
	for _, i := range in {
		out = append(out, i.ID())
	}
	return out
}

// TemplateRepo runs the TemplateRepo contract.
func TemplateRepo(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown template is not found": func(t *testing.T, s Store) {
			_, err := s.Templates.Find(bg, "ghost", 1)
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Templates.Latest(bg, "ghost")
			wantErr(t, err, ports.ErrNotFound)
		},
		"save then find round trips a version and a version is never rewritten": func(t *testing.T, s Store) {
			v1 := NewTemplate(t, "yard-entry", 1)
			must(t, s.Templates.Save(bg, v1))
			got, err := s.Templates.Find(bg, "yard-entry", 1)
			must(t, err)
			if !reflect.DeepEqual(got.Snapshot(), v1.Snapshot()) {
				t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), v1.Snapshot())
			}
			wantErr(t, s.Templates.Save(bg, NewTemplate(t, "yard-entry", 1)), ports.ErrConcurrentModification)
		},
		"latest is the highest version": func(t *testing.T, s Store) {
			for _, v := range []int{2, 1, 3} {
				must(t, s.Templates.Save(bg, NewTemplate(t, "yard-entry", v)))
			}
			got, err := s.Templates.Latest(bg, "yard-entry")
			must(t, err)
			if got.Version() != 3 {
				t.Fatalf("latest version %d", got.Version())
			}
		},
		"list latest gives one row per template, by id, after a keyset id": func(t *testing.T, s Store) {
			for _, id := range []string{"b-check", "a-check", "c-check"} {
				must(t, s.Templates.Save(bg, NewTemplate(t, id, 1)))
				must(t, s.Templates.Save(bg, NewTemplate(t, id, 2)))
			}
			all, err := s.Templates.ListLatest(bg, "", 10)
			must(t, err)
			if len(all) != 3 || all[0].ID() != "a-check" || all[2].ID() != "c-check" || all[0].Version() != 2 {
				t.Fatalf("ListLatest = %d rows", len(all))
			}
			rest, err := s.Templates.ListLatest(bg, "a-check", 1)
			must(t, err)
			if len(rest) != 1 || rest[0].ID() != "b-check" {
				t.Fatalf("ListLatest after a-check = %d rows", len(rest))
			}
		},
		"a policy's required inspections round trip": func(t *testing.T, s Store) {
			p, err := yardpolicy.Default("WH1")
			must(t, err)
			rules := []yardpolicy.InspectionRule{
				{Gate: shared.GatePreDock, TemplateID: "yard-entry"},
				{Gate: shared.GatePreLoad, TemplateID: "yard-entry", Purposes: []shared.Purpose{shared.PurposeLiveLoad}, TrailerKinds: []shared.TrailerKind{shared.KindReefer}},
			}
			must(t, p.Declare(yardpolicy.DeclareInput{LeaseSeconds: 300, SealMismatchAction: yardpolicy.Flag, RequiredInspections: rules}, func(string) bool { return true }))
			must(t, s.Policies.Save(bg, p))
			got, err := s.Policies.Find(bg, "WH1")
			must(t, err)
			if !reflect.DeepEqual(got.Snapshot(), p.Snapshot()) {
				t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), p.Snapshot())
			}
		},
	})
}
