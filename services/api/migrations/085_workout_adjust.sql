-- Migration 085: 自主訓練——課表可微調（距離型 ±1K、間歇型 ±趟、金字塔 ±400m）
-- workout_templates.adjust_type：distance(調總距離)/reps(調趟數)/pyramid(調峰值±400m)/none
ALTER TABLE workout_templates ADD COLUMN IF NOT EXISTS adjust_type TEXT NOT NULL DEFAULT 'none';
UPDATE workout_templates SET adjust_type='distance' WHERE category IN ('recovery','easy','lsd','tempo','threshold','progression');
UPDATE workout_templates SET adjust_type='reps'     WHERE category IN ('interval','fartlek','norwegian','yasso','rep');
UPDATE workout_templates SET adjust_type='pyramid'  WHERE category='pyramid';

-- 閾值跑原為「20 分時間段」→ 改為 5K 距離段，讓「總距離微調」可套用（否則距離型微調對它無效）。
UPDATE workout_templates SET
  segments = '[{"kind":"warmup","label":"熱身","effort":"easy","target_type":"distance","target":2000},{"kind":"work","label":"閾值持續 5K","effort":"threshold","target_type":"distance","target":5000},{"kind":"cooldown","label":"恢復緩和","effort":"easy","target_type":"distance","target":1000}]'::jsonb,
  description = '閾值配速持續 5 公里，提升乳酸耐受。'
WHERE code='threshold';

-- 每筆排程記住微調量（delta；0=課表預設）：距離型＝±公里、間歇型＝±趟、金字塔＝±(400m 峰值階)。
ALTER TABLE user_training_schedule ADD COLUMN IF NOT EXISTS adjust INT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version) VALUES ('085') ON CONFLICT DO NOTHING;
