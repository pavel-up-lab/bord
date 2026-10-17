ALTER TABLE request_subjects ADD COLUMN post_id INTEGER REFERENCES posts(id) ON DELETE SET NULL;
ALTER TABLE posts ADD COLUMN hidden_body TEXT;
ALTER TABLE posts ADD COLUMN hidden_attachment_id TEXT REFERENCES attachments(id) ON DELETE RESTRICT;

CREATE INDEX request_subjects_post_idx ON request_subjects(post_id);

CREATE TABLE used_challenges (
    id TEXT PRIMARY KEY,
    expires_at TEXT NOT NULL
);

CREATE INDEX used_challenges_expiry_idx ON used_challenges(expires_at);
