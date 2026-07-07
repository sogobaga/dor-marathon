-- Migration 055: 課表垂直切片「流程測試」迷你課表——走/跑約 90 公尺、~2 分鐘跑完整套，快速驗證
-- 321→分段驅動→即時配速→評星→結算 的完整流程（正式 400m 間歇 6.4K 太遠不便驗）。
-- 依賴：054。stage_order=-1 → 全域鏈最前，Day1 立即可挑戰。

INSERT INTO personal_plans (code, name, lifecycle, stage_order, entry_note, data_source, enabled)
VALUES ('TESTWO', '流程測試 · 迷你間歇', '測試', -1,
  '流程驗證用：走/跑約 90 公尺、約 2 分鐘即可跑完整套，驗 321 倒數→分段進度→即時配速→評星→結算。', 'gps', TRUE)
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, lifecycle=EXCLUDED.lifecycle, stage_order=EXCLUDED.stage_order,
  entry_note=EXCLUDED.entry_note, enabled=TRUE;

INSERT INTO personal_tasks (plan_id, day, week, seq, title, workout, workout_type, target_km, target_min, intensity,
  complete_cond, data_source, reward_exp, reward_dp, workout_kind, segments)
SELECT p.id, 1, 1, 1, '迷你間歇（流程測試）', '暖身 20s → 衝刺 30m×3（休 10s）→ 緩和 20s', '間歇', 0.2, 2, 'RPE6',
  '走/跑完整套即結算；30m 段配速達成度決定星數', 'gps', 10, 2, 'interval',
  '[
    {"kind":"warmup","label":"暖身","target_type":"time","target":20,"reps":1,"rest_s":0},
    {"kind":"work","label":"衝刺 30m","target_type":"distance","target":30,"pace_fast_s":180,"pace_slow_s":720,"reps":3,"rest_s":10},
    {"kind":"cooldown","label":"緩和","target_type":"time","target":20,"reps":1,"rest_s":0}
  ]'::jsonb
FROM personal_plans p WHERE p.code='TESTWO'
ON CONFLICT (plan_id, day) DO UPDATE SET
  title=EXCLUDED.title, workout=EXCLUDED.workout, workout_type=EXCLUDED.workout_type, target_km=EXCLUDED.target_km,
  target_min=EXCLUDED.target_min, intensity=EXCLUDED.intensity, complete_cond=EXCLUDED.complete_cond,
  reward_exp=EXCLUDED.reward_exp, reward_dp=EXCLUDED.reward_dp, workout_kind=EXCLUDED.workout_kind, segments=EXCLUDED.segments, enabled=TRUE;

INSERT INTO schema_migrations (version) VALUES ('055') ON CONFLICT DO NOTHING;
