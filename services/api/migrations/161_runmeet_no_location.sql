-- Migration 161: 團練支援「不限地點」
--
-- 需求（使用者原話）：「集合地點多一個『不限地點』的選項，因為有可能只是想發起一個團練，
-- 但是沒有想要大家一起在同一個地點團練。如果設定為『不限地點』的話，行政區和地點名稱，
-- 預設就帶入『不限』的文字；在團練詳細頁中，就隱藏 GPS 的地圖，改顯示『不限地點』的相關文字。」
--
-- ⚠️ 用獨立布林欄位而不是「比對 region 是不是『不限』」：
--    文字比對很脆弱（使用者本來就能自己在地點名稱打「不限」「不限定」等字樣），
--    而這個旗標會決定「要不要畫地圖」「要不要進附近搜尋」這些行為，必須是明確的狀態。
--
-- ⚠️ CHECK 保證資料一致：不限地點就不該有座標。
--    少了這條，某次改版忘了清空 lat/lng 就會出現「標示不限地點、卻仍能被附近搜尋撈到」的矛盾資料，
--    而附近搜尋是有隱私設計的（座標量化、距離分級，見 migration 156 與 internal/runmeet/geo.go），
--    留著死座標等於在使用者以為「我沒有公開地點」的情況下仍然洩漏位置。
--
-- 既有資料不受影響：預設 FALSE，且既有列的 lat/lng 不受新 CHECK 影響（NOT FALSE OR ... 恆真）。

ALTER TABLE run_meets
    ADD COLUMN IF NOT EXISTS no_location BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE run_meets DROP CONSTRAINT IF EXISTS run_meets_noloc_chk;
ALTER TABLE run_meets ADD CONSTRAINT run_meets_noloc_chk
    CHECK (NOT no_location OR (lat IS NULL AND lng IS NULL));

INSERT INTO schema_migrations (version) VALUES ('161') ON CONFLICT DO NOTHING;
