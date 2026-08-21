-- Migration 138: 活動優惠券——券種管理（event_coupon_defs）+ 活動獎勵新獎項(coupon) + 報名折抵三選一
-- 設計見 memory activity-reward-system 之活動優惠券擴充。event_coupon_defs：後台券種設定（名稱/面額/
-- 使用期限，兩種期限模式擇一：fixed 指定到期日｜days 獲得後 N 天內可用）。user_rewards（migration 127，
-- 原本只存序號類獎勵）擴充成序號/優惠券共用：新增 kind 區分（既有資料一律回填 'serial' 保持相容）、
-- coupon_def_id/amount_cents denormalize 面額與所屬券種、used_order_id 記錄核銷於哪筆訂單
-- （供 race.RegisterWithOrder 報名折抵 CAS 核銷稽核、SettleCancellation 取消退費時回填 used=FALSE）。

CREATE TABLE IF NOT EXISTS event_coupon_defs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(100) NOT NULL,
    amount_cents INT NOT NULL,
    expiry_mode  VARCHAR(8) NOT NULL DEFAULT 'days',   -- fixed(指定到期日) | days(獲得後N天)
    expires_at   TIMESTAMPTZ,                          -- fixed 用
    valid_days   INT,                                  -- days 用
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- user_rewards 擴充：kind 區分序號類(既有,'serial')／優惠券類(新增,'coupon')；coupon_def_id/amount_cents
-- 為 coupon 類專用（denormalize，即使券種事後被改名/改面額也不影響已發出的既有券）；used_order_id 為
-- coupon 類核銷時記錄用於哪筆訂單（稽核＋取消退費回填 used=FALSE 的依據，見 race.SettleCancellation）。
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS kind VARCHAR(16) NOT NULL DEFAULT 'serial'; -- serial | coupon
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS coupon_def_id UUID REFERENCES event_coupon_defs(id);
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS amount_cents INT;          -- 券面額 denormalize（coupon 類專用）
ALTER TABLE user_rewards ADD COLUMN IF NOT EXISTS used_order_id UUID REFERENCES orders(id);  -- 用於哪筆訂單(稽核)

INSERT INTO schema_migrations (version) VALUES ('138') ON CONFLICT DO NOTHING;
