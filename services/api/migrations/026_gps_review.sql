-- Migration 026: GPS 跑步後台審核
ALTER TABLE gps_runs ADD COLUMN IF NOT EXISTS reviewed_at   TIMESTAMPTZ;
ALTER TABLE gps_runs ADD COLUMN IF NOT EXISTS review_action TEXT; -- approved | rejected

-- 待審清單（flagged 且尚未審核）
CREATE INDEX IF NOT EXISTS idx_gps_runs_pending ON gps_runs(created_at DESC) WHERE flagged AND reviewed_at IS NULL;

INSERT INTO schema_migrations (version) VALUES ('026') ON CONFLICT DO NOTHING;
