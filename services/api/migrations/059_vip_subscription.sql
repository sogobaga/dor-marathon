-- VIP 訂閱制基礎：14 天試用、訂閱方案(月/年)、活動優惠券、VIP 限定賽事、可設定促銷檔期。
-- 金流(綠界定期定額)、報名折抵、後台分析於後續 migration/程式接續。

-- 1) users：訂閱 / 試用 / 活動優惠券欄位（vip_expires_at 已存在於 017_membership）
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS vip_plan                VARCHAR(10) NOT NULL DEFAULT '',   -- ''=無 / trial / monthly / annual
    ADD COLUMN IF NOT EXISTS vip_since               TIMESTAMPTZ,                       -- 首次成為 VIP（含試用）起始
    ADD COLUMN IF NOT EXISTS trial_notice_shown      BOOLEAN NOT NULL DEFAULT FALSE,    -- 試用到期彈窗是否已顯示過（只跳一次）
    ADD COLUMN IF NOT EXISTS activity_coupon_balance INT NOT NULL DEFAULT 0,            -- 活動優惠券($100)剩餘張數
    ADD COLUMN IF NOT EXISTS activity_coupon_month   VARCHAR(7) NOT NULL DEFAULT '';    -- 最後補券月份 YYYY-MM（每月補齊 3 張）

-- 2) races：VIP 限定（預設關閉）
ALTER TABLE races
    ADD COLUMN IF NOT EXISTS vip_only BOOLEAN NOT NULL DEFAULT FALSE;

-- 3) 訂閱紀錄：綠界定期定額每期資訊；供分析月繳/年繳、續訂/未續訂
CREATE TABLE IF NOT EXISTS vip_subscriptions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan               VARCHAR(10) NOT NULL,                    -- monthly | annual
    amount_cents       INT NOT NULL,                            -- 每期金額（分）
    status             VARCHAR(12) NOT NULL DEFAULT 'active',   -- active | cancelled | expired | failed
    started_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    current_period_end TIMESTAMPTZ,                             -- 目前週期到期（＝users.vip_expires_at）
    exec_times         INT NOT NULL DEFAULT 0,                  -- 已成功扣款期數
    provider           VARCHAR(20) NOT NULL DEFAULT 'ecpay',
    merchant_member_id VARCHAR(40),                             -- 綠界定期定額交易識別（MerchantTradeNo/MemberID）
    gwsr               VARCHAR(30),                             -- 綠界授權/交易單號
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_vip_subs_user   ON vip_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_vip_subs_status ON vip_subscriptions(status);

-- 4) 可設定的訂閱促銷檔期（首次試用到期促銷之外，後台可再開檔期）
CREATE TABLE IF NOT EXISTS vip_promos (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(60) NOT NULL,
    plan       VARCHAR(10) NOT NULL DEFAULT 'both',  -- monthly | annual | both
    pay_pct    INT NOT NULL,                         -- 實付百分比（70=付七成、即打七折；1..100）
    starts_at  TIMESTAMPTZ,
    ends_at    TIMESTAMPTZ,
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_vip_promos_active ON vip_promos(active);
