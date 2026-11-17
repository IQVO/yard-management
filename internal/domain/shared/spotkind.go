package shared

// SpotKind is what a yard spot is for, as facility-layout publishes it on
// LocationSlotRegistered (ADR 0045 there, ADR 0014 here). It is a consumed
// fact, never set by this context. The empty kind means "not classified":
// the slot was registered without one (every spot registered before the field
// existed) and is judged exactly as it always was.
type SpotKind string

// The spot kinds of facility-layout.
const (
	// SpotKindUnknown: no kind was published for the spot (legacy).
	SpotKindUnknown SpotKind = ""
	// SpotKindParking: an ordinary parking spot.
	SpotKindParking SpotKind = "PARKING"
	// SpotKindHolding: a holding spot for walk-ins and early arrivals.
	SpotKindHolding SpotKind = "HOLDING"
	// SpotKindReeferPlug: a spot with a power connection for a refrigerated
	// trailer.
	SpotKindReeferPlug SpotKind = "REEFER_PLUG"
	// SpotKindHazmat: a spot set aside for trailers carrying hazardous
	// materials.
	SpotKindHazmat SpotKind = "HAZMAT"
	// SpotKindStagingLane: a staging lane.
	SpotKindStagingLane SpotKind = "STAGING_LANE"
)

// Known reports whether k is one of the five published kinds (the empty
// kind is "unknown", not a kind).
func (k SpotKind) Known() bool {
	switch k {
	case SpotKindParking, SpotKindHolding, SpotKindReeferPlug, SpotKindHazmat, SpotKindStagingLane:
		return true
	}
	return false
}

// ParseSpotKind is the tolerant decode of a published spotKind: a known kind
// is kept, and anything else (absent, null, a kind a later version of
// facility-layout adds) reads as unknown, so a new producer value never makes
// the consumer refuse a slot.
func ParseSpotKind(s string) SpotKind {
	if k := SpotKind(s); k.Known() {
		return k
	}
	return SpotKindUnknown
}
