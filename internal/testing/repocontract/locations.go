package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/domain/shared"
)

func seedSite(t *testing.T, s Store) {
	t.Helper()
	must(t, s.LocationFeed.ApplyZone(bg, "z-yard", "WH1"))
	must(t, s.LocationFeed.ApplyZone(bg, "z-dock", "WH1"))
	for _, code := range []string{"WH1-P1", "WH1-P2", "WH1-P3"} {
		must(t, s.LocationFeed.ApplySlot(bg, code, "z-yard", shared.Spot, ""))
	}
	must(t, s.LocationFeed.ApplySlot(bg, "WH1-D1", "z-dock", shared.Door, "Inbound"))
}

// Locations runs the LocationView contract.
func Locations(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown location is not found":                           locationsCaseAnUnknownLocationIsNotFound,
		"a slot before its zone has no site until the zone arrives":  locationsCaseASlotBeforeItsZoneHasNoSiteUntilTheZoneArrives,
		"slots list by site, active only, by code":                   locationsCaseSlotsListBySiteActiveOnlyByCode,
		"free spots skip trailers, open-move reservations and doors": locationsCaseFreeSpotsSkipTrailersOpenMoveReservationsAndDoors,
	})
}

// locationsCaseAnUnknownLocationIsNotFound: an unknown location is not found.
func locationsCaseAnUnknownLocationIsNotFound(t *testing.T, s Store) {
	_, ok, err := s.Locations.Get(bg, "NOPE")
	must(t, err)
	if ok {
		t.Fatal("unknown location found")
	}
	list, err := s.Locations.ListBySite(bg, "WH1")
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("list = %v", list)
	}
}

// locationsCaseASlotBeforeItsZoneHasNoSiteUntilTheZoneArrives: a slot before its zone has no site until the zone arrives.
func locationsCaseASlotBeforeItsZoneHasNoSiteUntilTheZoneArrives(t *testing.T, s Store) {
	must(t, s.LocationFeed.ApplySlot(bg, "WH1-P1", "z-yard", shared.Spot, ""))
	info, ok, err := s.Locations.Get(bg, "WH1-P1")
	must(t, err)
	if !ok || info.SiteCode != "" || !info.Active || info.Kind != shared.Spot || info.ZoneID != "z-yard" {
		t.Fatalf("slot %+v", info)
	}
	list, _ := s.Locations.ListBySite(bg, "WH1")
	if len(list) != 0 {
		t.Fatalf("an unresolved slot belongs to no site: %v", list)
	}
	must(t, s.LocationFeed.ApplyZone(bg, "z-yard", "WH1"))
	info, _, _ = s.Locations.Get(bg, "WH1-P1")
	if info.SiteCode != "WH1" {
		t.Fatalf("the zone resolves the slot: %+v", info)
	}
	must(t, s.LocationFeed.ApplyZone(bg, "z-yard", "WH9")) // the zone moving does not move the slot
	info, _, _ = s.Locations.Get(bg, "WH1-P1")
	if info.SiteCode != "WH1" {
		t.Fatalf("slot %+v", info)
	}
}

// locationsCaseSlotsListBySiteActiveOnlyByCode: slots list by site, active only, by code.
func locationsCaseSlotsListBySiteActiveOnlyByCode(t *testing.T, s Store) {
	seedSite(t, s)
	must(t, s.LocationFeed.ApplySlot(bg, "WH2-P1", "z-other", shared.Spot, ""))
	must(t, s.LocationFeed.Decommission(bg, "WH1-P2"))
	must(t, s.LocationFeed.Decommission(bg, "NOPE")) // unknown: nothing happens
	list, err := s.Locations.ListBySite(bg, "WH1")
	must(t, err)
	codes := []string{}
	for _, l := range list {
		codes = append(codes, l.Code)
	}
	if !reflect.DeepEqual(codes, []string{"WH1-D1", "WH1-P1", "WH1-P3"}) {
		t.Fatalf("list = %v", codes)
	}
	door, _, _ := s.Locations.Get(bg, "WH1-D1")
	if door.Kind != shared.Door || door.DockFlow != "Inbound" {
		t.Fatalf("door %+v", door)
	}
	gone, _, _ := s.Locations.Get(bg, "WH1-P2")
	if gone.Active {
		t.Fatalf("decommissioned %+v", gone)
	}
	must(t, s.LocationFeed.ApplySlot(bg, "WH1-P2", "z-yard", shared.Spot, "")) // registering again reactivates
	again, _, _ := s.Locations.Get(bg, "WH1-P2")
	if !again.Active {
		t.Fatalf("re-registered %+v", again)
	}
}

// locationsCaseFreeSpotsSkipTrailersOpenMoveReservationsAndDoors: free spots skip trailers, open-move reservations and doors.
func locationsCaseFreeSpotsSkipTrailersOpenMoveReservationsAndDoors(t *testing.T, s Store) {
	seedSite(t, s)
	tr := Arrived(t, "trl-1", "WH1", "T1", 0)
	must(t, s.Trailers.Save(bg, tr))
	ArriveAt(t, tr, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, tr))
	must(t, s.Moves.Save(bg, NewMove(t, "mv-1", "trl-2", spot("WH1-P2"), 5, time.Time{}, 0)))
	free, err := s.Locations.FreeSpots(bg, "WH1", nil, 10)
	must(t, err)
	if !reflect.DeepEqual(free, []string{"WH1-P3"}) {
		t.Fatalf("free = %v", free)
	}
	must(t, tr.StartMoving(T0.Add(2*time.Minute)))
	must(t, s.Trailers.Save(bg, tr))
	free, _ = s.Locations.FreeSpots(bg, "WH1", nil, 1)
	if !reflect.DeepEqual(free, []string{"WH1-P1"}) {
		t.Fatalf("a trailer in a move holds no spot, limit 1: %v", free)
	}
	none, _ := s.Locations.FreeSpots(bg, "WH1", []string{"z-nowhere"}, 10)
	if len(none) != 0 {
		t.Fatalf("zones restrict the spots: %v", none)
	}
	zoned, _ := s.Locations.FreeSpots(bg, "WH1", []string{"z-yard"}, 10)
	if !reflect.DeepEqual(zoned, []string{"WH1-P1", "WH1-P3"}) {
		t.Fatalf("zoned = %v", zoned)
	}
}
