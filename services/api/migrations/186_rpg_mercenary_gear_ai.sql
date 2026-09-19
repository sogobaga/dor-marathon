-- 186_rpg_mercenary_gear_ai.sql
-- DORPG P9（scratchpad/dorpg_p9/{CONTRACT.md,WIRE.md}）：傭兵裝備比照玩家＋AI 戰鬥策略（可擴充）
-- ＋玩家自動戰鬥。P5–P8 契約仍有效（docs/dorpg/P5_*.md…P8_*.md），本檔只加不改。
--
-- ⚠️ 使用者手動套用到 Neon；可重複執行。部署順序：本檔要先套用，程式碼才能安全讀
-- rpg_companion_presets.equipment/strategy_id、player_characters.auto_battle/auto_strategy_id、
-- rpg_ai_strategies（比照既有 db-before-code-push 慣例，見 docs 記憶 db-before-code-push.md）。
--
-- 順序刻意如下：先建 rpg_ai_strategies 這張新表＋seed 六列，兩張既有表的新欄位才能安全地
-- REFERENCES rpg_ai_strategies(id)（FK 目標表必須先存在）。

-- ---------------------------------------------------------------------------
-- 1) rpg_ai_strategies（CONTRACT §2）：AI 戰鬥策略的顯示名稱/說明/參數覆寫/啟用/排序。
--    行為本身寫在引擎 engine/strategies.ts 的 registry，這張表只管「調門檻與文案」，不能新增
--    引擎沒有的行為（CONTRACT §1）——id 值域＝引擎 STRATEGY_IDS 白名單，後端 Go 常數
--    STRATEGY_IDS（strategies.go）同步維護同一份六個 id。
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS rpg_ai_strategies (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  params      JSONB NOT NULL DEFAULT '{}'::jsonb,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  sort_order  INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- >>> AI STRATEGIES SEED (generated) >>>
-- 六種策略（CONTRACT §4，使用者原話：1.MP保守使用 2.技能積極使用 3.保護隊友優先 4.集中火力
-- 5.屬性相剋優先，＋均衡＝現行五段優先序）。params 是引擎預設值的「初始覆寫」，數字跟契約
-- §4 逐一對照（heal_pct/self_heal_floor_pct/mp_reserve_pct/emergency_heal_pct/
-- min_mp_reserve_pct/heal_pct/shield_pct/auto_guard）——後台可調，引擎那邊拿它跟自己的
-- registry 預設值 merge（resolveStrategy），這裡先塞跟引擎預設一致的值，等於「初始不覆寫任何
-- 效果」，純粹讓後台 PUT 有東西可編輯。
INSERT INTO rpg_ai_strategies (id, name, description, params, is_active, sort_order) VALUES
  ('balanced', '均衡', '現行五段優先序（治療＞護盾／減傷＞傷害＞削弱＞普攻），目標跟隨玩家鎖定的敵人。',
    '{"heal_pct":50,"self_heal_floor_pct":15,"auto_guard":true}'::jsonb, TRUE, 1),
  ('mp_conserve', 'MP 保守使用', 'MP 低於門檻時不放技能改普攻，緊急情況（隊友瀕死）仍會治療。',
    '{"mp_reserve_pct":50,"emergency_heal_pct":30,"auto_guard":false}'::jsonb, TRUE, 2),
  ('skill_aggressive', '技能積極使用', '開場先上增益，之後有傷害技能就放、選預期傷害最高者，沒有才普攻。',
    '{"min_mp_reserve_pct":0,"auto_guard":false}'::jsonb, TRUE, 3),
  ('protect_allies', '保護隊友優先', '優先治療與保護血量最低的隊友，有護盾/減傷/嘲諷類技能會優先用在他們身上。',
    '{"heal_pct":60,"shield_pct":75,"auto_guard":true}'::jsonb, TRUE, 4),
  ('focus_fire', '集中火力', '全隊鎖定場上血量絕對值最低的敵人，直到目標死亡才換。',
    '{"auto_guard":false}'::jsonb, TRUE, 5),
  ('element_advantage', '屬性相剋優先', '鎖定屬性相剋倍率最高的敵人，優先使用對其相剋的傷害技能。',
    '{"auto_guard":false}'::jsonb, TRUE, 6)
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  description = EXCLUDED.description,
  params = EXCLUDED.params,
  is_active = EXCLUDED.is_active,
  sort_order = EXCLUDED.sort_order,
  updated_at = NOW();
-- <<< AI STRATEGIES SEED <<<

-- ---------------------------------------------------------------------------
-- 2) rpg_companion_presets（CONTRACT §2）：新增 equipment（八格 item_id，缺鍵＝空，應用層驗證，
--    無 FK——同一欄位可能指向 rpg_weapons 或 rpg_armor_items，無法用單一 FK 表達，比照 migration
--    184 對 player_equipment.item_id 拿掉 FK 的既有理由）與 strategy_id。
-- ---------------------------------------------------------------------------

ALTER TABLE rpg_companion_presets ADD COLUMN IF NOT EXISTS equipment JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE rpg_companion_presets ADD COLUMN IF NOT EXISTS strategy_id TEXT NOT NULL DEFAULT 'balanced' REFERENCES rpg_ai_strategies(id);

-- ---------------------------------------------------------------------------
-- 3) player_characters（CONTRACT §1）：玩家自動戰鬥開關與策略，比照 migration 180 的
--    ALTER … ADD COLUMN IF NOT EXISTS 風格。
-- ---------------------------------------------------------------------------

ALTER TABLE player_characters ADD COLUMN IF NOT EXISTS auto_battle BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE player_characters ADD COLUMN IF NOT EXISTS auto_strategy_id TEXT NOT NULL DEFAULT 'balanced' REFERENCES rpg_ai_strategies(id);

-- ---------------------------------------------------------------------------
-- 4) 四筆系統預設腳本（user_id IS NULL，migration 181 seed，Lv25）補上裝備與策略
--    （CONTRACT §2）：裝備＝該傭兵職業在 Lv25 可裝的最高級（武器/五部位防具 t3、飾品 t2，逐值
--    對照 migration 183/184 seed 的 id 命名慣例 `<job短碼>_<slot>_t<tier>`；t3 武器/防具
--    level_req=20、t2 飾品 level_req=20，皆 ≤ 25，presets_equipment_test.go 有回歸測試核對
--    這些 id 確實存在於 183/184 的 SQL）；策略依角色定位指派（阿光 balanced／阿深
--    protect_allies／小咪 mp_conserve／小優 focus_fire，CONTRACT §2）。武器＝各職業
--    rpg_weapon_types sort_order 第一種（light_knight→lk_sword／archer→ar_longbow／
--    cleric→cl_staff／heavy_knight→hk_greatsword）的 t3。
-- ---------------------------------------------------------------------------

-- 小咪（char_xiaomi，cleric）：cl_staff_t3 + 五部位 cl_*_t3 + 飾品 acc_mpregen_t2/acc_mp_t2；
-- mp_conserve。
UPDATE rpg_companion_presets SET
  equipment = '{"weapon":"cl_staff_t3","helmet":"cl_helmet_t3","gloves":"cl_gloves_t3","armor":"cl_armor_t3","legs":"cl_legs_t3","boots":"cl_boots_t3","accessory1":"acc_mpregen_t2","accessory2":"acc_mp_t2"}'::jsonb,
  strategy_id = 'mp_conserve',
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000101' AND user_id IS NULL;

-- 小優（char_xiaoyou，archer）：ar_longbow_t3 + 五部位 ar_*_t3 + 飾品 acc_crit_t2/acc_agi_t2；
-- focus_fire。
UPDATE rpg_companion_presets SET
  equipment = '{"weapon":"ar_longbow_t3","helmet":"ar_helmet_t3","gloves":"ar_gloves_t3","armor":"ar_armor_t3","legs":"ar_legs_t3","boots":"ar_boots_t3","accessory1":"acc_crit_t2","accessory2":"acc_agi_t2"}'::jsonb,
  strategy_id = 'focus_fire',
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000102' AND user_id IS NULL;

-- 阿光（char_aguang，light_knight）：lk_sword_t3 + 五部位 lk_*_t3 + 飾品
-- acc_dmgtaken_t2/acc_hp_t2；balanced。
UPDATE rpg_companion_presets SET
  equipment = '{"weapon":"lk_sword_t3","helmet":"lk_helmet_t3","gloves":"lk_gloves_t3","armor":"lk_armor_t3","legs":"lk_legs_t3","boots":"lk_boots_t3","accessory1":"acc_dmgtaken_t2","accessory2":"acc_hp_t2"}'::jsonb,
  strategy_id = 'balanced',
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000103' AND user_id IS NULL;

-- 阿深（char_ashen，heavy_knight）：hk_greatsword_t3 + 五部位 hk_*_t3 + 飾品
-- acc_hp_t2/acc_vit_t2；protect_allies。
UPDATE rpg_companion_presets SET
  equipment = '{"weapon":"hk_greatsword_t3","helmet":"hk_helmet_t3","gloves":"hk_gloves_t3","armor":"hk_armor_t3","legs":"hk_legs_t3","boots":"hk_boots_t3","accessory1":"acc_hp_t2","accessory2":"acc_vit_t2"}'::jsonb,
  strategy_id = 'protect_allies',
  updated_at = NOW()
WHERE id = '00000000-0000-0000-0000-000000000104' AND user_id IS NULL;

INSERT INTO schema_migrations (version) VALUES ('186') ON CONFLICT DO NOTHING;
