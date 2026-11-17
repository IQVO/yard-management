package inspection_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

func TestJudge(t *testing.T) {
	r := func(id, tpl string, g shared.Gate, s inspection.Status) inspection.Record {
		return inspection.Record{InspectionID: id, TemplateID: tpl, Gate: g, Status: s}
	}
	pass, fail := inspection.StatusPassed, inspection.StatusFailed
	cases := []struct {
		name    string
		records []inspection.Record
		want    error
	}{
		{"nothing completed", nil, inspection.ErrInspectionRequired},
		{"passed", []inspection.Record{r("a", "t17", shared.GatePreDock, pass)}, nil},
		{"failed", []inspection.Record{r("a", "t17", shared.GatePreDock, fail)}, inspection.ErrInspectionFailed},
		{"another template", []inspection.Record{r("a", "t7", shared.GatePreDock, pass)}, inspection.ErrInspectionRequired},
		{"another gate", []inspection.Record{r("a", "t17", shared.GatePreLoad, pass)}, inspection.ErrInspectionRequired},
		{"on demand never opens a gate", []inspection.Record{r("a", "t17", shared.GateOnDemand, pass)}, inspection.ErrInspectionRequired},
		{"a re-inspection after a failure opens the gate", []inspection.Record{r("a", "t17", shared.GatePreDock, fail), r("b", "t17", shared.GatePreDock, pass)}, nil},
		{"a failure after a pass closes it again", []inspection.Record{r("a", "t17", shared.GatePreDock, pass), r("b", "t17", shared.GatePreDock, fail)}, inspection.ErrInspectionFailed},
		{"the latest of the right template decides", []inspection.Record{r("a", "t17", shared.GatePreDock, pass), r("b", "t7", shared.GatePreDock, fail)}, nil},
		{"the latest of the right gate decides", []inspection.Record{r("a", "t17", shared.GatePreDock, pass), r("b", "t17", shared.GatePreLoad, fail)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := inspection.Judge(shared.GatePreDock, "t17", c.records)
			if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	err := inspection.Judge(shared.GatePreDock, "t17", []inspection.Record{r("insp-9", "t17", shared.GatePreDock, fail)})
	if !strings.Contains(err.Error(), "insp-9") || !strings.Contains(err.Error(), "t17") {
		t.Errorf("the detail names the failed inspection: %v", err)
	}
	if err := inspection.Judge(shared.GatePreDeparture, "t17", nil); !strings.Contains(err.Error(), "PRE_DEPARTURE") {
		t.Errorf("the detail names the gate: %v", err)
	}
}

func TestGates(t *testing.T) {
	for g, want := range map[shared.Gate][2]bool{
		shared.GatePreDock: {true, true}, shared.GatePreLoad: {true, true}, shared.GatePreDeparture: {true, true},
		shared.GateOnDemand: {true, false}, "": {false, false}, "NEVER": {false, false},
	} {
		if g.Valid() != want[0] || g.Gated() != want[1] {
			t.Errorf("%q: valid=%v gated=%v", g, g.Valid(), g.Gated())
		}
	}
}
