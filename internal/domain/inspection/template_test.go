package inspection_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

func publishInput() inspection.PublishInput {
	return inspection.PublishInput{
		ID: "yard-entry", Version: 1, Title: "Yard entry", At: at(0),
		Items: []inspection.TemplateItem{{Code: "lights", Text: "Lights work.", Required: true}},
	}
}

func TestPublishNormalisesAndExposesTheTemplate(t *testing.T) {
	tpl, err := inspection.Publish(publishInput())
	if err != nil {
		t.Fatal(err)
	}
	if tpl.ID() != "yard-entry" || tpl.Version() != 1 || tpl.Title() != "Yard entry" || tpl.BuiltIn() || !tpl.PublishedAt().Equal(at(0)) {
		t.Fatalf("template %+v", tpl.Snapshot())
	}
	items := tpl.Items()
	if len(items) != 1 || items[0].HoldReason != trailer.HoldQuality {
		t.Fatalf("an item without a hold reason defaults to QUALITY: %+v", items)
	}
	items[0].Code = "mutated"
	if tpl.Items()[0].Code != "lights" {
		t.Fatal("Items returns a copy")
	}
}

func TestPublishRefusals(t *testing.T) {
	long := strings.Repeat("a", 65)
	many := make([]inspection.TemplateItem, inspection.MaxItems+1)
	for n := range many {
		many[n] = inspection.TemplateItem{Code: "item-" + strings.Repeat("x", n%5) + string(rune('a'+n%26)) + string(rune('a'+n/26)), Text: "t"}
	}
	cases := map[string]func(*inspection.PublishInput){
		"blank id":            func(in *inspection.PublishInput) { in.ID = "" },
		"upper-case id":       func(in *inspection.PublishInput) { in.ID = "Yard" },
		"id starting hyphen":  func(in *inspection.PublishInput) { in.ID = "-yard" },
		"id too long":         func(in *inspection.PublishInput) { in.ID = long },
		"version zero":        func(in *inspection.PublishInput) { in.Version = 0 },
		"version too high":    func(in *inspection.PublishInput) { in.Version = 1_000_001 },
		"blank title":         func(in *inspection.PublishInput) { in.Title = " " },
		"title too long":      func(in *inspection.PublishInput) { in.Title = strings.Repeat("t", 121) },
		"no time":             func(in *inspection.PublishInput) { in.At = time.Time{} },
		"no items":            func(in *inspection.PublishInput) { in.Items = nil },
		"too many items":      func(in *inspection.PublishInput) { in.Items = many },
		"bad item code":       func(in *inspection.PublishInput) { in.Items[0].Code = "Lights" },
		"blank item text":     func(in *inspection.PublishInput) { in.Items[0].Text = "" },
		"item text too long":  func(in *inspection.PublishInput) { in.Items[0].Text = strings.Repeat("t", 301) },
		"bad hold reason":     func(in *inspection.PublishInput) { in.Items[0].HoldReason = "BORING" },
		"duplicate item code": func(in *inspection.PublishInput) { in.Items = append(in.Items, in.Items[0]) },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			in := publishInput()
			in.Items = append([]inspection.TemplateItem(nil), in.Items...)
			mod(&in)
			if _, err := inspection.Publish(in); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want validation, got %v", err)
			}
		})
	}
}

func TestPublishAcceptsTheBoundaries(t *testing.T) {
	in := publishInput()
	in.ID = strings.Repeat("a", 64)
	in.Version = 1_000_000
	in.Title = strings.Repeat("t", 120)
	in.Items = make([]inspection.TemplateItem, inspection.MaxItems)
	for n := range in.Items {
		in.Items[n] = inspection.TemplateItem{Code: "i" + string(rune('a'+n%26)) + string(rune('a'+n/26)), Text: strings.Repeat("x", 300)}
	}
	if _, err := inspection.Publish(in); err != nil {
		t.Fatalf("the limits themselves are valid: %v", err)
	}
}

func TestSameContentIgnoresVersionAndTime(t *testing.T) {
	a, _ := inspection.Publish(publishInput())
	in := publishInput()
	in.Version, in.At = 7, at(30)
	b, _ := inspection.Publish(in)
	if !a.SameContent(b) {
		t.Fatal("same title and items are the same content")
	}
	for name, mod := range map[string]func(*inspection.PublishInput){
		"title":    func(in *inspection.PublishInput) { in.Title = "Other" },
		"text":     func(in *inspection.PublishInput) { in.Items[0].Text = "Other." },
		"required": func(in *inspection.PublishInput) { in.Items[0].Required = false },
		"na":       func(in *inspection.PublishInput) { in.Items[0].AllowNotApplicable = true },
		"reason":   func(in *inspection.PublishInput) { in.Items[0].HoldReason = trailer.HoldSecurity },
		"extra": func(in *inspection.PublishInput) {
			in.Items = append(in.Items, inspection.TemplateItem{Code: "x", Text: "x"})
		},
	} {
		in := publishInput()
		in.Items = append([]inspection.TemplateItem(nil), in.Items...)
		mod(&in)
		c, err := inspection.Publish(in)
		if err != nil {
			t.Fatal(err)
		}
		if a.SameContent(c) {
			t.Errorf("a different %s is different content", name)
		}
	}
}

func TestTemplateSnapshotRoundTrip(t *testing.T) {
	tpl := customTemplate(t)
	back, err := inspection.RestoreTemplate(tpl.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !back.SameContent(tpl) || back.ID() != tpl.ID() || back.Version() != 2 || !back.PublishedAt().Equal(at(0)) || back.BuiltIn() {
		t.Fatalf("round trip %+v", back.Snapshot())
	}
	bad := tpl.Snapshot()
	bad.Items = nil
	if _, err := inspection.RestoreTemplate(bad); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("a stored template with no items: %v", err)
	}
}

func TestBuiltInTemplates(t *testing.T) {
	want := map[string]struct {
		items, notApplicable int
		holds                map[trailer.HoldReason]int
		mustHave             []string
	}{
		inspection.TractorTrailer17Point: {19, 1, map[trailer.HoldReason]int{trailer.HoldSecurity: 18, trailer.HoldQuality: 1},
			[]string{"bumper-tires-rims", "engine", "tires", "tractor-floor", "fuel-tanks", "cab-interior-sleeper", "air-tanks", "drive-shafts", "fifth-wheel", "undercarriage", "doors-outside-inside", "trailer-floor", "side-walls", "front-wall", "ceiling-roof", "refrigeration-unit", "exhaust", "pest-free", "door-hardware"}},
		inspection.EmptyContainer7Point: {10, 1, map[trailer.HoldReason]int{trailer.HoldSecurity: 9, trailer.HoldQuality: 1},
			[]string{"front-wall", "left-side", "right-side", "floor", "ceiling-roof", "doors-and-locking", "outside-undercarriage", "fan-housing", "pest-free", "door-hardware"}},
		inspection.ReeferPreCool: {6, 2, map[trailer.HoldReason]int{trailer.HoldDamaged: 1, trailer.HoldQuality: 5},
			[]string{"physical-condition", "sanitary-condition-washout", "no-previous-cargo-residue", "no-pest-infestation", "pre-cooled-to-set-temperature", "receiver-temperature-abuse-check"}},
	}
	got := inspection.BuiltIns()
	if len(got) != len(want) {
		t.Fatalf("%d built-in templates, want %d", len(got), len(want))
	}
	for _, tpl := range got {
		w, ok := want[tpl.ID()]
		if !ok {
			t.Fatalf("unexpected built-in %s", tpl.ID())
		}
		if !tpl.BuiltIn() || tpl.Version() != 1 || !tpl.PublishedAt().IsZero() || !inspection.IsBuiltInID(tpl.ID()) {
			t.Errorf("%s: built-in=%v version=%d published=%v", tpl.ID(), tpl.BuiltIn(), tpl.Version(), tpl.PublishedAt())
		}
		checkBuiltInItems(t, tpl, w.items, w.notApplicable, w.holds, w.mustHave)
	}
	if inspection.IsBuiltInID("yard-entry") || inspection.IsBuiltInID("") {
		t.Error("a custom id is not built in")
	}
}

func checkBuiltInItems(t *testing.T, tpl *inspection.Template, wantItems, wantNA int, wantHolds map[trailer.HoldReason]int, wantCodes []string) {
	t.Helper()
	items := tpl.Items()
	var codes []string
	na, holds := 0, map[trailer.HoldReason]int{}
	for _, it := range items {
		codes = append(codes, it.Code)
		if !it.Required {
			t.Errorf("%s/%s: every built-in item is required", tpl.ID(), it.Code)
		}
		if it.AllowNotApplicable {
			na++
		}
		holds[it.HoldReason]++
	}
	if len(items) != wantItems || na != wantNA || !equalCounts(holds, wantHolds) {
		t.Errorf("%s: %d items, %d not-applicable, holds %v; want %d, %d, %v", tpl.ID(), len(items), na, holds, wantItems, wantNA, wantHolds)
	}
	for n, code := range wantCodes {
		if codes[n] != code {
			t.Errorf("%s: item %d is %q, want %q", tpl.ID(), n, codes[n], code)
		}
	}
}

func equalCounts(a, b map[trailer.HoldReason]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
