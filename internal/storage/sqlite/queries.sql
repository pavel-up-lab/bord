-- name: SchemaVersion :one
SELECT version FROM schema_migrations LIMIT 1;

-- name: NextID :one
INSERT INTO id_sequences(kind, value) VALUES (sqlc.arg(kind), 1)
ON CONFLICT(kind) DO UPDATE SET value = value + 1
RETURNING value;

-- name: ListBoards :many
SELECT id, slug, title, enabled, created_at
FROM boards
WHERE enabled = 1
ORDER BY slug ASC;

-- name: BoardBySlug :one
SELECT id, slug, title, enabled, created_at
FROM boards
WHERE slug = sqlc.arg(slug);

-- name: BoardByID :one
SELECT id, slug, title, enabled, created_at
FROM boards
WHERE id = sqlc.arg(id);

-- name: ListManagedBoards :many
SELECT b.id, b.slug, b.title, b.enabled, b.created_at,
       (SELECT count(*) FROM threads t WHERE t.board_id = b.id) AS thread_count,
       (SELECT count(*) FROM posts p JOIN threads t ON t.id = p.thread_id WHERE t.board_id = b.id) AS post_count
FROM boards b
WHERE (sqlc.arg(before_id) = 0 OR b.id < sqlc.arg(before_id))
  AND (sqlc.arg(board_state) = 'all'
       OR (sqlc.arg(board_state) = 'active' AND b.enabled = 1)
       OR (sqlc.arg(board_state) = 'archived' AND b.enabled = 0))
ORDER BY b.id DESC
LIMIT sqlc.arg(page_size);

-- name: CreateBoard :exec
INSERT INTO boards(id, slug, title, enabled, created_at)
VALUES (sqlc.arg(id), sqlc.arg(slug), sqlc.arg(title), sqlc.arg(enabled), sqlc.arg(created_at));

-- name: SetBoardEnabled :execrows
UPDATE boards
SET enabled = sqlc.arg(enabled)
WHERE id = sqlc.arg(id) AND enabled = sqlc.arg(expected_enabled);

-- name: ActiveBoardForThread :one
SELECT b.id
FROM boards b JOIN threads t ON t.board_id = b.id
WHERE t.id = sqlc.arg(thread_id) AND b.enabled = 1;

-- name: ActiveBoardForPost :one
SELECT b.id
FROM boards b JOIN threads t ON t.board_id = b.id JOIN posts p ON p.thread_id = t.id
WHERE p.id = sqlc.arg(post_id) AND b.enabled = 1;

-- name: ActiveBoardForAttachment :one
SELECT b.id
FROM boards b
JOIN threads t ON t.board_id = b.id
JOIN posts p ON p.thread_id = t.id
JOIN attachments a ON a.id = p.attachment_id
WHERE a.id = sqlc.arg(attachment_id) AND b.enabled = 1;

-- name: ThreadByID :one
SELECT id, board_id, status, created_at, bumped_at
FROM threads
WHERE id = sqlc.arg(id);

-- name: ListThreads :many
SELECT id, board_id, status, created_at, bumped_at
FROM threads
WHERE board_id = sqlc.arg(board_id)
  AND (sqlc.arg(has_cursor) = 0
       OR bumped_at < sqlc.arg(cursor_bumped_at)
       OR (bumped_at = sqlc.arg(cursor_bumped_at) AND id < sqlc.arg(cursor_id)))
ORDER BY bumped_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: InsertThread :exec
INSERT INTO threads(id, board_id, status, created_at, bumped_at)
VALUES (sqlc.arg(id), sqlc.arg(board_id), sqlc.arg(status), sqlc.arg(created_at), sqlc.arg(bumped_at));

-- name: UpdateThreadBump :execrows
UPDATE threads SET bumped_at = sqlc.arg(bumped_at) WHERE id = sqlc.arg(id);

-- name: PostByID :one
SELECT *
FROM posts
WHERE id = sqlc.arg(id);

-- name: ListPosts :many
SELECT *
FROM posts
WHERE thread_id = sqlc.arg(thread_id) AND id > sqlc.arg(after_id)
ORDER BY id ASC
LIMIT sqlc.arg(page_size);

-- name: InsertPost :exec
INSERT INTO posts(id, thread_id, reply_to_id, author_id, body, attachment_id, status, created_at, deleted_at)
VALUES (sqlc.arg(id), sqlc.arg(thread_id), sqlc.narg(reply_to_id), sqlc.arg(author_id), sqlc.arg(body),
        sqlc.narg(attachment_id), sqlc.arg(status), sqlc.arg(created_at), sqlc.narg(deleted_at));

-- name: TombstonePost :execrows
UPDATE posts
SET body = '', attachment_id = NULL, status = 'deleted', deleted_at = sqlc.arg(deleted_at)
WHERE id = sqlc.arg(id) AND status = 'visible';

-- name: InsertDeleteVerifier :exec
INSERT INTO post_delete_tokens(post_id, verifier, params_version, created_at)
VALUES (sqlc.arg(post_id), sqlc.arg(verifier), 1, sqlc.arg(created_at));

-- name: DeleteVerifier :one
SELECT verifier FROM post_delete_tokens WHERE post_id = sqlc.arg(post_id);

-- name: LoadIdempotency :one
SELECT request_hash, response, created_at
FROM idempotency_keys
WHERE operation = sqlc.arg(operation) AND key = sqlc.arg(key);

-- name: StoreIdempotency :exec
INSERT INTO idempotency_keys(operation, key, request_hash, response, created_at)
VALUES (sqlc.arg(operation), sqlc.arg(key), sqlc.arg(request_hash), sqlc.arg(response), sqlc.arg(created_at));

-- name: AppendOutbox :exec
INSERT INTO event_outbox(schema_version, event_type, board_id, thread_id, post_id, payload, created_at)
VALUES (sqlc.arg(schema_version), sqlc.arg(event_type), sqlc.narg(board_id), sqlc.narg(thread_id),
        sqlc.narg(post_id), sqlc.arg(payload), sqlc.arg(created_at));

-- name: CountOutbox :one
SELECT count(*) FROM event_outbox;
