-- Migration 030: 打卡點任務（geofence check-in / 多點集章）
-- 任務指標 metric_type='checkpoint'：在賽事期間到指定座標半徑內打卡，集滿全部點即完成。
-- 打卡以 GPS 軌跡佐證（前景追蹤的近期軌跡），缺佐證者標記待審。

-- 任務的打卡點（一個 checkpoint 任務可有多個點 → 集章）
CREATE TABLE IF NOT EXISTS task_checkpoints (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES race_tasks(id) ON DELETE CASCADE,
    lat           DOUBLE PRECISION NOT NULL,
    lng           DOUBLE PRECISION NOT NULL,
    radius_m      INT NOT NULL DEFAULT 20,   -- 可接受打卡半徑（公尺）
    title         VARCHAR(120),              -- 點位名稱（前台顯示）
    display_order INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_task_checkpoints_task ON task_checkpoints(task_id);

-- 會員打卡紀錄（每人每點唯一）
CREATE TABLE IF NOT EXISTS checkpoint_checkins (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    checkpoint_id UUID NOT NULL REFERENCES task_checkpoints(id) ON DELETE CASCADE,
    race_id       UUID REFERENCES races(id) ON DELETE SET NULL,
    lat           DOUBLE PRECISION NOT NULL,
    lng           DOUBLE PRECISION NOT NULL,
    accuracy      DOUBLE PRECISION,
    distance_m    DOUBLE PRECISION,          -- 打卡點與會員位置的實際距離（伺服器重算）
    flagged       BOOLEAN NOT NULL DEFAULT FALSE,  -- 缺軌跡佐證等 → 待審（不計完成）
    flag_reason   TEXT,
    checked_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, checkpoint_id)
);
CREATE INDEX IF NOT EXISTS idx_checkin_user ON checkpoint_checkins(user_id);
CREATE INDEX IF NOT EXISTS idx_checkin_flagged ON checkpoint_checkins(flagged) WHERE flagged;

INSERT INTO schema_migrations (version) VALUES ('030') ON CONFLICT DO NOTHING;
