-- Migration 031: DP 幣（DOR Point）— EXP 的平行貨幣，取得來源與 EXP 相同但費率獨立。
-- 採「擴充既有表」而非另開平行表：DP 與 EXP 在同一時機發放，共用結算/里程/事件流程。
-- DP 純為可消耗貨幣（未來商店/輪盤/道具），不參與等級。

-- 會員 DP 餘額
ALTER TABLE users ADD COLUMN IF NOT EXISTS dp INT NOT NULL DEFAULT 0;

-- DP 取得費率（與 EXP 同放 exp_rules 單例，獨立設定）
ALTER TABLE exp_rules
  ADD COLUMN IF NOT EXISTS dp_per_collective_task INT NOT NULL DEFAULT 30,
  ADD COLUMN IF NOT EXISTS dp_per_group_task      INT NOT NULL DEFAULT 15,
  ADD COLUMN IF NOT EXISTS dp_per_individual_task INT NOT NULL DEFAULT 5,
  ADD COLUMN IF NOT EXISTS dp_per_km              INT NOT NULL DEFAULT 1;

-- 各分組「完成賽事」可得 DP
ALTER TABLE race_groups ADD COLUMN IF NOT EXISTS dp_reward INT NOT NULL DEFAULT 0;

-- 結算分類帳同列記 DP（沿用 UNIQUE(user_id,race_id,source) 冪等）
ALTER TABLE exp_ledger ADD COLUMN IF NOT EXISTS dp_amount INT NOT NULL DEFAULT 0;

-- 日常里程事件同列記 DP（與 EXP 同一 km 跨越發放、共用 seen_at）
ALTER TABLE mileage_exp_events ADD COLUMN IF NOT EXISTS dp_amount INT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version) VALUES ('031') ON CONFLICT DO NOTHING;
