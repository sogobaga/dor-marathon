-- Migration 038: 事件任務時段插圖 — 白天/黃昏/晚上各自可上不同圖，前台依跑者當下時間顯示對應圖。
-- 白天 06:00–17:00、黃昏 17:00–19:00、晚上 19:00–06:00。未設定該時段則回退到 image_url（預設圖）。
ALTER TABLE event_task_defs ADD COLUMN IF NOT EXISTS image_day_url   TEXT NOT NULL DEFAULT '';
ALTER TABLE event_task_defs ADD COLUMN IF NOT EXISTS image_dusk_url  TEXT NOT NULL DEFAULT '';
ALTER TABLE event_task_defs ADD COLUMN IF NOT EXISTS image_night_url TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('038') ON CONFLICT DO NOTHING;
