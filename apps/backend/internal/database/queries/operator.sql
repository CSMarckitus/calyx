-- name: OperatorListActiveOwners :many
SELECT organization_id, email FROM users WHERE role = 'owner' AND disabled_at IS NULL;

-- name: OperatorGetActiveOwner :one
SELECT id, email FROM users
WHERE organization_id = sqlc.arg(organization_id) AND role = 'owner' AND disabled_at IS NULL;

-- name: OperatorListCoreNames :many
SELECT core_name FROM instances WHERE core_id = sqlc.arg(core_id);

-- name: OperatorForeignCoreID :one
SELECT core_id FROM instances WHERE core_id <> sqlc.arg(core_id) LIMIT 1;
