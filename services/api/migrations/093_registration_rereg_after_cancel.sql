-- Migration 093: 允許退款/取消後重新報名同一場賽事
-- 依賴：092_ecpay_refund_hardening.sql
--
-- 背景：001_init.sql 的 registrations 有 UNIQUE(user_id, race_id)（不分狀態）。退款會把 registrations
-- 改成 status='cancelled'（見 race.Repository.MarkOrderRefunded）並釋放分組名額，但這條全域唯一約束
-- 仍然卡著同一組 (user_id, race_id)，導致玩家退款後永遠無法對同一場賽事重新報名（即使名額已釋放），
-- 只能請客服手動改 DB。
--
-- 改法：把「同一使用者對同一賽事永遠只能有一筆報名」放寬成「同一使用者對同一賽事同時間只能有一筆
-- 『未取消』的報名」——已取消的舊報名紀錄保留（供退款/客服對帳追蹤），但不再擋新報名。
ALTER TABLE registrations DROP CONSTRAINT IF EXISTS registrations_user_id_race_id_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_registrations_active_user_race
    ON registrations(user_id, race_id) WHERE status <> 'cancelled';

INSERT INTO schema_migrations (version) VALUES ('093') ON CONFLICT DO NOTHING;
