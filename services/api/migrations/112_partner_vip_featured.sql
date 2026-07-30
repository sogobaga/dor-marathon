-- Migration 112: 跑者充電站——VIP 精選分類
-- 依賴：091_partner_shops.sql（partner_shops）
--
-- audience 區分商家可見對象：
--   all          全部會員（預設；既有商家資料一律落在這裡，行為不變）
--   vip_featured VIP 精選——僅 VIP 身分 + 累積里程達門檻（app_settings.partner_vip_featured_min_km）
--                的使用者可見，見 internal/partner service.go vipFeaturedEligibility。
ALTER TABLE partner_shops ADD COLUMN IF NOT EXISTS audience VARCHAR(16) NOT NULL DEFAULT 'all';

-- DB 層守住合法值，防止繞過應用層驗證（internal/partner/service.go normalizeAndValidate）直接寫入髒資料。
ALTER TABLE partner_shops
    ADD CONSTRAINT partner_shops_audience_check CHECK (audience IN ('all', 'vip_featured'));

-- VIP 精選解鎖門檻（累積里程 km；後台可調，見 internal/partner Repository.MinVIPFeaturedKm/
-- SetMinVIPFeaturedKm）。刻意不登記進 internal/appsettings 的 specs 白名單——比照
-- monopoly_dup_gp/monopoly_redeem_fallback_gp 的既有慣例，這把由 partner 模組自己的後台端點
-- （GET/PUT /admin/partner-shops/vip-featured-min-km，perm partners）管理，不透過通用系統設定頁面。
INSERT INTO app_settings (key, value) VALUES
  ('partner_vip_featured_min_km', '10')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('112') ON CONFLICT DO NOTHING;
