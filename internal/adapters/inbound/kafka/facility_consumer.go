package kafka

import (
	"context"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"
)

// FacilityTopic is facility-layout's integration topic.
const FacilityTopic = "warehouse.facility.events"

// EnvFacilityGroup names the environment variable that carries the consumer
// group of the facility consumer; boot fails naming it when blank.
const EnvFacilityGroup = "FACILITY_CONSUMER_GROUP"

// The only facility-layout types the consumer acts on, byte-identical to the
// producer's AsyncAPI. Every other type on the topic is ignored.
const (
	TypeZoneRegistered             = "com.warehouse.wms.facility-layout.zone.ZoneRegistered"
	TypeLocationSlotRegistered     = "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered"
	TypeLocationSlotDecommissioned = "com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned"
)

// The facility roles the yard cares about; an absent role is Storage.
const (
	roleYard = "Yard"
	roleDock = "Dock"
)

// FacilityUseCases are the use cases the facility consumer drives;
// *usecases.Yard implements it.
type FacilityUseCases interface {
	ApplyZone(ctx context.Context, eventID, zoneID, siteCode string) error
	ApplySlot(ctx context.Context, eventID, code, zoneID, role, dockFlow string) error
	DecommissionSlot(ctx context.Context, eventID, code string) error
}

// zoneData mirrors the part of facility-layout's camelCase ZoneRegistered
// payload this service needs.
type zoneData struct {
	ZoneID   string `json:"zoneId"`
	SiteCode string `json:"siteCode"`
}

// slotData mirrors the part of the camelCase LocationSlotRegistered /
// LocationSlotDecommissioned payloads this service needs. An absent role
// arrives as "" (Storage).
type slotData struct {
	LocationCode string `json:"locationCode"`
	ZoneID       string `json:"zoneId"`
	Role         string `json:"role"`
	DockFlow     string `json:"dockFlow"`
}

// NewFacilityConsumer consumes FacilityTopic under groupID (from
// FACILITY_CONSUMER_GROUP, never a literal) and keeps the zones and locations
// local copies: a Yard slot is a spot, a Dock slot a door, any other role is
// not the yard's business. A slot that arrives before its zone is held pending
// by the copy until ZoneRegistered resolves its site (X5). It does not dial; a
// blank group is an error naming the variable.
func NewFacilityConsumer(f Factory, groupID string, uc FacilityUseCases, logger *slog.Logger) (*Consumer, error) {
	return NewFacilityConsumerForTopic(f, FacilityTopic, groupID, uc, logger)
}

// NewFacilityConsumerForTopic is NewFacilityConsumer on an explicit topic.
func NewFacilityConsumerForTopic(f Factory, topic, groupID string, uc FacilityUseCases, logger *slog.Logger) (*Consumer, error) {
	log := defaultLogger(logger)
	handlers := map[string]HandlerFunc{
		TypeZoneRegistered: on(func(ctx context.Context, e ce.Event, d zoneData) error {
			if err := requireNonBlank(e, "zoneId", d.ZoneID, "siteCode", d.SiteCode); err != nil {
				return err
			}
			return uc.ApplyZone(ctx, e.ID(), d.ZoneID, d.SiteCode)
		}),
		TypeLocationSlotRegistered: on(func(ctx context.Context, e ce.Event, d slotData) error {
			if err := requireNonBlank(e, "locationCode", d.LocationCode, "zoneId", d.ZoneID); err != nil {
				return err
			}
			if d.Role != roleYard && d.Role != roleDock {
				log.DebugContext(ctx, "ignoring a slot that is not a yard spot or a dock door", "location_code", d.LocationCode, "role", d.Role)
				return nil
			}
			return uc.ApplySlot(ctx, e.ID(), d.LocationCode, d.ZoneID, d.Role, d.DockFlow)
		}),
		TypeLocationSlotDecommissioned: on(func(ctx context.Context, e ce.Event, d slotData) error {
			if err := requireNonBlank(e, "locationCode", d.LocationCode); err != nil {
				return err
			}
			return uc.DecommissionSlot(ctx, e.ID(), d.LocationCode)
		}),
	}
	return newConsumer("facility consumer", topic, groupID, EnvFacilityGroup, f, handlers, logger)
}
