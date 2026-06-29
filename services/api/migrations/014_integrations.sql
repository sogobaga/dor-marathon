-- Migration 014: 第三方運動數據整合（先接 Strava，可擴充其他 provider）
-- 依賴：013_race_tasks.sql
-- user_integrations：每位使用者對各 provider 的 OAuth token
-- activities 擴充：爬升海拔、平均心率、來源與外部 id（去重）

CREATE TABLE IF NOT EXISTS user_integrations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         VARCHAR(20) NOT NULL,            -- strava | coros | ...
    provider_user_id VARCHAR(64) NOT NULL,            -- 例：Strava athlete id（webhook owner_id 對應）
    access_token     TEXT NOT NULL,
    refresh_token    TEXT NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    scope            TEXT,
    athlete_name     VARCHAR(200),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, provider)
);
CREATE INDEX IF NOT EXISTS idx_user_integrations_provider_uid ON user_integrations(provider, provider_user_id);

ALTER TABLE activities
    ADD COLUMN IF NOT EXISTS ascent_m    DECIMAL(8,2),       -- 爬升海拔（公尺）
    ADD COLUMN IF NOT EXISTS avg_hr      INT,                -- 平均心率（bpm）
    ADD COLUMN IF NOT EXISTS source      VARCHAR(20),        -- manual | strava | ...
    ADD COLUMN IF NOT EXISTS external_id VARCHAR(64);        -- provider 活動 id（去重）

-- 同來源同外部 id 不重複匯入（多次 webhook / 回填皆冪等）
CREATE UNIQUE INDEX IF NOT EXISTS idx_activities_source_ext
    ON activities(source, external_id)
    WHERE source IS NOT NULL AND external_id IS NOT NULL;

INSERT INTO schema_migrations (version) VALUES ('014') ON CONFLICT DO NOTHING;
