-- Migration 146：虛擬選手（後台可建立的機器人跑者，用於賽事名額造勢/測試）。
-- 一次做三件事：
--   1. users.is_virtual：標記機器人帳號。虛擬選手＝users(is_virtual=TRUE) 的特殊帳號——不建
--      user_identities（provider/provider_uid 天然缺席），因此永遠無法透過任何登入方式登入，
--      這是刻意的「無法登入」設計，不是漏洞。password_hash 沿用 003_events.sql 已放寬的可空欄位。
--   2. vr_level_presets：8 級能力值範本（配速/週跑量的參考值），供後台建立/批次產生虛擬選手時
--      帶入初始能力值（±5% 抖動，見 internal/virtualrunner）。level 為 PK，字串代碼，供
--      virtual_runners.level 外鍵參照與前端下拉選單 value 用；sort_order 供前端由易到難排序。
--      pace_fast_s/pace_slow_s 單位為「秒/公里」，fast < slow（fast 是配速較快、數字較小的那端）。
--   3. virtual_runners：1:1 掛在 users 之下的虛擬選手參數列（能力值/城市/活躍時段/勤勞度），
--      user_id 即 users.id，ON DELETE CASCADE——刪除 users 那筆（見 internal/virtualrunner
--      Repository.DeleteRunner）會一併帶走本表列。level 外鍵參照 vr_level_presets，供後台調整
--      「這位虛擬選手屬於哪個能力等級」；能力值欄位（avg_km/monthly_km/pace_fast_s/pace_slow_s）
--      複製一份到本表（而非每次都 JOIN presets 現算），因為建立當下已加了 ±5% 個體抖動，且後台
--      可對單一選手覆寫調整，不該再跟著 presets 之後的調整連動（PUT /presets/{level} 明確不回溯
--      已建選手，見套件文件）。city/window_hour 白名單見 internal/virtualrunner/model.go，本表故意
--      不加 DB CHECK 約束——比照全站慣例（gender/audience 等枚舉欄位一律只在 Go 層驗證，SQL 只留
--      註解列出合法值，DB 層不設 CHECK），驗證邏輯集中一處、schema 保持單純。
--
-- 不建索引在 virtual_runners.level／city／enabled：本表預期規模是後台人工/批次建立的機器人數量
-- （百至千級），非高頻查詢的大表，AdminList 全表掃描即可，比照 internal/partner 慣例不過早優化。

-- 1. users.is_virtual + partial index（列出「所有虛擬選手」用；比照 partial index 只索引少數符合
--    條件的列，一般會員不進索引，維持索引精簡）
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_virtual BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_users_is_virtual ON users(id) WHERE is_virtual;

-- 2. 等級範本（8 級，由易到難）
CREATE TABLE IF NOT EXISTS vr_level_presets (
    level        VARCHAR(20) PRIMARY KEY,
    label        VARCHAR(30) NOT NULL,
    sort_order   INT NOT NULL DEFAULT 0,
    avg_km       NUMERIC(5,2) NOT NULL,   -- 單次平均跑量（km）
    monthly_km   NUMERIC(6,1) NOT NULL,   -- 月跑量（km）
    pace_fast_s  INT NOT NULL,            -- 配速下限（秒/km，數字較小＝較快）
    pace_slow_s  INT NOT NULL             -- 配速上限（秒/km，數字較大＝較慢；恆 > pace_fast_s）
);

INSERT INTO vr_level_presets (level, label, sort_order, avg_km, monthly_km, pace_fast_s, pace_slow_s) VALUES
    ('beginner',        '初跑者',   1, 3,  60,  480, 510),
    ('citizen',         '市民跑者', 2, 5,  90,  420, 450),
    ('advanced',        '準跑者',   3, 7,  120, 390, 420),
    ('half_challenger',  '半馬挑戰者', 4, 9,  150, 360, 390),
    ('half_finisher',   '半馬選手', 5, 11, 190, 330, 360),
    ('full_challenger', '全馬挑戰者', 6, 13, 230, 300, 340),
    ('full_finisher',   '全馬選手', 7, 16, 280, 280, 310),
    ('elite',           '菁英選手', 8, 19, 340, 240, 280)
ON CONFLICT (level) DO NOTHING;

-- 3. 虛擬選手參數（1:1 掛在 users 之下）
CREATE TABLE IF NOT EXISTS virtual_runners (
    user_id            UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    level              VARCHAR(20) NOT NULL REFERENCES vr_level_presets(level),
    diligence          INT NOT NULL DEFAULT 3,      -- 勤勞度 1-5（Go 層驗證，見 model.go）
    city               VARCHAR(12),                 -- taipei|new_taipei|taoyuan|hsinchu|taichung|tainan|kaohsiung
    window_hour        INT,                         -- 活躍時段起始時（24hr）：4|5|6|19|20|21|22
    avg_km             NUMERIC(5,2) NOT NULL,
    monthly_km         NUMERIC(6,1) NOT NULL,
    pace_fast_s        INT NOT NULL,
    pace_slow_s        INT NOT NULL,
    enabled            BOOLEAN NOT NULL DEFAULT TRUE,
    last_generated_at  TIMESTAMPTZ,                 -- 最後一次自動產生活動的時間（Phase 2 用，本輪只存欄位）
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_virtual_runners_level ON virtual_runners(level);

INSERT INTO schema_migrations (version) VALUES ('146') ON CONFLICT DO NOTHING;
