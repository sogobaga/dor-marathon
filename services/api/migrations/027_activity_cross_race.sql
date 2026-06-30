-- Migration 027: 跨賽事歸戶 — 一筆跑步計入「報名中 + recorded_at 落在賽事期間」的所有賽事
-- （查詢改以 user_id + recorded_at 範圍比對，不再用 activity.race_id）。加索引維持效能。
CREATE INDEX IF NOT EXISTS idx_activities_user_recorded
    ON activities(user_id, recorded_at) WHERE NOT flagged;

INSERT INTO schema_migrations (version) VALUES ('027') ON CONFLICT DO NOTHING;
