-- 174_pet_scoring.sql
-- 寵物雲端馬拉松（2026-09-09 owner request，接續 migration 173 的「報名」部分）：新增兩種完賽條件/
-- 排名規則——(1) 狗狗累積里程 (2) 飼主＋狗狗累積里程加總。本輪只做計分，不動報名資料模型。
--
-- races.pet_score_mode：僅 pet_kind != '' 時有意義（非寵物賽事一律必須是 ''，app 層
-- normalizeRequest 強制清空）。''=依飼主里程（預設，等同今天行為）｜pet=狗狗累積里程｜
-- owner_pet_sum=飼主＋狗狗里程加總。
ALTER TABLE races
  ADD COLUMN IF NOT EXISTS pet_score_mode VARCHAR(16) NOT NULL DEFAULT ''
    CHECK (pet_score_mode IN ('', 'pet', 'owner_pet_sum'));

-- pet_activities：狗狗（寵物）的移動里程紀錄，來源中立（source-agnostic）——本輪唯一寫入路徑是
-- 'owner_run'（飼主自己的跑步紀錄勾選「狗狗有一起跑」），但表結構同時準備給未來的第三方寵物資料
-- 平台／項圈裝置（source='platform'）與後台手動輸入（source='manual'）寫入同一張表，不必另建管線。
--
-- activity_id 僅 source='owner_run' 時有值（指向 activities.id，飼主那趟跑步的活動列）；
-- UNIQUE (registration_pet_id, activity_id) WHERE activity_id IS NOT NULL 確保同一隻寵物同一趟
-- 活動只會被記一次（worker 端 insert 的冪等保護，取代 ON CONFLICT 判斷）。
-- flagged 比照 activities.flagged：異常回收（recall）時一併標記，計分時排除。
CREATE TABLE IF NOT EXISTS pet_activities (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    registration_pet_id UUID NOT NULL REFERENCES registration_pets(id) ON DELETE CASCADE,
    registration_id     UUID NOT NULL,
    race_id             UUID NOT NULL,
    user_id             UUID NOT NULL,
    activity_id         UUID NULL,
    source              VARCHAR(16) NOT NULL CHECK (source IN ('owner_run', 'platform', 'manual')),
    distance_km         NUMERIC(8,3) NOT NULL,
    duration_s          INT NOT NULL DEFAULT 0,
    recorded_at         TIMESTAMPTZ NOT NULL,
    flagged             BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_pet_activities_pet_activity
  ON pet_activities(registration_pet_id, activity_id) WHERE activity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pet_activities_race_reg_time
  ON pet_activities(race_id, registration_id, recorded_at);

-- gps_runs.pet_ids：上傳當下（活動列尚未存在前）先記下這趟跑步是哪幾隻寵物一起跑（歸戶用），
-- worker 建好 activities 列之後立刻依此寫入 pet_activities（見 D3(a)）；同時保留原始欄位供孤兒
-- 復原路徑（RequeueUnenqueued）與異常回收（recall）追溯歸戶用。
ALTER TABLE gps_runs
  ADD COLUMN IF NOT EXISTS pet_ids UUID[] NOT NULL DEFAULT '{}';

INSERT INTO schema_migrations (version) VALUES ('174') ON CONFLICT DO NOTHING;
