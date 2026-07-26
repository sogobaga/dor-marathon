-- Migration 097: 完成任務 GP 獎勵（環台大富翁 Phase 0a）
-- 依賴：096_gp_wallet.sql（GP 貨幣地基：users.gp + internal/wallet.AwardGP/SpendGP）
--
-- 與既有 EXP/DP/VIP 天數「完成任務」獎勵並排：取得來源同為任務三種 scope（全體/分組/個人），
-- 比照 081（VIP 天數）的範圍——里程日常發放、「完成賽事」分組獎勵（race_groups.exp_reward/
-- dp_reward）皆不發 GP，僅任務達標才發。
ALTER TABLE exp_rules ADD COLUMN IF NOT EXISTS gp_per_collective_task INT NOT NULL DEFAULT 0;
ALTER TABLE exp_rules ADD COLUMN IF NOT EXISTS gp_per_group_task      INT NOT NULL DEFAULT 0;
ALTER TABLE exp_rules ADD COLUMN IF NOT EXISTS gp_per_individual_task INT NOT NULL DEFAULT 0;

-- 結算分類帳同列記 GP，沿用 UNIQUE(user_id,race_id,source) 冪等：GP 與 EXP/DP 走同一筆
-- INSERT ... ON CONFLICT DO NOTHING，同一 source 只會成功插入一次，重跑結算不會重複發放 GP
-- （不可能發生「EXP 冪等但 GP 重發」）。
ALTER TABLE exp_ledger ADD COLUMN IF NOT EXISTS gp_amount INT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version) VALUES ('097') ON CONFLICT DO NOTHING;
