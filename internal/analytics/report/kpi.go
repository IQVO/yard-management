package report

// The KPI standard v1 (ADR 0011). Every definition lives here and in the ADR,
// once; the reports and the API documentation only point at it.
const (
	// DefaultDetentionThresholdMinutes is the dwell above which a departed
	// trailer counts as detention exposure: the two hour norm that regulators
	// and carriers use as a proxy, because no standard definition of detention
	// exists. It is exposure, never a billing rule.
	DefaultDetentionThresholdMinutes = 120
	// MinDetentionThresholdMinutes and MaxDetentionThresholdMinutes bound the
	// thresholdMinutes query parameter (one minute to seven days).
	MinDetentionThresholdMinutes = 1
	MaxDetentionThresholdMinutes = 10080
)

// ValidThreshold reports whether n is an accepted thresholdMinutes.
func ValidThreshold(n int) bool {
	return n >= MinDetentionThresholdMinutes && n <= MaxDetentionThresholdMinutes
}

// HoldReasons are the TrailerHeld reasons of apis/asyncapi.yaml, in the order
// the exceptions report lists them.
var HoldReasons = []string{"DAMAGED", "SEAL_BROKEN", "NOT_FOUND", "QUALITY", "SECURITY"}

// Purposes are the visit purposes of VisitCheckedIn, in the order of the
// contract.
var Purposes = []string{"DROP", "LIVE_UNLOAD", "LIVE_LOAD", "PICKUP", "BOBTAIL"}

var (
	validPurposes = toSet(Purposes)
	validHolds    = toSet(HoldReasons)
)

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

// Ratio is num/den, or nil (null in the API) when nothing was counted (den is
// not positive) or the numerator is negative. It is not capped: a seal
// exception raised at the gate of a visit that was then rejected counts in the
// numerator of the seal exception rate, so that rate can exceed 1.
func Ratio(num, den int64) *float64 {
	if den <= 0 || num < 0 {
		return nil
	}
	r := float64(num) / float64(den)
	return &r
}

// Share is a part of a whole, 0..1, or nil when the whole is empty. A part
// larger than its whole is a bug upstream; it is capped at 1 rather than
// reported as more than all.
func Share(part, whole int64) *float64 {
	r := Ratio(part, whole)
	if r != nil && *r > 1 {
		one := 1.0
		return &one
	}
	return r
}

// RejectionRate is rejected over all visits that reached the gate decision
// (checked in plus rejected); nil when the gate saw nobody.
func RejectionRate(checkedIn, rejected int64) *float64 {
	return Share(rejected, checkedIn+rejected)
}

// SealExceptionRate is seal exceptions per check-in; nil when nobody checked in.
func SealExceptionRate(sealExceptions, checkedIn int64) *float64 {
	return Ratio(sealExceptions, checkedIn)
}

// validateKPI validates the five kinds of the KPI standard; the common fields
// (event id, time, site) were checked by Validate.
func (e Event) validateKPI(reject func(string, ...any) error) error {
	switch e.Kind {
	case KindVisitCheckedOut:
		if e.OnSiteMinutes < 0 {
			return reject("on_site_minutes %d is negative", e.OnSiteMinutes)
		}
		return require(reject, "visit_id", e.VisitID, "departed_at", instant(e.DepartedAt))
	case KindVisitRejected:
		return require(reject, "visit_id", e.VisitID, "reason", e.Reason, "rejected_at", instant(e.RejectedAt))
	case KindSealExceptionRaised:
		return require(reject, "trailer_id or the visit subject", e.Subject(), "raised_at", instant(e.RaisedAt))
	case KindTrailerHeld:
		if !validHolds[e.Reason] {
			return reject("reason %q is not a hold reason", e.Reason)
		}
		if e.Version < 1 {
			return reject("version %d is below 1", e.Version)
		}
		return require(reject, "trailer_id", e.TrailerID)
	case KindYardCheckClosed:
		if e.Discrepancies < 0 {
			return reject("discrepancies %d is negative", e.Discrepancies)
		}
		return require(reject, "yard_check_id", e.YardCheckID, "closed_at", instant(e.ClosedAt))
	}
	return reject("unknown kind")
}
