-- Migration 042: 通用系統設定（key-value 單表），供後台「系統設定」頁調教。
-- 首批：事件任務的隨機等待區間（取代原本寫死的 15 分鐘冷卻）。
CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO app_settings (key, value) VALUES
    ('event_wait_min_sec', '300'),
    ('event_wait_max_sec', '900')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('042') ON CONFLICT DO NOTHING;
