-- Migration 054: 個人任務結構化課表（垂直切片）——分段課表 + 配速分級；移除肌力；休息日完成=3★（後端處理）。
-- 依賴：050/053。
-- segments：有序分段課表 JSONB。每段 {kind, label, target_type(distance|time), target(m或s), pace_fast_s, pace_slow_s, reps, rest_s}。
--   pace_fast_s=較快界(秒/公里，較小)、pace_slow_s=較慢界。work 段依配速達成度給星（全在區間3★/部分2★/只完成1★）。
-- workout_kind：課表型別（interval/aerobic/tempo/easy/recovery/progression/fartlek/pyramid/norwegian4x4...）；非空＝結構化課表，挑戰時帶到 GPS 追蹤跑。
-- pace_zones：每計畫(級別)的具名配速區（未來用；本切片配速直接寫在 segments）。

ALTER TABLE personal_tasks
  ADD COLUMN IF NOT EXISTS segments     JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS workout_kind TEXT  NOT NULL DEFAULT '';

ALTER TABLE personal_plans
  ADD COLUMN IF NOT EXISTS pace_zones JSONB NOT NULL DEFAULT '{}'::jsonb;

-- 移除肌力訓練（測不到）：停用既有肌力任務（鏈以 day 排序、跳過停用者，不影響前後銜接）
UPDATE personal_tasks SET enabled = FALSE WHERE workout_type LIKE '%肌力%';

-- 種子：示範計畫（stage_order=0 → 全域鏈最前，Day1 即可挑戰）＋ 400m 間歇課表（比照使用者範例）。
INSERT INTO personal_plans (code, name, lifecycle, stage_order, entry_note, data_source, enabled)
VALUES ('DEMO', '示範 · 間歇課表', '示範', 0, '垂直切片示範：挑戰後帶到「GPS 跑步追蹤」跑分段課表', 'gps', TRUE)
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, lifecycle=EXCLUDED.lifecycle, stage_order=EXCLUDED.stage_order,
  entry_note=EXCLUDED.entry_note, data_source=EXCLUDED.data_source, enabled=TRUE;

INSERT INTO personal_tasks (plan_id, day, week, seq, title, workout, workout_type, target_km, target_min, intensity,
  complete_cond, data_source, reward_exp, reward_dp, workout_kind, segments)
SELECT p.id, 1, 1, 1, '400m 間歇 ×6', '暖身 2K → 400m×6（間休 60s）→ 緩和 2K', '間歇', 6.4, 40, 'RPE7-8',
  '完成整份課表；400m 配速達成度決定星數', 'gps', 120, 24, 'interval',
  '[
    {"kind":"warmup","label":"暖身","target_type":"distance","target":2000,"pace_fast_s":450,"pace_slow_s":510,"reps":1,"rest_s":0},
    {"kind":"work","label":"400m 間歇","target_type":"distance","target":400,"pace_fast_s":330,"pace_slow_s":360,"reps":6,"rest_s":60},
    {"kind":"cooldown","label":"緩和","target_type":"distance","target":2000,"pace_fast_s":420,"pace_slow_s":480,"reps":1,"rest_s":0}
  ]'::jsonb
FROM personal_plans p WHERE p.code='DEMO'
ON CONFLICT (plan_id, day) DO UPDATE SET
  title=EXCLUDED.title, workout=EXCLUDED.workout, workout_type=EXCLUDED.workout_type, target_km=EXCLUDED.target_km,
  target_min=EXCLUDED.target_min, intensity=EXCLUDED.intensity, complete_cond=EXCLUDED.complete_cond,
  reward_exp=EXCLUDED.reward_exp, reward_dp=EXCLUDED.reward_dp, workout_kind=EXCLUDED.workout_kind, segments=EXCLUDED.segments, enabled=TRUE;

INSERT INTO schema_migrations (version) VALUES ('054') ON CONFLICT DO NOTHING;
