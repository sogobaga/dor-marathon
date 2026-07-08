-- Migration 057: 城市探索（打卡點關主挑戰 + 卡片收集）
-- 有別於「賽事內打卡任務(task_checkpoints)」——城市探索綁在玩家個人身上、與賽事無關、全免費、可持續擴充。
-- 每個打卡點＝一位「關主」：到點打卡→關主挑戰(結構化課表，比照個人任務)→完成得 1-3★→3★ 取得關主卡片。

CREATE TABLE IF NOT EXISTS explore_bosses (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code            TEXT NOT NULL UNIQUE,               -- DOR-TPE-001
  name            TEXT NOT NULL DEFAULT '',           -- 大安小鹿
  title           TEXT NOT NULL DEFAULT '',           -- 大安的傳說級守門人
  region          TEXT NOT NULL DEFAULT '',           -- 臺北市·大安區
  place           TEXT NOT NULL DEFAULT '',           -- 大安森林公園
  gender          TEXT NOT NULL DEFAULT '',
  age             INT  NOT NULL DEFAULT 0,
  workout_label   TEXT NOT NULL DEFAULT '',           -- 節奏跑混合型
  difficulty_stars INT NOT NULL DEFAULT 3,            -- 關主難度 1-6 → 挑戰消耗 DP = difficulty_stars × 10
  quote           TEXT NOT NULL DEFAULT '',           -- 卡片標語：蟬都叫成這樣了，你還不加速？
  skill_name      TEXT NOT NULL DEFAULT '',           -- 必殺技能：樹影瞬移
  skill_desc      TEXT NOT NULL DEFAULT '',           -- 技能說明
  dialogue_intro  TEXT NOT NULL DEFAULT '',           -- 打卡後、挑戰前關主說的話
  dialogue_start  TEXT NOT NULL DEFAULT '',           -- 接受挑戰後關主說的話
  scene_image_url TEXT NOT NULL DEFAULT '',           -- 挑戰時顯示的場景圖 (Scene)
  card_image_url  TEXT NOT NULL DEFAULT '',           -- 挑戰成功取得的卡片 (Card)
  lat             DOUBLE PRECISION NOT NULL DEFAULT 0,
  lng             DOUBLE PRECISION NOT NULL DEFAULT 0,
  radius_m        INT NOT NULL DEFAULT 40,            -- 可接受打卡（接受挑戰）半徑
  reward_exp      INT NOT NULL DEFAULT 0,             -- 完成挑戰獎勵
  reward_dp       INT NOT NULL DEFAULT 0,
  retry_dp_cost   INT NOT NULL DEFAULT 0,             -- 重挑 DP（0 = 用 difficulty_stars×10）
  workout_kind    TEXT NOT NULL DEFAULT '',           -- 課表型別（interval/tempo/mixed...）
  segments        JSONB NOT NULL DEFAULT '[]'::jsonb, -- 結構化課表分段（比照 personal_tasks.segments）
  data_source     TEXT NOT NULL DEFAULT 'gps',
  display_order   INT NOT NULL DEFAULT 0,
  enabled         BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_explore_bosses_order ON explore_bosses(display_order, code);

-- 玩家對每位關主的進度（挑戰星數 / 是否已取得卡片 / 進行中挑戰）
CREATE TABLE IF NOT EXISTS explore_progress (
  user_id             UUID NOT NULL,
  boss_id             UUID NOT NULL REFERENCES explore_bosses(id) ON DELETE CASCADE,
  stars               INT  NOT NULL DEFAULT 0,        -- 最佳挑戰星數 0-3
  awarded_stars       INT  NOT NULL DEFAULT 0,        -- 發獎冪等
  card_obtained       BOOLEAN NOT NULL DEFAULT FALSE, -- stars>=3 → 取得卡片
  attempts            INT  NOT NULL DEFAULT 0,
  active              BOOLEAN NOT NULL DEFAULT FALSE, -- 進行中挑戰（已接受、尚未完成/放棄）
  challenge_started_at TIMESTAMPTZ,
  completed_at        TIMESTAMPTZ,
  card_obtained_at    TIMESTAMPTZ,
  PRIMARY KEY (user_id, boss_id)
);
CREATE INDEX IF NOT EXISTS idx_explore_progress_user ON explore_progress(user_id);

-- 種子：第一位關主 大安小鹿（圖片已放 public/characters/，由 Next 靜態服務）
INSERT INTO explore_bosses (code, name, title, region, place, gender, age, workout_label, difficulty_stars,
  quote, skill_name, skill_desc, dialogue_intro, dialogue_start, scene_image_url, card_image_url,
  lat, lng, radius_m, reward_exp, reward_dp, workout_kind, segments, display_order, enabled)
VALUES ('DOR-TPE-001', '大安小鹿', '大安的傳說級守門人', '臺北市·大安區', '大安森林公園', '女', 39, '節奏跑混合型', 6,
  '蟬都叫成這樣了，你還不加速？', '樹影瞬移', '融入樹影，瞬間移動，穿梭林間，讓對手跟不上她的節奏！',
  '哦？有新的挑戰者來到大安森林公園。想拿到我的卡片，就得先跟上我的節奏。',
  '準備好了嗎？蟬鳴響起就是起跑訊號——別讓我等太久！',
  '/characters/DOR-TPE-001-Scene.png', '/characters/DOR-TPE-001-Card.png',
  25.0296, 121.5357, 50, 120, 30, 'mixed',
  '[
    {"kind":"warmup","label":"暖身","target_type":"distance","target":1500,"pace_fast_s":420,"pace_slow_s":480,"reps":1,"rest_s":0},
    {"kind":"work","label":"節奏跑","target_type":"distance","target":2000,"pace_fast_s":330,"pace_slow_s":360,"reps":1,"rest_s":0},
    {"kind":"work","label":"間歇","target_type":"distance","target":400,"pace_fast_s":300,"pace_slow_s":330,"reps":4,"rest_s":90},
    {"kind":"cooldown","label":"緩和","target_type":"distance","target":1000,"pace_fast_s":420,"pace_slow_s":480,"reps":1,"rest_s":0}
  ]'::jsonb, 1, TRUE)
ON CONFLICT (code) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('057') ON CONFLICT DO NOTHING;
