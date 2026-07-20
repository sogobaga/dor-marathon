-- Migration 091: 跑者充電站（合作商家目錄）
-- 依賴：001_init.sql（users）

CREATE TABLE IF NOT EXISTS partner_shops (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(200) NOT NULL,
  summary VARCHAR(300) NOT NULL DEFAULT '',
  banner_url TEXT NOT NULL DEFAULT '',
  detail_html TEXT NOT NULL DEFAULT '',
  photo_urls JSONB NOT NULL DEFAULT '[]'::jsonb,
  video_url TEXT NOT NULL DEFAULT '',
  cta_url TEXT NOT NULL DEFAULT '',
  cta_label VARCHAR(50) NOT NULL DEFAULT '',
  display_order INT NOT NULL DEFAULT 0,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_partner_shops_order ON partner_shops(display_order, created_at);

CREATE TABLE IF NOT EXISTS partner_shop_favorites (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  shop_id UUID NOT NULL REFERENCES partner_shops(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_id, shop_id)
);
CREATE INDEX IF NOT EXISTS idx_partner_shop_favorites_shop ON partner_shop_favorites(shop_id);

INSERT INTO schema_migrations (version) VALUES ('091') ON CONFLICT DO NOTHING;
