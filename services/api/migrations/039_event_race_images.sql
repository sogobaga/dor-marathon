-- Migration 039: 賽事多人事件也支援時段插圖（白天/黃昏/晚上 + 預設圖），與 Phase A 一致。
-- 前台邀請橫幅與進行中橫幅依跑者當下時間顯示對應時段圖，未設定回退 image_url。
ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS image_url       TEXT NOT NULL DEFAULT '';
ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS image_day_url   TEXT NOT NULL DEFAULT '';
ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS image_dusk_url  TEXT NOT NULL DEFAULT '';
ALTER TABLE event_race_defs ADD COLUMN IF NOT EXISTS image_night_url TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('039') ON CONFLICT DO NOTHING;
