package bdd

import (
	"github.com/cucumber/godog"
)

// Steps of the hazmat and reefer placement feature (ADR 0014): the spot kinds
// the site published, as a LocationSlotRegistered event carrying spotKind would
// leave them in the location copy.
func registerPlacementSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^spot \"([^\"]*)\" of site \"([^\"]*)\" is of kind ([A-Z_]+)$`, w.givenSpotKind)
}

// givenSpotKind registers the spot again through the consumer use case, so the
// tolerant decoding of a kind this service does not know is exercised too.
func (w *world) givenSpotKind(code, site, kind string) error {
	return w.env.Yard.ApplySlot(w.env.Ctx, w.eventID(), code, site+"-yard", "Yard", "", kind)
}
