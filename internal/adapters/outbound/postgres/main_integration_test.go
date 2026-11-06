//go:build integration

package postgres_test

import (
	"testing"

	// The shared pgtest helper boots the database through testcontainers; the
	// blank import makes the rule visible to every integration test file of
	// this package (TestPostgresIntegrationTestsUseTestcontainers).
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	m.Run()
	pgtest.Shutdown()
}
