-- Migration 029: 賽事可設定兩種排行榜是否顯示（預設都顯示）
-- 累積里程榜 / 完成時間榜（時間·配速）。非里程或非配速賽可關閉其一。
ALTER TABLE races ADD COLUMN IF NOT EXISTS show_distance_rank BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE races ADD COLUMN IF NOT EXISTS show_time_rank     BOOLEAN NOT NULL DEFAULT TRUE;

INSERT INTO schema_migrations (version) VALUES ('029') ON CONFLICT DO NOTHING;
