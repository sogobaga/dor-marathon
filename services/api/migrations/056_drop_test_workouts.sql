-- Migration 056: 移除測試用計畫——DEMO(示範·400m間歇)、TESTWO(流程測試·迷你間歇)。
-- 正式課表庫 P01→P10 已由 cmd/seedworkouts 灌入，測試資料不再需要。
-- 依賴：054(建 DEMO)、055(建 TESTWO)。DELETE 會連帶清掉其 personal_tasks 與 personal_task_progress（ON DELETE CASCADE）。

DELETE FROM personal_plans WHERE code IN ('DEMO', 'TESTWO');

INSERT INTO schema_migrations (version) VALUES ('056') ON CONFLICT DO NOTHING;
