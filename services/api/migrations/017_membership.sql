-- Migration 017: 會員中心（帳號編碼 + 等級/EXP + VIP）
-- 依賴：016_fix_activity_unique.sql
-- 本輪只做資料層 + 顯示/後台設定；訂閱付費與 EXP 結算為後續輪。

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS account_code   VARCHAR(12) UNIQUE,  -- 帳號專屬編碼（好友機制基礎）；app lazy 產生
    ADD COLUMN IF NOT EXISTS exp            INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS vip_expires_at TIMESTAMPTZ;         -- NULL/過去=非VIP；未來=VIP（之後訂閱延續此日期）

-- 等級門檻（後台可設）：達到該等級所需「累積 EXP」；等級由 exp 推導、不另存避免不同步
CREATE TABLE IF NOT EXISTS level_config (
    level        INT PRIMARY KEY,
    title        VARCHAR(50),
    exp_required INT NOT NULL
);
INSERT INTO level_config (level, title, exp_required) VALUES
    (1, '新手', 0),
    (2, '入門', 100),
    (3, '進階', 250),
    (4, '資深', 500),
    (5, '菁英', 1000)
ON CONFLICT DO NOTHING;

-- EXP 規則（單列，後台可設）：每場參賽 / 每個任務達成的 EXP（earning 邏輯後續輪才接）
CREATE TABLE IF NOT EXISTS exp_rules (
    id       BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    per_race INT NOT NULL DEFAULT 50,
    per_task INT NOT NULL DEFAULT 20
);
INSERT INTO exp_rules (id) VALUES (TRUE) ON CONFLICT DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('017') ON CONFLICT DO NOTHING;
