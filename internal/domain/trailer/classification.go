package trailer

import (
	"fmt"
	"slices"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

// Classification is what a trailer carries, as far as the yard must treat it
// differently (ADR 0014): a US DOT hazard class, the UN number of the
// material, and whether the load needs temperature control. The zero
// Classification is a trailer with no such facts, which every legacy trailer
// is, and no placement rule applies to it.
type Classification struct {
	HazmatClass shared.HazmatClass
	UNNumber    shared.UNNumber
	// RequiresTemperatureControl is true for a refrigerated load that needs a
	// plug: a REEFER-kind trailer is NOT implied to need it, the flag is the
	// declaration.
	RequiresTemperatureControl bool
}

// NewClassification validates the declared facts: a known hazard class
// (ErrInvalidHazmatClass), a well-formed UN number (ErrInvalidUNNumber), and
// a UN number only together with a hazard class.
func NewClassification(hazmatClass, unNumber string, requiresTemperatureControl bool) (Classification, error) {
	class, err := shared.ParseHazmatClass(hazmatClass)
	if err != nil {
		return Classification{}, err
	}
	un, err := shared.ParseUNNumber(unNumber)
	if err != nil {
		return Classification{}, err
	}
	c := Classification{HazmatClass: class, UNNumber: un, RequiresTemperatureControl: requiresTemperatureControl}
	return c, c.Validate()
}

// Validate is the invariant of a Classification: a hazard class and a UN
// number are valid when present, and a UN number needs a hazard class.
func (c Classification) Validate() error {
	if _, err := shared.ParseHazmatClass(string(c.HazmatClass)); err != nil {
		return err
	}
	if _, err := shared.ParseUNNumber(string(c.UNNumber)); err != nil {
		return err
	}
	if c.UNNumber != "" && c.HazmatClass == "" {
		return shared.Invalid("a UN number %s needs a hazmat class", c.UNNumber)
	}
	return nil
}

// Declared reports whether the trailer carries any classification fact. Only
// a declared classification is judged by the placement rules.
func (c Classification) Declared() bool {
	return c.HazmatClass != "" || c.UNNumber != "" || c.RequiresTemperatureControl
}

// Classify replaces the classification (a full replacement, like the policy:
// a field left out is cleared). It reports changed=false, and changes
// nothing, when the trailer already has exactly this classification. A
// DEPARTED trailer is not classified. The placement rules judge a placement,
// so a trailer already parked is not moved by being classified.
func (t *Trailer) Classify(c Classification) (changed bool, err error) {
	if t.state == Departed {
		return false, t.illegal("Classify")
	}
	if err := c.Validate(); err != nil {
		return false, err
	}
	if t.class == c {
		return false, nil
	}
	t.class = c
	t.version++
	return true, nil
}

// Classification returns what the trailer carries.
func (t *Trailer) Classification() Classification { return t.class }

// SiteSpots are the facts about the spots of a trailer's site that the
// placement rules need (ADR 0014): whether the site has any active HAZMAT or
// REEFER_PLUG spot, and the site policy allowHazmatOnUnclassifiedSpots.
type SiteSpots struct {
	HasHazmat, HasReeferPlug bool
	// AllowHazmatOnUnclassified is the policy allowHazmatOnUnclassifiedSpots.
	AllowHazmatOnUnclassified bool
}

// SpotRule is the placement rule of one trailer at one site: which kinds of
// yard spot it may take. A trailer with no classification, or whose
// classification the site gives no rule for, is not Restricted.
type SpotRule struct {
	Restricted bool
	// Allowed are the spot kinds the trailer may take (SpotKindUnknown stands
	// for a spot with no kind); empty with Restricted means no spot at all.
	Allowed []shared.SpotKind
	// Why names the rule, for the detail of the refusal.
	Why string
}

// Allows reports whether the rule lets the trailer take a spot of kind k.
func (r SpotRule) Allows(k shared.SpotKind) bool {
	return !r.Restricted || slices.Contains(r.Allowed, k)
}

// Refusal is the error of a rule that allows no spot of the site at all (a
// hazmat trailer at a site with no HAZMAT spot whose policy does not allow
// unclassified ones); its detail names the rule.
func (r SpotRule) Refusal() error { return fmt.Errorf("%w: %s", ErrPlacementRuleViolated, r.Why) }

// SpotRule derives the placement rule of the trailer at a site (ADR 0014):
//
//   - a trailer with a hazard class takes only a HAZMAT spot; at a site with
//     no HAZMAT spot it takes a spot with no kind when the policy
//     allowHazmatOnUnclassifiedSpots says so, and none otherwise;
//   - a trailer that requires temperature control takes only a REEFER_PLUG
//     spot, when the site has any; with none it is not restricted;
//   - a trailer that is both is judged by the hazmat rule only: no spot kind
//     says "hazmat with power", and the stricter safety rule wins;
//   - doors are never restricted (the caller asks about a yard spot only).
func (t *Trailer) SpotRule(f SiteSpots) SpotRule {
	switch {
	case t.class.HazmatClass != "":
		return t.hazmatRule(f)
	case t.class.RequiresTemperatureControl && f.HasReeferPlug:
		return SpotRule{Restricted: true, Allowed: []shared.SpotKind{shared.SpotKindReeferPlug},
			Why: "a trailer that requires temperature control may only be placed on a REEFER_PLUG spot, and this site has them"}
	}
	return SpotRule{}
}

func (t *Trailer) hazmatRule(f SiteSpots) SpotRule {
	switch {
	case f.HasHazmat:
		return SpotRule{Restricted: true, Allowed: []shared.SpotKind{shared.SpotKindHazmat},
			Why: fmt.Sprintf("a trailer with hazmat class %s may only be placed on a HAZMAT spot", t.class.HazmatClass)}
	case f.AllowHazmatOnUnclassified:
		return SpotRule{Restricted: true, Allowed: []shared.SpotKind{shared.SpotKindUnknown},
			Why: fmt.Sprintf("the site has no HAZMAT spot, so a trailer with hazmat class %s may only be placed on a spot with no kind (allowHazmatOnUnclassifiedSpots)", t.class.HazmatClass)}
	}
	return SpotRule{Restricted: true,
		Why: fmt.Sprintf("a trailer with hazmat class %s may only be placed on a HAZMAT spot, the site has none and its policy does not allowHazmatOnUnclassifiedSpots", t.class.HazmatClass)}
}

// SpotRuleViolation is the refusal of a placement on a yard spot of the given
// kind, or nil when the rule allows it; the detail names the rule.
func (t *Trailer) SpotRuleViolation(code string, kind shared.SpotKind, f SiteSpots) error {
	rule := t.SpotRule(f)
	if rule.Allows(kind) {
		return nil
	}
	is := "not classified"
	if kind != shared.SpotKindUnknown {
		is = "a " + string(kind) + " spot"
	}
	return fmt.Errorf("%w: %s; spot %s is %s", ErrPlacementRuleViolated, rule.Why, code, is)
}
