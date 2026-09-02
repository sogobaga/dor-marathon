-- Migration 165: user_integrations 加 via 欄位，區分「直連 OAuth」與「經 Terra 聚合器」連線
-- 依賴：014_integrations.sql
-- Terra Phase 1：同一品牌（如 coros）可能是使用者直接走本站既有 OAuth（COROS 開發者後台）連上，
-- 也可能是透過 Terra 聚合器（tryterra.co，一次接 Garmin/COROS/Polar/Suunto/Wahoo）連上。
-- via 標記這條連線的來源管道：'direct'（既有 OAuth 流程，預設值，涵蓋所有既有資料列，
-- 不需回填）或 'terra'（本次新增，Terra 寫入的連線一律標記此值）。
-- ⚠️ (user_id, provider) 仍是唯一鍵（未變更，見 014）：同一品牌不會同時有一條 direct 與一條 terra
-- 連線並存——後連上的一方會覆蓋前者（並把 via 一併改成最新管道），見 internal/integration/repository.go
-- Save()/SaveTerra() 的註解。/status 端點依 via 篩選「這張卡片該顯示哪條連線」。
ALTER TABLE user_integrations ADD COLUMN IF NOT EXISTS via VARCHAR(20) NOT NULL DEFAULT 'direct';
COMMENT ON COLUMN user_integrations.via IS '連線管道：direct=本站既有 OAuth 直連；terra=透過 Terra 聚合器（tryterra.co）';

INSERT INTO schema_migrations (version) VALUES ('165') ON CONFLICT DO NOTHING;
