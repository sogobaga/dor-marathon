-- Migration 037: 後台手動觸發事件任務給指定帳號（測試用）。
-- 後台寫一筆 push；目標帳號「開始跑步」中的 /track 會輪詢認領（claim）→ 立即觸發該事件。
-- 未被認領的 push 3 分鐘後視為過期（代表當下對方沒在跑步）。
CREATE TABLE IF NOT EXISTS event_manual_pushes (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    def_id         UUID NOT NULL REFERENCES event_task_defs(id) ON DELETE CASCADE,
    target_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    consumed_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_manual_push_target ON event_manual_pushes(target_user_id, consumed_at, created_at DESC);

INSERT INTO schema_migrations (version) VALUES ('037') ON CONFLICT DO NOTHING;
