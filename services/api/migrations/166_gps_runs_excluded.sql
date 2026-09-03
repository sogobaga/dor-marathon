-- Migration 166: gps_runs 加「被排除區段」統計欄位（軌跡斷點防弊/UX 修正）
-- 依賴：025_gps_runs.sql、154_gps_calibration.sql
-- 事故背景（2026-09-03）：使用者步行約 2km 後搭捷運（地下無 GPS 訊號）移動約 10km，原本只有
-- 「d/dt > 極限速度」一種超速判定，長時間斷點（dt≈25分鐘）讓兩點間直線距離除以經過時間仍低於
-- 極限速度、未被攔下，整段直線 10km 被誤計入距離。新增規則（見 internal/activity/gps.go
-- computeRun）：dt > 60 秒「且」d > 250 公尺 的區段一律視為無效（不論速度是否超標）——訊號斷點
-- 不算作弊，但該區段距離不計入、也不列入「超速占比」防弊判定，只是被排除。
-- excluded_km：該趟被排除區段的原始直線距離加總（公尺 / 1000，四捨五入到小數 3 位）；不套 GPS
-- 距離校正係數 k（校正只用於「有效」距離，見 gpscalib）。excluded_segments：被排除的區段數
-- （超速規則與斷點規則兩者皆可能命中同一區段，只算一次）。前端歷史頁依此顯示「⚠️ 已排除 Xkm」
-- 提示；這趟本身仍照常存檔，不因排除而整趟作廢。
ALTER TABLE gps_runs
  ADD COLUMN IF NOT EXISTS excluded_km       NUMERIC(8,3) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS excluded_segments INTEGER      NOT NULL DEFAULT 0;
COMMENT ON COLUMN gps_runs.excluded_km IS '被排除區段的原始直線距離加總（km，不套校正係數 k）；訊號斷點(dt>60s且d>250m)或超速皆計入，同一區段只算一次';
COMMENT ON COLUMN gps_runs.excluded_segments IS '被排除的區段數（超速規則∪斷點規則，同一區段只算一次）';

INSERT INTO schema_migrations (version) VALUES ('166') ON CONFLICT DO NOTHING;
