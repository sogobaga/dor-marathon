-- 111_payment_tx_pending_unique.sql
-- 稽核修正 SEC-M6 續補：Checkout 的 SupersedePendingTx + CreateTx 原本各自 autocommit、
-- 無外層交易、無對 order 上鎖，DB 也沒有唯一約束，仍存在 TOCTOU 競態——併發兩次 Checkout
-- （雙擊/兩分頁，都在既有 10/min 限流內）可能交錯成：
--   A supersede(無事) → B supersede(無事) → A insert tx1(pending) → B insert tx2(pending)
-- 兩筆 pending 各自都能被 MarkTxPaid 的 CAS（WHERE status IN ('pending','failed')）標成 paid，
-- 若剛好兩筆都真的完成付款，就是真實雙扣款。
--
-- 應用層已改用 SELECT ... FOR UPDATE 鎖住 order 列、把「作廢舊 pending + 建立新 pending」包進
-- 單一交易序列化（見 internal/payment/payment.go Repository.CheckoutCreateTx）。
-- 這裡加部分唯一索引作為 DB 層最後一道防線：保證任何時刻同一訂單最多只有一筆 status='pending'
-- 的交易，即使應用層的鎖未來失效（改連線池、新增繞過本方法的寫入路徑等）也無法真的插入第二筆
-- pending——INSERT 會直接違反唯一約束而失敗，而不是靜默留下兩筆。
--
-- 清理既有髒資料（本漏洞曾被觸發留下的多筆 pending，否則唯一索引建不起來 23505）：
-- 同一訂單多筆 pending 時，保留「最新一筆」（created_at 最大，id 為次要 tiebreaker），其餘標 superseded。
-- 這些都是未付款的重複結帳嘗試（paid 者狀態不會是 pending），標 superseded 安全，且與應用層新流程一致。
UPDATE payment_transactions t
SET status = 'superseded'
WHERE t.status = 'pending'
  AND EXISTS (
    SELECT 1 FROM payment_transactions t2
    WHERE t2.order_id = t.order_id
      AND t2.status = 'pending'
      AND (t2.created_at > t.created_at
           OR (t2.created_at = t.created_at AND t2.id > t.id))
  );

CREATE UNIQUE INDEX IF NOT EXISTS uq_paytx_pending_per_order
    ON payment_transactions(order_id)
    WHERE status = 'pending';

INSERT INTO schema_migrations (version) VALUES ('111') ON CONFLICT DO NOTHING;
