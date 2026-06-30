-- Migration 028: GPS 軌跡回放/歷史 — 壓縮儲存
-- 改存「簡化(Douglas-Peucker) + encoded polyline」字串（每筆數百 bytes），所有跑步都留軌跡可回放。
-- 不再存原始點陣列（points 欄保留為 legacy，新資料一律 NULL）。
ALTER TABLE gps_runs ADD COLUMN IF NOT EXISTS polyline TEXT;

INSERT INTO schema_migrations (version) VALUES ('028') ON CONFLICT DO NOTHING;
