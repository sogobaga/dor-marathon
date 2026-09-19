# P10 介面契約（後端 ↔ 前端 ↔ 引擎）— 欄位名不得自改；P5–P9 WIRE 仍有效

## REST
- JobDTO（/rpg/me、/rpg/jobs、/rpg/tavern 的 job、後台 jobs）新增 `paths: [{ id: string, key: "a"|"b"|"c", name: string, desc: string }]`（依 key 排序；只有 heavy_knight 有 c）與 `traits: { damage_taken_pct?: number }`（缺省 `{}`）。既有 `path_a_id/path_a_name/path_a_desc/path_b_*` 欄位保留。
- SkillDTO：`kind` 可為 `"taunt"`；`path` 可為 `"c"`；展開值（既有 `effect_expanded`／display 欄位所在處）新增 `taunt: { duration_ms, damage_taken_pct, retarget }`（僅 kind=taunt）。passive 展開新增 `guard_taunt: boolean`。
- 後台：`PUT /admin/rpg/skills` 接受 kind=taunt、path=c；`PUT /admin/rpg/jobs` 接受 `path_c_id/path_c_name/path_c_desc/traits`。
- 酒館／腳本／裝備端點形狀不變（傭兵換職業只是資料）。

## 戰鬥 bootstrap（既有 wire 加欄位）
- party member 新增 `guardTaunt: boolean`（守護本能 ≥1 級）、`jobTraits: { damageTakenPct: number }`（已合併進 `equipmentEffects.damageTakenPct`，另送供顯示）。
- wire skill（玩家 `skills[]` 與傭兵 `skills[]`）新增 `kind: "taunt"` 與 `taunt: { durationMs, damageTakenPct, retarget }`。
- 事件新增 `{ kind: "taunt", actorId, enemyIds: string[] }`。

## 引擎（TS）
- `types.ts`：`SkillKind` 加 `taunt`；`PartyActor` 加 `tauntUntil: number`（0＝無）、`guardTaunt: boolean`；`BattleEvent` 加 `taunt`。
- `formulas.ts`：`inGuardianState(actor, now)`；`pickEnemyTarget(ctx, rng)`（守護者優先，否則 `pickWeightedAliveTarget`）。
- `combat.ts`：`resolveTaunt(ctx, caster, skill)`（設 tauntUntil、可選 damage_taken_pct ActiveEffect、retarget 所有存活敵人、發 taunt 事件）。
- `ai.ts`：`advanceEnemyAI` 改用 `pickEnemyTarget`；`decideAction` 加守護判斷（CONTRACT §3）。
- `dispatch.ts`：USE_SKILL 對 kind=taunt 走 resolveTaunt（玩家）；GUARD_BEGIN/END 不改（守護狀態由 inGuardianState 判定）。
- `fromApi.ts`／`fixture.ts`：映射 guardTaunt、jobTraits、taunt 技能欄位；離線隊伍阿深帶 hk_c1/hk_c3。
- 前端顯示：PartyCard 狀態標籤「守護」；FloatText「挑釁！」；SKILL_KIND_LABEL taunt→「守護」。
