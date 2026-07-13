-- Migration 077: Phase B3 後端排程主動觸發 + 管理員立即發起
-- server/admin 發起的 collective 事件沒有實際觸發跑者（非某位玩家跑步累積門檻觸發），
-- 因此 event_race_instances.initiator_user_id 需可為 NULL。
ALTER TABLE event_race_instances ALTER COLUMN initiator_user_id DROP NOT NULL;

-- 排程表：到指定時間且尚未觸發者，由 RunScheduleLoop 依該賽事「目前在跑」名單建立 collective 事件實例。
CREATE TABLE IF NOT EXISTS event_race_schedules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    def_id     UUID NOT NULL REFERENCES event_race_defs(id) ON DELETE CASCADE,
    race_id    UUID REFERENCES races(id) ON DELETE SET NULL, -- NULL = 退回 def 綁定的賽事（def 也未綁賽事則無法決定在跑名單，略過不發）
    event_at   TIMESTAMPTZ NOT NULL,
    fired_at   TIMESTAMPTZ, -- NULL = 尚未觸發；由 RunScheduleLoop 單次搶發（UPDATE ... WHERE fired_at IS NULL）
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_event_race_schedules_pending ON event_race_schedules(fired_at, event_at);

INSERT INTO schema_migrations (version) VALUES ('077') ON CONFLICT DO NOTHING;
