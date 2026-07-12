-- Migration 074: 賽事打卡點改為「在範圍內即成功、一律免審核」
-- 拿掉 25m 移動軌跡佐證與待審機制（與城市探索打卡一致）。
-- 既有待審(flagged=true)的打卡一律視為核准、計入集章。
UPDATE checkpoint_checkins SET flagged=FALSE, flag_reason=NULL WHERE flagged=TRUE;

INSERT INTO schema_migrations (version) VALUES ('074') ON CONFLICT DO NOTHING;
