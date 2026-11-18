// Package standards is the ONE place that says which standard meaning a
// published yard-management fact corresponds to (ADR 0018): the ANSI X12 214
// shipment status code, the GS1 EPCIS Core Business Vocabulary (CBV) business
// step and the CBV disposition. Partners read those, not this context's private
// event names and enums.
//
// The rule is "only honest mappings": an event (for one deciding value, where
// the fact's meaning depends on one) maps to a code only when the standard's own
// definition says what the fact says. Everything else is decided NOT mapped,
// with the reason written next to it, and the published field is then omitted
// (never an invented, widened or approximate code). Every published event type
// has a decision here: the tests enumerate the types from the domain sources and
// the AsyncAPI contract, so a new type without a decision fails the build.
//
// Pure Go: no I/O, no clock, nothing outside the domain.
package standards

import "strings"

// ANSI X12 214 (Transportation Carrier Shipment Status Message) Shipment Status
// Codes (element 1650, carried in AT7-01), with the official names of the code
// list (stedi.com/edi/x12-004010/element/1650).
const (
	// X12InGate is I1, "In-Gate".
	X12InGate = "I1"
	// X12OutGate is OA, "Out-Gate".
	X12OutGate = "OA"
)

// GS1 Core Business Vocabulary 2.0 business steps (section 7.1) in their URN
// form, the one and only form this context publishes.
const (
	// CBVArriving: "an object arrives at a location" (example: "Truckload of a
	// shipment arrives into a yard").
	CBVArriving = "urn:epcglobal:cbv:bizstep:arriving"
	// CBVDeparting: "an object leaves a location on its way to a destination"
	// (example: "Truckload of a shipment departs a yard, typically through a gate").
	CBVDeparting = "urn:epcglobal:cbv:bizstep:departing"
	// CBVInspecting: "reviewing objects to address potential physical or
	// documentation defects".
	CBVInspecting = "urn:epcglobal:cbv:bizstep:inspecting"
)

// GS1 Core Business Vocabulary 2.0 dispositions (section 7.2) in their URN form.
const (
	// CBVConformant: "outcome of a successful/passed inspection in an
	// inspecting or repairing step".
	CBVConformant = "urn:epcglobal:cbv:disp:conformant"
	// CBVNonConformant: "outcome of an unsuccessful/failed inspection in an
	// inspecting or repairing step".
	CBVNonConformant = "urn:epcglobal:cbv:disp:non_conformant"
	// CBVDamaged: "object is impaired in its usefulness and/or reduced in value
	// due to a defect".
	CBVDamaged = "urn:epcglobal:cbv:disp:damaged"
	// CBVContainerOpen: "a container's doors have been opened; or a seal of a
	// shipment has been broken" (no indication whether the act was authorised).
	CBVContainerOpen = "urn:epcglobal:cbv:disp:container_open"
)

// Semantics is the standard meaning of one fact. An empty field means "no honest
// mapping" and is omitted from the published payload.
type Semantics struct {
	// X12Status is the X12 214 shipment status code, or "".
	X12Status string
	// CBVBizStep is the CBV business step URN, or "".
	CBVBizStep string
	// CBVDisposition is the CBV disposition URN, or "".
	CBVDisposition string
}

// IsZero reports whether nothing is mapped.
func (s Semantics) IsZero() bool { return s == Semantics{} }

// Decision is one row of the mapping table: the decision for an event, for one
// value of the field that decides it (or for every value when Variant is empty).
type Decision struct {
	// Event is the event name, e.g. "VisitCheckedIn".
	Event string
	// Variant is the value of the one data field the meaning depends on: the
	// `outcome` of InspectionCompleted, the `reason` of TrailerHeld. Empty for an
	// event whose meaning does not depend on its data.
	Variant string
	// Field is the json name of that data field, set exactly when Variant is.
	Field string
	Semantics
	// Reason says why a field is NOT mapped. It is empty exactly when all three
	// fields are mapped.
	Reason string
}

// The reasons a mapping is withheld, shared by several rows.
const (
	reasonNoDisposition = "a CBV disposition is the state the object is in after the event and this fact does not say one"
	reasonNoX12         = "X12 214 has no shipment status for this"
	reasonNoHoldingStep = "CBV holding is an object segregated to a holding area and a hold here is a state of the trailer, wherever it stands"
	reasonInternal      = "an internal yard movement or bookkeeping fact, not a gate or shipment event"
)

// reasons joins the clauses of a reason.
func reasons(clauses ...string) string { return strings.Join(clauses, "; ") }

// decisions is the mapping table. Order is the order of the ADR 0018 table.
var decisions = []Decision{
	{Event: "VisitCheckedIn", Semantics: Semantics{X12Status: X12InGate, CBVBizStep: CBVArriving}, Reason: reasonNoDisposition},
	{Event: "VisitRejected", Reason: reasons(
		"a visit turned away never passed the gate, so X12 I1 (In-Gate) would say it entered, and X12 A7 (Refused by Consignee) refuses a shipment, not an entry",
		"the CBV has no business step for refusing entry (entering_exiting is the retail entrance door)", reasonNoDisposition)},
	{Event: "VisitCheckedOut", Semantics: Semantics{X12Status: X12OutGate, CBVBizStep: CBVDeparting}, Reason: reasonNoDisposition},
	{Event: "TrailerParked", Reason: reasons(
		"X12 S1 (Trailer Spotted at Consignee's Location) is a drop at the consignee and a trailer rests on a yard spot for many other reasons",
		"CBV storing is an object moved into and out of storage within a location, not a trailer's position", reasonNoDisposition)},
	{Event: "TrailerDocked", Reason: reasons(
		"X12 X5 (Arrived at Delivery Location Loading Dock) and X8 (Arrived at Pick-up Location Loading Dock) differ by direction and the fact carries none: the trailer's direction is a yard inference (a load state, an appointment, a visit purpose) that is not on the fact",
		"the CBV has no business step for docking", reasonNoDisposition)},
	{Event: "TrailerUndocked", Reason: reasons(reasonNoX12, "the CBV has no business step for leaving a door", reasonNoDisposition)},
	{Event: "DoorWorkStarted", Reason: reasons(
		"X12 L1 (Loading) and CBV loading say a load, CBV unloading an unload, and the fact carries no direction (door work is one concept for both)",
		reasonNoDisposition)},
	{Event: "DoorWorkCompleted", Reason: reasons(
		"X12 D1 (Completed Unloading at Delivery Location), CP (Completed Loading at Pick-up Location) and CBV unloading and loading each say which of the two was done; the fact carries only the load state afterwards, recorded by the operator and not cross-checked against the work (and PARTIAL fits both)",
		reasonNoDisposition)},
	{Event: "TrailerDeparted", Semantics: Semantics{X12Status: X12OutGate, CBVBizStep: CBVDeparting}, Reason: reasons(
		"X12 AF (Carrier Departed Pick-up Location with Shipment) and CD (Carrier Departed Delivery Location) differ by direction and the fact carries none", reasonNoDisposition)},
	{Event: "SealExceptionRaised", Reason: reasons(
		"a seal number that differs from the expected one is not a broken seal (CBV container_open) and X12 214 has no seal status", "no business step: it is a finding, not an activity")},
	{Event: "TrailerDwellExceeded", Reason: reasons("a threshold alarm this context derives, not a status either standard defines", reasonNoDisposition)},
	{Event: "TrailerHeld", Field: "reason", Variant: "DAMAGED", Semantics: Semantics{CBVDisposition: CBVDamaged}, Reason: reasons(
		reasonNoX12, reasonNoHoldingStep)},
	{Event: "TrailerHeld", Field: "reason", Variant: "SEAL_BROKEN", Semantics: Semantics{CBVDisposition: CBVContainerOpen}, Reason: reasons(
		reasonNoX12, reasonNoHoldingStep)},
	{Event: "TrailerHeld", Field: "reason", Variant: "NOT_FOUND", Reason: reasons(
		reasonNoX12, reasonNoHoldingStep,
		"no CBV disposition says an object was not found where it was expected (unknown means its condition is not known)")},
	{Event: "TrailerHeld", Field: "reason", Variant: "QUALITY", Reason: reasons(
		reasonNoX12, reasonNoHoldingStep,
		"a quality hold may follow a failed inspection (non_conformant) or any other judgement, and the fact does not say which")},
	{Event: "TrailerHeld", Field: "reason", Variant: "SECURITY", Reason: reasons(
		reasonNoX12, reasonNoHoldingStep,
		"a security hold does not say the object is stolen or tampered with")},
	{Event: "TrailerHoldReleased", Reason: reasons(
		reasonNoX12, "CBV available is an object returned to service and a released hold does not say the cause was cleared", reasonNoDisposition)},
	{Event: "TrailerSecured", Reason: reasons(
		"a vehicle restraint or wheel chocks are neither a shipment status in X12 214 nor an activity in the CBV (container_closed is a loaded container with doors closed and a seal applied)",
		reasonNoDisposition)},
	{Event: "TrailerSecuringReleased", Reason: reasons(
		"a released restraint or removed chocks are neither a shipment status in X12 214 nor an activity in the CBV", reasonNoDisposition)},
	{Event: "YardMoveRequested", Reason: reasons(reasonInternal, "the CBV transporting is a move between locations by a vehicle, not a hostler's move within one yard")},
	{Event: "YardMoveClaimed", Reason: reasons(reasonInternal, reasonNoX12)},
	{Event: "YardMoveCompleted", Reason: reasons(reasonInternal, reasonNoX12)},
	{Event: "YardMoveCancelled", Reason: reasons(reasonInternal, reasonNoX12)},
	{Event: "YardMoveLeaseExpired", Reason: reasons(reasonInternal, reasonNoX12)},
	{Event: "InspectionCompleted", Field: "outcome", Variant: "PASSED", Semantics: Semantics{CBVBizStep: CBVInspecting, CBVDisposition: CBVConformant}, Reason: reasonNoX12},
	{Event: "InspectionCompleted", Field: "outcome", Variant: "FAILED", Semantics: Semantics{CBVBizStep: CBVInspecting, CBVDisposition: CBVNonConformant}, Reason: reasonNoX12},
	{Event: "InspectionFailed", Semantics: Semantics{CBVBizStep: CBVInspecting, CBVDisposition: CBVNonConformant}, Reason: reasons(
		reasonNoX12, "X12 reason code P2 (Waiting Inspection) is a reason, not a status, and does not say the inspection failed")},
	{Event: "YardCheckClosed", Reason: reasons(
		"a yard check reconciles trailers against spots, while CBV stock_taking and cycle_counting count objects within a location for inventory",
		reasonNoX12, reasonNoDisposition)},
}

// Decisions returns a copy of the mapping table.
func Decisions() []Decision { return append([]Decision(nil), decisions...) }

// Events returns every event name the table decides, once each, in table order.
func Events() []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range decisions {
		if !seen[d.Event] {
			seen[d.Event] = true
			out = append(out, d.Event)
		}
	}
	return out
}

// VariantField returns the json name of the data field the meaning of the event
// depends on (the Variant of its rows), or "" for an event decided as a whole.
func VariantField(event string) string {
	for _, d := range decisions {
		if d.Event == event && d.Field != "" {
			return d.Field
		}
	}
	return ""
}

// Of returns the standard meaning of an event. variant is the value of the one
// data field the event's meaning depends on (the outcome of InspectionCompleted,
// the reason of TrailerHeld) and "" for every other event. known is false when
// the event has no decision at all: the publisher then publishes no standard
// field, and the exhaustiveness tests make that state unreachable in a build that
// passes. An event decided per variant, asked for a variant it does not decide,
// is known and maps to nothing: the table never guesses.
func Of(event, variant string) (s Semantics, known bool) {
	for _, d := range decisions {
		if d.Event != event {
			continue
		}
		known = true
		if d.Variant == variant {
			return d.Semantics, true
		}
	}
	return Semantics{}, known
}
