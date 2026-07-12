-- Migration 073: 體力值 SP 系統 + 跑步水準（fitness）
-- SP 上限依等級(24 + floor((lv-1)/5))；跑完扣 SP(距離×強度)；依水準恢復(30-120分/點)；扣到0凍結6小時。
ALTER TABLE users ADD COLUMN IF NOT EXISTS sp INT NOT NULL DEFAULT 24;                 -- 目前體力值
ALTER TABLE users ADD COLUMN IF NOT EXISTS sp_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(); -- 上次 SP 結算時間(懶惰恢復錨點)
ALTER TABLE users ADD COLUMN IF NOT EXISTS sp_freeze_until TIMESTAMPTZ;                -- 過度訓練→恢復凍結到此時間(NULL=無)
ALTER TABLE users ADD COLUMN IF NOT EXISTS fitness_score INT NOT NULL DEFAULT 0;       -- 跑步水準 0-100
ALTER TABLE users ADD COLUMN IF NOT EXISTS fitness_updated_at TIMESTAMPTZ;             -- 上次水準計算時間

INSERT INTO schema_migrations (version) VALUES ('073') ON CONFLICT DO NOTHING;
