-- Migration 095: 取消報名（使用者申請 → 後台審核 → 核准後才執行）
-- 依賴：094_order_invoices.sql
--
-- 產品設計（勿更動語意）：
--   - 流程：使用者線上申請 → 後台審核 → 核准後才執行（不是申請就直接生效）。
--   - 退費依「距賽事天數」分級：系統有一組預設政策，個別賽事可在 races.config.cancellation_policy
--     覆寫（見 race.RaceConfig.CancellationPolicy／race.ResolveCancellationPolicy）。
--   - 費率鎖在「申請當下」：申請時把 days_before_race / refund_ratio / refund_amount_cents /
--     order_total_cents 快照下來，審核時直接用快照，不重算（審核作業時間不該由使用者承擔）。

CREATE TABLE IF NOT EXISTS registration_cancel_requests (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    registration_id     UUID NOT NULL REFERENCES registrations(id) ON DELETE CASCADE,
    order_id            UUID REFERENCES orders(id), -- 可為 NULL：未付款/無訂單的報名也能申請取消
    user_id             UUID NOT NULL REFERENCES users(id),

    -- processing：核准流程的 CAS 前置鎖（見 race.beginCancelRequestProcessing）；正常情況極短暫，
    -- 只有核准流程中途失敗才會停留在這個狀態，代表需要人工介入，藉此保證同一筆申請不會被重試而
    -- 重複建立退款（退款是真金流，退兩次＝真的退兩次錢）。
    status              VARCHAR(16) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','approved','rejected')),
    reason              TEXT NOT NULL DEFAULT '', -- 使用者填寫的取消原因

    -- 申請當下的快照（審核時直接用，不重算）
    days_before_race    INT,
    refund_ratio        INT, -- 百分比 0–100
    refund_amount_cents INT,
    order_total_cents   INT,

    -- 後台審核
    reviewed_by         UUID REFERENCES users(id),
    reviewed_at         TIMESTAMPTZ,
    review_note         TEXT NOT NULL DEFAULT '',

    -- 核准且實際建立退款後回填（未建立退款、或退 0 元則維持 NULL）
    refund_id           UUID REFERENCES payment_refunds(id),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cancel_requests_registration ON registration_cancel_requests(registration_id);
CREATE INDEX IF NOT EXISTS idx_cancel_requests_user ON registration_cancel_requests(user_id);
CREATE INDEX IF NOT EXISTS idx_cancel_requests_status ON registration_cancel_requests(status);

-- 同一筆報名同時只能有一筆待審／處理中申請（processing 也要擋，否則核准流程卡在 processing 期間
-- 使用者仍能對同一筆報名再申請一次取消，形成兩筆申請最終各自嘗試退款的隱性重複退款路徑）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_cancel_requests_pending_per_registration
    ON registration_cancel_requests(registration_id) WHERE status IN ('pending','processing');

-- promo_code_usages 補作廢欄位：取消核准後要回補 promo_codes.used_count，同時把這筆使用紀錄標記作廢，
-- 否則 per_user_once 序號的使用者取消報名後永遠無法再用同一張序號
-- （見 promo.LockAndValidateTx 的 per-user 判斷，已同步加上 AND voided_at IS NULL）。
ALTER TABLE promo_code_usages ADD COLUMN IF NOT EXISTS voided_at TIMESTAMPTZ; -- NULL=有效

-- orders 補是否使用 VIP 活動優惠券（$100）的旗標：報名時原本只在「當場 0 元完成」的情況把 payment_ref
-- 存成 'COUPON'，一旦報名費扣完券後仍有加購金額要走金流（最常見的情況——券面額 $100 通常不足以覆蓋整筆
-- 報名費），payment_ref 就不會是 'COUPON'，取消核准時就無從得知要不要回補 activity_coupon_balance。
-- 改成不論訂單當下是否已付清，一律在下單當時就把「這筆訂單是否用了活動券」明確記下來。
ALTER TABLE orders ADD COLUMN IF NOT EXISTS coupon_used BOOLEAN NOT NULL DEFAULT false;

-- 回填既有訂單的 coupon_used：新欄位對既有資料一律預設 false，若不回填，這些舊訂單日後被核准取消時
-- activity_coupon_balance 不會回補，使用者的 VIP 活動券會憑空消失且沒有告警。
--
-- 可辨識條件：在本次 migration 之前，程式（race.RegisterWithOrder）只有在「這筆訂單當場用券折抵到
-- 0 元、不必走金流」時才會把 payment_ref 存成常數 'COUPON'——這是既有資料裡唯一能可靠回推「這筆訂單
-- 用了 VIP 活動券」的欄位，沒有其他佐證（沒有獨立的券使用紀錄表，users.activity_coupon_balance/
-- activity_coupon_month 只是滾動計數器，看不出是哪一筆訂單扣的）。因此只回填 payment_ref='COUPON' 的訂單。
--
-- 涵蓋範圍與限制：無法涵蓋「用券折抵報名費、但加購金額仍需另外走金流付款」的舊訂單——這種情況下
-- payment_ref 當時被金流回傳值蓋掉，不會是 'COUPON'，且既有資料完全沒有其他欄位可以回推是否用了券，
-- 因此這部分【刻意不回填】。這類舊訂單如果日後被核准取消，activity_coupon_balance 不會被回補，
-- 需要客服依人工紀錄逐筆核對；不是為了「盡量回補」而用不可靠的條件亂猜。
UPDATE orders SET coupon_used = true WHERE payment_ref = 'COUPON' AND NOT coupon_used;

-- orders.registration_id 加 partial unique index：把「一筆報名最多一筆訂單」從程式邏輯保證升級成
-- DB 層保證（race.Repository.SettleCancellation 用 QueryRow 假設單筆，若未來新增了製造重複訂單的路徑，
-- 這裡會直接擋下，而不是讓 SettleCancellation 悄悄只處理其中一筆）。
--
-- 資料安全性：目前唯一會 INSERT INTO orders 的路徑是 race.Repository.RegisterWithOrder，且該函式在
-- 同一交易內先 INSERT 一筆全新的 registrations 再用其剛產生的 id INSERT 對應的 orders 列，每筆
-- registration 只會經過這個路徑一次，因此結構上不可能產生兩筆 orders 指向同一 registration_id，
-- 既有資料不會違反此限制。此處用一般 CREATE UNIQUE INDEX（非 CONCURRENTLY）——本專案的
-- migration runner（cmd/migrate/main.go）本來就把每個檔案包在單一 transaction 內執行，
-- CONCURRENTLY 在 transaction block 中會直接報錯，無法使用。
CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_registration_id
    ON orders(registration_id) WHERE registration_id IS NOT NULL;

-- 系統預設取消政策：截止＝賽事開始前 14 天；距賽事 ≥30 天退 90%、≥14 天退 50%。
-- 個別賽事可在 races.config.cancellation_policy 覆寫；沒有覆寫就繼承這裡的值；
-- 這裡也查無資料（例如整列被刪除）則退回程式內建預設（同樣的數值，見 race.defaultCancellationPolicy）。
INSERT INTO app_settings (key, value) VALUES
    ('cancellation_policy', '{"deadline_days":14,"tiers":[{"days_before":30,"ratio":90},{"days_before":14,"ratio":50}]}')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('095') ON CONFLICT DO NOTHING;
