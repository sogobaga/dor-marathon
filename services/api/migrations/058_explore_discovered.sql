-- Migration 058: 城市探索「是否已揭露」旗標——保留神秘感。
-- 玩家「打卡」該點後才揭露背後的關主(Scene/名稱/難度/課表/對話)；未打卡前只是一個地點打卡任務。
-- discovered 由 Phase 3 打卡時設 TRUE；前台列表對未揭露者遮蔽關主資料（伺服器端遮蔽，devtools 也看不到）。

ALTER TABLE explore_progress ADD COLUMN IF NOT EXISTS discovered BOOLEAN NOT NULL DEFAULT FALSE;

INSERT INTO schema_migrations (version) VALUES ('058') ON CONFLICT DO NOTHING;
