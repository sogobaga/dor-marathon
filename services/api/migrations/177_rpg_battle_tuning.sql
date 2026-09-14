-- 177_rpg_battle_tuning.sql
-- DORPG P2 數值調校：把平衡模擬（scratchpad/dorpg_p2/BALANCE.md ＋ TUNE 三輪重跑）確定的最終數值
-- 套到「已經存在」的 seed 列上。
--
-- 為什麼需要這一份（而不是改 176 重跑）：176 的 seed 全部是 ON CONFLICT DO NOTHING，使用者在
-- 2026-09-14 已經把 176 套進正式庫、seed 列已存在——之後對 176 檔案裡 seed 數值的任何修改，重跑
-- 都不會生效（衝突就跳過）。所以調校後的數值必須用獨立一份 UPDATE 才會真的落地。176 檔案本身也
-- 已同步改成這裡的最終值，兩邊一致；新環境從零套 176 就直接是正確數值，套過 176 的環境再套 177。
--
-- 本檔是「對齊到正式值」而不是「相對差異」：每個欄位都直接寫死目標值，重複執行為 no-op（冪等）。
--
-- ===========================================================================
-- 2026-09-14 P2 修正第 1 輪重寫說明（審查 review/data.md 缺陷1、REBALANCE.md 根因修復：
-- 「參考 HP/MP 縮放」）：
--
-- 為什麼重寫這一份：data.md 缺陷1 發現原本的六場調校是針對「Base Lv=5／HP=300」的虛構角色校準，
-- 但正式庫擁有者帳號實際是 Base Lv=27／HPMax=919（exp=2339，對照真實 99 筆 level_config 門檻表）
-- ——同一瓶紅藥水/同一次治療在高等級角色身上的回血比例被嚴重稀釋。修法是新增
-- rpg_config.battle_reference_hp/battle_reference_mp 兩個欄位＋ScaleSkill/ScaleItem 縮放函式
-- （internal/rpg/scaling.go，TS 鏡像 apps/web/src/lib/dorpg/fixture.ts），讓 rpg_skills.flat
-- （heal/shield）與 rpg_items.amount（hp/mp）的語意從「絕對值」改成「參考玩家（HPMax=
-- battle_reference_hp、MPMax=battle_reference_mp）身上的絕對值」，依實際玩家 HPMax/MPMax 的比例
-- 縮放（clamp 0.25~8 倍）。
--
-- 本輪對「表格列數值」實際不需要新增任何 UPDATE（見下方查證）：
--   1) rpg_config 的兩個新欄位不落地在任何 SQL 表格欄位裡——它是 app_settings.value 底下一整包
--      JSON（key='rpg_config'）。ParseConfig()（config.go）採「先套 DefaultConfig() 預設值、
--      再 json.Unmarshal 覆蓋」的既有模式：正式庫目前存的 rpg_config JSON（本輪重寫前用 pg8000
--      唯讀查過，比照 scratchpad/q_terra.py 連線方式，未印出內容/連線字串）長度 1130 bytes、
--      確認完全不含 "battle_reference_hp"／"battle_reference_mp" 這兩個 key（TUNE 上一輪 PUT
--      /admin/rpg/config 存檔時，這兩個欄位在 Go struct 裡還不存在）——所以任何環境（新裝或已經
--      存過 rpg_config 的）讀到的都會是 Go 端 DefaultConfig() 給的 300/100，不需要、也不應該用
--      SQL 改 app_settings，改了反而要冒著把使用者手動調過的其餘 20 個 battle_* 欄位一併覆寫掉
--      的風險（app_settings.value 是整包 JSON，沒有能只更新單一 key 的安全 UPDATE 寫法）。
--   2) 唯讀重查正式庫目前的 rpg_items／rpg_encounters／rpg_encounter_monsters／rpg_skills／
--      rpg_monsters（schema_migrations 只有 176、還沒有 177——上一輪 177 從未被使用者實際套用
--      過），現況與下面既有的 UPDATE 目標值逐項核對：
--        rpg_items:      hp_potion.amount=300, mp_potion.amount=120, revive_feather.amount=50
--        rpg_encounters: power_scale = 0.550/0.800/1.000/1.200/1.400/1.600（六場依序，草案值）
--        rpg_encounter_monsters: tamsui_dusk.front_left=B-0089、jiannan_trail.front_left=B-0089、
--                                jiannan_trail.front_right=C-0229（皆為草案編組）
--        rpg_skills:  flat/mp_cost/coefficient 與 176 seed 完全相同（20/60/80/30/60，無漂移）
--        rpg_monsters: 四倍率與 176 seed 完全相同（無漂移）
--      也就是說，現況與 176 最終值之間的差異，仍然只有原本這三類（items/power_scale/
--      encounter_monsters 編組）——skills/monsters 兩張表完全沒有差異，不需要新增 UPDATE。
--   3) REBALANCE.md（本輪報告）用真實 P1 引擎＋這裡的最終數值，以三個等級（Lv5全新玩家／
--      Lv27擁有者現況／Lv50未來高等級）× 6 場 × 3 策略 × 150 個種子重新模擬，結論是「維持
--      power_scale／道具 amount／技能 flat 全部不動」——REBALANCE §7 用兩個獨立場次
--      （taipei101_boss、jiannan_trail）各自掃描過 power_scale／heal.flat 的鄰近值，證實
--      「調整這些值換取等級間一致性」的淨效益是負的（會讓已達標的其餘場次/等級掉出目標區間），
--      因此本輪不調整任何一個 power_scale/amount/flat，下面的 UPDATE 內容與重寫前完全相同。
--
-- 已知殘留落差（本輪修正的是缺陷1本身，不是新發現的次要問題，故不在本檔處理）：REBALANCE §5/§8
-- 診斷出「參考 HP 縮放」修好了 heal/道具的單次回復比例，但小咪（治療隊友）MP 池成長曲線
-- （mp_per_base_level=5）比玩家 HP 成長曲線（hp_per_base_level=30）平緩，heal 技能 mp_cost=20
-- 是固定值（本輪 SPEC 明講一律不動），導致高等級角色的小咪能施放的治療次數遠多於低等級角色——
-- 只在「幾乎不使用道具、依賴自動治療的長戰鬥」（BOSS 戰「蓄氣+防禦+補血」、難度2+ 場次「只點
-- 普攻」）才顯現，契約鼓勵的主玩法（蓄氣+防禦+補血）在難度1–4 全部 0.0pp 等級間差距，不受影響。
-- REBALANCE 已用契約允許的槓桿掃描過，找不到能同時解決又不犧牲其他目標的組合，留給下一輪決定是
-- 否解凍 heal.mp_cost 或替引擎加 MP 自然恢復（見 REBALANCE.md §8「建議留給下一輪決策」）。
-- ===========================================================================
--
-- 調校結論摘要（SIM_SEEDS=30，三種玩家 × 六場 × 三種操作策略 = 540 格模擬，無「>5 分鐘打不死」、
-- 無「<15 秒秒殺玩家」）：
--   training_ground 只點普攻 100% 勝率 / 38.6s；ximen_night 73% / 92.6s；fuhe_bridge 83% / 98.3s；
--   tamsui_dusk 77% / 115.1s（時長略超上限，但壓低 power_scale 會讓勝率衝到 83–100%、抹掉「不防禦
--   會輸」的風險，故保留）；jiannan_trail 73% / 130.9s；taipei101_boss 蓄氣+防禦+補血 50% / 203.7s，
--   只點普攻 0%（＝驗證「不用技能與防禦真的過不了」）。
--
-- 怪物四倍率（hp/atk/def/speed）與隊友倍率經模擬確認不需調整，故本檔不動 rpg_monsters／rpg_companions。

-- ---------------------------------------------------------------------------
-- 1) 道具回復量：原草案沿用 P0 展示值（紅 300／藍 120），對縮放後的玩家 HP 過強，一瓶就補滿、
--    讓「補血時機」完全沒有取捨。調到玩家 MaxHP 的約三分之一。
-- ---------------------------------------------------------------------------
UPDATE rpg_items SET amount = 150, updated_at = NOW() WHERE id = 'hp_potion'      AND amount IS DISTINCT FROM 150;
UPDATE rpg_items SET amount =  80, updated_at = NOW() WHERE id = 'mp_potion'      AND amount IS DISTINCT FROM 80;
UPDATE rpg_items SET amount =  50, updated_at = NOW() WHERE id = 'revive_feather' AND amount IS DISTINCT FROM 50;

-- ---------------------------------------------------------------------------
-- 2) 遭遇強度 power_scale：草案是「難度愈高數字愈大」的直覺值（0.55→1.6），但 D2 公式裡這個倍率
--    同時乘在怪物 HP 與 ATK 上，1.0 以上會讓戰鬥同時變長又變致命。模擬後的實際可玩區間是 0.46–0.73，
--    難度遞增改由「怪物編組」（換更硬的怪、填滿五格）承擔，而不是把倍率往上堆。
--    ⚠️ 因此 boss 場的 0.46 比訓練場的 0.60 還小是正確的：boss 自身 hp_mult=7.0，整場再乘大倍率會
--    變成打不死的肉山（P1 首領萬血就是這個問題）。
-- ---------------------------------------------------------------------------
UPDATE rpg_encounters SET power_scale = 0.600, updated_at = NOW() WHERE code = 'training_ground' AND power_scale IS DISTINCT FROM 0.600;
UPDATE rpg_encounters SET power_scale = 0.730, updated_at = NOW() WHERE code = 'ximen_night'     AND power_scale IS DISTINCT FROM 0.730;
UPDATE rpg_encounters SET power_scale = 0.600, updated_at = NOW() WHERE code = 'fuhe_bridge'     AND power_scale IS DISTINCT FROM 0.600;
UPDATE rpg_encounters SET power_scale = 0.550, updated_at = NOW() WHERE code = 'tamsui_dusk'     AND power_scale IS DISTINCT FROM 0.550;
UPDATE rpg_encounters SET power_scale = 0.520, updated_at = NOW() WHERE code = 'jiannan_trail'   AND power_scale IS DISTINCT FROM 0.520;
UPDATE rpg_encounters SET power_scale = 0.460, updated_at = NOW() WHERE code = 'taipei101_boss'  AND power_scale IS DISTINCT FROM 0.460;

-- ---------------------------------------------------------------------------
-- 3) 怪物編組修正：176 草案裡 tamsui_dusk／jiannan_trail 兩場的編組，與平衡模擬實際驗證的那一份
--    不同（模擬腳本用的是另一份編組）。不改的話，正式資料跟報告裡驗過的數字對不上，等於沒驗證。
--    tamsui_dusk：2C+2B+1D → 2C+2D+1B（front_left B→D）
--    jiannan_trail：3B+2C → 2B+2C+1D（front_left B→C、front_right C→D）
-- ---------------------------------------------------------------------------
UPDATE rpg_encounter_monsters m SET monster_id = 'DOR-MON-D-0182'
  FROM rpg_encounters e
 WHERE e.id = m.encounter_id AND e.code = 'tamsui_dusk' AND m.slot = 'front_left'
   AND m.monster_id IS DISTINCT FROM 'DOR-MON-D-0182';

UPDATE rpg_encounter_monsters m SET monster_id = 'DOR-MON-C-0229'
  FROM rpg_encounters e
 WHERE e.id = m.encounter_id AND e.code = 'jiannan_trail' AND m.slot = 'front_left'
   AND m.monster_id IS DISTINCT FROM 'DOR-MON-C-0229';

UPDATE rpg_encounter_monsters m SET monster_id = 'DOR-MON-D-0182'
  FROM rpg_encounters e
 WHERE e.id = m.encounter_id AND e.code = 'jiannan_trail' AND m.slot = 'front_right'
   AND m.monster_id IS DISTINCT FROM 'DOR-MON-D-0182';

INSERT INTO schema_migrations (version) VALUES ('177') ON CONFLICT DO NOTHING;
