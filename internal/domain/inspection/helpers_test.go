package inspection_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func at(min int) time.Time { return time.Date(2026, 11, 5, 14, min, 0, 0, time.UTC) }

// customTemplate is a four-item checklist: two required (one of which allows
// NOT_APPLICABLE), one required with its own hold reason and one optional.
func customTemplate(t *testing.T) *inspection.Template {
	t.Helper()
	tpl, err := inspection.Publish(inspection.PublishInput{
		ID: "yard-entry", Version: 2, Title: "Yard entry check", At: at(0),
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

func open(t *testing.T, gate shared.Gate) *inspection.Inspection {
	t.Helper()
	i, err := inspection.Open(inspection.OpenInput{
		ID: "insp-1", SiteID: "WH1", TrailerID: "trl-1", VisitID: "visit-1", Template: customTemplate(t), Gate: gate, PerformerRef: "emp-7", At: at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func rec(t *testing.T, i *inspection.Inspection, code string, r inspection.Result) {
	t.Helper()
	if err := i.RecordItem(code, inspection.ItemRecord{Result: r}); err != nil {
		t.Fatalf("RecordItem %s: %v", code, err)
	}
}

// allPass passes every required item of the custom template.
func allPass(t *testing.T, i *inspection.Inspection) {
	t.Helper()
	rec(t, i, "lights", inspection.ResultPass)
	rec(t, i, "reefer-unit", inspection.ResultNotApplicable)
	rec(t, i, "seal-intact", inspection.ResultPass)
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

func eq(t *testing.T, got, want map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("data\n got  %v\n want %v", got, want)
	}
}

func zero() time.Time { return time.Time{} }
