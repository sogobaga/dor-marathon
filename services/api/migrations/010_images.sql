-- Migration 010: 圖片上傳儲存（存 Postgres bytea）
-- 依賴：009_brochure.sql

CREATE TABLE IF NOT EXISTS images (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mime       VARCHAR(50) NOT NULL,
    data       BYTEA NOT NULL,
    size       INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('010') ON CONFLICT DO NOTHING;
