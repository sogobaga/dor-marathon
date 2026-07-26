-- Migration 098: 城市探索「打卡」改版 —— 可重複打卡（24h 冷卻）+ 每日上限 + 隨機 DP/GP 獎勵
-- 依賴：096_gp_wallet.sql（users.gp / wallet.AwardGP）
--
-- 背景：explore_progress 是 per-(user,boss) 終態表，算不出「今日跨點打卡次數」與「本點上次打卡時間」，
-- 需要一張逐次紀錄的新表。舊規則「打卡=揭露關主（一次性）」不變，本次只是在打卡這個動作上疊加「可重複
-- 拿獎勵」的新玩法：同一點過 24h 可再打卡拿 DP+GP（不會再次觸發挑戰面板，active 短路見程式邏輯）；
-- 純打卡點(checkin_only)本就沒有挑戰，重複打卡單純就是刷 DP+GP。

CREATE TABLE IF NOT EXISTS explore_checkins (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    boss_id       UUID NOT NULL REFERENCES explore_bosses(id) ON DELETE CASCADE,
    checked_in_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dp_awarded    INT NOT NULL DEFAULT 0,
    gp_awarded    INT NOT NULL DEFAULT 0
);
-- 每日次數(跨所有點加總，用台北日界)：(user_id, checked_in_at)
CREATE INDEX IF NOT EXISTS idx_explore_checkins_user_day ON explore_checkins(user_id, checked_in_at);
-- 同點冷卻(取最近一次)：(user_id, boss_id, checked_in_at DESC)
CREATE INDEX IF NOT EXISTS idx_explore_checkins_user_boss ON explore_checkins(user_id, boss_id, checked_in_at DESC);

-- 每點可設的打卡/完成獎勵區間（皆預設 0 = 沿用舊行為的「不發」，後台需個別設定才會發）
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS checkin_reward_dp_min INT NOT NULL DEFAULT 0;
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS checkin_reward_dp_max INT NOT NULL DEFAULT 0;
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS checkin_reward_gp_min INT NOT NULL DEFAULT 0;
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS checkin_reward_gp_max INT NOT NULL DEFAULT 0;
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS complete_reward_gp_min INT NOT NULL DEFAULT 0;
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS complete_reward_gp_max INT NOT NULL DEFAULT 0;
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS complete_reward_gp_chance INT NOT NULL DEFAULT 0; -- 0-100，完成挑戰時額外發 GP 的機率(%)

INSERT INTO schema_migrations (version) VALUES ('098') ON CONFLICT DO NOTHING;
