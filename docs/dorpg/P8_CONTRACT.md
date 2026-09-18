# DORPG P8 契約：防具系統（頭盔／手套／衣服／褲裙／鞋子 依職業各一套＋通用飾品×2）— 2026-09-19（含使用者補充）

單一真相。P5–P7 契約仍有效（`docs/dorpg/P5_*.md`…`P7_*.md`），本檔只加不改。

## 0. 使用者需求（原話濃縮）
- 部位：頭盔×1、手套×1、衣服×1、褲裙×1、鞋子×1、飾品×2。
- 衣服與褲裙＝DEF 主要來源，另加 VIT／HP；頭盔＝DEF＋INT；手套＝DEF＋STR；鞋子＝DEF＋AGI／DEX。
- 飾品不加 DEF，可加：HP／MP／STR／VIT／DEX／AGI／INT／LUK／降低攻擊間隔／爆擊率／爆擊傷害／減少 MP 消耗%／定時恢復 HP／定時恢復 MP／增加 ATK／增加 MATK／減少 HP 傷害／屬性傷害降 5%。
- 頭盔、手套、衣服、褲裙、鞋子各 10 級（每級 1 件）；飾品每種效果各 1 件（共 18 件）。
- **補充一**：五部位防具**每個職業都有對應的一套**，數值依職業不同（例：重騎士 DEF 高、魔法師 DEF 低）；**飾品通用**，各職業都可戴。
- **補充二**：通用飾品**分 5 級**，每級給予不同程度的數值（18 種效果 × 5 級＝90 件）。

## 1. 資料模型
- `rpg_armor_items`：`id TEXT PK`（防具 `<job短碼>_<slot>_t<tier>`，短碼 lk/ar/hk/cl/mc/mg；飾品 `acc_<effect>_t<tier>`）, `job_id TEXT NULL REFERENCES rpg_jobs(id)`（**NULL＝通用**，只有飾品為 NULL）, `slot TEXT`（helmet|gloves|armor|legs|boots|accessory）, `tier INT`（防具 1..10；飾品 1..5）, `name`, `rarity`（防具 tier 1–3 common／4–6 rare／7–9 epic／10 legendary；飾品 t1 common／t2–3 rare／t4 epic／t5 legendary）, `level_req INT`, `profile JSONB`（§2）, `description`, `is_active`, `sort_order`, timestamps。索引 (job_id, slot, tier)。
- `player_equipment`（既有）：slot CHECK 由 `('weapon')` 擴成 `('weapon','helmet','gloves','armor','legs','boots','accessory1','accessory2')`；**移除** `item_id` 對 rpg_weapons 的 FK（正式庫約束名 `player_equipment_item_id_fkey`、`player_equipment_slot_check`；migration 184 用 `DROP CONSTRAINT IF EXISTS` 再重建 CHECK），改由應用層依 slot 驗證 item 存在（weapon 查 rpg_weapons，其餘查 rpg_armor_items）；讀取時查無 item → 視為未裝備（不 500，log 一行）。
- 取得與裝備規則（測試階段）：防具必須 `job_id`＝目前職業（否則 `wrong_job`），飾品不限；唯一條件＝有效等級 ≥ level_req；飾品兩格不可裝同一件（`duplicate_accessory`）。**換職業時自動卸下五部位防具（與武器同一交易），飾品保留**。傭兵本輪不裝備。

## 2. ArmorProfile（每件防具的 profile JSON；缺省＝中性）
```
{ "def": n, "str": n, "agi": n, "vit": n, "dex": n, "int": n, "luk": n,
  "hp_pct": n, "mp_pct": n, "atk_pct": n, "matk_pct": n,
  "interval_pct": n,            // 負＝攻擊間隔縮短（與武器 interval_pct 相加）
  "crit_pct": n, "crit_dmg_pct": n,
  "mp_cost_reduce_pct": n,      // 技能 MP 消耗 ×(1−pct/100)，floor，下限 1
  "hp_regen_pct_per_5s": n,     // 每 5 秒回復 HPMax 的 n%（floor，戰鬥中持續）
  "mp_regen_pct_per_5s": n,
  "damage_taken_pct": n,        // 負＝少受傷（與 buff 的 damage_taken_pct 相加）
  "element_resist_pct": n }     // 與武器（鍊）相加
```
**彙總規則**（Go `AggregateEquipment(weapon *WeaponProfile, armors []ArmorProfile) EquipBonus`）：六素質 flat 相加（含武器 int_bonus）；def flat 相加；hp_pct／mp_pct／atk_pct／matk_pct／crit_pct／crit_dmg_pct 相加；interval_pct 相加後 clamp ≥ −50；element_resist_pct 相加後 clamp ≤ 60；damage_taken_pct 相加後 clamp ≥ −60；mp_cost_reduce_pct clamp ≤ 50；regen 相加。
套用位置：
- Compute（Go）：素質 flat 在算衍生值前加入（**不受 stat cap 限制**，cap 只管配點）；`DEF = (statusDEF + def) × (1+def_pct/100)`；HP/MP 上限 ×(1+pct)；atk/matk pct；crit/crit_dmg；全部 floor。玩家頁與 bootstrap 共用同一份彙總；只有武器時結果與 P7 逐位元一致。
- 引擎（TS）：bootstrap 送 `equipmentEffects`（防具＋飾品彙總，不含武器）＝`{ intervalPct, mpCostReducePct, hpRegenPctPer5s, mpRegenPctPer5s, damageTakenPct, elementResistPct }`；引擎把 weapon profile 的 intervalPct／elementResistPct 與 equipmentEffects 相加後套用；mp cost 減免套在 dispatch 的技能 MP 檢查與扣除；regen 每 5000ms（戰鬥時鐘）對玩家回復 floor(hpMax×pct/100)、不超上限、死亡不回；damage_taken_pct 與 buff 相加後夾 −60；全部整數不變式。

## 3. 數值規則（設計代理依此程式化產生）
- 等級門檻同武器：tier→1/10/20/30/40/50/60/70/80/90。基準 `RefDef(L)`（refPlayerTable.json）：Lv1 0、10 10、20 18、30 26、40 34、50 43、60 50、70 57、80 64、90 72。
- **基礎防具總 DEF(t) = round(RefDef(L) × 0.5)**，分配 衣服 35%／褲裙 25%／頭盔 15%／手套 12.5%／鞋子 12.5%；再乘**職業 DEF 係數**：heavy_knight 1.3、light_knight 1.1、merchant 1.0、archer 0.9、cleric 0.85、mage 0.7；每件 `max(1, round(...))`。（線性減防之下防具 DEF 必須克制；模擬階段驗證怪物傷害是否歸零，必要時建議調 `battle_lvl_atk_ratio`。）
- 副屬性基礎（t1→t10 線性）：衣服 vit 1→8、hp_pct 2→12；褲裙 vit 1→6、hp_pct 1→8；頭盔 int 1→8；手套 str 1→8；鞋子 agi 1→5、dex 1→5。再乘**職業副屬性係數**（依部位）：
  - 衣服／褲裙（VIT、hp_pct）：hk 1.3、mc 1.1、lk 1.0、ar 0.9、cl 0.9、mg 0.8
  - 頭盔（INT）：mg 1.4、cl 1.3、lk 1.0、mc 0.8、ar 0.7、hk 0.6
  - 手套（STR）：hk 1.4、lk 1.2、mc 1.0、ar 0.8、cl 0.6、mg 0.6
  - 鞋子（AGI、DEX）：ar 1.4、lk 1.1、mc 0.9、hk 0.8、cl 0.8、mg 0.8
  四捨五入，素質 `max(1, …)`，hp_pct 四捨五入到整數。
- 飾品 18 種效果 × **5 級**＝90 件（通用）。level_req 依級：t1 1、t2 20、t3 40、t4 60、t5 80。每種效果 t1→t5 線性（四捨五入到整數，t3 大致等於原提案值）：HP% 4→12｜MP% 4→12｜STR/VIT/DEX/AGI/INT/LUK 各 2→10｜攻擊間隔 −3→−12｜爆擊率 2→8｜爆擊傷害 6→25｜MP 消耗 −5→−25｜每 5 秒回 HP 1→4%｜每 5 秒回 MP 1→4%｜ATK% 2→10｜MATK% 2→10｜受到傷害 −3→−12｜屬性傷害 2→10（t3＝5，對應原話「屬性傷害降 5%」）。飾品名稱＝效果物件名＋等級後綴（例：心率帶 I～V 或 入門/進階/專業/菁英/傳說）。
- 命名：每職業一組前綴詞（例：輕騎士「巡街」、弓箭手「獵風」、重騎士「重甲」、聖職者「聖護」、商人「行商」、魔法師「咒紋」，可自訂但六職業要明顯不同）＋部位系列 10 級遞進名（慢跑帽系、運動手套系、排汗衣→競賽背心系、跑褲系、跑鞋系；DOR 城市跑步味，不得用 RO 原名）；飾品＝心率帶、運動錶、水壺腰帶、號碼布、能量膠、壓縮袖套、反光手環、防磨膏、鹽錠…各對應效果要合理；description 一句話。
- 產出：`rpg_armor_items` 6 職業×5 部位×10 級＝300＋飾品 18×5＝90，共 **390 筆**；Excel `docs/dorpg/DORPG_防具表_v1.xlsx`（總表＋每職業一表＋飾品表（18 效果×5 級）＋職業係數表）與 `docs/dorpg/ARMOR_v1.md`；seed SQL 片段；驗證腳本（件數、唯一、level_req 單調、每職業每 tier DEF 總和＝基礎×職業係數±2、副屬性係數、飾品 18 效果×5 級數值單調、profile 詞彙）。

## 4. 介面（WIRE.md）
- `GET /rpg/equipment`：`equipped` 八格、`armor_items`＝**目前職業的 50 件防具＋90 件通用飾品**（未選職業→只有飾品）、`equip_bonus`；`PUT /rpg/equipment/{slot}`（helmet|gloves|armor|legs|boots|accessory1|accessory2）錯誤 not_found／wrong_slot／wrong_job／level_too_low／duplicate_accessory；武器端點不變；`PUT /rpg/job` 換職業自動卸下武器＋五部位防具（同一交易）。`/rpg/me` 帶 `equipment` 八格與 `equip_bonus`。後台 `/admin/rpg/armor-items` CRUD（可依職業／部位篩選）。

## 5. 畫面
- EquipmentScreen 頂部改成**裝備欄**（八格：武器、頭盔、手套、衣服、褲裙、鞋子、飾品 1、飾品 2；顯示名稱或「空」；點格子切換下方清單）。武器格沿用 P7 三分頁；防具格＝該部位本職業 10 級清單；飾品格＝依效果分組（18 組，每組 5 級可切換或列出），顯示效果文字與等級門檻，「裝到飾品 1／飾品 2」；等級不足灰化；卸下鈕；未選職業時防具格提示先選職業。角色頁裝備摘要八格（「・」分隔、空略過）＋加成摘要（DEF+n、VIT+n…）。手機優先、金底白字、不新增 fixed 覆蓋層。
- 戰鬥：定時回復沿用既有 regen 浮字；其餘無 UI。

## 6. 驗證
- Go：AggregateEquipment（相加與 clamp）、Compute×EquipBonus 各欄位、level_req／wrong_slot／wrong_job／duplicate_accessory、換職業卸下防具保留飾品、查無 item 視為未裝備、只有武器時與 P7 逐位元一致、seed profile 詞彙全過。
- 引擎：MP 減免、每 5 秒 HP/MP 回復（整數、不超上限）、damage_taken 與 buff 相加夾限、interval 與武器相加夾限、element_resist 相加夾限；既有斷言不變。
- 模擬：Lv27 輕騎士 本職業全套 t3 防具＋雙劍 t3＋兩飾品（受傷 −8%、回 HP）vs Lv20/30/40（50 種子）：勝率與受傷量；另跑重騎士全套 t3（DEF 最高）與魔法師全套 t3（最低）對照；若 Lv30 幾乎零傷害則回報建議的 `battle_lvl_atk_ratio`（不自行改）。
- Neon：184 套用與冪等、八格流程、五種錯誤碼、換職業卸下、bootstrap equipmentEffects。E2E：裝備欄八格截圖、裝防具後角色頁 DEF/HP 變化、戰鬥回復浮字。審查。
