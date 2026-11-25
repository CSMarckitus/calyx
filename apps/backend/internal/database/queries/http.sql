-- name: HTTPGetOrganizationSummary :one
SELECT id, name FROM organizations WHERE id = sqlc.arg(id);

-- name: HTTPGetUser :one
SELECT * FROM users WHERE id = sqlc.arg(id);

