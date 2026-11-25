-- name: InsertBulkJob :execrows
INSERT INTO bulk_jobs (id, instance_id, organization_id, client_id, created_by, created_at)
VALUES (sqlc.arg(id), sqlc.arg(instance_id), sqlc.arg(organization_id), sqlc.arg(client_id),
        sqlc.arg(created_by), now())
ON CONFLICT (id) DO NOTHING;

-- name: GetBulkJobInScope :one
SELECT * FROM bulk_jobs
WHERE id = sqlc.arg(id)
  AND organization_id = sqlc.arg(organization_id)
  AND (sqlc.narg(client_id)::uuid IS NULL OR client_id = sqlc.narg(client_id));

-- name: ListBulkJobsOfInstance :many
SELECT * FROM bulk_jobs
WHERE instance_id = sqlc.arg(instance_id)
ORDER BY created_at DESC, id DESC
LIMIT 50;
