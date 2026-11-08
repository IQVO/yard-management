package repocontract

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// ProcessedEvents runs the inbox contract.
func ProcessedEvents(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"the first claim wins":                     processedEventsCaseTheFirstClaimWins,
		"a claim rolls back with its unit of work": processedEventsCaseAClaimRollsBackWithItsUnitOfWork,
	})
}

// IdempotencyStore runs the Idempotency-Key store contract.
func IdempotencyStore(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"an unknown key is a miss":                idempotencyStoreCaseAnUnknownKeyIsAMiss,
		"save then lookup round trips the answer": idempotencyStoreCaseSaveThenLookupRoundTripsTheAnswer,
		"the first writer of a key wins":          idempotencyStoreCaseTheFirstWriterOfAKeyWins,
		"an empty body round trips as empty":      idempotencyStoreCaseAnEmptyBodyRoundTripsAsEmpty,
	})
}

// UnitOfWork runs the UnitOfWork + outbox contract: the aggregates a use case
// saves and the events it publishes commit or roll back together.
func UnitOfWork(t *testing.T, newStore NewStore) {
	t.Helper()
	run(t, newStore, map[string]func(t *testing.T, s Store){
		"a committed unit of work saves the aggregate and its events": unitOfWorkCaseACommittedUnitOfWorkSavesTheAggregateAndItsEvents,
		"a failed unit of work saves nothing and publishes nothing":   unitOfWorkCaseAFailedUnitOfWorkSavesNothingAndPublishesNothing,
		"a nested unit of work joins the outer one":                   unitOfWorkCaseANestedUnitOfWorkJoinsTheOuterOne,
		"events are published in order with their payloads' names":    unitOfWorkCaseEventsArePublishedInOrderWithTheirPayloadsNames,
		"the constraint backstops fail the unit of work":              unitOfWorkCaseTheConstraintBackstopsFailTheUnitOfWork,
	})
}

// processedEventsCaseTheFirstClaimWins: the first claim wins.
func processedEventsCaseTheFirstClaimWins(t *testing.T, s Store) {
	first, err := s.Processed.Claim(bg, "ev-1")
	must(t, err)
	again, err := s.Processed.Claim(bg, "ev-1")
	must(t, err)
	other, err := s.Processed.Claim(bg, "ev-2")
	must(t, err)
	if !first || again || !other {
		t.Fatalf("first=%v again=%v other=%v", first, again, other)
	}
}

// processedEventsCaseAClaimRollsBackWithItsUnitOfWork: a claim rolls back with its unit of work.
func processedEventsCaseAClaimRollsBackWithItsUnitOfWork(t *testing.T, s Store) {
	boom := ports.ErrNotFound
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		first, err := s.Processed.Claim(ctx, "ev-1")
		must(t, err)
		if !first {
			t.Fatal("not first")
		}
		return boom
	})
	wantErr(t, err, boom)
	first, err := s.Processed.Claim(bg, "ev-1")
	must(t, err)
	if !first {
		t.Fatal("the rolled-back claim must be undone, so the redelivery is processed")
	}
}

// idempotencyStoreCaseAnUnknownKeyIsAMiss: an unknown key is a miss.
func idempotencyStoreCaseAnUnknownKeyIsAMiss(t *testing.T, s Store) {
	_, ok, err := s.Idempotency.Lookup(bg, "k-404")
	must(t, err)
	if ok {
		t.Fatal("hit")
	}
}

// idempotencyStoreCaseSaveThenLookupRoundTripsTheAnswer: save then lookup round trips the answer.
func idempotencyStoreCaseSaveThenLookupRoundTripsTheAnswer(t *testing.T, s Store) {
	must(t, s.Idempotency.Save(bg, rec))
	got, ok, err := s.Idempotency.Lookup(bg, "k-1")
	must(t, err)
	if !ok || !reflect.DeepEqual(got, rec) {
		t.Fatalf("got %+v", got)
	}
}

// idempotencyStoreCaseTheFirstWriterOfAKeyWins: the first writer of a key wins.
func idempotencyStoreCaseTheFirstWriterOfAKeyWins(t *testing.T, s Store) {
	must(t, s.Idempotency.Save(bg, rec))
	second := rec
	second.Status, second.Body, second.Fingerprint = 409, []byte("late"), "fp-2"
	must(t, s.Idempotency.Save(bg, second))
	got, _, _ := s.Idempotency.Lookup(bg, "k-1")
	if got.Status != 201 || got.Fingerprint != "fp-1" {
		t.Fatalf("got %+v", got)
	}
}

// idempotencyStoreCaseAnEmptyBodyRoundTripsAsEmpty: an empty body round trips as empty.
func idempotencyStoreCaseAnEmptyBodyRoundTripsAsEmpty(t *testing.T, s Store) {
	empty := views.IdempotencyRecord{Key: "k-2", Fingerprint: "fp", Status: 204, CreatedAt: T0}
	must(t, s.Idempotency.Save(bg, empty))
	got, ok, err := s.Idempotency.Lookup(bg, "k-2")
	must(t, err)
	if !ok || len(got.Body) != 0 || got.Status != 204 {
		t.Fatalf("got %+v", got)
	}
}

// unitOfWorkCaseACommittedUnitOfWorkSavesTheAggregateAndItsEvents: a committed unit of work saves the aggregate and its events.
func unitOfWorkCaseACommittedUnitOfWorkSavesTheAggregateAndItsEvents(t *testing.T, s Store) {
	v := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
	events := v.PullEvents()
	must(t, s.UoW.Do(bg, func(ctx context.Context) error {
		if err := s.Visits.Save(ctx, v); err != nil {
			return err
		}
		return s.Events.Publish(ctx, events...)
	}))
	if _, err := s.Visits.FindByID(bg, "visit-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingEvents(t); !reflect.DeepEqual(got, []string{"VisitCheckedIn"}) {
		t.Fatalf("events = %v", got)
	}
}

// unitOfWorkCaseAFailedUnitOfWorkSavesNothingAndPublishesNothing: a failed unit of work saves nothing and publishes nothing.
func unitOfWorkCaseAFailedUnitOfWorkSavesNothingAndPublishesNothing(t *testing.T, s Store) {
	v := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
	events := v.PullEvents()
	boom := ports.ErrNotFound
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		if err := s.Visits.Save(ctx, v); err != nil {
			return err
		}
		if err := s.Events.Publish(ctx, events...); err != nil {
			return err
		}
		return boom
	})
	wantErr(t, err, boom)
	if _, err := s.Visits.FindByID(bg, "visit-1"); err == nil {
		t.Fatal("the rolled-back visit is stored")
	}
	if got := s.PendingEvents(t); len(got) != 0 {
		t.Fatalf("the rolled-back events are published: %v", got)
	}
}

// unitOfWorkCaseANestedUnitOfWorkJoinsTheOuterOne: a nested unit of work joins the outer one.
func unitOfWorkCaseANestedUnitOfWorkJoinsTheOuterOne(t *testing.T, s Store) {
	a := NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))
	b := NewVisit(t, "visit-2", "WH1", "PLATE2", 0, Ref("trl-2", "T2"))
	boom := ports.ErrNotFound
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		if err := s.UoW.Do(ctx, func(ctx context.Context) error { return s.Visits.Save(ctx, a) }); err != nil {
			return err
		}
		if err := s.Visits.Save(ctx, b); err != nil {
			return err
		}
		return boom
	})
	wantErr(t, err, boom)
	for _, id := range []string{"visit-1", "visit-2"} {
		if _, err := s.Visits.FindByID(bg, id); err == nil {
			t.Fatalf("%s survived the outer rollback", id)
		}
	}
}

// unitOfWorkCaseEventsArePublishedInOrderWithTheirPayloadsNames: events are published in order with their payloads' names.
func unitOfWorkCaseEventsArePublishedInOrderWithTheirPayloadsNames(t *testing.T, s Store) {
	tr := Arrived(t, "trl-1", "WH1", "T1", 0)
	ArriveAt(t, tr, door("WH1-D1"), time.Minute)
	must(t, tr.StartMoving(T0.Add(2*time.Minute)))
	events := tr.PullEvents()
	must(t, s.Events.Publish(bg, events...))
	if got := s.PendingEvents(t); !reflect.DeepEqual(got, []string{"TrailerDocked", "TrailerUndocked"}) {
		t.Fatalf("events = %v", got)
	}
}

// unitOfWorkCaseTheConstraintBackstopsFailTheUnitOfWork: the constraint backstops fail the unit of work.
func unitOfWorkCaseTheConstraintBackstopsFailTheUnitOfWork(t *testing.T, s Store) {
	must(t, s.Visits.Save(bg, NewVisit(t, "visit-1", "WH1", "PLATE1", 0, Ref("trl-1", "T1"))))
	err := s.UoW.Do(bg, func(ctx context.Context) error {
		return s.Visits.Save(ctx, NewVisit(t, "visit-2", "WH1", "PLATE1", 0, Ref("trl-2", "T2")))
	})
	wantErr(t, err, yardvisit.ErrVisitAlreadyOpen)
	occupied := Arrived(t, "trl-1", "WH1", "T1", 0)
	must(t, s.Trailers.Save(bg, occupied))
	ArriveAt(t, occupied, spot("WH1-P1"), time.Minute)
	must(t, s.Trailers.Save(bg, occupied))
	other := Arrived(t, "trl-2", "WH1", "T2", 0)
	must(t, s.Trailers.Save(bg, other))
	ArriveAt(t, other, spot("WH1-P1"), time.Minute)
	wantErr(t, s.UoW.Do(bg, func(ctx context.Context) error { return s.Trailers.Save(ctx, other) }), trailer.ErrLocationOccupied)
}

var (
	closure = views.Closure{ID: "clo-1", SiteID: "WH1", DoorCode: "WH1-D1", From: T0, To: T0.Add(time.Hour)}
	opened  = views.Receipt{ID: "rcp-1", ASNNumber: "ASN-1", AppointmentID: "appt-1", DoorCode: "WH1-D1"}
	rec     = views.IdempotencyRecord{Key: "k-1", Fingerprint: "fp-1", Status: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`), CreatedAt: T0}
)

func closedAt(t *testing.T, s Store, at time.Time) bool {
	t.Helper()
	closed, err := s.Closures.Closed(bg, "WH1-D1", at)
	must(t, err)
	return closed
}
