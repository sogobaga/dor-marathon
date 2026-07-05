-- Migration 046: 跨來源（App GPS / Strava）重複活動去重——「偏好來源勝出」。
-- 玩家可能同時開 App「開始跑步」又戴 COROS→Strava，產生兩筆高度類似活動；賽事排名/完賽會重複計算。
-- 加 per-user 偏好來源（預設 GPS）＋「是否已提示過去重彈窗」旗標。實際去重由 worker 週期掃描 flag 非偏好那筆。
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS preferred_data_source TEXT NOT NULL DEFAULT 'gps'; -- gps | strava
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS dedup_prompted BOOLEAN NOT NULL DEFAULT FALSE;

INSERT INTO schema_migrations (version) VALUES ('046') ON CONFLICT DO NOTHING;
