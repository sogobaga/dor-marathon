-- 122_partner_content_images.sql
-- 跑者充電站商家：加「滿版長圖」欄位 content_images（JSONB 陣列），與 photo_urls（輪播相簿）分開，
-- 詳細頁滿版直列顯示（填滿寬、依原比例、不限高），適合放產品 DM／長圖資訊圖。
-- 存取模式照抄 photo_urls/video_urls（見 internal/partner/repository.go marshalPhotoURLs/unmarshalPhotoURLs）。

ALTER TABLE partner_shops ADD COLUMN IF NOT EXISTS content_images JSONB NOT NULL DEFAULT '[]'::jsonb;

INSERT INTO schema_migrations (version) VALUES ('122') ON CONFLICT DO NOTHING;
