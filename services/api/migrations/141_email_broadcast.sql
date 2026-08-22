-- Email 廣播：退訂名單 + 廣播記錄（進度/結果稽核）。
CREATE TABLE IF NOT EXISTS email_unsubscribes (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS email_broadcasts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject     VARCHAR(200) NOT NULL,
    body_html   TEXT NOT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'sending',  -- sending | done | failed | partial
    total_count INT NOT NULL DEFAULT 0,
    sent_count  INT NOT NULL DEFAULT 0,
    fail_count  INT NOT NULL DEFAULT 0,
    error_note  TEXT NOT NULL DEFAULT '',
    created_by  UUID REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);
INSERT INTO schema_migrations (version) VALUES ('141') ON CONFLICT DO NOTHING;
