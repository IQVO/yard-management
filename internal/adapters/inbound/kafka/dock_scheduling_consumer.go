package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
)

// DockSchedulingTopic is dock-scheduling's integration topic.
const DockSchedulingTopic = "warehouse.dock-scheduling.events"

// EnvDockSchedulingGroup names the environment variable that carries the
// consumer group of the dock-scheduling consumer; boot fails naming it when
// blank.
const EnvDockSchedulingGroup = "DOCK_SCHEDULING_CONSUMER_GROUP"

// The only dock-scheduling types the consumer acts on, byte-identical to the
// producer's AsyncAPI. DockAppointmentCheckedIn / Completed are analytics only
// and deliberately not consumed (X4). The carrier registry events feed the
// CarrierView local copy (ADR 0016).
const (
	TypeDockAppointmentBooked      = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked"
	TypeDockAppointmentRescheduled = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentRescheduled"
	TypeDockAppointmentCancelled   = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentCancelled"
	TypeDockAppointmentNoShow      = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentNoShow"
	TypeDoorClosureScheduled       = "com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureScheduled"
	TypeDoorClosureLifted          = "com.warehouse.wms.dock-scheduling.dockcalendar.DoorClosureLifted"
	TypeCarrierRegistered          = "com.warehouse.wms.dock-scheduling.carrier.CarrierRegistered"
	TypeCarrierSuspended           = "com.warehouse.wms.dock-scheduling.carrier.CarrierSuspended"
	TypeCarrierReinstated          = "com.warehouse.wms.dock-scheduling.carrier.CarrierReinstated"
)

// DockSchedulingUseCases are the use cases the dock-scheduling consumer drives;
// *usecases.Yard implements it.
type DockSchedulingUseCases interface {
	ApplyAppointment(ctx context.Context, eventID string, f usecases.AppointmentFact) error
	ScheduleClosure(ctx context.Context, eventID string, c views.Closure) error
	LiftClosure(ctx context.Context, eventID, closureID string) error
	ApplyCarrierRegistered(ctx context.Context, eventID string, c views.Carrier, at time.Time) error
	ApplyCarrierSuspended(ctx context.Context, eventID, scac, reason string, version int64, at time.Time) error
	ApplyCarrierReinstated(ctx context.Context, eventID, scac string, version int64, at time.Time) error
}

// appointmentData mirrors the union of the fields of the four appointment
// payloads (snake_case) the local copy relies on; each event fills a subset and
// the rest default. Nullable strings arrive as "".
type appointmentData struct {
	AppointmentID      string    `json:"appointment_id"`
	SiteID             string    `json:"site_id"`
	Direction          string    `json:"direction"`
	DoorCode           string    `json:"door_code"`
	PreviousDoorCode   string    `json:"previous_door_code"`
	CarrierSCAC        string    `json:"carrier_scac"`
	WindowStart        time.Time `json:"window_start"`
	WindowEnd          time.Time `json:"window_end"`
	ASNNumbers         []string  `json:"asn_numbers"`
	ShipmentRefs       []string  `json:"shipment_refs"`
	ExpectedSealNumber string    `json:"expected_seal_number"`
	ToleranceMinutes   int       `json:"arrival_tolerance_minutes"`
	Version            int64     `json:"version"`
}

func (d appointmentData) appointment(status string) views.Appointment {
	return views.Appointment{
		ID: d.AppointmentID, SiteID: d.SiteID, Direction: d.Direction, DoorCode: d.DoorCode, CarrierSCAC: d.CarrierSCAC,
		WindowStart: d.WindowStart.UTC(), WindowEnd: d.WindowEnd.UTC(), ASNNumbers: d.ASNNumbers, ShipmentRefs: d.ShipmentRefs,
		ExpectedSeal: d.ExpectedSealNumber, ToleranceMinutes: d.ToleranceMinutes, Status: status, Version: d.Version,
	}
}

// requireVersion is poison for a version below 1: the aggregate version starts
// at 1 and is the copy's ordering guard, so an event without it cannot apply.
func requireVersion(e ce.Event, v int64) error {
	if v < 1 {
		return poisonf("payload of %s carries no version (>= 1)", e.Type())
	}
	return nil
}

func requireWindow(e ce.Event, d appointmentData) error {
	if d.WindowStart.IsZero() || d.WindowEnd.IsZero() {
		return poisonf("payload of %s carries no window", e.Type())
	}
	return nil
}

func applyAppointment(uc DockSchedulingUseCases, status string, checks ...func(ce.Event, appointmentData) error) HandlerFunc {
	return on(func(ctx context.Context, e ce.Event, d appointmentData) error {
		if err := requireNonBlank(e, "appointment_id", d.AppointmentID); err != nil {
			return err
		}
		if err := requireVersion(e, d.Version); err != nil {
			return err
		}
		for _, check := range checks {
			if err := check(e, d); err != nil {
				return err
			}
		}
		a := d.appointment(status)
		if status == views.ApptBooked {
			a.ASNNumbers, a.ShipmentRefs = nonNil(a.ASNNumbers), nonNil(a.ShipmentRefs)
		}
		return uc.ApplyAppointment(ctx, e.ID(), usecases.AppointmentFact{Appointment: a, PreviousDoorCode: d.PreviousDoorCode})
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func needDoorAndWindow(e ce.Event, d appointmentData) error {
	if err := requireNonBlank(e, "door_code", d.DoorCode); err != nil {
		return err
	}
	return requireWindow(e, d)
}

// closureData mirrors DoorClosureScheduled / DoorClosureLifted (snake_case).
type closureData struct {
	SiteID    string    `json:"site_id"`
	ClosureID string    `json:"closure_id"`
	DoorCode  string    `json:"door_code"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
}

// carrierData mirrors the union of the fields of the three carrier payloads
// (snake_case); each event fills a subset and the rest default. A null reason,
// suspended_at or reinstated_at decodes as the zero value, the repo's rule for
// nullable payload fields.
type carrierData struct {
	SCAC         string    `json:"scac"`
	Name         string    `json:"name"`
	USDOT        string    `json:"usdot"`
	MC           string    `json:"mc"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	SuspendedAt  time.Time `json:"suspended_at"`
	ReinstatedAt time.Time `json:"reinstated_at"`
	Version      int64     `json:"version"`
}

// requireCarrier is poison without a SCAC (the producer's key and the copy's
// primary key) or a version: a carrier fact carrying neither can never apply.
func requireCarrier(e ce.Event, d carrierData) error {
	if err := requireNonBlank(e, "scac", d.SCAC); err != nil {
		return err
	}
	return requireVersion(e, d.Version)
}

// applyCarrierRegistered folds CarrierRegistered, the whole carrier snapshot,
// into the carrier copy (ADR 0016); the use case stamps the event time.
func applyCarrierRegistered(uc DockSchedulingUseCases) HandlerFunc {
	return on(func(ctx context.Context, e ce.Event, d carrierData) error {
		if err := requireCarrier(e, d); err != nil {
			return err
		}
		return uc.ApplyCarrierRegistered(ctx, e.ID(), views.Carrier{
			SCAC: d.SCAC, Name: d.Name, USDOT: d.USDOT, MC: d.MC, Status: d.Status, Version: d.Version,
		}, e.Time())
	})
}

// applyCarrierSuspended folds CarrierSuspended; the payload's reason and the
// event time reach the use case, the snapshot fields do not.
func applyCarrierSuspended(uc DockSchedulingUseCases) HandlerFunc {
	return on(func(ctx context.Context, e ce.Event, d carrierData) error {
		if err := requireCarrier(e, d); err != nil {
			return err
		}
		return uc.ApplyCarrierSuspended(ctx, e.ID(), d.SCAC, d.Reason, d.Version, e.Time())
	})
}

// applyCarrierReinstated folds CarrierReinstated.
func applyCarrierReinstated(uc DockSchedulingUseCases) HandlerFunc {
	return on(func(ctx context.Context, e ce.Event, d carrierData) error {
		if err := requireCarrier(e, d); err != nil {
			return err
		}
		return uc.ApplyCarrierReinstated(ctx, e.ID(), d.SCAC, d.Version, e.Time())
	})
}

// NewDockSchedulingConsumer consumes DockSchedulingTopic under groupID (from
// DOCK_SCHEDULING_CONSUMER_GROUP, never a literal) and keeps the appointments,
// door_closures and carriers local copies; Cancelled, NoShow and a reschedule
// to another door also run NoShowFreesHolding. It does not dial; a blank group
// is an error naming the variable.
func NewDockSchedulingConsumer(f Factory, groupID string, uc DockSchedulingUseCases, logger *slog.Logger) (*Consumer, error) {
	return NewDockSchedulingConsumerForTopic(f, DockSchedulingTopic, groupID, uc, logger)
}

// NewDockSchedulingConsumerForTopic is NewDockSchedulingConsumer on an
// explicit topic.
func NewDockSchedulingConsumerForTopic(f Factory, topic, groupID string, uc DockSchedulingUseCases, logger *slog.Logger) (*Consumer, error) {
	handlers := map[string]HandlerFunc{
		TypeDockAppointmentBooked:      applyAppointment(uc, views.ApptBooked, needDoorAndWindow),
		TypeDockAppointmentRescheduled: applyAppointment(uc, "", needDoorAndWindow),
		TypeDockAppointmentCancelled:   applyAppointment(uc, views.ApptCancelled),
		TypeDockAppointmentNoShow:      applyAppointment(uc, views.ApptNoShow),
		TypeCarrierRegistered:          applyCarrierRegistered(uc),
		TypeCarrierSuspended:           applyCarrierSuspended(uc),
		TypeCarrierReinstated:          applyCarrierReinstated(uc),
		TypeDoorClosureScheduled: on(func(ctx context.Context, e ce.Event, d closureData) error {
			if err := requireNonBlank(e, "closure_id", d.ClosureID, "door_code", d.DoorCode); err != nil {
				return err
			}
			if d.From.IsZero() || d.To.IsZero() {
				return poisonf("payload of %s carries no closure span", e.Type())
			}
			return uc.ScheduleClosure(ctx, e.ID(), views.Closure{
				ID: d.ClosureID, SiteID: d.SiteID, DoorCode: d.DoorCode, From: d.From.UTC(), To: d.To.UTC(),
			})
		}),
		TypeDoorClosureLifted: on(func(ctx context.Context, e ce.Event, d closureData) error {
			if err := requireNonBlank(e, "closure_id", d.ClosureID); err != nil {
				return err
			}
			return uc.LiftClosure(ctx, e.ID(), d.ClosureID)
		}),
	}
	return newConsumer("dock-scheduling consumer", topic, groupID, EnvDockSchedulingGroup, f, handlers, logger)
}
