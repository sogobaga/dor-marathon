-- 109_activities_pending_award_index.sql
-- 效能修正（稽核 PERF-H3）：worker.reconcileMileageExp 每 ~5s（有新活動時）查
--   SELECT ... FROM activities WHERE exp_awarded=false AND created_at BETWEEN ... ORDER BY created_at
-- 但 108 的 partial index idx_activities_user_awarded_rec 是 WHERE exp_awarded=true（相反條件），
-- 對這條查詢無用，隨 activities 成長會退化成全表掃描。補一支對應的 partial index：
-- 只索引「尚未發放」的列（正常極少且短命），故此索引很小、掃描很快。
CREATE INDEX IF NOT EXISTS idx_activities_pending_award
    ON activities (created_at)
    WHERE exp_awarded = false;
