-- Migration 065: 手動校正重點場館(縣市立/大學/標準田徑場)座標至跑道中心
-- 方法：OSM Nominatim 依名稱定位 + 就近吸附 OSM 跑道橢圓/標準尺寸跑道（高信度），或場館型 Nominatim 中心（中信度）。
-- 重點場館 66，校正 21（高13/中8），需人工 45。

UPDATE explore_bosses SET lat=24.80601, lng=120.972293 WHERE code='DOR-HSZ-001';  -- 新竹市立田徑場 [高] nomi核心+oval 移動416m
UPDATE explore_bosses SET lat=24.794096, lng=120.980168 WHERE code='DOR-HSZ-005';  -- 陽明交通大學博愛校區田徑場 [高] nomi核心+oval 移動1928m
UPDATE explore_bosses SET lat=24.577243, lng=120.841497 WHERE code='DOR-MIA-001';  -- 苗栗縣立體育場 [高] nomi+oval 移動2772m
UPDATE explore_bosses SET lat=24.066456, lng=120.554433 WHERE code='DOR-CHA-001';  -- 彰化縣立體育場 [高] nomi+oval 移動562m
UPDATE explore_bosses SET lat=23.695887, lng=120.527072 WHERE code='DOR-YUN-001';  -- 雲林縣立田徑場 [高] nomi核心+oval 移動1898m
UPDATE explore_bosses SET lat=23.471422, lng=120.461901 WHERE code='DOR-CYI-001';  -- 嘉義市立體育場 [高] nomi+oval 移動953m
UPDATE explore_bosses SET lat=23.456529, lng=120.289521 WHERE code='DOR-CYQ-001';  -- 嘉義縣立田徑場 [高] nomi+oval 移動564m
UPDATE explore_bosses SET lat=22.983403, lng=120.207793 WHERE code='DOR-TNN-001';  -- 臺南市立體育場 [高] std-oval-near-orig 移動496m
UPDATE explore_bosses SET lat=23.320746, lng=120.312864 WHERE code='DOR-TNN-010';  -- 新營體育場 [高] nomi+oval 移動1529m
UPDATE explore_bosses SET lat=22.67602, lng=120.492682 WHERE code='DOR-PIF-001';  -- 屏東縣立田徑場 [高] std-oval-near-orig 移動674m
UPDATE explore_bosses SET lat=23.568581, lng=119.578909 WHERE code='DOR-PEN-001';  -- 澎湖縣立體育場 [高] nomi+oval 移動429m
UPDATE explore_bosses SET lat=24.935407, lng=121.222791 WHERE code='DOR-TRK-TAO-011';  -- 平鎮高中田徑場 [高] std-oval-near-orig 移動566m
UPDATE explore_bosses SET lat=24.745373, lng=121.747651 WHERE code='DOR-TRK-ILA-006';  -- 蘭陽女中田徑場 [高] std-oval-near-orig 移動612m
UPDATE explore_bosses SET lat=25.010166, lng=121.468382 WHERE code='DOR-NTP-002';  -- 板橋第一運動場 [中] nomi-stadium 移動221m
UPDATE explore_bosses SET lat=25.013897, lng=121.457759 WHERE code='DOR-NTP-003';  -- 板橋第二運動場 [中] nomi-stadium 移動169m
UPDATE explore_bosses SET lat=24.993532, lng=121.324548 WHERE code='DOR-TAO-001';  -- 桃園市立田徑場 [中] nomi-stadium 移動416m
UPDATE explore_bosses SET lat=24.151331, lng=120.689836 WHERE code='DOR-TXG-003';  -- 國立臺灣體育運動大學田徑場 [中] nomi-stadium 移動447m
UPDATE explore_bosses SET lat=23.905231, lng=120.681928 WHERE code='DOR-NAN-001';  -- 南投縣立體育場 [中] nomi-sports_centre 移動1216m
UPDATE explore_bosses SET lat=22.751266, lng=121.145383 WHERE code='DOR-TTT-001';  -- 臺東縣立體育場 [中] nomi-pitch 移動482m
UPDATE explore_bosses SET lat=24.430128, lng=118.313894 WHERE code='DOR-KIN-001';  -- 金門縣立體育場 [中] nomi-sports_centre 移動753m
UPDATE explore_bosses SET lat=26.158551, lng=119.94422 WHERE code='DOR-TRK-LIE-002';  -- 連江縣立南竿田徑場 [中] nomi-construction 移動90m

INSERT INTO schema_migrations (version) VALUES ('065') ON CONFLICT DO NOTHING;
