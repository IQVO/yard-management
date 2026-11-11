-- 0003_standard_identifiers.down.sql: lossy on purpose. APPLIED, VERIFIED and
-- MISMATCH collapse to INTACT (a seal that is present and not broken), which is
-- what the previous release called every such seal; the class, the scheme and
-- the minimum are dropped.
UPDATE trailers SET seal_state = 'INTACT' WHERE seal_state IN ('APPLIED', 'VERIFIED', 'MISMATCH');

ALTER TABLE yard_policies DROP COLUMN minimum_seal_class;
ALTER TABLE trailers DROP COLUMN unit_number_scheme;
ALTER TABLE trailers DROP COLUMN seal_class;

ALTER TABLE trailers DROP CONSTRAINT trailers_seal_state_check;
ALTER TABLE trailers ADD CONSTRAINT trailers_seal_state_check
    CHECK (seal_state IN ('NONE', 'INTACT', 'BROKEN'));
