package bdd

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/yard-management/internal/application/views"
)

func initializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.reset()
		return ctx, nil
	})
	registerGivenSteps(sc, w)
	registerGateSteps(sc, w)
	registerYardSteps(sc, w)
	registerRequestSteps(sc, w)
	registerThenSteps(sc, w)
	registerIdentifierSteps(sc, w)
	registerInterlockSteps(sc, w)
	registerPlacementSteps(sc, w)
}

func registerGivenSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a yard site "([^"]*)" with spots "([^"]*)" and doors "([^"]*)"$`, w.givenSite)
	sc.Step(`^the yard policy of site "([^"]*)" is (\{.*\})$`, w.givenPolicy)
	sc.Step(`^appointment "([^"]*)" is booked at door "([^"]*)" for ASN "([^"]*)" starting in (-?\d+) minutes(?: with expected seal "([^"]*)")?$`, w.givenInboundAppointment)
	sc.Step(`^appointment "([^"]*)" is booked at door "([^"]*)" for shipment "([^"]*)" starting in (-?\d+) minutes$`, w.givenOutboundAppointment)
	sc.Step(`^ASN "([^"]*)" is registered$`, w.givenASN)
	sc.Step(`^receipt "([^"]*)" is opened for ASN "([^"]*)" on appointment "([^"]*)" at door "([^"]*)"$`, w.givenReceipt)
	sc.Step(`^the door "([^"]*)" is closed for the next (\d+) hours?$`, w.givenClosure)
	sc.Step(`^trailer "([^"]*)" is parked$`, w.givenParked)
	sc.Step(`^trailer "([^"]*)" is docked at door "([^"]*)"$`, w.givenDocked)
}

func (w *world) eventID() string {
	w.keys++
	return "bdd-evt-" + strconv.Itoa(w.keys)
}

func (w *world) givenSite(site, spots, doors string) error {
	flows := map[string]string{}
	for _, d := range list(doors) {
		code, flow := d, "Both"
		if i := indexByte(d, ':'); i >= 0 {
			code, flow = d[:i], d[i+1:]
		}
		flows[code] = flow
	}
	w.env.Site(site, list(spots), flows)
	w.site = site
	return nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func (w *world) givenPolicy(site, body string) error {
	return w.must(w.call(http.MethodPut, "/sites/"+site+"/yard-policy", body, ""), http.StatusOK, "declare the yard policy")
}

func (w *world) siteOfDoor(door string) (string, error) {
	info, ok, err := w.env.Store.Locations().Get(context.Background(), door)
	if err != nil || !ok {
		return "", fmt.Errorf("door %s is not in the location copy (%v)", door, err)
	}
	return info.SiteCode, nil
}

func (w *world) book(a views.Appointment, door string, startsIn string) error {
	site, err := w.siteOfDoor(door)
	if err != nil {
		return err
	}
	minutes, _ := strconv.Atoi(startsIn)
	a.SiteID, a.DoorCode = site, door
	a.WindowStart = w.env.Store.Now().Add(time.Duration(minutes) * time.Minute)
	a.WindowEnd = a.WindowStart.Add(2 * time.Hour)
	w.env.Book(a)
	return nil
}

func (w *world) givenInboundAppointment(id, door, asn, startsIn, seal string) error {
	return w.book(views.Appointment{ID: id, Direction: "INBOUND", ASNNumbers: []string{asn}, ExpectedSeal: seal, CarrierSCAC: "ACME"}, door, startsIn)
}

func (w *world) givenOutboundAppointment(id, door, shipment, startsIn string) error {
	return w.book(views.Appointment{ID: id, Direction: "OUTBOUND", ShipmentRefs: []string{shipment}, CarrierSCAC: "ACME"}, door, startsIn)
}

func (w *world) givenASN(asn string) error {
	return w.env.Yard.RegisterAsn(context.Background(), w.eventID(), asn)
}

func (w *world) givenReceipt(id, asn, appointment, door string) error {
	return w.env.Yard.OpenReceipt(context.Background(), w.eventID(), views.Receipt{ID: id, ASNNumber: asn, AppointmentID: appointment, DoorCode: door})
}

func (w *world) givenClosure(door, hours string) error {
	site, err := w.siteOfDoor(door)
	if err != nil {
		return err
	}
	h, _ := strconv.Atoi(hours)
	now := w.env.Store.Now()
	return w.env.Yard.ScheduleClosure(context.Background(), w.eventID(), views.Closure{
		ID: "clo-" + door, SiteID: site, DoorCode: door, From: now.Add(-time.Minute), To: now.Add(time.Duration(h) * time.Hour),
	})
}

// givenParked checks a walk-in DROP visit in and has the driver park the trailer.
func (w *world) givenParked(unit string) error {
	r := w.checkIn(unit, "DROP", "PLATE-"+unit, unit, "DRY_VAN", "LOADED", "", "")
	if err := w.must(r, http.StatusCreated, "check "+unit+" in"); err != nil {
		return err
	}
	id, err := w.openMoveID(unit)
	if err != nil {
		return err
	}
	return w.must(w.call(http.MethodPost, "/moves/"+id+"/completions", nil, ""), http.StatusOK, "park "+unit)
}

// givenDocked parks the trailer, then a supervisor sends it to the door and a
// setup hostler delivers it.
func (w *world) givenDocked(unit, door string) error {
	if err := w.givenParked(unit); err != nil {
		return err
	}
	id, _ := w.trailerID(unit)
	body := map[string]any{"trailerId": id, "to": map[string]string{"kind": "DOOR", "code": door}}
	if err := w.must(w.call(http.MethodPost, "/moves", body, ""), http.StatusCreated, "request the move to "+door); err != nil {
		return err
	}
	claim := map[string]any{"siteId": w.site, "hostlerRef": "setup-hostler"}
	if err := w.must(w.call(http.MethodPost, "/moves/claims", claim, ""), http.StatusCreated, "claim the move"); err != nil {
		return err
	}
	moveID, err := w.openMoveID(unit)
	if err != nil {
		return err
	}
	return w.must(w.call(http.MethodPost, "/moves/"+moveID+"/completions", map[string]any{"hostlerRef": "setup-hostler"}, ""), http.StatusOK, "dock "+unit)
}
