# DORPG P12 契約：怪物前排／後排 × 武器排位加成（弓＋10% 後排、鈍器＋10% 前排、槍貫穿 50%）— 2026-09-20

單一真相。P5–P10 契約仍有效，本檔只加不改。

## 0. 使用者需求（原話）
「怪物的位置中，細分為前排和後排。武器可以有對應的加成：弓類，對於後排的怪物有 10% 的加成；鈍器類的武器，對於前排的怪物有 10% 的加成；槍，攻擊前排的怪物，有機會造成"貫穿"的傷害，讓對應位置的後排怪物也受到原傷害 50% 的波及傷害。」

## 1. 決策（編排者定案）
- **排位＝既有槽位**：`rpg_encounter_monsters.slot` front_left／front_center／front_right → `row='front'`；rear_left／rear_right → `row='rear'`。引擎 enemy 帶 `slot` 與 `row`（bootstrap 已有 slot 就直接推導，沒有就補送）。
- **加成掛在武器類型**（`rpg_weapon_types.traits` JSONB，P7 已有此欄），不動 390 件武器：
  - 弓類（archer：ar_longbow／ar_shortbow／ar_crossbow）：`{"row_bonus_rear_pct": 10}`
  - 鈍器類（merchant：mc_hammer／mc_mallet／mc_club）：`{"row_bonus_front_pct": 10}`
  - 槍（heavy_knight：hk_spear）：`{"pierce_chance_pct": 30, "pierce_dmg_pct": 50}`（機率預設 30%，後台可調）
  其他類型 traits 不變。注意：traits 目前已存放 P7 產生器用的描述鍵（style／special／positioning／size_bonus／interval_pct／…_range），本輪只**新增**上述鍵、不動既有鍵；引擎只讀這四個新鍵。後台武器類型頁可編輯 traits。
- 引擎 enemy 已有 `slot: EnemySlotId`（engine/types.ts:137），`row` 直接由 slot 推導，bootstrap 不需加欄位。
- **套用範圍**：排位加成套用於「以武器造成的物理傷害」＝普攻＋物理技能（P7 已定義物理技能帶武器屬性者），乘在最終傷害上（在暴擊、屬性、體型之後、floor 之前）；貫穿只在**普攻**命中前排目標時判定（含多段：每段各自判定一次機率、各自波及），波及傷害＝`floor(該段對前排目標實際造成的傷害 × pierce_dmg_pct/100)`，不再扣後排怪的 DEF、不再判暴擊；對應位置：front_left→rear_left、front_right→rear_right、front_center→rear_left（若已死或不存在則 rear_right；都沒有則不貫穿）；波及傷害事件沿用既有攻擊事件加 `pierce: true`（比照 P7 濺射 `splash: true`），浮字「貫穿」。
- 貫穿與斧的濺射（同排相鄰）互不影響；貫穿不觸發二次貫穿。P8 飾品／裝備效果不變。

## 2. 資料模型（migration 188，可重複執行）
- `UPDATE rpg_weapon_types SET traits = traits || '{...}'::jsonb` 七筆（見 §1）；`INSERT schema_migrations 188`。
- docs：`docs/dorpg/WEAPONS_v1.md`（或既有武器文件）補「排位加成」一節；武器表 xlsx 若有類型表則加 traits 欄（同步 Downloads）。

## 3. 介面（WIRE）
- WeaponTypeDTO.traits 已存在；WeaponDTO 不變。裝備頁武器類型說明加一行加成文字（弓：對後排 +10%；鈍器：對前排 +10%；槍：30% 貫穿、後排 50%）。
- bootstrap：`weapon.profile`（WeaponProfileWire）新增 `rowBonusFrontPct`、`rowBonusRearPct`、`pierceChancePct`、`pierceDmgPct`（由 type.traits 合併，缺省 0）；enemy 新增 `slot`、`row`（'front'|'rear'）。
- 引擎：`rowBonusMultiplier(profile, enemy.row)`；`resolveWeaponAttack` 在前排目標命中後依 `pierceChancePct`（rng）對映後排目標加波及；事件 `attack` 加 `pierce?: true`。

## 4. 畫面
- 戰鬥：貫穿波及浮字「貫穿」（與濺射同樣式、不同字）；敵人前後排既有排版不變。
- 裝備頁：武器類型卡片顯示加成文字；後台武器類型 traits JSON 可編輯。

## 5. 驗證
- Go：traits 合併進 WeaponProfileWire（缺省 0、非法值忽略）、enemy row 推導、188 seed 七筆 id 存在。
- 引擎：弓對後排 ×1.10、對前排 ×1.00；鈍器反之；技能（物理）也吃；魔法技能不吃；槍 rng<30% 貫穿：front_left→rear_left、center fallback、無後排不貫穿、多段各自判定、波及＝floor(實傷×50%)、事件 pierce 旗標、整數不變式；既有 375＋59 條斷言不變。
- 模擬（快速）：重騎士槍 vs 巨劍在五隻編組的總輸出；弓箭手打後排 vs 前排。Neon：188 冪等、traits 內容；E2E：裝備頁加成文字、戰鬥貫穿浮字（用槍打前排直到出現）；審查。
