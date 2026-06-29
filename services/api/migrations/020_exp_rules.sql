-- Migration 020: EXP 取得規則改版
-- 依賴：019_athlete.sql
-- 來源三類：
--   1) 完成賽事 → 每分組各自設定（race_groups.exp_reward；42K 與 21K 不同）
--   2) 完成任務 → 依層級：全體(預設100)/分組(預設50)/個人(預設20)，單一任務目標完成可得
--   3) 日常里程 → 每 1 公里得 EXP（預設 1）

ALTER TABLE exp_rules
    ADD COLUMN IF NOT EXISTS per_collective_task INT NOT NULL DEFAULT 100,
    ADD COLUMN IF NOT EXISTS per_group_task      INT NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS per_individual_task INT NOT NULL DEFAULT 20,
    ADD COLUMN IF NOT EXISTS per_km              INT NOT NULL DEFAULT 1;

ALTER TABLE exp_rules DROP COLUMN IF EXISTS per_race;
ALTER TABLE exp_rules DROP COLUMN IF EXISTS per_task;

-- 完成賽事的 EXP 獎勵：各分組各自設定
ALTER TABLE race_groups ADD COLUMN IF NOT EXISTS exp_reward INT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version) VALUES ('020') ON CONFLICT DO NOTHING;
