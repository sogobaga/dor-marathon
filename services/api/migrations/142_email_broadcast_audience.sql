-- Email 廣播：支援指定帳號/Email 對象（原本只能寄給全部玩家）。audience 記錄這次廣播的對象
-- 描述，後台歷史列表顯示用：'all'（全部玩家）或 'custom:N人'（指定 N 位會員）。
ALTER TABLE email_broadcasts ADD COLUMN IF NOT EXISTS audience TEXT NOT NULL DEFAULT 'all';  -- 'all' | 'custom:N人'
INSERT INTO schema_migrations (version) VALUES ('142') ON CONFLICT DO NOTHING;
