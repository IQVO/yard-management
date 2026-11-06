-- 0001_yard-management.down.sql: drops the whole schema of 0001 (no foreign
-- keys, so any order works; this one is the reverse of creation).
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS processed_events;
DROP TABLE IF EXISTS receipts;
DROP TABLE IF EXISTS asns;
DROP TABLE IF EXISTS door_closures;
DROP TABLE IF EXISTS appointments;
DROP TABLE IF EXISTS locations;
DROP TABLE IF EXISTS zones;
DROP TABLE IF EXISTS yard_policies;
DROP TABLE IF EXISTS yard_checks;
DROP TABLE IF EXISTS yard_moves;
DROP TABLE IF EXISTS trailers;
DROP TABLE IF EXISTS yard_visits;
