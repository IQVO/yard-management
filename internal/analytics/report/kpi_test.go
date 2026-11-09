package report_test

import (
	"math"
	"testing"

	"github.com/claudioed/yard-management/internal/analytics/report"
)

func TestThresholdBounds(t *testing.T) {
	cases := []struct {
		n    int
		want bool
	}{{0, false}, {-1, false}, {1, true}, {120, true}, {10080, true}, {10081, false}}
	for _, c := range cases {
		if got := report.ValidThreshold(c.n); got != c.want {
			t.Errorf("ValidThreshold(%d) = %v, want %v", c.n, got, c.want)
		}
	}
	if report.DefaultDetentionThresholdMinutes != 120 || !report.ValidThreshold(report.DefaultDetentionThresholdMinutes) {
		t.Errorf("the default is the two hour norm and is itself valid")
	}
}

// same compares two nullable ratios: nil is null in the report.
func same(got *float64, want *float64) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return math.Abs(*got-*want) < 1e-12
}

func ptr(f float64) *float64 { return &f }

func TestShareIsAPartOfAWholeBetweenZeroAndOneOrNullWhenEmpty(t *testing.T) {
	cases := []struct {
		name        string
		part, whole int64
		want        *float64
	}{
		{"empty window", 0, 0, nil},
		{"negative whole", 1, -3, nil},
		{"none exceeding", 0, 4, ptr(0)},
		{"a single sample exceeding", 1, 1, ptr(1)},
		{"a single sample not exceeding", 0, 1, ptr(0)},
		{"half", 2, 4, ptr(0.5)},
		{"all", 7, 7, ptr(1)},
		{"a part larger than its whole is capped", 9, 7, ptr(1)},
		{"a negative part", -1, 7, nil},
	}
	for _, c := range cases {
		if got := report.Share(c.part, c.whole); !same(got, c.want) {
			t.Errorf("%s: Share(%d, %d) = %v, want %v", c.name, c.part, c.whole, got, c.want)
		}
	}
}

func TestRatioIsNotCapped(t *testing.T) {
	if got := report.Ratio(3, 2); !same(got, ptr(1.5)) {
		t.Errorf("Ratio(3, 2) = %v, want 1.5", got)
	}
	if got := report.Ratio(0, 0); got != nil {
		t.Errorf("Ratio(0, 0) = %v, want null", got)
	}
}

func TestRejectionRateIsRejectedOverEveryVisitThatReachedTheGateDecision(t *testing.T) {
	cases := []struct {
		name                string
		checkedIn, rejected int64
		want                *float64
	}{
		{"nobody at the gate", 0, 0, nil},
		{"nobody rejected", 5, 0, ptr(0)},
		{"one in four", 3, 1, ptr(0.25)},
		{"everybody rejected", 0, 2, ptr(1)},
		{"a single sample", 1, 0, ptr(0)},
		{"tie", 5, 5, ptr(0.5)},
	}
	for _, c := range cases {
		if got := report.RejectionRate(c.checkedIn, c.rejected); !same(got, c.want) {
			t.Errorf("%s: RejectionRate(%d, %d) = %v, want %v", c.name, c.checkedIn, c.rejected, got, c.want)
		}
	}
}

func TestSealExceptionRateIsPerCheckIn(t *testing.T) {
	cases := []struct {
		name            string
		seal, checkedIn int64
		want            *float64
	}{
		{"nobody checked in", 2, 0, nil},
		{"empty window", 0, 0, nil},
		{"none", 0, 8, ptr(0)},
		{"one in eight", 1, 8, ptr(0.125)},
		{"gate rejections can push it over one", 3, 2, ptr(1.5)},
	}
	for _, c := range cases {
		if got := report.SealExceptionRate(c.seal, c.checkedIn); !same(got, c.want) {
			t.Errorf("%s: SealExceptionRate(%d, %d) = %v, want %v", c.name, c.seal, c.checkedIn, got, c.want)
		}
	}
}
