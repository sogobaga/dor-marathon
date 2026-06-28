-- Migration 008: 賽事開始/結束改為含時間（DATE → TIMESTAMPTZ）
-- 依賴：007_race_status.sql
-- 原本是日期（午夜）；改成 timestamptz 後可設定上午/下午等時間，display_status 推導更精準。

ALTER TABLE races
    ALTER COLUMN start_date TYPE TIMESTAMPTZ USING start_date::timestamptz,
    ALTER COLUMN end_date   TYPE TIMESTAMPTZ USING end_date::timestamptz;

INSERT INTO schema_migrations (version) VALUES ('008') ON CONFLICT DO NOTHING;
