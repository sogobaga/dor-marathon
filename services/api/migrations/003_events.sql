-- Migration 003: 賽事系統正規化（三模式 + 分組 + 加購 + 物資 + 會員個資 + 訂單）
-- 依賴：001_init.sql、002_organizer.sql
--
-- 設計目標：單場破萬人。分組/加購/物資全部正規化成關聯表，
-- 取代原本塞在 races.config JSONB / distances[] / registrations.faction 的原型結構。
-- 舊欄位保留供相容，新流程改用本檔新增的關聯表。

-- ─────────────────────────────────────────────
-- 1. 會員 / 個人資訊 / OAuth
-- ─────────────────────────────────────────────

-- Google-only 帳號沒有密碼 → password_hash 改為可空
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- 個人資訊（1:1，PII 與 users 分離；報名時自動帶入 / 回填）
CREATE TABLE IF NOT EXISTS user_profiles (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    real_name  VARCHAR(100),                 -- 真實名稱
    nickname   VARCHAR(50),                  -- 暱稱
    phone      VARCHAR(20),
    address    TEXT,
    birthday   DATE,
    gender     VARCHAR(10),                  -- male | female | other | NULL（未填）
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- email 沿用 users.email（不重複存）

-- OAuth 身分（為 Google 登入預留；本輪不串接但表先建）
CREATE TABLE IF NOT EXISTS user_identities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider     VARCHAR(20) NOT NULL,        -- google
    provider_uid VARCHAR(255) NOT NULL,       -- Google sub
    email        VARCHAR(255),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, provider_uid)
);
CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id);

-- ─────────────────────────────────────────────
-- 2. 賽事擴充欄位
-- ─────────────────────────────────────────────

ALTER TABLE races
    ADD COLUMN IF NOT EXISTS event_mode         VARCHAR(20) NOT NULL DEFAULT 'general',
        -- general（一般） | competition（競賽） | faction_battle（分組對抗）
    ADD COLUMN IF NOT EXISTS registration_start TIMESTAMPTZ,   -- 報名開始
    ADD COLUMN IF NOT EXISTS registration_end   TIMESTAMPTZ,   -- 報名截止
    ADD COLUMN IF NOT EXISTS goal_type          VARCHAR(20) NOT NULL DEFAULT 'distance';
        -- 競賽完賽目標：cumulative（各分組總累積里程） | distance（指定完成里程）
-- start_date / end_date 沿用為「競賽時間」；blurb 沿用為「賽事說明」

-- ─────────────────────────────────────────────
-- 3. 分組（核心正規化表）
-- ─────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS race_groups (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id             UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    name                VARCHAR(100) NOT NULL,   -- 自訂，例：全馬組 / 紅隊
    description         TEXT,
    display_order       INT NOT NULL DEFAULT 0,
    slot_limit          INT,                     -- 該分組人數限制，NULL=不限
    slots_taken         INT NOT NULL DEFAULT 0,  -- 計數器，報名交易內 +1（破萬人併發防超賣）
    gender_limit        VARCHAR(10) NOT NULL DEFAULT 'any',  -- any | male | female
    age_min             INT,                     -- 年齡限制（NULL=不限）
    age_max             INT,
    target_distance_km  DECIMAL(8,2),            -- 完賽目標里程
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_race_groups_race ON race_groups(race_id);

-- 分組預設選單（可擴充）：後台新增分組時的下拉選項
CREATE TABLE IF NOT EXISTS group_presets (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(100) NOT NULL UNIQUE,
    default_distance_km DECIMAL(8,2),
    is_system           BOOLEAN NOT NULL DEFAULT FALSE,  -- 系統內建 vs 後台新增
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO group_presets (name, default_distance_km, is_system) VALUES
    ('全馬組', 42.20, TRUE),
    ('半馬組', 21.10, TRUE),
    ('10K 歡樂組', 10.00, TRUE),
    ('5K 體驗組', 5.00, TRUE)
ON CONFLICT (name) DO NOTHING;

-- ─────────────────────────────────────────────
-- 4. 加購項目
-- ─────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS race_addons (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id        UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    name           VARCHAR(100) NOT NULL,
    description    TEXT,
    image_url      TEXT,                         -- 照片
    price_cents    INT NOT NULL DEFAULT 0,
    per_user_limit INT,                          -- 個人限購數量，NULL=不限
    total_stock    INT,                          -- 總銷售數量，NULL=不限
    sold_count     INT NOT NULL DEFAULT 0,
    display_order  INT NOT NULL DEFAULT 0,
    active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_race_addons_race ON race_addons(race_id);

-- ─────────────────────────────────────────────
-- 5. 物資（一張表涵蓋 共用×分組 與 參賽×完賽）
-- ─────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS race_supplies (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id       UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    group_id      UUID REFERENCES race_groups(id) ON DELETE CASCADE,
                  -- NULL=賽事層級共用；非 NULL=該分組專屬
    kind          VARCHAR(20) NOT NULL,          -- race_pack（參賽物資） | finisher（完賽物資）
    name          VARCHAR(100) NOT NULL,
    description   TEXT,
    image_url     TEXT,
    display_order INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_race_supplies_race ON race_supplies(race_id, group_id, kind);

-- ─────────────────────────────────────────────
-- 6. 報名改造（為下一輪前台報名預備）
-- ─────────────────────────────────────────────

ALTER TABLE registrations
    ADD COLUMN IF NOT EXISTS group_id        UUID REFERENCES race_groups(id),
    ADD COLUMN IF NOT EXISTS group_revealed  BOOLEAN NOT NULL DEFAULT FALSE,  -- 分組對抗：賽前隱藏
    ADD COLUMN IF NOT EXISTS snap_real_name  VARCHAR(100),                    -- 報名當下個資快照
    ADD COLUMN IF NOT EXISTS snap_phone      VARCHAR(20),
    ADD COLUMN IF NOT EXISTS snap_address    TEXT;
CREATE INDEX IF NOT EXISTS idx_registrations_group ON registrations(group_id);
-- 舊欄位 distance / faction 保留供相容，新流程改用 group_id

-- ─────────────────────────────────────────────
-- 7. 訂單（金流預留，本輪不接金流）
-- ─────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS orders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    race_id         UUID NOT NULL REFERENCES races(id),
    registration_id UUID REFERENCES registrations(id),
    total_cents     INT NOT NULL DEFAULT 0,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending|paid|cancelled|refunded
    payment_ref     VARCHAR(100),
    paid_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_race ON orders(race_id);

CREATE TABLE IF NOT EXISTS order_items (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    item_type        VARCHAR(20) NOT NULL,        -- entry（報名費） | addon（加購）
    addon_id         UUID REFERENCES race_addons(id),
    qty              INT NOT NULL DEFAULT 1,
    unit_price_cents INT NOT NULL DEFAULT 0,
    subtotal_cents   INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id);

-- ─────────────────────────────────────────────
-- 8. 競賽排行榜預聚合（破萬人關鍵；本輪只建表，worker 之後填）
-- ─────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS race_group_standings (
    race_id        UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    group_id       UUID NOT NULL REFERENCES race_groups(id) ON DELETE CASCADE,
    total_km       DECIMAL(12,2) NOT NULL DEFAULT 0,  -- 分組總累積里程
    member_count   INT NOT NULL DEFAULT 0,
    avg_km         DECIMAL(10,2) NOT NULL DEFAULT 0,  -- 平均里程
    avg_pace_s     INT NOT NULL DEFAULT 0,            -- 平均配速（秒/公里）
    finish_total_s BIGINT NOT NULL DEFAULT 0,         -- 完成指定里程的累計總時間
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (race_id, group_id)
);

INSERT INTO schema_migrations (version) VALUES ('003') ON CONFLICT DO NOTHING;
