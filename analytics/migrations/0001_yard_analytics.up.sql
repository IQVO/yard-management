-- yard-management analytics read model (ADR 0006).
--
-- Everything is in the schema "analytics", owned by the projector's own role and
-- readable by the reports role. It is written only by cmd/yard-projector and
-- is a projection of warehouse.yard-management.analytics: it can be dropped and
-- rebuilt by replaying the topic under a new consumer group.
--
-- Facts, not aggregates: the three reports are counted from these rows over
-- the requested window, so any window can be recomputed.

CREATE SCHEMA IF NOT EXISTS analytics;

-- Idempotency: every applied CloudEvents id is recorded exactly once, in the
-- SAME transaction as its effect. occurred_at is the CloudEvents `time`;
-- max(occurred_at) is the freshness of the projection.
CREATE TABLE analytics.processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics.processed_events (occurred_at);

-- trailer_facts: one row per trailer.
--   kind, unit_number, visit_id, arrived_at  from VisitCheckedIn (the only event carrying the kind)
--   first_door_work_started_at               the earliest DoorWorkStarted (driver wait = this - arrived_at)
--   departed_at, dwell_minutes               TrailerDeparted
-- A trailer whose VisitCheckedIn was not seen has a NULL kind and is left out
-- of the dwell report (replaying from the earliest offset is the backfill).
CREATE TABLE analytics.trailer_facts (
    trailer_id                 TEXT COLLATE "C" PRIMARY KEY,
    site_id                    TEXT COLLATE "C",
    kind                       TEXT COLLATE "C",
    unit_number                TEXT COLLATE "C",
    visit_id                   TEXT COLLATE "C",
    arrived_at                 TIMESTAMPTZ,
    first_door_work_started_at TIMESTAMPTZ,
    departed_at                TIMESTAMPTZ,
    dwell_minutes              BIGINT
);

CREATE INDEX idx_trailer_facts_departed ON analytics.trailer_facts (departed_at) WHERE departed_at IS NOT NULL;

-- door_dockings: one row per TrailerDocked, closed by the next TrailerUndocked
-- of the same trailer and door (same Kafka key, so they arrive in order).
CREATE TABLE analytics.door_dockings (
    trailer_id  TEXT COLLATE "C" NOT NULL,
    docked_at   TIMESTAMPTZ      NOT NULL,
    site_id     TEXT COLLATE "C" NOT NULL,
    door_code   TEXT COLLATE "C" NOT NULL,
    undocked_at TIMESTAMPTZ,
    PRIMARY KEY (trailer_id, docked_at)
);

CREATE INDEX idx_door_dockings_door ON analytics.door_dockings (site_id, door_code, docked_at);

-- door_work: one row per DoorWorkStarted, closed by the next DoorWorkCompleted
-- of the same trailer.
CREATE TABLE analytics.door_work (
    trailer_id   TEXT COLLATE "C" NOT NULL,
    started_at   TIMESTAMPTZ      NOT NULL,
    site_id      TEXT COLLATE "C" NOT NULL,
    door_code    TEXT COLLATE "C" NOT NULL,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (trailer_id, started_at)
);

CREATE INDEX idx_door_work_door ON analytics.door_work (site_id, door_code, started_at);

-- hostler_moves: one row per YardMoveCompleted performed by a HOSTLER (the
-- visiting driver's moves are not yard-truck productivity).
CREATE TABLE analytics.hostler_moves (
    move_id          TEXT COLLATE "C" PRIMARY KEY,
    site_id          TEXT COLLATE "C" NOT NULL,
    trailer_id       TEXT COLLATE "C" NOT NULL,
    hostler_ref      TEXT COLLATE "C" NOT NULL,
    completed_at     TIMESTAMPTZ      NOT NULL,
    duration_seconds BIGINT           NOT NULL CHECK (duration_seconds >= 0)
);

CREATE INDEX idx_hostler_moves_completed ON analytics.hostler_moves (completed_at);

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
