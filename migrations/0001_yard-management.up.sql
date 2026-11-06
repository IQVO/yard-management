-- 0001_yard-management.up.sql: the whole schema of the yard-management
-- context (ADR 0002 aggregates, ADR 0003 local copies, ADR 0004 outbox).
--
-- Conventions: ids and codes are byte-ordered (COLLATE "C") so keyset pages
-- sort the same in Postgres and in the in-memory doubles; optional text is
-- the empty string, optional instants are NULL; every aggregate row carries
-- the optimistic-concurrency `version` (an INSERT is version 1, an UPDATE is
-- guarded by version - 1).

-- yard_visits: the YardVisit aggregate. At most one CHECKED_IN visit per
-- tractor plate and site (ErrVisitAlreadyOpen) is the partial unique index.
CREATE TABLE yard_visits (
    visit_id         TEXT COLLATE "C" PRIMARY KEY,
    site_id          TEXT COLLATE "C" NOT NULL,
    carrier          TEXT        NOT NULL,
    carrier_scac     TEXT        NOT NULL DEFAULT '',
    tractor_plate    TEXT COLLATE "C" NOT NULL,
    driver_ref       TEXT        NOT NULL DEFAULT '',
    purpose          TEXT        NOT NULL DEFAULT '' CHECK (purpose IN ('', 'DROP', 'LIVE_UNLOAD', 'LIVE_LOAD', 'PICKUP', 'BOBTAIL')),
    appointment_id   TEXT COLLATE "C" NOT NULL DEFAULT '',
    state            TEXT        NOT NULL CHECK (state IN ('CHECKED_IN', 'CHECKED_OUT', 'REJECTED')),
    rejection_reason TEXT        NOT NULL DEFAULT '',
    trailers_in      TEXT[]      NOT NULL DEFAULT '{}',
    trailers_out     TEXT[]      NOT NULL DEFAULT '{}',
    arrived_at       TIMESTAMPTZ NOT NULL,
    departed_at      TIMESTAMPTZ,
    version          BIGINT      NOT NULL CHECK (version >= 1),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_yard_visits_one_open_per_plate
    ON yard_visits (site_id, tractor_plate) WHERE state = 'CHECKED_IN';
CREATE INDEX idx_yard_visits_arrival ON yard_visits (arrived_at DESC, visit_id DESC);
CREATE INDEX idx_yard_visits_site ON yard_visits (site_id, arrived_at DESC, visit_id DESC);

-- trailers: the Trailer aggregate. A spot or a door holds at most one trailer
-- (ErrLocationOccupied): the partial unique index on the location of the
-- PARKED and DOCKED rows. The natural key (scac, unit number) is unique among
-- the trailers on site (ErrTrailerAlreadyOnSite).
CREATE TABLE trailers (
    trailer_id       TEXT COLLATE "C" PRIMARY KEY,
    site_id          TEXT COLLATE "C" NOT NULL,
    scac             TEXT COLLATE "C" NOT NULL DEFAULT '',
    unit_number      TEXT COLLATE "C" NOT NULL,
    kind             TEXT        NOT NULL CHECK (kind IN ('DRY_VAN', 'REEFER', 'FLATBED', 'CONTAINER')),
    load_state       TEXT        NOT NULL CHECK (load_state IN ('EMPTY', 'LOADED', 'PARTIAL')),
    asn_numbers      TEXT[]      NOT NULL DEFAULT '{}',
    outbound_ref     TEXT        NOT NULL DEFAULT '',
    purpose          TEXT        NOT NULL CHECK (purpose IN ('DROP', 'LIVE_UNLOAD', 'LIVE_LOAD', 'PICKUP', 'BOBTAIL')),
    seal_number      TEXT        NOT NULL DEFAULT '',
    seal_state       TEXT        NOT NULL CHECK (seal_state IN ('NONE', 'INTACT', 'BROKEN')),
    hold             TEXT        NOT NULL DEFAULT '' CHECK (hold IN ('', 'DAMAGED', 'SEAL_BROKEN', 'NOT_FOUND', 'QUALITY', 'SECURITY')),
    state            TEXT        NOT NULL CHECK (state IN ('PARKED', 'IN_MOVE', 'DOCKED', 'DEPARTED')),
    location_kind    TEXT        NOT NULL DEFAULT '' CHECK (location_kind IN ('', 'SPOT', 'DOOR')),
    location_code    TEXT COLLATE "C" NOT NULL DEFAULT '',
    door_work        TEXT        NOT NULL DEFAULT '' CHECK (door_work IN ('', 'WAITING', 'IN_PROGRESS', 'DONE')),
    origin_kind      TEXT        NOT NULL DEFAULT '' CHECK (origin_kind IN ('', 'SPOT', 'DOOR')),
    origin_code      TEXT COLLATE "C" NOT NULL DEFAULT '',
    origin_work      TEXT        NOT NULL DEFAULT '' CHECK (origin_work IN ('', 'WAITING', 'IN_PROGRESS', 'DONE')),
    visit_id         TEXT COLLATE "C" NOT NULL,
    appointment_id   TEXT COLLATE "C" NOT NULL DEFAULT '',
    arrived_at       TIMESTAMPTZ NOT NULL,
    departed_at      TIMESTAMPTZ,
    dwell_alerted_at TIMESTAMPTZ,
    version          BIGINT      NOT NULL CHECK (version >= 1),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((state IN ('PARKED', 'DOCKED')) = (location_code <> ''))
);

CREATE UNIQUE INDEX uq_trailers_one_per_location
    ON trailers (location_kind, location_code) WHERE state IN ('PARKED', 'DOCKED');
CREATE UNIQUE INDEX uq_trailers_unit_on_site
    ON trailers (site_id, scac, unit_number) WHERE state <> 'DEPARTED';
CREATE INDEX idx_trailers_site_on_site ON trailers (site_id, arrived_at, trailer_id) WHERE state <> 'DEPARTED';
CREATE INDEX idx_trailers_arrival ON trailers (arrived_at, trailer_id);
CREATE INDEX idx_trailers_appointment ON trailers (appointment_id) WHERE appointment_id <> '' AND state <> 'DEPARTED';

-- yard_moves: the YardMove aggregate. The three partial unique indexes are
-- the backstops of "a trailer has at most one open move"
-- (ErrTrailerHasOpenMove), "a destination is reserved by one open move"
-- (ErrDestinationReserved) and "a hostler holds one CLAIMED move"
-- (ErrHostlerBusy).
CREATE TABLE yard_moves (
    move_id          TEXT COLLATE "C" PRIMARY KEY,
    site_id          TEXT COLLATE "C" NOT NULL,
    trailer_id       TEXT COLLATE "C" NOT NULL,
    appointment_id   TEXT COLLATE "C" NOT NULL DEFAULT '',
    performer        TEXT        NOT NULL CHECK (performer IN ('HOSTLER', 'VISITING_DRIVER')),
    from_kind        TEXT        NOT NULL DEFAULT '' CHECK (from_kind IN ('', 'SPOT', 'DOOR')),
    from_code        TEXT COLLATE "C" NOT NULL DEFAULT '',
    to_kind          TEXT        NOT NULL CHECK (to_kind IN ('SPOT', 'DOOR')),
    to_code          TEXT COLLATE "C" NOT NULL,
    actual_kind      TEXT        NOT NULL DEFAULT '' CHECK (actual_kind IN ('', 'SPOT', 'DOOR')),
    actual_code      TEXT COLLATE "C" NOT NULL DEFAULT '',
    reason           TEXT        NOT NULL CHECK (reason IN ('TO_DOOR', 'FROM_DOOR', 'TO_SPOT', 'RELOCATE', 'TO_HOLDING')),
    priority_band    INTEGER     NOT NULL CHECK (priority_band BETWEEN 1 AND 9),
    due_by           TIMESTAMPTZ,
    state            TEXT        NOT NULL CHECK (state IN ('REQUESTED', 'CLAIMED', 'COMPLETED', 'CANCELLED')),
    hostler_ref      TEXT COLLATE "C" NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    requested_at     TIMESTAMPTZ NOT NULL,
    claimed_at       TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ,
    version          BIGINT      NOT NULL CHECK (version >= 1),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_yard_moves_one_open_per_trailer
    ON yard_moves (trailer_id) WHERE state IN ('REQUESTED', 'CLAIMED');
CREATE UNIQUE INDEX uq_yard_moves_open_destination
    ON yard_moves (to_kind, to_code) WHERE state IN ('REQUESTED', 'CLAIMED');
CREATE UNIQUE INDEX uq_yard_moves_one_claim_per_hostler
    ON yard_moves (site_id, hostler_ref) WHERE state = 'CLAIMED' AND hostler_ref <> '';
-- claim-next: lowest band, earliest due-by (none last), then request time.
CREATE INDEX idx_yard_moves_claimable
    ON yard_moves (site_id, priority_band, due_by NULLS LAST, requested_at, move_id)
    WHERE state = 'REQUESTED' AND performer = 'HOSTLER';
CREATE INDEX idx_yard_moves_expiry ON yard_moves (lease_expires_at) WHERE state = 'CLAIMED' AND performer = 'HOSTLER';
CREATE INDEX idx_yard_moves_appointment ON yard_moves (appointment_id) WHERE appointment_id <> '' AND state IN ('REQUESTED', 'CLAIMED');
CREATE INDEX idx_yard_moves_requested ON yard_moves (requested_at, move_id);

-- yard_checks: the YardCheck aggregate (observations and the discrepancies of
-- the closing are small ordered lists). At most one OPEN check per site
-- (ErrYardCheckOpen).
CREATE TABLE yard_checks (
    yard_check_id TEXT COLLATE "C" PRIMARY KEY,
    site_id       TEXT COLLATE "C" NOT NULL,
    state         TEXT        NOT NULL CHECK (state IN ('OPEN', 'CLOSED')),
    observations  JSONB       NOT NULL DEFAULT '[]',
    discrepancies JSONB       NOT NULL DEFAULT '[]',
    opened_at     TIMESTAMPTZ NOT NULL,
    closed_at     TIMESTAMPTZ,
    version       BIGINT      NOT NULL CHECK (version >= 1),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_yard_checks_one_open_per_site ON yard_checks (site_id) WHERE state = 'OPEN';

-- yard_policies: the YardPolicy aggregate, one row per site that ever declared
-- one. An undeclared site answers the default policy (version 1, never stored),
-- so the first stored version is 2.
CREATE TABLE yard_policies (
    site_id                 TEXT COLLATE "C" PRIMARY KEY,
    require_appointment     BOOLEAN     NOT NULL,
    holding_spot_codes      TEXT[]      NOT NULL DEFAULT '{}',
    parking_zone_ids        TEXT[]      NOT NULL DEFAULT '{}',
    dwell_threshold_minutes JSONB       NOT NULL DEFAULT '{}',
    lease_seconds           INTEGER     NOT NULL CHECK (lease_seconds >= 1),
    seal_mismatch_action    TEXT        NOT NULL CHECK (seal_mismatch_action IN ('FLAG', 'REJECT')),
    free_time_minutes       INTEGER     NOT NULL DEFAULT 0 CHECK (free_time_minutes >= 0),
    declared                BOOLEAN     NOT NULL,
    version                 BIGINT      NOT NULL CHECK (version >= 2),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Local copies (ADR 0003): event-fed, read by the use cases, never looked up live.

-- zones / locations: facility-layout. A slot registered before its zone keeps
-- a NULL site until the zone arrives (X5); a NULL site means unknown.
CREATE TABLE zones (
    zone_id   TEXT COLLATE "C" PRIMARY KEY,
    site_code TEXT COLLATE "C" NOT NULL
);

CREATE TABLE locations (
    location_code TEXT COLLATE "C" PRIMARY KEY,
    zone_id       TEXT COLLATE "C" NOT NULL,
    site_code     TEXT COLLATE "C",
    kind          TEXT    NOT NULL CHECK (kind IN ('SPOT', 'DOOR')),
    dock_flow     TEXT    NOT NULL DEFAULT '',
    active        BOOLEAN NOT NULL DEFAULT true
);

CREATE INDEX idx_locations_site ON locations (site_code, location_code) WHERE site_code IS NOT NULL;
CREATE INDEX idx_locations_zone ON locations (zone_id);

-- appointments: dock-scheduling. Version-guarded: only a newer fact changes a
-- known row; CANCELLED and NO_SHOW are terminal.
CREATE TABLE appointments (
    appointment_id            TEXT COLLATE "C" PRIMARY KEY,
    site_id                   TEXT COLLATE "C" NOT NULL DEFAULT '',
    direction                 TEXT        NOT NULL DEFAULT '',
    door_code                 TEXT COLLATE "C" NOT NULL DEFAULT '',
    carrier_scac              TEXT        NOT NULL DEFAULT '',
    window_start              TIMESTAMPTZ,
    window_end                TIMESTAMPTZ,
    asn_numbers               TEXT[]      NOT NULL DEFAULT '{}',
    shipment_refs             TEXT[]      NOT NULL DEFAULT '{}',
    expected_seal_number      TEXT        NOT NULL DEFAULT '',
    arrival_tolerance_minutes INTEGER     NOT NULL DEFAULT 0,
    status                    TEXT        NOT NULL CHECK (status IN ('BOOKED', 'CHECKED_IN', 'USED', 'CANCELLED', 'NO_SHOW')),
    version                   BIGINT      NOT NULL DEFAULT 0
);

CREATE INDEX idx_appointments_door ON appointments (door_code, window_start, appointment_id);

-- door_closures: dock-scheduling. Lifting an unseen closure leaves a lifted marker.
CREATE TABLE door_closures (
    closure_id  TEXT COLLATE "C" PRIMARY KEY,
    site_id     TEXT COLLATE "C" NOT NULL DEFAULT '',
    door_code   TEXT COLLATE "C" NOT NULL DEFAULT '',
    closed_from TIMESTAMPTZ,
    closed_to   TIMESTAMPTZ,
    lifted      BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX idx_door_closures_door ON door_closures (door_code) WHERE NOT lifted;

-- asns / receipts: inbound-receiving. An ASN once cancelled stays cancelled;
-- a receipt once closed stays closed.
CREATE TABLE asns (
    asn_number TEXT COLLATE "C" PRIMARY KEY,
    cancelled  BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE receipts (
    receipt_id     TEXT COLLATE "C" PRIMARY KEY,
    asn_number     TEXT COLLATE "C" NOT NULL DEFAULT '',
    appointment_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    door_code      TEXT COLLATE "C" NOT NULL DEFAULT '',
    closed_at      TIMESTAMPTZ
);

CREATE INDEX idx_receipts_appointment ON receipts (appointment_id) WHERE appointment_id <> '';

-- processed_events: the inbox. A consumed CloudEvents id is claimed with
-- INSERT ... ON CONFLICT DO NOTHING in the SAME transaction as the effect, so a
-- rollback un-claims it and the redelivery is processed.
CREATE TABLE processed_events (
    event_id     TEXT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- outbox: the transactional outbox. One row per domain fact, inserted in the
-- SAME transaction as the aggregate; the relay drains due rows to Kafka as
-- CloudEvents. payload is the self-describing {"type": <EventName>, "data":
-- {...}} envelope and is a JSON (not JSONB) column ON PURPOSE: the exact text
-- is preserved, so the relay publishes byte-identical data on every retry.
-- event_id is the CloudEvents id, minted once at enqueue time. A row that
-- failed OUTBOX_MAX_ATTEMPTS times stays with its attempts and last_error and
-- is excluded from claims until an operator re-arms it (attempts = 0).
CREATE TABLE outbox (
    id              BIGSERIAL   PRIMARY KEY,
    event_id        TEXT        NOT NULL UNIQUE,
    aggreg_type     TEXT        NOT NULL,
    aggreg_id       TEXT COLLATE "C" NOT NULL,
    payload         JSON        NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL,
    attempts        INTEGER     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_outbox_due ON outbox (attempts, next_attempt_at);
