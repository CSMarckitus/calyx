-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, organization_id, role, client_id, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.arg(password_hash), sqlc.arg(organization_id),
        sqlc.arg(role), sqlc.narg(client_id), now(), now())
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = sqlc.arg(id);

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = sqlc.arg(email);

-- name: EmailExists :one
SELECT EXISTS (SELECT 1 FROM users WHERE email = sqlc.arg(email));

-- name: GetActiveOwner :one
SELECT * FROM users
WHERE organization_id = sqlc.arg(organization_id) AND role = 'owner' AND disabled_at IS NULL;

-- name: ClientHasActiveUser :one
SELECT EXISTS (SELECT 1 FROM users WHERE client_id = sqlc.arg(client_id)::uuid AND disabled_at IS NULL);

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = sqlc.arg(password_hash), updated_at = now()
WHERE id = sqlc.arg(id);

-- name: DisableUser :one
-- A user that is already disabled keeps its first disabled_at.
UPDATE users SET disabled_at = COALESCE(disabled_at, sqlc.arg(disabled_at)::timestamptz), updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: EnableUser :one
UPDATE users SET disabled_at = NULL, updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: LockUserByEmail :one
SELECT * FROM users WHERE email = sqlc.arg(email) FOR UPDATE;

-- name: GetActiveUserOfClient :one
SELECT * FROM users WHERE client_id = sqlc.arg(client_id)::uuid AND disabled_at IS NULL;

-- name: ReactivateClientUser :one
UPDATE users SET password_hash = sqlc.arg(password_hash), disabled_at = NULL, updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;
