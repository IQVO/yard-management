package bdd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

func registerGateSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the gate checks in visit "([^"]*)" for purpose ([A-Z_]+) with plate "([^"]*)"$`, w.whenCheckInNoTrailer)
	sc.Step(`^the gate checks in visit "([^"]*)" for purpose ([A-Z_]+) with plate "([^"]*)" and trailer "([^"]*)" \(([A-Z_]+), ([A-Z_]+)\)$`, w.whenCheckInTrailer)
	sc.Step(`^the gate checks in visit "([^"]*)" for purpose ([A-Z_]+) with plate "([^"]*)" and trailer "([^"]*)" \(([A-Z_]+), ([A-Z_]+)\) on appointment "([^"]*)"$`, w.whenCheckInAppointment)
	sc.Step(`^the gate checks in visit "([^"]*)" for purpose ([A-Z_]+) with plate "([^"]*)" and trailer "([^"]*)" \(([A-Z_]+), ([A-Z_]+)\) sealed "([^"]*)"$`, w.whenCheckInSealed)
	sc.Step(`^the gate checks in visit "([^"]*)" for purpose ([A-Z_]+) with plate "([^"]*)" and trailer "([^"]*)" \(([A-Z_]+), ([A-Z_]+)\) on appointment "([^"]*)" sealed "([^"]*)"$`, w.whenCheckInAppointmentSealed)
	sc.Step(`^the gate checks in visit "([^"]*)" for purpose ([A-Z_]+) with plate "([^"]*)" on appointment "([^"]*)" and no trailer$`, w.whenCheckInAppointmentNoTrailer)
	sc.Step(`^the gate rejects a visit with plate "([^"]*)" because ([A-Z_]+)$`, w.whenReject)
	sc.Step(`^the gate rejects a visit with plate "([^"]*)" and driver "([^"]*)" because ([A-Z_]+)$`, w.whenRejectWithDriver)
	sc.Step(`^visit "([^"]*)" checks out taking no trailers$`, w.whenCheckOutNone)
	sc.Step(`^visit "([^"]*)" checks out taking trailers "([^"]*)"$`, w.whenCheckOut)
}

// checkIn sends POST /visits and remembers the ids of the answer.
func (w *world) checkIn(alias, purpose, plate, unit, kind, load, appointment, seal string) reply {
	body := map[string]any{"siteId": w.site, "carrier": "Acme Freight", "carrierScac": "ACME", "tractorPlate": plate, "purpose": purpose}
	if appointment != "" {
		body["appointmentId"] = appointment
	}
	if unit != "" {
		trailer := map[string]any{"unitNumber": unit, "kind": kind, "loadState": load}
		if seal != "" {
			trailer["sealNumber"] = seal
		}
		body["trailers"] = []any{trailer}
	}
	r := w.send(http.MethodPost, "/visits", body)
	if r.Code == http.StatusCreated {
		w.recordIDs(alias, r)
	}
	return r
}

func (w *world) whenCheckInNoTrailer(alias, purpose, plate string) {
	w.checkIn(alias, purpose, plate, "", "", "", "", "")
}

func (w *world) whenCheckInTrailer(alias, purpose, plate, unit, kind, load string) {
	w.checkIn(alias, purpose, plate, unit, kind, load, "", "")
}

func (w *world) whenCheckInAppointment(alias, purpose, plate, unit, kind, load, appointment string) {
	w.checkIn(alias, purpose, plate, unit, kind, load, appointment, "")
}

func (w *world) whenCheckInSealed(alias, purpose, plate, unit, kind, load, seal string) {
	w.checkIn(alias, purpose, plate, unit, kind, load, "", seal)
}

func (w *world) whenCheckInAppointmentSealed(alias, purpose, plate, unit, kind, load, appointment, seal string) {
	w.checkIn(alias, purpose, plate, unit, kind, load, appointment, seal)
}

func (w *world) whenCheckInAppointmentNoTrailer(alias, purpose, plate, appointment string) {
	w.checkIn(alias, purpose, plate, "", "", "", appointment, "")
}

func (w *world) whenReject(plate, reason string) {
	w.send(http.MethodPost, "/visit-rejections", map[string]any{"siteId": w.site, "carrier": "Acme Freight", "tractorPlate": plate, "reason": reason})
}

func (w *world) whenRejectWithDriver(plate, driver, reason string) {
	w.send(http.MethodPost, "/visit-rejections", map[string]any{"siteId": w.site, "carrier": "Acme Freight", "tractorPlate": plate, "driverRef": driver, "reason": reason})
}

func (w *world) whenCheckOutNone(alias string) error { return w.checkOut(alias, nil) }

func (w *world) whenCheckOut(alias, units string) error { return w.checkOut(alias, list(units)) }

func (w *world) checkOut(alias string, units []string) error {
	visit, err := w.visitID(alias)
	if err != nil {
		return err
	}
	out := make([]map[string]string, 0, len(units))
	for _, u := range units {
		id, err := w.trailerID(u)
		if err != nil {
			return err
		}
		out = append(out, map[string]string{"trailerId": id})
	}
	w.send(http.MethodPost, "/visits/"+visit+"/check-outs", map[string]any{"trailersOut": out})
	return nil
}

func registerYardSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the supervisor requests a move of trailer "([^"]*)" to (SPOT|DOOR) "([^"]*)"$`, w.whenRequestMove)
	sc.Step(`^supervisors concurrently request moves of trailers "([^"]*)" and "([^"]*)" to (SPOT|DOOR) "([^"]*)"$`, w.whenRaceMoves)
	sc.Step(`^hostler "([^"]*)" claims the next move at site "([^"]*)"$`, w.whenClaim)
	sc.Step(`^hostler "([^"]*)" completes the move of trailer "([^"]*)"$`, w.whenHostlerCompletes)
	sc.Step(`^hostler "([^"]*)" completes the move of trailer "([^"]*)" at (SPOT|DOOR) "([^"]*)"(?: noting "([^"]*)")?$`, w.whenHostlerCompletesElsewhere)
	sc.Step(`^the gate clerk completes the move of trailer "([^"]*)"$`, w.whenClerkCompletes)
	sc.Step(`^the supervisor cancels the move of trailer "([^"]*)"$`, w.whenCancelMove)
	sc.Step(`^the dock operator starts door work on trailer "([^"]*)"$`, w.whenStartDoorWork)
	sc.Step(`^the dock operator completes door work on trailer "([^"]*)"$`, w.whenCompleteDoorWork)
	sc.Step(`^the supervisor places a ([A-Z_]+) hold on trailer "([^"]*)"$`, w.whenHold)
	sc.Step(`^the supervisor releases the hold on trailer "([^"]*)"$`, w.whenReleaseHold)
	sc.Step(`^the clock advances (\d+) (seconds|minutes)$`, w.whenClockAdvances)
	sc.Step(`^the lease expiry sweep runs$`, func() { w.send(http.MethodPost, "/move-lease-sweeps", nil) })
	sc.Step(`^the dwell sweep runs$`, func() { w.send(http.MethodPost, "/dwell-sweeps", nil) })
	sc.Step(`^inbound-receiving closes receipt "([^"]*)" for ASN "([^"]*)"$`, w.whenReceiptCloses)
}

func (w *world) whenRequestMove(unit, kind, code string) error {
	id, err := w.trailerID(unit)
	if err != nil {
		return err
	}
	w.send(http.MethodPost, "/moves", map[string]any{"trailerId": id, "to": map[string]string{"kind": kind, "code": code}})
	return nil
}

func (w *world) whenRaceMoves(first, second, kind, code string) error {
	w.racing = make([]reply, 2)
	done := make(chan struct{}, 2)
	for i, unit := range []string{first, second} {
		id, err := w.trailerID(unit)
		if err != nil {
			return err
		}
		go func() {
			w.racing[i] = w.call(http.MethodPost, "/moves", map[string]any{"trailerId": id, "to": map[string]string{"kind": kind, "code": code}}, "")
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	return nil
}

func (w *world) whenClaim(hostler, site string) {
	w.send(http.MethodPost, "/moves/claims", map[string]any{"siteId": site, "hostlerRef": hostler})
}

func (w *world) complete(unit string, body map[string]any) error {
	id, err := w.openMoveID(unit)
	if err != nil {
		return err
	}
	w.send(http.MethodPost, "/moves/"+id+"/completions", body)
	return nil
}

func (w *world) whenHostlerCompletes(hostler, unit string) error {
	return w.complete(unit, map[string]any{"hostlerRef": hostler})
}

func (w *world) whenHostlerCompletesElsewhere(hostler, unit, kind, code, note string) error {
	body := map[string]any{"hostlerRef": hostler, "actualLocation": map[string]string{"kind": kind, "code": code}}
	if note != "" {
		body["deviationNote"] = note
	}
	return w.complete(unit, body)
}

func (w *world) whenClerkCompletes(unit string) error { return w.complete(unit, nil) }

func (w *world) whenCancelMove(unit string) error {
	id, err := w.openMoveID(unit)
	if err != nil {
		return err
	}
	w.send(http.MethodPost, "/moves/"+id+"/cancellations", map[string]any{"reason": "supervisor decision"})
	return nil
}

func (w *world) trailerAction(unit, action string, body any) error {
	id, err := w.trailerID(unit)
	if err != nil {
		return err
	}
	w.send(http.MethodPost, "/trailers/"+id+"/"+action, body)
	return nil
}

func (w *world) whenStartDoorWork(unit string) error {
	return w.trailerAction(unit, "door-work-starts", nil)
}

func (w *world) whenCompleteDoorWork(unit string) error {
	return w.trailerAction(unit, "door-work-completions", map[string]any{"loadState": "EMPTY"})
}

func (w *world) whenHold(reason, unit string) error {
	return w.trailerAction(unit, "holds", map[string]any{"reason": reason})
}

func (w *world) whenReleaseHold(unit string) error {
	return w.trailerAction(unit, "hold-releases", nil)
}

func (w *world) whenClockAdvances(n int, unit string) {
	d := time.Duration(n) * time.Second
	if strings.HasPrefix(unit, "minute") {
		d = time.Duration(n) * time.Minute
	}
	w.env.Store.Advance(d)
}

func (w *world) whenReceiptCloses(receipt, asn string) error {
	if err := w.env.Yard.CloseReceipt(context.Background(), w.eventID(), receipt, asn, w.env.Store.Now()); err != nil {
		return fmt.Errorf("close receipt %s: %w", receipt, err)
	}
	return nil
}
