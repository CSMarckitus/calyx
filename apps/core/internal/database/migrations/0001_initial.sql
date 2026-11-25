-- Calyx tables. Times are Unix milliseconds, ids are UUID text, booleans are 0/1.
-- whatsmeow creates its own tables (whatsmeow_*) in the same file.

CREATE TABLE instances (
    id                             TEXT PRIMARY KEY,
    name                           TEXT NOT NULL UNIQUE,
    provider                       TEXT NOT NULL,              -- WHATSAPP_WEB | META_CLOUD
    is_enabled                     INTEGER NOT NULL DEFAULT 1,
    is_used_only_for_send_messages INTEGER NOT NULL DEFAULT 0,
    connection_status              TEXT NOT NULL DEFAULT 'NEEDS_AUTH',
    device_jid                     TEXT,                       -- whatsmeow device, NULL until paired
    provider_config                TEXT,                       -- Meta credentials as JSON
    created_at                     INTEGER NOT NULL,
    updated_at                     INTEGER NOT NULL
);

CREATE TABLE webhooks (
    id          TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    url         TEXT NOT NULL,
    secret      TEXT,
    is_active   INTEGER NOT NULL DEFAULT 1,
    UNIQUE (instance_id, event_type)
);

CREATE TABLE bulk_jobs (
    id              TEXT PRIMARY KEY,
    instance_id     TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'QUEUED',
    total_messages  INTEGER NOT NULL,
    sent_messages   INTEGER NOT NULL DEFAULT 0,
    failed_messages INTEGER NOT NULL DEFAULT 0,
    estimated_ms    INTEGER NOT NULL,
    error           TEXT,
    created_at      INTEGER NOT NULL,
    started_at      INTEGER,
    finished_at     INTEGER
);

CREATE INDEX bulk_jobs_instance_status ON bulk_jobs (instance_id, status);

CREATE TABLE bulk_job_messages (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id              TEXT NOT NULL REFERENCES bulk_jobs(id) ON DELETE CASCADE,
    position            INTEGER NOT NULL,
    recipient           TEXT NOT NULL,
    text                TEXT NOT NULL,                         -- cleared once processed
    delay_ms            INTEGER NOT NULL,
    pause_after_ms      INTEGER NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'PENDING',
    provider_message_id TEXT,
    error               TEXT,
    sent_at             INTEGER,
    UNIQUE (job_id, position)
);

CREATE INDEX bulk_job_messages_job_status ON bulk_job_messages (job_id, status);
