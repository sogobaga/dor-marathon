-- 活動數據來源合規 gate：每場賽事可設定是否採用 Strava 等外部數據做排名/里程統計。
-- 預設 FALSE（只認 App 內 GPS 跑步追蹤，符合 Strava 使用規範）；既有賽事自動得 FALSE。
ALTER TABLE races ADD COLUMN IF NOT EXISTS external_data BOOLEAN NOT NULL DEFAULT FALSE;
INSERT INTO schema_migrations (version) VALUES ('128') ON CONFLICT DO NOTHING;
