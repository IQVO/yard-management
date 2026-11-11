package yardpolicy

import (
	"slices"

	"github.com/claudioed/yard-management/internal/domain/inspection"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// MaxInspectionRules bounds requiredInspections: a policy is read on every
// dock request, door work start and check-out.
const MaxInspectionRules = 20

// InspectionRule says that, at a gate, a trailer needs a PASSED inspection of a
// template (ADR 0015). Purposes and TrailerKinds narrow it: empty means every
// purpose or every kind. A rule judges the trailer's purpose (why its visit
// came) and its kind.
type InspectionRule struct {
	Gate         shared.Gate
	TemplateID   string
	Purposes     []shared.Purpose
	TrailerKinds []shared.TrailerKind
}

func (r InspectionRule) clone() InspectionRule {
	r.Purposes, r.TrailerKinds = slices.Clone(r.Purposes), slices.Clone(r.TrailerKinds)
	return r
}

// applies reports whether the rule covers a trailer of the purpose (the one of
// the visit that brought the trailer) and kind.
func (r InspectionRule) applies(purpose shared.Purpose, kind shared.TrailerKind) bool {
	return (len(r.Purposes) == 0 || slices.Contains(r.Purposes, purpose)) &&
		(len(r.TrailerKinds) == 0 || slices.Contains(r.TrailerKinds, kind))
}

// InspectionRules returns the rules the site asks for at gate that cover a
// trailer of the purpose and kind. A site that declared none gets none: the
// behaviour before ADR 0015.
func (p *Policy) InspectionRules(gate shared.Gate, purpose shared.Purpose, kind shared.TrailerKind) []InspectionRule {
	var out []InspectionRule
	for _, r := range p.inspections {
		if r.Gate == gate && r.applies(purpose, kind) {
			out = append(out, r.clone())
		}
	}
	return out
}

// RequiredInspections returns every rule of the policy (never nil).
func (p *Policy) RequiredInspections() []InspectionRule {
	out := make([]InspectionRule, 0, len(p.inspections))
	for _, r := range p.inspections {
		out = append(out, r.clone())
	}
	return out
}

func cloneRules(in []InspectionRule) []InspectionRule {
	out := make([]InspectionRule, 0, len(in))
	for _, r := range in {
		out = append(out, r.clone())
	}
	return out
}

// validateInspectionRules checks a declaration's requiredInspections: at most
// MaxInspectionRules, each on a real gate (not ON_DEMAND) with a template id
// of the right shape and only known purposes and kinds.
func validateInspectionRules(rules []InspectionRule) error {
	if len(rules) > MaxInspectionRules {
		return shared.Invalid("at most %d required inspections", MaxInspectionRules)
	}
	for _, r := range rules {
		if !r.Gate.Gated() {
			return shared.Invalid("a required inspection is for the gate PRE_DOCK, PRE_LOAD or PRE_DEPARTURE")
		}
		if !inspection.ValidCode(r.TemplateID) {
			return shared.Invalid("required inspection template id %q is not valid", r.TemplateID)
		}
		for _, purpose := range r.Purposes {
			if !purpose.Valid() {
				return shared.Invalid("required inspection purpose %q is not valid", purpose)
			}
		}
		for _, kind := range r.TrailerKinds {
			if !kind.Valid() {
				return shared.Invalid("required inspection trailer kind %q is not valid", kind)
			}
		}
	}
	return nil
}
