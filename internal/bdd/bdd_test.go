package bdd

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// TestFeatures runs the Gherkin scenarios of ../../features against the REST
// adapter, the real router and the real Idempotency-Key middleware over the
// use cases on the in-memory store (the same wiring as the adapter unit
// tests; cmd/api's integration test proves the Postgres + Kafka wiring).
// BDD_TAGS overrides the tag filter (default: everything except @known-bug).
func TestFeatures(t *testing.T) {
	tags := os.Getenv("BDD_TAGS")
	if tags == "" {
		tags = "~@known-bug"
	}
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format:   "progress",
			Paths:    []string{"../../features"},
			Strict:   true,
			TestingT: t,
			Tags:     tags,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
