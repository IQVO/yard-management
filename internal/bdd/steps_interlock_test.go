package bdd

import (
	"fmt"

	"github.com/cucumber/godog"
)

// Steps of the dock safety interlock feature (ADR 0013): securing a docked
// trailer, releasing the securing and what the trailer says about it.
func registerInterlockSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the dock operator secures trailer \"([^\"]*)\" with ([A-Z_]+)$`, w.whenSecure)
	sc.Step(`^the dock operator releases the securing of trailer \"([^\"]*)\"$`, w.whenReleaseSecuring)
	sc.Step(`^trailer \"([^\"]*)\" is secured by ([A-Z_]+)$`, w.thenSecuredBy)
	sc.Step(`^trailer \"([^\"]*)\" is not secured$`, w.thenNotSecured)
	sc.Step(`^trailer \"([^\"]*)\" has no open move$`, w.thenNoOpenMove)
}

func (w *world) whenSecure(unit, method string) error {
	return w.trailerAction(unit, "securings", map[string]any{"method": method})
}

func (w *world) whenReleaseSecuring(unit string) error {
	return w.trailerAction(unit, "securing-releases", nil)
}

func (w *world) thenSecuredBy(unit, method string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["securedBy"] != method || t["securedAt"] == nil {
		return fmt.Errorf("trailer %s is securedBy %v at %v, want %s", unit, t["securedBy"], t["securedAt"], method)
	}
	return nil
}

func (w *world) thenNotSecured(unit string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["securedBy"] != nil || t["securedAt"] != nil || t["securerRef"] != nil {
		return fmt.Errorf("trailer %s is secured (%v at %v), want not secured", unit, t["securedBy"], t["securedAt"])
	}
	return nil
}

func (w *world) thenNoOpenMove(unit string) error {
	t, err := w.trailerDoc(unit)
	if err != nil {
		return err
	}
	if t["openMoveId"] != nil {
		return fmt.Errorf("trailer %s has the open move %v, want none", unit, t["openMoveId"])
	}
	return nil
}
