-- Migration 195: GPS 原始定位點記錄（除錯用，見 docs/gps/GPS_START_GATE_RAWLOG_CONTRACT.md 契約 B）
-- 依賴：025_gps_runs.sql（gps_runs.id UUID 主鍵）、users.id UUID 主鍵（見 001 系列 schema）
--
-- 背景（2026-09-29 owner 拍板）：當天實跑 App 5.44 km vs COROS 5.307 km，懷疑 App GPS 精度問題，
-- 需要保存「按下開始那一刻起」的原始定位點序列（含未被採納/被排除的點與其分類碼）供離線重播分析。
-- 只對使用者本人帳號（白名單，見 internal/gpsrawlog.Allowed／app_settings gps_raw_log_whitelist），
-- 30 天保存期限（見 internal/gpsrawlog.PurgeExpired，掛在既有每日報告排程，不另開週期性 DB 查詢），
-- 只有後台超管看得到（GET /api/v1/admin/gps-runs/{runId}/raw-points，RequireSuper）。
--
-- run_id 直接當主鍵（一趟跑步至多一筆原始記錄，同一 run 重送＝覆寫，見 UPSERT 寫入端）；
-- ON DELETE CASCADE 隨 gps_runs／users 列刪除自動清除，不會留孤兒列。
-- points 格式：{"v":1,"fields":[...],"rows":[[...],...]}（欄位定義見契約 B「前端收集」段，
-- 每列＝[t_ms, lat, lng, acc, speed|null, heading|null, code]），存整包 JSONB 而非正規化多列表——
-- 這是除錯用途的離線重播資料，不需要對個別欄位做 SQL 查詢/索引，正規化只會徒增寫入/讀取成本。

CREATE TABLE IF NOT EXISTS gps_run_raw_points (
  run_id      UUID PRIMARY KEY REFERENCES gps_runs(id) ON DELETE CASCADE,
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  point_count INT  NOT NULL,
  points      JSONB NOT NULL,          -- {"v":1,"fields":[...],"rows":[[...],...]}
  client_version TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 供保存期限排程（DELETE ... WHERE created_at < now() - interval '30 days'）快速掃描，
-- 也是唯一會被用來查詢這張表的欄位（其餘存取一律是 run_id 主鍵單筆讀寫）。
CREATE INDEX IF NOT EXISTS idx_gps_run_raw_points_created ON gps_run_raw_points(created_at);

INSERT INTO schema_migrations (version) VALUES ('195') ON CONFLICT DO NOTHING;
