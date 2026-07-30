-- 110_order_items_qty_check.sql
-- 稽核修正 SEC-C1（🔴 Critical）：加購數量整數溢位 → price_cents*qty 溢位成負數，
-- 讓 payable 被拉成 <50 分而被判定「免費完成付款」，繞過綠界金流。
-- 應用層（internal/race/service.go、repository.go）已補：
--   ① 加購單品項 qty 上限 1..999（checkAddonQty）
--   ② price_cents*qty 溢位安全乘法（safeAddonLineTotal，含除法防溢位檢查）
--   ③ payable += addonsTotal 之後再夾一次 >=0
-- 這裡在 DB 層對 order_items.qty 加 CHECK 做最後一道防線，避免未來新增的呼叫路徑
-- （後台工具、批次匯入等）繞過應用層驗證直接寫入異常數量。
--
-- 注意：若正式庫已存在 qty 超出範圍的舊資料（例如本漏洞曾被利用留下的髒資料），
-- 本 migration 會直接失敗；套用前請先 SELECT * FROM order_items WHERE qty<=0 OR qty>999
-- 確認乾淨，或先人工修正/清除異常列。

ALTER TABLE order_items
    ADD CONSTRAINT order_items_qty_range CHECK (qty > 0 AND qty <= 999);

INSERT INTO schema_migrations (version) VALUES ('110') ON CONFLICT DO NOTHING;
