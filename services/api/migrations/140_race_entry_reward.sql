-- 參賽虛擬獎勵：races.entry_reward_config 沿用即時獎勵 RewardConfig JSONB 結構；
-- race_entry_reward_grants 為「每人每賽事發一次」的冪等閘門（CAS INSERT，比照 race_task_completions）。
ALTER TABLE races ADD COLUMN IF NOT EXISTS entry_reward_config JSONB;
CREATE TABLE IF NOT EXISTS race_entry_reward_grants (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id    UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (race_id, user_id)
);
INSERT INTO schema_migrations (version) VALUES ('140') ON CONFLICT DO NOTHING;
