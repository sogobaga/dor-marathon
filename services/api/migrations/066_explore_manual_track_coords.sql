-- Migration 066: 手動+自動抓取校正剩餘重點場館座標（使用者授權直接抓、事後複核）
-- 已自動剔除與 064/065 或本批撞號(<30m)、位移過大者。
-- 套用 30 筆；剔除 7 筆(見下方註解)。

UPDATE explore_bosses SET lat=25.042441, lng=121.451241 WHERE code='DOR-NTP-004';  -- [確認] 使用者提供座標
UPDATE explore_bosses SET lat=24.985049, lng=121.456351 WHERE code='DOR-NTP-008';  -- [確認] 使用者提供座標
UPDATE explore_bosses SET lat=25.06537, lng=121.658424 WHERE code='DOR-NTP-010';  -- 汐止運動公園／綜合運動場 [中] Nominatim-pitch 移動301m
UPDATE explore_bosses SET lat=24.828635, lng=121.025197 WHERE code='DOR-HSQ-001';  -- 新竹縣第二運動場 [高] 就近吸附OSM跑道 移動1428m
UPDATE explore_bosses SET lat=24.119624, lng=120.66818 WHERE code='DOR-TXG-004';  -- 中興大學操場 [高] 就近吸附OSM跑道 移動815m
UPDATE explore_bosses SET lat=22.993244, lng=120.213893 WHERE code='DOR-TNN-002';  -- 成功大學操場 [高] 就近吸附OSM跑道 移動789m
UPDATE explore_bosses SET lat=26.153083, lng=119.938627 WHERE code='DOR-LIE-001';  -- 南竿運動場 [中] Nominatim-administrative 移動1335m
UPDATE explore_bosses SET lat=25.025249, lng=121.561462 WHERE code='DOR-TRK-TPE-002';  -- 臺北醫學大學田徑場 [中] Nominatim-university 移動70m
UPDATE explore_bosses SET lat=25.05552, lng=121.458833 WHERE code='DOR-TRK-NTP-018';  -- 頭前國中田徑場 [中] Nominatim-school 移動25m
UPDATE explore_bosses SET lat=25.185731, lng=121.444972 WHERE code='DOR-TRK-NTP-036';  -- 淡江大學田徑場 [高] 就近吸附OSM跑道 移動1353m
UPDATE explore_bosses SET lat=25.15499, lng=121.729634 WHERE code='DOR-TRK-KEE-001';  -- 經國管理暨健康學院綜合運動場跑道（原登錄：經國管理暨健康學院綜合運動場） [高] 就近吸附OSM跑道 移動579m
UPDATE explore_bosses SET lat=24.760017, lng=120.953401 WHERE code='DOR-TRK-HSZ-006';  -- 中華大學田徑場 [中] Nominatim-assembly_point 移動361m
UPDATE explore_bosses SET lat=24.556193, lng=120.832974 WHERE code='DOR-TRK-MIA-016';  -- 苗栗農工田徑場 [中] Nominatim-school 移動95m
UPDATE explore_bosses SET lat=24.150297, lng=120.683286 WHERE code='DOR-TRK-TXG-003';  -- 臺中科大田徑場 [中] Nominatim-university 移動125m
UPDATE explore_bosses SET lat=24.17298, lng=120.735843 WHERE code='DOR-TRK-TXG-004';  -- 中臺科技大學田徑場(操場) [中] Nominatim-university 移動93m
UPDATE explore_bosses SET lat=24.09667, lng=120.711177 WHERE code='DOR-TRK-TXG-012';  -- 修平科技大學田徑場 [中] Nominatim-university 移動121m
UPDATE explore_bosses SET lat=24.13094, lng=120.72047 WHERE code='DOR-TRK-TXG-016';  -- 臺中市太平區運動場跑道（原登錄：臺中市太平區運動場） [中] Nominatim-bicycle_rental 移動53m
UPDATE explore_bosses SET lat=24.170493, lng=120.619305 WHERE code='DOR-TRK-TXG-023';  -- 協和國小操場 [中] Nominatim-school 移動33m
UPDATE explore_bosses SET lat=24.26072, lng=120.71703 WHERE code='DOR-TRK-TXG-027';  -- 臺中市立豐原區體育場跑道（原登錄：臺中市立豐原區體育場） [中] Nominatim-bicycle_rental 移動76m
UPDATE explore_bosses SET lat=24.080431, lng=120.715889 WHERE code='DOR-TRK-TXG-030';  -- 霧峰區綜合運動場跑道（原登錄：霧峰區綜合運動場） [中] Nominatim-sports_centre 移動136m
UPDATE explore_bosses SET lat=23.877263, lng=120.521482 WHERE code='DOR-TRK-CHA-003';  -- 北斗國中田徑場 [中] Nominatim-school 移動20m
UPDATE explore_bosses SET lat=24.077919, lng=120.55274 WHERE code='DOR-TRK-CHA-013';  -- 彰化師範大學田徑場 [高] 就近吸附OSM跑道 移動862m
UPDATE explore_bosses SET lat=23.059221, lng=120.15395 WHERE code='DOR-TRK-TNN-012';  -- 康寧大學田徑場 [中] Nominatim-bus_stop 移動256m
UPDATE explore_bosses SET lat=22.669865, lng=120.318822 WHERE code='DOR-TRK-KHH-003';  -- 文藻外語大學田徑場 [中] Nominatim-university 移動9m
UPDATE explore_bosses SET lat=22.708754, lng=120.302306 WHERE code='DOR-TRK-KHH-022';  -- 油廠國田徑場 [中] Nominatim-station 移動183m
UPDATE explore_bosses SET lat=22.734243, lng=120.283498 WHERE code='DOR-TRK-KHH-024';  -- 高雄大學田徑場 [中] Nominatim-university 移動444m
UPDATE explore_bosses SET lat=22.624964, lng=120.321387 WHERE code='DOR-TRK-KHH-027';  -- 中正運動場跑道（原登錄：中正運動場） [高] 就近吸附OSM跑道 移動1323m
UPDATE explore_bosses SET lat=22.643024, lng=120.610006 WHERE code='DOR-TRK-PIF-002';  -- 屏東科技大學田徑場 [中] Nominatim-university 移動649m
UPDATE explore_bosses SET lat=22.789369, lng=121.112272 WHERE code='DOR-TRK-TTT-004';  -- 卑南國中田徑場 [中] Nominatim-school 移動83m
UPDATE explore_bosses SET lat=22.754915, lng=121.147721 WHERE code='DOR-TRK-TTT-005';  -- 臺東大學附小操場 [中] Nominatim-school 移動16m

-- 以下剔除(不校正、保留原座標，需人工)：
--   DOR-KEE-001  位移過大 3777m 可疑
--   DOR-HSZ-004  撞已修 DOR-TRK-HSZ-004
--   DOR-YUN-008  撞已修 DOR-YUN-001
--   DOR-ILA-001  撞已修 DOR-TRK-ILA-006
--   DOR-TRK-TPE-007  撞已修 DOR-TRK-TPE-008
--   DOR-TRK-TPE-010  撞已修 DOR-TRK-TPE-011
--   DOR-TRK-HSZ-001  撞已修 DOR-TRK-HSZ-004

INSERT INTO schema_migrations (version) VALUES ('066') ON CONFLICT DO NOTHING;
