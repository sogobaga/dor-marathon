# P5 介面契約（後端 ↔ 前端 ↔ 引擎）— 三位工人共同遵守，欄位名不得自行更動

## 會員端 REST（全部沿用 requireEntry 白名單閘門與既有 RateLimit）
- `GET /rpg/jobs` → `{ jobs: JobDTO[] }`，JobDTO = `{ id, name, tagline, description, path_a: {id,name,desc}, path_b: {id,name,desc}, weapon, atk_branch, recommended_stats, sort_order }`
- `GET /rpg/me`（既有）新增欄位：`job: JobDTO|null`, `test_level: number|null`, `effective_level: number`（test_level ?? 真實等級）, `stat_points_total`, `stat_points_free`, `stat_cap`（= min(max_stat, effective_level)）, `skill_points_total`, `skill_points_free`；既有 `free_points` 改為回傳與 `stat_points_free` 相同的推導值。
- `PUT /rpg/job` body `{ job_id: string|null }` → 回 `/rpg/me` 同形。
- `PUT /rpg/test-level` body `{ level: number|null }`（1–99）→ 回 `/rpg/me` 同形。
- `POST /rpg/allocate` body `{ stat, points?: number, mode?: "max" }`（二選一；points≥1；mode=max 由伺服器算能加幾點）→ 回 `/rpg/me` 同形；超過 stat_cap 回 400 `{error:"stat_cap"}`。
- `POST /rpg/stats/reset` → 六圍回 initial、寫 player_stat_log 6 筆（cost 負值）→ 回 `/rpg/me` 同形。
- `GET /rpg/skills` → `{ job: JobDTO|null, skills: SkillDTO[], skill_points_total, skill_points_free }`；未選職業 → skills 為空陣列。
  SkillDTO = `{ id, name, path: "a"|"b", tier, kind, dmg_type, element, target, max_level, level, prereq_skill_id, prereq_level, prereq_ok: bool, can_level_up: bool, mp_cost, mp_cost_per_level, cooldown_ms, cast_ms, display_text, implemented, effect_at_level: EffectAtLevel, effect_next_level: EffectAtLevel|null, lv_preview: {"1":string,"5":string,"10":string} }`
  EffectAtLevel = `{ kind, stat?, value?, duration_ms?, coef?, flat?, hits?, target, mp_cost }`（把 base+per_level×(lv−1) 展開後的即時值；level=0 時用 lv=1 預覽）
- `POST /rpg/skills/allocate` body `{ skill_id, delta: 1|-1|"max" }` → 回 `/rpg/skills` 同形；違反前置／點數不足／超過 max_level 回 400 帶 `error` 代碼（`prereq`, `no_points`, `max_level`, `min_level`）。
- `POST /rpg/skills/reset` → 目前職業全部技能 level=0 → 回 `/rpg/skills` 同形。

## 戰鬥 bootstrap（`GET /rpg/battle/bootstrap`，既有 wire 型別加欄位；名稱用既有的 camelCase 慣例）
- config 區塊新增：`critMultMin`（1.75）、`critMultMax`（2.25）、`weaknessBonusPct`（25）。既有 `critMultiplier` 可保留但前端不再使用。
- enemy 新增：`weakElements: string[]`（DOR 8 桶英文代碼）。
- 玩家 party member：`weapon` 改由職業決定（未選職業維持現行預設）、新增 `jobId: string|null`；玩家的 `stats` 已含 passive 技能加成（後端 Compute 時套用）。
- `skills`（技能欄）改為**最多 10 格**（`(wireSkill|null)[]`，長度 10）；每格 wireSkill 新增：`level`, `maxLevel`, `displayText`, `dmgType`, `implemented`, `effect: EffectAtLevel`（已依 level 展開）。未選職業 → 維持現行 is_default 技能填法（最多 8 格，其餘 null）。已選職業 → 該職業 level≥1 的技能依 path→tier 排序填入；`implemented=false` 的技能也送，但前端顯示為不可按。

## 引擎（TS）要吃的新欄位
- 暴擊倍率：每次暴擊 `mult = critMultMin + rng()*(critMultMax−critMultMin)`；仍扣防。
- 相剋：`elementMultiplier(skillElement, enemy)`：若 `battle_element_chart` 有 `[enemy.attribute][skillElement]` 的覆寫 → 用覆寫；否則 `skillElement ∈ enemy.weakElements ? 1+weaknessBonusPct/100 : 1`。
- 新 kind：`damage`（`hits`＞1 時每段各自結算與浮字；`target=allEnemies` 打全體）、`buff`／`debuff`（帶 duration，stat 集合見 CONTRACT §5；到期自動移除）、`passive`（不出現在技能欄；後端已算進 stats）、`special`（技能欄顯示但不可用）。`heal`／`shield` 沿用。
- `dmgType=magic` 用 MATK 扣 MDEF；`physical` 用 ATK 扣 DEF（沿用現行）。
