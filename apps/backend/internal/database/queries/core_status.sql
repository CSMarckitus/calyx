-- name: GetCoreUnreachableSince :one
SELECT unreachable_since FROM core_status WHERE core_id = sqlc.arg(core_id);

-- name: RecordCoreCheckFailed :one
INSERT INTO core_status (core_id, failed_checks, created_at, updated_at)
VALUES (sqlc.arg(core_id), 1, now(), now())
ON CONFLICT (core_id) DO UPDATE SET
  failed_checks = core_status.failed_checks + 1,
  unreachable_since = CASE
      WHEN core_status.unreachable_since IS NULL AND core_status.failed_checks + 1 >= 2
      THEN sqlc.arg(now)::timestamptz
      ELSE core_status.unreachable_since
  END,
  updated_at = now()
RETURNING core_id, failed_checks, unreachable_since, alerted_at, last_ok_at;

-- name: RecordCoreCheckOK :exec
INSERT INTO core_status (core_id, failed_checks, unreachable_since, alerted_at, last_ok_at, created_at, updated_at)
VALUES (sqlc.arg(core_id), 0, NULL, NULL, sqlc.arg(now)::timestamptz, now(), now())
ON CONFLICT (core_id) DO UPDATE SET
  failed_checks = 0,
  unreachable_since = NULL,
  alerted_at = NULL,
  last_ok_at = sqlc.arg(now)::timestamptz,
  updated_at = now();

-- name: MarkCoreAlertSent :exec
UPDATE core_status SET alerted_at = sqlc.arg(now)::timestamptz, updated_at = now()
WHERE core_id = sqlc.arg(core_id);

-- name: GetCoreStatus :one
SELECT core_id, failed_checks, unreachable_since, alerted_at, last_ok_at
FROM core_status WHERE core_id = sqlc.arg(core_id);
