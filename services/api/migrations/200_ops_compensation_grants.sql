-- Migration 200: 營運補償發放帳本（一次性補償的冪等紀錄）。
-- 用途：例如 2026-10「手錶數據同步方式調整」對受影響會員補償 VIP 天數。每筆＝某次補償活動（campaign）發給某位會員；
-- PK (campaign, user_id) 保證同一活動同一人只發一次——發放腳本以 INSERT … ON CONFLICT DO NOTHING RETURNING
-- 取得「這次新發的人」，只對這些人延長 VIP，所以重複執行不會重複發放。
-- 程式目前沒有讀寫這張表（發放由擁有者在 Neon 執行一次性腳本），套用順序不受程式版本限制。全部冪等。
CREATE TABLE IF NOT EXISTS ops_compensation_grants (
    campaign    VARCHAR(40) NOT NULL,                                   -- 例：'2026-10-wearable'
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        VARCHAR(16) NOT NULL,                                   -- 'vip_days'
    amount      INT         NOT NULL CHECK (amount > 0),                -- vip_days＝天數
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign, user_id),
    CONSTRAINT chk_ops_compensation_kind CHECK (kind IN ('vip_days'))
);

INSERT INTO schema_migrations (version) VALUES ('200') ON CONFLICT DO NOTHING;
