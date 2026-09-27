-- Migration 193: 帳號層級「風格設定」(users.ui_skin) —— 取代第 22 套 scifi 的純前端 localStorage
-- 偏好，改為伺服器權威儲存（見 retro_skin/CONTRACT.md §2.1）。
--
-- ui_skin：NULL＝未曾選擇（等同 'default'）；'default'|'scifi'|'retro' 三選一。CHECK 約束用
-- pg_constraint 是否已存在判斷是否要加，可重複執行（比照 191_partner_shop_variants.sql 前例）。
--
-- 資料延續：既有白名單帳號 sogobaga@gmail.com 原本是靠純前端 localStorage 開啟 scifi（伺服器端從
-- 未記過這個選擇），本次上線把它顯式寫回 DB，避免「原本開著科幻風格的使用者升級後突然被打回預設」。
-- 只在 ui_skin 尚未設定過（IS NULL）時才寫，不覆蓋任何已存在的值（此欄位在本版之前不存在，實務上
-- 這條件恆真，寫成這樣只是防呆——例如本檔案被重複執行的情況）。

ALTER TABLE users ADD COLUMN IF NOT EXISTS ui_skin TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'users_ui_skin_check'
    ) THEN
        ALTER TABLE users
            ADD CONSTRAINT users_ui_skin_check CHECK (ui_skin IS NULL OR ui_skin IN ('default', 'scifi', 'retro'));
    END IF;
END $$;

UPDATE users SET ui_skin = 'scifi' WHERE lower(email) = 'sogobaga@gmail.com' AND ui_skin IS NULL;

INSERT INTO schema_migrations (version) VALUES ('193') ON CONFLICT DO NOTHING;
