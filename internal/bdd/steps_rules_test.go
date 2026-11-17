package bdd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// registerRuleSteps adds the vocabulary of features/yard_rules.feature: the
// facts a sibling context feeds the yard (appointments, slots, zones), the
// gate variants the documentation names, a few read-side assertions (moves,
// the yard board, events) and the Givens that put a trailer in a situation.
func registerRuleSteps(sc *godog.ScenarioContext, w *world) {
	registerFeedSteps(sc, w)
	registerGateRuleSteps(sc, w)
	registerYardRuleSteps(sc, w)
	registerRuleThenSteps(sc, w)
}

func registerFeedSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^dock-scheduling cancels appointment "([^"]*)"$`, func(id string) error { return w.appointmentFact(id, views.ApptCancelled) })
	sc.Step(`^dock-scheduling records a no-show for appointment "([^"]*)"$`, func(id string) error { return w.appointmentFact(id, views.ApptNoShow) })
	sc.Step(`^a stale booking of appointment "([^"]*)" arrives late$`, w.staleBooking)
	sc.Step(`^appointment "([^"]*)" is booked at door "([^"]*)" for ASNs "([^"]*)" starting in (-?\d+) minutes$`, w.givenMultiAsnAppointment)
	sc.Step(`^the closure of door "([^"]*)" is lifted$`, w.givenClosureLifted)
	sc.Step(`^facility-layout delivers event "([^"]*)" registering spot "([^"]*)" in zone "([^"]*)"$`, w.deliverSpot)
	sc.Step(`^facility-layout delivers event "([^"]*)" registering zone "([^"]*)" for site "([^"]*)"$`, w.deliverZone)
	sc.Step(`^facility-layout delivers event "([^"]*)" decommissioning slot "([^"]*)"$`, w.deliverDecommission)
	sc.Step(`^the site "([^"]*)" has a spot "([^"]*)" in zone "([^"]*)"$`, w.givenExtraSpot)
	sc.Step(`^the gate works at site "([^"]*)"$`, func(site string) { w.site = site })
}

func (w *world) appointmentFact(id, status string) error {
	cur, ok, err := w.env.Store.Appointments().Get(w.env.Ctx, id)
	if err != nil || !ok {
		return fmt.Errorf("appointment %s is unknown (%v)", id, err)
	}
	fact := usecases.AppointmentFact{Appointment: views.Appointment{ID: id, Status: status, Version: cur.Version + 1}}
	return w.env.Yard.ApplyAppointment(w.env.Ctx, w.eventID(), fact)
}

// staleBooking replays a DockAppointmentBooked of version 1 (a new event id,
// an old version): the copy must keep what it has.
func (w *world) staleBooking(id string) error {
	fact := usecases.AppointmentFact{Appointment: views.Appointment{ID: id, Status: views.ApptBooked, Version: 1}}
	return w.env.Yard.ApplyAppointment(w.env.Ctx, w.eventID(), fact)
}

func (w *world) givenMultiAsnAppointment(id, door, asns, startsIn string) error {
	return w.book(views.Appointment{ID: id, Direction: "INBOUND", ASNNumbers: list(asns), CarrierSCAC: "ACME"}, door, startsIn)
}

func (w *world) givenClosureLifted(door string) error {
	return w.env.Yard.LiftClosure(w.env.Ctx, w.eventID(), "clo-"+door)
}

func (w *world) deliverSpot(eventID, code, zone string) error {
	return w.env.Yard.ApplySlot(w.env.Ctx, eventID, code, zone, "Yard", "", "")
}

func (w *world) deliverZone(eventID, zone, site string) error {
	return w.env.Yard.ApplyZone(w.env.Ctx, eventID, zone, site)
}

func (w *world) deliverDecommission(eventID, code string) error {
	return w.env.Yard.DecommissionSlot(w.env.Ctx, eventID, code)
}

// givenExtraSpot adds a spot of another parking zone to a seeded site.
func (w *world) givenExtraSpot(site, code, zone string) error {
	feed := w.env.Store.Locations()
	if err := feed.ApplyZone(w.env.Ctx, zone, site); err != nil {
		return err
	}
	return feed.ApplySlot(w.env.Ctx, code, zone, shared.Spot, "", shared.SpotKindUnknown)
}

func registerGateRuleSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the gate checks in a bobtail with plate "([^"]*)" for a carrier named with (\d+) characters$`, w.whenBobtailCarrierLength)
	sc.Step(`^the gate checks in a bobtail with plate "([^"]*)" for carrier "([^"]*)" with SCAC "([^"]*)"$`, w.whenBobtailCarrier)
	sc.Step(`^the gate checks in visit "([^"]*)" for purpose ([A-Z_]+) with plate "([^"]*)" and trailers "([^"]*)" \(([A-Z_]+), ([A-Z_]+)\)(?: on appointment "([^"]*)")?$`, w.whenCheckInMany)
	sc.Step(`^trailer "([^"]*)" is parked empty$`, w.givenParkedEmpty)
	sc.Step(`^trailer "([^"]*)" has departed$`, w.givenDeparted)
}

func (w *world) bobtail(plate, carrier, scac string) {
	body := map[string]any{"siteId": w.site, "carrier": carrier, "tractorPlate": plate, "purpose": "BOBTAIL"}
	if scac != "" {
		body["carrierScac"] = scac
	}
	w.send(http.MethodPost, "/visits", body)
}

func (w *world) whenBobtailCarrierLength(plate string, n int) {
	w.bobtail(plate, strings.Repeat("C", n), "")
}

func (w *world) whenBobtailCarrier(plate, carrier, scac string) { w.bobtail(plate, carrier, scac) }

// whenCheckInMany checks in one visit that brings several trailers of the same
// kind and load, optionally on an appointment, and remembers their ids.
func (w *world) whenCheckInMany(alias, purpose, plate, units, kind, load, appointment string) {
	trailers := []any{}
	for _, u := range list(units) {
		trailers = append(trailers, map[string]any{"unitNumber": u, "kind": kind, "loadState": load})
	}
	body := map[string]any{"siteId": w.site, "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": plate, "purpose": purpose, "trailers": trailers}
	if appointment != "" {
		body["appointmentId"] = appointment
	}
	if r := w.send(http.MethodPost, "/visits", body); r.Code == http.StatusCreated {
		w.recordIDs(alias, r)
	}
}

// givenParkedEmpty is givenParked for an EMPTY trailer.
func (w *world) givenParkedEmpty(unit string) error {
	r := w.checkIn(unit, "DROP", "PLATE-"+unit, unit, "DRY_VAN", "EMPTY", "", "")
	if err := w.must(r, http.StatusCreated, "check "+unit+" in"); err != nil {
		return err
	}
	id, err := w.openMoveID(unit)
	if err != nil {
		return err
	}
	return w.must(w.call(http.MethodPost, "/moves/"+id+"/completions", nil, ""), http.StatusOK, "park "+unit)
}

// givenDeparted parks the trailer, then a pickup visit seals it and takes it out.
func (w *world) givenDeparted(unit string) error {
	if err := w.givenParked(unit); err != nil {
		return err
	}
	pickup := "pick-" + unit
	if err := w.must(w.checkIn(pickup, "PICKUP", "PICK-"+unit, "", "", "", "", ""), http.StatusCreated, "check the pickup in"); err != nil {
		return err
	}
	id, _ := w.trailerID(unit)
	seal := map[string]string{"state": "INTACT", "sealNumber": "SL-" + unit}
	if err := w.must(w.call(http.MethodPost, "/trailers/"+id+"/seals", seal, ""), http.StatusOK, "seal "+unit); err != nil {
		return err
	}
	if err := w.checkOut(pickup, []string{unit}); err != nil {
		return err
	}
	return w.must(w.last, http.StatusOK, "check "+unit+" out")
}

func registerYardRuleSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the supervisor requests a move of trailer "([^"]*)" to (SPOT|DOOR) "([^"]*)" with priority band (-?\d+)$`, w.whenRequestMoveBand)
	sc.Step(`^hostler "([^"]*)" claims the next move at site "([^"]*)" and gets the move of trailer "([^"]*)"$`, w.whenClaimGetting)
	sc.Step(`^the dock operator completes door work on trailer "([^"]*)" leaving it ([A-Z_]+)$`, w.whenCompleteDoorWorkLeaving)
	sc.Step(`^the supervisor records seal "([^"]*)" on trailer "([^"]*)"$`, w.whenRecordSeal)
}

func (w *world) whenRequestMoveBand(unit, kind, code string, band int) error {
	id, err := w.trailerID(unit)
	if err != nil {
		return err
	}
	w.send(http.MethodPost, "/moves", map[string]any{"trailerId": id, "to": map[string]string{"kind": kind, "code": code}, "priorityBand": band})
	return nil
}

func (w *world) whenClaimGetting(hostler, site, unit string) error {
	w.whenClaim(hostler, site)
	if w.last.Code != http.StatusCreated {
		return fmt.Errorf("claim by %s: %d, want 201: %s", hostler, w.last.Code, w.last.Body)
	}
	want, err := w.trailerID(unit)
	if err != nil {
		return err
	}
	if got := lookup(w.last.doc(), "trailerId"); got != want {
		return fmt.Errorf("%s claimed the move of %v, want the move of %s (%s)", hostler, got, unit, want)
	}
	return nil
}

func (w *world) whenCompleteDoorWorkLeaving(unit, load string) error {
	return w.trailerAction(unit, "door-work-completions", map[string]any{"loadState": load})
}

func (w *world) whenRecordSeal(number, unit string) error {
	return w.trailerAction(unit, "seals", map[string]any{"state": "INTACT", "sealNumber": number})
}

func registerRuleThenSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the latest move of trailer "([^"]*)" has priority band (\d+) due in (\d+) minutes$`, w.thenMovePriority)
	sc.Step(`^trailer "([^"]*)" has no place$`, w.thenNoPlace)
	sc.Step(`^trailer "([^"]*)" is at version (\d+)$`, w.thenTrailerVersion)
	sc.Step(`^the latest "([^"]*)" event carries "([^"]*)" (.+)$`, w.thenEventCarries)
	sc.Step(`^the yard board of site "([^"]*)" shows trailer "([^"]*)" at (spot|door) "([^"]*)"$`, w.thenBoardShows)
	sc.Step(`^the yard board of site "([^"]*)" shows (spot|door) "([^"]*)" free$`, w.thenBoardFree)
	sc.Step(`^the yard board of site "([^"]*)" shows door "([^"]*)" (closed|open)$`, w.thenBoardClosure)
	sc.Step(`^the yard board of site "([^"]*)" lists (\d+) trailers? in move and (\d+) open moves?$`, w.thenBoardCounts)
}

func (w *world) thenMovePriority(unit string, band int, dueIn int) error {
	m, err := w.moveOf(unit, "")
	if err != nil {
		return err
	}
	if got := lookup(m, "priority.band"); got != float64(band) {
		return fmt.Errorf("the latest move of %s has band %v, want %d", unit, got, band)
	}
	due, _ := lookup(m, "priority.dueBy").(string)
	want := w.env.Store.Now().Add(time.Duration(dueIn) * time.Minute).UTC().Format(time.RFC3339)
	if due != want {
		return fmt.Errorf("the latest move of %s is due by %q, want %q", unit, due, want)
	}
	return nil
}

func (w *world) thenNoPlace(unit string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["location"] != nil {
		return fmt.Errorf("trailer %s is at %v, want no place", unit, t["location"])
	}
	return nil
}

func (w *world) thenTrailerVersion(unit string, want int) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["version"] != float64(want) {
		return fmt.Errorf("trailer %s is at version %v, want %d", unit, t["version"], want)
	}
	return nil
}

// thenEventCarries looks into the payload of the newest published event of
// the name (the data the outbox would carry).
func (w *world) thenEventCarries(name, field, expr string) error {
	var latest shared.Event
	for _, e := range w.env.Store.Events() {
		if e.EventName() == name {
			latest = e
		}
	}
	if latest == nil {
		return fmt.Errorf("event %s was not published (published: %v)", name, w.env.Store.EventNames())
	}
	raw, err := json.Marshal(latest)
	if err != nil {
		return err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if !present(doc, field) {
		return fmt.Errorf("event %s has no field %q: %s", name, field, raw)
	}
	if got, want := lookup(doc, field), literal(expr); !sameValue(got, want) {
		return fmt.Errorf("event %s field %q is %v, want %v", name, field, got, want)
	}
	return nil
}

func (w *world) board(site string) (map[string]any, error) {
	r := w.call(http.MethodGet, "/sites/"+site+"/yard-board", nil, "")
	if r.Code != http.StatusOK {
		return nil, fmt.Errorf("GET the yard board of %s: %d %s", site, r.Code, r.Body)
	}
	m, _ := r.doc().(map[string]any)
	return m, nil
}

// boardPlace finds the spot or door with the code on the board.
func (w *world) boardPlace(site, kind, code string) (map[string]any, error) {
	b, err := w.board(site)
	if err != nil {
		return nil, err
	}
	places, _ := b[kind+"s"].([]any)
	for _, p := range places {
		if m, ok := p.(map[string]any); ok && m["code"] == code {
			return m, nil
		}
	}
	return nil, fmt.Errorf("the yard board of %s has no %s %s", site, kind, code)
}

func (w *world) thenBoardShows(site, unit, kind, code string) error {
	p, err := w.boardPlace(site, kind, code)
	if err != nil {
		return err
	}
	if got := lookup(p, "trailer.unitNumber"); got != unit {
		return fmt.Errorf("the yard board shows %v at %s %s, want %s", got, kind, code, unit)
	}
	return nil
}

func (w *world) thenBoardFree(site, kind, code string) error {
	p, err := w.boardPlace(site, kind, code)
	if err != nil {
		return err
	}
	if p["trailer"] != nil {
		return fmt.Errorf("the yard board shows %v at %s %s, want it free", lookup(p, "trailer.unitNumber"), kind, code)
	}
	return nil
}

func (w *world) thenBoardClosure(site, code, state string) error {
	p, err := w.boardPlace(site, "door", code)
	if err != nil {
		return err
	}
	if p["closed"] != (state == "closed") {
		return fmt.Errorf("the yard board shows door %s closed=%v, want %s", code, p["closed"], state)
	}
	return nil
}

func (w *world) thenBoardCounts(site string, inMove, open int) error {
	b, err := w.board(site)
	if err != nil {
		return err
	}
	gotMove, _ := b["inMove"].([]any)
	gotOpen, _ := b["openMoves"].([]any)
	if len(gotMove) != inMove || len(gotOpen) != open {
		return fmt.Errorf("the yard board lists %d trailers in move and %d open moves, want %d and %d", len(gotMove), len(gotOpen), inMove, open)
	}
	return nil
}
