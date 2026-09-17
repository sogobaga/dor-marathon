-- 180_rpg_jobs_skills.sql
-- DORPG P5：職業／測試等級／配點規則／技能等級——契約見
-- scratchpad/dorpg_p5/{CONTRACT.md,WIRE.md}（本檔只依契約 §7 建表/加欄＋灌 6 職業 seed，
-- 60 個職業技能與 5 隻既有怪物的 weak_elements 由另一個 Workflow 產生，貼進下方兩對標記之間）。
--
-- ⚠️ 使用者手動套用到 Neon，不得自行套到正式庫；本檔全部 CREATE TABLE IF NOT EXISTS /
-- ADD COLUMN IF NOT EXISTS、seed 全部 ON CONFLICT DO NOTHING，可安全重複執行。
--
-- ⚠️⚠️ 部署順序硬性要求（比 176 更嚴格）：176 當時新增的是全新的表，程式碼可以用
-- isMissingRelation(42P01) 攔截「表不存在」優雅降級成 503；這次改的是「既有表格加欄位」
-- （player_characters/rpg_skills/rpg_monsters），一旦程式碼部署但這份 migration 還沒套用，
-- SELECT job_id/test_level/... 這些新欄位會是 42703（欄位不存在），內容層現有的
-- isMissingRelation 攔不到 42703——/rpg/me 等既有端點會直接 500，不是優雅的 503。
-- 本輪程式碼務必等使用者確認已套用 180 才能 push（比照 db-before-code-push.md）。

-- ---------------------------------------------------------------------------
-- 1) rpg_jobs：六職業（DOR 正名，不照抄 RO），欄位對齊 CONTRACT §1 與 WIRE JobDTO。
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rpg_jobs (
  id                 TEXT PRIMARY KEY,          -- 契約 §1 固定 id：light_knight/archer/heavy_knight/cleric/merchant/mage
  name               TEXT NOT NULL,
  tagline            TEXT NOT NULL DEFAULT '',  -- 一句話定位
  description        TEXT NOT NULL DEFAULT '',
  path_a_id          TEXT NOT NULL,
  path_a_name        TEXT NOT NULL,
  path_a_desc        TEXT NOT NULL DEFAULT '',
  path_b_id          TEXT NOT NULL,
  path_b_name        TEXT NOT NULL,
  path_b_desc        TEXT NOT NULL DEFAULT '',
  weapon             TEXT NOT NULL DEFAULT 'sword',  -- sword|staff|bow|greatsword（既有四種視覺）
  atk_branch         TEXT NOT NULL DEFAULT 'melee',  -- melee|ranged（決定 Compute 素質物攻走哪個分支）
  recommended_stats  TEXT NOT NULL DEFAULT '',       -- 顯示用純文字，不做值域限制
  sort_order         INT NOT NULL DEFAULT 0,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- tagline/description 為本檔作者撰寫（契約明講「自己寫」）；recommended_stats 逐字取自契約 §1
-- 「重點配點」欄；weapon/atk_branch 逐字取自契約 §1 對照表。
INSERT INTO rpg_jobs (id, name, tagline, description, path_a_id, path_a_name, path_a_desc, path_b_id, path_b_name, path_b_desc, weapon, atk_branch, recommended_stats, sort_order) VALUES
  ('light_knight', '輕騎士', '劍與魔導兼修的平衡騎士',
    '近戰與魔法兩用的全能型騎士，攻守兼備，依路線側重劍技爆發或魔法應變。',
    'light_knight_sword', '劍技騎士', '專精劍技連段，追求近戰爆發輸出。',
    'light_knight_magic', '魔法騎士', '善用魔法強化自身與武器，戰場應變力強。',
    'sword', 'melee', 'STR + AGI + LUK（劍技騎士）／INT（魔法騎士）', 1),
  ('archer', '弓箭手', '疾風迅雷的遠程射手',
    '敏捷型遠程職業，善用弓箭與身法在戰場上牽制敵人、伺機收割。',
    'archer_sniper', '狙擊型', '強化攻擊速度，追求極限輸出節奏。',
    'archer_assassin', '刺客型', '強化迴避與身法，擅長閃避反擊。',
    'bow', 'ranged', 'AGI + LUK（狙擊型）／DEX（刺客型）', 2),
  ('heavy_knight', '重騎士', '一夫當關的重裝戰士',
    '力量型近戰職業，可走狂暴輸出或重裝防禦兩種極端路線。',
    'heavy_knight_berserker', '狂戰士', '捨棄防禦、專注最大化攻擊力。',
    'heavy_knight_guardian', '重裝騎士', '堆疊防禦與反擊，穩紮穩打。',
    'greatsword', 'melee', 'STR + LUK（狂戰士）／STR + VIT（重裝騎士）', 3),
  ('cleric', '聖職者', '守護隊伍的光之使者',
    '恢復型職業，透過治療與狀態強化維持隊伍續戰力。',
    'cleric_healer', '恢復免疫型', '強化治療量與異常抗性，隊伍生存的核心。',
    'cleric_support', '輔助強化型', '以增益技能提升隊友的戰鬥表現。',
    'staff', 'melee', 'INT + DEX（依技能樹）', 4),
  ('merchant', '商人', '精打細算的財富獵人',
    '發展型職業，技能圍繞資源與運氣經營，回報多半反映在戰鬥之外。',
    'merchant_trader', '賺錢型', '強化戰鬥外的收益效率。',
    'merchant_lucky', '幸運型', '強化掉落品質與運氣相關效果。',
    'sword', 'melee', 'VIT + LUK（依技能樹）', 5),
  ('mage', '魔法師', '毀滅性魔法的操縱者',
    '暴力型魔法職業，追求單體爆發或範圍殲滅的極致傷害。',
    'mage_single', '單體魔法型', '強化單體技能係數，追求最高單傷。',
    'mage_aoe', '範圍魔法型', '強化群體技能，掌控戰場範圍傷害。',
    'staff', 'melee', 'INT + DEX（依技能樹）', 6)
ON CONFLICT (id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2) player_characters：job_id（可隨時切換，測試階段不鎖定）＋ test_level（測試用，1~99）。
-- ---------------------------------------------------------------------------

ALTER TABLE player_characters ADD COLUMN IF NOT EXISTS job_id TEXT NULL REFERENCES rpg_jobs(id);
ALTER TABLE player_characters ADD COLUMN IF NOT EXISTS test_level INT NULL CHECK (test_level BETWEEN 1 AND 99);

-- ---------------------------------------------------------------------------
-- 3) rpg_skills：加欄支援職業技能樹（既有 5 個技能列 job_id 維持 NULL，供隊友／舊資料引用，
--    玩家技能欄只吃職業技能，見 CONTRACT §7）。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS job_id TEXT NULL REFERENCES rpg_jobs(id);
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS path TEXT NOT NULL DEFAULT '';        -- "a"|"b"（既有列留空字串）
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS tier INT NOT NULL DEFAULT 0;          -- 路線內第幾個（前置鏈序）
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS max_level INT NOT NULL DEFAULT 1;
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS effect JSONB NOT NULL DEFAULT '{}'::jsonb; -- CONTRACT §5 效果詞彙
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS dmg_type TEXT NOT NULL DEFAULT 'physical'; -- physical|magic
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS prereq_skill_id TEXT NULL;
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS prereq_level INT NOT NULL DEFAULT 0;
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS mp_cost_per_level NUMERIC(7,3) NOT NULL DEFAULT 0;
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS display_text TEXT NOT NULL DEFAULT '';
ALTER TABLE rpg_skills ADD COLUMN IF NOT EXISTS implemented BOOLEAN NOT NULL DEFAULT TRUE;

-- ---------------------------------------------------------------------------
-- 60 個職業技能 seed（另一個 Workflow 產生，貼在下方兩個標記之間；標記本身不要動）。
-- ---------------------------------------------------------------------------

-- >>> SKILLS SEED (generated) >>>
-- DORPG P5：六職業×10技能＝60筆，來源 scratchpad/dorpg_p5/skills_all.json
-- （VALIDATOR 產出，validate_skills.py 全過 FAIL=0；EXPORTER 產生本片段，
--  未跑過 migration，需人工拼進 migration 180 並在套用前再次確認欄位已存在）。
-- 既有 5 個技能（slash/fireball/heal/ice_lance/shield，job_id NULL）不受影響、原樣保留。
INSERT INTO rpg_skills (id, name, icon_id, kind, target, weapon, element, mp_cost, cooldown_ms, coefficient, flat, cast_ms, is_default, is_active, sort_order, job_id, path, tier, max_level, effect, dmg_type, prereq_skill_id, prereq_level, mp_cost_per_level, display_text, implemented) VALUES
  ('lk_a1', '街角連斬', '', 'damage', 'enemy', 'sword', 'neutral', 8, 3000, 1.0, 5, 300, FALSE, TRUE, 101, 'light_knight', 'a', 1, 10, '{"coef_base": 1.0, "coef_per_level": 0.05, "hits": 1, "element": "neutral", "target": "enemy", "flat_base": 5, "flat_per_level": 1}'::jsonb, 'physical', NULL, 0, 1, '近戰揮劍造成物理傷害，每級 +5% 係數、+1 固定傷害。', TRUE),
  ('lk_a2', '劍羽迴身', '', 'passive', 'self', 'sword', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 102, 'light_knight', 'a', 2, 10, '{"stat": "crit_pct", "value_base": 2, "value_per_level": 0.4}'::jsonb, 'physical', 'lk_a1', 3, 0, '被動提升暴擊率，每級 +0.4%。', TRUE),
  ('lk_a3', '疾風步伐', '', 'buff', 'self', 'sword', 'neutral', 14, 6000, 1, 0, 400, FALSE, TRUE, 103, 'light_knight', 'a', 3, 5, '{"stat": "aspd", "value_base": 8, "value_per_level": 3, "duration_ms": 10000, "target": "self"}'::jsonb, 'physical', 'lk_a2', 3, 2, '短暫提升自身攻擊速度，每級 +3%，持續10秒。', TRUE),
  ('lk_a4', '三段連斬', '', 'damage', 'enemy', 'sword', 'neutral', 22, 7000, 0.8, 8, 700, FALSE, TRUE, 104, 'light_knight', 'a', 4, 5, '{"coef_base": 0.8, "coef_per_level": 0.04, "hits": 3, "element": "neutral", "target": "enemy", "flat_base": 8, "flat_per_level": 1.5}'::jsonb, 'physical', 'lk_a3', 3, 3, '揮出三連斬（各段各自吃防禦），每級 +4% 係數、+1.5 固定傷害，是劍技路線核心連段技。', TRUE),
  ('lk_a5', '霓虹終擊', '', 'special', 'enemy', 'sword', 'neutral', 28, 8000, 1, 0, 1000, FALSE, TRUE, 105, 'light_knight', 'a', 5, 5, '{"text": "本輪未實裝：消耗前面連段的節奏能量發動終結一擊，未來規劃＝根據近期命中次數疊加傷害加成，並附帶短暫必定暴擊視窗。"}'::jsonb, 'physical', 'lk_a4', 5, 2, '路線終極技（本輪未實裝）：消耗連段節奏發動終結一擊，未來規劃＝依近期命中次數疊加傷害加成，並開啟短暫必定暴擊視窗。', FALSE),
  ('lk_b1', '街燈聚光彈', '', 'damage', 'enemy', 'sword', 'light', 12, 4000, 1.1, 8, 500, FALSE, TRUE, 201, 'light_knight', 'b', 1, 10, '{"coef_base": 1.1, "coef_per_level": 0.05, "hits": 1, "element": "light", "target": "enemy", "flat_base": 8, "flat_per_level": 1}'::jsonb, 'magic', NULL, 0, 1, '以路燈光波造成單體魔法傷害，每級 +5% 係數、+1 固定傷害。', TRUE),
  ('lk_b2', '夜讀符光', '', 'passive', 'self', 'sword', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 202, 'light_knight', 'b', 2, 10, '{"stat": "matk_pct", "value_base": 2, "value_per_level": 0.4}'::jsonb, 'magic', 'lk_b1', 3, 0, '被動提升魔法攻擊力，每級 +0.4%。', TRUE),
  ('lk_b3', '月光加持', '', 'buff', 'allAllies', 'sword', 'light', 18, 7000, 1, 0, 600, FALSE, TRUE, 203, 'light_knight', 'b', 3, 5, '{"stat": "matk_pct", "value_base": 6, "value_per_level": 2, "duration_ms": 12000, "target": "allAllies"}'::jsonb, 'magic', 'lk_b2', 3, 2, '為全隊灑下月光，提升全隊魔法攻擊力，每級 +2%，持續12秒。', TRUE),
  ('lk_b4', '破魔紋', '', 'debuff', 'enemy', 'sword', 'dark', 20, 6500, 1, 0, 500, FALSE, TRUE, 204, 'light_knight', 'b', 4, 5, '{"stat": "mdef_pct", "value_base": -8, "value_per_level": -2.5, "duration_ms": 8000, "target": "enemy"}'::jsonb, 'magic', 'lk_b3', 3, 2, '在敵人身上刻下破魔紋，降低其魔法防禦，每級 -2.5%，持續8秒。', TRUE),
  ('lk_b5', '街角結界', '', 'special', 'self', 'sword', 'light', 24, 8000, 1, 0, 800, FALSE, TRUE, 205, 'light_knight', 'b', 5, 5, '{"text": "本輪未實裝：施法期間展開路口結界，未來規劃＝詠唱中不會被打斷、並降低詠唱期間受到的傷害。"}'::jsonb, 'magic', 'lk_b4', 5, 2, '路線終極技（本輪未實裝）：施法期間展開路口結界，未來規劃＝詠唱中不會被打斷，並降低詠唱期間受到的傷害。', FALSE),
  ('ar_a1', '河濱速射', '', 'damage', 'enemy', 'bow', 'neutral', 9, 3200, 1.2, 6, 350, FALSE, TRUE, 101, 'archer', 'a', 1, 10, '{"coef_base": 1.2, "coef_per_level": 0.05, "hits": 1, "element": "neutral", "target": "enemy", "flat_base": 6, "flat_per_level": 1}'::jsonb, 'physical', NULL, 0, 1, '沿河濱快速放箭造成物理傷害，每級 +5% 係數、+1 固定傷害。', TRUE),
  ('ar_a2', '獵風直覺', '', 'passive', 'self', 'bow', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 102, 'archer', 'a', 2, 10, '{"stat": "aspd", "value_base": 2, "value_per_level": 0.4}'::jsonb, 'physical', 'ar_a1', 3, 0, '被動提升攻擊速度，每級 +0.4%。', TRUE),
  ('ar_a3', '鎖定訊標', '', 'buff', 'self', 'bow', 'neutral', 15, 6000, 1, 0, 400, FALSE, TRUE, 103, 'archer', 'a', 3, 5, '{"stat": "crit_pct", "value_base": 10, "value_per_level": 3, "duration_ms": 9000, "target": "self"}'::jsonb, 'physical', 'ar_a2', 3, 2, '標記目標弱點，提升自身暴擊率，每級 +3%，持續9秒。', TRUE),
  ('ar_a4', '三連珠', '', 'damage', 'enemy', 'bow', 'neutral', 24, 7500, 0.8, 7, 800, FALSE, TRUE, 104, 'archer', 'a', 4, 5, '{"coef_base": 0.8, "coef_per_level": 0.045, "hits": 3, "element": "neutral", "target": "enemy", "flat_base": 7, "flat_per_level": 1.5}'::jsonb, 'physical', 'ar_a3', 3, 3, '連續射出三箭（各自吃防禦），每級 +4.5% 係數、+1.5 固定傷害，是狙擊路線核心連段技。', TRUE),
  ('ar_a5', '終焉狙殺視窗', '', 'special', 'enemy', 'bow', 'neutral', 26, 8000, 1, 0, 600, FALSE, TRUE, 105, 'archer', 'a', 5, 5, '{"text": "本輪未實裝：開啟短暫時間窗，未來規劃＝視窗內下一擊必定暴擊，並額外造成一段無視防禦的真實傷害。"}'::jsonb, 'physical', 'ar_a4', 5, 2, '路線終極技（本輪未實裝）：開啟短暫時間窗，未來規劃＝視窗內下一擊必定暴擊，並額外造成一段無視防禦的真實傷害。', FALSE),
  ('ar_b1', '暗巷雙矢', '', 'damage', 'enemy', 'bow', 'dark', 10, 3500, 0.8, 4, 350, FALSE, TRUE, 201, 'archer', 'b', 1, 10, '{"coef_base": 0.8, "coef_per_level": 0.03, "hits": 2, "element": "dark", "target": "enemy", "flat_base": 4, "flat_per_level": 0.75}'::jsonb, 'physical', NULL, 0, 1, '連續射出雙箭造成物理傷害（各自吃防禦），每級 +3% 係數、+0.75 固定傷害。', TRUE),
  ('ar_b2', '夜行步法', '', 'passive', 'self', 'bow', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 202, 'archer', 'b', 2, 10, '{"stat": "flee", "value_base": 3, "value_per_level": 0.6}'::jsonb, 'physical', 'ar_b1', 3, 0, '被動提升迴避率，每級 +0.6%。', TRUE),
  ('ar_b3', '隱匿步伐', '', 'buff', 'self', 'bow', 'dark', 14, 6000, 1, 0, 400, FALSE, TRUE, 203, 'archer', 'b', 3, 5, '{"stat": "flee", "value_base": 10, "value_per_level": 3, "duration_ms": 10000, "target": "self"}'::jsonb, 'physical', 'ar_b2', 3, 2, '潛入陰影提升自身迴避率，每級 +3%，持續10秒。', TRUE),
  ('ar_b4', '煙幕撒放', '', 'debuff', 'enemy', 'bow', 'neutral', 18, 6500, 1, 0, 450, FALSE, TRUE, 204, 'archer', 'b', 4, 5, '{"stat": "hit", "value_base": -8, "value_per_level": -2.5, "duration_ms": 8000, "target": "enemy"}'::jsonb, 'physical', 'ar_b3', 3, 2, '撒放煙幕降低敵人命中率，每級 -2.5%，持續8秒。', TRUE),
  ('ar_b5', '殘影脫身', '', 'special', 'self', 'bow', 'dark', 22, 8000, 1, 0, 300, FALSE, TRUE, 205, 'archer', 'b', 5, 5, '{"text": "本輪未實裝：瞬間留下殘影脫離戰鬥焦點，未來規劃＝短暫必定迴避＋清除自身異常狀態並免疫下一次負面效果。"}'::jsonb, 'physical', 'ar_b4', 5, 2, '路線終極技（本輪未實裝）：瞬間留下殘影脫離戰鬥焦點，未來規劃＝短暫必定迴避，並清除自身異常狀態、免疫下一次負面效果。', FALSE),
  ('hk_a1', '重拳踏地', '', 'damage', 'enemy', 'greatsword', 'earth', 10, 3500, 1.05, 10, 400, FALSE, TRUE, 101, 'heavy_knight', 'a', 1, 10, '{"coef_base": 1.05, "coef_per_level": 0.05, "hits": 1, "element": "earth", "target": "enemy", "flat_base": 10, "flat_per_level": 1.5}'::jsonb, 'physical', NULL, 0, 1, '以蠻力重拳砸地造成物理傷害，每級 +5% 係數、+1.5 固定傷害。', TRUE),
  ('hk_a2', '蠻力本能', '', 'passive', 'self', 'greatsword', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 102, 'heavy_knight', 'a', 2, 10, '{"stat": "atk_pct", "value_base": 2, "value_per_level": 0.4}'::jsonb, 'physical', 'hk_a1', 3, 0, '被動提升攻擊力，每級 +0.4%。', TRUE),
  ('hk_a3', '狂怒咆哮', '', 'buff', 'self', 'greatsword', 'neutral', 16, 6500, 1, 0, 500, FALSE, TRUE, 103, 'heavy_knight', 'a', 3, 5, '{"stat": "atk_pct", "value_base": 10, "value_per_level": 3, "duration_ms": 9000, "target": "self"}'::jsonb, 'physical', 'hk_a2', 3, 2, '怒吼提升自身攻擊力，每級 +3%，持續9秒。', TRUE),
  ('hk_a4', '震地重擊', '', 'debuff', 'enemy', 'greatsword', 'earth', 20, 7000, 1, 0, 600, FALSE, TRUE, 104, 'heavy_knight', 'a', 4, 5, '{"stat": "aspd", "value_base": -8, "value_per_level": -2.5, "duration_ms": 8000, "target": "enemy"}'::jsonb, 'physical', 'hk_a3', 3, 2, '重擊震盪敵人，降低其攻擊速度，每級 -2.5%，持續8秒。', TRUE),
  ('hk_a5', '崩城怒擊', '', 'special', 'enemy', 'greatsword', 'earth', 28, 8000, 1, 0, 900, FALSE, TRUE, 105, 'heavy_knight', 'a', 5, 5, '{"text": "本輪未實裝：怒氣傾洩的越級重擊，未來規劃＝對主目標的超額傷害會溢散給場上其他敵人。"}'::jsonb, 'physical', 'hk_a4', 5, 2, '路線終極技（本輪未實裝）：怒氣傾洩的越級重擊，未來規劃＝對主目標的超額傷害會溢散給場上其他敵人。', FALSE),
  ('hk_b1', '盾甲衝撞', '', 'damage', 'enemy', 'greatsword', 'neutral', 9, 3500, 1.0, 8, 400, FALSE, TRUE, 201, 'heavy_knight', 'b', 1, 10, '{"coef_base": 1.0, "coef_per_level": 0.04, "hits": 1, "element": "neutral", "target": "enemy", "flat_base": 8, "flat_per_level": 1}'::jsonb, 'physical', NULL, 0, 1, '以厚重裝甲衝撞造成物理傷害，每級 +4% 係數、+1 固定傷害。', TRUE),
  ('hk_b2', '鋼鐵之軀', '', 'passive', 'self', 'greatsword', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 202, 'heavy_knight', 'b', 2, 10, '{"stat": "def_pct", "value_base": 2, "value_per_level": 0.4}'::jsonb, 'physical', 'hk_b1', 3, 0, '被動提升防禦力，每級 +0.4%。', TRUE),
  ('hk_b3', '堅守姿態', '', 'buff', 'self', 'greatsword', 'neutral', 16, 7000, 1, 0, 500, FALSE, TRUE, 203, 'heavy_knight', 'b', 3, 5, '{"stat": "damage_taken_pct", "value_base": -8, "value_per_level": -2, "duration_ms": 10000, "target": "self"}'::jsonb, 'physical', 'hk_b2', 3, 2, '擺出堅守姿態，降低自身受到的傷害，每級 +2%，持續10秒。', TRUE),
  ('hk_b4', '鐵壁護盾', '', 'shield', 'self', 'greatsword', 'neutral', 22, 8000, 1, 60, 700, FALSE, TRUE, 204, 'heavy_knight', 'b', 4, 5, '{"flat_base": 60, "flat_per_level": 15, "duration_ms": 12000}'::jsonb, 'physical', 'hk_b3', 3, 3, '展開護盾吸收傷害，每級 +15 點護盾量（以參考血量300為基準），持續12秒。', TRUE),
  ('hk_b5', '絕對反擊', '', 'special', 'self', 'greatsword', 'neutral', 24, 8000, 1, 0, 500, FALSE, TRUE, 205, 'heavy_knight', 'b', 5, 5, '{"text": "本輪未實裝：進入防禦姿態時，未來規劃＝格擋成功後對攻擊者觸發一次反擊，反擊傷害依自身防禦力換算。"}'::jsonb, 'physical', 'hk_b4', 5, 2, '路線終極技（本輪未實裝）：進入防禦姿態時，未來規劃＝格擋成功後對攻擊者觸發一次反擊，反擊傷害依自身防禦力換算。', FALSE),
  ('cl_a1', '巷口包紮', '', 'heal', 'ally', 'staff', 'neutral', 10, 4000, 1.0, 30, 500, FALSE, TRUE, 101, 'cleric', 'a', 1, 5, '{"coef_base": 1.0, "coef_per_level": 0.15, "flat_base": 30, "flat_per_level": 6, "target": "ally"}'::jsonb, 'magic', NULL, 0, 1, '立即為單一隊友包紮止血，依 MATK 與固定量回復生命值；每級 +0.15 倍係數、+6 點固定回復量。', TRUE),
  ('cl_a2', '運動貼紮術', '', 'buff', 'ally', 'staff', 'neutral', 12, 4500, 1, 0, 500, FALSE, TRUE, 102, 'cleric', 'a', 2, 5, '{"stat": "damage_taken_pct", "value_base": -8, "value_per_level": -2, "duration_ms": 8000, "target": "ally"}'::jsonb, 'magic', 'cl_a1', 3, 1, '為隊友貼上運動貼布與護具，持續 8 秒內減少其受到的傷害；每級再多減 2%。', TRUE),
  ('cl_a3', '補給站陣線', '', 'heal', 'allAllies', 'staff', 'neutral', 18, 6000, 0.8, 25, 700, FALSE, TRUE, 103, 'cleric', 'a', 3, 10, '{"coef_base": 0.8, "coef_per_level": 0.08, "flat_base": 25, "flat_per_level": 3, "target": "allAllies"}'::jsonb, 'magic', 'cl_a2', 3, 1, '在原地架起臨時補給站，一次治療全隊；每級 +0.08 倍係數、+3 點固定回復量。', TRUE),
  ('cl_a4', '抗壓體質', '', 'passive', 'self', 'staff', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 104, 'cleric', 'a', 4, 10, '{"stat": "hp_max_pct", "value_base": 3, "value_per_level": 0.6}'::jsonb, 'magic', 'cl_a3', 5, 0, '長期在第一線照護他人練就的體魄，永久提升自身最大生命值上限；每級 +0.6%。', TRUE),
  ('cl_a5', '不倒的意志', '', 'special', 'allAllies', 'staff', 'neutral', 26, 9000, 1, 0, 700, FALSE, TRUE, 105, 'cleric', 'a', 5, 5, '{"text": "短暫賦予全隊「異常免疫」狀態，免疫任何負面能力下降效果；等級提升可延長免疫持續時間（本輪引擎尚未實裝異常狀態與抵抗機制，先保留技能格與定位，數值曲線待狀態系統上線後套用）。"}'::jsonb, 'magic', 'cl_a4', 5, 2, '短暫賦予全隊「異常免疫」，免疫任何負面能力下降效果；等級提升可延長免疫持續時間（本輪引擎尚未實裝異常狀態與抵抗機制，先保留技能格與定位，是恢復免疫型的招牌技）。', FALSE),
  ('cl_b1', '加油吶喊', '', 'buff', 'ally', 'staff', 'neutral', 8, 3500, 1, 0, 400, FALSE, TRUE, 201, 'cleric', 'b', 1, 5, '{"stat": "atk_pct", "value_base": 8, "value_per_level": 2, "duration_ms": 10000, "target": "ally"}'::jsonb, 'magic', NULL, 0, 1, '對隊友大聲加油打氣，短暫提升其攻擊力；每級再多 +2%。', TRUE),
  ('cl_b2', '領航指引', '', 'buff', 'ally', 'staff', 'neutral', 10, 4000, 1, 0, 400, FALSE, TRUE, 202, 'cleric', 'b', 2, 5, '{"stat": "hit", "value_base": 10, "value_per_level": 2, "duration_ms": 10000, "target": "ally"}'::jsonb, 'magic', 'cl_b1', 3, 1, '為隊友指出敵人的破綻與路線，短暫提升命中率；每級再多 +2 點。', TRUE),
  ('cl_b3', '同步呼吸法', '', 'buff', 'allAllies', 'staff', 'neutral', 16, 6000, 1, 0, 600, FALSE, TRUE, 203, 'cleric', 'b', 3, 10, '{"stat": "matk_pct", "value_base": 5, "value_per_level": 1, "duration_ms": 10000, "target": "allAllies"}'::jsonb, 'magic', 'cl_b2', 3, 1, '帶領全隊調整呼吸節奏，短暫同步提升全隊魔法攻擊力；每級再多 +1%。', TRUE),
  ('cl_b4', '專注結界', '', 'special', 'allAllies', 'staff', 'neutral', 20, 7000, 1, 0, 600, FALSE, TRUE, 204, 'cleric', 'b', 4, 10, '{"text": "在隊伍周圍展開專注結界，短暫使隊友施法不會被打斷；等級提升可延長保護持續時間（本輪引擎尚未實裝詠唱中斷與保護機制，先保留技能格與定位，數值曲線待機制上線後套用）。"}'::jsonb, 'magic', 'cl_b3', 5, 1, '在隊伍周圍展開專注結界，短暫使隊友施法不會被打斷（本輪引擎尚未實裝詠唱中斷與保護機制，先保留技能格與定位）。', FALSE),
  ('cl_b5', '全隊起跑衝刺', '', 'buff', 'allAllies', 'staff', 'neutral', 28, 7000, 1, 0, 900, FALSE, TRUE, 205, 'cleric', 'b', 5, 5, '{"stat": "aspd", "value_base": 10, "value_per_level": 2, "duration_ms": 9000, "target": "allAllies"}'::jsonb, 'magic', 'cl_b4', 5, 0, '帶領全隊如鳴槍起跑般瞬間提速，短暫提升全隊攻擊速度；每級再多 +2%，是輔助強化路線的招牌技。', TRUE),
  ('mc_a1', '走跳江湖', '', 'passive', 'self', 'sword', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 101, 'merchant', 'a', 1, 5, '{"stat": "mdef_pct", "value_base": 3, "value_per_level": 0.6}'::jsonb, 'physical', NULL, 0, 0, '走跳商場多年練就的心防，永久小幅提升魔法防禦力，不容易被話術唬弄；每級 +0.6%。', TRUE),
  ('mc_a2', '殺價交涉', '', 'debuff', 'enemy', 'sword', 'neutral', 10, 4000, 1, 0, 500, FALSE, TRUE, 102, 'merchant', 'a', 2, 5, '{"stat": "atk_pct", "value_base": -8, "value_per_level": -2, "duration_ms": 8000, "target": "enemy"}'::jsonb, 'physical', 'mc_a1', 3, 1, '死纏爛打地跟對手殺價，擾亂其攻擊節奏，降低其攻擊力；每級再多降 2%。', TRUE),
  ('mc_a3', '顧攤蹲點', '', 'buff', 'self', 'sword', 'neutral', 16, 6000, 1, 0, 500, FALSE, TRUE, 103, 'merchant', 'a', 3, 10, '{"stat": "def_pct", "value_base": 10, "value_per_level": 1.5, "duration_ms": 10000, "target": "self"}'::jsonb, 'physical', 'mc_a2', 3, 1, '像顧攤一樣蹲穩腳步、寸步不讓，短暫提升自身防禦力；每級再多 +1.5%。', TRUE),
  ('mc_a4', '精打細算', '', 'special', 'self', 'sword', 'neutral', 18, 6500, 1, 0, 500, FALSE, TRUE, 104, 'merchant', 'a', 4, 10, '{"text": "戰鬥結束後，依表現額外獲得一筆 GP／經驗效益加成，等級愈高加成愈多（本輪引擎尚未實裝戰鬥經濟獎勵，先保留技能格與定位，數值曲線待經濟系統上線後套用）。"}'::jsonb, 'physical', 'mc_a3', 5, 1, '戰鬥結束後，依表現額外獲得一筆 GP／經驗效益加成，等級愈高加成愈多（本輪引擎尚未實裝戰鬥經濟獎勵，先保留技能格與定位）。', FALSE),
  ('mc_a5', '全店跳樓大拍賣', '', 'special', 'self', 'sword', 'neutral', 26, 8500, 1, 0, 800, FALSE, TRUE, 105, 'merchant', 'a', 5, 5, '{"text": "施展一次「跳樓大拍賣」，戰鬥結束後大幅提升本場戰利品的兌換價值，並在城鎮商店取得限時折扣，等級愈高效果愈強（本輪引擎尚未實裝經濟與商店系統，先保留技能格，是賺錢型路線的招牌技）。"}'::jsonb, 'physical', 'mc_a4', 5, 2, '施展一次「跳樓大拍賣」，戰鬥結束後大幅提升本場戰利品的兌換價值，並在城鎮商店取得限時折扣，是賺錢型路線的招牌技（本輪引擎尚未實裝經濟與商店系統，先保留技能格）。', FALSE),
  ('mc_b1', '發財金加持', '', 'passive', 'self', 'sword', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 201, 'merchant', 'b', 1, 5, '{"stat": "crit_pct", "value_base": 3, "value_per_level": 0.6}'::jsonb, 'physical', NULL, 0, 0, '隨身帶著發財金硬幣求個好兆頭，永久提升暴擊率；每級 +0.6%。', TRUE),
  ('mc_b2', '好彩頭護身', '', 'passive', 'self', 'sword', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 202, 'merchant', 'b', 2, 5, '{"stat": "flee", "value_base": 5, "value_per_level": 1}'::jsonb, 'physical', 'mc_b1', 3, 0, '隨身佩帶的護身符帶來好彩頭，永久提升迴避率；每級 +1 點。', TRUE),
  ('mc_b3', '鴻運當頭', '', 'buff', 'self', 'sword', 'neutral', 14, 5000, 1, 0, 400, FALSE, TRUE, 203, 'merchant', 'b', 3, 10, '{"stat": "hit", "value_base": 10, "value_per_level": 1.5, "duration_ms": 8000, "target": "self"}'::jsonb, 'physical', 'mc_b2', 3, 1, '感覺今天運氣特別好，短暫大幅提升命中率；每級再多 +1.5 點。', TRUE),
  ('mc_b4', '識破破綻', '', 'debuff', 'enemy', 'sword', 'neutral', 18, 6000, 1, 0, 500, FALSE, TRUE, 204, 'merchant', 'b', 4, 10, '{"stat": "flee", "value_base": -8, "value_per_level": -1, "duration_ms": 8000, "target": "enemy"}'::jsonb, 'physical', 'mc_b3', 5, 1, '精明的商人眼光看穿對手的破綻，降低其迴避率；每級再多降 1 點。', TRUE),
  ('mc_b5', '財神顯靈', '', 'special', 'self', 'sword', 'neutral', 24, 8000, 1, 0, 700, FALSE, TRUE, 205, 'merchant', 'b', 5, 5, '{"text": "祈求財神降臨，大幅提升本場戰鬥後的戰利品品質與稀有掉落機率，等級愈高效果愈強（本輪引擎尚未實裝掉落品質系統，先保留技能格，是幸運型路線的招牌技，數值曲線待掉落系統上線後套用）。"}'::jsonb, 'physical', 'mc_b4', 5, 2, '祈求財神降臨，大幅提升本場戰鬥後的戰利品品質與稀有掉落機率，等級愈高效果愈強，是幸運型路線的招牌技（本輪引擎尚未實裝掉落品質系統，先保留技能格）。', FALSE),
  ('mg_a1', '熱島效應', '', 'damage', 'enemy', 'staff', 'fire', 10, 4000, 0.85, 5, 400, FALSE, TRUE, 101, 'mage', 'a', 1, 5, '{"coef_base": 0.85, "coef_per_level": 0.15, "hits": 1, "element": "fire", "target": "enemy", "flat_base": 5, "flat_per_level": 2}'::jsonb, 'magic', NULL, 0, 1, '凝聚城市柏油路面蓄積的熱能，對敵人造成火屬性魔法傷害；每級 +0.15 倍係數、+2 點固定傷害。', TRUE),
  ('mg_a2', '訊號干擾', '', 'debuff', 'enemy', 'staff', 'neutral', 12, 4500, 1, 0, 500, FALSE, TRUE, 102, 'mage', 'a', 2, 5, '{"stat": "mdef_pct", "value_base": -7, "value_per_level": -2, "duration_ms": 8000, "target": "enemy"}'::jsonb, 'magic', 'mg_a1', 3, 1, '干擾敵人感知周遭魔力的能力，降低其魔法防禦力；每級再多降 2%。', TRUE),
  ('mg_a3', '路燈聚光', '', 'damage', 'enemy', 'staff', 'light', 18, 6000, 1.5, 15, 700, FALSE, TRUE, 103, 'mage', 'a', 3, 10, '{"coef_base": 1.5, "coef_per_level": 0.09, "hits": 1, "element": "light", "target": "enemy", "flat_base": 15, "flat_per_level": 2}'::jsonb, 'magic', 'mg_a2', 3, 1, '召喚整條街的路燈瞬間聚焦成一道光束，對單一敵人造成光屬性魔法傷害，是單體魔法型的核心技能；每級 +0.09 倍係數、+2 點固定傷害。', TRUE),
  ('mg_a4', '訊號放大器', '', 'passive', 'self', 'staff', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 104, 'mage', 'a', 4, 10, '{"stat": "matk_pct", "value_base": 4, "value_per_level": 0.6}'::jsonb, 'magic', 'mg_a3', 5, 0, '在體內建立穩定的訊號放大迴路，永久提升魔法攻擊力；每級 +0.6%。', TRUE),
  ('mg_a5', '城市電網過載', '', 'damage', 'enemy', 'staff', 'dark', 28, 8000, 1.0, 10, 1100, FALSE, TRUE, 105, 'mage', 'a', 5, 5, '{"coef_base": 1.0, "coef_per_level": 0.15, "hits": 2, "element": "dark", "target": "enemy", "flat_base": 10, "flat_per_level": 3}'::jsonb, 'magic', 'mg_a4', 5, 0, '引爆城市電網的過載能量，對單一敵人連續轟擊兩次，是單體魔法型的終極招牌技；每級 +0.15 倍係數、+3 點固定傷害。', TRUE),
  ('mg_b1', '驟雨特報', '', 'damage', 'allEnemies', 'staff', 'water', 12, 4500, 0.8, 2, 500, FALSE, TRUE, 201, 'mage', 'b', 1, 5, '{"coef_base": 0.8, "coef_per_level": 0.05, "hits": 1, "element": "water", "target": "allEnemies", "flat_base": 2, "flat_per_level": 0.5}'::jsonb, 'magic', NULL, 0, 1, '召喚一陣驟雨橫掃戰場，對所有敵人造成水屬性魔法傷害，是範圍魔法型的入門技能；每級 +0.05 倍係數、+0.5 點固定傷害。', TRUE),
  ('mg_b2', '路面結冰', '', 'debuff', 'allEnemies', 'staff', 'neutral', 14, 5000, 1, 0, 600, FALSE, TRUE, 202, 'mage', 'b', 2, 5, '{"stat": "aspd", "value_base": -6, "value_per_level": -1.5, "duration_ms": 8000, "target": "allEnemies"}'::jsonb, 'magic', 'mg_b1', 3, 1, '讓戰場路面瞬間結冰，全體敵人動作變得遲緩，攻擊速度下降；每級再多降 1.5%。', TRUE),
  ('mg_b3', '土石警戒', '', 'damage', 'allEnemies', 'staff', 'earth', 20, 6500, 0.82, 8, 800, FALSE, TRUE, 203, 'mage', 'b', 3, 10, '{"coef_base": 0.82, "coef_per_level": 0.06, "hits": 1, "element": "earth", "target": "allEnemies", "flat_base": 8, "flat_per_level": 1.2}'::jsonb, 'magic', 'mg_b2', 3, 1, '召喚土石崩落砸向整片戰場，對所有敵人造成地屬性魔法傷害，是範圍魔法型的核心技能；每級 +0.06 倍係數、+1.2 點固定傷害。', TRUE),
  ('mg_b4', '氣象觀測儲能', '', 'passive', 'self', 'staff', 'neutral', 0, 0, 1, 0, 0, FALSE, TRUE, 204, 'mage', 'b', 4, 10, '{"stat": "mp_max_pct", "value_base": 4, "value_per_level": 0.6}'::jsonb, 'magic', 'mg_b3', 5, 0, '建立個人氣象觀測站般的能量儲備，永久提升最大魔力值上限，讓範圍法術施放更持久；每級 +0.6%。', TRUE),
  ('mg_b5', '全境雷擊警報', '', 'damage', 'allEnemies', 'staff', 'dark', 28, 8000, 0.8, 5, 1200, FALSE, TRUE, 205, 'mage', 'b', 5, 5, '{"coef_base": 0.8, "coef_per_level": 0.03, "hits": 2, "element": "dark", "target": "allEnemies", "flat_base": 5, "flat_per_level": 2}'::jsonb, 'magic', 'mg_b4', 5, 0, '在整個戰場降下雷擊警報，對所有敵人連續轟擊兩次，是範圍魔法型的終極招牌技，真正的地圖兵器；每級 +0.03 倍係數、+2 點固定傷害。', TRUE)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, icon_id=EXCLUDED.icon_id, kind=EXCLUDED.kind, target=EXCLUDED.target, weapon=EXCLUDED.weapon, element=EXCLUDED.element, mp_cost=EXCLUDED.mp_cost, cooldown_ms=EXCLUDED.cooldown_ms, coefficient=EXCLUDED.coefficient, flat=EXCLUDED.flat, cast_ms=EXCLUDED.cast_ms, is_default=EXCLUDED.is_default, is_active=EXCLUDED.is_active, sort_order=EXCLUDED.sort_order, job_id=EXCLUDED.job_id, path=EXCLUDED.path, tier=EXCLUDED.tier, max_level=EXCLUDED.max_level, effect=EXCLUDED.effect, dmg_type=EXCLUDED.dmg_type, prereq_skill_id=EXCLUDED.prereq_skill_id, prereq_level=EXCLUDED.prereq_level, mp_cost_per_level=EXCLUDED.mp_cost_per_level, display_text=EXCLUDED.display_text, implemented=EXCLUDED.implemented;
-- <<< SKILLS SEED <<<

-- ---------------------------------------------------------------------------
-- 4) player_skill_levels：玩家每個技能目前等級（切職業時其他職業的分配保留、不計入目前職業的
--    已花費技能點，見 CONTRACT §4）。
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS player_skill_levels (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    skill_id    TEXT NOT NULL REFERENCES rpg_skills(id) ON DELETE CASCADE,
    level       INT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, skill_id)
);
CREATE INDEX IF NOT EXISTS idx_player_skill_levels_user ON player_skill_levels(user_id);

-- ---------------------------------------------------------------------------
-- 5) rpg_monsters：屬性相剋（CONTRACT §6：技能 element ∈ 怪物 weak_elements → 傷害
--    ×(1+battle_weakness_bonus_pct/100)）。既有 5 隻怪的相剋清單由另一個 Workflow 產生，
--    貼在下方兩個標記之間（UPDATE 陳述式，非 INSERT——五隻怪已經在 176 seed 過）。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_monsters ADD COLUMN IF NOT EXISTS weak_elements TEXT[] NOT NULL DEFAULT '{}';

-- >>> MONSTER WEAKNESS SEED >>>
-- DORPG P5：既有 5 隻怪物（176_rpg_battle_content.sql）補上 weak_elements。
-- 對照 60 技能中 damage kind 的 element 分布（neutral 44/60、dark 6、light 4、
-- earth 4、fire 1、water 1；damage 技能本身 element 分布＝neutral 5、dark 3、
-- light 2、earth 2、fire 1、water 1）：neutral 依慣例不設弱點對應（無元素基準值，
-- 不參與相剋加成），其餘 dark/light/earth/fire/water 五種在下列 5 隻怪物的
-- weak_elements 中都至少出現一次，確保每個元素的技能都有機會打出相剋 +25%。
-- metal/wood 兩桶本輪 60 技能沒有任何 damage 技能使用該元素，故不強求怪物弱點覆蓋。
-- 需搭配 migration 180 的 ALTER TABLE rpg_monsters ADD COLUMN IF NOT EXISTS
-- weak_elements TEXT[] NOT NULL DEFAULT '{}' 一起套用。

-- 幽暗食人花首領（DOR-MON-A-67000200001，屬性=闇(dark)）：闇屬性對位「光克闇」的常見設定；本體為植物系首領，追加火弱點以呼應法師系火系技能（mg_a1熱島效應等），讓首領戰有兩種可切入的元素策略。
UPDATE rpg_monsters SET weak_elements = ARRAY['light', 'fire'] WHERE id='DOR-MON-A-67000200001';

-- 鋼鐵巨鉗蟹（DOR-MON-B-0089，屬性=金(metal)）：五行「火克金」，烈焰熔穿鋼殼——標準對應，不重複使用其他弱點以維持單一克制路線。
UPDATE rpg_monsters SET weak_elements = ARRAY['fire'] WHERE id='DOR-MON-B-0089';

-- 沙塵骷髏騎士（DOR-MON-C-0229，屬性=土(earth)）：水沖蝕沙塵之軀，浸水後骨架結構瓦解；刻意不與 A 共用 light，讓 60 技能中的 water 系技能（mg_b1驟雨特報）有明確可打的目標。
UPDATE rpg_monsters SET weak_elements = ARRAY['water'] WHERE id='DOR-MON-C-0229';

-- 灰白獸人（DOR-MON-D-0182，屬性=無(neutral)）：補齊 60 技能中 earth 屬性（hk_a1重拳踏地、mg_b3土石警戒）尚缺對應弱點的怪物；灰白獸人設定為粗曠土系人形，懼怕地壓／土石崩塌效果。
UPDATE rpg_monsters SET weak_elements = ARRAY['earth'] WHERE id='DOR-MON-D-0182';

-- 荊棘毒蛾（DOR-MON-E-0052，屬性=木(wood)）：補齊 60 技能中 dark 屬性（ar_b1暗巷雙矢、mg_a5城市電網過載、mg_b5全境雷擊警報，共 3 個）尚缺對應弱點的怪物；夜蛾依光導航，黑暗使其迷失方向、防禦崩潰。
UPDATE rpg_monsters SET weak_elements = ARRAY['dark'] WHERE id='DOR-MON-E-0052';

-- <<< MONSTER WEAKNESS SEED <<<

INSERT INTO schema_migrations (version) VALUES ('180') ON CONFLICT DO NOTHING;
