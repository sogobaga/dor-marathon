-- 181_rpg_level_tavern.sql
-- DORPG P6：怪物等級制／酒館與隊伍／傭兵腳本——契約見
-- scratchpad/dorpg_p6/{CONTRACT.md,WIRE.md}。P5 的 rpg_jobs/rpg_skills.job_id 等（migration 180）
-- 仍是本檔的前置依賴（rpg_companion_presets 的驗證規則、四筆系統預設腳本的技能 id 都取自
-- migration 180 seed 的 60 個職業技能）。
--
-- ⚠️ 使用者手動套用到 Neon，不得自行套到正式庫；本檔全部 CREATE TABLE IF NOT EXISTS /
-- ADD COLUMN IF NOT EXISTS、UPDATE 皆帶 IS DISTINCT FROM 防重複寫、INSERT 皆 ON CONFLICT，
-- 可安全重複執行。
--
-- ⚠️⚠️ 部署順序硬性要求（同 180）：本檔改的是既有表加欄位（rpg_encounters/rpg_companions）
-- ＋兩張全新表；程式碼讀 monster_level/job_id（companions）/rpg_companion_presets/player_party
-- 這些新欄位/新表，push 前必須等使用者確認已套用本檔（比照 db-before-code-push.md）。

-- ---------------------------------------------------------------------------
-- 1) rpg_encounters.monster_level（契約 §2）：六場依序 10/20/30/40/50/60。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_encounters ADD COLUMN IF NOT EXISTS monster_level INT NOT NULL DEFAULT 10;

UPDATE rpg_encounters SET monster_level = 10, updated_at = NOW() WHERE code = 'training_ground' AND monster_level IS DISTINCT FROM 10;
UPDATE rpg_encounters SET monster_level = 20, updated_at = NOW() WHERE code = 'ximen_night'     AND monster_level IS DISTINCT FROM 20;
UPDATE rpg_encounters SET monster_level = 30, updated_at = NOW() WHERE code = 'fuhe_bridge'     AND monster_level IS DISTINCT FROM 30;
UPDATE rpg_encounters SET monster_level = 40, updated_at = NOW() WHERE code = 'tamsui_dusk'     AND monster_level IS DISTINCT FROM 40;
UPDATE rpg_encounters SET monster_level = 50, updated_at = NOW() WHERE code = 'jiannan_trail'   AND monster_level IS DISTINCT FROM 50;
UPDATE rpg_encounters SET monster_level = 60, updated_at = NOW() WHERE code = 'taipei101_boss'  AND monster_level IS DISTINCT FROM 60;

-- ---------------------------------------------------------------------------
-- 2) rpg_companions.job_id（契約 §3.1）：小咪 cleric、小優 archer、阿光 light_knight、
--    阿深 heavy_knight；小井（is_player_portrait）維持 NULL——不是傭兵。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_companions ADD COLUMN IF NOT EXISTS job_id TEXT NULL REFERENCES rpg_jobs(id);

UPDATE rpg_companions SET job_id = 'cleric',       updated_at = NOW() WHERE id = 'char_xiaomi'  AND job_id IS DISTINCT FROM 'cleric';
UPDATE rpg_companions SET job_id = 'archer',       updated_at = NOW() WHERE id = 'char_xiaoyou' AND job_id IS DISTINCT FROM 'archer';
UPDATE rpg_companions SET job_id = 'light_knight', updated_at = NOW() WHERE id = 'char_aguang'  AND job_id IS DISTINCT FROM 'light_knight';
UPDATE rpg_companions SET job_id = 'heavy_knight', updated_at = NOW() WHERE id = 'char_ashen'   AND job_id IS DISTINCT FROM 'heavy_knight';

-- ---------------------------------------------------------------------------
-- 3) rpg_companion_presets（契約 §3.1）：傭兵腳本——user_id NULL＝系統預設（唯讀）。
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rpg_companion_presets (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NULL REFERENCES users(id) ON DELETE CASCADE,  -- NULL＝系統預設（唯讀，不可 PUT/DELETE）
  companion_id  TEXT NOT NULL REFERENCES rpg_companions(id),
  name          TEXT NOT NULL,
  level         INT NOT NULL CHECK (level BETWEEN 1 AND 99),
  stats         JSONB NOT NULL DEFAULT '{}'::jsonb,          -- {str,agi,vit,dex,int,luk}
  skill_levels  JSONB NOT NULL DEFAULT '{}'::jsonb,          -- {skill_id: level}
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rpg_companion_presets_user_companion ON rpg_companion_presets(user_id, companion_id);

-- ---------------------------------------------------------------------------
-- 4) player_party（契約 §3.1）：使用者目前隊伍——沒有任何列＝預設隊伍＝小咪＋其系統預設腳本
--    （應用層 tavern.go defaultPartySlots() 判斷，這裡不需要為「預設」寫任何列）。
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS player_party (
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slot          INT NOT NULL CHECK (slot BETWEEN 1 AND 4),
  companion_id  TEXT NOT NULL REFERENCES rpg_companions(id),
  preset_id     UUID NULL REFERENCES rpg_companion_presets(id) ON DELETE SET NULL,  -- NULL＝用該傭兵的系統預設腳本
  PRIMARY KEY (user_id, slot),
  UNIQUE (user_id, companion_id)  -- 同一傭兵不可重複佔用兩格
);

-- ---------------------------------------------------------------------------
-- 5) 四筆系統預設腳本 seed（user_id NULL，名稱「預設」，Lv25）——契約 §3.1：
--    「由實作者依規則算出合法配置寫進 seed，並用同一套驗證函式在測試裡驗證合法」，見
--    internal/rpg/presets_test.go TestSystemPresetSeeds_AreValidAtLevel25（單一真相：
--    這裡的 JSON 數值與該測試的 Go 常數逐值相同，改一邊要記得改另一邊）。
--
-- Lv25：配點預算 170（TotalStatPoints，DefaultConfig 的 48/3/5）、cap 25（=min(99,25)）、
-- 技能點預算 24（TotalSkillPoints，DefaultConfig 的 0/1）。id 用固定 UUID（不是
-- gen_random_uuid()）才能配合 ON CONFLICT (id) DO NOTHING 冪等重跑。
--
-- 小咪（cleric，INT/DEX 高）：STR1/AGI1/VIT10/DEX20/INT25/LUK5（花費139/170）；
--   技能：cl_a1 巷口包紮 Lv5（滿）、cl_a2 運動貼紮術 Lv5（滿，前置 cl_a1>=3）、
--   cl_a3 補給站陣線 Lv10（滿，前置 cl_a2>=3）——共 20/24 技能點，逐字對應契約 §3.1
--   「小咪：INT/DEX 高＋巷口包紮 Lv5＋運動貼紮術＋補給站陣線」。
INSERT INTO rpg_companion_presets (id, user_id, companion_id, name, level, stats, skill_levels) VALUES
  ('00000000-0000-0000-0000-000000000101', NULL, 'char_xiaomi', '預設', 25,
    '{"str":1,"agi":1,"vit":10,"dex":20,"int":25,"luk":5}'::jsonb,
    '{"cl_a1":5,"cl_a2":5,"cl_a3":10}'::jsonb)
ON CONFLICT (id) DO NOTHING;

-- 小優（archer，AGI/DEX/LUK）：STR1/AGI25/VIT1/DEX20/INT1/LUK15（花費145/170）；
--   技能：ar_a1 河濱速射 Lv10（滿）、ar_a2 獵風直覺 Lv3（滿足 ar_a3 的前置門檻，非契約點名但
--   為 ar_a3 的必要前置鏈）、ar_a3 鎖定訊標 Lv5（滿，前置 ar_a2>=3）——共 18/24 技能點，涵蓋
--   契約點名的「河濱速射／鎖定訊標」。
INSERT INTO rpg_companion_presets (id, user_id, companion_id, name, level, stats, skill_levels) VALUES
  ('00000000-0000-0000-0000-000000000102', NULL, 'char_xiaoyou', '預設', 25,
    '{"str":1,"agi":25,"vit":1,"dex":20,"int":1,"luk":15}'::jsonb,
    '{"ar_a1":10,"ar_a2":3,"ar_a3":5}'::jsonb)
ON CONFLICT (id) DO NOTHING;

-- 阿光（light_knight，STR/AGI/LUK）：STR22/AGI20/VIT1/DEX1/INT1/LUK15（花費133/170）；
--   技能：lk_a1 街角連斬 Lv10（滿）、lk_a2 劍羽迴身 Lv9（前置 lk_a1>=3）、lk_a3 疾風步伐 Lv5
--   （滿，前置 lk_a2>=3）——共 24/24 技能點（滿額），逐字對應契約點名的三個技能。
INSERT INTO rpg_companion_presets (id, user_id, companion_id, name, level, stats, skill_levels) VALUES
  ('00000000-0000-0000-0000-000000000103', NULL, 'char_aguang', '預設', 25,
    '{"str":22,"agi":20,"vit":1,"dex":1,"int":1,"luk":15}'::jsonb,
    '{"lk_a1":10,"lk_a2":9,"lk_a3":5}'::jsonb)
ON CONFLICT (id) DO NOTHING;

-- 阿深（heavy_knight，STR/VIT）：STR25/AGI1/VIT25/DEX1/INT1/LUK1（花費132/170）；
--   技能：hk_b1 盾甲衝撞 Lv7、hk_b2 鋼鐵之軀 Lv6（前置 hk_b1>=3）、hk_b3 堅守姿態 Lv6
--   （前置 hk_b2>=3）、hk_b4 鐵壁護盾 Lv5（滿，前置 hk_b3>=3）——共 24/24 技能點（滿額），
--   逐字對應契約點名的四個技能。
INSERT INTO rpg_companion_presets (id, user_id, companion_id, name, level, stats, skill_levels) VALUES
  ('00000000-0000-0000-0000-000000000104', NULL, 'char_ashen', '預設', 25,
    '{"str":25,"agi":1,"vit":25,"dex":1,"int":1,"luk":1}'::jsonb,
    '{"hk_b1":7,"hk_b2":6,"hk_b3":6,"hk_b4":5}'::jsonb)
ON CONFLICT (id) DO NOTHING;

INSERT INTO schema_migrations (version) VALUES ('181') ON CONFLICT DO NOTHING;
