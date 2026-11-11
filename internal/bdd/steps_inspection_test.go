package bdd

import (
	"fmt"
	"net/http"

	"github.com/cucumber/godog"
)

// Steps of the compliance inspections feature (ADR 0015): the inspector's
// requests, the helpers that put a trailer through an inspection, and the
// aliases that let a scenario name an inspection it opened.
func registerInspectionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the custom template "([^"]*)" version (\d+) is published$`, w.givenTemplate)
	sc.Step(`^trailer "([^"]*)" of a ([A-Z_]+) visit is docked at door "([^"]*)"$`, w.givenDockedAs)
	sc.Step(`^trailer "([^"]*)" has a (PASSED|FAILED) (PRE_DOCK|PRE_LOAD|PRE_DEPARTURE|ON_DEMAND) inspection of template "([^"]*)"$`, w.givenInspected)
	sc.Step(`^the inspector opens a (PRE_DOCK|PRE_LOAD|PRE_DEPARTURE|ON_DEMAND) inspection "([^"]*)" of template "([^"]*)" on trailer "([^"]*)"$`, w.whenOpenInspection)
	sc.Step(`^the inspector records item "([^"]*)" of inspection "([^"]*)" as ([A-Z_]+)$`, w.whenRecordItem)
	sc.Step(`^the inspector records item "([^"]*)" of inspection "([^"]*)" as ([A-Z_]+) noting "([^"]*)" with evidence "([^"]*)"$`, w.whenRecordItemWithEvidence)
	sc.Step(`^the inspector records every item of inspection "([^"]*)" as PASS$`, w.whenPassAll)
	sc.Step(`^the inspector completes inspection "([^"]*)"(?: with condition ([A-Z_]+))?$`, w.whenComplete)
	sc.Step(`^inspection "([^"]*)" has status ([A-Z]+)$`, w.thenInspectionStatus)
}

// yardEntryTemplate is the four-item custom checklist the scenarios use: three
// required items (one may be not applicable, one holds for SEAL_BROKEN) and an
// optional one.
const yardEntryTemplate = `{"title":"Yard entry check","items":[
  {"itemCode":"lights","text":"Lights work.","required":true},
  {"itemCode":"reefer-unit","text":"Reefer unit is sound.","required":true,"allowNotApplicable":true},
  {"itemCode":"seal-intact","text":"Seal is intact.","required":true,"holdReason":"SEAL_BROKEN"},
  {"itemCode":"paint","text":"Paint is fine.","required":false}]}`

func (w *world) givenTemplate(id string, version int) error {
	path := fmt.Sprintf("/inspection-templates/%s/versions/%d", id, version)
	return w.must(w.call(http.MethodPut, path, yardEntryTemplate, ""), http.StatusCreated, "publish template "+id)
}

func (w *world) givenDockedAs(unit, purpose, door string) error {
	if err := w.parkedAs(unit, purpose); err != nil {
		return err
	}
	return w.dockParked(unit, door)
}

// inspectionID resolves the alias a scenario gave to an inspection it opened.
func (w *world) inspectionID(alias string) (string, error) {
	id, ok := w.inspections[alias]
	if !ok {
		return "", fmt.Errorf("no inspection %q was opened", alias)
	}
	return id, nil
}

func (w *world) openInspection(alias, gate, template, unit string) (reply, error) {
	id, err := w.trailerID(unit)
	if err != nil {
		return reply{}, err
	}
	r := w.send(http.MethodPost, "/trailers/"+id+"/inspections", map[string]any{"templateId": template, "gate": gate, "performerRef": "emp-7"})
	if r.Code == http.StatusCreated {
		w.inspections[alias], _ = lookup(r.doc(), "inspectionId").(string)
	}
	return r, nil
}

func (w *world) whenOpenInspection(gate, alias, template, unit string) error {
	_, err := w.openInspection(alias, gate, template, unit)
	return err
}

func (w *world) recordItem(alias, item, result string, extra map[string]any) error {
	id, err := w.inspectionID(alias)
	if err != nil {
		return err
	}
	body := map[string]any{"result": result}
	for k, v := range extra {
		body[k] = v
	}
	w.send(http.MethodPut, "/inspections/"+id+"/items/"+item, body)
	return nil
}

func (w *world) whenRecordItem(item, alias, result string) error {
	return w.recordItem(alias, item, result, nil)
}

func (w *world) whenRecordItemWithEvidence(item, alias, result, note, evidence string) error {
	return w.recordItem(alias, item, result, map[string]any{"note": note, "evidenceRefs": list(evidence)})
}

// itemCodes lists the item codes of an inspection, with whether each is required.
func (w *world) itemCodes(alias string) (codes []string, required []bool, err error) {
	id, err := w.inspectionID(alias)
	if err != nil {
		return nil, nil, err
	}
	items, _ := lookup(w.call(http.MethodGet, "/inspections/"+id, nil, "").doc(), "items").([]any)
	for _, it := range items {
		m := it.(map[string]any)
		codes = append(codes, m["itemCode"].(string))
		req, _ := m["required"].(bool)
		required = append(required, req)
	}
	return codes, required, nil
}

func (w *world) whenPassAll(alias string) error {
	codes, _, err := w.itemCodes(alias)
	if err != nil {
		return err
	}
	id, _ := w.inspectionID(alias)
	for _, code := range codes {
		if err := w.must(w.call(http.MethodPut, "/inspections/"+id+"/items/"+code, map[string]any{"result": "PASS"}, ""), http.StatusOK, "record "+code); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) whenComplete(alias, grade string) error {
	id, err := w.inspectionID(alias)
	if err != nil {
		return err
	}
	var body any
	if grade != "" {
		body = map[string]any{"conditionGrade": grade}
	}
	w.send(http.MethodPost, "/inspections/"+id+"/completions", body)
	return nil
}

// givenInspected puts a trailer through a whole inspection: every item PASS,
// and for a FAILED one the first required item FAIL.
func (w *world) givenInspected(unit, outcome, gate, template string) error {
	alias := "given-" + unit + "-" + gate + "-" + template + "-" + outcome
	r, err := w.openInspection(alias, gate, template, unit)
	if err != nil {
		return err
	}
	if err := w.must(r, http.StatusCreated, "open the "+gate+" inspection"); err != nil {
		return err
	}
	if err := w.whenPassAll(alias); err != nil {
		return err
	}
	id, _ := w.inspectionID(alias)
	if outcome == "FAILED" {
		codes, required, _ := w.itemCodes(alias)
		for n, code := range codes {
			if required[n] {
				if err := w.must(w.call(http.MethodPut, "/inspections/"+id+"/items/"+code, map[string]any{"result": "FAIL", "note": "found by the inspector"}, ""), http.StatusOK, "fail "+code); err != nil {
					return err
				}
				break
			}
		}
	}
	return w.must(w.call(http.MethodPost, "/inspections/"+id+"/completions", nil, ""), http.StatusOK, "complete the inspection")
}

func (w *world) thenInspectionStatus(alias, status string) error {
	id, err := w.inspectionID(alias)
	if err != nil {
		return err
	}
	if got := lookup(w.call(http.MethodGet, "/inspections/"+id, nil, "").doc(), "status"); got != status {
		return fmt.Errorf("inspection %s is %v, want %s", alias, got, status)
	}
	return nil
}
