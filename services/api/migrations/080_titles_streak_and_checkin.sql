-- Migration 080: 稱號擴充
--  (A) 打卡數量 checkin 類別往上加 5 階（600–1000，新增純打卡點後總點數 ~1098）
--  (B) 全新「連續跑步」streak 類別 17 階——名稱不含天數、只傳達「連續堅持」意象；未解鎖沿用 ？？？ 遮蔽
INSERT INTO title_defs (code, category, threshold, unit, name, tier, sort_order) VALUES
('checkin_18','checkin',600,'個','千里行者',6,159),
('checkin_19','checkin',700,'個','踏遍山河',6,160),
('checkin_20','checkin',800,'個','走讀天下',6,161),
('checkin_21','checkin',900,'個','環島封神',6,162),
('checkin_22','checkin',1000,'個','大地行者・神',6,163),
('streak_00','streak',1,'日','晨光初起',1,164),
('streak_01','streak',3,'日','習慣萌芽',1,165),
('streak_02','streak',5,'日','漸入佳境',1,166),
('streak_03','streak',7,'日','從不缺席',2,167),
('streak_04','streak',10,'日','堅持之道',2,168),
('streak_05','streak',15,'日','風雨無阻',2,169),
('streak_06','streak',20,'日','自律成性',3,170),
('streak_07','streak',30,'日','恆心者',3,171),
('streak_08','streak',50,'日','毅力宗師',4,172),
('streak_09','streak',60,'日','日日精進',4,173),
('streak_10','streak',80,'日','堅毅不拔',4,174),
('streak_11','streak',100,'日','意志如鐵',5,175),
('streak_12','streak',150,'日','心如磐石',5,176),
('streak_13','streak',200,'日','時間的信徒',5,177),
('streak_14','streak',250,'日','不滅之焰',6,178),
('streak_15','streak',300,'日','恆星之志',6,179),
('streak_16','streak',365,'日','永恆步者',6,180)
ON CONFLICT (code) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('080') ON CONFLICT DO NOTHING;
