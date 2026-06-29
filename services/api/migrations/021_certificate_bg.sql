-- Migration 021: 賽事完賽證明底圖（後台可針對該賽事上傳；空=用預設證書設計）
ALTER TABLE races ADD COLUMN IF NOT EXISTS certificate_bg_url TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('021') ON CONFLICT DO NOTHING;
