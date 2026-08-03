-- 114_explore_master_image.sql
-- 關主場景圖 + 角色圖拆成兩張、AVG 疊圖顯示：新增 master_image_url（關主角色立繪，疊在 scene_image_url 場景圖上）。
-- 比照 scene_image_url/card_image_url，前後台共用；未揭露時後端 maskBoss 一併遮蔽。
ALTER TABLE explore_bosses ADD COLUMN IF NOT EXISTS master_image_url TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (version) VALUES ('114') ON CONFLICT DO NOTHING;
