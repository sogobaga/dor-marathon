-- Migration 009: 賽事簡章（主標 + 內容區塊）
-- 依賴：008_race_datetime.sql

ALTER TABLE races ADD COLUMN IF NOT EXISTS brochure_title VARCHAR(200);

CREATE TABLE IF NOT EXISTS race_brochure_blocks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id       UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    block_type    VARCHAR(10) NOT NULL,   -- text | image | video
    content       TEXT NOT NULL,          -- text: HTML；image: 圖片URL；video: YouTube 連結/ID
    caption       TEXT,                   -- 圖片/影片說明（選填）
    display_order INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_brochure_race ON race_brochure_blocks(race_id);

INSERT INTO schema_migrations (version) VALUES ('009') ON CONFLICT DO NOTHING;
