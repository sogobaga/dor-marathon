-- Migration 078: 城市探索「純打卡點」旗標
-- checkin_only=TRUE 的點：到範圍內打卡即完成、不揭露關主、不觸發挑戰（其餘關主欄位留空即可）。
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS checkin_only BOOLEAN NOT NULL DEFAULT FALSE;

INSERT INTO schema_migrations (version) VALUES ('078') ON CONFLICT DO NOTHING;
