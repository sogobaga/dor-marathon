-- Migration 007: 賽事狀態雙層化（手動控制 control_status + 每場倒數天數 + 測試白名單）
-- 依賴：006_payment.sql

ALTER TABLE races
    ADD COLUMN IF NOT EXISTS control_status     VARCHAR(20) NOT NULL DEFAULT 'active',
        -- active 正常運作中 | paused 暫停報名 | suspended 賽事中止 | closed 賽事關閉 | hidden 賽事隱藏 | testing 賽事測試中
    ADD COLUMN IF NOT EXISTS starting_soon_days INT NOT NULL DEFAULT 5;
        -- 「賽事即將開始」倒數門檻（賽事前 N 天）

-- 該賽事專屬測試白名單（control_status=testing 時，只有白名單 email 能在前台看到）
CREATE TABLE IF NOT EXISTS race_test_whitelist (
    race_id UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    email   VARCHAR(255) NOT NULL,
    PRIMARY KEY (race_id, email)
);

-- 全域預設測試白名單（套用到所有 testing 賽事）
CREATE TABLE IF NOT EXISTS default_test_whitelist (
    email      VARCHAR(255) PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('007') ON CONFLICT DO NOTHING;
