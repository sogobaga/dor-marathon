-- Migration 108: 活動里程 EXP/DP 發放冪等旗標
-- 依賴：052_mileage_reward.sql（exp_rules 里程獎勵欄位：per_km/dp_per_km/mileage_cap_km/mileage_min_pace_s）
--
-- 修 bug：Strava/Terra(Garmin/COROS) 匯入的活動完全沒有發里程 EXP/DP/total_km（只有 GPS worker 會發），
-- 且原本的 GPS worker 發放邏輯也沒有跨來源去重（同一趟若 GPS 與 Strava/Terra 都有記錄會被各自的來源
-- 各發一次）。本欄位標記「這筆活動是否已經處理過里程 EXP/DP 發放」，供三來源(GPS worker / Strava / Terra)
-- 共用的「去重感知、冪等」發放邏輯使用（因跨 Go module，兩邊各自實作一份，需保持行為一致：
-- 見 services/worker/main.go 的 Worker.awardMileageDedup、
-- services/api/internal/integration/mileage_exp.go 的 Repository.AwardMileageExp）。

ALTER TABLE activities ADD COLUMN IF NOT EXISTS exp_awarded BOOLEAN NOT NULL DEFAULT false;

-- 回填：既有活動全部視為已結算，此修正只往後生效，避免對歷史活動突然補發或誤判去重。
UPDATE activities SET exp_awarded = true;

-- 供「同使用者、時間重疊、已發放」查詢加速（去重判斷用的部分索引）
CREATE INDEX IF NOT EXISTS idx_activities_user_awarded_rec ON activities(user_id, recorded_at) WHERE exp_awarded = true;

INSERT INTO schema_migrations (version) VALUES ('108') ON CONFLICT DO NOTHING;
