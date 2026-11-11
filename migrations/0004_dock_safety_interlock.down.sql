-- 0004_dock_safety_interlock.down.sql: drops the securing facts. A trailer
-- that was secured is simply not secured for the previous release, which has no
-- interlock.
ALTER TABLE yard_policies DROP COLUMN require_securing_before_door_work;

ALTER TABLE trailers DROP CONSTRAINT trailers_secured_consistent;
ALTER TABLE trailers DROP COLUMN secured_by_ref;
ALTER TABLE trailers DROP COLUMN secured_at;
ALTER TABLE trailers DROP COLUMN secured_by;
