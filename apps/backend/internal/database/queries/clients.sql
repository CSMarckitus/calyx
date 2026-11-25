-- name: CreateClient :one
INSERT INTO clients (id, organization_id, name, is_default, can_create_instances, max_instances, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(name), sqlc.arg(is_default),
        sqlc.arg(can_create_instances), sqlc.arg(max_instances), now(), now())
RETURNING *;

-- name: LockClient :one
-- Lock order: the organization first (when the caller locks it), then this client.
SELECT * FROM clients
WHERE id = sqlc.arg(id) AND organization_id = sqlc.arg(organization_id)
FOR UPDATE;

-- name: UpdateClient :one
UPDATE clients SET
    name                 = COALESCE(sqlc.narg(name)::text, name),
    can_create_instances = COALESCE(sqlc.narg(can_create_instances)::bool, can_create_instances),
    max_instances        = COALESCE(sqlc.narg(max_instances)::int, max_instances),
    updated_at           = now()
WHERE id = sqlc.arg(id) AND organization_id = sqlc.arg(organization_id)
RETURNING *;

-- name: GetClientView :one
SELECT sqlc.embed(clients),
       (SELECT count(*) FROM instances i WHERE i.client_id = clients.id)::int AS instance_count,
       COALESCE((SELECT u.email FROM users u
                 WHERE u.client_id = clients.id AND u.disabled_at IS NULL), '')::text AS user_email,
       inv.email AS pending_email,
       inv.expires_at AS pending_expires_at
FROM clients
-- At most one row: the index invitations_one_pending_per_client. An expired one also counts.
LEFT JOIN invitations inv ON inv.client_id = clients.id AND inv.role = 'client' AND inv.accepted_at IS NULL
WHERE clients.id = sqlc.arg(id) AND clients.organization_id = sqlc.arg(organization_id);

-- name: ListClientViews :many
SELECT sqlc.embed(clients),
       (SELECT count(*) FROM instances i WHERE i.client_id = clients.id)::int AS instance_count,
       COALESCE((SELECT u.email FROM users u
                 WHERE u.client_id = clients.id AND u.disabled_at IS NULL), '')::text AS user_email,
       inv.email AS pending_email,
       inv.expires_at AS pending_expires_at
FROM clients
-- At most one row: the index invitations_one_pending_per_client. An expired one also counts.
LEFT JOIN invitations inv ON inv.client_id = clients.id AND inv.role = 'client' AND inv.accepted_at IS NULL
WHERE clients.organization_id = sqlc.arg(organization_id)
ORDER BY clients.is_default DESC, clients.created_at, clients.id;

-- name: DeleteClient :exec
-- The client's user and invitations go with it (users.client_id and invitations.client_id are
-- ON DELETE CASCADE); the user's sessions, API keys and password resets
-- go with the user (ON DELETE CASCADE).
DELETE FROM clients WHERE id = sqlc.arg(id) AND organization_id = sqlc.arg(organization_id);
