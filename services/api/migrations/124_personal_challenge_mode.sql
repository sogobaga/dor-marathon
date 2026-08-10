-- Migration 124: 個人挑戰模式（event_mode='personal'）P1 資料模型
-- 依賴：093_registration_rereg_after_cancel.sql（uq_registrations_active_user_race）
--
-- 個人挑戰模式：每次挑戰＝一筆報名(attempt)，可重複報名再挑戰；規則存 races.challenge_rule。
-- 詳見 memory personal-challenge-mode（第13套「個人挑戰模式」）分階段計畫，本檔為 P1（資料＋後台）。

-- 每次挑戰的次數/起訖時間戳（P2 報名/判定引擎使用；P1 先建欄位）
ALTER TABLE registrations ADD COLUMN IF NOT EXISTS attempt_no INT NOT NULL DEFAULT 1;
ALTER TABLE registrations ADD COLUMN IF NOT EXISTS challenge_started_at TIMESTAMPTZ;
ALTER TABLE registrations ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

-- 唯一約束放寬：093 版是「未取消」擋再報名（WHERE status <> 'cancelled'）。個人挑戰模式需要
-- completed/expired 也釋放才能再報名再挑戰；改成「只有進行中(pending/paid)」擋再報名。
-- 對非 personal 賽事等價於原本的 <>'cancelled'——它們的 registrations 不會出現 completed/expired 狀態，
-- 差異只在 personal 賽事新增的兩個終結狀態不再被這條唯一索引卡住。
DROP INDEX IF EXISTS uq_registrations_active_user_race;
CREATE UNIQUE INDEX uq_registrations_active_user_race ON registrations(user_id, race_id)
    WHERE status NOT IN ('cancelled','completed','expired');

-- 個人挑戰規則（僅 personal 模式使用）：{completion_type, days, min_km_per_day, window_days, cum_km, single_km}
ALTER TABLE races ADD COLUMN IF NOT EXISTS challenge_rule JSONB;

-- 完成/排行榜查詢用（P4 用；先建索引）
CREATE INDEX IF NOT EXISTS idx_registrations_race_completed ON registrations(race_id, user_id) WHERE status='completed';

INSERT INTO schema_migrations (version) VALUES ('124') ON CONFLICT DO NOTHING;
