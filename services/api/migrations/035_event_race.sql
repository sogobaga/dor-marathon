-- Migration 035: 賽事多人連動事件（Phase B）
-- 觸發者（正在跑步、且與收件者報名同一賽事）累積移動達門檻 → 觸發一個多人事件，
-- 依「相對觸發者」的對象規則（分組/追蹤/性別；同類互斥、跨類交集）挑出同賽事報名者，
-- 經 WebSocket 即時邀請 → 限時 join → 各自在時限內達標 → 各自發獎（含每人每日上限）。
-- 全域規則：一個跑者同時只有一個進行中任務、任務間至少 15 分鐘冷卻（跨 Phase A/B）。

-- 多人事件定義
CREATE TABLE IF NOT EXISTS event_race_defs (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  VARCHAR(120) NOT NULL,
    description           TEXT,
    enabled               BOOLEAN NOT NULL DEFAULT TRUE,
    race_id               UUID REFERENCES races(id) ON DELETE CASCADE, -- NULL = 適用所有賽事
    weight                INT NOT NULL DEFAULT 100,       -- 多筆符合時的加權隨機
    trigger_min_m         INT NOT NULL DEFAULT 1000,      -- 觸發者累積移動達此公尺數即可能觸發
    initiator_cooldown_sec INT NOT NULL DEFAULT 900,      -- 同一觸發者兩次觸發最小間隔（預設 15 分）
    target_count          INT NOT NULL DEFAULT 0,         -- 隨機推撥人數上限（0 = 全部符合者）
    group_rel             VARCHAR(10) NOT NULL DEFAULT 'any',    -- any | same | diff（同組/非同組）
    follow_rel            VARCHAR(12) NOT NULL DEFAULT 'any',    -- any | following | follower
    gender_rel            VARCHAR(10) NOT NULL DEFAULT 'any',    -- any | same | diff
    join_window_s         INT NOT NULL DEFAULT 60,        -- 收到邀請後可加入的秒數
    completion_type       VARCHAR(40) NOT NULL,           -- move_more | move_less（沿用 Phase A 完成型錄）
    completion_params     JSONB NOT NULL DEFAULT '{}',
    message               TEXT NOT NULL DEFAULT '',        -- 邀請時的劇情文案
    reward_exp            INT NOT NULL DEFAULT 0,
    reward_dp             INT NOT NULL DEFAULT 0,
    per_user_daily_cap    INT NOT NULL DEFAULT 0,          -- 每人每日此事件發獎次數上限（0 = 不限）
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_event_race_defs_enabled ON event_race_defs(enabled) WHERE enabled;

-- 每次觸發的事件實例
CREATE TABLE IF NOT EXISTS event_race_instances (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    def_id            UUID NOT NULL REFERENCES event_race_defs(id) ON DELETE CASCADE,
    race_id           UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    initiator_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    triggered_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    join_deadline     TIMESTAMPTZ NOT NULL,      -- 觸發時間 + join_window_s
    target_user_ids   UUID[] NOT NULL DEFAULT '{}', -- 當下被邀請的對象（防弊：只有名單內可 join）
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_event_race_inst_race ON event_race_instances(race_id, triggered_at DESC);

-- 參與者（收邀 → join 後建立；每人每實例唯一）
CREATE TABLE IF NOT EXISTS event_race_participants (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id  UUID NOT NULL REFERENCES event_race_instances(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status       VARCHAR(20) NOT NULL DEFAULT 'joined', -- joined | completed | failed | expired
    joined_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deadline     TIMESTAMPTZ NOT NULL,          -- join 時間 + 完成時限（completion limit_s）
    completed_at TIMESTAMPTZ,
    moved_m      DOUBLE PRECISION,
    reward_exp   INT NOT NULL DEFAULT 0,
    reward_dp    INT NOT NULL DEFAULT 0,
    awarded      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (instance_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_event_race_part_user ON event_race_participants(user_id, joined_at DESC);

INSERT INTO schema_migrations (version) VALUES ('035') ON CONFLICT DO NOTHING;
