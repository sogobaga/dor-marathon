-- Migration 094: 電子發票資料收集（只收集儲存，不串接綠界發票 API）
-- 依賴：001_init.sql（orders, user_profiles）
--
-- 背景：即將切綠界正式特店開始收報名費，會有報名者需要三聯式發票報帳。發票開出去後不能改統編
-- （只能作廢重開），所以要在結帳當下（報名 API 的同一個 DB 交易）把發票資料收齊。本次只落地資料
-- 模型與驗證，不觸發實際開立；invoice_number/invoice_status/issued_at/invoice_raw 是預留給日後
-- 串接綠界發票 API 時使用的欄位，現在一律不寫入，一次設計進去之後串接不用再開 migration。

CREATE TABLE IF NOT EXISTS order_invoices (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id      UUID NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
  buyer_type    VARCHAR(16) NOT NULL CHECK (buyer_type IN ('personal','company','donation')),
  tax_id        VARCHAR(8) NOT NULL DEFAULT '',   -- 統編（company 專用）
  title         VARCHAR(120) NOT NULL DEFAULT '', -- 發票抬頭（company 專用）
  carrier_type  VARCHAR(16) NOT NULL DEFAULT '',  -- ''(雲端發票存證) | mobile（personal 專用）
  carrier_id    VARCHAR(64) NOT NULL DEFAULT '',  -- 載具號碼
  love_code     VARCHAR(7) NOT NULL DEFAULT '',   -- 愛心碼（donation 專用）

  -- 預留給日後實際開立（現在不寫入）
  invoice_number VARCHAR(20) NOT NULL DEFAULT '',
  invoice_status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (invoice_status IN ('pending','issued','void','failed')),
  issued_at      TIMESTAMPTZ,
  invoice_raw    JSONB,

  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- user_profiles 發票預填欄位（供「記住上次填的」）。
-- 注意：語意與 real_name 等既有欄位不同——這幾欄每次報名都會被覆寫成最新填的值（使用者可能換統編／
-- 換載具），不套用既有欄位「只補空欄位」的 COALESCE(既有, 新值) 模式。詳見 race.Repository.RegisterWithOrder。
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS inv_buyer_type  VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS inv_tax_id      VARCHAR(8)  NOT NULL DEFAULT '';
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS inv_title       VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS inv_carrier_type VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS inv_carrier_id  VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS inv_love_code   VARCHAR(7)  NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('094') ON CONFLICT DO NOTHING;
