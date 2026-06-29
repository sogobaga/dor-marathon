-- Migration 019: 選手分級（依匯入數據算加權分數 → 入門/初級/中級/進階/菁英）
-- 依賴：018_follows.sql
-- 供「報名頁追蹤者推薦」相似度評分與後台選手標籤；前台不顯示標籤。

-- 各指標的權重與正規化參考範圍（後台可設）
CREATE TABLE IF NOT EXISTS athlete_metric_config (
    metric_key    VARCHAR(20) PRIMARY KEY,  -- volume | pace | avg_dist | longest | monthly_freq
    weight        INT NOT NULL,
    ref_lo        NUMERIC(10,2) NOT NULL,    -- 對應 0 分的值
    ref_hi        NUMERIC(10,2) NOT NULL,    -- 對應 100 分的值（pace 為「越低越好」，計算時反向）
    display_order INT NOT NULL DEFAULT 0
);
INSERT INTO athlete_metric_config (metric_key, weight, ref_lo, ref_hi, display_order) VALUES
    ('volume',       25,   0,  300, 0),   -- 跑量(累積 km)
    ('pace',         20, 240,  480, 1),   -- 配速(秒/km；4:00 快→8:00 慢)
    ('avg_dist',     20,   0,   21, 2),   -- 平均每次距離(km)
    ('longest',      20,   0,   42, 3),   -- 最長單次(km)
    ('monthly_freq', 15,   0,   20, 4)    -- 月平均次數
ON CONFLICT DO NOTHING;

-- 等級門檻（composite 分數 → 等級名；後台可設）
CREATE TABLE IF NOT EXISTS athlete_levels (
    min_score INT PRIMARY KEY,
    name      VARCHAR(20) NOT NULL
);
INSERT INTO athlete_levels (min_score, name) VALUES
    (0, '入門'), (20, '初級'), (40, '中級'), (60, '進階'), (80, '菁英')
ON CONFLICT DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('019') ON CONFLICT DO NOTHING;
