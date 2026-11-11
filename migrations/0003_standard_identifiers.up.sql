-- 0003_standard_identifiers.up.sql (ADR 0012): ISO 17712 seal class, the wider
-- seal state vocabulary, the unit number scheme and the site minimum seal
-- class. Additive and backwards compatible: every new column has a default
-- that reads as "not declared", and the seal state check only gains values, so
-- every existing row loads and every statement of the previous release still
-- runs.

ALTER TABLE trailers DROP CONSTRAINT trailers_seal_state_check;
ALTER TABLE trailers ADD CONSTRAINT trailers_seal_state_check
    CHECK (seal_state IN ('NONE', 'INTACT', 'BROKEN', 'APPLIED', 'VERIFIED', 'MISMATCH'));

-- '' = no class declared; the class is I, S or H (ISO 17712).
ALTER TABLE trailers ADD COLUMN seal_class TEXT NOT NULL DEFAULT ''
    CHECK (seal_class IN ('', 'I', 'S', 'H'));

-- '' = free-form unit number; ISO6346 = validated as an ISO 6346 number.
ALTER TABLE trailers ADD COLUMN unit_number_scheme TEXT NOT NULL DEFAULT ''
    CHECK (unit_number_scheme IN ('', 'ISO6346'));

-- '' = no minimum (today's behaviour).
ALTER TABLE yard_policies ADD COLUMN minimum_seal_class TEXT NOT NULL DEFAULT ''
    CHECK (minimum_seal_class IN ('', 'I', 'S', 'H'));
