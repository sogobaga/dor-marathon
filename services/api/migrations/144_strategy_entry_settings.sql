-- Migration 144: 賽事策略入口可見性預設（開發期入口白名單，預設僅開放測試帳號 sogobaga@gmail.com）
INSERT INTO app_settings (key, value) VALUES
  ('strategy_entry_state', 'whitelist'),
  ('strategy_entry_whitelist', 'sogobaga@gmail.com')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('144') ON CONFLICT DO NOTHING;
