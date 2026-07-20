-- Migration 092: 綠界正式特店切換硬化 + 退款流程
-- 依賴：091_partner_shops.sql

-- payment_transactions 補欄位：
--   ecpay_env / ecpay_merchant_id：這筆交易當下用的是正式還是測試特店（故障安全設計，見 payment.MultiConfig），
--     Notify 驗章、退款都要用同一組憑證，不可一律用全域設定。
--   ecpay_trade_no：綠界自己的交易編號（Notify 回傳的 TradeNo），退刷 API 必填，之前完全沒有存。
--   payment_type：綠界回傳的付款方式（如 Credit_CreditCard / ATM_LandBank / CVS_CVS...），退款要依此分流
--     （信用卡走 API 退刷，其餘走人工退款，API 不支援）。
--   trade_amt_cents：綠界回傳的實際交易金額（分），與 orders.total_cents 比對用，防金額竄改。
--   rtn_msg：綠界回傳文字說明，方便客服判斷失敗原因（原本只存 rtn_code）。
ALTER TABLE payment_transactions
    ADD COLUMN IF NOT EXISTS ecpay_env         VARCHAR(10) NOT NULL DEFAULT 'stage',
    ADD COLUMN IF NOT EXISTS ecpay_merchant_id VARCHAR(20),
    ADD COLUMN IF NOT EXISTS ecpay_trade_no    VARCHAR(30),
    ADD COLUMN IF NOT EXISTS payment_type      VARCHAR(20),
    ADD COLUMN IF NOT EXISTS trade_amt_cents   INT,
    ADD COLUMN IF NOT EXISTS rtn_msg           TEXT;

CREATE INDEX IF NOT EXISTS idx_paytx_ecpay_trade_no ON payment_transactions(ecpay_trade_no) WHERE ecpay_trade_no IS NOT NULL;

COMMENT ON COLUMN payment_transactions.status IS 'pending|paid|failed|refunded';

-- 退款紀錄
CREATE TABLE IF NOT EXISTS payment_refunds (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id    UUID NOT NULL REFERENCES payment_transactions(id) ON DELETE CASCADE,
    order_id          UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    amount_cents      INT NOT NULL,
    status            VARCHAR(20) NOT NULL DEFAULT 'pending', -- pending|success|failed|manual_required|manual_done
    method            VARCHAR(10) NOT NULL,                   -- api|manual
    reason            TEXT,
    operator_admin_id UUID REFERENCES users(id),
    ecpay_rtn_code    VARCHAR(10),
    ecpay_rtn_msg     TEXT,
    raw               JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_payref_order ON payment_refunds(order_id);
CREATE INDEX IF NOT EXISTS idx_payref_tx ON payment_refunds(transaction_id);

-- 冪等防線：同一筆交易同時間只能有一筆「未結案」的退款，擋下重複觸發（雙擊/併發請求）重複打退刷 API，
-- 或重複建立人工退款(manual_required)。涵蓋 pending（API 呼叫中）、manual_required（等人工匯款）、
-- unknown（呼叫綠界逾時/連線失敗，結果不明）——這三種都代表「額度可能已被佔用但還沒結案」，
-- 不能讓下一筆退款請求把它們當作不存在而重複建立。
CREATE UNIQUE INDEX IF NOT EXISTS uq_payref_pending_per_tx ON payment_refunds(transaction_id)
    WHERE status IN ('pending', 'manual_required', 'unknown');

INSERT INTO schema_migrations (version) VALUES ('092') ON CONFLICT DO NOTHING;
