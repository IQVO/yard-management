package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// The list of outcome refusals is ONE visible list with exactly one entry.
// Growing it changes the middleware's behaviour for another error and needs an
// ADR; this test is the speed bump.
func TestOutcomeRefusalsListsExactlyTheSuspendedCarrier(t *testing.T) {
	if len(outcomeRefusals) != 1 || !errors.Is(outcomeRefusals[0], yardvisit.ErrCarrierSuspended) {
		t.Fatalf("outcomeRefusals = %v, want exactly [yardvisit.ErrCarrierSuspended]", outcomeRefusals)
	}
}

// writeError marks the capturing writer for the listed refusal (also wrapped,
// as the use case returns it) and for nothing else.
func TestWriteErrorKeepsTheWritesOfAnOutcomeRefusalOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		keep bool
	}{
		{"the suspended carrier", fmt.Errorf("%w: SCAC ACME is suspended", yardvisit.ErrCarrierSuspended), true},
		{"a plate that is already open", yardvisit.ErrVisitAlreadyOpen, false},
		{"a concurrent modification", ports.ErrConcurrentModification, false},
		{"an unmapped error", errors.New("boom"), false},
	} {
		out := newCapture()
		writeError(out, httptest.NewRequest(http.MethodPost, "/visits", nil), tc.err)
		if out.keep != tc.keep {
			t.Errorf("%s: keep = %v, want %v", tc.name, out.keep, tc.keep)
		}
	}
}
