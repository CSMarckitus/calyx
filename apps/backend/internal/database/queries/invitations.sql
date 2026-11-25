-- name: CreateInvitation :one
INSERT INTO invitations (id, organization_id, client_id, role, email, token_hash, expires_at,
                         replaces_owner_id, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(organization_id), sqlc.narg(client_id), sqlc.arg(role), sqlc.arg(email),
        sqlc.arg(token_hash), sqlc.arg(expires_at), sqlc.narg(replaces_owner_id), now(), now())
RETURNING *;

-- name: DeletePendingClientInvitations :execrows
DELETE FROM invitations WHERE client_id = sqlc.arg(client_id)::uuid AND accepted_at IS NULL;

-- name: DeletePendingOwnerInvitations :exec
DELETE FROM invitations
WHERE organization_id = sqlc.arg(organization_id) AND role = 'owner' AND accepted_at IS NULL;

-- name: GetInvitationByTokenHash :one
SELECT * FROM invitations WHERE token_hash = sqlc.arg(token_hash);

-- name: GetInvitationForLookup :one
SELECT i.email, i.expires_at, i.accepted_at, o.name AS organization_name
FROM invitations i
JOIN organizations o ON o.id = i.organization_id
WHERE i.token_hash = sqlc.arg(token_hash);

-- name: LockInvitation :one
SELECT * FROM invitations WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: MarkInvitationAccepted :exec
UPDATE invitations SET accepted_at = sqlc.arg(accepted_at)::timestamptz, updated_at = now()
WHERE id = sqlc.arg(id);

-- name: GetOrganizationForInvitation :one
SELECT id, name FROM organizations WHERE id = sqlc.arg(id);

-- name: GetClientForInvitation :one
SELECT c.id, c.name, c.is_default, o.name AS organization_name
FROM clients c
JOIN organizations o ON o.id = c.organization_id
WHERE c.id = sqlc.arg(client_id) AND c.organization_id = sqlc.arg(organization_id);
