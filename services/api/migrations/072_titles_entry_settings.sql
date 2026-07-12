-- Migration 072: 稱號/成就入口可見性預設（測試中：僅白名單 sogobaga@gmail.com 可見）
INSERT INTO app_settings (key, value) VALUES
  ('title_entry_state', 'whitelist'),
  ('title_entry_whitelist', 'sogobaga@gmail.com'),
  ('achievement_entry_state', 'whitelist'),
  ('achievement_entry_whitelist', 'sogobaga@gmail.com')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('072') ON CONFLICT DO NOTHING;
