package standards_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/standards"
)

// parseDomain parses every non-test Go file under the domain directory.
func parseDomain(t *testing.T, visit func(path string, file *ast.File)) {
	t.Helper()
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		visit(path, file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// embedsHeader reports whether a struct type embeds shared.Header: that is what
// makes a struct a published domain event.
func embedsHeader(st *ast.StructType) bool {
	for _, f := range st.Fields.List {
		sel, ok := f.Type.(*ast.SelectorExpr)
		if ok && len(f.Names) == 0 && sel.Sel.Name == "Header" {
			if id, isIdent := sel.X.(*ast.Ident); isIdent && id.Name == "shared" {
				return true
			}
		}
	}
	return false
}

// forEachType calls visit with every type declaration of the domain.
func forEachType(t *testing.T, visit func(ts *ast.TypeSpec)) {
	t.Helper()
	parseDomain(t, func(_ string, file *ast.File) {
		for _, decl := range file.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
				for _, spec := range gd.Specs {
					visit(spec.(*ast.TypeSpec))
				}
			}
		}
	})
}

// domainEventNames returns the name of every domain event: the struct types that
// embed shared.Header (the struct's name is the event name). A new event added
// to the domain shows up here without anyone listing it.
func domainEventNames(t *testing.T) []string {
	t.Helper()
	var names []string
	forEachType(t, func(ts *ast.TypeSpec) {
		if st, isStruct := ts.Type.(*ast.StructType); isStruct && embedsHeader(st) {
			names = append(names, ts.Name.Name)
		}
	})
	sort.Strings(names)
	return names
}

// typedConsts returns the string values of the constants of the named type in
// the domain: the universe of values a deciding field can take.
func typedConsts(t *testing.T, typeName string) []string {
	t.Helper()
	var out []string
	parseDomain(t, func(_ string, file *ast.File) {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				id, typed := vs.Type.(*ast.Ident)
				if !typed || id.Name != typeName || len(vs.Values) != 1 {
					continue
				}
				if lit, isLit := vs.Values[0].(*ast.BasicLit); isLit && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					out = append(out, v)
				}
			}
		}
	})
	sort.Strings(out)
	return out
}

// variantsOf lists the variants the table decides for an event.
func variantsOf(event string) []string {
	var out []string
	for _, d := range standards.Decisions() {
		if d.Event == event && d.Variant != "" {
			out = append(out, d.Variant)
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryPublishedEventHasADecision is the build-failing guard: a domain event
// type with no row in the mapping table (or a row for a type that no longer
// exists) fails here. Adding an event means deciding what it maps to, even if
// the decision is "nothing" with a reason.
func TestEveryPublishedEventHasADecision(t *testing.T) {
	declared := domainEventNames(t)
	if len(declared) != 23 {
		t.Fatalf("the domain declares %d event types %v; the detector or the catalogue changed, update this count deliberately", len(declared), declared)
	}
	decided := append([]string(nil), standards.Events()...)
	sort.Strings(decided)
	if strings.Join(declared, ",") != strings.Join(decided, ",") {
		t.Fatalf("the mapping table and the domain events disagree\n domain events: %v\n table events:  %v\nDecide every event type in internal/domain/standards (ADR 0018).", declared, decided)
	}
	for _, name := range declared {
		if _, known := standards.Of(name, ""); !known {
			t.Errorf("Of(%q) is not known", name)
		}
	}
}

// The deciding fields take values from closed sets in the domain: every value of
// the set is decided, and nothing else is.
func TestEveryVariantOfADecidingFieldIsDecided(t *testing.T) {
	holds := typedConsts(t, "HoldReason")
	if got := variantsOf("TrailerHeld"); strings.Join(got, ",") != strings.Join(holds, ",") || len(holds) != 5 {
		t.Errorf("TrailerHeld decides %v, the domain's hold reasons are %v", got, holds)
	}
	var completed []string
	for _, status := range typedConsts(t, "Status") {
		if status != "OPEN" { // an inspection that is still open raises no InspectionCompleted
			completed = append(completed, status)
		}
	}
	if got := variantsOf("InspectionCompleted"); strings.Join(got, ",") != strings.Join(completed, ",") || len(completed) != 2 {
		t.Errorf("InspectionCompleted decides %v, the domain's completed outcomes are %v", got, completed)
	}
}

func TestTheTableRowsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range standards.Decisions() {
		key := d.Event + "/" + d.Variant
		if (d.Variant == "") != (d.Field == "") {
			t.Errorf("%s: variant %q and field %q are set together or not at all", key, d.Variant, d.Field)
		}
		if seen[key] {
			t.Errorf("duplicate decision %s", key)
		}
		seen[key] = true
		for _, problem := range rowProblems(d) {
			t.Errorf("%s: %s", key, problem)
		}
	}
}

// rowProblems lists what is wrong with one row of the table.
func rowProblems(d standards.Decision) []string {
	var out []string
	allMapped := d.X12Status != "" && d.CBVBizStep != "" && d.CBVDisposition != ""
	if allMapped && d.Reason != "" {
		out = append(out, "every field is mapped, so there is no reason to give: "+d.Reason)
	}
	if !allMapped && d.Reason == "" {
		out = append(out, "a field is not mapped and no reason says why")
	}
	if d.CBVBizStep != "" && !strings.HasPrefix(d.CBVBizStep, "urn:epcglobal:cbv:bizstep:") {
		out = append(out, d.CBVBizStep+" is not a CBV business step URN (one form only)")
	}
	if d.CBVDisposition != "" && !strings.HasPrefix(d.CBVDisposition, "urn:epcglobal:cbv:disp:") {
		out = append(out, d.CBVDisposition+" is not a CBV disposition URN (one form only)")
	}
	if len(d.X12Status) != 0 && len(d.X12Status) != 2 {
		out = append(out, "X12 214 status codes are two characters, got "+d.X12Status)
	}
	return out
}

// An event is decided either for every value (one row, no variant) or per value
// (rows with a variant, never also a variant-free one).
func TestAnEventIsDecidedPerVariantOrNotAtAll(t *testing.T) {
	for _, name := range standards.Events() {
		free, withVariant := 0, 0
		for _, d := range standards.Decisions() {
			switch {
			case d.Event != name:
			case d.Variant == "":
				free++
			default:
				withVariant++
			}
		}
		if (free == 1 && withVariant != 0) || (free == 0 && withVariant == 0) || free > 1 {
			t.Errorf("%s: %d variant-free and %d variant rows", name, free, withVariant)
		}
	}
}

// TestTheMapping pins every decision: the code and, for a withheld mapping, the
// fact that nothing is published. This is the table of ADR 0018.
func TestTheMapping(t *testing.T) {
	const (
		arriving  = "urn:epcglobal:cbv:bizstep:arriving"
		departing = "urn:epcglobal:cbv:bizstep:departing"
		inspect   = "urn:epcglobal:cbv:bizstep:inspecting"
		disp      = "urn:epcglobal:cbv:disp:"
	)
	none := standards.Semantics{}
	cases := []struct {
		event, variant string
		want           standards.Semantics
	}{
		{"VisitCheckedIn", "", standards.Semantics{X12Status: "I1", CBVBizStep: arriving}},
		{"VisitRejected", "", none},
		{"VisitCheckedOut", "", standards.Semantics{X12Status: "OA", CBVBizStep: departing}},
		{"TrailerParked", "", none},
		{"TrailerDocked", "", none},
		{"TrailerUndocked", "", none},
		{"DoorWorkStarted", "", none},
		{"DoorWorkCompleted", "", none},
		{"TrailerDeparted", "", standards.Semantics{X12Status: "OA", CBVBizStep: departing}},
		{"SealExceptionRaised", "", none},
		{"TrailerDwellExceeded", "", none},
		{"TrailerHeld", "DAMAGED", standards.Semantics{CBVDisposition: disp + "damaged"}},
		{"TrailerHeld", "SEAL_BROKEN", standards.Semantics{CBVDisposition: disp + "container_open"}},
		{"TrailerHeld", "NOT_FOUND", none},
		{"TrailerHeld", "QUALITY", none},
		{"TrailerHeld", "SECURITY", none},
		{"TrailerHoldReleased", "", none},
		{"TrailerSecured", "", none},
		{"TrailerSecuringReleased", "", none},
		{"YardMoveRequested", "", none},
		{"YardMoveClaimed", "", none},
		{"YardMoveCompleted", "", none},
		{"YardMoveCancelled", "", none},
		{"YardMoveLeaseExpired", "", none},
		{"InspectionCompleted", "PASSED", standards.Semantics{CBVBizStep: inspect, CBVDisposition: disp + "conformant"}},
		{"InspectionCompleted", "FAILED", standards.Semantics{CBVBizStep: inspect, CBVDisposition: disp + "non_conformant"}},
		{"InspectionFailed", "", standards.Semantics{CBVBizStep: inspect, CBVDisposition: disp + "non_conformant"}},
		{"YardCheckClosed", "", none},
	}
	if len(cases) != len(standards.Decisions()) {
		t.Fatalf("%d cases for %d decision rows", len(cases), len(standards.Decisions()))
	}
	for _, c := range cases {
		got, known := standards.Of(c.event, c.variant)
		if !known || got != c.want {
			t.Errorf("Of(%s, %q) = %+v known=%v, want %+v", c.event, c.variant, got, known, c.want)
		}
		if got.IsZero() != (c.want == none) {
			t.Errorf("IsZero of %+v is wrong", got)
		}
	}
}

func TestOfAnUnknownEventOrVariant(t *testing.T) {
	if s, known := standards.Of("TrailerTeleported", ""); known || !s.IsZero() {
		t.Errorf("an undecided event must be unknown and map to nothing, got %+v known=%v", s, known)
	}
	// An event decided per variant, asked with no (or a wrong) variant, is known
	// but maps to nothing: the table never guesses.
	for _, variant := range []string{"", "SIDEWAYS", "damaged"} {
		if s, known := standards.Of("TrailerHeld", variant); !known || !s.IsZero() {
			t.Errorf("Of(TrailerHeld, %q) = %+v known=%v, want known and empty", variant, s, known)
		}
	}
	// A variant-free event asked for a variant is likewise known and empty.
	if s, known := standards.Of("VisitCheckedIn", "PASSED"); !known || !s.IsZero() {
		t.Errorf("Of(VisitCheckedIn, PASSED) = %+v known=%v, want known and empty", s, known)
	}
}

func TestDecisionsAndEventsAreCopiesInTableOrder(t *testing.T) {
	first := standards.Decisions()
	first[0].X12Status = "ZZ"
	if standards.Decisions()[0].X12Status == "ZZ" {
		t.Fatal("Decisions exposes the table itself: a caller could rewrite the mapping")
	}
	got := standards.Events()
	if len(got) != 23 || got[0] != "VisitCheckedIn" || got[11] != "TrailerHeld" || got[len(got)-1] != "YardCheckClosed" {
		t.Fatalf("Events() = %v, want 23 names, each once, in table order", got)
	}
}

// The official names of the codes, as the code lists publish them (stedi.com/edi/
// x12-004010/element/1650), so a typo in a constant fails.
func TestTheX12CodesAreTheOnesTheListDefines(t *testing.T) {
	for code, name := range map[string]string{
		standards.X12InGate:  "I1 In-Gate",
		standards.X12OutGate: "OA Out-Gate",
	} {
		if !strings.HasPrefix(name, code+" ") {
			t.Errorf("constant %q does not start the official name %q", code, name)
		}
	}
	// An independent vector: the AT7 segment of Canadian National's published
	// 214 example (cn.ca 214 implementation guide 4010), whose own annotation
	// reads "(I1=ingate)"; AT7-01 is the shipment status code.
	const at7 = "AT7*I1*NS***20250112*1430"
	if got := strings.Split(at7, "*")[1]; got != standards.X12InGate {
		t.Errorf("AT7-01 of the CN example is %q, the constant is %q", got, standards.X12InGate)
	}
}

// The CBV defines each value twice, as a URN and as the ref.gs1.org web URI, and
// calls them equivalent (CBV 2.0 sections 7.1.1 and 7.2.1): the payload after the
// prefix is the same string. The URN of "departing" is also the one the CBV's own
// EPCIS example writes.
func TestTheCBVValuesAreTheOnesTheVocabularyDefines(t *testing.T) {
	for urn, web := range map[string]string{
		standards.CBVArriving:      "https://ref.gs1.org/cbv/BizStep-arriving",
		standards.CBVDeparting:     "https://ref.gs1.org/cbv/BizStep-departing",
		standards.CBVInspecting:    "https://ref.gs1.org/cbv/BizStep-inspecting",
		standards.CBVConformant:    "https://ref.gs1.org/cbv/Disp-conformant",
		standards.CBVNonConformant: "https://ref.gs1.org/cbv/Disp-non_conformant",
		standards.CBVDamaged:       "https://ref.gs1.org/cbv/Disp-damaged",
		standards.CBVContainerOpen: "https://ref.gs1.org/cbv/Disp-container_open",
	} {
		kind := "bizstep:"
		webPrefix := "https://ref.gs1.org/cbv/BizStep-"
		if strings.HasPrefix(web, "https://ref.gs1.org/cbv/Disp-") {
			kind, webPrefix = "disp:", "https://ref.gs1.org/cbv/Disp-"
		}
		if want := "urn:epcglobal:cbv:" + kind + strings.TrimPrefix(web, webPrefix); urn != want {
			t.Errorf("constant %q is not the URN equivalent of %s (%s)", urn, web, want)
		}
	}
	if standards.CBVDeparting != "urn:epcglobal:cbv:bizstep:departing" {
		t.Errorf("CBVDeparting = %q, the CBV's own example writes urn:epcglobal:cbv:bizstep:departing", standards.CBVDeparting)
	}
}

// jsonName is the json name in a struct tag literal such as `json:"outcome"`.
func jsonName(tag *ast.BasicLit) string {
	raw, _ := strconv.Unquote(tag.Value)
	return strings.Split(strings.TrimPrefix(raw, `json:"`), `"`)[0]
}

// structTags returns the json names of the fields of the struct type of the
// named event.
func structTags(t *testing.T, event string) []string {
	t.Helper()
	var tags []string
	forEachType(t, func(ts *ast.TypeSpec) {
		st, isStruct := ts.Type.(*ast.StructType)
		if !isStruct || ts.Name.Name != event {
			return
		}
		for _, f := range st.Fields.List {
			if f.Tag != nil {
				tags = append(tags, jsonName(f.Tag))
			}
		}
	})
	return tags
}

// The field a variant is a value of is a field of the event: the publisher reads
// it from the wire data by that name.
func TestTheVariantFieldIsADataFieldOfTheEvent(t *testing.T) {
	checked := 0
	for _, name := range standards.Events() {
		field := standards.VariantField(name)
		if field == "" {
			continue
		}
		checked++
		found := false
		for _, tag := range structTags(t, name) {
			found = found || tag == field
		}
		if !found {
			t.Errorf("%s: the table reads the variant from data field %q, which the event does not have (%v)", name, field, structTags(t, name))
		}
	}
	if checked != 2 {
		t.Errorf("%d events are decided per variant, want 2 (TrailerHeld, InspectionCompleted)", checked)
	}
	if standards.VariantField("TrailerDeparted") != "" || standards.VariantField("NoSuchEvent") != "" {
		t.Error("an event decided as a whole, or an unknown one, has no variant field")
	}
	if standards.VariantField("TrailerHeld") != "reason" || standards.VariantField("InspectionCompleted") != "outcome" {
		t.Error("the variant fields are reason (TrailerHeld) and outcome (InspectionCompleted)")
	}
}
