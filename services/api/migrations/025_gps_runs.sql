-- Migration 025: 網頁版 GPS 跑步追蹤（PoC）
-- 記錄前景追蹤的軌跡 + 伺服器端重算與防弊結果。實際里程/EXP 仍走既有活動管線。
CREATE TABLE IF NOT EXISTS gps_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    race_id     UUID REFERENCES races(id) ON DELETE SET NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    ended_at    TIMESTAMPTZ NOT NULL,
    distance_km NUMERIC NOT NULL,
    duration_s  INT NOT NULL,
    avg_pace_s  INT NOT NULL,
    flagged     BOOLEAN NOT NULL DEFAULT FALSE,
    flag_reason TEXT,
    point_count INT NOT NULL DEFAULT 0,
    points      JSONB,                 -- [[lat,lng,t_ms,acc],...]
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_gps_runs_user ON gps_runs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_gps_runs_flagged ON gps_runs(flagged) WHERE flagged;

INSERT INTO schema_migrations (version) VALUES ('025') ON CONFLICT DO NOTHING;
