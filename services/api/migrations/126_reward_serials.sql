-- Migration 126: 活動獎勵系統 P1 — 序號庫存系統（後台）
-- 設計見 memory activity-reward-system。合作商家 → 活動序號組（面額/期限/使用次數/配發數/對應活動）→ 序號（全系統唯一）。
-- P1 只做序號庫存管理；即時獎勵 roll(P2)、玩家錢包(P3)、到期提醒(P4) 於後續 migration 接續。

CREATE TABLE IF NOT EXISTS reward_merchants (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name       VARCHAR(120) NOT NULL,
  note       TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS reward_serial_groups (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  merchant_id       UUID REFERENCES reward_merchants(id),
  name              VARCHAR(160) NOT NULL,
  item_label        VARCHAR(120),                          -- 面額/品項（如「100元」「咖啡兌換」）
  is_line_point     BOOLEAN NOT NULL DEFAULT FALSE,
  valid_until       TIMESTAMPTZ,                            -- 使用期限（null=無期限）
  use_limit_type    VARCHAR(12) NOT NULL DEFAULT 'single',  -- single|repeat|unlimited
  use_limit_count   INT,                                    -- repeat 時的次數
  grant_count       INT NOT NULL DEFAULT 1,                 -- 每次中獎配發幾枚序號
  applies_all_races BOOLEAN NOT NULL DEFAULT FALSE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS reward_serial_group_races (  -- 指定活動（可複選）；applies_all_races=false 時用
  group_id UUID NOT NULL REFERENCES reward_serial_groups(id) ON DELETE CASCADE,
  race_id  UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
  PRIMARY KEY (group_id, race_id)
);

CREATE TABLE IF NOT EXISTS reward_serials (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id   UUID NOT NULL REFERENCES reward_serial_groups(id) ON DELETE CASCADE,
  code       VARCHAR(200) NOT NULL,
  link       TEXT,
  status     VARCHAR(12) NOT NULL DEFAULT 'available',  -- available(未發送)|issued(已發送)|void(註銷)
  used       BOOLEAN NOT NULL DEFAULT FALSE,
  used_at    TIMESTAMPTZ,
  issued_to  UUID,
  issued_at  TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_reward_serials_code ON reward_serials(code);  -- 全系統唯一
CREATE INDEX IF NOT EXISTS idx_reward_serials_group ON reward_serials(group_id, status);

INSERT INTO schema_migrations (version) VALUES ('126') ON CONFLICT DO NOTHING;
