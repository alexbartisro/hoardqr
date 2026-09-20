-- name: InsertAuditLog :exec
-- §3's audit_log table, first written to by the MCP move_item tool (§11).
-- user_id is nullable and left NULL here, same as every other row in the
-- app right now — there's no real user to attribute it to until auth
-- exists (deferred indefinitely, see CLAUDE.md).
INSERT INTO audit_log (entity_type, entity_id, action, user_id, details)
VALUES (sqlc.arg(entity_type)::text, sqlc.arg(entity_id)::bigint, sqlc.arg(action)::text, sqlc.narg(user_id)::bigint, sqlc.arg(details)::jsonb);
