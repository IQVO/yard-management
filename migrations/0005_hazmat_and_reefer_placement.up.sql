-- 0005_hazmat_and_reefer_placement.up.sql (ADR 0014): the contents
-- classification of a trailer, the spot kind of a yard spot and the hazmat
-- placement policy. Additive and backwards compatible: every new column
-- defaults to "nothing declared" / "kind unknown" / "policy off", so every
-- existing row loads, is judged exactly as before and every statement of the
-- previous release still runs.

-- US DOT hazard class ('' = none declared): a class 1 to 9 or a division of
-- classes 1, 2, 4, 5 and 6, written as the domain accepts it.
ALTER TABLE trailers ADD COLUMN hazmat_class TEXT NOT NULL DEFAULT ''
    CHECK (hazmat_class IN ('', '1', '1.1', '1.2', '1.3', '1.4', '1.5', '1.6', '2', '2.1', '2.2', '2.3', '3', '4', '4.1', '4.2', '4.3',
                            '5', '5.1', '5.2', '6', '6.1', '6.2', '7', '8', '9'));
-- UN and four digits ('' = none declared); a UN number needs a hazard class.
ALTER TABLE trailers ADD COLUMN un_number TEXT NOT NULL DEFAULT ''
    CHECK (un_number = '' OR un_number ~ '^UN[0-9]{4}$');
ALTER TABLE trailers ADD COLUMN requires_temperature_control BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE trailers ADD CONSTRAINT trailers_un_number_needs_class CHECK (un_number = '' OR hazmat_class <> '');

-- The facility-layout spotKind of a Yard slot, consumed from
-- LocationSlotRegistered; '' = none published (every spot registered before the
-- field existed), which the placement rules read as "unknown".
ALTER TABLE locations ADD COLUMN spot_kind TEXT NOT NULL DEFAULT ''
    CHECK (spot_kind IN ('', 'PARKING', 'HOLDING', 'REEFER_PLUG', 'HAZMAT', 'STAGING_LANE'));

-- false = a hazmat trailer needs a HAZMAT spot (today nothing is hazmat, so
-- nothing changes).
ALTER TABLE yard_policies ADD COLUMN allow_hazmat_on_unclassified_spots BOOLEAN NOT NULL DEFAULT false;
