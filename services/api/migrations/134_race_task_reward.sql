-- Migration 134: 即時獎勵一般化——從個人挑戰模式(personal)擴大到所有賽事模式。
-- 觸發點＝完成「個人額外挑戰」(race_tasks.scope='group_individual') 當下（見 race.GetRaceProgress /
-- race.MarkRaceTaskCompletedAndGrant）。races.reward_config 欄位（migration 127）本輪起不再限 personal
-- 模式才能設定；ChallengeRule 與其專屬完成引擎(personal_progress.go)仍僅 personal 使用，不受影響。
--
-- race_task_completions 記錄「哪個使用者、哪個任務」已觸發過一次即時獎勵 CAS——UNIQUE(task_id,user_id)
-- 是唯一的冪等防線（比照 personal 模式用 registrations.status/completed_at 的 CAS UPDATE 手法，這裡改用
-- INSERT...ON CONFLICT DO NOTHING，因為「完成一個任務」沒有既有的狀態欄位可 CAS）。

CREATE TABLE IF NOT EXISTS race_task_completions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id      UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    task_id      UUID NOT NULL REFERENCES race_tasks(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (task_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_rtc_race_user ON race_task_completions(race_id, user_id);

INSERT INTO schema_migrations (version) VALUES ('134') ON CONFLICT DO NOTHING;
