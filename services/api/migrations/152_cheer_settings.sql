-- Migration 152: 每公里應援設定 —— 測試觸發按鈕入口（開發期白名單）+ 應援表演顯示毫秒數
INSERT INTO app_settings (key, value) VALUES
  ('cheer_test_entry_state', 'whitelist'),
  ('cheer_test_entry_whitelist', 'sogobaga@gmail.com'),
  ('cheer_display_ms', '3000')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('152') ON CONFLICT DO NOTHING;
