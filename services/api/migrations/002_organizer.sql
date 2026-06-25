-- Migration 002: 合作方角色體系
-- 依賴：001_init.sql

-- 1. users.role 已存在（DEFAULT 'user'），新增可用值說明：
--    'user'       — 一般參賽者（預設）
--    'organizer'  — 外部賽事合作方
--    'admin'      — 平台管理員（聚澤）

-- 2. 合作方 Profile（擴充 users 的商業資訊）
CREATE TABLE IF NOT EXISTS organizer_profiles (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    company_name    VARCHAR(100) NOT NULL,
    contact_name    VARCHAR(50),
    contact_email   VARCHAR(255),
    contact_phone   VARCHAR(20),
    website         TEXT,
    description     TEXT,
    verified        BOOLEAN NOT NULL DEFAULT FALSE,   -- 平台審核通過後才能提交賽事
    verified_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 3. 賽事加入審核流程欄位
ALTER TABLE races
    ADD COLUMN IF NOT EXISTS review_status VARCHAR(20) NOT NULL DEFAULT 'approved',
    ADD COLUMN IF NOT EXISTS review_note   TEXT,
    ADD COLUMN IF NOT EXISTS reviewed_by   UUID REFERENCES users(id),
    ADD COLUMN IF NOT EXISTS reviewed_at   TIMESTAMPTZ;

-- review_status 可能值：
--   'approved'       — 已核准（admin 自建賽事預設，或審核通過）
--   'pending'        — 合作方提交，待平台審核
--   'rejected'       — 退回，附原因

-- 確保 created_by 是 organizer 可查詢的關鍵欄位
CREATE INDEX IF NOT EXISTS idx_races_created_by ON races(created_by);
CREATE INDEX IF NOT EXISTS idx_races_review_status ON races(review_status);

-- 4. 初始化一個平台 admin 帳號（密碼請在部署時用 bcrypt hash 替換）
-- INSERT INTO users (email, handle, name, password_hash, role)
-- VALUES ('admin@dor.tw', 'dor_admin', 'DOR 管理員', '$2a$10$REPLACE_WITH_REAL_HASH', 'admin')
-- ON CONFLICT DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('002') ON CONFLICT DO NOTHING;
