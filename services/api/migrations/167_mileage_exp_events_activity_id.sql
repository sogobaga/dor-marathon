-- Migration 167: mileage_exp_events 加 activity_id（異常活動回收機制的必要前提）
-- 依賴：024_daily_mileage_exp.sql（建表）、031_dp_currency.sql（dp_amount）
-- 事故背景（2026-09-03 owner 決策）：已入帳（exp_awarded=TRUE）的異常活動被發現後，系統目前完全
-- 沒有反向路徑——total_km/exp/dp 一旦發放，AdminRejectGPS 也只能動「flagged AND reviewed_at IS
-- NULL」的待審列，對已核發完畢的活動無能為力。mileage_exp_events 是「已核發了多少」的唯一分類帳，
-- 但目前只能靠 (user_id, recorded_at) 這種弱關聯回頭找是哪一筆活動核發的——同一使用者同一秒可能
-- 有一筆以上的紀錄（例如 recorded_at 相同但不同 race_id 的資料傾斜狀況），弱關聯無法保證精準對應。
-- 加 activity_id 後，新的回收端點（internal/activity/gps_recall.go）才能：
--   1) 精準查出某活動核發過多少 exp/dp/km_added（SUM WHERE activity_id=$1）
--   2) 用「activity_id=$1 AND exp_amount<=0 AND km_added<=0 AND distance_km<0」的負值列，
--      冪等判斷「這筆活動是否已經回收過」，避免重複扣款
-- 舊資料的 activity_id 一律 NULL（無法回溯配對，回收端點對這批舊列 fallback 回 (user_id,
-- recorded_at) 弱關聯，見 gps_recall.go RecallGPSRun 註解）；此後 worker（services/worker/main.go）
-- 與 API 端（internal/integration/mileage_exp.go）的每一筆 INSERT 都會帶上 activity_id。
ALTER TABLE mileage_exp_events ADD COLUMN IF NOT EXISTS activity_id UUID NULL;
CREATE INDEX IF NOT EXISTS idx_mileage_exp_events_activity ON mileage_exp_events(activity_id) WHERE activity_id IS NOT NULL;

INSERT INTO schema_migrations (version) VALUES ('167') ON CONFLICT DO NOTHING;
