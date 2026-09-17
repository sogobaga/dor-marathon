# DORPG P5 契約：職業／配點／技能（測試階段）— 2026-09-17 使用者拍板版

本檔＝本輪實作的單一真相。任何與 `docs/dorpg/RO_IMPLEMENTATION_PLAN.md` 衝突之處，以本檔為準（該計畫是拍板前的建議稿）。

## 0. 使用者拍板摘要（原話濃縮）
- 怪物數值**不隨玩家戰力縮放**，改絕對值（→ 第二輪做，本輪不動 scaling）。未來：地圖移動隨機遇敵、共同挑戰 BOSS、可單人可組隊；怪物特徵要能引發攻略討論。
- 升級主要來源＝**跑步任務／活動**；打怪與純跑步只給零星 EXP（政策，本輪不發戰鬥 EXP）。
- 等級對防禦的影響：Lv≤50 每級 +0.5、Lv≥51 每級 +0.35；其餘成長靠裝備與技能。
- 要 RO 的爽感：暴擊＋高速連擊；也要技巧型（攻／防／反擊時機）。
- 初期**不做轉職、不做職業等級（Job Lv）**。成長與變化性來自角色本身。**不照抄 RO**，職業用 DOR 正名（§1）。
- 決策：①怪物絕對值（第二輪）②硬防維持線性減算 ③MATK 維持線性 ④暴擊浮動倍率 1.75–2.25 且仍扣防 ⑤技能帶屬性、怪物有相剋屬性、命中相剋 +25% 傷害 ⑥體型只展示（怪物有設定，武器系統再用）⑦不做 HP/SP 職業係數 ⑧六職業各 10 技能（兩條路線各 5）共 60，職業不共用，先文字呈現，交付 Excel ⑨STR＋INT 整十階梯都做 ⑩戰報防弊先用節流＋合理性檢查。

## 1. 六職業（DOR 正名，id 固定）
| id | 名稱 | 定位 | 路線 A | 路線 B | 重點配點 | 武器視覺（現有四種） | 物攻分支 |
|---|---|---|---|---|---|---|---|
| light_knight | 輕騎士 | 平衡型 | 劍技騎士（重視劍技的近戰） | 魔法騎士（善用魔法） | STR+AGI+LUK vs INT | sword | melee |
| archer | 弓箭手 | 敏捷型 | 狙擊型（攻擊速度） | 刺客型（閃避） | AGI+LUK vs DEX | bow | ranged |
| heavy_knight | 重騎士 | 力量型 | 狂戰士（攻擊力） | 重裝騎士（防禦與反擊） | STR+LUK vs STR+VIT | greatsword | melee |
| cleric | 聖職者 | 恢復型 | 恢復免疫型 | 輔助強化型 | INT+DEX（依技能樹） | staff | melee |
| merchant | 商人 | 發展型 | 賺錢型（賺錢效益） | 幸運型（掉落品質） | VIT+LUK（依技能樹） | sword | melee |
| mage | 魔法師 | 暴力型 | 單體魔法型（單一最強傷害） | 範圍魔法型（地圖兵器） | INT+DEX（依技能樹） | staff | melee |

- 職業只影響：武器視覺、物攻分支（archer=ranged，其餘 melee）、技能集合。**不影響 HP/MP 係數**（拍板⑦）。
- 測試階段可**隨時切換**職業（不鎖定）；切換點：戰鬥列表（EncounterPicker）最上方＋角色頁。
- 資料：`rpg_jobs` 表（id, name, tagline, description, path_a_id, path_a_name, path_a_desc, path_b_id, path_b_name, path_b_desc, weapon, atk_branch, recommended_stats, sort_order）；`player_characters.job_id TEXT NULL REFERENCES rpg_jobs(id)`。未選職業＝沿用現行行為（全域 DefaultWeaponType、無職業技能）。

## 2. 測試等級（test_level）
- `player_characters.test_level INT NULL`（1–99）。有值時，所有用到 BaseLevel 的地方（Compute、配點總數、技能點總數、配點上限、戰鬥 bootstrap）一律用它，**完全不讀真實等級**；NULL 時用真實等級（users.exp 換算）。
- 角色頁提供輸入框＋「使用真實等級」清除鈕，明顯標示「測試用」。端點：`PUT /rpg/test-level {level|null}`。

## 3. 配點規則（RO 給點規則，改為可調）
- 總配點數 `TotalStatPoints(L) = stat_points_initial + Σ_{k=2..L} ( floor((k−1)/stat_points_step_levels) + stat_points_per_level_base )`；預設 48／5／3（＝RO pre-re：Lv1 48、Lv27 186、Lv99 1273）。
- 可配點數＝Total(L) − Σ_six spentBetween(InitialStat, stat)（**改為推導值**；`free_points` 欄位不再是真相，讀取時忽略、寫入時同步維護以免舊碼壞掉，或直接改成不寫）。
- 加點成本維持 `floor((n−1)/10)+2`。
- **上限**：每項素質 ≤ min(MaxStat, L)（L＝有效等級）。Lv1 時不能加點，這是刻意的。
- 端點：`POST /rpg/allocate {stat, points}` 維持；新增 `{stat, mode:"max"}`＝伺服器端反覆 +1 直到「點數不夠」或「達上限」；新增 `POST /rpg/stats/reset`＝六圍全回 InitialStat、寫 6 筆 player_stat_log（cost 負值）。
- UI：**移除 +5**；保留 +1；新增 **Max**；新增 **還原預設**（需二次確認）。顯示「可配點數 X／總點數 Y（Lv L）」與每項上限。

## 4. 技能點與技能等級
- 總技能點 `TotalSkillPoints(L) = skill_points_initial + max(0, L−1) × skill_points_per_level`；預設 0／1（Lv27 → 26 點）。
- 每個技能 `max_level`（5 或 10，由設計決定）；升 1 級花 1 技能點。
- 前置：同一路線內鏈狀，技能 n 需要技能 n−1 達 `prereq_level`（設計決定，預設 3）。
- `player_skill_levels(user_id, skill_id, level, PRIMARY KEY(user_id, skill_id))`；技能點花費只計算**目前職業**的技能（切職業時其他職業的分配保留、不計入）。
- 端點：`GET /rpg/skills`（目前職業 10 技能＋等級＋可升條件）、`POST /rpg/skills/allocate {skill_id, delta:+1|-1|"max"}`、`POST /rpg/skills/reset`。
- 角色頁新增「技能」區塊：兩條路線各 5 個，文字呈現（名稱／等級／效果文字／MP／冷卻／前置），＋／−／Max／全部重置。

## 5. 技能效果詞彙（engine 本輪要支援的集合；設計只能用這些）
每個技能一個 `effect JSONB`，`kind` ∈：
- `damage`：`{coef_base, coef_per_level, hits, element, target: "enemy"|"allEnemies", flat_base, flat_per_level}`（物理或魔法由 `dmg_type: "physical"|"magic"` 決定：physical 用 ATK 扣 DEF，magic 用 MATK 扣 MDEF）
- `heal`：`{coef_base, coef_per_level (乘 MATK), flat_base, flat_per_level, target: "self"|"ally"|"allAllies"}`
- `shield`：`{flat_base, flat_per_level, duration_ms}`
- `buff`：`{stat, value_base, value_per_level, duration_ms, target: "self"|"ally"|"allAllies"}`；stat ∈ atk_pct, matk_pct, def_pct, mdef_pct, aspd, crit_pct, flee, hit, hp_regen_pct, damage_taken_pct
- `debuff`：`{stat, value_base, value_per_level, duration_ms, target: "enemy"|"allEnemies"}`；stat ∈ atk_pct, def_pct, mdef_pct, aspd, flee, hit
- `passive`：`{stat, value_base, value_per_level}`；stat ∈ atk_pct, matk_pct, def_pct, mdef_pct, aspd, crit_pct, crit_dmg_pct, flee, hit, hp_max_pct, mp_max_pct, perfect_dodge（進 Compute／戰鬥初始值，角色頁看得到）
- `special`：`{text}`＝本輪引擎**未實裝**（賺錢效益、掉落品質、免疫異常、反擊、詠唱中斷保護…），UI 顯示文字並標「尚未實裝」，可配點但戰鬥中不可用。
共同欄位：`mp_cost_base, mp_cost_per_level, cooldown_ms, cast_ms, element`（damage 才有意義）、`dmg_type`。
設計要求：每職業 10 個＝路線 A 5 個＋路線 B 5 個；各職業不得共用同名或同效果技能；每條路線至少 3 個是本輪引擎可實裝的 kind（damage/heal/shield/buff/debuff/passive），`special` 每條路線最多 2 個；主動技能的數字要落在現有平衡量級（現有 5 個技能：coefficient 1.0–2.0、mp 8–30、cooldown 3–8 秒、cast 300–1200ms；治療 flat 以 battle_reference_hp=300 為基準）。

## 6. 戰鬥公式變更（Go Compute／scaling ＋ TS fixture／combat 鏡像）
- 暴擊倍率：每次暴擊在 `[crit_mult_min, crit_mult_max]` 均勻抽（預設 1.75／2.25），**仍扣防**。取代固定 critMultiplier 2.0。
- 相剋：`rpg_monsters.weak_elements TEXT[]`（DOR 屬性桶）；技能 element ∈ 怪物 weak_elements → 傷害 ×(1 + battle_weakness_bonus_pct/100)，預設 25。既有 `battle_element_chart` 保留為**管理者覆寫**（有對應 key 才用，可做 0 倍免疫），**預設表清空**。現有 5 隻怪依其 attribute 給一組合理 weak_elements（設計決定）。
- STR 整十階梯：`atk += floor(STR/10)² × str_tier_coef`（預設 1）；INT：`matk += floor(INT/10)² × int_tier_coef`（預設 1）。全職業統一（弓箭手也是 STR，已知簡化）。
- 等級→防禦：`lvDef(L) = L ≤ lv_def_breakpoint ? L × lv_def_per_low : breakpoint × lv_def_per_low + (L − breakpoint) × lv_def_per_high`，取 floor；預設 50／0.5／0.35（取代現行 floor(L/2)）。
- 技能等級進戰鬥：bootstrap 帶入目前職業已配點技能（level ≥ 1）的即時數值（coef/flat/mp/buff 值依 level 展開後送前端，前端不再自己算等級）。技能欄：最多 10 格（兩排各 5），依路線／順序排列。
- 未變：硬防線性減算、MATK 線性、HP/MP 公式、ASPD、命中/迴避、怪物戰力縮放（第二輪改絕對值）。

## 7. 資料與流程規則
- Migration **180**（一份）：rpg_jobs＋seed 6 列；player_characters 加 job_id/test_level；rpg_skills 加 job_id/path/tier/max_level/effect/prereq_skill_id/prereq_level/mp_cost_per_level/display_text 等欄；seed 60 技能；player_skill_levels 表；rpg_monsters 加 weak_elements 並更新既有 5 隻。**先套 DB 再推程式**：程式讀新欄位，push 前必須等使用者確認套用（commit 可先做）。
- 既有 5 個技能列保留（job_id NULL），供隊友／舊資料引用；玩家技能欄只吃職業技能。
- rpg_config 新欄位全部進 DefaultConfig＋Validate＋後台 /admin/rpg 設定頁。
- Excel：`docs/dorpg/DORPG_職業技能表_v1.xlsx`（同時複製到 `C:/Users/paris/Downloads/`），欄位＝職業／路線／順序／技能 id／名稱／類型／效果文字／等級上限／各等級數值（Lv1、Lv5、Lv10 三欄）／MP／冷卻／詠唱／屬性／前置／本輪是否實裝；另附 `docs/dorpg/SKILLS_v1.md` 同內容。
- 驗證：Go 測試（配點公式對 RO statpoint.yml 逐級比對 1–99；技能點；上限；max；reset）、verify-dorpg-engine.mjs 新增斷言（暴擊浮動範圍、相剋 +25%、新 kind）、tsc、next build、Neon 分支整合（套 180、切職業、設測試等級、配點到上限、Max、還原、技能配點與前置、bootstrap 技能欄）、CDP E2E 截圖（角色頁三顆按鈕、戰鬥列表職業切換、技能欄文字）。
