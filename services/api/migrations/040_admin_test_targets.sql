-- Migration 040: 後台管理者專屬的「測試觸發」常用帳號名單（每個管理者各自一份，非全站共用）。
-- 用於事件任務「測試觸發」快速選取目標帳號，可加入、移除、設定預設值。
CREATE TABLE IF NOT EXISTS admin_test_targets (
    admin_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email         TEXT NOT NULL,
    is_default    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (admin_user_id, email)
);

INSERT INTO schema_migrations (version) VALUES ('040') ON CONFLICT DO NOTHING;
