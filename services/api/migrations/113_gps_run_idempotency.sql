-- 113_gps_run_idempotency.sql
-- 根因（2026-07-31 鑑識）：同一趟 GPS 會被上傳兩次——一次是結束時 finish() 正常上傳，
-- 另一次是「手機跳掉→切回」時，localStorage(dor_gps_run) 殘留軌跡觸發「有一趟未上傳的跑步」
-- 提示、使用者再按上傳（uploadRecovered），而每一層都沒有防重：
--   * gps_runs 無任何 UNIQUE 約束 → 無條件 INSERT 兩筆。
--   * activities 的 `ON CONFLICT DO NOTHING` 掛在唯一索引 idx_activities_source_ext(source, external_id)，
--     GPS 來源這兩欄皆為 NULL，Postgres 唯一索引視 NULL 互不相等 → 對 GPS 形同虛設。
-- 結果：兩筆一模一樣的 GPS 活動都 flagged=false，數據探索 SUM(distance_km) WHERE NOT flagged 會把兩筆
--       都加總（4.65+4.65=9.3）。EXP 因 awardMileageDedup 只發一次（第二筆 exp_awarded=false），
--       但列表/里程仍雙計。
--
-- 本 migration 做兩件事：
--   (1) 清 gps_runs 既有重複 → 建 UNIQUE(user_id, started_at)，讓「同一 user + 同一起跑時間」只能有一筆
--       （應用層再配 ON CONFLICT DO NOTHING，重送即 no-op、不再 emit 活動事件）。
--   (2) 清 activities 既有「同源(GPS)完全重複」列：同 user + 結束時間 + 時長 + 距離者，保留有發獎/最早那筆，
--       其餘標 flagged='duplicate'，使數據探索里程 SUM 立即回正（例：某使用者 7/31 由 9.3 回 4.65）。

-- (1a) gps_runs 去重：同 (user_id, started_at) 保留最早 created_at 一筆，其餘刪除（否則唯一索引建不起來 23505）。
--      這些是「同一趟被重送」產生的重複，刪除安全（軌跡完全相同）。
WITH d AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY user_id, started_at ORDER BY created_at ASC, id) AS rn
    FROM gps_runs
)
DELETE FROM gps_runs g USING d
WHERE g.id = d.id AND d.rn > 1;

-- (1b) 唯一索引：同一 user 的同一起跑時間只能存在一筆 GPS 軌跡（起跑時間為毫秒級，不同趟不可能相同）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_gps_runs_user_start ON gps_runs(user_id, started_at);

-- (2) activities 同源(GPS，source IS NULL)完全重複：同 user + recorded_at(結束時間) + duration_s + distance_km，
--     保留「有發獎(exp_awarded=true) 優先、其次 created_at 最早」那筆為 keeper，其餘標 flagged='duplicate'、dup_of 指向 keeper。
--     兩筆 GPS 若結束時間/時長/距離完全相同即為同一趟（不可能同時進行兩段 GPS 追蹤），標記安全。
WITH d AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY user_id, recorded_at, duration_s, distance_km
               ORDER BY exp_awarded DESC, created_at ASC, id
           ) AS rn,
           FIRST_VALUE(id) OVER (
               PARTITION BY user_id, recorded_at, duration_s, distance_km
               ORDER BY exp_awarded DESC, created_at ASC, id
           ) AS keeper
    FROM activities
    WHERE source IS NULL AND NOT flagged
)
UPDATE activities a
SET flagged = TRUE, flag_reason = 'duplicate', dup_of = d.keeper
FROM d
WHERE a.id = d.id AND d.rn > 1;

INSERT INTO schema_migrations (version) VALUES ('113') ON CONFLICT DO NOTHING;
