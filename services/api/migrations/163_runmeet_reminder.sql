-- Migration 163: 團練開跑前提醒（站內信 + Email）
--
-- 需求（使用者原話）：「開跑前提醒也要，這是大家容易忘記的點，如果可以，除了站內信、也要加上 Email 通知。」
--
-- ⚠️ 這是本專案第一封「事件觸發的交易型 Email」。在此之前全站只有後台手動的行銷廣播
--    （internal/emailbroadcast 走 Resend，每封附退訂連結），沒有任何報名確認/完賽通知類的自動信。
--    因此以下三個決定是刻意的，改動前請先讀完理由：
--
-- ① 通知開關與行銷退訂**分開**（users.runmeet_reminder_email，本檔新增）
--    既有的 email_unsubscribes（migration 141）是「電子報退訂」。若沿用它，使用者退訂行銷信之後
--    連「你加入的團練 3 小時後開始」也收不到——那不是他退訂時的本意。交易型通知要有自己的開關。
--    ⚠️ 但反過來要成立：若使用者在 email_unsubscribes 裡，仍要尊重（他明確表達過不想收信），
--    實作時兩個條件都要檢查。
--
-- ② 冪等用 per-row 標記（run_meets.reminder_sent_at）而不是 app_settings 全域 key
--    全域 key 是給「這個排程今天跑過沒」用的；提醒是 per 團練、各自時間不同，必須逐筆標記。
--    發送前用 CAS：UPDATE ... SET reminder_sent_at=NOW() WHERE id=$1 AND reminder_sent_at IS NULL，
--    RowsAffected()=0 代表已發過或被其他實例搶走 → 直接跳過，不重複打擾。
--
-- ③ 提前時數做成設定（runmeet_reminder_hours，預設 3 小時）
--    寫死在程式碼裡的話，營運想調整就要重新部署。3 小時是「還來得及準備、又不會早到忘記」的折衷。
--
-- 索引：排程每小時掃「即將開跑且還沒提醒過」的團練，只掃未刪除/未下架/open 狀態的少數列。

ALTER TABLE run_meets
    ADD COLUMN IF NOT EXISTS reminder_sent_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_run_meets_reminder ON run_meets (meet_at)
    WHERE reminder_sent_at IS NULL AND deleted_at IS NULL AND hidden_by_admin = FALSE AND status = 'open';

-- 使用者層級的開關：預設收（加入團練本來就代表想參加），要關自己去個人資料關。
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS runmeet_reminder_email BOOLEAN NOT NULL DEFAULT TRUE;

INSERT INTO app_settings (key, value) VALUES
  ('runmeet_reminder_enabled', '1'),   -- 總開關（出事時可一鍵停掉，不必重新部署）
  ('runmeet_reminder_hours',  '3')     -- 開跑前幾小時發送
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('163') ON CONFLICT DO NOTHING;
