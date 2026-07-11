-- Migration 070: 停用確認「不對外開放」的場地（避免關主挑戰跑不進去）
-- 開放性備註明載「已無空間對外開放」→ enabled=FALSE（前台不顯示、不可挑戰）。
UPDATE explore_bosses SET enabled=FALSE WHERE code='DOR-TRK-TPE-012';  -- 開平餐飲學校操場：已無空間對外開放

INSERT INTO schema_migrations (version) VALUES ('070') ON CONFLICT DO NOTHING;
