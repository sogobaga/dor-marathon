-- Migration 107: 完賽公仔兌換 A 後端 —— 兌換申請表
-- 依賴：106_monopoly_user_stickers.sql（monopoly_user_stickers 九宮格擁有表，集滿 9 片才能申請兌換）
--
-- 每位使用者集滿完賽公仔九宮格貼紙後，可申請兌換實體公仔；每人限一筆申請（user_id UNIQUE），
-- 後台審核後把狀態改成 fulfilled（已出貨）或 rejected（駁回），note 供後台填備註（如物流單號/駁回原因）。

CREATE TABLE IF NOT EXISTS monopoly_figure_redemptions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    status     VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | fulfilled | rejected
    note       VARCHAR(200) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_monopoly_figure_redemptions_status_created
    ON monopoly_figure_redemptions(status, created_at);

-- 兌換頁引導設定（LINE 官方帳號／兌換表單網址），供 GET /monopoly/stickers 與後台 figure-settings 讀寫。
INSERT INTO app_settings (key, value) VALUES
  ('monopoly_figure_line_oa', '@855xfwqe'),
  ('monopoly_figure_landing_url', 'https://runner-figure.bravelog.tw/')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('107') ON CONFLICT DO NOTHING;
