-- Migration 024: 日常里程 EXP 改為「累積即發」+ 結算彈窗用的事件
-- 里程 EXP 屬於「日常」來源（每公里），不該綁在賽事結算。本 migration：
--   1) 加 users.exp_rewarded_km（已換算成 EXP 的整公里數）
--   2) 既有里程不追溯（視為已換算，避免一次補一大筆）
--   3) 移除舊的「賽事結算里程」EXP（從 exp 扣回 + 刪 ledger），改由 worker 日常發放
--   4) mileage_exp_events：每次跨整公里的發放紀錄（供前台彈窗，seen_at 為 NULL=未顯示）

ALTER TABLE users ADD COLUMN IF NOT EXISTS exp_rewarded_km INT NOT NULL DEFAULT 0;
UPDATE users SET exp_rewarded_km = floor(total_km)::int WHERE exp_rewarded_km = 0;

UPDATE users u SET exp = GREATEST(0, u.exp - COALESCE(
    (SELECT SUM(amount) FROM exp_ledger l WHERE l.user_id = u.id AND l.source = 'mileage'), 0));
DELETE FROM exp_ledger WHERE source = 'mileage';

CREATE TABLE IF NOT EXISTS mileage_exp_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    exp_amount  INT  NOT NULL,
    km_added    INT  NOT NULL,        -- 本次跨過的整公里數
    distance_km NUMERIC NOT NULL,     -- 觸發此次發放的活動距離（明細顯示）
    recorded_at TIMESTAMPTZ,
    seen_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_mileage_events_unseen ON mileage_exp_events(user_id, created_at) WHERE seen_at IS NULL;

INSERT INTO schema_migrations (version) VALUES ('024') ON CONFLICT DO NOTHING;
