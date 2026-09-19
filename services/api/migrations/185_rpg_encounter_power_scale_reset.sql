-- 185_rpg_encounter_power_scale_reset.sql
-- DORPG 怪物難度校準（2026-09-19 使用者決策：怪物基礎能力＝同級參考玩家的 3–4 倍，因為怪物沒技能沒裝備）。
--
-- 為什麼要動資料：等級制公式維持 P6 契約
--   hp = floor(Ref(N).HPMax × hp_mult × battle_lvl_hp_ratio × encounter.power_scale × slotScale)（atk/def/mdef 同型）
-- 但六場遭遇的 power_scale（訓練場 0.60／西門町夜巡 0.73／福和橋下 0.60／淡水河口 0.55／劍南山步道 0.52／
-- 台北 101 首領戰 0.46）是 P2「power 模式」校準留下來的值，在等級制裡照樣乘進去，把一隻標準怪壓到同級參考
-- 玩家的 ATK 0.75 倍、HP 0.48 倍以下——而後台該欄位的說明又寫著「level 模式下不生效」，程式與說明矛盾，
-- 是 P6–P8 三輪模擬「怎麼穿防具都全勝」的隱藏主因。決議：**資料歸 1.0**（不改公式），怪物強度改由
-- rpg_config 的 battle_lvl_*_ratio（程式預設同版一起改成 ×N，見 config.go DefaultConfig）＋每隻怪物的
-- hp_mult/atk_mult/def_mult 決定；power_scale 之後只當「單場微調」用（1.0＝不調）。
--
-- 四個 battle_lvl_*_ratio 不在這裡寫進 app_settings.rpg_config：比照 migration 177 的做法，ParseConfig() 是
-- 「先套 DefaultConfig() 再 json.Unmarshal 覆蓋」，正式庫存的 rpg_config JSON 沒有這四個鍵（2026-09-19 唯讀
-- 查證），所以改程式預設值就會生效；後台「參數設定→戰鬥」已能看到並覆寫這四個值。
--
-- ⚠️ 使用者手動套用到 Neon；可重複執行（第二次 UPDATE 影響 0 列）。rpg_encounter_monsters.power_scale
-- （每槽個別倍率）目前六場全部已是 1.0，不動。

UPDATE rpg_encounters
   SET power_scale = 1.0, updated_at = NOW()
 WHERE power_scale <> 1.0;

INSERT INTO schema_migrations (version) VALUES ('185') ON CONFLICT DO NOTHING;
