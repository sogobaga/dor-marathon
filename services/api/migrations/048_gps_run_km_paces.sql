-- Migration 048: gps_runs 也存每公里分段配速，供跑步歷史詳情頁「回看每公里配速」。
ALTER TABLE gps_runs ADD COLUMN IF NOT EXISTS km_paces INT[]; -- 每公里配速(秒/km)

INSERT INTO schema_migrations (version) VALUES ('048') ON CONFLICT DO NOTHING;
