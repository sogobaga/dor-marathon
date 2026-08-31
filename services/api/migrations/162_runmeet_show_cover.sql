-- Migration 162: 團練可選擇「不要在列表顯示封面圖」
--
-- 需求（使用者原話）：「團練編輯那邊可以用 checkbox 勾選『顯示圖片』，勾選代表要顯示圖片，預設為勾起。」
--
-- 用途：圖片有時是路線圖、集合點實景、群組截圖這類「給團員看沒問題，但不想放在公開列表被瀏覽」
-- 的內容。發起人可以取消勾選，卡片與社群分享卡就只顯示佔位，圖片仍在詳情頁給有權限的人看。
--
-- ⚠️ 這是「顯示偏好」，不是權限：真正決定誰看得到圖片的仍是既有的分層規則
--    （私密團未解鎖 → 後端本來就不給 cover_url；成員層資訊見 migration 156 的地點三層揭露）。
--    show_cover=false 只是在「本來就看得到」的前提下，額外把封面從列表與分享卡拿掉。
--    ⚠️ 因此後端遮蔽順序必須是「先判權限、再判偏好」，不可用這個欄位取代任何權限判斷。
--
-- 預設 TRUE：既有團練與新團練都維持現行行為（有圖就顯示），使用者要主動取消勾選才改變。

ALTER TABLE run_meets
    ADD COLUMN IF NOT EXISTS show_cover BOOLEAN NOT NULL DEFAULT TRUE;

INSERT INTO schema_migrations (version) VALUES ('162') ON CONFLICT DO NOTHING;
