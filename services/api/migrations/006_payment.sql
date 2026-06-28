-- Migration 006: 金流交易紀錄（綠界 ECPay）
-- 依賴：005_promo.sql

CREATE TABLE IF NOT EXISTS payment_transactions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id          UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    provider          VARCHAR(20) NOT NULL DEFAULT 'ecpay',
    merchant_trade_no VARCHAR(30) UNIQUE NOT NULL,            -- 送綠界的交易編號（每次結帳新建）
    amount_cents      INT NOT NULL,
    status            VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending|paid|failed
    rtn_code          VARCHAR(10),
    raw               JSONB,                                  -- 綠界回傳原始參數（稽核）
    paid_at           TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_paytx_order ON payment_transactions(order_id);

INSERT INTO schema_migrations (version) VALUES ('006') ON CONFLICT DO NOTHING;
