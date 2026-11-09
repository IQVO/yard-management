package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/yard-management/internal/analytics/report"
)

// AnalyticsTopic is this service's OWN analytics topic (ADR 0004): every
// published type, the integration ones mirrored. The projector reads it.
const AnalyticsTopic = "warehouse.yard-management.analytics"

// EnvAnalyticsGroup names the environment variable that carries the
// projector's consumer group; boot fails naming it when blank.
const EnvAnalyticsGroup = "ANALYTICS_CONSUMER_GROUP"

// analyticsKinds maps the FULL CloudEvents type of the twelve published events
// the reports are built from to their report.Kind (ADR 0006: the first seven;
// ADR 0011, the KPI standard: the last five), byte-identical to the catalogue of
// ADR 0004 / apis/asyncapi.yaml. Every other type on the topic (parked, hold
// released, move requested/claimed/cancelled, ...) is ignored.
var analyticsKinds = map[string]report.Kind{
	analyticsType("yardvisit", report.KindVisitCheckedIn):   report.KindVisitCheckedIn,
	analyticsType("trailer", report.KindTrailerDocked):      report.KindTrailerDocked,
	analyticsType("trailer", report.KindTrailerUndocked):    report.KindTrailerUndocked,
	analyticsType("trailer", report.KindDoorWorkStarted):    report.KindDoorWorkStarted,
	analyticsType("trailer", report.KindDoorWorkCompleted):  report.KindDoorWorkCompleted,
	analyticsType("trailer", report.KindTrailerDeparted):    report.KindTrailerDeparted,
	analyticsType("yardmove", report.KindYardMoveCompleted): report.KindYardMoveCompleted,

	analyticsType("yardvisit", report.KindVisitCheckedOut):   report.KindVisitCheckedOut,
	analyticsType("yardvisit", report.KindVisitRejected):     report.KindVisitRejected,
	analyticsType("trailer", report.KindSealExceptionRaised): report.KindSealExceptionRaised,
	analyticsType("trailer", report.KindTrailerHeld):         report.KindTrailerHeld,
	analyticsType("yardcheck", report.KindYardCheckClosed):   report.KindYardCheckClosed,
}

// analyticsType is com.warehouse.wms.yard-management.<entity>.<EventName>. The
// literal mirrors the outbound encoder without importing an outbound package
// (inbound adapters never depend on outbound ones, arch test).
func analyticsType(entity string, k report.Kind) string {
	return "com.warehouse.wms.yard-management." + entity + "." + string(k)
}

// analyticsData is the union of the analytics payload fields the projection
// reads, hand-mirrored from apis/asyncapi.yaml: snake_case, timestamps as the
// RFC 3339 strings the publisher writes, nullable fields as pointers.
type analyticsData struct {
	VisitID         string             `json:"visit_id"`
	SiteID          string             `json:"site_id"`
	TrailerID       string             `json:"trailer_id"`
	DoorCode        string             `json:"door_code"`
	Trailers        []analyticsTrailer `json:"trailers"`
	ArrivedAt       string             `json:"arrived_at"`
	DockedAt        string             `json:"docked_at"`
	UndockedAt      string             `json:"undocked_at"`
	StartedAt       string             `json:"started_at"`
	CompletedAt     string             `json:"completed_at"`
	DepartedAt      string             `json:"departed_at"`
	DwellMinutes    int64              `json:"dwell_minutes"`
	MoveID          string             `json:"move_id"`
	Performer       string             `json:"performer"`
	HostlerRef      *string            `json:"hostler_ref"`
	DurationSeconds int64              `json:"duration_seconds"`

	// The KPI standard kinds (ADR 0011). discrepancies is a LIST in the
	// contract (the audit's findings); only its length is projected.
	Purpose       string            `json:"purpose"`
	OnSiteMinutes int64             `json:"on_site_minutes"`
	Reason        string            `json:"reason"`
	Version       int64             `json:"version"`
	YardCheckID   string            `json:"yard_check_id"`
	RejectedAt    string            `json:"rejected_at"`
	RaisedAt      string            `json:"raised_at"`
	ClosedAt      string            `json:"closed_at"`
	Discrepancies []json.RawMessage `json:"discrepancies"`
}

type analyticsTrailer struct {
	TrailerID  string `json:"trailer_id"`
	UnitNumber string `json:"unit_number"`
	Kind       string `json:"kind"`
}

// NewAnalyticsConsumer builds the projector's consumer (ADR 0006): it reads
// AnalyticsTopic under groupID (from ANALYTICS_CONSUMER_GROUP, never a literal)
// and folds each event into the analytical model through projection. A brand
// new group starts at the earliest offset, so the model is rebuilt from the
// retained history. It does not dial; a blank group is an error naming the
// variable.
//
// Outcomes per message: not a CloudEvents 1.0 message: WARN and commit past; a
// valid CloudEvent of another type: ignored; a known type with an unusable
// payload, or one the store deterministically rejects (report.ErrRejected):
// dead-lettered at once to "<topic>.dlq", then committed past; a transient
// failure: retried on the SAME message with capped backoff, never
// dead-lettered.
func NewAnalyticsConsumer(f Factory, groupID string, projection report.Projection, logger *slog.Logger) (*Consumer, error) {
	return NewAnalyticsConsumerForTopic(f, AnalyticsTopic, groupID, projection, logger)
}

// NewAnalyticsConsumerForTopic is NewAnalyticsConsumer on an explicit topic.
func NewAnalyticsConsumerForTopic(f Factory, topic, groupID string, projection report.Projection, logger *slog.Logger) (*Consumer, error) {
	handlers := make(map[string]HandlerFunc, len(analyticsKinds))
	for fullType, kind := range analyticsKinds {
		handlers[fullType] = func(ctx context.Context, e ce.Event) error { return project(ctx, projection, kind, e) }
	}
	return newConsumer("analytics consumer", topic, groupID, EnvAnalyticsGroup, f, handlers, logger)
}

// project maps one decoded CloudEvent of a known kind onto a report.Event and
// applies it. Every deterministic refusal is a dead-letter marker; anything
// else is transient.
func project(ctx context.Context, projection report.Projection, kind report.Kind, e ce.Event) error {
	pe, err := toReportEvent(kind, e)
	if err != nil {
		return &deadLetterError{cause: err}
	}
	if _, err := projection.Apply(ctx, pe); err != nil {
		if errors.Is(err, report.ErrRejected) {
			//dlq:deterministic ErrRejected is minted only by the pure event validation or a Postgres SQLSTATE 22/23 rejection (analyticsstore.classify) -- never by a cancelled context
			return &deadLetterError{cause: err}
		}
		return fmt.Errorf("analytics: project %s %s: %w", kind, e.ID(), err)
	}
	return nil
}

// toReportEvent validates a decoded CloudEvent of a known kind and maps it to
// a report.Event. Every error is deterministic: the same bytes can never pass.
func toReportEvent(kind report.Kind, evt ce.Event) (report.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return report.Event{}, fmt.Errorf("decode %s data: %w", kind, err)
	}
	e := report.Event{
		Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), SiteID: d.SiteID,
		TrailerID: d.TrailerID, VisitID: d.VisitID, DoorCode: d.DoorCode,
		DwellMinutes: d.DwellMinutes, MoveID: d.MoveID, Performer: d.Performer, DurationSeconds: d.DurationSeconds,
		Purpose: d.Purpose, OnSiteMinutes: d.OnSiteMinutes, Reason: d.Reason, Version: d.Version,
		YardCheckID: d.YardCheckID, Discrepancies: int64(len(d.Discrepancies)),
	}
	if kind == report.KindSealExceptionRaised && e.TrailerID == "" {
		// Rejected at the gate: no trailer was persisted, the subject is the visit.
		e.VisitID = evt.Subject()
	}
	if d.HostlerRef != nil {
		e.HostlerRef = *d.HostlerRef
	}
	for _, t := range d.Trailers {
		e.Arrivals = append(e.Arrivals, report.Arrival{TrailerID: t.TrailerID, UnitNumber: t.UnitNumber, Kind: t.Kind})
	}
	if err := parseInstants(&e, d); err != nil {
		return report.Event{}, fmt.Errorf("%s %s: %w", kind, evt.ID(), err)
	}
	if want := e.Subject(); want != evt.Subject() {
		return report.Event{}, fmt.Errorf("%s %s: payload identifies %q but the subject is %q", kind, evt.ID(), want, evt.Subject())
	}
	if err := e.Validate(); err != nil {
		return report.Event{}, err
	}
	return e, nil
}

// parseInstants reads the RFC 3339 timestamps of the payload into e; a blank
// one stays the zero time (validation decides whether the kind needs it).
func parseInstants(e *report.Event, d analyticsData) error {
	for _, f := range []struct {
		field string
		raw   string
		dst   *time.Time
	}{
		{"arrived_at", d.ArrivedAt, &e.ArrivedAt}, {"docked_at", d.DockedAt, &e.DockedAt}, {"undocked_at", d.UndockedAt, &e.UndockedAt},
		{"started_at", d.StartedAt, &e.StartedAt}, {"completed_at", d.CompletedAt, &e.CompletedAt}, {"departed_at", d.DepartedAt, &e.DepartedAt},
		{"rejected_at", d.RejectedAt, &e.RejectedAt}, {"raised_at", d.RaisedAt, &e.RaisedAt}, {"closed_at", d.ClosedAt, &e.ClosedAt},
	} {
		if f.raw == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, f.raw)
		if err != nil {
			return fmt.Errorf("%s %q is not an RFC 3339 timestamp", f.field, f.raw)
		}
		*f.dst = at.UTC()
	}
	return nil
}
