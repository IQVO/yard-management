package postgres

import (
	"encoding/json"

	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
)

// ruleJSON is one required inspection of a policy as stored in the JSONB column.
type ruleJSON struct {
	Gate         string   `json:"gate"`
	TemplateID   string   `json:"template_id"`
	Purposes     []string `json:"purposes"`
	TrailerKinds []string `json:"trailer_kinds"`
}

func encodeRules(rules []yardpolicy.InspectionRule) ([]byte, error) {
	out := make([]ruleJSON, 0, len(rules))
	for _, r := range rules {
		j := ruleJSON{Gate: string(r.Gate), TemplateID: r.TemplateID, Purposes: []string{}, TrailerKinds: []string{}}
		for _, p := range r.Purposes {
			j.Purposes = append(j.Purposes, string(p))
		}
		for _, k := range r.TrailerKinds {
			j.TrailerKinds = append(j.TrailerKinds, string(k))
		}
		out = append(out, j)
	}
	return json.Marshal(out)
}

func decodeRules(raw []byte) ([]yardpolicy.InspectionRule, error) {
	var in []ruleJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := make([]yardpolicy.InspectionRule, 0, len(in))
	for _, j := range in {
		r := yardpolicy.InspectionRule{Gate: shared.Gate(j.Gate), TemplateID: j.TemplateID}
		for _, p := range j.Purposes {
			r.Purposes = append(r.Purposes, shared.Purpose(p))
		}
		for _, k := range j.TrailerKinds {
			r.TrailerKinds = append(r.TrailerKinds, shared.TrailerKind(k))
		}
		out = append(out, r)
	}
	return out, nil
}
