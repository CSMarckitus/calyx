-- name: CreatePasswordReset :one
INSERT INTO password_resets (id, user_id, token_hash, expires_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(token_hash), sqlc.arg(expires_at), now(), now())
RETURNING *;

-- name: DeletePendingPasswordResets :exec
DELETE FROM password_resets WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL;

-- name: GetPasswordResetByTokenHash :one
SELECT * FROM password_resets WHERE token_hash = sqlc.arg(token_hash);

-- name: LockPasswordReset :one
SELECT * FROM password_resets WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: MarkPasswordResetUsed :exec
UPDATE password_resets SET used_at = sqlc.arg(used_at)::timestamptz, updated_at = now()
WHERE id = sqlc.arg(id);
