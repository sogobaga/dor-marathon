# P11 介面契約（後端 ↔ 前端 ↔ 引擎）— 欄位名不得自改；P5–P10 WIRE 仍有效

## REST
- `GET /rpg/battle/encounters` 每場新增 `scaling_mode: "legacy"|"rank"`、`level_mode: "fixed"|"player"`、`rank: string|null`、`rank_label: string|null`、`badge_color: string|null`、`monster_count: number|null`、`group: "story"|"rank"`（前端分組用；legacy 六場＝story）。`monster_level` 在 level_mode=player 時回玩家有效等級（伺服器算好）。
- `GET /rpg/ranks` → `{ ranks: RankDTO[] }`（RankDTO = `{ rank, label, sort_order, hp_mult, atk_mult, def_mult, mdef_mult, description, badge_color }`；不含 summon／level_curve）。
- 後台：`GET/PUT /admin/rpg/monster-ranks`（PUT body `{ rank, label, sort_order, hp_mult, atk_mult, def_mult, mdef_mult, level_curve, summon, badge_color, description }`；rank 固定九值，不可新增刪除；倍率 > 0；summon.waves[].rank 必須存在且 count 1–5、at_hp_pct 1–99、**power_scale 0.05–2.0（缺省 1.0，2026-09-20 修正新增，召喚怪整體強度折減旋鈕）**）；遭遇 PUT 接受 scaling_mode／level_mode／rank／monster_count；怪物 PUT 的 rank 必須在九值內。

## 戰鬥 bootstrap（既有 wire 加欄位）
- 頂層新增 `scalingMode`、`levelMode`、`monsterLevel`（實際使用的 N）。
- enemy 新增 `rank: string`、`rankLabel: string`、`badgeColor: string`、`isSummoned: false`。
- 新增 `summonPool: [{ summonerEnemyId, atHpPct, enemies: EnemyWire[] }]`（每波預先算好、含完整數值與 sprite；`enemies[].id` 唯一，例如 `<summoner>_w1_1`；`enemies[].slot` 恆為空字串——bootstrap 階段還不知道觸發當下哪些槽位是空的，站位由引擎在召喚觸發時依 §引擎 的空槽順序決定並覆寫，見 battle.go buildSummonPool／summon.ts trySpawnWave）。

## 引擎（TS）
- `BattleState.summonPool`、`summonedWaves: string[]`（非 `Set<string>`——`BattleState`/`Ctx` 全部欄位走一般物件淺拷貝，見 engine/context.ts toCtx／fromCtx；用 `.includes(key)` 查重複、`.push(key)` 標記已消耗，見 engine/summon.ts advanceSummons）；`tick` 內 `advanceSummons(ctx)`：召喚者存活且 `hp/hpMax×100 ≤ atHpPct` 且該波未觸發 → 依空槽位數放入（front_left→front_right→rear_left→rear_right→front_center 順序取空位）、標 `isSummoned=true`、事件 `summon { summonerId, enemyIds }`；上限 5。
- `fixture.ts`：`scaleMonsterByRank(ref, rank, monster, ps, slot)`（與 Go 逐位元）；legacy 路徑不變。
- 目標選擇、守護狀態、集中火力對新敵人自然生效（敵人陣列動態）。
- 事件 `summon`；PartyCard 不變；EnemyCard 徽章。

## 資料（migration 189）
- 見 CONTRACT §2。
