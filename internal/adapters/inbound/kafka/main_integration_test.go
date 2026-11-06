//go:build integration

package kafka_test

import (
	"os"
	"testing"

	// The shared kafkatest / pgtest helpers boot the broker and the database
	// through testcontainers; the blank imports keep the rule visible from the
	// Kafka- and Postgres-touching files (the fleet fitness tests look for them).
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

// The infrastructure comes from testcontainers, never from the environment:
// pgtest boots ONE Postgres and kafkatest ONE Kafka per test binary (each lazily,
// on first use); TestMain only tears them down at the end.
func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}
