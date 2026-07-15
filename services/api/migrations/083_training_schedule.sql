-- Migration 083: 自主訓練 P2 —— 每日訓練排程（每人每日一份課表）
CREATE TABLE IF NOT EXISTS user_training_schedule (
  user_id        UUID NOT NULL,
  scheduled_date DATE NOT NULL,
  template_code  TEXT NOT NULL,          -- 對應 workout_templates.code（前端用來取 segments 解析）
  pace_level     INT  NOT NULL,          -- 對應 pace_levels.id
  name           TEXT NOT NULL DEFAULT '', -- 顯示用快照
  category       TEXT NOT NULL DEFAULT '',
  planned_km     NUMERIC NOT NULL DEFAULT 0, -- 預計里程/時間快照（存檔時由前端依 template×level 算好）
  planned_min    INT NOT NULL DEFAULT 0,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, scheduled_date)
);
CREATE INDEX IF NOT EXISTS idx_uts_user_date ON user_training_schedule(user_id, scheduled_date);

INSERT INTO schema_migrations (version) VALUES ('083') ON CONFLICT DO NOTHING;
