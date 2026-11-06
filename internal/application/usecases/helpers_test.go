package usecases_test

import (
	"errors"
	"testing"

	"github.com/claudioed/yard-management/internal/application/usecases"
	"github.com/claudioed/yard-management/internal/application/views"
	"github.com/claudioed/yard-management/internal/domain/yardmove"
	"github.com/claudioed/yard-management/internal/domain/yardpolicy"
	"github.com/claudioed/yard-management/internal/testing/yardtest"
)

type policyInput = yardpolicy.DeclareInput

func isErr(err, target error) bool { return errors.Is(err, target) }

func yardMoveBand(minutes int) int { return yardmove.BandForMinutes(minutes) }

// declare declares the WH1 policy: the defaults with mod applied.
func declare(t *testing.T, e *yardtest.Env, mod func(*policyInput)) views.YardPolicy {
	t.Helper()
	in := policyInput{LeaseSeconds: 300, SealMismatchAction: yardpolicy.Flag}
	if mod != nil {
		mod(&in)
	}
	p, err := e.Yard.DeclareYardPolicy(e.Ctx, usecases.DeclarePolicyInput{SiteID: "WH1", Policy: in})
	must(t, err)
	return p
}
