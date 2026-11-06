package usecases

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/shared"
	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// DefaultPageSize and MaxPageSize bound a list page (limit 1-500, default 100).
const (
	DefaultPageSize = 100
	MaxPageSize     = 500
)

func pageLimit(limit int) int {
	switch {
	case limit < 1:
		return DefaultPageSize
	case limit > MaxPageSize:
		return MaxPageSize
	}
	return limit
}

// GetVisit returns one visit.
func (y *Yard) GetVisit(ctx context.Context, id string) (views.Visit, error) {
	v, err := y.d.Visits.FindByID(ctx, id)
	if err != nil {
		return views.Visit{}, notFoundAs(err, ErrVisitNotFound)
	}
	return visitView(v), nil
}

// ListVisits returns one keyset page of visits, newest first.
func (y *Yard) ListVisits(ctx context.Context, f views.VisitFilter, after *views.Key, limit int) (views.Page[views.Visit], error) {
	limit = pageLimit(limit)
	rows, err := y.d.Visits.List(ctx, f, after, limit+1)
	if err != nil {
		return views.Page[views.Visit]{}, err
	}
	keys := make([]views.Key, 0, len(rows))
	items := make([]views.Visit, 0, len(rows))
	for _, v := range rows {
		s := v.Snapshot()
		keys = append(keys, views.Key{At: s.ArrivedAt, ID: s.ID})
		items = append(items, visitView(v))
	}
	return trim(items, keys, limit)
}

// trim cuts the extra row that tells a next page exists; Next is then the key
// of the last kept row.
func trim[T any](items []T, keys []views.Key, limit int) (views.Page[T], error) {
	page := views.Page[T]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.Next = &keys[limit-1]
	}
	return page, nil
}

// GetTrailer returns one trailer.
func (y *Yard) GetTrailer(ctx context.Context, id string) (views.Trailer, error) {
	tr, err := y.loadTrailer(ctx, id)
	if err != nil {
		return views.Trailer{}, err
	}
	open, err := y.openMoveID(ctx, id)
	return trailerView(tr, open), err
}

// ListTrailers returns one keyset page of trailers by arrival.
func (y *Yard) ListTrailers(ctx context.Context, f views.TrailerFilter, after *views.Key, limit int) (views.Page[views.Trailer], error) {
	limit = pageLimit(limit)
	rows, err := y.d.Trailers.List(ctx, f, after, limit+1)
	if err != nil {
		return views.Page[views.Trailer]{}, err
	}
	keys := make([]views.Key, 0, len(rows))
	items := make([]views.Trailer, 0, len(rows))
	for _, tr := range rows {
		s := tr.Snapshot()
		open, err := y.openMoveID(ctx, s.ID)
		if err != nil {
			return views.Page[views.Trailer]{}, err
		}
		keys = append(keys, views.Key{At: s.ArrivedAt, ID: s.ID})
		items = append(items, trailerView(tr, open))
	}
	return trim(items, keys, limit)
}

// ListMoves returns one keyset page of moves, oldest first.
func (y *Yard) ListMoves(ctx context.Context, f views.MoveFilter, after *views.Key, limit int) (views.Page[views.Move], error) {
	limit = pageLimit(limit)
	rows, err := y.d.Moves.List(ctx, f, after, limit+1)
	if err != nil {
		return views.Page[views.Move]{}, err
	}
	keys := make([]views.Key, 0, len(rows))
	items := make([]views.Move, 0, len(rows))
	for _, m := range rows {
		keys = append(keys, views.Key{At: m.Snapshot().RequestedAt, ID: m.ID()})
		items = append(items, moveView(m))
	}
	return trim(items, keys, limit)
}

// GetYardBoard builds the live spot and door map of a site from the location
// copy, the trailers on site and the open moves. A site with no known location
// is ErrSiteNotFound.
func (y *Yard) GetYardBoard(ctx context.Context, siteID string) (views.YardBoard, error) {
	locs, err := y.d.Locations.ListBySite(ctx, siteID)
	if err != nil {
		return views.YardBoard{}, err
	}
	if len(locs) == 0 {
		return views.YardBoard{}, ErrSiteNotFound
	}
	now := y.now()
	onSite, err := y.d.Trailers.ListOnSite(ctx, siteID)
	if err != nil {
		return views.YardBoard{}, err
	}
	moves, err := y.d.Moves.ListOpen(ctx, siteID)
	if err != nil {
		return views.YardBoard{}, err
	}
	openByTrailer := map[string]string{}
	board := views.YardBoard{SiteID: siteID, AsOf: now, Spots: []views.BoardPlace{}, Doors: []views.BoardPlace{}, InMove: []views.Trailer{}, OpenMoves: make([]views.Move, 0, len(moves))}
	for _, m := range moves {
		openByTrailer[m.TrailerID()] = m.ID()
		board.OpenMoves = append(board.OpenMoves, moveView(m))
	}
	at := map[shared.Location]views.Trailer{}
	for _, tr := range onSite {
		v := trailerView(tr, openByTrailer[tr.ID()])
		if tr.State() == trailer.InMove {
			board.InMove = append(board.InMove, v)
			continue
		}
		at[tr.Location()] = v
	}
	for _, l := range locs {
		place, err := y.boardPlace(ctx, l, at, now)
		if err != nil {
			return views.YardBoard{}, err
		}
		if l.Kind == shared.Door {
			board.Doors = append(board.Doors, place)
		} else {
			board.Spots = append(board.Spots, place)
		}
	}
	sort.Slice(board.InMove, func(i, j int) bool { return board.InMove[i].TrailerID < board.InMove[j].TrailerID })
	return board, nil
}

func (y *Yard) boardPlace(ctx context.Context, l views.LocationInfo, at map[shared.Location]views.Trailer, now time.Time) (views.BoardPlace, error) {
	place := views.BoardPlace{Code: l.Code, Kind: l.Kind}
	if l.DockFlow != "" {
		place.DockFlow = &l.DockFlow
	}
	if v, ok := at[shared.Location{Kind: l.Kind, Code: l.Code}]; ok {
		place.Trailer = &v
	}
	if l.Kind == shared.Door {
		closed, err := y.d.Closures.Closed(ctx, l.Code, now)
		if err != nil {
			return place, err
		}
		place.Closed = closed
	}
	return place, nil
}
