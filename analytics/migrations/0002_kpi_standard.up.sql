-- yard-management KPI standard v1 (ADR 0011): the facts behind gate-to-gate
-- turn time and the exception rates. Additive: nothing of 0001 changes.
--
-- Same rules as 0001: facts, never aggregates; every row is upserted by its
-- natural key, and the instants keep the earliest (LEAST) or fill a gap
-- (COALESCE), so the order in which events of different keys arrive never
-- matters and a replay of the topic converges to the same rows.

-- visit_facts: one row per visit.
--   site_id, purpose, arrived_at   from VisitCheckedIn (the only event carrying the purpose)
--   departed_at, on_site_minutes   from VisitCheckedOut (gate to gate = on_site_minutes)
-- A visit whose VisitCheckedIn was not seen has a NULL purpose and is left out
-- of the turn time report (replaying from the earliest offset is the backfill).
CREATE TABLE analytics.visit_facts (
    visit_id        TEXT COLLATE "C" PRIMARY KEY,
    site_id         TEXT COLLATE "C",
    purpose         TEXT COLLATE "C",
    arrived_at      TIMESTAMPTZ,
    departed_at     TIMESTAMPTZ,
    on_site_minutes BIGINT CHECK (on_site_minutes >= 0)
);

CREATE INDEX idx_visit_facts_departed ON analytics.visit_facts (departed_at) WHERE departed_at IS NOT NULL;
CREATE INDEX idx_visit_facts_arrived ON analytics.visit_facts (arrived_at) WHERE arrived_at IS NOT NULL;

-- gate_exceptions: one row per VisitRejected (kind VISIT_REJECTED, subject the
-- visit id, reason the rejection reason) and per SealExceptionRaised (kind
-- SEAL_EXCEPTION, subject the trailer id, or the visit id when the visit was
-- rejected at the gate and no trailer was persisted, reason NULL).
CREATE TABLE analytics.gate_exceptions (
    kind        TEXT COLLATE "C" NOT NULL CHECK (kind IN ('VISIT_REJECTED', 'SEAL_EXCEPTION')),
    subject_id  TEXT COLLATE "C" NOT NULL,
    occurred_at TIMESTAMPTZ      NOT NULL,
    site_id     TEXT COLLATE "C" NOT NULL,
    reason      TEXT COLLATE "C",
    PRIMARY KEY (kind, subject_id, occurred_at)
);

CREATE INDEX idx_gate_exceptions_occurred ON analytics.gate_exceptions (occurred_at);

-- trailer_holds: one row per TrailerHeld, keyed by the trailer and its
-- aggregate version after the hold. The payload carries no instant, so
-- held_at is the CloudEvents time of the event.
CREATE TABLE analytics.trailer_holds (
    trailer_id TEXT COLLATE "C" NOT NULL,
    version    BIGINT           NOT NULL CHECK (version >= 1),
    site_id    TEXT COLLATE "C" NOT NULL,
    reason     TEXT COLLATE "C" NOT NULL,
    held_at    TIMESTAMPTZ      NOT NULL,
    PRIMARY KEY (trailer_id, version)
);

CREATE INDEX idx_trailer_holds_held ON analytics.trailer_holds (held_at);

-- yard_check_results: one row per YardCheckClosed. discrepancies is the number
-- of findings in the event's list (the contract carries a list, not a count).
CREATE TABLE analytics.yard_check_results (
    yard_check_id TEXT COLLATE "C" PRIMARY KEY,
    site_id       TEXT COLLATE "C" NOT NULL,
    closed_at     TIMESTAMPTZ      NOT NULL,
    discrepancies INTEGER          NOT NULL CHECK (discrepancies >= 0)
);

CREATE INDEX idx_yard_check_results_closed ON analytics.yard_check_results (closed_at);

-- The reports role (when provisioned) may read, never write.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'yard_analytics_reader') THEN
        GRANT USAGE ON SCHEMA analytics TO yard_analytics_reader;
        GRANT SELECT ON ALL TABLES IN SCHEMA analytics TO yard_analytics_reader;
        ALTER DEFAULT PRIVILEGES IN SCHEMA analytics GRANT SELECT ON TABLES TO yard_analytics_reader;
    END IF;
END
$$;
