-- 188_rpg_p12_row_bonus.sql
-- DORPG P12（scratchpad/dorpg_p12/CONTRACT.md）：怪物前排／後排 × 武器排位加成——弓對後排
-- +10%、鈍器對前排 +10%、槍攻擊前排時機率貫穿波及同位置後排（50% 傷害）。P5～P10 契約仍有效
-- （docs/dorpg/P5_*.md…P10_*.md），本檔只加不改。
--
-- 排位（front/rear）本身不是新資料——直接由既有 rpg_encounter_monsters.slot 推導（Go 層
-- enemyRowFromSlot，battle.go），本檔完全不動 rpg_encounter_monsters。
--
-- 加成掛在 rpg_weapon_types.traits（P7 migration 183 已建欄位，JSONB NOT NULL DEFAULT '{}'）：
-- 只「新增」四個鍵，用 `traits || '{...}'::jsonb` 合併寫回，不動 P7 產生器留下的既有描述鍵
-- （style/special/positioning/size_bonus/…_range 等）。冪等：||運算子覆蓋同名鍵、其餘鍵不變，
-- 重複執行結果相同；WHERE 子句用 traits @> 判斷已套用就跳過，避免每次重跑都無意義地
-- UPDATE/updated_at。
--
-- ⚠️ 使用者手動套用到 Neon，不得自行套到正式庫。

-- ---------------------------------------------------------------------------
-- 1) 弓類（archer）：對後排怪物 +10% 傷害。
-- ---------------------------------------------------------------------------
UPDATE rpg_weapon_types
SET traits = traits || '{"row_bonus_rear_pct": 10}'::jsonb, updated_at = NOW()
WHERE id IN ('ar_longbow', 'ar_shortbow', 'ar_crossbow')
  AND NOT (traits @> '{"row_bonus_rear_pct": 10}'::jsonb);

-- ---------------------------------------------------------------------------
-- 2) 鈍器類（merchant）：對前排怪物 +10% 傷害。
-- ---------------------------------------------------------------------------
UPDATE rpg_weapon_types
SET traits = traits || '{"row_bonus_front_pct": 10}'::jsonb, updated_at = NOW()
WHERE id IN ('mc_hammer', 'mc_mallet', 'mc_club')
  AND NOT (traits @> '{"row_bonus_front_pct": 10}'::jsonb);

-- ---------------------------------------------------------------------------
-- 3) 槍（heavy_knight）：攻擊前排時 30% 機率貫穿，波及同位置後排 50% 傷害（CONTRACT §1：
--    機率預設 30%、後台可調——traits 是後台武器類型頁可編輯的既有欄位）。
-- ---------------------------------------------------------------------------
UPDATE rpg_weapon_types
SET traits = traits || '{"pierce_chance_pct": 30, "pierce_dmg_pct": 50}'::jsonb, updated_at = NOW()
WHERE id = 'hk_spear'
  AND NOT (traits @> '{"pierce_chance_pct": 30, "pierce_dmg_pct": 50}'::jsonb);

INSERT INTO schema_migrations (version) VALUES ('188') ON CONFLICT DO NOTHING;
