package http

import (
	"context"
	"net/http"
	"time"

	"github.com/claudioed/yard-management/internal/application/views"
)

// maxReportDays bounds a report window.
const maxReportDays = 92

// reportBody is the shape of the three reports: the window, the site filter
// (null for every site) and the rows.
type reportBody[R any] struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	SiteID *string   `json:"siteId"`
	Rows   []R       `json:"rows"`
}

// reportWindow reads and validates ?from=&to=&siteId=.
func reportWindow(r *http.Request) (views.ReportWindow, error) {
	q := r.URL.Query()
	from, ferr := time.Parse(time.RFC3339, q.Get("from"))
	to, terr := time.Parse(time.RFC3339, q.Get("to"))
	if ferr != nil || terr != nil || !to.After(from) || to.Sub(from) > maxReportDays*24*time.Hour {
		return views.ReportWindow{}, errInvalidReportRange
	}
	return views.ReportWindow{From: from.UTC(), To: to.UTC(), SiteID: q.Get("siteId")}, nil
}

func newReport[R any](w views.ReportWindow, rows []R) reportBody[R] {
	body := reportBody[R]{From: w.From, To: w.To, Rows: rows}
	if body.Rows == nil {
		body.Rows = []R{}
	}
	if w.SiteID != "" {
		body.SiteID = &w.SiteID
	}
	return body
}

// serveReport validates the window, asks the read-side port (when wired) and
// answers the report; with no read side the rows are empty.
func serveReport[R any](s *Server, w http.ResponseWriter, r *http.Request, read func(context.Context, views.ReportWindow) ([]R, error)) {
	win, err := reportWindow(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var rows []R
	if s.Reports != nil {
		if rows, err = read(r.Context(), win); err != nil {
			writeError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, newReport(win, rows))
}

// handleYardDwell backs GET /reports/yard-dwell.
func (s *Server) handleYardDwell(w http.ResponseWriter, r *http.Request) {
	serveReport(s, w, r, func(ctx context.Context, win views.ReportWindow) ([]views.DwellRow, error) {
		return s.Reports.YardDwell(ctx, win)
	})
}

// handleDoorUtilisation backs GET /reports/door-utilisation.
func (s *Server) handleDoorUtilisation(w http.ResponseWriter, r *http.Request) {
	serveReport(s, w, r, func(ctx context.Context, win views.ReportWindow) ([]views.DoorRow, error) {
		return s.Reports.DoorUtilisation(ctx, win)
	})
}

// handleHostlerProductivity backs GET /reports/hostler-productivity.
func (s *Server) handleHostlerProductivity(w http.ResponseWriter, r *http.Request) {
	serveReport(s, w, r, func(ctx context.Context, win views.ReportWindow) ([]views.HostlerRow, error) {
		return s.Reports.HostlerProductivity(ctx, win)
	})
}

type freshnessBody struct {
	LastEventAt *time.Time `json:"lastEventAt"`
	AsOf        time.Time  `json:"asOf"`
}

// handleFreshness backs GET /reports/freshness: when the projector last
// folded an event (null until the first one, or with no read side).
func (s *Server) handleFreshness(w http.ResponseWriter, r *http.Request) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	body := freshnessBody{AsOf: now().UTC()}
	if s.Reports != nil {
		at, ok, err := s.Reports.LastEventAt(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		if ok {
			u := at.UTC()
			body.LastEventAt = &u
		}
	}
	writeJSON(w, http.StatusOK, body)
}
