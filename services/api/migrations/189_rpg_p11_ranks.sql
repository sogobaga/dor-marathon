-- 189_rpg_p11_ranks.sql
-- DORPG P11：怪物強度九級表（F～特S）＋強度挑戰對戰列表（每級 1／3／5 隻）＋高階怪召喚。
-- 契約見 scratchpad/dorpg_p11/{CONTRACT.md,WIRE.md}（v2 校準後定稿，數值來自
-- scratchpad/dorpg_p11/RANK_TABLE.md §1.1「原始校準值」，不是 §1.5 的 PAVA 單調備援表）。
--
-- ⚠️ 使用者手動套用到 Neon，不得自行套到正式庫；本檔全部 CREATE TABLE IF NOT EXISTS /
-- ADD COLUMN IF NOT EXISTS、seed 全部 ON CONFLICT DO UPDATE（可安全重複執行、也可用來修正
-- 校準數值），FK 用「先 DROP CONSTRAINT IF EXISTS 再 ADD CONSTRAINT」達成等效的可重複執行。
--
-- ⚠️⚠️ 部署順序（同 180/181/184 等既有慣例，db-before-code-push.md）：
--   (a) rpg_monster_ranks 是全新表，程式碼查詢它查無資料表會拿到 42P01，
--       ranks.go/battle.go 已用 respondIfMissingRelationMsg 接住、回 503（不影響既有六場）。
--   (b) rpg_encounters 新增的 scaling_mode/level_mode/rank/monster_count 四欄是既有表加欄位
--       （不是新表）——沿用 jobs.go loadJobExtras() 檔頭的既有决定：程式碼直接把這四欄併入
--       content_repo.go 既有的 encounterCols/scanEncounter，migration 未套用時會是 42703
--       （欄位不存在），本輪不額外做「優雅降級」，push 前必須先套用本檔。
--
-- 為什麼要重設既有六場「劇情場景」與五隻既有怪物的 mult：不重設（見 RANK_TABLE.md §5.2）。
-- CONTRACT §1 決定：既有六場 scaling_mode 恆為 'legacy'，繼續吃現行公式
-- （battle_lvl 3/3/0.4/0.4 × 怪物既有 hp_mult/atk_mult/def_mult × power_scale 1.0，
-- 這條路徑完全零改動）；只有新建的「強度挑戰」20 場（scaling_mode='rank'）改吃
-- rpg_monster_ranks 的九級向量，兩套系統的乘數互不重疊，不會互相污染彼此的手感。

-- ---------------------------------------------------------------------------
-- 1) rpg_monster_ranks：九級強度表（後台可調倍率/召喚規則）。
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rpg_monster_ranks (
  rank            TEXT PRIMARY KEY CHECK (rank IN ('F','E','D','C','B','A','SA','S','SS')),
  label           TEXT NOT NULL,                    -- F級／E級／…／特A級／…／特S級（顯示用中文）
  sort_order      INT NOT NULL DEFAULT 0,           -- 1~9，對應 F→E→D→C→B→A→SA→S→SS
  hp_mult         NUMERIC(8,3) NOT NULL DEFAULT 1,
  atk_mult        NUMERIC(8,3) NOT NULL DEFAULT 1,
  def_mult        NUMERIC(8,3) NOT NULL DEFAULT 1,
  mdef_mult       NUMERIC(8,3) NOT NULL DEFAULT 1,
  -- level_curve：{ "<level>": <multiplier> }，ScaleMonsterByRank() 依 strconv(level) 查表，
  -- 查無鍵或值 <=0 一律視為 1（契約：本輪九級全部種 {}，等級漂移已知缺口留給 P13 重算
  -- refPlayerTable 之後再回頭填，見 RANK_TABLE.md §4.3）。
  level_curve     JSONB NOT NULL DEFAULT '{}'::jsonb,
  -- summon：{ "waves": [ { "at_hp_pct":60, "rank":"E", "count":2, "monster_ids":[], "power_scale":0.15 }, ... ] }，
  --   power_scale＝召喚怪整體折減（預設 1.0、值域 0.05–2.0）。2026-09-20 sim3 實測：召喚怪若用完整 rank 向量，
  --   A 級 3 人隊 270 場只贏 1 場、S／特S 全敗，故 A 0.15（90% 勝）、S 0.25（47%／約 5.3 分）、特S 0.05（40%／約 27.8 分）；
  --   ⚠️特A 在 0.05–2.0 全區間都無法讓滿隊中位達 90%（最佳 0.10 約 80–83%，重騎士 100% 但輕騎士／法師偏低），
  --   暫取 0.10 並列為已知缺口（後台可調、待結構調整：減少隻數／提高門檻／職業補手段）。
  -- monster_ids 空陣列＝用該 rank 的分級怪 DOR-MON-R-<rank>。只有 A 以上（A/SA/S/SS）有值，
  -- F~B 恆為 {}（CONTRACT §1）。
  summon          JSONB NOT NULL DEFAULT '{}'::jsonb,
  badge_color     TEXT NOT NULL DEFAULT '',         -- UI 徽章色，不影響任何數值公式
  description     TEXT NOT NULL DEFAULT '',         -- 這一級的定義文字（使用者原話濃縮，§0）
  -- calibrated_note：校準基準等級與已知落差，供後台管理者調整前先看到警語（RANK_TABLE.md §1.1
  -- 「有效範圍」欄逐級摘要）——PUT /admin/rpg/monster-ranks 的請求 body 不含這欄（WIRE 明講），
  -- ranks.go upsertRank() 刻意不覆寫它，只有這個 migration 的 seed 會寫入/更新這欄。
  calibrated_note TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- badge_color 依級遞進（灰→綠→藍→紫→金→紅→深紅→紫紅→近黑，九色一一對應九級，越後面代表
-- 越接近「終局內容」，純 UI 設計決策、不影響任何戰鬥數值）。
INSERT INTO rpg_monster_ranks
  (rank, label, sort_order, hp_mult, atk_mult, def_mult, mdef_mult, level_curve, summon, badge_color, description, calibrated_note)
VALUES
  ('F', 'F級', 1, 1.8, 1.8, 0.240, 0.240, '{}'::jsonb, '{}'::jsonb,
    '#9e9e9e', '裸裝、無技能、無屬性相剋即可輕鬆取勝', 'Lv10/30/50 全過，無已知落差（校準基準 Lv10）'),
  ('E', 'E級', 2, 0.7, 8.75, 0.093, 0.093, '{}'::jsonb, '{}'::jsonb,
    '#4caf50', '裸裝＋技能即可輕鬆取勝', '校準基準 Lv10；Lv30 需再 ×1.10 才過；Lv50 未解（見 RANK_TABLE §4.1）'),
  ('D', 'D級', 3, 6.6, 2.2, 0.290, 0.290, '{}'::jsonb, '{}'::jsonb,
    '#2196f3', '技能＋裝備齊備即可輕鬆取勝', '校準基準 Lv10/30/50；Lv50 裸裝仍可能過關（不足條件方向對但未達嚴格門檻）'),
  ('C', 'C級', 4, 1.8, 9.0, 0.240, 0.240, '{}'::jsonb, '{}'::jsonb,
    '#9c27b0', '靠屬性相剋即可輕鬆取勝', '校準基準 Lv30；Lv10 過難、Lv50 過易（元素加成造成結構性上限，見 RANK_TABLE §4.1）'),
  ('B', 'B級', 5, 6, 6, 0.800, 0.800, '{}'::jsonb, '{}'::jsonb,
    '#ffb300', '需要精準防禦等戰鬥技巧才能取勝', '校準基準 Lv30（k=6）；通過窗口隨等級整段平移，Lv10 需×0.79、Lv50 需×1.29'),
  ('A', 'A級', 6, 11.5, 11.5, 1.530, 1.530,
    '{}'::jsonb, '{"waves":[{"at_hp_pct":60,"rank":"E","count":2,"monster_ids":[],"power_scale":0.15}]}'::jsonb,
    '#e53935', '單人全開手段打不贏，需組隊 3 人才能輕鬆取勝', '校準基準 Lv20-40（傭兵資料限制）；真實 Lv50 嚴重失效，heavy_knight 缺口最大'),
  ('SA', '特A級', 7, 14.2, 14.2, 1.890, 1.890,
    '{}'::jsonb, '{"waves":[{"at_hp_pct":70,"rank":"D","count":2,"monster_ids":[],"power_scale":0.1},{"at_hp_pct":35,"rank":"C","count":1,"monster_ids":[],"power_scale":0.1}]}'::jsonb,
    '#b71c1c', '需組隊 5 人才能輕鬆取勝', '校準基準 Lv20-40；無法驗證真實 Lv10/50（傭兵資料限制），p3 隨等級上升有破線風險'),
  ('S', 'S級', 8, 20, 10, 2.660, 2.660,
    '{}'::jsonb, '{"waves":[{"at_hp_pct":75,"rank":"D","count":3,"monster_ids":[],"power_scale":0.25},{"at_hp_pct":50,"rank":"B","count":2,"monster_ids":[],"power_scale":0.25},{"at_hp_pct":25,"rank":"A","count":1,"monster_ids":[],"power_scale":0.25}]}'::jsonb,
    '#6a1b9a', '裝備／技能／相剋／技巧／道具皆備＋滿隊，仍需努力 5–10 分鐘才可能取勝', '校準基準 Lv30（60%勝率／392s）；Lv40 太易，另有 Lv40 專用向量待評估未採用'),
  ('SS', '特S級', 9, 190, 6.5, 0.870, 0.870,
    '{}'::jsonb, '{"waves":[{"at_hp_pct":80,"rank":"C","count":2,"monster_ids":[],"power_scale":0.05},{"at_hp_pct":60,"rank":"B","count":2,"monster_ids":[],"power_scale":0.05},{"at_hp_pct":40,"rank":"A","count":1,"monster_ids":[],"power_scale":0.05},{"at_hp_pct":20,"rank":"B","count":2,"monster_ids":[],"power_scale":0.05},{"at_hp_pct":20,"rank":"A","count":1,"monster_ids":[],"power_scale":0.05}]}'::jsonb,
    '#212121', '同 S 級，需努力 20–30 分鐘才可能取勝', '校準基準 Lv30/40（47-50%勝率／約26分鐘）；Lv20 不過（低於 30% 下限）')
ON CONFLICT (rank) DO UPDATE SET
  label=EXCLUDED.label, sort_order=EXCLUDED.sort_order, hp_mult=EXCLUDED.hp_mult, atk_mult=EXCLUDED.atk_mult,
  def_mult=EXCLUDED.def_mult, mdef_mult=EXCLUDED.mdef_mult, level_curve=EXCLUDED.level_curve,
  summon=EXCLUDED.summon, badge_color=EXCLUDED.badge_color, description=EXCLUDED.description,
  calibrated_note=EXCLUDED.calibrated_note, updated_at=NOW();

-- ---------------------------------------------------------------------------
-- 2) rpg_monsters：rank 加 FK（既有 A~E 五隻已合法，見檔頭）＋新增九隻分級怪。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_monsters DROP CONSTRAINT IF EXISTS rpg_monsters_rank_fkey;
ALTER TABLE rpg_monsters ADD CONSTRAINT rpg_monsters_rank_fkey FOREIGN KEY (rank) REFERENCES rpg_monster_ranks(rank);

-- 九隻分級怪：mult 全 1.0（強度改由 rank 向量承擔，見檔頭）；poster_url 沿用既有五隻怪物的圖
-- （F/E←荊棘毒蛾 E-0052、D←灰白獸人 D-0182、C←沙塵骷髏騎士 C-0229、B/SA←鋼鐵巨鉗蟹 B-0089、
-- A/S/SS←幽暗食人花首領 A-67000200001）——battle.go wireEnemy 只送 imageUrl=poster_url，前端
-- sprite 動畫由 poster_url 反解資料夾 id，沿用既有圖代表沿用既有的四動作戰鬥動畫，不需要新圖。
-- size/race 沿用來源怪物；weak_elements 依五行相剋（木→fire、土→water、金→fire、闇→light/fire；
-- 無屬性的 D 沒有天生弱點，維持空陣列）；is_boss 只有 SA/S/SS 為 TRUE（A 本身不是 boss 旗標，
-- CONTRACT §1：「A（A 圖，非首領旗標）」）；threat 依級遞增。
INSERT INTO rpg_monsters
  (id, name, rank, attribute, size, race, poster_url, hp_mult, atk_mult, def_mult, speed_mult, threat, is_boss, is_active, sort_order, weak_elements)
VALUES
  ('DOR-MON-R-F',  '荊棘幼蛾',       'F',  '木', '小型', '昆蟲', '/ui/dorpg/mon/DOR-MON-E-0052.webp',        1,1,1,1,   5,   FALSE, TRUE, 10, ARRAY['fire']),
  ('DOR-MON-R-E',  '荊棘毒蛾·躁',     'E',  '木', '小型', '昆蟲', '/ui/dorpg/mon/DOR-MON-E-0052.webp',        1,1,1,1,  10,   FALSE, TRUE, 11, ARRAY['fire']),
  ('DOR-MON-R-D',  '灰白獸人·壯',     'D',  '無', '中型', '人形', '/ui/dorpg/mon/DOR-MON-D-0182.webp',        1,1,1,1,  20,   FALSE, TRUE, 12, ARRAY[]::text[]),
  ('DOR-MON-R-C',  '沙塵骷髏騎士·銳', 'C',  '土', '中型', '不死', '/ui/dorpg/mon/DOR-MON-C-0229.webp',        1,1,1,1,  30,   FALSE, TRUE, 13, ARRAY['water']),
  ('DOR-MON-R-B',  '鋼鐵巨鉗蟹·甲',   'B',  '金', '大型', '魚貝', '/ui/dorpg/mon/DOR-MON-B-0089.webp',        1,1,1,1,  40,   FALSE, TRUE, 14, ARRAY['fire']),
  ('DOR-MON-R-A',  '幽暗食人花',     'A',  '闇', '大型', '植物', '/ui/dorpg/mon/DOR-MON-A-67000200001.webp', 1,1,1,1, 100,   FALSE, TRUE, 15, ARRAY['light','fire']),
  ('DOR-MON-R-SA', '鋼鐵巨鉗蟹王',   'SA', '金', '大型', '魚貝', '/ui/dorpg/mon/DOR-MON-B-0089.webp',        1,1,1,1, 150,   TRUE,  TRUE, 16, ARRAY['fire']),
  ('DOR-MON-R-S',  '幽暗食人花魔王', 'S',  '闇', '大型', '植物', '/ui/dorpg/mon/DOR-MON-A-67000200001.webp', 1,1,1,1, 200,   TRUE,  TRUE, 17, ARRAY['light','fire']),
  ('DOR-MON-R-SS', '深淵食人花始祖', 'SS', '闇', '大型', '植物', '/ui/dorpg/mon/DOR-MON-A-67000200001.webp', 1,1,1,1, 300,   TRUE,  TRUE, 18, ARRAY['light','fire'])
ON CONFLICT (id) DO UPDATE SET
  name=EXCLUDED.name, rank=EXCLUDED.rank, attribute=EXCLUDED.attribute, size=EXCLUDED.size, race=EXCLUDED.race,
  poster_url=EXCLUDED.poster_url, hp_mult=EXCLUDED.hp_mult, atk_mult=EXCLUDED.atk_mult, def_mult=EXCLUDED.def_mult,
  speed_mult=EXCLUDED.speed_mult, threat=EXCLUDED.threat, is_boss=EXCLUDED.is_boss, is_active=EXCLUDED.is_active,
  sort_order=EXCLUDED.sort_order, weak_elements=EXCLUDED.weak_elements, updated_at=NOW();

-- ---------------------------------------------------------------------------
-- 3) rpg_encounters：新增四欄（scaling_mode/level_mode/rank/monster_count）。既有六場不 UPDATE
--    ——DEFAULT 'legacy'/'fixed'/NULL/NULL 已經是它們要的值（零改動，見檔頭）。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_encounters ADD COLUMN IF NOT EXISTS scaling_mode  TEXT NOT NULL DEFAULT 'legacy' CHECK (scaling_mode IN ('legacy','rank'));
ALTER TABLE rpg_encounters ADD COLUMN IF NOT EXISTS level_mode    TEXT NOT NULL DEFAULT 'fixed'  CHECK (level_mode IN ('fixed','player'));
ALTER TABLE rpg_encounters ADD COLUMN IF NOT EXISTS rank          TEXT NULL REFERENCES rpg_monster_ranks(rank);
ALTER TABLE rpg_encounters ADD COLUMN IF NOT EXISTS monster_count INT  NULL;

-- ---------------------------------------------------------------------------
-- 4) 20 場「強度挑戰」種子（scaling_mode='rank'、level_mode='player'）：F~B 各 ×1/×3/×5、
--    A ×1/×3、特A/S/特S ×1。scene_id 六場景輪用；scene_kind 只有 SA/S/SS 是 'boss'；
--    difficulty 依級（F/E=1、D/C=2、B=3、A=4、SA/S/SS=5）；power_scale：×1 為 1.0；×3 為 0.4、×5 為 0.3（2026-09-20 sim3：同倍率下 F×3 只剩 13% 勝、多數全滅，C/B 級為瓶頸取全域保守值）。其餘強度交給
--    rank 向量，之後要單場微調才動這欄）；monster_level 填 1（level_mode=player 時完全不使用，
--    只是讓 EncounterRow.Validate() 的既有邊界 1..99 保持滿足，供後台若誤把這場切回 fixed 時
--    仍有個合理預設）；escape_chance/can_escape 除 S/SS 外沿用既有 0.350/TRUE，S/SS 為 0/FALSE
--    （CONTRACT §1：「S／特S 不可」）。
-- ---------------------------------------------------------------------------

INSERT INTO rpg_encounters
  (code, title, subtitle, scene_id, scene_kind, difficulty, power_scale, monster_level, escape_chance, can_escape, is_active, sort_order, scaling_mode, level_mode, rank, monster_count)
VALUES
  ('rank_f_x1',  'F 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級', 'scene_taipei_101',      'normal', 1, 1.0, 1, 0.350, TRUE,  TRUE, 100, 'rank', 'player', 'F',  1),
  ('rank_f_x3',  'F 級・三隻', '強度挑戰．同級怪物 3 隻．Lv.＝你的等級', 'scene_fuhe_bridge',     'normal', 1, 0.4, 1, 0.350, TRUE,  TRUE, 101, 'rank', 'player', 'F',  3),
  ('rank_f_x5',  'F 級・五隻', '強度挑戰．同級怪物 5 隻．Lv.＝你的等級', 'scene_taipei_stadium',  'normal', 1, 0.3, 1, 0.350, TRUE,  TRUE, 102, 'rank', 'player', 'F',  5),
  ('rank_e_x1',  'E 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級', 'scene_ximending',       'normal', 1, 1.0, 1, 0.350, TRUE,  TRUE, 103, 'rank', 'player', 'E',  1),
  ('rank_e_x3',  'E 級・三隻', '強度挑戰．同級怪物 3 隻．Lv.＝你的等級', 'scene_tamsui_estuary',  'normal', 1, 0.4, 1, 0.350, TRUE,  TRUE, 104, 'rank', 'player', 'E',  3),
  ('rank_e_x5',  'E 級・五隻', '強度挑戰．同級怪物 5 隻．Lv.＝你的等級', 'scene_jiannan_mountain','normal', 1, 0.3, 1, 0.350, TRUE,  TRUE, 105, 'rank', 'player', 'E',  5),
  ('rank_d_x1',  'D 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級', 'scene_taipei_101',      'normal', 2, 1.0, 1, 0.350, TRUE,  TRUE, 106, 'rank', 'player', 'D',  1),
  ('rank_d_x3',  'D 級・三隻', '強度挑戰．同級怪物 3 隻．Lv.＝你的等級', 'scene_fuhe_bridge',     'normal', 2, 0.4, 1, 0.350, TRUE,  TRUE, 107, 'rank', 'player', 'D',  3),
  ('rank_d_x5',  'D 級・五隻', '強度挑戰．同級怪物 5 隻．Lv.＝你的等級', 'scene_taipei_stadium',  'normal', 2, 0.3, 1, 0.350, TRUE,  TRUE, 108, 'rank', 'player', 'D',  5),
  ('rank_c_x1',  'C 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級', 'scene_ximending',       'normal', 2, 1.0, 1, 0.350, TRUE,  TRUE, 109, 'rank', 'player', 'C',  1),
  ('rank_c_x3',  'C 級・三隻', '強度挑戰．同級怪物 3 隻．Lv.＝你的等級', 'scene_tamsui_estuary',  'normal', 2, 0.4, 1, 0.350, TRUE,  TRUE, 110, 'rank', 'player', 'C',  3),
  ('rank_c_x5',  'C 級・五隻', '強度挑戰．同級怪物 5 隻．Lv.＝你的等級', 'scene_jiannan_mountain','normal', 2, 0.3, 1, 0.350, TRUE,  TRUE, 111, 'rank', 'player', 'C',  5),
  ('rank_b_x1',  'B 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級', 'scene_taipei_101',      'normal', 3, 1.0, 1, 0.350, TRUE,  TRUE, 112, 'rank', 'player', 'B',  1),
  ('rank_b_x3',  'B 級・三隻', '強度挑戰．同級怪物 3 隻．Lv.＝你的等級', 'scene_fuhe_bridge',     'normal', 3, 0.4, 1, 0.350, TRUE,  TRUE, 113, 'rank', 'player', 'B',  3),
  ('rank_b_x5',  'B 級・五隻', '強度挑戰．同級怪物 5 隻．Lv.＝你的等級', 'scene_taipei_stadium',  'normal', 3, 0.3, 1, 0.350, TRUE,  TRUE, 114, 'rank', 'player', 'B',  5),
  ('rank_a_x1',  'A 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級（60% HP 召喚 2 隻 E 級）', 'scene_ximending',       'normal', 4, 1.0, 1, 0.350, TRUE,  TRUE, 115, 'rank', 'player', 'A',  1),
  ('rank_a_x3',  'A 級・三隻', '強度挑戰．同級怪物 3 隻．Lv.＝你的等級（每隻 60% HP 召喚 2 隻 E 級）', 'scene_tamsui_estuary',  'normal', 4, 0.4, 1, 0.350, TRUE,  TRUE, 116, 'rank', 'player', 'A',  3),
  ('rank_sa_x1', '特A 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級（召喚 D／C 級增援）', 'scene_jiannan_mountain','boss',   5, 1.0, 1, 0.350, TRUE,  TRUE, 117, 'rank', 'player', 'SA', 1),
  ('rank_s_x1',  'S 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級．不可逃跑（召喚 D／B／A 級增援）', 'scene_taipei_101',      'boss',   5, 1.0, 1, 0.000, FALSE, TRUE, 118, 'rank', 'player', 'S',  1),
  ('rank_ss_x1', '特S 級・單挑', '強度挑戰．同級怪物 1 隻．Lv.＝你的等級．不可逃跑（召喚 C／B／A 級增援）', 'scene_fuhe_bridge',     'boss',   5, 1.0, 1, 0.000, FALSE, TRUE, 119, 'rank', 'player', 'SS', 1)
ON CONFLICT (code) DO UPDATE SET
  title=EXCLUDED.title, subtitle=EXCLUDED.subtitle, scene_id=EXCLUDED.scene_id, scene_kind=EXCLUDED.scene_kind,
  difficulty=EXCLUDED.difficulty, power_scale=EXCLUDED.power_scale, monster_level=EXCLUDED.monster_level,
  escape_chance=EXCLUDED.escape_chance, can_escape=EXCLUDED.can_escape, is_active=EXCLUDED.is_active,
  sort_order=EXCLUDED.sort_order, scaling_mode=EXCLUDED.scaling_mode, level_mode=EXCLUDED.level_mode,
  rank=EXCLUDED.rank, monster_count=EXCLUDED.monster_count, updated_at=NOW();

-- rpg_encounter_monsters：槽位數由剛寫入的 monster_count 直接推導（1→front_center；
-- 3→front_left/front_center/front_right；5→全五格），保證「槽位數＝monster_count」恆成立
-- （不會有兩處各自維護、之後改一邊忘了改另一邊的風險——p11_test.go 對這個 CASE 對映與 20 場
-- monster_count 值分別做靜態核對）。monster_id 一律 'DOR-MON-R-'||rank（20 場都是同級同怪）。
INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale)
SELECT e.id, s.slot, 'DOR-MON-R-' || e.rank, 1.0
FROM rpg_encounters e
CROSS JOIN LATERAL unnest(
  CASE e.monster_count
    WHEN 1 THEN ARRAY['front_center']
    WHEN 3 THEN ARRAY['front_left','front_center','front_right']
    WHEN 5 THEN ARRAY['front_left','front_center','front_right','rear_left','rear_right']
  END
) AS s(slot)
WHERE e.scaling_mode = 'rank' AND e.level_mode = 'player' AND e.rank IS NOT NULL
ON CONFLICT (encounter_id, slot) DO UPDATE SET
  monster_id = EXCLUDED.monster_id, power_scale = EXCLUDED.power_scale;

INSERT INTO schema_migrations (version) VALUES ('189') ON CONFLICT DO NOTHING;
