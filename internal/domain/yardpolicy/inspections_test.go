package yardpolicy_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

func declared(t *testing.T, rules ...yardpolicy.InspectionRule) *yardpolicy.Policy {
	t.Helper()
	p, _ := yardpolicy.Default("WH1")
	in := declareInput()
	in.RequiredInspections = rules
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoRequiredInspectionsIsTheDefault(t *testing.T) {
	p, _ := yardpolicy.Default("WH1")
	if got := p.RequiredInspections(); got == nil || len(got) != 0 {
		t.Fatalf("default rules %v", got)
	}
	if got := p.InspectionRules(shared.GatePreDock, shared.PurposeLiveUnload, shared.KindDryVan); len(got) != 0 {
		t.Fatalf("a default policy asks for nothing: %v", got)
	}
	q := declared(t)
	if len(q.RequiredInspections()) != 0 || len(q.Snapshot().RequiredInspections) != 0 {
		t.Fatal("declaring none keeps none")
	}
}

func TestInspectionRulesSelectByGatePurposeAndKind(t *testing.T) {
	p := declared(t,
		yardpolicy.InspectionRule{Gate: shared.GatePreDock, TemplateID: "t17"},
		yardpolicy.InspectionRule{Gate: shared.GatePreDock, TemplateID: "reefer", TrailerKinds: []shared.TrailerKind{shared.KindReefer}},
		yardpolicy.InspectionRule{Gate: shared.GatePreLoad, TemplateID: "t17"},
		yardpolicy.InspectionRule{Gate: shared.GatePreDeparture, TemplateID: "t7", Purposes: []shared.Purpose{shared.PurposePickup, shared.PurposeDrop}, TrailerKinds: []shared.TrailerKind{shared.KindContainer}},
	)
	ids := func(gate shared.Gate, purpose shared.Purpose, kind shared.TrailerKind) []string {
		out := []string{}
		for _, r := range p.InspectionRules(gate, purpose, kind) {
			out = append(out, r.TemplateID)
		}
		return out
	}
	for name, c := range map[string]struct {
		got, want []string
	}{
		"a dry van docks under the open rule":            {ids(shared.GatePreDock, shared.PurposeLiveUnload, shared.KindDryVan), []string{"t17"}},
		"a reefer also meets the reefer rule":            {ids(shared.GatePreDock, shared.PurposeLiveUnload, shared.KindReefer), []string{"t17", "reefer"}},
		"the load gate has its own rule":                 {ids(shared.GatePreLoad, shared.PurposeLiveUnload, shared.KindDryVan), []string{"t17"}},
		"the load gate does not see the dock rule":       {ids(shared.GatePreLoad, shared.PurposePickup, shared.KindReefer), []string{"t17"}},
		"departure needs the purpose and the kind":       {ids(shared.GatePreDeparture, shared.PurposeDrop, shared.KindContainer), []string{"t7"}},
		"departure of another purpose is not covered":    {ids(shared.GatePreDeparture, shared.PurposeLiveLoad, shared.KindContainer), []string{}},
		"departure of another kind is not covered":       {ids(shared.GatePreDeparture, shared.PurposePickup, shared.KindDryVan), []string{}},
		"on demand is never a gate":                      {ids(shared.GateOnDemand, shared.PurposePickup, shared.KindContainer), []string{}},
		"a purpose rule matches the second listed value": {ids(shared.GatePreDeparture, shared.PurposePickup, shared.KindContainer), []string{"t7"}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %v, want %v", name, c.got, c.want)
		}
	}
}

func TestRequiredInspectionsDoNotAlias(t *testing.T) {
	rule := yardpolicy.InspectionRule{Gate: shared.GatePreDock, TemplateID: "t17", Purposes: []shared.Purpose{shared.PurposeDrop}, TrailerKinds: []shared.TrailerKind{shared.KindReefer}}
	p := declared(t, rule)
	rule.Purposes[0], rule.TrailerKinds[0] = shared.PurposePickup, shared.KindFlatbed
	got := p.RequiredInspections()
	if got[0].Purposes[0] != shared.PurposeDrop || got[0].TrailerKinds[0] != shared.KindReefer {
		t.Fatal("Declare copies its input")
	}
	got[0].Purposes[0] = shared.PurposePickup
	if p.RequiredInspections()[0].Purposes[0] != shared.PurposeDrop {
		t.Fatal("RequiredInspections returns copies")
	}
	sel := p.InspectionRules(shared.GatePreDock, shared.PurposeDrop, shared.KindReefer)
	sel[0].TrailerKinds[0] = shared.KindFlatbed
	if p.RequiredInspections()[0].TrailerKinds[0] != shared.KindReefer {
		t.Fatal("InspectionRules returns copies")
	}
}

func TestRedeclaringWithoutRulesClearsThem(t *testing.T) {
	p := declared(t, yardpolicy.InspectionRule{Gate: shared.GatePreDock, TemplateID: "t17"})
	in := declareInput()
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Fatal(err)
	}
	if len(p.RequiredInspections()) != 0 {
		t.Fatal("a replacement without requiredInspections turns them off, like every optional field")
	}
}

func TestRequiredInspectionRefusals(t *testing.T) {
	twenty := func(n int) []yardpolicy.InspectionRule {
		out := make([]yardpolicy.InspectionRule, n)
		for i := range out {
			out[i] = yardpolicy.InspectionRule{Gate: shared.GatePreDock, TemplateID: "t17"}
		}
		return out
	}
	ok := yardpolicy.InspectionRule{Gate: shared.GatePreDock, TemplateID: "t17"}
	with := func(f func(*yardpolicy.InspectionRule)) []yardpolicy.InspectionRule {
		r := ok
		f(&r)
		return []yardpolicy.InspectionRule{r}
	}
	cases := map[string][]yardpolicy.InspectionRule{
		"on demand":        with(func(r *yardpolicy.InspectionRule) { r.Gate = shared.GateOnDemand }),
		"unknown gate":     with(func(r *yardpolicy.InspectionRule) { r.Gate = "LATER" }),
		"no gate":          with(func(r *yardpolicy.InspectionRule) { r.Gate = "" }),
		"blank template":   with(func(r *yardpolicy.InspectionRule) { r.TemplateID = "" }),
		"bad template":     with(func(r *yardpolicy.InspectionRule) { r.TemplateID = "Not A Code" }),
		"unknown purpose":  with(func(r *yardpolicy.InspectionRule) { r.Purposes = []shared.Purpose{"JOYRIDE"} }),
		"unknown kind":     with(func(r *yardpolicy.InspectionRule) { r.TrailerKinds = []shared.TrailerKind{"HOVERCRAFT"} }),
		"too many":         twenty(yardpolicy.MaxInspectionRules + 1),
		"template too big": with(func(r *yardpolicy.InspectionRule) { r.TemplateID = strings.Repeat("a", 65) }),
	}
	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			p, _ := yardpolicy.Default("WH1")
			in := declareInput()
			in.RequiredInspections = rules
			if err := p.Declare(in, known("H1", "H2")); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want validation, got %v", err)
			}
			if p.Declared() || p.Version() != 1 {
				t.Fatal("a refused declaration changes nothing")
			}
		})
	}
	p, _ := yardpolicy.Default("WH1")
	in := declareInput()
	in.RequiredInspections = twenty(yardpolicy.MaxInspectionRules)
	if err := p.Declare(in, known("H1", "H2")); err != nil {
		t.Fatalf("the maximum itself is valid: %v", err)
	}
}

func TestRequiredInspectionsSurviveSnapshotAndRestore(t *testing.T) {
	p := declared(t, yardpolicy.InspectionRule{Gate: shared.GatePreLoad, TemplateID: "t17", Purposes: []shared.Purpose{shared.PurposeLiveLoad}})
	back, err := yardpolicy.Restore(p.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.RequiredInspections(), p.RequiredInspections()) {
		t.Fatalf("round trip %+v", back.RequiredInspections())
	}
	s := p.Snapshot()
	s.RequiredInspections = []yardpolicy.InspectionRule{{Gate: shared.GateOnDemand, TemplateID: "t17"}}
	if _, err := yardpolicy.Restore(s); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("a stored row with a bad rule: %v", err)
	}
}
