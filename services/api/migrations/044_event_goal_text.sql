-- Migration 044: 事件任務可自訂「任務目標」說明文字（留空＝用系統依完成條件自動產生的文字，防呆）。
ALTER TABLE event_task_defs ADD COLUMN IF NOT EXISTS goal_text TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('044') ON CONFLICT DO NOTHING;
