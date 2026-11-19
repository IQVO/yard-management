package architecture

// Standard-vocabulary fitness tests (ADR 0018). The mapping table in
// internal/domain/standards is the ONE place that decides which X12 214 code,
// which CBV business step and which CBV disposition a published fact maps to.
// These tests tie it to the two documents partners read, so none of the three can
// drift:
//
//   - every event type apis/asyncapi.yaml publishes has a decision in the table
//     (and the table decides nothing the contract does not publish);
//   - the contract declares each standard field, as an optional property, on
//     exactly the data schemas whose facts can carry it, never as required;
//   - ADR 0018 lists every event, every code and the sources it cites.
//
// The domain test (standards_test.go) ties the table to the Go event types.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/claudioed/yard-management/internal/domain/standards"
)

const ownTypePrefix = "com.warehouse.wms.yard-management."

// standardFieldOf maps a published field to the value the table carries for it.
var standardFieldOf = map[string]func(standards.Semantics) string{
	"x12_status":      func(s standards.Semantics) string { return s.X12Status },
	"cbv_biz_step":    func(s standards.Semantics) string { return s.CBVBizStep },
	"cbv_disposition": func(s standards.Semantics) string { return s.CBVDisposition },
}

// asyncAPI is the part of apis/asyncapi.yaml these tests read.
type asyncAPI struct {
	Components struct {
		Schemas map[string]struct {
			Required   []string       `yaml:"required"`
			Properties map[string]any `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadAsyncAPI(t *testing.T) (asyncAPI, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "apis", "asyncapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc asyncAPI
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("apis/asyncapi.yaml: %v", err)
	}
	return doc, string(raw)
}

// publishedEvents returns the event names of the types this service publishes,
// read from the full type strings the contract writes out.
func publishedEvents(contract string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range cloudEventTypeRx.FindAllString(contract, -1) {
		if !strings.HasPrefix(m, ownTypePrefix) {
			continue
		}
		name := m[strings.LastIndex(m, ".")+1:]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func TestEveryPublishedTypeOfTheContractHasAStandardsDecision(t *testing.T) {
	_, contract := loadAsyncAPI(t)
	published := publishedEvents(contract)
	decided := append([]string(nil), standards.Events()...)
	sort.Strings(decided)
	if len(published) != 23 {
		t.Fatalf("%d published types found in apis/asyncapi.yaml, want 23: the detector is broken", len(published))
	}
	if strings.Join(published, ",") != strings.Join(decided, ",") {
		t.Fatalf("%s", archViolation("contents",
			"every event type apis/asyncapi.yaml publishes has a row in internal/domain/standards, and the table decides nothing else",
			"contract publishes "+strings.Join(published, ", ")+"; the table decides "+strings.Join(decided, ", ")+
				". FIX: decide the new type in internal/domain/standards (a code, or NOT mapped with the reason) and in ADR 0018"))
	}
}

// expectedStandardFields lists the standard fields an event's data schema must
// declare: those the table maps for at least one variant of the event. A field
// that is never mapped is not declared, so the contract never promises a code the
// table cannot produce.
func expectedStandardFields(event string) map[string]bool {
	want := map[string]bool{}
	for _, d := range standards.Decisions() {
		if d.Event != event {
			continue
		}
		for field, value := range standardFieldOf {
			if value(d.Semantics) != "" {
				want[field] = true
			}
		}
	}
	return want
}

func TestTheContractDeclaresTheStandardFieldsOnTheRightSchemasAsOptional(t *testing.T) {
	doc, _ := loadAsyncAPI(t)
	checked := 0
	for _, event := range standards.Events() {
		schema, ok := doc.Components.Schemas[event+"Data"]
		if !ok {
			t.Errorf("%sData is missing from the contract", event)
			continue
		}
		want := expectedStandardFields(event)
		for field := range standardFieldOf {
			if _, declared := schema.Properties[field]; declared != want[field] {
				t.Errorf("%sData: %s declared = %v, want %v (a field is declared exactly where the table maps it)", event, field, declared, want[field])
			}
			for _, r := range schema.Required {
				if r == field {
					t.Errorf("%sData: %s must be optional (omitted when absent), not required", event, field)
				}
			}
		}
		checked++
	}
	if checked != 23 {
		t.Fatalf("%d data schemas were checked, want 23: the detector is broken", checked)
	}
}

func TestADR0018ListsTheMapping(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "adr", "0018-standard-vocabulary-on-published-facts.md"))
	if err != nil {
		t.Fatal(err)
	}
	adr := string(raw)
	for _, d := range standards.Decisions() {
		if !strings.Contains(adr, d.Event) {
			t.Errorf("ADR 0018 does not name the event %s", d.Event)
		}
		if d.Variant != "" && !strings.Contains(adr, d.Variant) {
			t.Errorf("ADR 0018 does not name the variant %s of %s", d.Variant, d.Event)
		}
		for _, code := range []string{d.X12Status, d.CBVBizStep, d.CBVDisposition} {
			if code != "" && !strings.Contains(adr, "`"+code+"`") {
				t.Errorf("ADR 0018 does not list the code `%s` of %s %s", code, d.Event, d.Variant)
			}
		}
	}
	for _, source := range []string{"stedi.com/edi/x12-004010/element/1650", "ref.gs1.org/cbv", "cn.ca"} {
		if !strings.Contains(adr, source) {
			t.Errorf("ADR 0018 does not cite its source %s", source)
		}
	}
}
