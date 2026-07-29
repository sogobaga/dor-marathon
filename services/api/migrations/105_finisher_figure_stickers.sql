-- Migration 105: 完賽公仔九宮格貼紙 Phase 2b 後端 A —— 種子資料
-- 依賴：102_monopoly_draw.sql（sticker_pieces / user_stickers / monopoly_pool_entries 建表）
--
-- 素材（已在 public，URL 即路徑）：
--   - 彩色完整公仔：/source/ui/03_figure/runner_figure_color.png（1254×1254）
--   - 灰階九宮格切片：/source/ui/03_figure/DOR-Finisher-Figure-Gray-Tile-01.png ~ -09.png
--     （各 300×300，3×3 row-major：01=左上…05=中央…09=右下）
--
-- 本檔只種子資料，不改表結構：9 片貼紙定義 + app_settings（彩圖 URL／標題）+ 機會/命運池各加一項
-- reward_type='sticker'，讓停在機會/命運格開始有機率抽到完賽公仔碎片。

-- 9 片貼紙（set_key='finisher', position 1..9），image_url 補零 01..09。
INSERT INTO sticker_pieces (set_key, position, title, image_url, rarity, is_active)
SELECT
    'finisher',
    n,
    '公仔碎片 ' || n,
    '/source/ui/03_figure/DOR-Finisher-Figure-Gray-Tile-' || LPAD(n::text, 2, '0') || '.png',
    'common',
    true
FROM generate_series(1, 9) AS n
ON CONFLICT (set_key, position) DO NOTHING;

-- 完整彩圖 URL + 標題（供 GET /monopoly/stickers 讀取展示）。
INSERT INTO app_settings (key, value) VALUES
  ('monopoly_figure_color_url', '/source/ui/03_figure/runner_figure_color.png'),
  ('monopoly_figure_title', '完賽跑者公仔')
ON CONFLICT (key) DO NOTHING;

-- 機會/命運池各加一項 sticker，讓完賽公仔碎片開始有機率抽到（比照 102 的獎勵池寫法）。
INSERT INTO monopoly_pool_entries (pool, reward_type, weight, amount, is_active, note) VALUES
  ('chance',  'sticker', 5, 0, true, '完賽公仔碎片'),
  ('destiny', 'sticker', 5, 0, true, '完賽公仔碎片')
ON CONFLICT DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('105') ON CONFLICT DO NOTHING;
