-- 0005_hazmat_and_reefer_placement.down.sql: drops the classification, the
-- spot kind and the policy flag. The previous release has no placement rule,
-- so a classified trailer is simply unclassified for it.
ALTER TABLE yard_policies DROP COLUMN allow_hazmat_on_unclassified_spots;
ALTER TABLE locations DROP COLUMN spot_kind;
ALTER TABLE trailers DROP CONSTRAINT trailers_un_number_needs_class;
ALTER TABLE trailers DROP COLUMN requires_temperature_control;
ALTER TABLE trailers DROP COLUMN un_number;
ALTER TABLE trailers DROP COLUMN hazmat_class;
