-- Migration 067: 使用者手動提供的重點場館精確座標（跑道中心）
-- 14 筆（DOR-TRK-PIF-006 因代碼=屏東但座標=苗栗、待使用者確認，暫不納入）。
UPDATE explore_bosses SET lat=25.133894, lng=121.761580 WHERE code='DOR-KEE-001';      -- 基隆市立田徑場
UPDATE explore_bosses SET lat=24.794206, lng=120.990658 WHERE code='DOR-HSZ-004';      -- 清華大學田徑場
UPDATE explore_bosses SET lat=23.693033, lng=120.537848 WHERE code='DOR-YUN-008';      -- 雲林科技大學操場
UPDATE explore_bosses SET lat=24.738221, lng=121.752929 WHERE code='DOR-ILA-001';      -- 宜蘭運動公園
UPDATE explore_bosses SET lat=25.086352, lng=121.529343 WHERE code='DOR-TRK-TPE-007';  -- 銘傳大學田徑場
UPDATE explore_bosses SET lat=25.023011, lng=121.544964 WHERE code='DOR-TRK-TPE-010';  -- 臺北教育大學田徑場
UPDATE explore_bosses SET lat=24.801391, lng=120.981769 WHERE code='DOR-TRK-HSZ-001';  -- 新竹市立體育場
UPDATE explore_bosses SET lat=24.979626, lng=121.427904 WHERE code='DOR-NTP-009';      -- 樹林體育園區
UPDATE explore_bosses SET lat=22.703339, lng=120.295334 WHERE code='DOR-KHH-001';      -- 高雄國家體育場／世運主場館
UPDATE explore_bosses SET lat=22.625711, lng=120.335235 WHERE code='DOR-KHH-011';      -- 高雄市立體育場／中正運動場
UPDATE explore_bosses SET lat=24.777661, lng=121.090437 WHERE code='DOR-TRK-HSQ-008';  -- 敏實科技大學（原大華科大）
UPDATE explore_bosses SET lat=24.562237, lng=120.713795 WHERE code='DOR-TRK-MIA-018';  -- 苗栗縣立新國民中學田徑場
UPDATE explore_bosses SET lat=24.672680, lng=121.661224 WHERE code='DOR-TRK-ILA-001';  -- 宜蘭縣三星鄉綜合運動場
UPDATE explore_bosses SET lat=24.865034, lng=121.817922 WHERE code='DOR-TRK-ILA-014';  -- 蘭陽技術學院運動場

INSERT INTO schema_migrations (version) VALUES ('067') ON CONFLICT DO NOTHING;
