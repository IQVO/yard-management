package http

import (
	"testing"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// The reason attribute of yard.visits.checked_in is the problem slug of the REST
// answer: the use case keeps its own table (it may not import this adapter) and
// this test fails when the two drift.
func TestCheckInMetricReasonsAreTheProblemSlugsOfTheCatalogue(t *testing.T) {
	for _, err := range usecases.CheckInRefusalErrors() {
		if got, want := usecases.CheckInRefusalReason(err), problemFor(err).slug; got != want {
			t.Errorf("%v: the metric reason is %q, the REST problem slug is %q", err, got, want)
		}
	}
	if got := problemFor(yardvisit.ErrCarrierSuspended).slug; got != "carrier-suspended" {
		t.Errorf("a suspended carrier is the problem %q; the metric reason is carrier-suspended", got)
	}
}
