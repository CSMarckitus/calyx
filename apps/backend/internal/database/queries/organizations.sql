-- name: InsertOrganization :one
INSERT INTO organizations (id, name, max_instances, whatsapp_web_allowed, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(max_instances), sqlc.arg(whatsapp_web_allowed), now(), now())
RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = sqlc.arg(id);

-- name: LockOrganization :one
SELECT * FROM organizations WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateOrganization :one
UPDATE organizations SET
    name                 = COALESCE(sqlc.narg(name)::text, name),
    max_instances        = COALESCE(sqlc.narg(max_instances)::int, max_instances),
    whatsapp_web_allowed = COALESCE(sqlc.narg(whatsapp_web_allowed)::bool, whatsapp_web_allowed),
    updated_at           = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: AcknowledgeWhatsAppWeb :one
-- The first acceptance is kept: a second call changes nothing but updated_at.
UPDATE organizations SET
    whatsapp_web_acknowledged_at = COALESCE(whatsapp_web_acknowledged_at, sqlc.arg(acknowledged_at)::timestamptz),
    whatsapp_web_acknowledged_by = COALESCE(whatsapp_web_acknowledged_by, sqlc.arg(user_id)::uuid),
    updated_at                   = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetOrganizationView :one
SELECT sqlc.embed(organizations),
       (SELECT count(*) FROM instances i WHERE i.organization_id = organizations.id)::int AS instance_count
FROM organizations
WHERE organizations.id = sqlc.arg(id);

-- name: ListOrganizationViews :many
SELECT sqlc.embed(organizations),
       (SELECT count(*) FROM instances i WHERE i.organization_id = organizations.id)::int AS instance_count
FROM organizations
ORDER BY organizations.created_at, organizations.id;
