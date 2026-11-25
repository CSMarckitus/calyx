-- name: CountInstancesOfOrganization :one
-- Every row counts, whatever its state.
SELECT count(*)::int AS count FROM instances WHERE organization_id = sqlc.arg(organization_id);

-- name: CountInstancesOfClient :one
SELECT count(*)::int AS count FROM instances WHERE client_id = sqlc.arg(client_id);

-- name: PhoneNumberIDInUse :one
SELECT EXISTS (SELECT 1 FROM instances WHERE phone_number_id = sqlc.arg(phone_number_id)::text)::bool AS in_use;

-- name: InsertInstance :one
INSERT INTO instances (id, organization_id, client_id, core_id, core_name, display_name, provider,
                       send_only, enabled, phone_number_id, device_identity, locale, state, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(client_id), sqlc.arg(core_id), sqlc.arg(core_name),
        sqlc.arg(display_name), sqlc.arg(provider), sqlc.arg(send_only), true,
        sqlc.narg(phone_number_id)::text, sqlc.narg(device_identity)::text, sqlc.narg(locale)::text,
        'creating', now(), now())
RETURNING *;

-- ---------------------------------------------------------------------------
-- The instances service.
-- ---------------------------------------------------------------------------

-- name: GetInstanceInScope :one
SELECT * FROM instances
WHERE id = sqlc.arg(id)
  AND organization_id = sqlc.arg(organization_id)
  AND (sqlc.narg(client_id)::uuid IS NULL OR client_id = sqlc.narg(client_id));

-- name: ListInstancesInScope :many
SELECT * FROM instances
WHERE organization_id = sqlc.arg(organization_id)
  AND (sqlc.narg(client_id)::uuid IS NULL OR client_id = sqlc.narg(client_id))
ORDER BY created_at, id;

-- name: GetInstanceByID :one
SELECT * FROM instances WHERE id = sqlc.arg(id);

-- name: GetClientForNewInstance :one
SELECT id, is_default, can_create_instances FROM clients
WHERE id = sqlc.arg(id)
  AND organization_id = sqlc.arg(organization_id)
  AND (sqlc.narg(client_id)::uuid IS NULL OR id = sqlc.narg(client_id));

-- name: GetDefaultClientID :one
SELECT id FROM clients
WHERE organization_id = sqlc.arg(organization_id) AND is_default;

-- name: GetOrganizationWhatsAppWeb :one
SELECT whatsapp_web_allowed, whatsapp_web_acknowledged_at FROM organizations
WHERE id = sqlc.arg(id);

-- name: PhoneNumberIDUsedByOther :one
-- New Meta credentials in Update. Create uses PhoneNumberIDInUse inside ReserveInstance.
SELECT EXISTS (
  SELECT 1 FROM instances
  WHERE phone_number_id = sqlc.arg(phone_number_id)::text AND id <> sqlc.arg(except_id)
) AS in_use;

-- name: ActivatePairedInstance :execrows
UPDATE instances
SET state = 'active',
    last_status = 'CONNECTED',
    last_status_at = sqlc.arg(now)::timestamptz,
    status_checked_at = sqlc.arg(now)::timestamptz,
    updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'creating';

-- name: SetRepairedInstanceConnected :exec
UPDATE instances
SET last_status = 'CONNECTED',
    last_status_at = CASE WHEN last_status IS DISTINCT FROM 'CONNECTED'
                          THEN sqlc.arg(now)::timestamptz ELSE last_status_at END,
    status_checked_at = sqlc.arg(now)::timestamptz,
    updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'active';

-- name: DeleteCreatingInstance :execrows
DELETE FROM instances WHERE id = sqlc.arg(id) AND state = 'creating';

-- name: UpdateInstanceSettings :one
UPDATE instances
SET display_name    = COALESCE(sqlc.narg(display_name)::text, display_name),
    enabled         = COALESCE(sqlc.narg(enabled)::boolean, enabled),
    send_only       = COALESCE(sqlc.narg(send_only)::boolean, send_only),
    phone_number_id = COALESCE(sqlc.narg(phone_number_id)::text, phone_number_id),
    updated_at      = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetInstanceState :execrows
UPDATE instances SET state = sqlc.arg(to_state), updated_at = now()
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: DeleteInstance :exec
DELETE FROM instances WHERE id = sqlc.arg(id);

-- name: ListRecoverableInstances :many
SELECT * FROM instances
WHERE core_id = sqlc.arg(core_id)
  AND ((state = 'creating' AND created_at < sqlc.arg(creating_before)::timestamptz)
       OR state = 'removing')
ORDER BY created_at, id;

-- name: SetInstanceIdentity :exec
-- The device identity and the locale that the core used for the last pairing.
UPDATE instances
SET device_identity = sqlc.narg(device_identity)::text,
    locale = sqlc.narg(locale)::text,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: ActivateRecoveredInstance :execrows
UPDATE instances
SET state = 'active',
    last_status = sqlc.arg(last_status)::text,
    last_status_at = sqlc.arg(now)::timestamptz,
    status_checked_at = sqlc.arg(now)::timestamptz,
    updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'creating';

-- ---------------------------------------------------------------------------
-- The watcher.
-- ---------------------------------------------------------------------------

-- name: ListWatchedInstances :many
SELECT * FROM instances WHERE core_id = sqlc.arg(core_id) ORDER BY created_at, id;

-- name: GetInstanceByCoreName :one
SELECT * FROM instances WHERE core_id = sqlc.arg(core_id) AND core_name = sqlc.arg(core_name);

-- name: SetObservedStatus :exec
UPDATE instances
SET last_status = sqlc.arg(status)::text,
    last_status_at = CASE WHEN last_status IS DISTINCT FROM sqlc.arg(status)::text
                          THEN sqlc.arg(now)::timestamptz ELSE last_status_at END,
    status_checked_at = sqlc.arg(now)::timestamptz,
    updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'active';

-- name: MarkInstanceMissing :execrows
UPDATE instances SET state = 'missing', updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'active';

-- name: MarkInstanceReappeared :execrows
UPDATE instances
SET state = 'active',
    last_status = sqlc.arg(status)::text,
    last_status_at = sqlc.arg(now)::timestamptz,
    status_checked_at = sqlc.arg(now)::timestamptz,
    updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'missing';

-- name: SaveAlertState :exec
UPDATE instances
SET alert_kind = sqlc.narg(alert_kind)::text,
    alert_opened_at = sqlc.narg(alert_opened_at)::timestamptz,
    alerted_at = sqlc.narg(alerted_at)::timestamptz,
    alert_attempts = sqlc.arg(alert_attempts),
    alert_next_attempt_at = sqlc.narg(alert_next_attempt_at)::timestamptz,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: GetAlertRecipient :one
SELECT u.email AS owner_email, o.name AS organization_name, c.name AS client_name
FROM instances i
JOIN organizations o ON o.id = i.organization_id
JOIN clients c ON c.id = i.client_id
JOIN users u ON u.organization_id = o.id AND u.role = 'owner' AND u.disabled_at IS NULL
WHERE i.id = sqlc.arg(id);

-- name: GetNextDueAlert :one
-- The next alert that is open, due and not mailed. The due rule is the one of watcher.Evaluate.
-- The state and enabled checks stop a mail for a row that changed after the round.
SELECT * FROM instances
WHERE core_id = sqlc.arg(core_id)
  AND enabled AND state IN ('active', 'missing')
  AND alert_kind IS NOT NULL AND alerted_at IS NULL
  AND (alert_next_attempt_at IS NULL OR alert_next_attempt_at <= sqlc.arg(now)::timestamptz)
  AND (alert_kind <> 'disconnected' OR alert_opened_at <= sqlc.arg(disconnected_before)::timestamptz)
ORDER BY alert_opened_at, id
LIMIT 1;

-- name: SaveAlertMailResult :execrows
-- The guard (same kind, same opening time) stops a mail from marking a newer alert.
UPDATE instances
SET alerted_at = sqlc.narg(alerted_at)::timestamptz,
    alert_attempts = sqlc.arg(alert_attempts),
    alert_next_attempt_at = sqlc.narg(alert_next_attempt_at)::timestamptz,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND alert_kind = sqlc.arg(alert_kind)::text
  AND alert_opened_at IS NOT DISTINCT FROM sqlc.narg(alert_opened_at)::timestamptz;
