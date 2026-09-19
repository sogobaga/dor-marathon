# P9 介面契約（後端 ↔ 前端 ↔ 引擎）— 欄位名不得自改；P5–P8 WIRE 仍有效

## REST（requireEntry＋既有 RateLimit）
- `GET /rpg/tavern` 回應新增 `strategies: StrategyDTO[]`（只含 is_active，依 sort_order）；`PresetDTO` 新增 `equipment: EquippedGearDTO`（八格，各格 WeaponDTO|ArmorDTO|null，同 P8 `/rpg/equipment` 的 `equipped` 形狀）、`equip_bonus: EquipBonusDTO`、`strategy_id: string`；`derived` 已含裝備。
  - StrategyDTO = `{ id, name, description, params: object, sort_order }`
- `GET /rpg/tavern/gear?job_id=<job>&level=<n>` → `{ weapon_types, weapons: WeaponDTO[], armor_items: ArmorDTO[] }`（該職業全部武器＋該職業 50 件防具＋90 件飾品；`can_equip` 依 level 計算；`equipped`／`equipped_in` 恆 false／null——腳本編輯器自己比對）。
- `POST /rpg/presets`、`PUT /rpg/presets/{id}` body 新增 `equipment: { weapon?, helmet?, gloves?, armor?, legs?, boots?, accessory1?, accessory2?: string|null }`、`strategy_id: string`；回 PresetDTO。
- `POST /rpg/presets/validate` body 同上新增；回應 `errors[]` 新增 `field: "equipment.<slot>"`（code `not_found`｜`wrong_slot`｜`wrong_job`｜`level_too_low`｜`duplicate_accessory`）與 `field: "strategy_id"`（code `unknown_strategy`）；回應新增 `equip_bonus: EquipBonusDTO`；`derived` 含裝備。
- `PUT /rpg/auto-battle` body `{ enabled: bool, strategy_id: string }` → `{ enabled, strategy_id }`；錯誤 `unknown_strategy`。`/rpg/me` 新增 `auto_battle: { enabled, strategy_id }`。
- 後台：`GET/PUT/DELETE /admin/rpg/ai-strategies`（PUT body `{ id, name, description, params, is_active, sort_order }`；id 必須在引擎 registry 白名單（後端持有同一份 id 清單常數）否則 400 `unknown_strategy`；DELETE 只允許非 balanced 且無腳本／玩家引用，否則 409 `in_use`）。

## 戰鬥 bootstrap（既有 wire 加欄位）
- 頂層新增 `autoBattle: boolean`。
- 每位 party member 新增 `strategyId: string`（玩家＝auto_strategy_id；傭兵＝preset.strategy_id）。
- 傭兵 party member 新增 `weapon: WeaponWire|null`（與玩家同形：`{ id, name, typeId, visual, profile: WeaponProfileWire }`）與真實 `equipmentEffects`（P8 形狀）；傭兵 `weapon` 視覺字串改由 type.visual（同 P7 玩家規則），未裝備時沿用 companion 表的 weapon 欄位。
- config 新增 `aiStrategies: { [id: string]: { params: object } }`（引擎預設 ⊕ DB 覆寫；只含 is_active 的 id）。

## 引擎（TS）
- `engine/strategies.ts`：`STRATEGY_IDS` 常數陣列、`StrategyDef { id, defaultParams }`、`resolveStrategy(id, cfg) → { id, params }`（未知→balanced）。
- `engine/ai.ts`：`decideAction(ctx, actor, strategy) → Decision`（隊友與自動戰鬥玩家共用；純函式、不改狀態），`advanceAllyAI` 改為「decide → 既有執行路徑」；各策略行為依 CONTRACT §4。
- `engine/autopilot.ts`：`advanceAutoBattle(ctx)`：玩家可行動且 `ctx.autoBattle` 時 decide → 轉成既有 Command 經 applyCommand 執行（普攻用 ATTACK_BEGIN/ATTACK_RELEASE，蓄氣武器等 chargeTime；技能 USE_SKILL；敵人 windup 鎖定玩家且 params.auto_guard 時 GUARD_BEGIN，windup 結束 GUARD_END；HP%<30 有藥水 USE_ITEM；focus_fire/element_advantage 先 SELECT_TARGET）；由 tick 在隊友 AI 之前呼叫。
- `player_characters` 新增欄位 `auto_battle`、`auto_strategy_id`（後端）。
- `PartyActor` 新增 `strategyId: string`；`BattleState` 新增 `autoBattle: boolean`、`focusTargetId: string|null`（集中火力共用目標）。
- dispatch 新增 `SET_AUTO_BATTLE { enabled, strategyId }`（本地即時生效；前端另呼叫 `PUT /rpg/auto-battle` 持久化）；tick 在玩家可行動且 `autoBattle` 時呼叫 decideAction 並走既有 dispatch 路徑（不繞過冷卻／詠唱／MP 檢查）。
- fixture.ts：離線示範隊伍給傭兵預設裝備與策略、玩家 autoBattle=false。
