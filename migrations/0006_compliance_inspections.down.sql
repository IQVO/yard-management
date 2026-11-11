-- 0006_compliance_inspections.down.sql: drops the inspection tables and the
-- policy column. The previous release has no inspection, so nothing it reads is
-- lost; the inspections already recorded are discarded with the tables.
ALTER TABLE yard_policies DROP COLUMN required_inspections;
DROP TABLE inspections;
DROP TABLE inspection_templates;
