-- Migration 076: Phase B１ 共享累積目標（co-op / 眾志成城）
-- 在既有 Phase B 個人賽（individual）表上增量支援 collective 模式：
-- 觸發後全體受邀者共同貢獻里程累積進度，達標（跨全員的單一 faction=''）即全員一次結算發獎。
-- individual 既有流程/欄位預設值不變（mode 預設 'individual'），不影響既有行為。

ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'individual'; -- individual | collective
ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS goal_metric TEXT; -- B1 僅實作 distance_m
ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS goal_target NUMERIC;
ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS goal_window_s INT;

ALTER TABLE event_race_participants ADD COLUMN IF NOT EXISTS faction TEXT; -- co-op 留 NULL（保留給日後陣營對抗模式）
ALTER TABLE event_race_participants ADD COLUMN IF NOT EXISTS contributed NUMERIC NOT NULL DEFAULT 0;
-- 規格未列但 B1 防弊邏輯（單次貢獻上限＝距上次貢獻秒數 * 7.7m/s）需要的狀態：每位參與者上次成功貢獻的時間戳。
ALTER TABLE event_race_participants ADD COLUMN IF NOT EXISTS last_contributed_at TIMESTAMPTZ;

ALTER TABLE event_race_instances ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'individual'; -- 觸發時快照 def.mode
ALTER TABLE event_race_instances ADD COLUMN IF NOT EXISTS goal_deadline TIMESTAMPTZ;

-- 累積進度（faction=''：B1 全員共享同一進度；預留 faction 供日後陣營對抗模式擴充為多列）
-- target 必須 >0：RaceTrigger 只在 goal_target>0 時才會 INSERT 這張表，避免產生永遠達不成的 instance。
CREATE TABLE IF NOT EXISTS event_race_progress (
    instance_id UUID NOT NULL REFERENCES event_race_instances(id) ON DELETE CASCADE,
    faction     TEXT NOT NULL DEFAULT '',
    current     NUMERIC NOT NULL DEFAULT 0,
    target      NUMERIC NOT NULL CHECK (target > 0),
    reached_at  TIMESTAMPTZ,
    PRIMARY KEY (instance_id, faction)
);

INSERT INTO schema_migrations (version) VALUES ('076') ON CONFLICT DO NOTHING;
