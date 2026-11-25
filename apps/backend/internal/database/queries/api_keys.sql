-- name: CreateAPIKey :one
INSERT INTO api_keys (id, user_id, name, prefix, token_hash, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(prefix), sqlc.arg(token_hash), now(), now())
RETURNING *;

-- name: ListActiveAPIKeysOfUser :many
SELECT * FROM api_keys
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL
ORDER BY created_at DESC, id;

-- name: GetAPIKeyForAuth :one
SELECT k.id, u.id AS user_id, u.organization_id, u.role, u.client_id, u.disabled_at
FROM api_keys k
JOIN users u ON u.id = k.user_id
WHERE k.token_hash = sqlc.arg(token_hash) AND k.revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = sqlc.arg(last_used_at)::timestamptz
WHERE id = sqlc.arg(id)
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg(last_used_at)::timestamptz - interval '1 minute');

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET revoked_at = sqlc.arg(revoked_at)::timestamptz, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: RevokeAPIKeysOfUser :exec
UPDATE api_keys SET revoked_at = sqlc.arg(revoked_at)::timestamptz, updated_at = now()
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;
