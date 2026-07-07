-- Migration 053: 個人任務改「挑戰制」——挑戰→(進行中/放棄)→達標才可完成；可重複挑戰爬星(1→3★)、難度遞增；重挑扣 DP
-- 依賴：050_personal_tasks.sql
-- 星級目標倍率（後端常數 tierMult）：1★×1.00、2★×1.15、3★×1.30；休息日「不能有里程」窗口預設 10 分。
-- 獎勵：每爬一顆「新星」再發一次任務基準 EXP/DP（awarded_stars 冪等）。

-- 每任務的「重新挑戰」DP 花費（第一次挑戰免費；未來每任務可各自設定）
ALTER TABLE personal_tasks
  ADD COLUMN IF NOT EXISTS retry_dp_cost INT NOT NULL DEFAULT 10;

-- 進度表加「進行中挑戰」狀態欄位
ALTER TABLE personal_task_progress
  ADD COLUMN IF NOT EXISTS attempts             INT NOT NULL DEFAULT 0,     -- 已開始挑戰次數（>0 → 下次挑戰要付 DP）
  ADD COLUMN IF NOT EXISTS active               BOOLEAN NOT NULL DEFAULT FALSE, -- 是否有進行中的挑戰
  ADD COLUMN IF NOT EXISTS challenge_tier        INT NOT NULL DEFAULT 0,     -- 進行中挑戰的星級 1..3
  ADD COLUMN IF NOT EXISTS challenge_target_km   NUMERIC NOT NULL DEFAULT 0, -- 進行中挑戰的縮放目標（快照）
  ADD COLUMN IF NOT EXISTS challenge_started_at  TIMESTAMPTZ,               -- 挑戰起算時間（里程/休息窗口皆從此起算）
  ADD COLUMN IF NOT EXISTS awarded_stars         INT NOT NULL DEFAULT 0;    -- 已發過獎的最高星（多階段冪等）

-- 舊資料相容：既有 completed 進度視為「已完成、已挑戰過一次、已發獎至該星」
UPDATE personal_task_progress
   SET awarded_stars = GREATEST(awarded_stars, stars),
       attempts      = GREATEST(attempts, 1)
 WHERE stars > 0;

CREATE INDEX IF NOT EXISTS idx_personal_progress_active ON personal_task_progress(user_id) WHERE active;

INSERT INTO schema_migrations (version) VALUES ('053') ON CONFLICT DO NOTHING;
