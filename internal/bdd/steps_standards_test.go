package bdd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/yard-management/internal/application/outbox"
	"github.com/claudioed/yard-management/internal/domain/shared"
)

// standardFieldNames are the optional fields of ADR 0018.
var standardFieldNames = []string{"x12_status", "cbv_biz_step", "cbv_disposition"}

// Steps of the standard vocabulary feature (ADR 0018). They read the `data` the
// outbox row would carry: the event goes through outbox.Payload, the very
// function the Postgres publisher writes the row with, so what is asserted is the
// payload a partner would receive (the in-memory store keeps domain events).
func registerStandardsSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the latest "([^"]*)" event on the wire has "([^"]*)" "([^"]*)"$`, w.thenWireField)
	sc.Step(`^the latest "([^"]*)" event on the wire has no "([^"]*)"$`, w.thenNoWireField)
	sc.Step(`^the latest "([^"]*)" event on the wire is exactly its legacy payload$`, w.thenLegacyPayload)
	sc.Step(`^a consumer written before the standard fields decodes the latest "([^"]*)" event on the wire and ignores them$`, w.thenLegacyConsumer)
}

// wireData returns the data of the newest published event of the name, as the
// outbox row would carry it, next to the data of the event with no standard fields.
func (w *world) wireData(name string) (wire, legacy []byte, err error) {
	events := w.env.Store.Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].EventName() != name {
			continue
		}
		return wireAndLegacy(events[i])
	}
	return nil, nil, fmt.Errorf("no %s event was published (published: %v)", name, w.env.Store.EventNames())
}

func wireAndLegacy(e shared.Event) (wire, legacy []byte, err error) {
	payload, err := outbox.Payload(e)
	if err != nil {
		return nil, nil, err
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, nil, err
	}
	legacy, err = json.Marshal(e)
	return env.Data, legacy, err
}

func (w *world) wireDoc(name string) (map[string]any, string, error) {
	wire, _, err := w.wireData(name)
	if err != nil {
		return nil, "", err
	}
	var doc map[string]any
	if err := json.Unmarshal(wire, &doc); err != nil {
		return nil, "", err
	}
	return doc, string(wire), nil
}

func (w *world) thenWireField(name, field, want string) error {
	doc, raw, err := w.wireDoc(name)
	if err != nil {
		return err
	}
	if got, present := doc[field]; !present || got != want {
		return fmt.Errorf("the %s event carries %s = %v (present %v), want %q: %s", name, field, got, present, want, raw)
	}
	return nil
}

// thenNoWireField: the field is OMITTED, not null and not empty.
func (w *world) thenNoWireField(name, field string) error {
	doc, raw, err := w.wireDoc(name)
	if err != nil {
		return err
	}
	if got, present := doc[field]; present {
		return fmt.Errorf("the %s event carries %s = %v, want it omitted: %s", name, field, got, raw)
	}
	return nil
}

// thenLegacyPayload: the data is byte-for-byte what json.Marshal of the event
// wrote before the standard fields existed, and none of them is present.
func (w *world) thenLegacyPayload(name string) error {
	wire, legacy, err := w.wireData(name)
	if err != nil {
		return err
	}
	if string(wire) != string(legacy) {
		return fmt.Errorf("the %s payload changed:\n wire:   %s\n legacy: %s", name, wire, legacy)
	}
	for _, f := range standardFieldNames {
		if strings.Contains(string(wire), `"`+f+`"`) {
			return fmt.Errorf("the %s payload carries %s: %s", name, f, wire)
		}
	}
	return nil
}

// legacyCheckedIn is what a consumer written before ADR 0018 decodes from
// VisitCheckedIn (the shape inbound-receiving reads): it knows none of the
// standard fields and, like every fleet consumer, tolerates unknown ones.
type legacyCheckedIn struct {
	VisitID       string  `json:"visit_id"`
	SiteID        string  `json:"site_id"`
	Carrier       string  `json:"carrier"`
	CarrierSCAC   *string `json:"carrier_scac"`
	Purpose       string  `json:"purpose"`
	AppointmentID *string `json:"appointment_id"`
	ArrivedAt     string  `json:"arrived_at"`
	Version       int64   `json:"version"`
}

func (w *world) thenLegacyConsumer(name string) error {
	doc, raw, err := w.wireDoc(name)
	if err != nil {
		return err
	}
	var with legacyCheckedIn
	if err := json.Unmarshal([]byte(raw), &with); err != nil {
		return fmt.Errorf("a consumer written before the standard fields cannot decode %s: %w", raw, err)
	}
	for _, f := range standardFieldNames {
		if _, present := doc[f]; f == "x12_status" && !present {
			return fmt.Errorf("the scenario needs a payload that carries the standard fields: %s", raw)
		}
		delete(doc, f)
	}
	stripped, _ := json.Marshal(doc)
	var without legacyCheckedIn
	if err := json.Unmarshal(stripped, &without); err != nil {
		return err
	}
	if with.VisitID == "" || with.SiteID != "WH1" || with.Version != 1 || !reflect.DeepEqual(with, without) {
		return fmt.Errorf("the standard fields changed what the old consumer reads:\n with:    %+v\n without: %+v", with, without)
	}
	return nil
}
