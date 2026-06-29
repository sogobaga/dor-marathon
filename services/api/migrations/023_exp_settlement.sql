-- Migration 023: EXP 結算引擎
-- 賽事結束時依「完成賽事 / 完成任務 / 里程」發 EXP（加進 users.exp，等級由 level_config 即時推導）。
-- exp_ledger 記每筆發放，UNIQUE(user_id,race_id,source) 保證可重跑不重複發。

ALTER TABLE races ADD COLUMN IF NOT EXISTS exp_settled_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS exp_ledger (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    race_id    UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    source     TEXT NOT NULL,          -- completion | mileage | task:<task_id>
    amount     INT  NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, race_id, source)
);
CREATE INDEX IF NOT EXISTS idx_exp_ledger_race ON exp_ledger(race_id);
CREATE INDEX IF NOT EXISTS idx_exp_ledger_user ON exp_ledger(user_id);

INSERT INTO schema_migrations (version) VALUES ('023') ON CONFLICT DO NOTHING;
