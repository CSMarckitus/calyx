-- name: CreateSession :one
INSERT INTO sessions (id, token_hash, user_id, expires_at, absolute_expires_at, last_seen_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(expires_at),
        sqlc.arg(absolute_expires_at), sqlc.arg(last_seen_at), now(), now())
RETURNING *;

-- name: GetSessionForAuth :one
SELECT s.id, s.expires_at, s.absolute_expires_at,
       u.id AS user_id, u.organization_id, u.role, u.client_id, u.disabled_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg(token_hash);

-- name: TouchSession :exec
UPDATE sessions SET expires_at = sqlc.arg(expires_at), last_seen_at = sqlc.arg(last_seen_at), updated_at = now()
WHERE id = sqlc.arg(id);

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = sqlc.arg(id);

-- name: DeleteSessionByTokenHash :exec
DELETE FROM sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteSessionsOfUser :exec
DELETE FROM sessions WHERE user_id = sqlc.arg(user_id);

-- name: DeleteOtherSessionsOfUser :exec
DELETE FROM sessions WHERE user_id = sqlc.arg(user_id) AND token_hash <> sqlc.arg(keep_token_hash);

-- name: DeleteExpiredSessionsOfUser :exec
DELETE FROM sessions
WHERE user_id = sqlc.arg(user_id)
  AND (expires_at <= sqlc.arg(now) OR absolute_expires_at <= sqlc.arg(now));
