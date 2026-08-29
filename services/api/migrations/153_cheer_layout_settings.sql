-- Migration 153: 啦啦隊角色位置校正 —— 校正模式入口白名單 + 位置校正值（預設置中、不偏移、原尺寸）
INSERT INTO app_settings (key, value) VALUES
  ('cheer_edit_entry_state', 'whitelist'),
  ('cheer_edit_entry_whitelist', 'sogobaga@gmail.com'),
  ('cheer_char_layout', '{"01":{"dx":0,"dy":0,"scale":1},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1}}')
ON CONFLICT (key) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('153') ON CONFLICT DO NOTHING;
