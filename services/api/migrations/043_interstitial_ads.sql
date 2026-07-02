-- Migration 043: 蓋板廣告（拍立得卡片堆疊）。多張卡片、可排序；總開關存 app_settings.interstitial_enabled。
CREATE TABLE IF NOT EXISTS interstitial_ads (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order  INT NOT NULL DEFAULT 0,
    image_url   TEXT NOT NULL DEFAULT '',
    headline    TEXT NOT NULL DEFAULT '', -- 標語（照片下方大字）
    description TEXT NOT NULL DEFAULT '', -- 描述（標語下方小字，可留白）
    cta_label   TEXT NOT NULL DEFAULT '', -- CTA 文字（如「了解更多」）
    cta_url     TEXT NOT NULL DEFAULT '', -- CTA 連結（內部路徑或外部網址；留白＝按了只關閉）
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_interstitial_ads_order ON interstitial_ads (sort_order, created_at);

-- 總開關（預設關閉，避免設定好之前就意外彈出）
INSERT INTO app_settings (key, value) VALUES ('interstitial_enabled', '0') ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('043') ON CONFLICT DO NOTHING;
