-- Migration 100: 環台大富翁入口可見性預設（測試中：僅白名單 sogobaga@gmail.com 可見）
INSERT INTO app_settings (key, value) VALUES
  ('monopoly_entry_state', 'whitelist'),
  ('monopoly_entry_whitelist', 'sogobaga@gmail.com')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('100') ON CONFLICT DO NOTHING;
