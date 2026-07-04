-- Migration 045: 即時「在跑」名單（心跳）。跑步中每 ~30 秒 upsert；後台總覽查近 2 分鐘內有心跳者。
CREATE TABLE IF NOT EXISTS live_tracking (
    user_id   UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_live_tracking_seen ON live_tracking (last_seen);

INSERT INTO schema_migrations (version) VALUES ('045') ON CONFLICT DO NOTHING;
