package repocontract

import (
	"reflect"
	"testing"

	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// trailerClassification: the hazard class, the UN number and the temperature
// control need survive the store, and classifying again with nothing clears
// them (ADR 0014).
func trailerClassification(t *testing.T, s Store) {
	tr := Arrived(t, "trl-cls", "WH1", "CLS-1", 0)
	must(t, s.Trailers.Save(bg, tr))
	_, err := tr.Classify(trailer.Classification{HazmatClass: "2.1", UNNumber: "UN1075", RequiresTemperatureControl: true})
	must(t, err)
	must(t, s.Trailers.Save(bg, tr))
	got, err := s.Trailers.FindByID(bg, "trl-cls")
	must(t, err)
	if c := got.Classification(); c.HazmatClass != "2.1" || c.UNNumber != "UN1075" || !c.RequiresTemperatureControl || !reflect.DeepEqual(got.Snapshot(), tr.Snapshot()) {
		t.Fatalf("round trip changed the classification:\ngot  %+v\nwant %+v", got.Snapshot(), tr.Snapshot())
	}
	_, err = got.Classify(trailer.Classification{})
	must(t, err)
	must(t, s.Trailers.Save(bg, got))
	again, err := s.Trailers.FindByID(bg, "trl-cls")
	must(t, err)
	if again.Classification().Declared() {
		t.Fatalf("a cleared classification stays cleared: %+v", again.Classification())
	}
}
