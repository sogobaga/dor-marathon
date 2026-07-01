-- Migration 034: 事件任務（Phase A：日常隨機事件）
-- 跑步中依 GPS 即時觸發的小任務。觸發/完成/獎勵為模組化（type + jsonb params）。
-- 獎勵不走 exp_ledger（其 race_id NOT NULL、綁賽事）；比照日常里程直接加 users.exp/dp，
-- 並以 occurrence 列作為紀錄與冪等守門。

-- 事件定義（＝可重複引用的範本）
CREATE TABLE IF NOT EXISTS event_task_defs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             VARCHAR(120) NOT NULL,
    description      TEXT,
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    weight           INT NOT NULL DEFAULT 100,   -- 隨機加權（越大越常被選中）
    cooldown_sec     INT NOT NULL DEFAULT 300,   -- 同一次跑步兩次觸發最小間隔
    trigger_type     VARCHAR(40) NOT NULL,       -- distance_below | distance_above | ...
    trigger_params   JSONB NOT NULL DEFAULT '{}',
    completion_type  VARCHAR(40) NOT NULL,       -- move_more | move_less | ...
    completion_params JSONB NOT NULL DEFAULT '{}',
    message          TEXT NOT NULL DEFAULT '',   -- 觸發時的劇情文案
    reward_exp       INT NOT NULL DEFAULT 0,
    reward_dp        INT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_event_defs_enabled ON event_task_defs(enabled) WHERE enabled;

-- 每次觸發實例（防弊/冪等/紀錄）
CREATE TABLE IF NOT EXISTS event_task_occurrences (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    def_id            UUID NOT NULL REFERENCES event_task_defs(id) ON DELETE CASCADE,
    status            VARCHAR(20) NOT NULL DEFAULT 'active', -- active|completed|failed|expired
    triggered_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at      TIMESTAMPTZ,
    reward_exp        INT NOT NULL DEFAULT 0,   -- 觸發當下快照的獎勵
    reward_dp         INT NOT NULL DEFAULT 0,
    awarded           BOOLEAN NOT NULL DEFAULT FALSE,
    trigger_dist_m    DOUBLE PRECISION,        -- 觸發時的累積距離（公尺）
    trigger_elapsed_s INT,                     -- 觸發時的已跑秒數
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_event_occ_user ON event_task_occurrences(user_id, created_at DESC);

INSERT INTO schema_migrations (version) VALUES ('034') ON CONFLICT DO NOTHING;
