-- Migration 036: 事件任務可放圖片（前台橫幅顯示、後台上傳）→ 增加沉浸感（如「狗群追來」的插圖）
ALTER TABLE event_task_defs ADD COLUMN IF NOT EXISTS image_url TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('036') ON CONFLICT DO NOTHING;
