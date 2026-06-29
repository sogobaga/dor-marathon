-- Migration 018: 追蹤系統（社交基礎，供報名推薦/好友擴充）
-- 依賴：017_membership.sql

CREATE TABLE IF NOT EXISTS follows (
    follower_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followee_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);
CREATE INDEX IF NOT EXISTS idx_follows_followee ON follows(followee_id);

INSERT INTO schema_migrations (version) VALUES ('018') ON CONFLICT DO NOTHING;
