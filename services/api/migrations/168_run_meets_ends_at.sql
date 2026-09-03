-- Migration 168: run_meets 加結束時間（ends_at）
-- 依賴：156_run_meets.sql（建表，meet_at 欄位）
-- 需求背景（2026-09-04 owner 決策）：團練邀請目前只有開始時間（meet_at），使用者反映不知道
-- 該團練預計辦到幾點。新增 ends_at，規則：
--   1) 必須與 meet_at 同一個台北日曆日（Asia/Taipei，UTC+8）——團練是單日活動，
--      不支援跨日（例如夜跑跨過午夜），跨日需求另開單處理。
--   2) 必須晚於 meet_at（不得等於或早於）。
-- ⚠️ ends_at 刻意「只做資訊顯示 + 建立/編輯時驗證」，不牽動任何既有以 meet_at 為準的規則——
--    is_ended（活動是否已結束）、可編輯時間窗、提醒排程（reminder.go）、瀏覽排序（ListMeets）、
--    每月配額，全部維持只讀 meet_at，行為與 migration 168 之前完全一致。之後如需「活動實際
--    進行到 ends_at 才算結束」等語意，需另開 migration/owner 決策，本次不動。
-- ⚠️ 舊資料 ends_at 一律 NULL（無法回溯）；DTO 對外一律 nullable（json "ends_at": string|null）。
--    NULL 不觸發 CHECK（ends_at IS NULL OR ends_at > meet_at 允許 NULL 通過）。
-- ⚠️ 本 migration 只寫檔，未套用到任何資料庫。部署順序：先套 migration 再推程式
--    （owner 手動套用至 Neon）。
ALTER TABLE run_meets ADD COLUMN IF NOT EXISTS ends_at TIMESTAMPTZ NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'run_meets_ends_after_start'
    ) THEN
        ALTER TABLE run_meets
            ADD CONSTRAINT run_meets_ends_after_start CHECK (ends_at IS NULL OR ends_at > meet_at);
    END IF;
END $$;

INSERT INTO schema_migrations (version) VALUES ('168') ON CONFLICT DO NOTHING;
