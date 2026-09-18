# P6 介面契約（後端 ↔ 前端 ↔ 引擎）— 三位工人共同遵守，欄位名不得自改；P5 WIRE 仍有效

## REST（全部走 requireEntry＋既有 RateLimit）
- `GET /rpg/tavern` → `{ leader: { job: JobDTO|null, effective_level, name }, party: PartySlotDTO[4], mercenaries: MercenaryDTO[] }`
  - PartySlotDTO = `{ slot: 1..4, companion_id: string|null, preset_id: string|null, preset_name: string|null, level: number|null }`（空格 companion_id=null）
  - MercenaryDTO = `{ id, name, portrait_id, role, job: JobDTO, in_party: bool, presets: PresetDTO[] }`（presets 含系統預設在前、使用者自訂在後）
  - PresetDTO = `{ id, companion_id, name, level, is_system: bool, stats: {str,agi,vit,dex,int,luk}, skill_levels: {[skill_id]: number}, derived: DerivedDTO, stat_points_total, stat_points_free, stat_cap, skill_points_total, skill_points_free }`
  - DerivedDTO = `{ hp_max, mp_max, atk, matk, def, mdef, hit, flee, aspd, crit_pct }`（整數，已乘傭兵倍率）
- `PUT /rpg/party` body `{ slots: [{ slot, companion_id, preset_id|null }] }`（只送有人的格子；伺服器整批覆蓋 4 格）→ 回 `/rpg/tavern` 同形；錯誤碼 `party_full`、`duplicate_companion`、`not_mercenary`、`preset_mismatch`（腳本不屬於該傭兵或不屬於本人／系統）。
- `POST /rpg/presets` body `{ companion_id, name, level, stats, skill_levels }` → 201 `PresetDTO`；`PUT /rpg/presets/{id}` 同 body → `PresetDTO`；`DELETE /rpg/presets/{id}` → `{ok:true}`（系統預設 → 403 `preset_readonly`）。
- `POST /rpg/presets/validate` body `{ companion_id, level, stats, skill_levels }` → `{ ok: bool, errors: [{field, code, message}], stat_points_total, stat_points_free, stat_cap, skill_points_total, skill_points_free, derived: DerivedDTO, skills: SkillDTO[] }`（skills＝該職業 10 個含 level／prereq_ok／can_level_up，與 P5 SkillDTO 同形）。錯誤碼：`stat_over_budget`、`stat_over_cap`、`stat_below_initial`、`skill_over_budget`、`skill_prereq`、`skill_max_level`、`skill_not_in_job`、`level_range`、`name_required`。
- 戰鬥 `GET /rpg/battle/encounters`：每場新增 `monster_level`。

## 戰鬥 bootstrap（既有 wire 加欄位）
- enemy 新增 `level: number`（怪物等級 N，顯示 Lv.N）。
- party member（隊友）：`stats`／`rating` 改為該傭兵自己的（Compute×倍率）；新增 `skills: wireSkill[]`（AI 可用技能，已展開、不含 passive、只含 implemented=true）；新增 `presetName: string`、`level: number`。
- config 新增 `scaleMode: "level"|"power"`（純顯示／除錯）。

## 引擎（TS）
- 整數不變式：所有 hp/mp 變動 floor。
- 隊友 AI 讀 `member.skills`，依契約 §3.2 五段優先序決策；沿用既有冷卻/MP/詠唱規則與 rng。
- fixture.ts：level 模式用 `refPlayerTable.json`；離線預覽的隊友用內建示範腳本。
