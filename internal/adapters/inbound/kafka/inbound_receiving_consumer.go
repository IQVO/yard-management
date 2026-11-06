package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/yard-management/internal/application/views"
)

// InboundReceivingTopic is inbound-receiving's integration topic.
const InboundReceivingTopic = "warehouse.inbound-receiving.events"

// EnvInboundReceivingGroup names the environment variable that carries the
// consumer group of the inbound-receiving consumer; boot fails naming it when
// blank.
const EnvInboundReceivingGroup = "INBOUND_RECEIVING_CONSUMER_GROUP"

// The only inbound-receiving types the consumer acts on, byte-identical to the
// producer's AsyncAPI. The topic also carries the legacy importer's
// com.warehouse.wms.inbound-receiving.dockappointment.* types (strangler stage
// A, dock-scheduling's concern), which dispatch ignores.
const (
	TypeASNRegistered = "com.warehouse.wms.inbound-receiving.asn.ASNRegistered"
	TypeASNCancelled  = "com.warehouse.wms.inbound-receiving.asn.ASNCancelled"
	TypeReceiptOpened = "com.warehouse.wms.inbound-receiving.receipt.ReceiptOpened"
	TypeReceiptClosed = "com.warehouse.wms.inbound-receiving.receipt.ReceiptClosed"
)

// InboundReceivingUseCases are the use cases the inbound-receiving consumer
// drives; *usecases.Yard implements it.
type InboundReceivingUseCases interface {
	RegisterAsn(ctx context.Context, eventID, asnNumber string) error
	CancelAsn(ctx context.Context, eventID, asnNumber string) error
	OpenReceipt(ctx context.Context, eventID string, r views.Receipt) error
	CloseReceipt(ctx context.Context, eventID, receiptID, asnNumber string, closedAt time.Time) error
}

// asnData mirrors the part of the snake_case ASNRegistered / ASNCancelled
// payloads this service needs (lines, supplier_ref, cartons... are ignored).
type asnData struct {
	ASNNumber string `json:"asn_number"`
}

// receiptData mirrors the part of the snake_case ReceiptOpened / ReceiptClosed
// payloads this service needs. appointment_id and door_code are omitted by the
// producer for a walk-in delivery and arrive as "".
type receiptData struct {
	ReceiptID     string    `json:"receipt_id"`
	ASNNumber     string    `json:"asn_number"`
	AppointmentID string    `json:"appointment_id"`
	DoorCode      string    `json:"door_code"`
	ClosedAt      time.Time `json:"closed_at"`
}

// NewInboundReceivingConsumer consumes InboundReceivingTopic under groupID
// (from INBOUND_RECEIVING_CONSUMER_GROUP, never a literal) and keeps the asns
// and receipts local copies; ReceiptClosed also runs AutoCompleteUnload (Y-7:
// the receipt joins to the trailer through its appointment, and door work
// completes only when every ASN on the trailer has a closed receipt). It does
// not dial; a blank group is an error naming the variable.
func NewInboundReceivingConsumer(f Factory, groupID string, uc InboundReceivingUseCases, logger *slog.Logger) (*Consumer, error) {
	return NewInboundReceivingConsumerForTopic(f, InboundReceivingTopic, groupID, uc, logger)
}

// NewInboundReceivingConsumerForTopic is NewInboundReceivingConsumer on an
// explicit topic.
func NewInboundReceivingConsumerForTopic(f Factory, topic, groupID string, uc InboundReceivingUseCases, logger *slog.Logger) (*Consumer, error) {
	handlers := map[string]HandlerFunc{
		TypeASNRegistered: on(func(ctx context.Context, e ce.Event, d asnData) error {
			if err := requireNonBlank(e, "asn_number", d.ASNNumber); err != nil {
				return err
			}
			return uc.RegisterAsn(ctx, e.ID(), d.ASNNumber)
		}),
		TypeASNCancelled: on(func(ctx context.Context, e ce.Event, d asnData) error {
			if err := requireNonBlank(e, "asn_number", d.ASNNumber); err != nil {
				return err
			}
			return uc.CancelAsn(ctx, e.ID(), d.ASNNumber)
		}),
		TypeReceiptOpened: on(func(ctx context.Context, e ce.Event, d receiptData) error {
			if err := requireNonBlank(e, "receipt_id", d.ReceiptID, "asn_number", d.ASNNumber); err != nil {
				return err
			}
			return uc.OpenReceipt(ctx, e.ID(), views.Receipt{
				ID: d.ReceiptID, ASNNumber: d.ASNNumber, AppointmentID: d.AppointmentID, DoorCode: d.DoorCode,
			})
		}),
		TypeReceiptClosed: on(func(ctx context.Context, e ce.Event, d receiptData) error {
			if err := requireNonBlank(e, "receipt_id", d.ReceiptID, "asn_number", d.ASNNumber); err != nil {
				return err
			}
			if d.ClosedAt.IsZero() {
				return poisonf("payload of %s carries no closed_at", e.Type())
			}
			return uc.CloseReceipt(ctx, e.ID(), d.ReceiptID, d.ASNNumber, d.ClosedAt.UTC())
		}),
	}
	return newConsumer("inbound-receiving consumer", topic, groupID, EnvInboundReceivingGroup, f, handlers, logger)
}
