-- Migration 064: 校正田徑場/跑道/操場關主座標至跑道正中心
-- 來源 OpenStreetMap（leisure=track ∪ sport=athletics/running 的幾何中心），對到門檻 400m。
-- 田徑場類關主 386，對到並校正 38，未對到 345（保留原座標、待人工複核）。

UPDATE explore_bosses SET lat=23.991952, lng=121.607501 WHERE code='DOR-HUA-001';  -- 花蓮縣立田徑場 移動374m
UPDATE explore_bosses SET lat=25.048995, lng=121.549727 WHERE code='DOR-TPE-002';  -- 臺北田徑場 移動224m
UPDATE explore_bosses SET lat=25.019815, lng=121.535392 WHERE code='DOR-TPE-003';  -- 國立臺灣大學田徑場 移動197m
UPDATE explore_bosses SET lat=24.130919, lng=120.46956 WHERE code='DOR-TRK-CHA-027';  -- 線西國小田徑場 移動8m
UPDATE explore_bosses SET lat=23.471422, lng=120.461901 WHERE code='DOR-TRK-CYI-001';  -- 嘉義市立田徑場 移動9m
UPDATE explore_bosses SET lat=24.80601, lng=120.972311 WHERE code='DOR-TRK-HSZ-003';  -- 新竹市東門國小操場 移動88m
UPDATE explore_bosses SET lat=24.794096, lng=120.980168 WHERE code='DOR-TRK-HSZ-004';  -- 新竹高商田徑場 移動348m
UPDATE explore_bosses SET lat=24.753901, lng=121.765791 WHERE code='DOR-TRK-ILA-007';  -- 黎明國小田徑場 移動393m
UPDATE explore_bosses SET lat=24.592216, lng=121.854535 WHERE code='DOR-TRK-ILA-009';  -- 士敏國小田徑場 移動28m
UPDATE explore_bosses SET lat=24.595603, lng=121.835831 WHERE code='DOR-TRK-ILA-011';  -- 蘇澳國中田徑場 移動28m
UPDATE explore_bosses SET lat=22.624217, lng=120.310602 WHERE code='DOR-TRK-KHH-016';  -- 高雄高商田徑場 移動11m
UPDATE explore_bosses SET lat=22.883562, lng=120.328397 WHERE code='DOR-TRK-KHH-032';  -- 阿蓮國小田徑場 移動86m
UPDATE explore_bosses SET lat=24.433504, lng=120.64659 WHERE code='DOR-TRK-MIA-011';  -- 文苑國小跑道 移動74m
UPDATE explore_bosses SET lat=24.686523, lng=120.934427 WHERE code='DOR-TRK-MIA-024';  -- 僑善國小田徑場 移動34m
UPDATE explore_bosses SET lat=25.099627, lng=121.44985 WHERE code='DOR-TRK-NTP-006';  -- 成州國民小學學校操場 移動6m
UPDATE explore_bosses SET lat=25.070511, lng=121.468068 WHERE code='DOR-TRK-NTP-007';  -- 更寮國小操場 移動49m
UPDATE explore_bosses SET lat=25.027285, lng=121.737313 WHERE code='DOR-TRK-NTP-013';  -- 平溪國小操場 移動12m
UPDATE explore_bosses SET lat=25.076406, lng=121.381263 WHERE code='DOR-TRK-NTP-025';  -- 林口高中田徑場 移動378m
UPDATE explore_bosses SET lat=24.862382, lng=121.546764 WHERE code='DOR-TRK-NTP-039';  -- 烏來運動場田徑場（原登錄：烏來運動場） 移動82m
UPDATE explore_bosses SET lat=24.777363, lng=121.50226 WHERE code='DOR-TRK-NTP-041';  -- 福山國小田徑場 移動11m
UPDATE explore_bosses SET lat=25.111941, lng=121.856366 WHERE code='DOR-TRK-NTP-043';  -- 時雨高中田徑場 移動296m
UPDATE explore_bosses SET lat=25.107908, lng=121.803457 WHERE code='DOR-TRK-NTP-045';  -- 瑞芳國小田徑場 移動70m
UPDATE explore_bosses SET lat=23.590222, lng=119.61136 WHERE code='DOR-TRK-PEN-002';  -- 志清國中田徑場 移動22m
UPDATE explore_bosses SET lat=23.527969, lng=119.590173 WHERE code='DOR-TRK-PEN-003';  -- 五德國小田徑場 移動19m
UPDATE explore_bosses SET lat=22.363227, lng=120.600565 WHERE code='DOR-TRK-PIF-011';  -- 僑德國小田徑場 移動28m
UPDATE explore_bosses SET lat=24.950261, lng=121.205692 WHERE code='DOR-TRK-TAO-012';  -- 文化國小田徑場 移動366m
UPDATE explore_bosses SET lat=22.949196, lng=120.249969 WHERE code='DOR-TRK-TNN-002';  -- 德南國小環形/直線慢跑道(非田徑場型) 移動68m
UPDATE explore_bosses SET lat=23.190729, lng=120.314735 WHERE code='DOR-TRK-TNN-015';  -- 隆田國小田徑場 移動202m
UPDATE explore_bosses SET lat=22.97094, lng=120.223757 WHERE code='DOR-TRK-TNN-020';  -- 崇明國中運動場田徑場（原登錄：崇明國中運動場） 移動90m
UPDATE explore_bosses SET lat=22.96198, lng=120.297461 WHERE code='DOR-TRK-TNN-024';  -- 紅瓦厝國小田徑場 移動10m
UPDATE explore_bosses SET lat=25.095186, lng=121.515425 WHERE code='DOR-TRK-TPE-008';  -- 陽明高中田徑場 移動325m
UPDATE explore_bosses SET lat=25.019311, lng=121.547861 WHERE code='DOR-TRK-TPE-011';  -- 芳和國中田徑場 移動191m
UPDATE explore_bosses SET lat=25.031962, lng=121.541236 WHERE code='DOR-TRK-TPE-012';  -- 開平餐飲學校操場 移動389m
UPDATE explore_bosses SET lat=24.351053, lng=120.616444 WHERE code='DOR-TRK-TXG-010';  -- 大甲體育場田徑場（原登錄：大甲體育場） 移動27m
UPDATE explore_bosses SET lat=24.137164, lng=120.69894 WHERE code='DOR-TRK-TXG-018';  -- 育英國中田徑場 移動156m
UPDATE explore_bosses SET lat=24.256716, lng=120.571126 WHERE code='DOR-TRK-TXG-021';  -- 臺中港區第六運動場跑道（原登錄：臺中港區第六運動場） 移動40m
UPDATE explore_bosses SET lat=23.579326, lng=120.308266 WHERE code='DOR-TRK-YUN-006';  -- 北港農工田徑場 移動122m
UPDATE explore_bosses SET lat=23.713349, lng=120.551941 WHERE code='DOR-TRK-YUN-017';  -- 斗六市立運動場(田徑場) 移動51m

INSERT INTO schema_migrations (version) VALUES ('064') ON CONFLICT DO NOTHING;
