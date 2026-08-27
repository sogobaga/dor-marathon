-- Migration 148：會員活躍度分析——後台每日彙整報告（六大區塊：註冊/上線/里程配速/參與度/卡片收集/
-- 各系統使用足跡），JSONB 整包存檔，避免每次讀取都重跑重查詢（各區塊多為全表population掃描，
-- 見 internal/analytics/compute.go）。
--
-- day：報告代表的台灣日（YYYY-MM-DD，PK；ON CONFLICT(day) DO UPDATE 供同日重算覆寫，見
-- internal/analytics/repository.go SaveReport）。report 內容契約固定鍵名，前後端共同依據（見
-- internal/analytics/model.go 各 struct 的 json tag）；computed_at 為實際計算完成時間（非 day
-- 當天 00:00，供 GET /admin/analytics/report 判斷 stale：超過 48h 未重算即視為過期）。
--
-- 排程骨架比照 internal/ops/selfcheck.go（hourly tick + pg_try_advisory_lock + in-memory
-- lastRunDate 當日冪等），執行窗口改台灣時間 03:00-03:59（離峰離開 08:00 的每日自檢/營運報告，
-- 三個排程互不搶時段）；鎖名 "member_analytics_daily" 獨立於既有 "ops_daily_selfcheck"/
-- "ops_daily_report"，避免互搶。
--
-- ORDER BY day DESC LIMIT 1（讀最新一筆）直接吃 PK 的 btree 索引反向掃描，不需額外索引。
CREATE TABLE IF NOT EXISTS member_analytics_reports (
    day         DATE PRIMARY KEY,
    report      JSONB NOT NULL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('148') ON CONFLICT DO NOTHING;
