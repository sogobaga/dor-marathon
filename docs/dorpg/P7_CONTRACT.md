# DORPG P7 契約：武器系統＋怪物體型／屬性實裝 — 2026-09-18

單一真相。P5／P6 契約仍有效（`docs/dorpg/P5_*.md`、`P6_*.md`），本檔只加不改。

## 0. 使用者需求（原話濃縮）
- 怪物要加上屬性；玩家可穿裝備。基本裝備＝武器與防具，**先做武器**。
- 每職業三種武器：輕騎士 單手劍/雙劍/細劍；弓箭手 長弓/短弓/弩；魔法師 杖/棍/書；聖職者 杖/書/鍊；商人 錘/槌/棒；重騎士 巨劍/槍/斧。
- 武器特性（原話）：單手劍 均衡、可有屬性、部分額外加魔攻｜雙劍 二刀流攻擊兩次、攻擊力 60%｜細劍 攻速上升（攻擊間隔縮短、稀有度越高縮越多）、對小型 +5%｜長弓 間隔拉長、攻擊力高、加暴擊傷害、對大型 +5%｜短弓 間隔短、攻擊力中、加迴避、可有屬性｜弩 間隔最短、攻擊力弱、加暴擊傷害｜杖 加 INT 與 MP｜棍 加魔攻｜書 魔法技能效果 +5%｜鍊 魔法抗性（屬性效果減免）｜錘 暴擊傷害%、對大型 +5%｜槌 物攻%、對大型 +5%｜棒 物防%、對大型 +5%｜巨劍 蓄氣時間變長但傷害大幅增加、對大型 +5%｜槍 普攻 2 連擊各 50%、加暴擊率與暴擊傷害、有機率 3 連擊、可有屬性｜斧 間隔變長、範圍傷害（左右兩側怪物受 40%）、對大型 +5%。
- 每種武器 10 個等級，各有玩家等級門檻；可有屬性的武器另設一組七屬性（金/木/水/火/土/光/闇）版本。
- 怪物實裝體型（大/中/小）與屬性（七種）的對應參數。

## 1. 怪物體型與屬性（本輪真正進入公式）
- 欄位沿用 `rpg_monsters.attribute`（metal|wood|water|fire|earth|light|dark|neutral）與 `size`（small|medium|large）；後台已可編輯。wire 已送 `attribute`/`size`（確認 fromApi 有映射）。
- **屬性相剋表（五行＋光暗）**：剋＝金→木、木→土、土→水、水→火、火→金；光↔闇互剋。攻擊屬性 A 對怪物屬性 M：A 剋 M → `1 + battle_element_advantage_pct/100`（預設 25）；M 剋 A（被剋）→ `1 + battle_element_disadvantage_pct/100`（預設 −25）；A＝M → `1 + battle_element_same_pct/100`（預設 −25）；任一方 neutral → 1。另：A ∈ 怪物 `weak_elements` → 至少 `1 + battle_weakness_bonus_pct/100`（取兩者較大）。管理者 `battle_element_chart` 覆寫仍最優先。純函式 `ElementMultiplier(cfg, attackElement, monsterAttr, weakElements)` Go／TS 鏡像＋雙邊測試。
- **怪物攻擊帶自己的屬性**：怪物普攻／技能視為攻擊屬性＝其 attribute（neutral 則無）；玩家沒有防禦屬性，但裝備（鍊）給 `elementResistPct` → 來自非 neutral 怪物的傷害 ×(1 − elementResistPct/100)。
- **體型**：本輪只作為武器 `sizeBonus` 的判定依據（對小型／大型 +5%）；不做 RO 體型懲罰表。

## 2. 武器資料模型
- `rpg_weapon_types`：`id TEXT PK`（lk_sword/lk_dual/lk_rapier/ar_longbow/ar_shortbow/ar_crossbow/mg_staff/mg_rod/mg_book/cl_staff/cl_book/cl_chain/mc_hammer/mc_mallet/mc_club/hk_greatsword/hk_spear/hk_axe）, `job_id REFERENCES rpg_jobs`, `name`（單手劍…）, `visual`（sword|bow|staff|greatsword，現有四套 FX）, `elemental_capable BOOL`（lk_sword/ar_shortbow/hk_spear＝true）, `description`, `traits JSONB`（型別層固定特性，供顯示與設計依據）, `sort_order`。
- `rpg_weapons`：`id TEXT PK`（`<type>_t<tier>` 或 `<type>_t<tier>_<element>`）, `type_id`, `tier INT 1..10`, `name`, `rarity`（common: t1–3｜rare: t4–6｜epic: t7–9｜legendary: t10）, `level_req INT`（tier→1/10/20/30/40/50/60/70/80/90）, `element TEXT`（neutral 或七種）, `profile JSONB`（本件最終數值，見 §3）, `description`, `is_active`, `sort_order`。
- `player_equipment`：`user_id REFERENCES users ON DELETE CASCADE, slot TEXT CHECK (slot IN ('weapon')), item_id TEXT REFERENCES rpg_weapons(id), updated_at, PRIMARY KEY(user_id, slot)`。（未來防具再加 slot。）
- 取得方式（測試階段）：職業對應的全部武器都可裝備，唯一條件＝有效等級 ≥ level_req；不做背包／掉落／商店（未來走 DP／掉落）。切換職業時若現有武器不屬新職業 → 自動卸下。
- 傭兵本輪不裝備（維持腳本＋固定視覺）；資料設計不阻擋未來在 preset 加 weapon_id。

## 3. WeaponProfile（引擎詞彙；每件武器的 profile JSON，缺省＝中性）
```
{ "atk": n, "matk": n, "int_bonus": n, "mp_pct": n, "atk_pct": n, "matk_pct": n, "def_pct": n,
  "hits": 1, "hit_mul": 1.0, "extra_hit_chance_pct": 0,
  "interval_pct": 0,            // 攻擊間隔變化％：負＝更快（細劍/短弓/弩），正＝更慢（長弓/斧）
  "charge_time_mul": 1.0, "charge_dmg_mul": 1.0,   // 巨劍
  "splash_pct": 0,              // 斧：同排左右相鄰怪物受到的％
  "size_bonus": {"small": 0, "medium": 0, "large": 0},
  "crit_pct": 0, "crit_dmg_pct": 0, "flee_bonus": 0,
  "element_resist_pct": 0, "magic_skill_pct": 0,
  "element": "neutral" }
```
套用位置：
- Compute（Go；玩家頁與 bootstrap 共用）：`INT += int_bonus` 於算衍生值前；`ATK = (statusATK + atk) × (1 + atk_pct/100)`；`MATK = (statusMATK + matk) × (1 + matk_pct/100)`；`DEF ×(1+def_pct/100)`；`MPMax ×(1+mp_pct/100)`；`CombatRating.critPct += crit_pct`、`flee += flee_bonus`、`critDmgPct += crit_dmg_pct`。全部 floor 成整數（HP/MP/ATK…）。
- 引擎（TS，只在戰鬥）：普攻 `hits` 段各 `×hit_mul`，每段獨立 miss/crit；`extra_hit_chance_pct` 由引擎 rng 決定加一段；`interval_pct` 乘在 attackCooldownFor 結果；蓄氣 `chargeFullMs × charge_time_mul`、蓄氣倍率超出 1 的部分 ×`charge_dmg_mul`（即 `1 + (chargeMul−1)×charge_dmg_mul`）；`splash_pct` 對同排相鄰（slot 順序 front: left/center/right，rear: left/right）存活怪物各造成主目標傷害的 %（獨立 floor，不觸發 miss/crit 重算）；`size_bonus[enemy.size]` 乘傷害；普攻與 **element=neutral 的物理技能** 帶武器 `element`（魔法技能維持自身屬性）；`magic_skill_pct` 乘在 dmg_type=magic 技能與 heal 的 coef 上；`element_resist_pct` 減免非 neutral 怪物造成的傷害。
- 武器視覺＝type.visual（覆蓋職業預設）。

## 4. 各武器類型的設計規則（設計代理依此產生 10 級數值；數值用 refPlayerTable.json 的同級參考玩家 ATK 當基準）
- 基準：`base_atk(tier) = round(RefAtk(level_req) × 0.8 + 6)`；魔法系（杖/棍/書/鍊）用 `base_matk(tier) = round(RefMatk(level_req) × 0.8 + 6)`，其 atk 給 `round(base_atk×0.4)`。
- 型別係數與特性（%為線性隨 tier 成長：t1 取下限、t10 取上限）：
  - 單手劍 atk×1.0；屬性版另 +matk（t1 4 → t10 40）。
  - 雙劍 hits 2、hit_mul 0.6、atk×1.0。
  - 細劍 atk×0.9、interval_pct 依 rarity：common −6、rare −10、epic −14、legendary −20；size_bonus.small 5。
  - 長弓 atk×1.25、interval_pct +20、crit_dmg_pct 5→15、size_bonus.large 5。
  - 短弓 atk×1.0、interval_pct −10、flee_bonus 3→12；屬性版。
  - 弩 atk×0.8、interval_pct −25、crit_dmg_pct 8→20。
  - 杖（魔法師／聖職者各自一系列）int_bonus 2→12、mp_pct 5→20。
  - 棍 matk_pct 5→20。
  - 書（魔法師／聖職者各自）magic_skill_pct 5（固定，原話）＋ matk 較低（×0.85）。
  - 鍊 element_resist_pct 5→25、mdef_pct 5→15（用 def_pct 同型欄位 `mdef_pct` 補進詞彙）。
  - 錘 crit_dmg_pct 5→20、size_bonus.large 5。
  - 槌 atk_pct 5→15、size_bonus.large 5。
  - 棒 def_pct 5→15、size_bonus.large 5。
  - 巨劍 atk×1.15、charge_time_mul 1.5、charge_dmg_mul 1.6→2.0、size_bonus.large 5。
  - 槍 hits 2、hit_mul 0.5、crit_pct 3→10、crit_dmg_pct 5→15、extra_hit_chance_pct 10→30；屬性版。
  - 斧 atk×1.1、interval_pct +25、splash_pct 40、size_bonus.large 5。
- 屬性版（三種類型 × 10 級 × 7 屬性＝210 件）：與同級無屬性版數值相同，只多 element（單手劍屬性版再加上述 matk）。名稱加屬性後綴（例：「‧焰」「‧霜」）。
- 名稱要有 DOR／城市／跑步味，不得用 RO 原名；每級名稱不同（10 級遞進感）；description 一句話。
- 產出：`rpg_weapon_types` 18 筆、`rpg_weapons` 180＋210＝390 筆；Excel `docs/dorpg/DORPG_武器表_v1.xlsx`（總表＋每職業一表＋類型特性總覽）與 `docs/dorpg/WEAPONS_v1.md`；seed SQL 片段供 migration 183 拼接；驗證腳本檢查數量、唯一、level_req 單調、數值範圍、profile 詞彙合法。

## 5. 介面（見 WIRE.md）
- `GET /rpg/equipment`（目前裝備＋本職業武器清單含可裝備旗標）、`PUT /rpg/equipment/weapon {item_id|null}`、`/rpg/me` 帶 `weapon`；bootstrap 玩家帶 `weapon` profile 與 visual；後台 `/admin/rpg` 武器類型／武器分頁（列表＋篩選＋編輯 profile JSON）；config 新增 `battle_element_advantage_pct 25、battle_element_disadvantage_pct -25、battle_element_same_pct -25`。

## 6. 畫面
- 新 screen「裝備」（角色頁入口，與酒館同層）：頂部目前武器卡（名稱、等級門檻、稀有度、屬性、關鍵效果文字、卸下）；下方三個類型分頁，每頁 10 級列表（名稱、Lv 門檻、ATK/MATK、效果摘要、屬性版的七顆屬性 chip 可切換），可裝備者「裝備」、等級不足灰化並標「需 Lv.N」。角色頁衍生值即時反映裝備。手機優先、金底白字、不新增 fixed 覆蓋層。
- 戰鬥：玩家武器視覺依 type.visual；斧濺射、槍連擊、雙劍二刀流的浮字各段獨立。

## 7. 驗證
- Go：ElementMultiplier 表（五行全 25 格＋光暗＋neutral＋weak_elements＋override）、Compute 套 WeaponProfile（每個欄位一個案例、整數）、level_req 門檻、換職業自動卸下、seed 全部 profile 詞彙合法（跟 validator 同規則）。
- 引擎：雙劍 2 段各 60%、槍 2 段 50% 且固定種子下第三段機率符合、斧濺射同排相鄰各 40% 且不濺後排、細劍冷卻縮短、長弓冷卻加長、巨劍蓄氣時間 ×1.5 與傷害倍率、體型 +5%、普攻帶武器屬性且五行相剋倍率正確、鍊減免、書 magic_skill_pct；全部整數不變式。
- Go↔TS：ElementMultiplier 鏡像測試同輸入同輸出（表格列進兩邊測試）。
- 模擬：Lv27 輕騎士分別持 單手劍/雙劍/細劍 t3 打 Lv20/Lv30/Lv40，各 50 種子，看 DPS 差異在合理範圍（±30% 內）並回報；不強求調整。
- Neon：183、裝備流程、換職業卸下、bootstrap weapon。E2E：裝備頁截圖、換裝後角色頁數值變化、戰鬥中雙劍兩段浮字／斧濺射。審查。
