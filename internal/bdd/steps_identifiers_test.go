package bdd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"
)

// Steps of the standard identifiers feature (ADR 0012): a raw check-in body,
// the seal and unit number facts of a trailer, and the payload of a published
// event.
func registerIdentifierSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the gate checks in visit \"([^\"]*)\" with the body (\{.*\})$`, w.whenCheckInBody)
	sc.Step(`^trailer \"([^\"]*)\" has seal state ([A-Z]+)$`, w.thenSealState)
	sc.Step(`^trailer \"([^\"]*)\" has seal class ([A-Z]+)$`, w.thenSealClass)
	sc.Step(`^trailer \"([^\"]*)\" has no seal class$`, w.thenNoSealClass)
	sc.Step(`^the response field \"([^\"]*)\" contains \"([^\"]*)\"$`, w.thenFieldContains)
	sc.Step(`^the latest \"([^\"]*)\" event has field \"([^\"]*)\" (.+)$`, w.thenEventField)
}

func (w *world) whenCheckInBody(alias, body string) {
	w.last = w.call(http.MethodPost, "/visits", body, "")
	if w.last.Code == http.StatusCreated {
		w.recordIDs(alias, w.last)
	}
}

func (w *world) thenSealState(unit, want string) error {
	return w.thenSealFact(unit, "state", want)
}

func (w *world) thenSealClass(unit, want string) error {
	return w.thenSealFact(unit, "class", want)
}

func (w *world) thenNoSealClass(unit string) error {
	return w.thenSealFact(unit, "class", "")
}

func (w *world) thenSealFact(unit, field, want string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	seal, _ := t["seal"].(map[string]any)
	got := seal[field]
	if (want == "" && got != nil) || (want != "" && got != want) {
		return fmt.Errorf("trailer %s has seal %s %v, want %q", unit, field, got, want)
	}
	return nil
}

func (w *world) thenFieldContains(path, part string) error {
	got, _ := lookup(w.last.doc(), path).(string)
	if !strings.Contains(got, part) {
		return fmt.Errorf("field %q is %q, want it to contain %q", path, got, part)
	}
	return nil
}

// thenEventField looks at the data of the newest published event of the name:
// the json tags are the snake_case payload of apis/asyncapi.yaml.
func (w *world) thenEventField(name, path, expr string) error {
	events := w.env.Store.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventName() != name {
			continue
		}
		raw, err := json.Marshal(events[i])
		if err != nil {
			return err
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		if !present(doc, path) {
			return fmt.Errorf("the %s event has no field %q: %s", name, path, raw)
		}
		if got, want := lookup(doc, path), literal(expr); !sameValue(got, want) {
			return fmt.Errorf("the %s event field %q is %v, want %v", name, path, got, want)
		}
		return nil
	}
	return fmt.Errorf("no %s event was published (published: %v)", name, w.env.Store.EventNames())
}
