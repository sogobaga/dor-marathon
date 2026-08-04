-- 117_partner_video_urls.sql
-- 跑者充電站商家：從單一 video_url 擴充為多支 YouTube 影片連結 video_urls（JSONB 陣列）。
-- 存取模式照抄 photo_urls（見 internal/partner/repository.go marshalPhotoURLs/unmarshalPhotoURLs）。
-- video_url 欄位保留不刪（back-compat）。

ALTER TABLE partner_shops ADD COLUMN IF NOT EXISTS video_urls JSONB NOT NULL DEFAULT '[]'::jsonb;

-- 把既有單一 video_url 回填進陣列（只在 video_urls 還是空陣列時，避免覆蓋已手動維護過的資料）。
UPDATE partner_shops SET video_urls = jsonb_build_array(video_url)
  WHERE video_url <> '' AND video_urls = '[]'::jsonb;

INSERT INTO schema_migrations (version) VALUES ('117') ON CONFLICT DO NOTHING;
