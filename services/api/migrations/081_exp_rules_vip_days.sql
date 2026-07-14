-- Migration 081: 活動任務完成獎勵新增「VIP 天數」（全體/分組/個人 三種目標，與 EXP/DP 並排）
-- 對應 /admin/levels 的「完成全體/分組/個人任務」獎勵；賽事結算 settlement.go 達標時與 EXP/DP 一起發放。
ALTER TABLE exp_rules ADD COLUMN IF NOT EXISTS vip_days_collective_task  INT NOT NULL DEFAULT 0;
ALTER TABLE exp_rules ADD COLUMN IF NOT EXISTS vip_days_group_task       INT NOT NULL DEFAULT 0;
ALTER TABLE exp_rules ADD COLUMN IF NOT EXISTS vip_days_individual_task  INT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version) VALUES ('081') ON CONFLICT DO NOTHING;
