-- Migration 016: 修正活動去重唯一索引
-- 014 建的是「部分唯一索引」(WHERE source/external_id NOT NULL)，
-- PostgreSQL 的 ON CONFLICT (source, external_id) 推斷無法匹配部分索引 → 每次匯入都報錯、0 筆寫入。
-- 改為非部分唯一索引（NULL 在唯一索引中視為相異，手動活動的 NULL 仍不衝突）。

DROP INDEX IF EXISTS idx_activities_source_ext;
CREATE UNIQUE INDEX IF NOT EXISTS idx_activities_source_ext ON activities(source, external_id);

INSERT INTO schema_migrations (version) VALUES ('016') ON CONFLICT DO NOTHING;
