package bdd

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

func registerRequestSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I send (GET|POST|PUT) "([^"]*)"$`, w.sendPlain)
	sc.Step(`^I send (GET|POST|PUT) "([^"]*)" with (\{.*\}|\[.*\])$`, w.sendWithBody)
	sc.Step(`^I send POST "([^"]*)" without an Idempotency-Key$`, w.sendKeyless)
	sc.Step(`^I send (POST|PUT) "([^"]*)" with the Idempotency-Key "([^"]*)" and body (.+)$`, w.sendKeyed)
	sc.Step(`^I send PUT "([^"]*)" with If-Match version (\d+) and body (.+)$`, w.sendIfMatch)
}

var placeholder = regexp.MustCompile(`<(visit|trailer|move) ([^>]+)>`)

// resolve replaces <visit A>, <trailer TR-1> and <move TR-1> (the open move of
// the trailer) with the ids the scenario met.
func (w *world) resolve(s string) (string, error) {
	var failure error
	out := placeholder.ReplaceAllStringFunc(s, func(m string) string {
		parts := placeholder.FindStringSubmatch(m)
		var id string
		var err error
		switch parts[1] {
		case "visit":
			id, err = w.visitID(parts[2])
		case "trailer":
			id, err = w.trailerID(parts[2])
		default:
			id, err = w.openMoveID(parts[2])
		}
		if err != nil {
			failure = err
		}
		return id
	})
	return out, failure
}

func (w *world) sendPlain(method, path string) error { return w.sendWithBody(method, path, "") }

func (w *world) sendWithBody(method, path, body string) error {
	path, err := w.resolve(path)
	if err != nil {
		return err
	}
	body, err = w.resolve(body)
	if err != nil {
		return err
	}
	w.send(method, path, body)
	return nil
}

func (w *world) sendKeyless(path string) {
	w.last = w.call(http.MethodPost, path, nil, "-")
}

func (w *world) sendKeyed(method, path, key, body string) error {
	path, err := w.resolve(path)
	if err != nil {
		return err
	}
	w.last = w.call(method, path, body, key)
	return nil
}

func (w *world) sendIfMatch(path, version, body string) {
	w.last = w.callWithHeader(http.MethodPut, path, body, "If-Match", `W/"`+version+`"`)
}

func registerThenSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the response status is (\d+)$`, w.thenStatus)
	sc.Step(`^the problem is "([^"]*)"$`, w.thenProblem)
	sc.Step(`^the response field "([^"]*)" is (.+)$`, w.thenField)
	sc.Step(`^the response has no field "([^"]*)"$`, w.thenNoField)
	sc.Step(`^the response list "([^"]*)" has (\d+) items?$`, w.thenListLen)
	sc.Step(`^the response header "([^"]*)" is "([^"]*)"$`, w.thenHeader)
	sc.Step(`^the response has no header "([^"]*)"$`, w.thenNoHeader)
	sc.Step(`^the response ETag is version (\d+)$`, w.thenETag)
	sc.Step(`^trailer "([^"]*)" is (PARKED|IN_MOVE|DOCKED|DEPARTED)(?: at (SPOT|DOOR) "([^"]*)")?$`, w.thenTrailerAt)
	sc.Step(`^trailer "([^"]*)" has door work ([A-Z_]+) and load state ([A-Z_]+)$`, w.thenDoorWork)
	sc.Step(`^trailer "([^"]*)" is on hold ([A-Z_]+)$`, w.thenOnHold)
	sc.Step(`^trailer "([^"]*)" is not on site$`, w.thenDeparted)
	sc.Step(`^the latest move of trailer "([^"]*)" is ([A-Z_]+)$`, w.thenMoveState)
	sc.Step(`^the latest move of trailer "([^"]*)" has reason ([A-Z_]+) and performer ([A-Z_]+)$`, w.thenMoveReason)
	sc.Step(`^the latest move of trailer "([^"]*)" goes to (SPOT|DOOR) "([^"]*)"$`, w.thenMoveTo)
	sc.Step(`^visit "([^"]*)" is (CHECKED_IN|CHECKED_OUT|REJECTED)$`, w.thenVisitState)
	sc.Step(`^appointment "([^"]*)" is marked ([A-Z_]+)$`, w.thenAppointment)
	sc.Step(`^the event "([^"]*)" was published(?: (\d+) times?)?$`, w.thenEvent)
	sc.Step(`^no "([^"]*)" event was published$`, w.thenNoEvent)
	sc.Step(`^the published events include, in order, "([^"]*)"$`, w.thenEventsInOrder)
	sc.Step(`^exactly one concurrent request succeeded and the other was refused with 409$`, w.thenRaceWon)
}

func (w *world) callWithHeader(method, path, body, header, value string) reply {
	return w.callHeaders(method, path, body, map[string]string{header: value})
}

func (w *world) thenStatus(want int) error {
	if w.last.Code != want {
		return fmt.Errorf("status %d, want %d: %s", w.last.Code, want, w.last.Body)
	}
	return nil
}

func (w *world) thenProblem(slug string) error {
	if got := w.last.slug(); got != slug {
		return fmt.Errorf("problem %q, want %q (%d %s)", got, slug, w.last.Code, w.last.Body)
	}
	if ct := w.last.Header.Get("Content-Type"); ct != "application/problem+json" {
		return fmt.Errorf("content type %q, want application/problem+json", ct)
	}
	return nil
}

func (w *world) thenField(path, expr string) error {
	doc := w.last.doc()
	if !present(doc, path) {
		return fmt.Errorf("the response has no field %q: %s", path, w.last.Body)
	}
	if got, want := lookup(doc, path), literal(expr); !sameValue(got, want) {
		return fmt.Errorf("field %q is %v, want %v", path, got, want)
	}
	return nil
}

func (w *world) thenNoField(path string) error {
	if present(w.last.doc(), path) {
		return fmt.Errorf("the response has field %q: %s", path, w.last.Body)
	}
	return nil
}

func (w *world) thenListLen(path string, want int) error {
	items, ok := lookup(w.last.doc(), path).([]any)
	if !ok {
		return fmt.Errorf("%q is not a list in %s", path, w.last.Body)
	}
	if len(items) != want {
		return fmt.Errorf("list %q has %d items, want %d", path, len(items), want)
	}
	return nil
}

func (w *world) thenHeader(name, want string) error {
	if got := w.last.Header.Get(name); got != want {
		return fmt.Errorf("header %s is %q, want %q", name, got, want)
	}
	return nil
}

func (w *world) thenETag(version string) error { return w.thenHeader("ETag", `W/"`+version+`"`) }

func (w *world) thenNoHeader(name string) error {
	if got := w.last.Header.Get(name); got != "" {
		return fmt.Errorf("header %s is %q, want none", name, got)
	}
	return nil
}

func (w *world) thenTrailerAt(unit, state, kind, code string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["state"] != state {
		return fmt.Errorf("trailer %s is %v, want %s", unit, t["state"], state)
	}
	if code != "" {
		loc, _ := t["location"].(map[string]any)
		if loc["kind"] != kind || loc["code"] != code {
			return fmt.Errorf("trailer %s is at %v, want %s %s", unit, loc, kind, code)
		}
	}
	return nil
}

func (w *world) thenDoorWork(unit, work, load string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["doorWork"] != work || t["loadState"] != load {
		return fmt.Errorf("trailer %s has door work %v and load %v, want %s and %s", unit, t["doorWork"], t["loadState"], work, load)
	}
	return nil
}

func (w *world) thenOnHold(unit, reason string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["hold"] != reason {
		return fmt.Errorf("trailer %s hold is %v, want %s", unit, t["hold"], reason)
	}
	return nil
}

func (w *world) thenDeparted(unit string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["state"] != "DEPARTED" {
		return fmt.Errorf("trailer %s is %v, want DEPARTED", unit, t["state"])
	}
	return nil
}

func (w *world) thenMoveState(unit, state string) error {
	m, err := w.moveOf(unit, "")
	if err != nil {
		return err
	}
	if m["state"] != state {
		return fmt.Errorf("the latest move of %s is %v, want %s", unit, m["state"], state)
	}
	return nil
}

func (w *world) thenMoveReason(unit, reason, performer string) error {
	m, err := w.moveOf(unit, "")
	if err != nil {
		return err
	}
	if m["reason"] != reason || m["performer"] != performer {
		return fmt.Errorf("the latest move of %s is %v by %v, want %s by %s", unit, m["reason"], m["performer"], reason, performer)
	}
	return nil
}

func (w *world) thenMoveTo(unit, kind, code string) error {
	m, err := w.moveOf(unit, "")
	if err != nil {
		return err
	}
	to, _ := m["to"].(map[string]any)
	if to["kind"] != kind || to["code"] != code {
		return fmt.Errorf("the latest move of %s goes to %v, want %s %s", unit, to, kind, code)
	}
	return nil
}

func (w *world) thenVisitState(alias, state string) error {
	id, err := w.visitID(alias)
	if err != nil {
		return err
	}
	r := w.call(http.MethodGet, "/visits/"+id, nil, "")
	if got := lookup(r.doc(), "state"); got != state {
		return fmt.Errorf("visit %s is %v, want %s", alias, got, state)
	}
	return nil
}

func (w *world) thenAppointment(id, status string) error {
	a, ok, err := w.env.Store.Appointments().Get(w.env.Ctx, id)
	if err != nil || !ok {
		return fmt.Errorf("appointment %s is unknown (%v)", id, err)
	}
	if a.Status != status {
		return fmt.Errorf("appointment %s is %s, want %s", id, a.Status, status)
	}
	return nil
}

func (w *world) thenEvent(name, times string) error {
	n := 0
	for _, e := range w.env.Store.EventNames() {
		if e == name {
			n++
		}
	}
	want, _ := strconv.Atoi(times)
	switch {
	case times == "" && n == 0:
		return fmt.Errorf("event %s was not published (published: %v)", name, w.env.Store.EventNames())
	case times != "" && n != want:
		return fmt.Errorf("event %s was published %d times, want %d", name, n, want)
	}
	return nil
}

func (w *world) thenNoEvent(name string) error {
	for _, e := range w.env.Store.EventNames() {
		if e == name {
			return fmt.Errorf("event %s was published", name)
		}
	}
	return nil
}

func (w *world) thenEventsInOrder(names string) error {
	want := list(names)
	for _, got := range w.env.Store.EventNames() {
		if len(want) > 0 && got == want[0] {
			want = want[1:]
		}
	}
	if len(want) > 0 {
		return fmt.Errorf("events %v were not published in order (published: %v)", want, w.env.Store.EventNames())
	}
	return nil
}

func (w *world) thenRaceWon() error {
	var created, conflicts int
	for _, r := range w.racing {
		switch r.Code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("a concurrent request answered %d: %s", r.Code, strings.TrimSpace(string(r.Body)))
		}
	}
	if created != 1 || conflicts != 1 {
		return fmt.Errorf("%d created and %d conflicts, want exactly one of each", created, conflicts)
	}
	return nil
}
