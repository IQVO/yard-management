package shared

// Gate is the yard activity an inspection stands in front of (ADR 0015):
// before a trailer docks, before loading starts on it, before it leaves the
// site. ON_DEMAND is an inspection nobody's policy asks for; it never opens a
// gate but still holds a trailer that fails it.
type Gate string

// The gates.
const (
	GatePreDock      Gate = "PRE_DOCK"
	GatePreLoad      Gate = "PRE_LOAD"
	GatePreDeparture Gate = "PRE_DEPARTURE"
	GateOnDemand     Gate = "ON_DEMAND"
)

// Valid reports whether g is one of the four gates.
func (g Gate) Valid() bool { return g.Gated() || g == GateOnDemand }

// Gated reports whether a site policy can require an inspection at g: the
// three gates in front of an activity (ON_DEMAND is not one of them).
func (g Gate) Gated() bool { return g == GatePreDock || g == GatePreLoad || g == GatePreDeparture }
