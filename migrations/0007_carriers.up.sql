-- 0007_carriers.up.sql (ADR 0016): the local copy of the dock-scheduling carrier
-- registry, the suspended-carrier action of the yard policy and the carrier flag
-- of a visit. Additive and backwards compatible: one new table (nothing reads it
-- until the policy asks for the rule) and two new columns that default to "rule
-- off" and "no flag", so every existing row loads, is judged exactly as before
-- and every statement of the previous release still runs.

-- carriers: event-fed from CarrierRegistered, CarrierSuspended and
-- CarrierReinstated on warehouse.dock-scheduling.events. scac is the producer's
-- key. version is the producer's carrier version: a fact that is not newer than
-- the stored one changes nothing. name, usdot, mc and reason are '' when the
-- producer sent null (a carrier first seen through a suspension has no name).
CREATE TABLE carriers (
    scac       TEXT COLLATE "C" PRIMARY KEY,
    name       TEXT        NOT NULL DEFAULT '',
    usdot      TEXT        NOT NULL DEFAULT '',
    mc         TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL CHECK (status IN ('ACTIVE', 'SUSPENDED')),
    reason     TEXT        NOT NULL DEFAULT '',
    version    BIGINT      NOT NULL CHECK (version >= 1),
    updated_at TIMESTAMPTZ NOT NULL
);

-- NONE (the default) = the gate does not read the carrier copy, today's behaviour.
ALTER TABLE yard_policies ADD COLUMN suspended_carrier_action TEXT NOT NULL DEFAULT 'NONE'
    CHECK (suspended_carrier_action IN ('NONE', 'FLAG', 'REJECT'));

-- '' = no flag; SUSPENDED = admitted under the FLAG action while the carrier was suspended.
ALTER TABLE yard_visits ADD COLUMN carrier_flag TEXT NOT NULL DEFAULT ''
    CHECK (carrier_flag IN ('', 'SUSPENDED'));

-- A CARRIER_SUSPENDED rejection reason needs no schema change: rejection_reason
-- carries no CHECK constraint (see 0001).
