-- Migration 090: 回填 explore_progress.best_time_s
--
-- 背景：migration 075（2026-07-13 08:08）才把「挑戰完成時抓 gps_runs.duration_s 寫進 best_time_s」
-- 的邏輯加進 Complete handler。在那之前完成的挑戰，即使有對應的 GPS 跑步紀錄，best_time_s 仍為 NULL，
-- 導致他們在「挑戰者排名（比最短時間）」中拿不到時間（排名容錯後會顯示「—」，但看不到真正成績）。
--
-- 作法：用與 Complete handler 完全相同的啟發式回填——取該使用者「完成當下往前 20 分鐘內、結束時間最接近
-- 且不晚於 completed_at」的一筆「非 flagged、duration_s>0」gps_run 的 duration_s。gps_runs 沒有 boss 關聯
-- 欄位，Complete 本來就是靠這個時間鄰近性配對，故回填採同一規則、值與當時若有跑該段邏輯所寫入者一致。
-- 只回填 best_time_s IS NULL 的列（不覆蓋既有值）；EXISTS 守衛確保查無對應跑步時不會把 NULL 寫成 NULL。

UPDATE explore_progress p
SET best_time_s = (
    SELECT gr.duration_s
    FROM gps_runs gr
    WHERE gr.user_id = p.user_id
      AND gr.flagged = FALSE
      AND gr.duration_s > 0
      AND gr.ended_at <= p.completed_at
      AND gr.ended_at > p.completed_at - INTERVAL '20 minutes'
    ORDER BY gr.ended_at DESC
    LIMIT 1
)
WHERE p.best_time_s IS NULL
  AND p.completed_at IS NOT NULL
  AND p.stars > 0
  AND EXISTS (
    SELECT 1 FROM gps_runs gr
    WHERE gr.user_id = p.user_id
      AND gr.flagged = FALSE
      AND gr.duration_s > 0
      AND gr.ended_at <= p.completed_at
      AND gr.ended_at > p.completed_at - INTERVAL '20 minutes'
  );

INSERT INTO schema_migrations (version) VALUES ('090') ON CONFLICT DO NOTHING;
