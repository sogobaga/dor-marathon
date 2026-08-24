-- Migration 143: 賽事策略（配速計劃＋補給計劃）——自主訓練新分頁；開跑時帶 /track?strategy=<id>
-- 進入「比賽專注模式」。依賴：001_init.sql（users）。
-- segments：分段目標配速 [{from_km,to_km,pace_s}]，首段 from_km=0、段間連續銜接遞增（後端驗證，見
-- internal/training/strategies.go validateStrategy）。fuel：補給提醒點 [{kind,mode,at}]。
-- total_km 為冗餘欄位＝segments 最後一段 to_km，由後端計算寫入（不信前端），供列表顯示與 ETA 計算用。

CREATE TABLE IF NOT EXISTS user_race_strategies (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name VARCHAR(50) NOT NULL,
  total_km NUMERIC(6,2),
  segments JSONB NOT NULL DEFAULT '[]'::jsonb,
  fuel JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_race_strategies_user ON user_race_strategies(user_id, created_at DESC);

INSERT INTO schema_migrations (version) VALUES ('143') ON CONFLICT DO NOTHING;
