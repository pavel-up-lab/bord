-- name: SchemaVersion :one
SELECT version FROM schema_migrations LIMIT 1;
