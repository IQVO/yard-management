package report

import "time"

// Utilisation is the share of the window a door was occupied, 0..1. A
// non-positive window is 0.
func Utilisation(occupied, window time.Duration) float64 {
	if window <= 0 || occupied <= 0 {
		return 0
	}
	if occupied >= window {
		return 1
	}
	return float64(occupied) / float64(window)
}

// PerBusyHour is moves per hour in which the hostler completed at least one
// move: the productivity of the hours actually worked, not of the whole
// window (a window spans nights and weekends). Zero busy hours is 0.
func PerBusyHour(moves, busyHours int) float64 {
	if busyHours <= 0 || moves <= 0 {
		return 0
	}
	return float64(moves) / float64(busyHours)
}
