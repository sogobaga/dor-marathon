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
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempt_date    DATE NOT NULL DEFAULT (timezone('Asia/Taipei', now()))::date -- 當日冪等閘門用（台北日界線）
);
-- 冪等補欄：首次修正套用時，表可能已在先前失敗的嘗試中建立（無 attempt_date 欄），補上再建索引。
ALTER TABLE vip_renewal_attempts ADD COLUMN IF NOT EXISTS
    attempt_date DATE NOT NULL DEFAULT (timezone('Asia/Taipei', now()))::date;

-- ⚠️ 2026-09-01 修正：原寫法 (created_at::date) 依賴 session 時區屬 STABLE，PostgreSQL 禁止用於
-- 索引運算式（42P17），本檔因此**從未成功套用過**（漏網直到全庫盤點才發現）。改為實體欄位
-- attempt_date（插入當下以台北時區取日，DEFAULT 不受 IMMUTABLE 限制）＋普通複合唯一索引；
-- Go 端（internal/payment/vip_renewal.go）只依 isUniqueViolation 判斷、未指名衝突目標，語意不變，
-- 且日界線改為明確的台北時區（原寫法跟著 DB 時區走，反而含糊）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_vip_renewal_daily
    ON vip_renewal_attempts (subscription_id, attempt_date);
CREATE INDEX IF NOT EXISTS idx_vra_sub ON vip_renewal_attempts(subscription_id, created_at DESC);
INSERT INTO schema_migrations (version) VALUES ('137') ON CONFLICT DO NOTHING;
