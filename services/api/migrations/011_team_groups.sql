-- Migration 011: 跑團分組申請 + 跑團鑰匙
-- 依賴：010_images.sql
-- 1) races.allow_team_groups：競賽模式是否開放前台跑團成員自建分組
-- 2) race_groups.requires_key / group_key：該分組是否需要「跑團鑰匙」才能加入
-- 3) race_groups.created_by：NULL=官方建立；有值=前台使用者自建

ALTER TABLE races
    ADD COLUMN IF NOT EXISTS allow_team_groups BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE race_groups
    ADD COLUMN IF NOT EXISTS requires_key BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS group_key    VARCHAR(100),
    ADD COLUMN IF NOT EXISTS created_by   UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_race_groups_created_by ON race_groups(created_by);

INSERT INTO schema_migrations (version) VALUES ('011') ON CONFLICT DO NOTHING;
