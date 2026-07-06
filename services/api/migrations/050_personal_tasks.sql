-- 050: 個人任務系統（跑者生命週期 10 計畫 × 每 100 天鏈式任務；階段化解鎖、故事包裝、星星評分）
-- 有別於①活動內任務②跑步隨機事件任務，此為第三套：個人化、每日一個、完成前一個才開下一個。

CREATE TABLE IF NOT EXISTS personal_plans (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         TEXT NOT NULL UNIQUE,             -- P01..P10
  name         TEXT NOT NULL DEFAULT '',
  lifecycle    TEXT NOT NULL DEFAULT '',          -- 生命周期層級
  stage_order  INT  NOT NULL DEFAULT 0,           -- 1..10（計畫先後）
  target_km    NUMERIC NOT NULL DEFAULT 0,
  target_time  TEXT NOT NULL DEFAULT '',
  entry_note   TEXT NOT NULL DEFAULT '',          -- 進入門檻摘要
  data_source  TEXT NOT NULL DEFAULT 'gps',       -- gps|strava|both（新手 GPS、跑者 Strava）
  banner_url   TEXT NOT NULL DEFAULT '',
  enabled      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS personal_tasks (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  plan_id           UUID NOT NULL REFERENCES personal_plans(id) ON DELETE CASCADE,
  day               INT  NOT NULL DEFAULT 0,       -- 1..100
  week              INT  NOT NULL DEFAULT 0,
  seq               INT  NOT NULL DEFAULT 0,       -- 計畫內排序（= day，供鏈式）
  prereq_task_id    UUID REFERENCES personal_tasks(id) ON DELETE SET NULL, -- 前置任務；NULL=計畫起點
  title             TEXT NOT NULL DEFAULT '',      -- DOR任務文案（標題）
  story             TEXT NOT NULL DEFAULT '',      -- 故事包裝內文（先留空）
  workout           TEXT NOT NULL DEFAULT '',      -- 訓練菜單
  workout_type      TEXT NOT NULL DEFAULT '',      -- 課表類型
  target_km         NUMERIC NOT NULL DEFAULT 0,
  target_min        INT  NOT NULL DEFAULT 0,
  intensity         TEXT NOT NULL DEFAULT '',      -- 強度 RPE
  complete_cond     TEXT NOT NULL DEFAULT '',      -- 完成條件（文字，給玩家看）
  completion_type   TEXT NOT NULL DEFAULT 'manual',-- manual|distance|pace|...（先 manual）
  completion_params JSONB NOT NULL DEFAULT '{}'::jsonb,
  reward_exp        INT  NOT NULL DEFAULT 0,
  reward_dp         INT  NOT NULL DEFAULT 0,
  reward_extra      JSONB NOT NULL DEFAULT '{}'::jsonb, -- 未來擴充其他獎勵
  icon_url          TEXT NOT NULL DEFAULT '',      -- 任務圖示 1200x400
  data_source       TEXT NOT NULL DEFAULT '',      -- 覆寫用（空=用 plan 的）
  safety_note       TEXT NOT NULL DEFAULT '',      -- 安全退階規則
  enabled           BOOLEAN NOT NULL DEFAULT TRUE,
  UNIQUE (plan_id, day)
);
CREATE INDEX IF NOT EXISTS idx_personal_tasks_plan_seq ON personal_tasks(plan_id, seq);

CREATE TABLE IF NOT EXISTS personal_task_progress (
  user_id      UUID NOT NULL,
  task_id      UUID NOT NULL REFERENCES personal_tasks(id) ON DELETE CASCADE,
  status       TEXT NOT NULL DEFAULT 'completed', -- 進度只記已完成；available 由前後端依鏈計算
  stars        INT  NOT NULL DEFAULT 0,           -- 1..3
  evidence     JSONB NOT NULL DEFAULT '{}'::jsonb,-- 手動回報：實際里程/疼痛/RPE…
  awarded      BOOLEAN NOT NULL DEFAULT FALSE,    -- 發獎冪等
  completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_id, task_id)
);
CREATE INDEX IF NOT EXISTS idx_personal_progress_user ON personal_task_progress(user_id);
