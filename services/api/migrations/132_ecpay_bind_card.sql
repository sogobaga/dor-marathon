-- Migration 132: 綠界站內付2.0 綁卡地基。
-- 依賴：001_init.sql（users, orders）
--
-- payment_card_bindings：儲存 BindCardID 與顯示用卡片資訊（卡號全程留在綠界端，我方永不存卡號本身，
-- 只存綠界回傳的 BindCardID 供之後幕後請款帶入，以及末四碼/卡別/到期年月等顯示用資訊）。
CREATE TABLE IF NOT EXISTS payment_card_bindings (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider           VARCHAR(20) NOT NULL DEFAULT 'ecpay',
    merchant_member_id VARCHAR(60) NOT NULL,        -- 我方送給綠界的會員識別（供綁卡/請款帶入）
    bind_card_id       VARCHAR(64) NOT NULL,         -- 綠界回傳的 BindCardID（之後幕後請款用）
    card_type          VARCHAR(20),                  -- 卡別（顯示用）
    card_last4         VARCHAR(4),                   -- 末四碼（顯示用）
    card_expiry_mm     VARCHAR(2),                   -- 到期月（顯示用）
    card_expiry_yy     VARCHAR(4),                   -- 到期年（顯示用）
    status             VARCHAR(12) NOT NULL DEFAULT 'active',  -- active | deleted | disabled(綠界停用)
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 每人每 provider 至多一張 active 綁卡
CREATE UNIQUE INDEX IF NOT EXISTS uq_card_binding_active
    ON payment_card_bindings (user_id, provider) WHERE status='active';

-- VIP 訂閱為獨立訂單（無賽事），orders.race_id 改為可空（既有訂單皆有值、不受影響；
-- FK REFERENCES races ON DELETE CASCADE 維持不動，只拿掉 NOT NULL）。
ALTER TABLE orders ALTER COLUMN race_id DROP NOT NULL;

INSERT INTO schema_migrations (version) VALUES ('132') ON CONFLICT DO NOTHING;
