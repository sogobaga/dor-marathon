-- Migration 125: 個人挑戰模式（event_mode='personal'）P5 — 後台獎勵管理
-- 依賴：124_personal_challenge_mode.sql（registrations.status='completed' 為完成挑戰的終結狀態）
--
-- 獎勵＝「完成者中抽獎/限額」，LINE Point 由後台人工發放；系統只管「資格＋發放狀態」。
-- 每次完成＝一筆 status='completed' 的 registration；獎勵以「每一筆完成」為單位（同一人完成多次＝
-- 多筆完成＝多個抽獎資格，對應每次挑戰皆需付費 299 的經濟）。詳見設計 memory personal-challenge-mode，本檔為 P5。

ALTER TABLE registrations ADD COLUMN IF NOT EXISTS reward_status VARCHAR(20) NOT NULL DEFAULT '';
-- ''=完成但尚未處理/未中選, 'won'=中獎待發, 'fulfilled'=已發放
ALTER TABLE registrations ADD COLUMN IF NOT EXISTS reward_note TEXT;              -- 後台記 LINE Point 序號/備註
ALTER TABLE registrations ADD COLUMN IF NOT EXISTS reward_fulfilled_at TIMESTAMPTZ;

INSERT INTO schema_migrations (version) VALUES ('125') ON CONFLICT DO NOTHING;
