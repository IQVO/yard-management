-- 0002_idempotency_keys.up.sql: the Idempotency-Key store of the creating
-- POSTs and PUTs. The first writer of a key wins; only 2xx and 4xx answers
-- are stored. The fingerprint (method, path, body hash) makes a reuse of a key
-- with another request answer 409 idempotency-key-reused instead of replaying.
CREATE TABLE idempotency_keys (
    key          TEXT COLLATE "C" PRIMARY KEY,
    fingerprint  TEXT        NOT NULL,
    status       INTEGER     NOT NULL CHECK (status >= 200 AND status < 500),
    content_type TEXT        NOT NULL DEFAULT '',
    body         BYTEA       NOT NULL DEFAULT ''::bytea,
    created_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
