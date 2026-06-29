-- Migration 012: 會員「開放建立跑團分組」權限
-- 依賴：011_team_groups.sql
-- 前台「建立跑團分組」僅開放給有此權限的會員；後台會員管理可切換。

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS can_create_team_group BOOLEAN NOT NULL DEFAULT FALSE;

INSERT INTO schema_migrations (version) VALUES ('012') ON CONFLICT DO NOTHING;
