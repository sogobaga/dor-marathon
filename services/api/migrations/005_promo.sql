-- Migration 005: 優惠序號系統
-- 依賴：004_registration.sql

CREATE TABLE IF NOT EXISTS promo_codes (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code           VARCHAR(40) UNIQUE NOT NULL,                 -- 大寫英數
    discount_type  VARCHAR(10) NOT NULL,                        -- amount | percent
    discount_value INT NOT NULL,                                -- amount=分；percent=1..100
    max_uses       INT,                                         -- 總可用次數上限（NULL=不限）
    used_count     INT NOT NULL DEFAULT 0,
    per_user_once  BOOLEAN NOT NULL DEFAULT TRUE,               -- 同一帳號只能用一次
    race_id        UUID REFERENCES races(id) ON DELETE CASCADE, -- NULL=全賽事
    target_user_id UUID REFERENCES users(id),                   -- NULL=不限帳號
    valid_from     TIMESTAMPTZ,
    valid_until    TIMESTAMPTZ,
    batch_id       UUID,                                        -- 批次生成識別
    note           TEXT,
    active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_promo_codes_race ON promo_codes(race_id);
CREATE INDEX IF NOT EXISTS idx_promo_codes_batch ON promo_codes(batch_id);

CREATE TABLE IF NOT EXISTS promo_code_usages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    promo_code_id   UUID NOT NULL REFERENCES promo_codes(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id),
    race_id         UUID REFERENCES races(id),
    registration_id UUID REFERENCES registrations(id),
    order_id        UUID REFERENCES orders(id),
    discount_cents  INT NOT NULL,
    used_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_promo_usages_code ON promo_code_usages(promo_code_id);
CREATE INDEX IF NOT EXISTS idx_promo_usages_user ON promo_code_usages(user_id);

INSERT INTO schema_migrations (version) VALUES ('005') ON CONFLICT DO NOTHING;
