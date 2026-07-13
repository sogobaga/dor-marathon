-- Migration 075: 城市探索關主「自由挑戰」——記錄最短完成時間，供時間榜排序
-- 收服(3★)後可重複「自由挑戰」；排行改以最短一次完成時間排序。
ALTER TABLE explore_progress ADD COLUMN IF NOT EXISTS best_time_s INT; -- 最短一次有效完成挑戰的秒數（NULL=尚無）

INSERT INTO schema_migrations (version) VALUES ('075') ON CONFLICT DO NOTHING;
