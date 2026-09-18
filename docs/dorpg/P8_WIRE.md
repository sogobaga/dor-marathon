# P8 介面契約（後端 ↔ 前端 ↔ 引擎）— 欄位名不得自改；P5–P7 WIRE 仍有效

## REST（requireEntry＋既有 RateLimit）
- `GET /rpg/equipment` → `{ job, effective_level, equipped: { weapon: WeaponDTO|null, helmet: ArmorDTO|null, gloves: ArmorDTO|null, armor: ArmorDTO|null, legs: ArmorDTO|null, boots: ArmorDTO|null, accessory1: ArmorDTO|null, accessory2: ArmorDTO|null }, weapon_types, weapons, armor_items: ArmorDTO[], equip_bonus: EquipBonusDTO }`
  - `armor_items`＝目前職業的 50 件防具（job_id＝職業）＋ 90 件通用飾品（job_id null，18 效果×5 級）；未選職業＝只有飾品。
  - ArmorDTO = `{ id, job_id: string|null, slot: "helmet"|"gloves"|"armor"|"legs"|"boots"|"accessory", tier, name, rarity, level_req, profile: ArmorProfile, description, can_equip: bool, equipped_in: string|null }`（equipped_in＝實際佔用的格子名，例如 "accessory2"）
  - ArmorProfile（snake_case）＝ CONTRACT §2 的 JSON。
  - EquipBonusDTO（snake_case，彙總後，含武器）= `{ str, agi, vit, dex, int, luk, def, hp_pct, mp_pct, atk_pct, matk_pct, interval_pct, crit_pct, crit_dmg_pct, mp_cost_reduce_pct, hp_regen_pct_per_5s, mp_regen_pct_per_5s, damage_taken_pct, element_resist_pct }`。
- `PUT /rpg/equipment/{slot}`（slot ∈ helmet|gloves|armor|legs|boots|accessory1|accessory2）body `{ item_id: string|null }` → 回 `/rpg/equipment` 同形；錯誤碼 `not_found`、`wrong_slot`（item.slot 與格子不符；accessory1/2 對應 item.slot=accessory）、`wrong_job`（防具 job_id ≠ 目前職業）、`level_too_low`、`duplicate_accessory`。
- `PUT /rpg/equipment/weapon` 不變；`PUT /rpg/job` 換職業自動卸下武器與五部位防具（飾品保留）。
- `/rpg/me` 新增 `equipment: { weapon, helmet, gloves, armor, legs, boots, accessory1, accessory2 }`（各格 item 名稱或 null）與 `equip_bonus: EquipBonusDTO`；衍生值已含全部裝備。
- 後台：`GET/PUT/DELETE /admin/rpg/armor-items`（可 `?job_id=`／`?slot=` 篩選）。

## 戰鬥 bootstrap（既有 wire 加欄位）
- 玩家 party member 新增 `equipmentEffects: { intervalPct, mpCostReducePct, hpRegenPctPer5s, mpRegenPctPer5s, damageTakenPct, elementResistPct }`（**只彙總防具與飾品**；武器仍走 `weapon.profile`）。傭兵一律零值物件。
- 玩家 stats/rating 已含全部裝備（Compute）。

## 引擎（TS）
- `PartyActor.equipmentEffects`；攻擊冷卻用 `weapon.intervalPct + equipmentEffects.intervalPct`（clamp ≥ −50）；受傷時 `elementResistPct` 相加（clamp ≤ 60）、`damageTakenPct` 與 buff 相加（clamp ≥ −60）；技能 MP 成本 `max(1, floor(mpCost × (1 − mpCostReducePct/100)))` 同時用於檢查與扣除；每 5000ms 依戰鬥時鐘對玩家回復 floor(hpMax×hpRegenPctPer5s/100)、floor(mpMax×mpRegenPctPer5s/100)（發既有 regen 事件，浮字沿用；不超上限；死亡不回）。零值＝零改動（既有斷言不變）。
