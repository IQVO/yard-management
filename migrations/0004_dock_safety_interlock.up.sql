-- 0004_dock_safety_interlock.up.sql (ADR 0013): the dock safety interlock.
-- Additive and backwards compatible: every new column defaults to "not
-- secured" / "policy off", so every existing row loads, behaves as before and
-- every statement of the previous release still runs.

-- '' = not secured; a docked trailer is secured by a vehicle restraint or by
-- wheel chocks. secured_at is set exactly when secured_by is.
ALTER TABLE trailers ADD COLUMN secured_by TEXT NOT NULL DEFAULT ''
    CHECK (secured_by IN ('', 'RESTRAINT', 'WHEEL_CHOCKS'));
ALTER TABLE trailers ADD COLUMN secured_at TIMESTAMPTZ;
ALTER TABLE trailers ADD COLUMN secured_by_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE trailers ADD CONSTRAINT trailers_secured_consistent
    CHECK ((secured_by = '') = (secured_at IS NULL) AND (secured_by <> '' OR secured_by_ref = ''));

-- false = door work starts without the interlock (today's behaviour).
ALTER TABLE yard_policies ADD COLUMN require_securing_before_door_work BOOLEAN NOT NULL DEFAULT false;
