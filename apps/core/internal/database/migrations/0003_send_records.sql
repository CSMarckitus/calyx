-- One row for each single send and for each bulk job with an idempotency key. The rows hold no
-- message text and no recipient. Times are Unix milliseconds, as in the other tables.

CREATE TABLE send_records (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id     TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,   -- SINGLE | BULK
    idempotency_key TEXT,            -- NULL when the request had no key
    fingerprint     BLOB NOT NULL,   -- SHA-256 of the content (32 bytes)
    status          TEXT NOT NULL,   -- SINGLE: PENDING | SENT | UNKNOWN; BULK: CREATED
    result_id       TEXT,            -- SENT: the provider message id; CREATED: the bulk job id
    created_at      INTEGER NOT NULL,
    finished_at     INTEGER          -- time of SENT, UNKNOWN or CREATED
);
CREATE UNIQUE INDEX send_records_key ON send_records (instance_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX send_records_fingerprint ON send_records (instance_id, fingerprint);
CREATE INDEX send_records_created ON send_records (created_at);
