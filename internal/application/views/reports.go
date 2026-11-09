package views

import "time"

// ReportWindow is the window and site filter of an analytics report.
type ReportWindow struct {
	From, To time.Time
	SiteID   string // empty: every site
}

// DwellRow is one row of the yard dwell report: a trailer kind. The median and
// the 90th percentile show the long tail the average hides (ADR 0011); the
// wait (arrival to the first door work) and the processing (door work started
// to completed, averaged over the completed sessions of these trailers) split
// the time. A mean that has no sample is null.
type DwellRow struct {
	Kind                 string   `json:"kind"`
	Trailers             int      `json:"trailers"`
	AvgDwellMinutes      float64  `json:"avgDwellMinutes"`
	MaxDwellMinutes      int64    `json:"maxDwellMinutes"`
	AvgDriverWaitMinutes *float64 `json:"avgDriverWaitMinutes"`
	P50DwellMinutes      *float64 `json:"p50DwellMinutes"`
	P90DwellMinutes      *float64 `json:"p90DwellMinutes"`
	AvgProcessingMinutes *float64 `json:"avgProcessingMinutes"`
}

// TurnTimeRow is one row of the gate-to-gate turn time report: a visit purpose,
// over the visits that checked OUT in the window (ADR 0011).
type TurnTimeRow struct {
	Purpose    string  `json:"purpose"`
	Visits     int     `json:"visits"`
	AvgMinutes float64 `json:"avgMinutes"`
	P50Minutes float64 `json:"p50Minutes"`
	P90Minutes float64 `json:"p90Minutes"`
	MaxMinutes int64   `json:"maxMinutes"`
}

// DetentionRow is one row of the detention exposure report: a trailer kind,
// over the trailers that DEPARTED in the window (ADR 0011). Share is the
// exceeding trailers over all of them, null when there were none; ExcessMinutes
// is the sum of dwell minus threshold over the exceeding trailers. It is
// exposure, never a billed amount.
type DetentionRow struct {
	Kind          string   `json:"kind"`
	Trailers      int      `json:"trailers"`
	Exceeding     int      `json:"exceeding"`
	Share         *float64 `json:"share"`
	ExcessMinutes int64    `json:"excessMinutes"`
}

// ExceptionRow is one row of the exceptions report: a site (ADR 0011). The
// rates are null when their denominator is zero. HoldsByReason always lists
// every hold reason, zero included.
type ExceptionRow struct {
	SiteID            string         `json:"siteId"`
	VisitsCheckedIn   int            `json:"visitsCheckedIn"`
	VisitsRejected    int            `json:"visitsRejected"`
	RejectionRate     *float64       `json:"rejectionRate"`
	SealExceptions    int            `json:"sealExceptions"`
	SealExceptionRate *float64       `json:"sealExceptionRate"`
	HoldsByReason     map[string]int `json:"holdsByReason"`
	YardChecksClosed  int            `json:"yardChecksClosed"`
	Discrepancies     int            `json:"discrepancies"`
}

// DoorRow is one row of the door utilisation report: a dock door.
type DoorRow struct {
	SiteID          string  `json:"siteId"`
	DoorCode        string  `json:"doorCode"`
	Dockings        int     `json:"dockings"`
	OccupiedMinutes int64   `json:"occupiedMinutes"`
	WorkMinutes     int64   `json:"workMinutes"`
	Utilisation     float64 `json:"utilisation"`
}

// HostlerRow is one row of the hostler productivity report.
type HostlerRow struct {
	SiteID         string  `json:"siteId"`
	HostlerRef     string  `json:"hostlerRef"`
	Moves          int     `json:"moves"`
	AvgMoveSeconds float64 `json:"avgMoveSeconds"`
	MovesPerHour   float64 `json:"movesPerHour"`
}
