-- Migration 157: 團練圖片張數上限不寫死在資料庫層
--
-- 背景：156 建表時把 CHECK 寫成 cardinality(image_urls) <= 4，與當時 app_settings 的
-- runmeet_images_vip 預設值 4 相同。結果是「後台把 VIP 張數調到 5」會通過設定驗證，
-- 但實際建立團練時被 DB CHECK 擋下（500），等於營運可調的設定其實動不了。
--
-- 這次把 DB 層改成「技術安全上限」而非「營運政策上限」：
--   ・營運政策（每團幾張）由 app_settings 的 runmeet_images_normal / _vip 決定，後台隨時可調。
--   ・DB CHECK 只負責擋住異常值（程式 bug、直接寫 DB），取 20 張。
--   ・20 的取法：圖片存 Postgres bytea，壓縮後單張約 200–400KB；20 張約 8MB／團，
--     以 VIP 每月 10 團計約 80MB／人／月，對 Neon 儲存成本仍在可控範圍。
--     真要再放寬，改這裡與 appsettings.go 的 isPosIntMax、appSettings.ts 的 max 三處即可。
--
-- ⚠️ 既有資料不受影響：現有團練最多 4 張，一定滿足新的 <= 20。
--    放寬 CHECK 不需要重寫資料表，PostgreSQL 只做約束定義變更（不掃描既有列）。

ALTER TABLE run_meets DROP CONSTRAINT IF EXISTS run_meets_images_chk;
ALTER TABLE run_meets ADD CONSTRAINT run_meets_images_chk CHECK (cardinality(image_urls) <= 20);

INSERT INTO schema_migrations (version) VALUES ('157') ON CONFLICT DO NOTHING;
