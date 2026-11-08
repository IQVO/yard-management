//go:build integration

package kafka_test

import (
	"testing"

	// The shared kafkatest helper boots the broker through testcontainers; the
	// blank import keeps the rule visible from every Kafka-touching file.
	_ "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/claudioed/yard-management/internal/testing/kafkatest"
	"github.com/claudioed/yard-management/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
}
