-- 0007_carriers.down.sql: drops the carrier copy, the policy action and the visit
-- flag. The previous release has no carrier rule, so a flagged visit is simply an
-- unflagged one for it; a REJECTED visit with reason CARRIER_SUSPENDED stays as
-- history (the previous release stores the reason as text).
ALTER TABLE yard_visits DROP COLUMN carrier_flag;
ALTER TABLE yard_policies DROP COLUMN suspended_carrier_action;
DROP TABLE carriers;
