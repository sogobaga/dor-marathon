-- 121_partner_shop_slug.sql
-- 跑者充電站商家：加自訂 slug，讓專屬連結可用 /shop/{slug} 取代 /shop/{uuid}
-- （比照活動 /event/{slug} 那套，見 internal/race repository.go GetBySlug）。
-- nullable；UNIQUE 允許多個 NULL，所以沒設 slug 的商家存 NULL 不會互相衝突。
ALTER TABLE partner_shops ADD COLUMN IF NOT EXISTS slug VARCHAR(64) UNIQUE;

INSERT INTO schema_migrations (version) VALUES ('121') ON CONFLICT DO NOTHING;
