-- Migration 137: VIP 訂閱制 Phase D——每日續約排程的扣款嘗試紀錄。
-- 依賴：059_vip_subscription.sql（vip_subscriptions）、132_ecpay_bind_card.sql（orders.race_id
-- nullable／payment_card_bindings）、133_vip_billing_guards.sql（uq_vip_subs_active）。
--
-- 冪等（同訂閱同一天最多一次嘗試，見 uq_vip_renewal_daily）+ 重試計數（attempt_no）+ 稽核。
CREATE TABLE IF NOT EXISTS vip_renewal_attempts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id UUID NOT NULL REFERENCES vip_subscriptions(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    order_id        UUID REFERENCES orders(id),
    attempt_no      INT NOT NULL,                          -- 1..3
    status          VARCHAR(16) NOT NULL,                  -- processing | paid | failed | threeds_required
    rtn_code        VARCHAR(16) NOT NULL DEFAULT '',
    rtn_msg         TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_vip_renewal_daily
    ON vip_renewal_attempts (subscription_id, (created_at::date));
CREATE INDEX IF NOT EXISTS idx_vra_sub ON vip_renewal_attempts(subscription_id, created_at DESC);
INSERT INTO schema_migrations (version) VALUES ('137') ON CONFLICT DO NOTHING;
