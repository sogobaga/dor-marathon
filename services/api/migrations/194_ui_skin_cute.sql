-- Migration 194: 帳號層級「風格設定」新增第四個選項 'cute'（溫馨可愛，見
-- docs/skins/CUTE_CONTRACT.md §2）—— users_ui_skin_check 從
-- ('default','scifi','retro') 擴充為 ('default','scifi','retro','cute')。
--
-- 沿用 191_partner_shop_variants.sql／193_user_ui_skin.sql 前例：DROP CONSTRAINT IF EXISTS 再
-- ADD，可重複執行。不新增欄位、不回填任何帳號（cute 是新選項，沒有既有使用者需要延續）。

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_ui_skin_check;

ALTER TABLE users
    ADD CONSTRAINT users_ui_skin_check CHECK (ui_skin IS NULL OR ui_skin IN ('default', 'scifi', 'retro', 'cute'));

INSERT INTO schema_migrations (version) VALUES ('194') ON CONFLICT DO NOTHING;
