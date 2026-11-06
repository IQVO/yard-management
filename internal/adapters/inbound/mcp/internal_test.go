package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardcheck"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 30, 15, 123456789, time.UTC)
	got, err := decodeCursor(encodeCursor(views.Key{At: at, ID: "mv-1"}))
	if err != nil || got == nil || !got.At.Equal(at) || got.ID != "mv-1" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	if first, err := decodeCursor(""); first != nil || err != nil {
		t.Fatalf("empty cursor = %+v, %v; want the first page", first, err)
	}
	for _, bad := range []string{"%%%", "e30", "bm90LWpzb24", "eyJzIjoibm9wZSIsImkiOiJ4In0"} {
		if _, err := decodeCursor(bad); !errors.Is(err, errInvalidCursor) {
			t.Errorf("decodeCursor(%q) = %v, want errInvalidCursor", bad, err)
		}
	}
}

func TestPageParams(t *testing.T) {
	cases := []struct {
		limit   int
		want    int
		wantErr bool
	}{
		{0, usecases.DefaultPageSize, false},
		{1, 1, false},
		{500, 500, false},
		{501, 0, true},
		{-1, 0, true},
	}
	for _, tc := range cases {
		got, _, err := pageParams(tc.limit, "")
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("pageParams(%d) = %d, %v; want %d, err %v", tc.limit, got, err, tc.want, tc.wantErr)
		}
	}
	if _, _, err := pageParams(1, "!"); !errors.Is(err, errInvalidCursor) {
		t.Errorf("a bad cursor = %v", err)
	}
}

func TestPageOfNeverReturnsNullItems(t *testing.T) {
	p := pageOf(views.Page[views.Move]{})
	if p.Items == nil || len(p.Items) != 0 || p.NextCursor != "" {
		t.Fatalf("page = %+v", p)
	}
	next := views.Key{At: time.Now(), ID: "x"}
	if p := pageOf(views.Page[views.Move]{Items: []views.Move{{}}, Next: &next}); p.NextCursor == "" {
		t.Fatal("a next position produced no cursor")
	}
}

func TestOneOf(t *testing.T) {
	if err := oneOf("", "A", "B"); err != nil {
		t.Errorf("empty = %v", err)
	}
	if err := oneOf("A", "A", "B"); err != nil {
		t.Errorf("A = %v", err)
	}
	err := oneOf("C", "A", "B")
	if !errors.Is(err, errInvalidState) || !strings.Contains(err.Error(), "A, B") {
		t.Errorf("C = %v", err)
	}
}

// Every typed error a tool can meet maps to the SAME slug the REST adapter
// answers with; anything else is "internal-error".
func TestMapErrorUsesTheRestSlugs(t *testing.T) {
	cases := []struct {
		err  error
		slug string
	}{
		{usecases.ErrVisitNotFound, "visit-not-found"},
		{usecases.ErrTrailerNotFound, "trailer-not-found"},
		{usecases.ErrMoveNotFound, "move-not-found"},
		{usecases.ErrYardCheckNotFound, "yard-check-not-found"},
		{usecases.ErrSiteNotFound, "site-not-found"},
		{yardvisit.ErrVisitAlreadyOpen, "visit-already-open"},
		{trailer.ErrLocationOccupied, "location-occupied"},
		{trailer.ErrDoorClosed, "door-closed"},
		{trailer.ErrDoorFlowMismatch, "door-flow-mismatch"},
		{trailer.ErrTrailerOnHold, "trailer-on-hold"},
		{trailer.ErrTrailerNotOnHold, "trailer-not-on-hold"},
		{trailer.ErrDoorWorkOpen, "door-work-open"},
		{yardmove.ErrTrailerHasOpenMove, "trailer-has-open-move"},
		{yardmove.ErrDestinationReserved, "destination-reserved"},
		{yardcheck.ErrYardCheckOpen, "yard-check-open"},
		{usecases.ErrNoFreeSpot, "no-free-spot"},
		{shared.ErrIllegalTransition, "illegal-transition"},
		{ports.ErrConcurrentModification, "concurrent-modification"},
		{shared.ErrUnknownLocation, "unknown-location"},
		{shared.ErrValidation, "validation"},
		{errInvalidLimit, "invalid-limit"},
		{errInvalidCursor, "invalid-cursor"},
		{errInvalidState, "invalid-state"},
		{errInvalidReportRange, "invalid-report-range"},
		{errInvalidKey, "idempotency-key-invalid"},
		{errKeyReused, "idempotency-key-reused"},
	}
	for _, tc := range cases {
		wrapped := fmt.Errorf("context: %w", tc.err)
		got := mapError(wrapped)
		if !strings.HasPrefix(got.Error(), tc.slug+": ") {
			t.Errorf("mapError(%v) = %q, want slug %s", tc.err, got, tc.slug)
		}
	}
	if got := mapError(errors.New("pq: password authentication failed")); got.Error() != "internal-error: an unexpected internal error occurred" {
		t.Errorf("an untyped error = %q", got)
	}
	already := toolError("custom", "detail")
	if mapError(fmt.Errorf("wrap: %w", already)) != already {
		t.Error("a tool error was re-mapped")
	}
}

func TestFingerprintIgnoresKeyAndAttributionOnly(t *testing.T) {
	a := requestMoveInput{callArgs: callArgs{IdempotencyKey: "k1", AgentID: "a1"}, TrailerID: "t", To: locationInput{Kind: "DOOR", Code: "D1"}}
	b := a
	b.IdempotencyKey, b.AgentID, b.ProposalID, b.ApprovedBy = "k2", "a2", "p", "s"
	fa, err1 := fingerprint("request_move", a)
	fb, err2 := fingerprint("request_move", b)
	if err1 != nil || err2 != nil || fa != fb {
		t.Fatalf("fingerprints differ for the same call: %s %s (%v %v)", fa, fb, err1, err2)
	}
	b.To.Code = "D2"
	if fc, _ := fingerprint("request_move", b); fc == fa {
		t.Fatal("another destination has the same fingerprint")
	}
	if fd, _ := fingerprint("cancel_move", a); fd == fa {
		t.Fatal("another tool has the same fingerprint")
	}
}

func TestValidIdempotencyKey(t *testing.T) {
	for key, want := range map[string]bool{
		"a": true, "k-1_2.3": true, " ": true, "~": true,
		"": false, "\x1f": false, "\x7f": false, "é": false, strings.Repeat("a", 128): true, strings.Repeat("a", 129): false,
	} {
		if got := validIdempotencyKey(key); got != want {
			t.Errorf("validIdempotencyKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestParseResourceID(t *testing.T) {
	cases := []struct {
		uri, prefix, suffix string
		want                string
		ok                  bool
	}{
		{"warehouse://yard/sites/WH1/board", boardResourcePrefix, boardResourceSuffix, "WH1", true},
		{"warehouse://yard/sites//board", boardResourcePrefix, boardResourceSuffix, "", false},
		{"warehouse://yard/sites/WH1", boardResourcePrefix, boardResourceSuffix, "", false},
		{"warehouse://yard/sites/a/b/board", boardResourcePrefix, boardResourceSuffix, "", false},
		{"warehouse://yard/trailers/trl-1", trailerResourcePrefix, "", "trl-1", true},
		{"warehouse://other/trailers/trl-1", trailerResourcePrefix, "", "", false},
	}
	for _, tc := range cases {
		got, err := parseResourceID(tc.uri, tc.prefix, tc.suffix, "template")
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseResourceID(%q) = %q, %v", tc.uri, got, err)
		}
	}
}
