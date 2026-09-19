-- 187_rpg_p10_guardian.sql
-- DORPG P10（scratchpad/dorpg_p10/{CONTRACT.md,WIRE.md}）：傭兵換職業（小咪→魔法師／小優→
-- 聖職者／阿光→弓箭手）＋重騎士新增第三條技能路線「守護」（挑釁／仇恨／守護狀態）＋重騎士防禦
-- 拉高（防具 DEF 係數 1.7＋職業天生特性 damage_taken_pct −15%）。P5～P9 契約仍有效
-- （docs/dorpg/P5_*.md…P9_*.md），本檔只加不改。
--
-- ⚠️ 使用者手動套用到 Neon，不得自行套到正式庫；本檔全部 ADD COLUMN IF NOT EXISTS / INSERT ...
-- ON CONFLICT DO UPDATE / 冪等 UPDATE，可安全重複執行。
--
-- ⚠️⚠️ 部署順序硬性要求（同 180/181/183/184 等既有慣例，見 docs 記憶 db-before-code-push.md）：
-- 本檔改的是既有表格加欄位（rpg_jobs）——程式碼一旦部署但這份 migration 還沒套用，SELECT
-- path_c_id/traits 會是 42703（欄位不存在），本輪新增的 getJobByIDFull/listJobsFull 沒有對這個
-- 錯碼做優雅降級（isMissingRelation 只認 42P01 缺表），/rpg/me、/rpg/jobs、/rpg/tavern、
-- /rpg/skills 會直接 500。務必等使用者確認已套用本檔才能 push 程式碼。
--
-- HK 防具 DEF 重生（CONTRACT §2「防具 DEF 職業係數 1.3 → 1.7，重生 50 件重騎士防具的
-- profile.def」）與系統預設腳本的裝備/策略欄位交給 DESIGN／INTEGRATOR 對齊後拼進本檔下方標記
-- 之間（見檔案下段 HK ARMOR DEF SEED 標記）——本檔（BACKEND 負責的部分）只處理 rpg_jobs／
-- rpg_skills／rpg_companions／rpg_companion_presets 的 stats/skill_levels/equipment/
-- strategy_id 四樣。

-- ---------------------------------------------------------------------------
-- 1) rpg_jobs：第三條技能路線「守護」＋職業天生特性（CONTRACT §1/§2）。只有 heavy_knight 有
--    path_c_*；traits 目前只定義 damage_taken_pct 一項（Go 層 JobTraits，見 internal/rpg/
--    content.go），其餘五個職業維持 NULL/{}。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_jobs ADD COLUMN IF NOT EXISTS path_c_id   TEXT NULL;
ALTER TABLE rpg_jobs ADD COLUMN IF NOT EXISTS path_c_name TEXT NULL;
ALTER TABLE rpg_jobs ADD COLUMN IF NOT EXISTS path_c_desc TEXT NULL;
ALTER TABLE rpg_jobs ADD COLUMN IF NOT EXISTS traits      JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE rpg_jobs SET
  path_c_id   = 'heavy_knight_guardian_wall',
  path_c_name = '守護',
  path_c_desc = '吸引怪物仇恨、把攻擊全部攬在自己身上的守護者路線。',
  traits      = '{"damage_taken_pct":-15}'::jsonb,
  updated_at  = NOW()
WHERE id = 'heavy_knight'
  AND (path_c_id IS DISTINCT FROM 'heavy_knight_guardian_wall'
    OR traits IS DISTINCT FROM '{"damage_taken_pct":-15}'::jsonb);

-- ---------------------------------------------------------------------------
-- 2) rpg_skills：重騎士 path c「守護」四技能（CONTRACT §2 表格）。sort_order 接在既有 path b
--    的 hk_b5（205）之後，比照既有慣例 tier×100+path序（path a→1xx／path b→2xx／path c→3xx）。
--    kind=taunt 是 DORPG P10 新增的技能種類（internal/rpg/content.go validSkillKinds）；
--    effect JSONB 的 duration_base_ms/duration_per_level_ms/retarget/damage_taken_pct_base/
--    damage_taken_pct_per_level/guard_taunt 是這輪新增的效果詞彙（ExpandEffect，skills.go）。
-- ---------------------------------------------------------------------------

INSERT INTO rpg_skills (
  id, name, icon_id, kind, target, weapon, element, mp_cost, cooldown_ms, coefficient, flat, cast_ms,
  is_default, is_active, sort_order, job_id, path, tier, max_level, effect, dmg_type,
  prereq_skill_id, prereq_level, mp_cost_per_level, display_text, implemented
) VALUES
  ('hk_c1', '挑釁', '', 'taunt', 'self', 'greatsword', 'neutral', 8, 6000, 1, 0, 500,
    FALSE, TRUE, 301, 'heavy_knight', 'c', 1, 10,
    '{"duration_base_ms":4000,"duration_per_level_ms":500,"retarget":true}'::jsonb,
    'physical', NULL, 0, 1,
    '大喝一聲吸引所有敵人的攻擊目標鎖定自己，每級延長持續時間 0.5 秒。', TRUE),
  ('hk_c2', '守護本能', '', 'passive', 'self', 'greatsword', 'neutral', 0, 0, 1, 0, 0,
    FALSE, TRUE, 302, 'heavy_knight', 'c', 2, 10,
    '{"stat":"def_pct","value_base":3,"value_per_level":0.7,"guard_taunt":true}'::jsonb,
    'physical', 'hk_c1', 3, 0,
    '被動提升防禦力，每級 +0.7%；學會後按防禦（GUARD）期間會進入守護狀態，吸引怪物攻擊自己。', TRUE),
  ('hk_c3', '守護姿態', '', 'taunt', 'self', 'greatsword', 'neutral', 18, 12000, 1, 0, 600,
    FALSE, TRUE, 303, 'heavy_knight', 'c', 3, 5,
    '{"duration_base_ms":10000,"duration_per_level_ms":1000,"damage_taken_pct_base":-10,"damage_taken_pct_per_level":-2,"retarget":false}'::jsonb,
    'physical', 'hk_c2', 3, 2,
    '擺出守護姿態，進入守護狀態並降低自身受到的傷害，每級再多減 2%、延長 1 秒持續時間（不會改變敵人目前已鎖定的目標）。', TRUE),
  ('hk_c4', '守護誓約', '', 'buff', 'allAllies', 'greatsword', 'neutral', 22, 15000, 1, 0, 700,
    FALSE, TRUE, 304, 'heavy_knight', 'c', 4, 5,
    '{"stat":"damage_taken_pct","target":"allAllies","value_base":-8,"value_per_level":-2,"duration_ms":10000}'::jsonb,
    'physical', 'hk_c3', 3, 3,
    '向全隊立下守護誓約，短暫降低全體受到的傷害，每級再多減 2%，是守護路線的招牌技，讓隊友在自己吸引仇恨時更加安全。', TRUE)
ON CONFLICT (id) DO UPDATE SET
  name=EXCLUDED.name, icon_id=EXCLUDED.icon_id, kind=EXCLUDED.kind, target=EXCLUDED.target,
  weapon=EXCLUDED.weapon, element=EXCLUDED.element, mp_cost=EXCLUDED.mp_cost, cooldown_ms=EXCLUDED.cooldown_ms,
  coefficient=EXCLUDED.coefficient, flat=EXCLUDED.flat, cast_ms=EXCLUDED.cast_ms,
  is_default=EXCLUDED.is_default, is_active=EXCLUDED.is_active, sort_order=EXCLUDED.sort_order,
  job_id=EXCLUDED.job_id, path=EXCLUDED.path, tier=EXCLUDED.tier, max_level=EXCLUDED.max_level,
  effect=EXCLUDED.effect, dmg_type=EXCLUDED.dmg_type, prereq_skill_id=EXCLUDED.prereq_skill_id,
  prereq_level=EXCLUDED.prereq_level, mp_cost_per_level=EXCLUDED.mp_cost_per_level,
  display_text=EXCLUDED.display_text, implemented=EXCLUDED.implemented, updated_at=NOW();

-- ---------------------------------------------------------------------------
-- 3) rpg_companions：小咪→魔法師、小優→聖職者、阿光→弓箭手（CONTRACT §2「傭兵換職業只改
--    資料」）；阿深不動。skill_ids 是 P2 時代的舊欄位（P6 起玩家技能欄改用 job_id+rpg_skills，
--    這欄只剩後台顯示用途，見 content_repo.go companionCols 註解），比照契約字面一併更新。
-- ---------------------------------------------------------------------------

UPDATE rpg_companions SET
  job_id = 'mage', role = '法師', weapon = 'staff', skill_ids = '{}', updated_at = NOW()
WHERE id = 'char_xiaomi'
  AND (job_id IS DISTINCT FROM 'mage' OR role IS DISTINCT FROM '法師' OR weapon IS DISTINCT FROM 'staff' OR skill_ids IS DISTINCT FROM '{}');

UPDATE rpg_companions SET
  job_id = 'cleric', role = '治療', weapon = 'staff', skill_ids = '{heal}', matk_mult = 1.1, updated_at = NOW()
WHERE id = 'char_xiaoyou'
  AND (job_id IS DISTINCT FROM 'cleric' OR role IS DISTINCT FROM '治療' OR weapon IS DISTINCT FROM 'staff'
    OR skill_ids IS DISTINCT FROM '{heal}' OR matk_mult IS DISTINCT FROM 1.1);

UPDATE rpg_companions SET
  job_id = 'archer', role = '游擊', weapon = 'bow', updated_at = NOW()
WHERE id = 'char_aguang'
  AND (job_id IS DISTINCT FROM 'archer' OR role IS DISTINCT FROM '游擊' OR weapon IS DISTINCT FROM 'bow');

-- 阿深（char_ashen）維持 heavy_knight，本檔不動 rpg_companions 這一列。

-- ---------------------------------------------------------------------------
-- 4) 四筆系統預設腳本（migration 181 seed／182/186 修過，id 固定 UUID，user_id IS NULL）：
--    stats/skill_levels 依 CONTRACT §2 重配；equipment/strategy_id 三位換職業的隨之更新
--    （阿深不動，仍是 186 seed 的 hk_greatsword_t3 全套＋protect_allies）。單一真相：這裡的
--    JSON 數值必須跟 internal/rpg/presets_test.go 的 systemPresetSeeds()／
--    heavyKnightSkillsFixture()／magePathASkills() 逐值相同（TestSystemPresetSeeds_
--    AreValidAtLevel25 已經跑過這四筆配置，全數通過 ValidatePreset）。
-- ---------------------------------------------------------------------------

-- 小咪（char_xiaomi，現為 mage）：a 路線（傷害線）24 點；mg_staff_t3 + 五部位 mg_*_t3 + 飾品
-- acc_mp_t2/acc_mpregen_t2；element_advantage（魔法師打屬性相剋收益最高）。
UPDATE rpg_companion_presets SET
  stats = '{"str":1,"agi":1,"vit":10,"dex":20,"int":25,"luk":5}'::jsonb,
  skill_levels = '{"mg_a1":5,"mg_a2":5,"mg_a3":10,"mg_a4":4}'::jsonb,
  equipment = '{"weapon":"mg_staff_t3","helmet":"mg_helmet_t3","gloves":"mg_gloves_t3","armor":"mg_armor_t3","legs":"mg_legs_t3","boots":"mg_boots_t3","accessory1":"acc_mp_t2","accessory2":"acc_mpregen_t2"}'::jsonb,
  strategy_id = 'element_advantage',
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000101' AND user_id IS NULL;

-- 小優（char_xiaoyou，現為 cleric）：沿用原小咪的 cl_a1/a2/a3 配置（20 點）；cl_staff_t3 +
-- 五部位 cl_*_t3 + 飾品 acc_mpregen_t2/acc_hp_t2；protect_allies（治療型走保護隊友優先）。
UPDATE rpg_companion_presets SET
  stats = '{"str":1,"agi":1,"vit":10,"dex":20,"int":25,"luk":5}'::jsonb,
  skill_levels = '{"cl_a1":5,"cl_a2":5,"cl_a3":10}'::jsonb,
  equipment = '{"weapon":"cl_staff_t3","helmet":"cl_helmet_t3","gloves":"cl_gloves_t3","armor":"cl_armor_t3","legs":"cl_legs_t3","boots":"cl_boots_t3","accessory1":"acc_mpregen_t2","accessory2":"acc_hp_t2"}'::jsonb,
  strategy_id = 'protect_allies',
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000102' AND user_id IS NULL;

-- 阿光（char_aguang，現為 archer）：沿用原小優的 ar_a1/a2/a3 配置（18 點）；ar_longbow_t3 +
-- 五部位 ar_*_t3 + 飾品 acc_crit_t2/acc_agi_t2；focus_fire（集中火力輸出手）。
UPDATE rpg_companion_presets SET
  stats = '{"str":1,"agi":25,"vit":1,"dex":20,"int":1,"luk":15}'::jsonb,
  skill_levels = '{"ar_a1":10,"ar_a2":3,"ar_a3":5}'::jsonb,
  equipment = '{"weapon":"ar_longbow_t3","helmet":"ar_helmet_t3","gloves":"ar_gloves_t3","armor":"ar_armor_t3","legs":"ar_legs_t3","boots":"ar_boots_t3","accessory1":"acc_crit_t2","accessory2":"acc_agi_t2"}'::jsonb,
  strategy_id = 'focus_fire',
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000103' AND user_id IS NULL;

-- 阿深（char_ashen，heavy_knight 不動）：新增守護路線前三技能（hk_c1/c2/c3），合計 24 點
-- （5+6+3+3+3+4）；equipment/strategy_id 不動（186 seed 的 hk_greatsword_t3 全套 +
-- protect_allies）。
UPDATE rpg_companion_presets SET
  stats = '{"str":25,"agi":1,"vit":25,"dex":1,"int":1,"luk":1}'::jsonb,
  skill_levels = '{"hk_b1":5,"hk_b2":6,"hk_b3":3,"hk_c1":3,"hk_c2":3,"hk_c3":4}'::jsonb,
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000104' AND user_id IS NULL;

-- ---------------------------------------------------------------------------
-- 5) rpg_armor_items：重騎士 50 件防具 profile.def 依係數 1.7 重生（CONTRACT §2「防具 DEF
--    職業係數 1.3 → 1.7」）——DESIGN 依 scratchpad/dorpg_p8/gen_armor.py 重算後，INTEGRATOR
--    把逐件 UPDATE ... SET profile = jsonb_set(profile,'{def}', ...) 拼進下面兩個標記之間；
--    標記本身不要動。BACKEND（本檔其餘部分）不產生這些數值。
-- ---------------------------------------------------------------------------

-- >>> HK ARMOR DEF SEED (generated) >>>
-- DORPG P10：rpg_armor_items 重騎士 50 件 profile.def 依新係數 1.7 重生
-- 產生自 scratchpad/dorpg_p10/design/gen_armor_hk17.py（heavy_knight DEF 係數 1.3→1.7，
-- 其餘欄位／其他職業不動）；只動 profile 內的 'def' 鍵，VIT/HP%/STR/INT/AGI/DEX 副屬性
-- 與 rarity/level_req/name/description 全部不變，故用 jsonb_set 只覆寫單一鍵。
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t1';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t2';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '2'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t3';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '3'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t4';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '4'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t5';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '6'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t6';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '6'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t7';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '7'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t8';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '8'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t9';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '9'::jsonb), updated_at = NOW() WHERE id = 'hk_helmet_t10';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t1';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t2';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '2'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t3';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '3'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t4';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '4'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t5';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '5'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t6';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '5'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t7';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '6'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t8';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '7'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t9';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '8'::jsonb), updated_at = NOW() WHERE id = 'hk_gloves_t10';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t1';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '3'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t2';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '5'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t3';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '8'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t4';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '10'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t5';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '13'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t6';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '15'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t7';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '17'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t8';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '19'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t9';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '21'::jsonb), updated_at = NOW() WHERE id = 'hk_armor_t10';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t1';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '2'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t2';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '4'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t3';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '6'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t4';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '7'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t5';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '9'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t6';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '11'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t7';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '12'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t8';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '14'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t9';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '15'::jsonb), updated_at = NOW() WHERE id = 'hk_legs_t10';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t1';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '1'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t2';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '2'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t3';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '3'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t4';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '4'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t5';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '5'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t6';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '5'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t7';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '6'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t8';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '7'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t9';
UPDATE rpg_armor_items SET profile = jsonb_set(profile, '{def}', '8'::jsonb), updated_at = NOW() WHERE id = 'hk_boots_t10';
-- <<< HK ARMOR DEF SEED <<<

INSERT INTO schema_migrations (version) VALUES ('187') ON CONFLICT DO NOTHING;
