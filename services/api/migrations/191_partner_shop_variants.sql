-- Migration 191: partner_shops 多品項（variants）
-- 依賴：091_partner_shops.sql（建表）。docs/partner/VARIANTS_CONTRACT.md 為單一真相。
-- 需求背景（2026-09-21）：跑者充電站商家設定原本只能對單一品項設連結。擴充：商家可設
-- item_mode='single'（維持現狀）或 'multi'（底部渲染多個細項商品：圖片/名稱/描述/連結，
-- 可排序），資料放 variants JSONB（陣列順序＝顯示順序），比照 photo_urls/content_images
-- 慣例不另開表。
-- ⚠️ 本 migration 只寫檔，未套用到任何資料庫。部署順序：先套 migration 再推程式
--    （owner 手動套用至 Neon）。

ALTER TABLE partner_shops ADD COLUMN IF NOT EXISTS item_mode TEXT NOT NULL DEFAULT 'single';
ALTER TABLE partner_shops ADD COLUMN IF NOT EXISTS variants JSONB NOT NULL DEFAULT '[]'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'partner_shops_item_mode_check'
    ) THEN
        ALTER TABLE partner_shops
            ADD CONSTRAINT partner_shops_item_mode_check CHECK (item_mode IN ('single', 'multi'));
    END IF;
END $$;

INSERT INTO schema_migrations (version) VALUES ('191') ON CONFLICT DO NOTHING;
