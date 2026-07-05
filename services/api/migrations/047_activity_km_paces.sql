-- Migration 047: activities 每公里分段配速。供「平均配速區間」個人任務改用「任一公里落在區間即算」，
-- 比整段均配速好達成（整段均配速含暖身/緩和/紅綠燈，門檻過高）。GPS 追蹤才有；Strava/手動為空。
ALTER TABLE activities ADD COLUMN IF NOT EXISTS km_paces INT[]; -- 每公里配速(秒/km)

INSERT INTO schema_migrations (version) VALUES ('047') ON CONFLICT DO NOTHING;
