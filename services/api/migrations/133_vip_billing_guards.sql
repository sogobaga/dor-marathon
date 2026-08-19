-- Migration 133: VIP 訂閱金流防線（Phase C2 對抗式審查修正）。
-- 依賴：132_ecpay_bind_card.sql（orders.race_id nullable）、059_vip_subscription.sql（vip_subscriptions）
--
-- 兩道 DB 層唯一性防線，堵「連按兩次訂閱 → 兩筆 pending 訂單各自完成綁卡 → 重複扣款/重複延長 VIP/
-- 多列 active 訂閱」的併發窗（應用層檢查 hasActive 無鎖、擋不住並發，必須靠 DB 約束當最終防線）：
--
-- 1) 每人至多一筆「pending 的 VIP 訂單」（race_id IS NULL 目前唯一的語義就是 VIP 訂閱訂單，
--    見 race.Repository.CreateVipOrder；日後若有其他無賽事訂單型別，屆時需改用 order_items.item_type
--    判別並調整本索引）。Subscribe 會先作廢使用者既有 pending VIP 訂單再建新單；兩個並發 Subscribe
--    同時建單時，後到的 INSERT 撞本索引失敗 → API 回 409，不會產生第二筆可結算的訂單。
CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_pending_vip
    ON orders (user_id) WHERE status='pending' AND race_id IS NULL;

-- 2) 每人至多一筆 active 訂閱（最後防線：即使前面所有防護都被繞過，第二筆結算在 INSERT
--    vip_subscriptions 時撞本索引 → 整包 rollback，訂單留 pending 供人工對帳，不會出現雙 active）。
--    截至本 migration，vip_subscriptions 尚無任何資料列（P4 金流本次才上線），直接建索引安全；
--    防守性起見仍先把萬一存在的同人多筆 active 依 started_at 只留最新一筆：
UPDATE vip_subscriptions v SET status='expired', updated_at=NOW()
WHERE v.status='active'
  AND EXISTS (
    SELECT 1 FROM vip_subscriptions n
    WHERE n.user_id = v.user_id AND n.status='active'
      AND (n.started_at > v.started_at OR (n.started_at = v.started_at AND n.id > v.id))
  );
CREATE UNIQUE INDEX IF NOT EXISTS uq_vip_subs_active
    ON vip_subscriptions (user_id) WHERE status='active';

INSERT INTO schema_migrations (version) VALUES ('133') ON CONFLICT DO NOTHING;
