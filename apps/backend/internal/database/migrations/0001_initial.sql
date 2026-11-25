-- Calyx backend: initial schema.
-- Ids are generated in Go (uuid.New()); no id has a default.
-- created_at and updated_at are informational: every INSERT and UPDATE sets them with now().
-- Times that rules compare (expiries, alerts) are passed from the Go clock as parameters.

CREATE TABLE organizations (
    id                           uuid PRIMARY KEY,
    name                         text NOT NULL CHECK (name <> ''),
    max_instances                int NOT NULL DEFAULT 10 CHECK (max_instances >= 0),
    whatsapp_web_allowed         boolean NOT NULL,
    whatsapp_web_acknowledged_at timestamptz NULL,
    whatsapp_web_acknowledged_by uuid NULL, -- foreign key to users added below (users does not exist yet)
    created_at                   timestamptz NOT NULL DEFAULT now(),
    updated_at                   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE clients (
    id                   uuid PRIMARY KEY,
    organization_id      uuid NOT NULL REFERENCES organizations (id),
    name                 text NOT NULL CHECK (name <> ''),
    is_default           boolean NOT NULL DEFAULT false,
    can_create_instances boolean NOT NULL DEFAULT false,
    max_instances        int NOT NULL DEFAULT 0 CHECK (max_instances >= 0), -- 0 = no limit of its own
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    -- Target of the composite foreign keys below: a row that names a client also names the
    -- organization of that client, so it can never point to a client of another organization.
    CONSTRAINT clients_organization_id_id_key UNIQUE (organization_id, id)
);
CREATE UNIQUE INDEX clients_one_default_per_org ON clients (organization_id) WHERE is_default;

CREATE TABLE users (
    id              uuid PRIMARY KEY,
    email           text NOT NULL CHECK (email = lower(email) AND email <> ''),
    password_hash   text NOT NULL, -- the row is created when the invitation is accepted
    organization_id uuid NOT NULL REFERENCES organizations (id),
    role            text NOT NULL CHECK (role IN ('owner', 'client')),
    client_id       uuid NULL,
    disabled_at     timestamptz NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_role_client CHECK ((role = 'owner') = (client_id IS NULL)),
    -- Deleting a client deletes its user; sessions, API keys and resets go with the user.
    CONSTRAINT users_client_fkey FOREIGN KEY (organization_id, client_id)
        REFERENCES clients (organization_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX users_one_active_owner_per_org ON users (organization_id)
    WHERE role = 'owner' AND disabled_at IS NULL;
CREATE UNIQUE INDEX users_one_active_user_per_client ON users (client_id)
    WHERE disabled_at IS NULL;

ALTER TABLE organizations
    ADD CONSTRAINT organizations_acknowledged_by_fkey
    FOREIGN KEY (whatsapp_web_acknowledged_by) REFERENCES users (id);

CREATE TABLE sessions (
    id                  uuid PRIMARY KEY,
    token_hash          bytea NOT NULL,
    user_id             uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at          timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX sessions_user_id ON sessions (user_id);

CREATE TABLE api_keys (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (name <> ''),
    prefix       text NOT NULL CHECK (length(prefix) = 8), -- first 8 characters after "cx_"
    token_hash   bytea NOT NULL,
    last_used_at timestamptz NULL,
    revoked_at   timestamptz NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_keys_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX api_keys_user_id ON api_keys (user_id);

CREATE TABLE invitations (
    id                uuid PRIMARY KEY,
    organization_id   uuid NOT NULL REFERENCES organizations (id),
    client_id         uuid NULL,
    role              text NOT NULL CHECK (role IN ('owner', 'client')),
    email             text NOT NULL CHECK (email = lower(email) AND email <> ''),
    token_hash        bytea NOT NULL,
    expires_at        timestamptz NOT NULL,
    accepted_at       timestamptz NULL,
    replaces_owner_id uuid NULL REFERENCES users (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT invitations_token_hash_key UNIQUE (token_hash),
    CONSTRAINT invitations_role_client CHECK ((role = 'owner') = (client_id IS NULL)),
    CONSTRAINT invitations_transfer_is_owner CHECK (replaces_owner_id IS NULL OR role = 'owner'),
    -- Deleting a client deletes its pending invitations.
    CONSTRAINT invitations_client_fkey FOREIGN KEY (organization_id, client_id)
        REFERENCES clients (organization_id, id) ON DELETE CASCADE
);

CREATE TABLE password_resets (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT password_resets_token_hash_key UNIQUE (token_hash)
);
CREATE INDEX password_resets_user_id ON password_resets (user_id);

CREATE TABLE instances (
    id                    uuid PRIMARY KEY,
    organization_id       uuid NOT NULL,
    client_id             uuid NOT NULL,
    core_id               text NOT NULL CHECK (core_id <> ''),
    core_name             text NOT NULL,
    display_name          text NOT NULL CHECK (display_name <> ''),
    provider              text NOT NULL CHECK (provider IN ('WHATSAPP_WEB', 'META_CLOUD')),
    send_only             boolean NOT NULL DEFAULT false,
    enabled               boolean NOT NULL DEFAULT true,
    phone_number_id       text NULL,
    device_name           text NULL,
    state                 text NOT NULL CHECK (state IN ('creating', 'active', 'removing', 'missing')),
    last_status           text NULL
        CHECK (last_status IN ('CONNECTED', 'CONNECTING', 'DISCONNECTED', 'NEEDS_AUTH', 'ERROR')),
    last_status_at        timestamptz NULL,
    status_checked_at     timestamptz NULL,
    alert_kind            text NULL CHECK (alert_kind IN ('needs_auth', 'error', 'disconnected', 'missing')),
    alert_opened_at       timestamptz NULL,
    alerted_at            timestamptz NULL,
    alert_attempts        int NOT NULL DEFAULT 0,
    alert_next_attempt_at timestamptz NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT instances_core_id_core_name_key UNIQUE (core_id, core_name),
    CONSTRAINT instances_phone_number_id_key UNIQUE (phone_number_id),
    CONSTRAINT instances_core_name_from_id CHECK (core_name = 'i_' || replace(id::text, '-', '')),
    CONSTRAINT instances_phone_number_id_meta_only
        CHECK ((provider = 'META_CLOUD') = (phone_number_id IS NOT NULL)),
    -- No cascade: a client with instances cannot be deleted.
    CONSTRAINT instances_client_fkey FOREIGN KEY (organization_id, client_id)
        REFERENCES clients (organization_id, id)
);
CREATE INDEX instances_organization_client ON instances (organization_id, client_id);

CREATE TABLE webhooks (
    id                uuid PRIMARY KEY,
    instance_id       uuid NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    url               text NOT NULL,
    event             text NOT NULL,
    secret_ciphertext bytea NULL, -- 12-byte nonce, then the AES-256-GCM ciphertext
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    -- The core allows one webhook per event on an instance.
    CONSTRAINT webhooks_instance_id_event_key UNIQUE (instance_id, event)
);

CREATE TABLE bulk_jobs (
    id              uuid PRIMARY KEY, -- the core's job id
    instance_id     uuid NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    organization_id uuid NOT NULL,
    client_id       uuid NOT NULL,
    created_by      uuid NOT NULL REFERENCES users (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT bulk_jobs_client_fkey FOREIGN KEY (organization_id, client_id)
        REFERENCES clients (organization_id, id)
);
CREATE INDEX bulk_jobs_instance_created ON bulk_jobs (instance_id, created_at DESC);

CREATE TABLE core_status (
    core_id           text PRIMARY KEY,
    failed_checks     int NOT NULL DEFAULT 0,
    unreachable_since timestamptz NULL,
    alerted_at        timestamptz NULL,
    last_ok_at        timestamptz NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
