package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	inboundkafka "github.com/claudioed/yard-management/internal/adapters/inbound/kafka"
	"github.com/claudioed/yard-management/internal/application/usecases"
)

// buildConsumers constructs the three Kafka consumers of ADR 0009 over the use
// cases. The constructors do not dial; a blank group would be a boot error
// naming its variable (loadConfig has already refused one).
func buildConsumers(cfg config, yard *usecases.Yard, logger *slog.Logger) ([]*inboundkafka.Consumer, error) {
	factory := inboundkafka.NewFactory(cfg.brokers, logger)
	var out []*inboundkafka.Consumer
	for _, build := range []func() (*inboundkafka.Consumer, error){
		func() (*inboundkafka.Consumer, error) {
			return inboundkafka.NewDockSchedulingConsumer(factory, cfg.dockGroup, yard, logger)
		},
		func() (*inboundkafka.Consumer, error) {
			return inboundkafka.NewFacilityConsumer(factory, cfg.facilityGroup, yard, logger)
		},
		func() (*inboundkafka.Consumer, error) {
			return inboundkafka.NewInboundReceivingConsumer(factory, cfg.inboundGroup, yard, logger)
		},
	} {
		c, err := build()
		if err != nil {
			for _, started := range out {
				_ = started.Close()
			}
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// consumerSet runs the consumers in goroutines of their own and stops them in
// order: cancel, wait for every Run to return, then close the readers.
type consumerSet struct {
	consumers []*inboundkafka.Consumer
	logger    *slog.Logger
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	failed    chan error
}

// startConsumers starts every consumer. Their context is NOT derived from the
// caller's cancellation (WithoutCancel): the HTTP drain happens first and stop()
// is the only thing that cancels them, so shutdown has one explicit order.
func startConsumers(ctx context.Context, logger *slog.Logger, consumers []*inboundkafka.Consumer) *consumerSet {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &consumerSet{consumers: consumers, logger: logger, cancel: cancel, failed: make(chan error, len(consumers))}
	for _, c := range consumers {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			logger.Info("consumer starting", "consumer", c.Name, "topic", c.Topic)
			// Run returns only when its context ends (a clean stop surfaces as
			// the cancellation of the fetch) or the reader fails.
			if err := c.Run(runCtx); !errors.Is(err, context.Canceled) {
				s.failed <- fmt.Errorf("%s died: %w", c.Name, err)
			}
		}()
	}
	return s
}

// failures delivers the first consumer that died of anything but a stop; a
// dead consumer silently stops feeding the local copies, so the process exits.
func (s *consumerSet) failures() <-chan error { return s.failed }

// stop cancels the consumers, waits for the in-flight message of each (its
// offset stays uncommitted when it was cut short: redelivered on restart) and
// closes the readers and dead-letter writers.
func (s *consumerSet) stop() error {
	s.cancel()
	s.wg.Wait()
	var errs []error
	for _, c := range s.consumers {
		if err := c.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", c.Name, err))
		}
	}
	return errors.Join(errs...)
}
