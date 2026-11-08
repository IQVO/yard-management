package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
)

// MoveRepo runs the MoveRepo contract.
func MoveRepo(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown move is not found": func(t *testing.T, s Store) {
			_, err := s.Moves.FindByID(bg, "mv-404")
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Moves.FindOpenByTrailer(bg, "trl-404")
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Moves.FindOpenByDestination(bg, spot("WH1-P1"))
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Moves.FindClaimedByHostler(bg, "WH1", "h1")
			wantErr(t, err, ports.ErrNotFound)
			_, err = s.Moves.ClaimNext(bg, "WH1")
			wantErr(t, err, ports.ErrNotFound)
		},
		"save then find round trips a move in every state": moveRoundTrip,
		"a trailer has at most one open move": func(t *testing.T, s Store) {
			must(t, s.Moves.Save(bg, NewMove(t, "mv-1", "trl-1", spot("WH1-P1"), 5, time.Time{}, 0)))
			wantErr(t, s.Moves.Save(bg, NewMove(t, "mv-2", "trl-1", spot("WH1-P2"), 5, time.Time{}, 0)), yardmove.ErrTrailerHasOpenMove)
			if _, err := s.Moves.FindByID(bg, "mv-2"); err == nil {
				t.Fatal("the refused move must not be stored")
			}
		},
		"no two open moves share a destination": func(t *testing.T, s Store) {
			must(t, s.Moves.Save(bg, NewMove(t, "mv-1", "trl-1", spot("WH1-P1"), 5, time.Time{}, 0)))
			wantErr(t, s.Moves.Save(bg, NewMove(t, "mv-2", "trl-2", spot("WH1-P1"), 5, time.Time{}, 0)), yardmove.ErrDestinationReserved)
			must(t, s.Moves.Save(bg, NewMove(t, "mv-3", "trl-3", door("WH1-P1"), 5, time.Time{}, 0))) // a door is not the spot
			got, err := s.Moves.FindOpenByDestination(bg, spot("WH1-P1"))
			must(t, err)
			if got.ID() != "mv-1" {
				t.Fatalf("reservation = %s", got.ID())
			}
		},
		"a closed move frees its trailer and its destination": func(t *testing.T, s Store) {
			first := NewMove(t, "mv-1", "trl-1", spot("WH1-P1"), 5, time.Time{}, 0)
			must(t, s.Moves.Save(bg, first))
			Claim(t, first, "h1", time.Minute)
			must(t, s.Moves.Save(bg, first))
			must(t, first.Complete(yardmove.CompleteInput{Hostler: "h1", At: T0.Add(2 * time.Minute)}))
			must(t, s.Moves.Save(bg, first))
			must(t, s.Moves.Save(bg, NewMove(t, "mv-2", "trl-1", spot("WH1-P1"), 5, time.Time{}, 3*time.Minute)))
			second, err := s.Moves.FindByID(bg, "mv-2")
			must(t, err)
			must(t, second.Cancel("changed plan", T0.Add(4*time.Minute)))
			must(t, s.Moves.Save(bg, second))
			must(t, s.Moves.Save(bg, NewMove(t, "mv-3", "trl-1", spot("WH1-P1"), 5, time.Time{}, 5*time.Minute)))
		},
		"a hostler holds one claimed move": func(t *testing.T, s Store) {
			a := NewMove(t, "mv-1", "trl-1", spot("WH1-P1"), 5, time.Time{}, 0)
			b := NewMove(t, "mv-2", "trl-2", spot("WH1-P2"), 5, time.Time{}, 0)
			must(t, s.Moves.Save(bg, a))
			must(t, s.Moves.Save(bg, b))
			Claim(t, a, "h1", time.Minute)
			must(t, s.Moves.Save(bg, a))
			Claim(t, b, "h1", time.Minute)
			wantErr(t, s.Moves.Save(bg, b), yardmove.ErrHostlerBusy)
			held, err := s.Moves.FindClaimedByHostler(bg, "WH1", "h1")
			must(t, err)
			if held.ID() != "mv-1" {
				t.Fatalf("claimed = %s", held.ID())
			}
			if _, err := s.Moves.FindClaimedByHostler(bg, "WH2", "h1"); err == nil {
				t.Fatal("a claim is per site")
			}
		},
		"the version guard refuses stale writers": func(t *testing.T, s Store) {
			m := NewMove(t, "mv-1", "trl-1", spot("WH1-P1"), 5, time.Time{}, 0)
			must(t, s.Moves.Save(bg, m))
			wantErr(t, s.Moves.Save(bg, m), ports.ErrConcurrentModification)
			one, err := s.Moves.FindByID(bg, "mv-1")
			must(t, err)
			two, err := s.Moves.FindByID(bg, "mv-1")
			must(t, err)
			Claim(t, one, "h1", time.Minute)
			Claim(t, two, "h2", time.Minute)
			must(t, s.Moves.Save(bg, one))
			wantErr(t, s.Moves.Save(bg, two), ports.ErrConcurrentModification)
		},
		"claim-next orders by band, due-by and request time": moveClaimOrder,
		"lists and finders": moveLists,
	})
}

func moveRoundTrip(t *testing.T, s Store) {
	due := T0.Add(2 * time.Hour)
	hostler := NewMove(t, "mv-1", "trl-1", door("WH1-D1"), 2, due, 0)
	gate := GateMove(t, "mv-2", "trl-2", spot("WH1-H1"))
	check := func(want *yardmove.Move) {
		t.Helper()
		got, err := s.Moves.FindByID(bg, want.ID())
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), want.Snapshot()) {
			t.Fatalf("round trip changed the state:\ngot  %+v\nwant %+v", got.Snapshot(), want.Snapshot())
		}
	}
	must(t, s.Moves.Save(bg, hostler))
	must(t, s.Moves.Save(bg, gate))
	check(hostler)
	check(gate) // CLAIMED by the visiting driver: no hostler, no lease
	Claim(t, hostler, "h1", time.Minute)
	must(t, s.Moves.Save(bg, hostler))
	check(hostler)
	must(t, hostler.Complete(yardmove.CompleteInput{Hostler: "h1", Actual: spot("WH1-P7"), Note: "door busy", At: T0.Add(3 * time.Minute)}))
	must(t, s.Moves.Save(bg, hostler))
	check(hostler)
	must(t, gate.Complete(yardmove.CompleteInput{At: T0.Add(4 * time.Minute)}))
	must(t, s.Moves.Save(bg, gate))
	check(gate)
	cancelled := NewMove(t, "mv-3", "trl-3", spot("WH1-P3"), 9, time.Time{}, 0)
	must(t, s.Moves.Save(bg, cancelled))
	must(t, cancelled.Cancel("no longer needed", T0.Add(time.Minute)))
	must(t, s.Moves.Save(bg, cancelled))
	check(cancelled)
	expiring := NewMove(t, "mv-4", "trl-4", spot("WH1-P4"), 9, time.Time{}, 0)
	must(t, s.Moves.Save(bg, expiring))
	Claim(t, expiring, "h2", time.Minute)
	must(t, s.Moves.Save(bg, expiring))
	must(t, expiring.ExpireLease(T0.Add(10*time.Minute)))
	must(t, s.Moves.Save(bg, expiring))
	check(expiring) // back to REQUESTED, no holder
}

func moveClaimOrder(t *testing.T, s Store) {
	late := T0.Add(3 * time.Hour)
	soon := T0.Add(time.Hour)
	moves := []*yardmove.Move{
		NewMove(t, "mv-a", "trl-a", spot("WH1-P1"), 5, time.Time{}, 0),         // band 5
		NewMove(t, "mv-b", "trl-b", spot("WH1-P2"), 2, late, 2*time.Minute),    // band 2, due late
		NewMove(t, "mv-c", "trl-c", spot("WH1-P3"), 2, soon, 3*time.Minute),    // band 2, due soon
		NewMove(t, "mv-d", "trl-d", spot("WH1-P4"), 2, time.Time{}, time.Hour), // band 2, no due-by
		NewMove(t, "mv-e", "trl-e", spot("WH1-P5"), 2, time.Time{}, time.Minute),
	}
	other := NewMove(t, "mv-x", "trl-x", spot("WH1-P6"), 1, time.Time{}, 0)
	gate := GateMove(t, "mv-g", "trl-g", spot("WH1-P7"))
	for _, m := range append(moves, other, gate) {
		must(t, s.Moves.Save(bg, m))
	}
	// a move of another site is never claimed here
	otherSite := yardmoveOfSite(t, "WH2")
	must(t, s.Moves.Save(bg, otherSite))
	for _, want := range []string{"mv-x", "mv-c", "mv-b", "mv-e", "mv-d", "mv-a"} {
		got, err := s.Moves.ClaimNext(bg, "WH1")
		must(t, err)
		if got.ID() != want {
			t.Fatalf("claim-next = %s, want %s", got.ID(), want)
		}
		Claim(t, got, "h-"+want, 5*time.Hour)
		must(t, s.Moves.Save(bg, got))
	}
	_, err := s.Moves.ClaimNext(bg, "WH1")
	wantErr(t, err, ports.ErrNotFound)
	got, err := s.Moves.ClaimNext(bg, "WH2")
	must(t, err)
	if got.ID() != "mv-w" {
		t.Fatalf("claim-next WH2 = %s", got.ID())
	}
}

func yardmoveOfSite(t *testing.T, site string) *yardmove.Move {
	t.Helper()
	m, err := yardmove.Request(yardmove.RequestInput{
		ID: "mv-w", SiteID: site, TrailerID: "trl-w", Performer: yardmove.Hostler, To: spot("WH2-P1"), Reason: yardmove.Relocate,
		Priority: yardmove.Priority{Band: 1}, At: T0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func moveLists(t *testing.T, s Store) {
	a := NewMove(t, "mv-a", "trl-a", spot("WH1-P1"), 5, time.Time{}, 0)
	b := NewMove(t, "mv-b", "trl-b", spot("WH1-P2"), 5, time.Time{}, time.Minute)
	c := NewMove(t, "mv-c", "trl-c", spot("WH1-P3"), 5, time.Time{}, 2*time.Minute)
	for _, m := range []*yardmove.Move{a, b, c} {
		must(t, s.Moves.Save(bg, m))
	}
	Claim(t, b, "h1", 5*time.Minute)
	must(t, s.Moves.Save(bg, b))
	must(t, c.Cancel("x", T0.Add(6*time.Minute)))
	must(t, s.Moves.Save(bg, c))
	ids := func(ms []*yardmove.Move, err error) []string {
		must(t, err)
		out := []string{}
		for _, m := range ms {
			out = append(out, m.ID())
		}
		return out
	}
	if got := ids(s.Moves.ListOpen(bg, "WH1")); !reflect.DeepEqual(got, []string{"mv-a", "mv-b"}) {
		t.Fatalf("open = %v", got)
	}
	if got := ids(s.Moves.ListOpenByAppointment(bg, "appt-1")); !reflect.DeepEqual(got, []string{"mv-a", "mv-b"}) {
		t.Fatalf("open by appointment = %v", got)
	}
	if got := ids(s.Moves.ListOpenByAppointment(bg, "appt-404")); len(got) != 0 {
		t.Fatalf("open by unknown appointment = %v", got)
	}
	if got := ids(s.Moves.ListExpiredClaims(bg, T0.Add(5*time.Minute), 10)); len(got) != 0 {
		t.Fatalf("the lease runs until T0+10m, expired = %v", got)
	}
	if got := ids(s.Moves.ListExpiredClaims(bg, T0.Add(10*time.Minute), 10)); !reflect.DeepEqual(got, []string{"mv-b"}) {
		t.Fatalf("expired at the lease end = %v", got)
	}
	if got := ids(s.Moves.List(bg, views.MoveFilter{SiteID: "WH1"}, nil, 2)); !reflect.DeepEqual(got, []string{"mv-a", "mv-b"}) {
		t.Fatalf("page 1 = %v", got)
	}
	key := b.Snapshot()
	if got := ids(s.Moves.List(bg, views.MoveFilter{}, &views.Key{At: key.RequestedAt, ID: key.ID}, 10)); !reflect.DeepEqual(got, []string{"mv-c"}) {
		t.Fatalf("page 2 = %v", got)
	}
	if got := ids(s.Moves.List(bg, views.MoveFilter{State: "CANCELLED", TrailerID: "trl-c"}, nil, 10)); !reflect.DeepEqual(got, []string{"mv-c"}) {
		t.Fatalf("filtered = %v", got)
	}
	if got := ids(s.Moves.List(bg, views.MoveFilter{HostlerRef: "h1"}, nil, 10)); !reflect.DeepEqual(got, []string{"mv-b"}) {
		t.Fatalf("by hostler = %v", got)
	}
	if got := ids(s.Moves.List(bg, views.MoveFilter{HostlerRef: "nobody"}, nil, 10)); len(got) != 0 {
		t.Fatalf("unknown hostler = %v", got)
	}
	found, err := s.Moves.FindOpenByTrailer(bg, "trl-b")
	must(t, err)
	if found.ID() != "mv-b" || found.HostlerRef() != "h1" {
		t.Fatalf("open by trailer = %s", found.ID())
	}
}
