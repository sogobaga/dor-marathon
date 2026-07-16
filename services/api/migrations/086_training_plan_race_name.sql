-- Migration 086: 訓練計畫加「使用者自填的目標賽事名稱」
-- 顯示優先序：race_name（使用者自填，如「台北馬拉松」）> name（自動命名，如「23週·全馬」）
ALTER TABLE training_plans ADD COLUMN IF NOT EXISTS race_name TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('086') ON CONFLICT DO NOTHING;
