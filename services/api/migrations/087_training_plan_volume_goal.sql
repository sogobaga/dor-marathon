-- Migration 087: 訓練計畫加「目前月跑量」與「目標完賽時間」
-- monthly_km：使用者目前的月跑量(公里)。0=未填(沿用舊行為：長跑上限只依賽事距離)。
--   產生器據此推出週跑量基準(月跑量/4)，再以「當週跑量的 40%」約束 LSD 起始長度與上限——
--   單次跑過 21K ≠ 每週都能吃 21K，後者靠的是月跑量堆出來的耐受度。
-- goal_time_s：目標完賽秒數(如全馬 4:30:00 = 16200)。0=未設定。
--   只用於「目標配速」顯示/配速跑 + 用 Riegel 從 1K PB 推估的可行性提示；
--   訓練配速一律仍依「目前體能」(pace_level)，不因目標過激而整體拉快 → 避免新手天天超練受傷。
ALTER TABLE training_plans ADD COLUMN IF NOT EXISTS monthly_km  INT NOT NULL DEFAULT 0;
ALTER TABLE training_plans ADD COLUMN IF NOT EXISTS goal_time_s INT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version) VALUES ('087') ON CONFLICT DO NOTHING;
