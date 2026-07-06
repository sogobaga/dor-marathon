-- Migration 051: 個人任務「旅程起點」——自動里程結算的 Day-1 時間窗錨點
-- 依賴：050_personal_tasks.sql
-- 每位玩家的個人任務是一條全域鏈（10 計畫 × 每 100 天，依 stage_order, day 串接）。
-- 自動結算某任務時，計入的活動 = 「上一個任務完成後」到現在、且來源符合 data_source 的里程。
-- 第一個任務（尚無任何完成）沒有「上一個完成時間」，故以此表的 started_at 當窗口起點：
--   → 玩家第一次開啟個人任務頁時寫入 NOW()，避免用很久以前的舊活動回溯完成 Day 1。

CREATE TABLE IF NOT EXISTS personal_journey (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('051') ON CONFLICT DO NOTHING;
