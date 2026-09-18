# P7 介面契約（後端 ↔ 前端 ↔ 引擎）— 欄位名不得自改；P5／P6 WIRE 仍有效

## REST（requireEntry＋既有 RateLimit）
- `GET /rpg/equipment` → `{ job: JobDTO|null, effective_level, equipped: { weapon: WeaponDTO|null }, weapon_types: WeaponTypeDTO[], weapons: WeaponDTO[] }`（weapons＝目前職業全部武器，未選職業→空陣列）
  - WeaponTypeDTO = `{ id, job_id, name, visual, elemental_capable, description, traits: object, sort_order }`
  - WeaponDTO = `{ id, type_id, tier, name, rarity, level_req, element, profile: WeaponProfile, description, can_equip: bool, equipped: bool }`
  - WeaponProfile（snake_case，與 rpg_weapons.profile JSON 同形）= `{ atk, matk, int_bonus, mp_pct, atk_pct, matk_pct, def_pct, mdef_pct, hits, hit_mul, extra_hit_chance_pct, interval_pct, charge_time_mul, charge_dmg_mul, splash_pct, size_bonus: {small, medium, large}, crit_pct, crit_dmg_pct, flee_bonus, element_resist_pct, magic_skill_pct, element }`（缺欄位＝中性值）
- `PUT /rpg/equipment/weapon` body `{ item_id: string|null }` → 回 `/rpg/equipment` 同形；錯誤碼 `not_found`、`wrong_job`、`level_too_low`。
- `/rpg/me` 新增 `weapon: WeaponDTO|null`；衍生值已含裝備效果。
- `PUT /rpg/job`：換職業時若已裝備的武器 type.job_id ≠ 新職業 → 自動卸下（回應同 /rpg/me）。
- 後台：`GET/PUT /admin/rpg/weapon-types`、`GET/PUT/DELETE /admin/rpg/weapons`（比照既有 content CRUD 慣例；rpg_config 新增三個 element 百分比欄位）。

## 戰鬥 bootstrap（既有 wire 加欄位）
- 玩家 party member 新增 `weapon: { id, name, typeId, visual, profile: WeaponProfileWire }|null`；`weapon`（視覺字串）改由 type.visual 決定。
  - WeaponProfileWire（camelCase）= `{ atk, matk, hits, hitMul, extraHitChancePct, intervalPct, chargeTimeMul, chargeDmgMul, splashPct, sizeBonus: {small, medium, large}, critPct, critDmgPct, elementResistPct, magicSkillPct, element }`（Compute 已吃掉的 int_bonus/mp_pct/atk_pct/matk_pct/def_pct/mdef_pct/flee_bonus/crit_pct 不再送；critPct 仍送供除錯顯示）
- config 新增 `elementAdvantagePct`、`elementDisadvantagePct`、`elementSamePct`。
- enemy 已有 `attribute`、`size`、`weakElements`；本輪引擎真的讀它們。

## 引擎（TS）
- `elementMultiplier(cfg, attackElement, enemy)`：chart 覆寫 > 五行/光暗表（adv/disadv/same）與 weakElements 取大 > 1。
- 普攻：hits×hitMul、extraHitChancePct（rng）、sizeBonus、武器 element；neutral 物理技能帶武器 element；magicSkillPct 乘魔法技能與治療 coef；splashPct 對同排相鄰存活敵人；intervalPct 乘攻擊冷卻；chargeTimeMul／chargeDmgMul 乘蓄氣；elementResistPct 減免非 neutral 怪物傷害。事件：濺射用既有傷害事件加 `splash: true` 旗標供浮字區分。
