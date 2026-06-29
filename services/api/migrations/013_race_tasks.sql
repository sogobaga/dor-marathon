-- Migration 013: 賽事任務系統（任務模組 + 三層任務指派）
-- 依賴：012_team_group_permission.sql
-- 本輪只做設定/資料層（不含完成判定引擎與前台顯示）。
-- 三層 scope：race_collective（全部參賽者集體加總）| group_team（分組團體加總）| group_individual（分組個人各自）
-- metric kind：threshold（實際值 >= target_value 完成）| range（落在 [range_lo, range_hi] 完成）

-- 全站共用任務模組（範本 header，仿 group_presets）
CREATE TABLE IF NOT EXISTS task_modules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) NOT NULL UNIQUE,
    description TEXT,
    is_system   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 模組內的任務項目
CREATE TABLE IF NOT EXISTS task_module_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id     UUID NOT NULL REFERENCES task_modules(id) ON DELETE CASCADE,
    metric_type   VARCHAR(40) NOT NULL,
    target_value  DECIMAL(10,2),      -- threshold 用
    range_lo      DECIMAL(10,2),      -- range 用
    range_hi      DECIMAL(10,2),
    title         VARCHAR(120) NOT NULL,
    description   TEXT,
    display_order INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_task_module_items_module ON task_module_items(module_id);

-- 賽事實際指派的任務（三層 scope，仿 race_supplies 整批重建 + group_index 對應）
CREATE TABLE IF NOT EXISTS race_tasks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    race_id       UUID NOT NULL REFERENCES races(id) ON DELETE CASCADE,
    scope         VARCHAR(20) NOT NULL,   -- race_collective | group_team | group_individual
    group_id      UUID REFERENCES race_groups(id) ON DELETE CASCADE,  -- race_collective 時為 NULL
    metric_type   VARCHAR(40) NOT NULL,
    target_value  DECIMAL(10,2),
    range_lo      DECIMAL(10,2),
    range_hi      DECIMAL(10,2),
    title         VARCHAR(120) NOT NULL,
    description   TEXT,
    display_order INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_race_tasks_race ON race_tasks(race_id);

-- 範例系統模組（方便快速體驗；皆為有資料源的指標）
INSERT INTO task_modules (name, description, is_system) VALUES
    ('入門挑戰', '適合一般組的基礎里程任務', TRUE),
    ('進階里程', '較高強度的累積與配速挑戰', TRUE)
ON CONFLICT (name) DO NOTHING;

INSERT INTO task_module_items (module_id, metric_type, target_value, range_lo, range_hi, title, description, display_order)
SELECT m.id, v.metric_type, v.target_value, v.range_lo, v.range_hi, v.title, v.description, v.display_order
FROM task_modules m
JOIN (VALUES
    ('入門挑戰', 'cumulative_distance', 30.00, NULL::DECIMAL, NULL::DECIMAL, '累計完成 30K', '活動期間累積總里程達 30 公里', 0),
    ('入門挑戰', 'single_distance',      5.00, NULL::DECIMAL, NULL::DECIMAL, '單次跑滿 5K', '單次活動里程達 5 公里', 1),
    ('進階里程', 'cumulative_distance', 100.00, NULL::DECIMAL, NULL::DECIMAL, '累計完成 100K', '活動期間累積總里程達 100 公里', 0),
    ('進階里程', 'avg_pace_range',       NULL::DECIMAL, 300.00, 360.00, '配速 5:00–6:00', '平均配速落在每公里 5 至 6 分鐘', 1)
) AS v(module_name, metric_type, target_value, range_lo, range_hi, title, description, display_order)
  ON v.module_name = m.name
WHERE m.is_system = TRUE
  AND NOT EXISTS (SELECT 1 FROM task_module_items i WHERE i.module_id = m.id);

INSERT INTO schema_migrations (version) VALUES ('013') ON CONFLICT DO NOTHING;
