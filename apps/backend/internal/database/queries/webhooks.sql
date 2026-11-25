-- name: ListWebhooksOfInstance :many
SELECT * FROM webhooks WHERE instance_id = sqlc.arg(instance_id) ORDER BY event;

-- name: ListWebhooksInScope :many
SELECT w.instance_id, w.url, w.event, (w.secret_ciphertext IS NOT NULL)::boolean AS has_secret
FROM webhooks w
JOIN instances i ON i.id = w.instance_id
WHERE i.organization_id = sqlc.arg(organization_id)
  AND (sqlc.narg(client_id)::uuid IS NULL OR i.client_id = sqlc.narg(client_id))
ORDER BY w.instance_id, w.event;

-- name: DeleteWebhooksOfInstance :exec
DELETE FROM webhooks WHERE instance_id = sqlc.arg(instance_id);

-- name: InsertWebhook :exec
INSERT INTO webhooks (id, instance_id, url, event, secret_ciphertext, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(instance_id), sqlc.arg(url), sqlc.arg(event),
        sqlc.narg(secret_ciphertext), now(), now());
