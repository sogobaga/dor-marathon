-- Migration 131: 外部來源（Strava/Terra）里程獎勵發放帳本。
-- 依賴：014_integrations.sql（activities.source/external_id）
--
-- 背景（對抗式審查 CRITICAL-2）：撤權/中斷連接會硬刪 activities 裡對應 source 的活動列
-- （見 integration.Repository.DeleteProviderActivities）。原本的去重防線
-- （activities.exp_awarded 欄位、ON CONFLICT(source,external_id) 匯入去重）完全依附在活動列
-- 本身「還活著」——列被刪掉後，這層冪等保護就跟著消失。若使用者反覆「連接 → 累積歷史活動獲得
-- EXP/DP/total_km → 斷開連接(活動列被刪、獎勵已拍板不追回) → 重新連接 → backfill 重新匯入同一批
-- 歷史活動 → 全部被當成全新列 → 重新發放」，就能無限刷點數（連帶影響 referral 門檻/VIP/等級）。
--
-- 修法：獨立於 activities 之外、不會被 DeleteProviderActivities 觸及的帳本，只記「(user_id,
-- source, ext_hash) 這個外部活動是否已經被處理過發放邏輯」的存在性——ext_hash 為
-- sha256(source || ':' || external_id) 的十六進位字串，一次性、不可逆推回 Strava/Terra 的
-- external_id 本身，不保留任何可識別的外部活動內容，符合刪除義務精神。
CREATE TABLE IF NOT EXISTS external_award_ledger (
    user_id    UUID NOT NULL,
    source     TEXT NOT NULL,
    ext_hash   TEXT NOT NULL,
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, source, ext_hash)
);

INSERT INTO schema_migrations (version) VALUES ('131') ON CONFLICT DO NOTHING;
