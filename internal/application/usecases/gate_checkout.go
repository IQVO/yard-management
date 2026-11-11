package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/yard-management/internal/application/ports"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
	"github.com/claudioed/yard-management/internal/domain/yardvisit"
)

// CheckOutVisit is the gate check-out: the visit leaves with the listed
// trailers, each of which must be on site and released (Y-2) and, for a
// LIVE_LOAD or PICKUP visit, sealed when loaded. A trailer the visit brought
// and does not take stays where it is (a drop).
func (y *Yard) CheckOutVisit(ctx context.Context, visitID string, trailerIDs []string) (views.Visit, error) {
	var out views.Visit
	err := y.atomically(ctx, func(ctx context.Context) error {
		v, err := y.d.Visits.FindByID(ctx, visitID)
		if err != nil {
			return notFoundAs(err, ErrVisitNotFound)
		}
		now := y.now()
		trailers, departures, err := y.departures(ctx, v, trailerIDs)
		if err != nil {
			return err
		}
		if err := v.CheckOut(departures, now); err != nil {
			return err
		}
		events := v.PullEvents()
		if err := y.d.Visits.Save(ctx, v); err != nil {
			return err
		}
		if err := y.publish(ctx, events); err != nil {
			return err
		}
		if err := y.departTrailers(ctx, v.ID(), trailers, now); err != nil {
			return err
		}
		out = visitView(v)
		return nil
	})
	return out, err
}

// departTrailers departs every trailer the visit takes, each one only after the
// PRE_DEPARTURE inspection its site requires (ADR 0015) has passed.
func (y *Yard) departTrailers(ctx context.Context, visitID string, trailers []*trailer.Trailer, now time.Time) error {
	for _, tr := range trailers {
		if err := tr.Depart(visitID, now); err != nil {
			return err
		}
		if err := y.requireInspections(ctx, tr, shared.GatePreDeparture); err != nil {
			return err
		}
		if err := y.saveTrailer(ctx, tr); err != nil {
			return err
		}
	}
	return nil
}

// departures loads the trailers a visit takes and states what CheckOut needs
// to know about each.
func (y *Yard) departures(ctx context.Context, v *yardvisit.Visit, ids []string) ([]*trailer.Trailer, []yardvisit.Departure, error) {
	trailers := make([]*trailer.Trailer, 0, len(ids))
	out := make([]yardvisit.Departure, 0, len(ids))
	for _, id := range ids {
		tr, err := y.d.Trailers.FindByID(ctx, id)
		if errors.Is(err, ports.ErrNotFound) {
			out = append(out, yardvisit.Departure{TrailerID: id})
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		onSite := tr.State() != trailer.Departed && tr.SiteID() == v.SiteID()
		open := false
		if onSite {
			_, err := y.d.Moves.FindOpenByTrailer(ctx, id)
			switch {
			case err == nil:
				open = true
			case !errors.Is(err, ports.ErrNotFound):
				return nil, nil, err
			}
		}
		trailers = append(trailers, tr)
		out = append(out, yardvisit.Departure{
			TrailerID: id, OnSite: onSite, Released: onSite && tr.ReleasedForDeparture(open),
			Loaded: tr.LoadState() != shared.LoadEmpty, SealNumber: tr.SealNumber(),
		})
	}
	return trailers, out, nil
}
