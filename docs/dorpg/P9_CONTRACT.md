# DORPG P9 契約：傭兵裝備與技能比照玩家＋AI 戰鬥策略（可擴充）＋玩家自動戰鬥 — 2026-09-19

單一真相。P5–P8 契約仍有效（`docs/dorpg/P5_*.md`…`P8_*.md`），本檔只加不改。

## 0. 使用者需求（原話）
- 「傭兵也要比照一般玩家，有配置裝備和技能」
- 「另外有 AI 戰鬥策略：1. MP 保守使用 2. 技能積極使用 3. 保護隊友優先 4. 集中火力 5. 屬性相剋優先；未來可以持續擴充」
- 「然後多一個自動戰鬥，透過 AI 戰鬥策略來搭配進行」

## 1. 決策（編排者定案）
- **裝備掛在腳本（preset）上**：一份腳本＝一套完整配置（等級、配點、技能等級、八格裝備、AI 策略）。同一傭兵可有多份腳本切換；系統預設腳本也有預設裝備與策略。傭兵技能已在 P6 腳本裡（skill_levels），本輪不改技能規則。
- **傭兵裝備規則與玩家完全相同**：武器 type.job_id＝傭兵職業；五部位防具 job_id＝傭兵職業；飾品通用；`level_req ≤ 腳本等級`；飾品兩格不可同件。測試階段所有物品可自由選（與玩家一致，取得途徑另輪）。
- **AI 策略＝決策函式的參數化變體**：行為寫在引擎 `engine/strategies.ts` 的 registry（id → 定義＋預設參數），資料庫 `rpg_ai_strategies` 只管顯示名稱、說明、參數覆寫、啟用與排序（後台可調門檻與文案，不能新增引擎沒有的行為；新增行為＝在 registry 加一項＋seed 一列）。未知 id 一律退回 `balanced`。
- **每個腳本選一種策略**（`strategy_id`，預設 `balanced`＝現行五段優先序）；**玩家自動戰鬥也選一種策略**，開關與策略持久化在玩家角色列，戰鬥 HUD 可隨時切換。
- 策略只影響「用不用技能／用哪個技能／打誰／何時治療或防禦」，**不改**冷卻、詠唱、MP 成本、傷害公式；所有策略共用同一決策入口 `decideAction(actor, state, rng, strategyId)`，隊友與自動戰鬥的玩家都走這裡。
- 自動戰鬥開啟時，玩家手動操作仍然有效（插隊執行一次），之後 AI 接續；不會因為手動操作而關閉自動。
- 傭兵裝備會提高隊伍戰力；模擬階段回報對 X3 難度的影響，但**本輪不改** battle_lvl 四個比例（後台可調）。

## 2. 資料模型（migration 186，使用者手動套用；可重複執行）
- `rpg_ai_strategies`：`id TEXT PK`（balanced｜mp_conserve｜skill_aggressive｜protect_allies｜focus_fire｜element_advantage）、`name TEXT`、`description TEXT`、`params JSONB NOT NULL DEFAULT '{}'`（覆寫引擎預設門檻）、`is_active BOOLEAN`、`sort_order INT`、timestamps。seed 六列（名稱：均衡／MP 保守使用／技能積極使用／保護隊友優先／集中火力／屬性相剋優先）。
- `rpg_companion_presets` 新增 `equipment JSONB NOT NULL DEFAULT '{}'`（`{ weapon, helmet, gloves, armor, legs, boots, accessory1, accessory2 }` → item_id，缺鍵＝空；應用層驗證，無 FK）、`strategy_id TEXT NOT NULL DEFAULT 'balanced' REFERENCES rpg_ai_strategies(id)`。
- `player_characters`（比照 migration 180 的 ALTER … ADD COLUMN IF NOT EXISTS）新增 `auto_battle BOOLEAN NOT NULL DEFAULT FALSE`、`auto_strategy_id TEXT NOT NULL DEFAULT 'balanced' REFERENCES rpg_ai_strategies(id)`。
- 系統預設腳本（`user_id IS NULL` 四筆，Lv25）seed 更新：裝備＝該傭兵職業在 Lv25 可裝的最高級（武器 t3、五部位防具 t3、飾品 t2），飾品與策略依角色定位：阿光（輕騎士）acc_dmgtaken_t2＋acc_hp_t2、`balanced`；阿深（重騎士）acc_hp_t2＋acc_vit_t2、`protect_allies`；小咪（聖職者）acc_mpregen_t2＋acc_mp_t2、`mp_conserve`；小優（弓箭手）acc_crit_t2＋acc_agi_t2、`focus_fire`。武器＝各職業 rpg_weapon_types sort_order 第一種的 t3。

## 3. 數值與規則
- 傭兵衍生值：`Compute(ComputeInput{BaseLevel: preset.level, Stats, WeaponType, Passives, Equip: AggregateEquipment(weapon, armors)})` → 再乘既有 companion 倍率（hp/mp/atk/matk/def/mdef）→ floor（順序與 P6 相同，只是 Compute 多吃 Equip；equipment 空時與 P6 逐位元一致）。酒館、bootstrap 共用。
- 腳本驗證（POST/PUT /rpg/presets、/validate）新增錯誤：`equipment.<slot>`：`not_found`／`wrong_slot`／`wrong_job`／`level_too_low`／`duplicate_accessory`（與 P7/P8 同碼）；`strategy_id`：`unknown_strategy`（不存在或 is_active=false）。腳本等級下調導致 `level_too_low` 時由前端自動清空該格再送出（後端仍回錯誤，不自動卸）。
- bootstrap：傭兵 party member 新增 `weapon`（WeaponProfileWire，同玩家）與真實 `equipmentEffects`；每位成員新增 `strategyId`；玩家成員的 `strategyId`＝auto_strategy_id；頂層 `autoBattle`。config 新增 `aiStrategies: { [id]: { params } }`（引擎預設 ⊕ DB 覆寫，只含 is_active）。
- 傭兵在戰鬥中的 weapon 效果（多段、屬性、體型、濺射、間隔）與 equipmentEffects（回復、減傷、MP 減免）全部與玩家相同路徑生效。

## 4. AI 策略行為定義（引擎；每條可寫成決定性斷言）
共用：候選技能＝已展開、implemented、冷卻好（隊友用 aiSkillReadyAt、玩家用 skillReadyAt）、MP ≥ effectiveMpCost。**決策與執行分離**：`decideAction(actor, state, rng, strategyId) → Decision { kind: 'heal'|'buff'|'damage'|'debuff'|'attack'|'guard'|'item'|'wait', skillId?, targetId? }`；隊友的執行沿用現行 castNow／resolveWeaponAttack／finishAllyAction；玩家自動戰鬥的執行＝把 Decision 轉成既有指令（ATTACK_BEGIN→ATTACK_RELEASE（有蓄氣武器則等 chargeTime 才放）、USE_SKILL、GUARD_BEGIN/END、USE_ITEM、SELECT_TARGET）走 applyCommand，不繞過冷卻／詠唱／MP 檢查。現況：隊友普攻與技能都跟隨玩家鎖定的 `ctx.targetId`、治療門檻 50% 寫死（ai.ts）；本輪把門檻改讀策略 params（balanced 預設值＝現行常數，行為不變）。
- `balanced`（params: `heal_pct` 50、`self_heal_floor_pct` 15）：現行五段優先序，目標＝玩家鎖定的 ctx.targetId，零改動（既有斷言不變）。
- `mp_conserve`（params: `mp_reserve_pct` 50、`emergency_heal_pct` 30）：MP 比例 < mp_reserve_pct 時不放任何技能改普攻；例外：有治療技能且任一隊友 HP% < emergency_heal_pct 仍治療。
- `skill_aggressive`（params: `min_mp_reserve_pct` 0）：開場（首次可行動）先放可用 buff；之後只要有候選傷害技能就施放，選預期傷害（coef）最高者；無候選才普攻；治療沿用 balanced 門檻。
- `protect_allies`（params: `heal_pct` 60、`shield_pct` 75）：任一隊友 HP% < heal_pct 就治療（最低者）；有護盾/減傷/嘲諷類技能時對最低 HP% 隊友（或自己）施放（HP% < shield_pct）；目標＝正在鎖定最低 HP% 隊友的敵人（讀敵人 target），否則 balanced。
- `focus_fire`：目標＝場上存活敵人中 HP 絕對值最低者（同分取索引小者），寫入 `state.focusTargetId`；採此策略的所有角色同一目標（不跟隨玩家 ctx.targetId）；目標死亡才換；技能選擇沿用 balanced；玩家自動戰鬥採此策略時會發 SELECT_TARGET 切到該目標。
- `element_advantage`：對每個存活敵人算 elementMultiplier（自身普攻屬性＝武器屬性；技能屬性＝技能 element）；目標＝倍率最大且 > 1 者；技能優先選對該目標倍率 > 1 的傷害技能；全無相剋 → balanced。
- 玩家自動戰鬥：玩家可行動（普攻冷卻好／蓄氣完成、非詠唱中）時用同一 decideAction；防禦：敵人 windup 鎖定自己時 GUARD_BEGIN（params `auto_guard`，balanced／protect_allies 預設 true，其餘 false）；藥水：HP% < 30 且有藥水則用（沿用既有 ITEM 動作）。

## 5. 畫面
- 酒館腳本編輯器：新增「裝備」區（重用裝備頁八格面板抽成共用元件 `EquipmentPanel`；清單依傭兵職業＋腳本等級灰化）與「AI 策略」下拉（名稱＋一句說明）；儲存走既有 POST/PUT。系統預設腳本唯讀但可「複製為我的腳本」（既有行為）。
- 戰鬥 HUD：「自動」切換鈕（金底白字；開啟時顯示「AI・策略名」）與策略選單（同六種）；開啟時玩家操作區仍可點；AI 施放技能沿用既有事件與浮字，不新增事件型別。
- 角色頁：顯示「自動戰鬥：開／關・策略」一行（唯讀）。
- 後台：`/admin/rpg` 新增「AI 策略」分頁（id 唯讀、名稱／說明／params JSON／啟用／排序）。
- 手機優先、金底白字、不新增 fixed 覆蓋層。

## 6. 驗證
- Go：傭兵裝備五種錯誤碼＋unknown_strategy；Compute×倍率順序（有裝備）與 P6 無裝備逐位元一致（equipment 空時）；系統預設 seed 的裝備全部通過 canEquip；auto-battle 持久化。
- 引擎：每種策略 ≥ 3 條決定性斷言（給定狀態 → 預期 action／目標／技能）；balanced 零改動（既有 375 條不變）；玩家自動戰鬥 tick 會產生行動；手動插隊後 AI 接續；未知 strategyId 退回 balanced。
- 模擬（真引擎、滿隊 Lv27 三職業 vs Lv20/30/40、50 種子）：(a) 傭兵有預設裝備 vs 無裝備的勝率／時長；(b) 六種策略各自套在全隊 vs balanced 的勝率／時長／玩家被打／技能使用次數／MP 剩餘——證明每種策略行為可量測且方向正確；(c) 玩家自動戰鬥（各策略）vs 模擬既有手動 policy；回報對 X3 難度的影響（不改比例）。
- Neon 臨時分支：186 套用與冪等、預設腳本裝備正確、PUT preset 裝備錯誤碼、bootstrap 傭兵 weapon/equipmentEffects/strategyId、PUT /rpg/auto-battle 持久化與 /rpg/me、後台策略 CRUD。E2E：酒館裝備區與策略選單截圖、戰鬥自動開關（開啟後玩家自動出手、切策略生效）、手機視窗無橫向溢出、0 console error。審查。
