-- Migration 089: 訓練計畫加「保守/積極」模式
-- conservative（預設）：多一些保護、降一些強度，但仍是有機會完成目標的強度。
--   賽前一週不排強度課（頂多維持跑感），賽前一天直接休息不排課。
-- aggressive：賽前一週維持強度但降距離，賽前一天排維持跑感的輕鬆跑；成長曲線更陡，衝成績用。
--
-- 為何預設 conservative：能自行微調課表的是資深跑者（他們會主動選 aggressive）；
-- 不會微調的是新手，預設就該替他們把休息與恢復排好、避免受傷。
ALTER TABLE training_plans ADD COLUMN IF NOT EXISTS plan_mode TEXT NOT NULL DEFAULT 'conservative';

-- 既有計畫回填為 aggressive：它們是在本模式存在之前、用現在等同於 aggressive 的參數產生的
-- （成長上限 1.8、每週增幅 10%、賽前 3 天 shakeout、賽前一週不限制強度）。若讓 DEFAULT 把它們
-- 標成「保守」，計畫卡會顯示一個與實際排課不符的標籤——寧可標成比較接近事實的那個。
-- 只回填這一次；之後每筆 INSERT 都由後端明確帶入 plan_mode，不依賴 DEFAULT。
UPDATE training_plans SET plan_mode = 'aggressive' WHERE plan_mode = 'conservative';

INSERT INTO schema_migrations (version) VALUES ('089') ON CONFLICT DO NOTHING;
