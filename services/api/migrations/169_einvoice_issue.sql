-- Migration 169: 綠界 B2C 電子發票開立/折讓/作廢（internal/einvoice）
-- 依賴：094_order_invoices.sql（order_invoices 建表）、092_ecpay_refund_hardening.sql（payment_refunds）
--
-- 背景：094 只收集買受人資訊，不觸發實際開立（invoice_number/invoice_status/issued_at/invoice_raw
-- 是那時預留的欄位）。本次接上綠界 B2C 電子發票 API，訂單付款成功後非同步自動開立、退款成功後
-- 開立折讓、後台可手動開立/作廢/查詢同步——需要更完整的狀態機（見 internal/einvoice/issuer.go）：
-- pending → issuing(CAS) → issued | failed(attempts/last_error/next_attempt_at) | skipped | void。
--
-- ⚠️ 本 migration 只寫檔，未套用到任何資料庫。部署順序：先套 migration 再推程式（owner 手動套用）。

-- 1. invoice_status CHECK 擴充：094 原本只有 pending|issued|void|failed，補上 issuing（CAS 進行中的
--    中繼態）與 skipped（依規則判定不需要開立，如零元/虛擬會員/測試金流/自動開立未開）。
--    CHECK 約束無法直接 ALTER，須 DROP 舊的（Postgres 對匿名 CHECK 的預設命名規則＝
--    "<table>_<column>_check"）再補新的。
ALTER TABLE order_invoices DROP CONSTRAINT IF EXISTS order_invoices_invoice_status_check;
ALTER TABLE order_invoices ADD CONSTRAINT order_invoices_invoice_status_check
    CHECK (invoice_status IN ('pending','issuing','issued','void','failed','skipped'));

-- 2. 開立/重試/作廢/折讓所需的欄位。皆用 NOT NULL DEFAULT ''（比照本表既有欄位風格，避免額外處理
--    NULL），時間/數字類允許 NULL（語意是「尚未有值」，用 NULL 比空字串/0 更明確）。
ALTER TABLE order_invoices
    ADD COLUMN IF NOT EXISTS relate_number         VARCHAR(50)  NOT NULL DEFAULT '',  -- 'DOR'+訂單id去連字號，Issue/GetIssue 用
    ADD COLUMN IF NOT EXISTS invoice_date           DATE,                              -- 開立日期（供 Invalid/Allowance 的 InvoiceDate 帶入）
    ADD COLUMN IF NOT EXISTS invoice_datetime       TIMESTAMPTZ,                       -- 開立完整時刻（顯示用）
    ADD COLUMN IF NOT EXISTS random_number          VARCHAR(4)   NOT NULL DEFAULT '',  -- 發票防偽隨機碼
    ADD COLUMN IF NOT EXISTS ecpay_env              VARCHAR(8)   NOT NULL DEFAULT '',  -- 開立當下用的環境 stage|prod
    ADD COLUMN IF NOT EXISTS merchant_id            VARCHAR(10)  NOT NULL DEFAULT '',  -- 開立當下用的發票特店代號
    ADD COLUMN IF NOT EXISTS attempts               INT          NOT NULL DEFAULT 0,   -- 已嘗試開立次數（含失敗）
    ADD COLUMN IF NOT EXISTS last_error             TEXT         NOT NULL DEFAULT '',  -- 最近一次錯誤訊息
    ADD COLUMN IF NOT EXISTS next_attempt_at        TIMESTAMPTZ,                       -- 下次重試時間（NULL＝不再重試）
    ADD COLUMN IF NOT EXISTS skip_reason            VARCHAR(40)  NOT NULL DEFAULT '',  -- 略過原因 slug（見 issuer.go decideSkip）
    ADD COLUMN IF NOT EXISTS voided_at              TIMESTAMPTZ,                       -- 作廢時刻
    ADD COLUMN IF NOT EXISTS void_reason            VARCHAR(20)  NOT NULL DEFAULT '',  -- 作廢原因（ECPay Reason 上限 20 字）
    ADD COLUMN IF NOT EXISTS remain_allowance_ntd   INT,                               -- 剩餘可折讓金額（NTD，GetIssue 同步回填）
    ADD COLUMN IF NOT EXISTS sales_amount_ntd       INT          NOT NULL DEFAULT 0;   -- 開立金額快照（NTD，供後台列表顯示不必回頭查 orders）

-- 3. 索引：待處理/失敗中的列是 SweepPending 熱查詢（見 internal/einvoice/repository.go DuePending），
--    partial index 只索引這兩種狀態，避免 issued/void/skipped（絕大多數列的終態）拖累索引大小。
CREATE INDEX IF NOT EXISTS idx_order_invoices_pending
    ON order_invoices (invoice_status) WHERE invoice_status IN ('pending','failed');

-- relate_number 全域唯一（跨特店/跨環境亦然——一個訂單只會對應一組 RelateNumber，見
-- mapping.go RelateNumberFor），空字串（尚未進入開立流程的懶建立列）不受此限制。
CREATE UNIQUE INDEX IF NOT EXISTS uq_order_invoices_relate_number
    ON order_invoices (relate_number) WHERE relate_number <> '';

-- 4. 折讓紀錄（退款 → 開立折讓，見 PRODUCT DECISIONS #3）。refund_id 唯一（部分索引，允許多筆
-- NULL——手動由後台觸發、不掛某筆退款的折讓）：同一筆退款只能對應一筆折讓，防止重複觸發時
-- 重複打折讓 API（見 internal/einvoice/repository.go InsertAllowancePending 的冪等保證）。
CREATE TABLE IF NOT EXISTS order_invoice_allowances (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_invoice_id  UUID NOT NULL REFERENCES order_invoices(id) ON DELETE CASCADE,
    refund_id         UUID REFERENCES payment_refunds(id) ON DELETE SET NULL,
    amount_ntd        INT  NOT NULL,
    reason            TEXT,
    status            VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','success','failed')),
    allowance_no      VARCHAR(16) NOT NULL DEFAULT '',   -- ECPay IA_Allow_No
    allowance_date    TIMESTAMPTZ,
    last_error        TEXT NOT NULL DEFAULT '',
    raw               JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_order_invoice_allowances_invoice ON order_invoice_allowances(order_invoice_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_order_invoice_allowances_refund
    ON order_invoice_allowances (refund_id) WHERE refund_id IS NOT NULL;

-- 5. VIP 訂單（orders.race_id IS NULL）目前完全沒有 order_invoices 列（094 只在報名流程插入）。
-- 懶建立：買受人一律先當 personal 空白（雲端發票存證），日後若使用者有填寫發票資訊的入口
-- 再由該入口另行 UPDATE 覆寫（本次不涉及該入口）。ON CONFLICT DO NOTHING：報名流程建立的既有列
-- 一律保留，不覆寫使用者已填寫的買受人資訊。
INSERT INTO order_invoices (order_id, buyer_type)
SELECT o.id, 'personal' FROM orders o
WHERE o.race_id IS NULL
ON CONFLICT (order_id) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('169') ON CONFLICT DO NOTHING;
