-- Migration 068: DOR-TRK-PIF-006 屏東縣立大同高級中學 田徑場座標（使用者確認為屏東，非苗栗）
UPDATE explore_bosses SET lat=22.678609, lng=120.478190 WHERE code='DOR-TRK-PIF-006';  -- 屏東縣立大同高級中學

INSERT INTO schema_migrations (version) VALUES ('068') ON CONFLICT DO NOTHING;
