package kafka

import "github.com/claudioed/yard-management/internal/application/usecases"

// Compile-time proof that the application service implements every use-case
// interface the consumers drive, so the composition root can pass one *Yard.
var (
	_ DockSchedulingUseCases   = (*usecases.Yard)(nil)
	_ FacilityUseCases         = (*usecases.Yard)(nil)
	_ InboundReceivingUseCases = (*usecases.Yard)(nil)
)
