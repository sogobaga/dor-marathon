-- Migration 022: 全站外觀設定（單例）— 會員資訊面板底圖
CREATE TABLE IF NOT EXISTS site_settings (
    id                  BOOLEAN PRIMARY KEY DEFAULT TRUE,
    member_panel_bg_url TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT site_settings_singleton CHECK (id)
);

INSERT INTO site_settings (id) VALUES (TRUE) ON CONFLICT DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('022') ON CONFLICT DO NOTHING;
