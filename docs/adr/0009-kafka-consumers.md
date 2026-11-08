# ADR 0009: Kafka consumers

## Status

Accepted (2026-11-06). Refines the dispatch and dead-letter bullets of the Rules section of ADR 0003.

## Context

ADR 0003 fixed WHAT yard-management consumes and the local copies it feeds; the use cases that fold each event
(`ApplyZone`, `ApplySlot`, `DecommissionSlot`, `ApplyAppointment`, `ScheduleClosure`, `LiftClosure`, `RegisterAsn`,
`CancelAsn`, `OpenReceipt`, `CloseReceipt`) already exist in `internal/application/usecases/consumers.go`, each claiming the
CloudEvents id in `processed_events` in the same unit of work as its effect. This ADR records the inbound adapter that
reads Kafka and calls them: `internal/adapters/inbound/kafka`.

## Decision

### Three consumers, one per producer topic

| consumer | constructor | topic | consumer group env |
|---|---|---|---|
| dock-scheduling | `NewDockSchedulingConsumer(f Factory, groupID string, uc DockSchedulingUseCases, logger *slog.Logger) (*Consumer, error)` | `warehouse.dock-scheduling.events` | `DOCK_SCHEDULING_CONSUMER_GROUP` |
| facility-layout | `NewFacilityConsumer(f Factory, groupID string, uc FacilityUseCases, logger *slog.Logger) (*Consumer, error)` | `warehouse.facility.events` | `FACILITY_CONSUMER_GROUP` |
| inbound-receiving | `NewInboundReceivingConsumer(f Factory, groupID string, uc InboundReceivingUseCases, logger *slog.Logger) (*Consumer, error)` | `warehouse.inbound-receiving.events` | `INBOUND_RECEIVING_CONSUMER_GROUP` |

`*usecases.Yard` implements all three use-case interfaces (a compile-time check lives in the package). Each constructor has a
`...ForTopic` twin taking an explicit topic (the integration tests use unique topics). The constructors do not dial. A blank
group is `ErrGroupRequired` naming the environment variable, so boot fails naming it. Group ids come from configuration
only; the package contains no group literal.

`Factory` is the Kafka seam: `NewReader(topic, groupID) Reader` and `NewDeadLetter(topic) DeadLetterWriter`. `NewFactory(brokers,
logger)` is the production implementation over kafka-go (readers without `CommitInterval`, so commits are synchronous and
explicit; dead-letter writers with acks=all). Unit tests pass fakes, integration tests pass `NewFactory` over the testcontainers
broker. Nothing here is wired in `cmd/**`; the composition root owns that.

### Dispatch and decoding

- Every message goes through `cloudevents.Decode`; a value that is not a valid CloudEvents 1.0 event (including the retired
  flat envelope, junk and empty values) is logged at WARN and committed past, never dead-lettered (its type is unknowable).
- Dispatch is a map keyed by the FULL `type` string, byte-identical to the producers' AsyncAPI. Any other type on the topic
  (dock-scheduling analytics-only types, facility-layout's other aggregates, inbound-receiving's legacy-importer
  `dockappointment.*` types and receipt line events) is ignored and committed past.
- Decoding is tolerant: unknown fields are ignored, optional fields default (an omitted `appointment_id` of a walk-in
  `ReceiptOpened` is `""`, a null `carrier_scac` is `""`). facility-layout payloads are camelCase, the others snake_case.
- A facility slot whose role is neither `Yard` nor `Dock` (including an absent role = Storage) is skipped in the adapter
  before the unit of work, so the thousands of storage slots do not each write an inbox row.

### Poison versus transient

A message of a HANDLED type that can never be applied is poison: a `data` that does not decode (not an object, a time that
does not parse), a blank natural id (`appointment_id`, `closure_id`, `door_code` where the copy is keyed or joined on it,
`zoneId`, `siteCode`, `locationCode`, `asn_number`, `receipt_id`), an appointment fact without `version` >= 1 (the copy's
ordering guard), a Booked / Rescheduled without a window, a closure without a span, a ReceiptClosed without `closed_at`.
Poison is republished untouched (key and value) to `<topic>.dlq` with headers `x-dlq-source-topic` and `x-dlq-cause`, and its
offset is committed. The dead-letter publish itself is retried forever; a message is never committed past undelivered.

Everything else a use case returns is TRANSIENT: the SAME message is retried with capped exponential backoff (200 ms doubling to
5 s) and its offset is not committed until it succeeds. The unit of work rolls the inbox claim back with the effect, so the
redelivery is processed, not skipped as a duplicate. **This differs from ADR 0003's "dead-lettered after 5 attempts":**
dead-lettering good facts because Postgres was briefly down would park them and break per-aggregate order; the fleet's
exception-management consumers retry forever for the same reason, and so does this adapter. A message that is interrupted by
shutdown (`ctx` cancelled) is neither retried past the cancellation nor dead-lettered (fleet escape 18b): the guard lives in
the deciding function, `handleWithDeadLetter`, and the offset stays uncommitted for the restart.

### Ordering, replay and version guard

Idempotency is the claim of the CloudEvents id: a redelivery, a second pass by a new consumer group (replay) and a topic
re-read after a rewind change nothing. Per-aggregate order is the producer's key (appointment id, ASN number, location code);
the copies are also tolerant of the cross-topic and within-topic reorderings that ADR 0003 lists: a Cancelled before its Booked
leaves the row CANCELLED, an older Booked or Rescheduled never overwrites a newer version, a closure lifted before it was
scheduled stays lifted, an ASN cancelled before it was registered stays cancelled, a slot before its zone is pending until the zone
arrives (X5), a receipt closed before it was opened stays closed. The aggregate `version` of the appointment facts is the guard;
the closure, ASN and receipt copies are last-writer tombstones and carry none.

### Policies run inside the same unit of work

`ApplyAppointment` runs NoShowFreesHolding after the copy changed (Cancelled, NoShow, or a Rescheduled whose `previous_door_code`
differs from `door_code`): the open hostler TO_DOOR moves of that appointment are cancelled and the trailer stays where it is.
`CloseReceipt` runs AutoCompleteUnload (Y-7): the receipt joins to the trailer through the `appointment_id` of the ReceiptOpened
copy, and door work completes (WAITING or IN_PROGRESS to DONE, load state EMPTY) only when EVERY ASN on the trailer has a closed
receipt. Both are proven end to end through Kafka and Postgres in the integration tests.

### Tests

Unit tests use the producers' own AsyncAPI examples as fixtures (dock-scheduling from `origin/feature/service`, inbound-receiving
from `origin/develop`; facility-layout's AsyncAPI has no examples, so its fixtures are built from the message schemas) and assert
the mapping into the use-case arguments, every poison class, ignored types, the retry/commit/dead-letter loop and the
cancellation guard. Integration tests (`-tags=integration`, testcontainers Kafka and Postgres, no skips) cover replay,
redelivery, rollback and retry, reordering, dead-lettering with headers, malformed payloads, unknown types, junk, the version
guard, restart from the committed offset, NoShowFreesHolding and AutoCompleteUnload.

## Consequences

- A transiently unhandleable message blocks its partition (logged at ERROR on every attempt) rather than being lost.
- A deterministic domain error returned by a use case (none is known today; the policies swallow the expected ones) would be
  treated as transient and wedge the partition; if one appears it must be classified as poison here, with a test.
- A Rescheduled or NoShow that creates the row before its Booked arrives keeps no ASN coverage or seal (a later lower-version
  Booked is ignored). Within one appointment the producer's key preserves order, so this needs a rewind across a retention gap.
- A ReceiptClosed that arrives before its ReceiptOpened leaves the receipt closed but does not re-run AutoCompleteUnload when the
  Opened arrives later; the two share the ASN-number key, so per-partition order makes this a replay-only edge.
- dock-scheduling's `DockAppointmentCheckedIn` and `Completed` are not consumed (X4); yard-management marks its own copy used.
