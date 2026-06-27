-- Migration 004: 報名必填欄位設定
-- 依賴：003_events.sql
--
-- 後台可逐欄位設定報名時哪些參賽者資料必填，預設為真實姓名 + 手機。
-- 可能值：real_name | nickname | phone | address | birthday | gender

ALTER TABLE races
    ADD COLUMN IF NOT EXISTS required_fields TEXT[] NOT NULL DEFAULT '{real_name,phone}';

INSERT INTO schema_migrations (version) VALUES ('004') ON CONFLICT DO NOTHING;
