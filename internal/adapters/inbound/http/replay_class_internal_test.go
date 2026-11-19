package http

// The replay class of a problem type (ADR 0019): FINAL, OUTCOME or RETRYABLE,
// declared on its catalogue entry. These tests are the guard rails of that
// declaration: no entry without a class, the ADR table says what the code says,
// the one OUTCOME is the one ADR 0016 describes.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

const adrPath = "../../../../docs/adr/0019-idempotency-replay-classes.md"

// outsideTheStore are the problems the adapter writes that never reach the
// replay decision: the middleware writes them itself (not through the capture
// writer), the router writes them before any key is read, or they are a 5xx.
// They have no class; each one is listed in the ADR with the reason.
var outsideTheStore = map[string]string{
	"idempotency-key-required": "written by the middleware before a key exists",
	"idempotency-key-invalid":  "written by the middleware before a key is read",
	"idempotency-key-reused":   "written by the middleware on a replay; it is never the stored answer",
	"method-not-allowed":       "written by the router, outside the keyed routes",
	"resource-not-found":       "written by the router and the NUL guard, before the middleware",
	"dependency-unavailable":   "the 503 of /readyz, not a keyed route (and a 5xx is never stored)",
	"internal-error":           "a 5xx is never stored",
}

func TestEveryCatalogueEntryDeclaresAReplayClass(t *testing.T) {
	for _, e := range problemCatalogue {
		switch e.replay {
		case replayFinal, replayOutcome, replayRetryable:
		default:
			t.Errorf("%s has no replay class: declare FINAL, OUTCOME or RETRYABLE on its catalogue entry (ADR 0019)", e.p.slug)
		}
	}
}

// Every problem the package can write is either a catalogue entry (which has a
// class, above) or one of outsideTheStore; the exemption list holds nothing
// that is also a catalogue entry or that the adapter no longer writes.
func TestEveryProblemTheAdapterWritesHasAClassOrIsOutsideTheStore(t *testing.T) {
	answerable := answerableProblems(t)
	inCatalogue := map[string]bool{}
	for _, e := range problemCatalogue {
		inCatalogue[e.p.slug] = true
	}
	for _, slug := range sortedSlugs(answerable) {
		if _, outside := outsideTheStore[slug]; !inCatalogue[slug] && !outside {
			t.Errorf("`%s` is written by the adapter and has no replay class: add it to problemCatalogue with a class, or to outsideTheStore with the reason", slug)
		}
	}
	for slug, why := range outsideTheStore {
		if _, written := answerable[slug]; !written {
			t.Errorf("`%s` is no longer written by the adapter: delete it from outsideTheStore (%s)", slug, why)
		}
		if inCatalogue[slug] {
			t.Errorf("`%s` is a catalogue entry and listed outsideTheStore: keep one of the two", slug)
		}
	}
}

// OUTCOME means the command committed state and then refused: it stays rare and
// needs a use case that proves it. Today that is the suspended carrier only.
func TestTheSuspendedCarrierIsTheOnlyOutcome(t *testing.T) {
	var outcomes []string
	for _, e := range problemCatalogue {
		if e.replay == replayOutcome {
			outcomes = append(outcomes, e.p.slug)
		}
	}
	if len(outcomes) != 1 || outcomes[0] != "carrier-suspended" || entryOf(t, yardvisit.ErrCarrierSuspended).replay != replayOutcome {
		t.Fatalf("OUTCOME entries = %v, want exactly [carrier-suspended]: a new OUTCOME needs an ADR and a use case that commits before it refuses", outcomes)
	}
}

func entryOf(t *testing.T, err error) catalogueEntry {
	t.Helper()
	e, ok := entryFor(err)
	if !ok {
		t.Fatalf("%v is not in the catalogue", err)
	}
	return e
}

// replayOf reads the class of the first entry an error matches, also when the
// use case wrapped it, and says nothing for an error the catalogue does not know.
func TestReplayOfReadsTheClassOfTheMatchingEntry(t *testing.T) {
	for _, e := range problemCatalogue {
		wrapped := fmt.Errorf("while doing %d things: %w", 3, e.err)
		if got := replayOf(wrapped); got != e.replay {
			t.Errorf("%s wrapped: class %v, want %v", e.p.slug, got, e.replay)
		}
	}
	if got := replayOf(errors.New("boom")); got != replayUnset {
		t.Errorf("an unmapped error has class %v, want none", got)
	}
}

// writeError tells the capturing writer the class of what it is writing.
func TestWriteErrorMarksTheWriterWithTheClassOfTheProblem(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want replayClass
	}{
		{"the suspended carrier, wrapped as the use case returns it", fmt.Errorf("%w: SCAC ACME is suspended", yardvisit.ErrCarrierSuspended), replayOutcome},
		{"a concurrent modification", ports.ErrConcurrentModification, replayRetryable},
		{"a plate that is already open", yardvisit.ErrVisitAlreadyOpen, replayRetryable},
		{"a malformed body", errMalformedRequest, replayFinal},
		{"an unmapped error", errors.New("boom"), replayUnset},
	} {
		out := newCapture()
		writeError(out, httptest.NewRequest(http.MethodPost, "/visits", nil), tc.err)
		if out.class != tc.want {
			t.Errorf("%s: class %v, want %v", tc.name, out.class, tc.want)
		}
	}
}

// The ADR table and the catalogue say the same: one row per catalogue slug with
// its class, and a row for each problem that is outside the store.
func TestTheADRDeclaresTheClassOfEveryProblem(t *testing.T) {
	raw, err := os.ReadFile(adrPath)
	if err != nil {
		t.Fatalf("read the ADR: %v", err)
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (FINAL|OUTCOME|RETRYABLE|OUTSIDE) \\|")
	rows := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
		if prior, twice := rows[m[1]]; twice {
			t.Errorf("the ADR lists `%s` twice (%s, %s)", m[1], prior, m[2])
		}
		rows[m[1]] = m[2]
	}
	for _, e := range problemCatalogue {
		if got := rows[e.p.slug]; got != e.replay.String() {
			t.Errorf("`%s`: the ADR says %q, the catalogue %s", e.p.slug, got, e.replay)
		}
		delete(rows, e.p.slug)
	}
	for slug := range outsideTheStore {
		if rows[slug] != "OUTSIDE" {
			t.Errorf("`%s`: the ADR says %q, want OUTSIDE (a problem outside the store)", slug, rows[slug])
		}
		delete(rows, slug)
	}
	left := make([]string, 0, len(rows))
	for slug := range rows {
		left = append(left, slug)
	}
	sort.Strings(left)
	if len(left) > 0 {
		t.Errorf("the ADR classifies problems the adapter does not write: %s", strings.Join(left, ", "))
	}
}
