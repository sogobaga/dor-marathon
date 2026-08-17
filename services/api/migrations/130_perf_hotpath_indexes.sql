-- 1000 人併發效能盤點：熱路徑索引補強。

-- 1) activities 使用者統計熱路徑（dashboard/成就/稱號/體力/訓練/賽事進度排行榜全家族都是
--    user_id=$1 AND NOT flagged [AND recorded_at 範圍]）。既有 idx_activities_user_recorded 非 partial，
--    flagged 副本列仍要 heap-fetch 才濾掉；partial 版可讓只取 recorded_at 的查詢升級 Index Only Scan。
--    ⚠️ 保留舊索引不刪（personaltask.go accDistance 的 (NOT flagged OR flag_reason='cross_source_duplicate') OR 條件仍需要它）。
CREATE INDEX IF NOT EXISTS idx_activities_user_recorded_unflagged
    ON activities (user_id, recorded_at) WHERE NOT flagged;

-- 2) worker resolveCrossSourceDups 自癒 UPDATE 的謂詞完全無索引→每輪全表掃描；穩態下此 partial index 幾乎是空的
CREATE INDEX IF NOT EXISTS idx_activities_gps_stale_dup
    ON activities (id) WHERE source IS NULL AND flag_reason = 'cross_source_duplicate';

-- 3) explore.go:675 / personaltask.go:531 的 gps_runs 20 分鐘窗查詢：現有索引排序鍵是 created_at、查詢要 ended_at
CREATE INDEX IF NOT EXISTS idx_gps_runs_user_ended ON gps_runs (user_id, ended_at DESC);

-- 4) event_race.go:341-348 RaceTrigger 的 NOT EXISTS 子查詢 (def_id, initiator_user_id, triggered_at) 無索引，隨賽事進行線性惡化
CREATE INDEX IF NOT EXISTS idx_event_race_inst_def_initiator
    ON event_race_instances (def_id, initiator_user_id, triggered_at DESC);

INSERT INTO schema_migrations (version) VALUES ('130') ON CONFLICT DO NOTHING;
