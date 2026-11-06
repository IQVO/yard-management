package views

import "time"

// ReportWindow is the window and site filter of an analytics report.
type ReportWindow struct {
	From, To time.Time
	SiteID   string // empty: every site
}

// DwellRow is one row of the yard dwell report: a trailer kind.
type DwellRow struct {
	Kind                 string   `json:"kind"`
	Trailers             int      `json:"trailers"`
	AvgDwellMinutes      float64  `json:"avgDwellMinutes"`
	MaxDwellMinutes      int64    `json:"maxDwellMinutes"`
	AvgDriverWaitMinutes *float64 `json:"avgDriverWaitMinutes"`
}

// DoorRow is one row of the door utilisation report: a dock door.
type DoorRow struct {
	SiteID          string  `json:"siteId"`
	DoorCode        string  `json:"doorCode"`
	Dockings        int     `json:"dockings"`
	OccupiedMinutes int64   `json:"occupiedMinutes"`
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
