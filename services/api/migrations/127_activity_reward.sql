-- Migration 127: 活動獎勵系統 P2 — 即時獎勵設定（全域模板 + 每場挑戰 config）+ 完成觸發機率 roll
-- 設計見 memory activity-reward-system。P1(序號庫存, migration 126) 之後接續：
--   - reward_templates：全域即時獎勵模板，建立/編輯挑戰賽事時可套用再微調。
--   - races.reward_config：每場賽事的即時獎勵設定（僅 personal 模式使用，比照 challenge_rule）。
--   - user_rewards：只存序號類獎勵（經濟類 EXP/DP/GP/VIP 中獎直接入帳，不進此表）。
--   - reward_serial_groups 補三個「獎勵詳情」顯示欄位（使用說明/圖示/活動說明），供 user_rewards 配發時 denormalize。
-- 即時獎勵 roll 邏輯見 internal/activityreward；玩家活動獎勵錢包頁(P3)、首頁到期提醒(P4) 於後續 migration 接續。

CREATE TABLE IF NOT EXISTS reward_templates (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       VARCHAR(160) NOT NULL,
  items      JSONB NOT NULL DEFAULT '[]',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE races ADD COLUMN IF NOT EXISTS reward_config JSONB;  -- 每場即時獎勵設定（僅 personal 模式使用）

CREATE TABLE IF NOT EXISTS user_rewards (   -- 只存序號類獎勵（經濟類直接入帳不進此表）
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        UUID NOT NULL REFERENCES users(id),
  source_type    VARCHAR(24) NOT NULL,          -- personal_challenge | personal_task
  source_race_id UUID,
  source_reg_id  UUID,
  serial_id      UUID REFERENCES reward_serials(id),
  group_id       UUID REFERENCES reward_serial_groups(id),
  code           VARCHAR(200),
  link           TEXT,
  item_label     VARCHAR(120),
  merchant_name  VARCHAR(120),
  usage_note     TEXT,
  icon_url       TEXT,
  description    TEXT,
  valid_until    TIMESTAMPTZ,
  used           BOOLEAN NOT NULL DEFAULT FALSE,
  used_at        TIMESTAMPTZ,
  obtained_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_rewards_user ON user_rewards(user_id, obtained_at DESC);

ALTER TABLE reward_serial_groups ADD COLUMN IF NOT EXISTS usage_note TEXT;   -- 獎勵詳情：使用說明
ALTER TABLE reward_serial_groups ADD COLUMN IF NOT EXISTS icon_url TEXT;     -- 獎勵圖示
ALTER TABLE reward_serial_groups ADD COLUMN IF NOT EXISTS description TEXT; -- 活動/獎勵說明

INSERT INTO schema_migrations (version) VALUES ('127') ON CONFLICT DO NOTHING;
