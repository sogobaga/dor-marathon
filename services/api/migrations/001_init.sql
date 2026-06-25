-- DOR 初始 Schema
-- 執行順序：001_init.sql

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- 使用者
CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           VARCHAR(255) UNIQUE NOT NULL,
    handle          VARCHAR(50) UNIQUE NOT NULL,
    name            VARCHAR(100) NOT NULL,
    password_hash   TEXT NOT NULL,
    avatar_url      TEXT,
    total_km        DECIMAL(8,2) NOT NULL DEFAULT 0,
    role            VARCHAR(20) NOT NULL DEFAULT 'user',  -- user | admin
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 賽事
CREATE TABLE IF NOT EXISTS races (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            VARCHAR(50) UNIQUE NOT NULL,
    title           VARCHAR(100) NOT NULL,
    subtitle        VARCHAR(100),
    world           VARCHAR(100),
    blurb           TEXT,
    hero_image_url  TEXT,
    status          VARCHAR(10) NOT NULL DEFAULT 'soon',  -- soon|open|live|done
    distances       INT[] NOT NULL,
    group_type      VARCHAR(10) NOT NULL DEFAULT 'distance', -- faction|club|distance
    group_mode      VARCHAR(10) NOT NULL DEFAULT 'self',     -- random|self
    slots_total     INT NOT NULL DEFAULT 0,
    entry_fee       INT NOT NULL DEFAULT 0,               -- 分（NT$ × 100）
    start_date      DATE NOT NULL,
    end_date        DATE NOT NULL,
    config          JSONB NOT NULL DEFAULT '{}',          -- factions, clubs, mission days
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 報名
CREATE TABLE IF NOT EXISTS registrations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    race_id         UUID NOT NULL REFERENCES races(id),
    distance        INT NOT NULL,
    faction         VARCHAR(30),                          -- 分配陣營 ID
    team_id         UUID,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending|paid|cancelled
    paid_at         TIMESTAMPTZ,
    amount          INT NOT NULL DEFAULT 0,
    payment_ref     VARCHAR(100),                         -- 金流訂單號
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, race_id)
);

CREATE INDEX IF NOT EXISTS idx_registrations_race ON registrations(race_id);
CREATE INDEX IF NOT EXISTS idx_registrations_user ON registrations(user_id);

-- 跑步活動（按月份 range partition 設計，此處為母表）
CREATE TABLE IF NOT EXISTS activities (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    race_id         UUID REFERENCES races(id),
    mission_day     INT,
    distance_km     DECIMAL(6,3) NOT NULL,
    duration_s      INT NOT NULL,                         -- 總秒數
    avg_pace_s      INT NOT NULL,                         -- 秒/公里
    max_pace_s      INT,
    gps_point_count INT,
    recorded_at     TIMESTAMPTZ NOT NULL,
    processed       BOOLEAN NOT NULL DEFAULT FALSE,       -- Worker 處理完標記
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_activities_user_race   ON activities(user_id, race_id);
CREATE INDEX IF NOT EXISTS idx_activities_race_rec    ON activities(race_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_activities_unprocessed ON activities(processed) WHERE processed = FALSE;

-- 每日任務完成記錄
CREATE TABLE IF NOT EXISTS mission_completions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    race_id         UUID NOT NULL REFERENCES races(id),
    day             INT NOT NULL,
    activity_id     UUID REFERENCES activities(id),
    rescue_count    INT NOT NULL DEFAULT 0,
    completed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, race_id, day)
);

-- 門市
CREATE TABLE IF NOT EXISTS stores (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) NOT NULL,
    address     TEXT NOT NULL,
    city        VARCHAR(30),
    lat         DECIMAL(9,6),
    lng         DECIMAL(9,6),
    hours       VARCHAR(50),
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 門市打卡
CREATE TABLE IF NOT EXISTS checkins (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    store_id    UUID NOT NULL REFERENCES stores(id),
    race_id     UUID REFERENCES races(id),
    stamp_earned BOOLEAN NOT NULL DEFAULT FALSE,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_checkins_user_race ON checkins(user_id, race_id);

-- 轉盤抽獎記錄
CREATE TABLE IF NOT EXISTS wheel_spins (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    race_id         UUID NOT NULL REFERENCES races(id),
    result_id       VARCHAR(30) NOT NULL,
    result_kind     VARCHAR(20) NOT NULL,                 -- line|sticker|again|miss
    result_amount   INT NOT NULL DEFAULT 0,
    spun_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 集點卡（九宮格）
CREATE TABLE IF NOT EXISTS user_stickers (
    user_id     UUID NOT NULL REFERENCES users(id),
    sticker_no  INT NOT NULL CHECK (sticker_no BETWEEN 1 AND 9),
    race_id     UUID NOT NULL REFERENCES races(id),
    earned_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, sticker_no, race_id)
);

-- 審計日誌
CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID,
    action      VARCHAR(100) NOT NULL,
    resource    VARCHAR(50),
    resource_id UUID,
    meta        JSONB,
    ip          VARCHAR(45),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_user ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC);

-- Schema versions (for migration tracking)
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     VARCHAR(14) PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('001') ON CONFLICT DO NOTHING;
