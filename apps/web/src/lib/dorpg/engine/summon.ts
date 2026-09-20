// P11（DORPG_P11 CONTRACT §1「召喚（A 以上）」、WIRE「引擎」）：advanceSummons(ctx)——tick() 在
// 敵人 AI 之前呼叫一次的純函式，就地改 ctx.enemies/ctx.summonedWaves/ctx.events，不回傳值，跟
// 本引擎其餘 advance*() 系列（ai.ts advanceEnemyAI／effects.ts pruneAndRegenEffects）同一種呼叫
// 慣例。bootstrap 已經把每一波要召喚的怪物算好（BattleState.summonPool，見 dorpg/types.ts
// SummonWave 型別註解）——這裡只負責「什麼時候放」「放去哪個槽位」「放不下就略過」三件事，不
// 重新計算任何怪物數值。
import type { Enemy, EnemySlotId } from '../types';
import type { Ctx } from './context';
import { pushEvent } from './context';
import { toEnemyActor } from './formulas';
import type { EnemyActor } from './types';

/**
 * 契約 §3 逐字給的空槽嘗試順序——front_left→front_right→rear_left→rear_right→front_center。
 * 刻意跟 formulas.ts 既有的 SLOT_ORDER（後排優先，服務「初始鎖定目標」這種挑存活敵人的場景）
 * 方向相反：召喚怪從前排冒出來，視覺上更像「主戰者身後跳出支援」。兩份槽位序服務不同語意，
 * 不合併成一份共用常數。
 */
const SUMMON_SLOT_ORDER: readonly EnemySlotId[] = ['front_left', 'front_right', 'rear_left', 'rear_right', 'front_center'];

/** 場上（含召喚者本身與所有既有存活敵人）上限 5 隻——契約 §1「場上敵人上限 5（含召喚者）」。 */
const MAX_ALIVE_ENEMIES = 5;

/**
 * 依契約 §1 逐一嘗試放入 wave.enemies：容量（5 減目前存活敵人數）用完，或五個槽位都被存活敵人
 * 佔滿，就在該筆停止——放不下的部分直接捨棄，不會遞延到下一個 tick 補放（這一整波已經在呼叫端
 * 標記為「已消耗」，見 advanceSummons）。一隻都放不下時回傳空陣列，呼叫端據此判斷要不要推
 * 'summon' 事件（契約「無空位略過」，這裡把「完全無空位」與「部分放不下」統一用同一套容量邏輯
 * 表達，不是兩條獨立分支）。
 */
function trySpawnWave(ctx: Ctx, waveEnemies: Enemy[]): EnemyActor[] {
  const takenSlots = new Set(ctx.enemies.filter((e) => e.hp > 0).map((e) => e.slot));
  let aliveCount = takenSlots.size;
  const spawned: EnemyActor[] = [];
  for (const enemyWire of waveEnemies) {
    if (aliveCount >= MAX_ALIVE_ENEMIES) break;
    const slot = SUMMON_SLOT_ORDER.find((s) => !takenSlots.has(s));
    if (!slot) break; // 五個槽位都被佔滿（理論上跟 aliveCount>=5 同步，這裡是雙重防呆）。
    takenSlots.add(slot);
    aliveCount += 1;
    // WIRE「enemies[].slot 為空字串由引擎決定」：這裡覆寫成剛剛找到的空槽，wire 原始值只是佔位。
    const actor = toEnemyActor({ ...enemyWire, slot }, ctx.now, ctx.cfg, ctx.rng);
    actor.isSummoned = true; // engine 是「這隻怪確實是召喚出來的」唯一權威來源，不管 wire 原始值是什麼。
    spawned.push(actor);
  }
  return spawned;
}

export function advanceSummons(ctx: Ctx): void {
  if (ctx.summonPool.length === 0) return; // 沒有召喚機制的既有場景（legacy 六場）完全是 no-op。

  for (const wave of ctx.summonPool) {
    const key = `${wave.summonerEnemyId}:${wave.atHpPct}`;
    if (ctx.summonedWaves.includes(key)) continue; // 已觸發過，不重複判定。

    const summoner = ctx.enemies.find((e) => e.id === wave.summonerEnemyId);
    // 召喚者不存在（資料錯置）或已死亡——契約「召喚者死亡後未觸發的波不再觸發」：死亡判定必須
    // 排在門檻判定之前，這樣即使召喚者被一擊打到 0%（從未被任何一個 tick 看到它「正好」跨過
    // atHpPct 這個中間值），這一波也永遠不會觸發，而不是「反正 0% ≤ atHpPct」就補放。這裡故意
    // 不把這一波標進 summonedWaves——它從來沒有被判定過，語意上不是「已消耗」。
    if (!summoner || summoner.hp <= 0) continue;

    const hpPct = (summoner.hp / summoner.stats.hpMax) * 100;
    if (hpPct > wave.atHpPct) continue; // 尚未跨過門檻，之後的 tick 再檢查。

    ctx.summonedWaves.push(key); // 不論放不放得下都標記已消耗（契約：無空位略過，不重複判定）。

    const spawned = trySpawnWave(ctx, wave.enemies);
    if (spawned.length === 0) continue; // 一隻都放不下：契約「無空位略過」，不推事件。

    ctx.enemies.push(...spawned);
    pushEvent(ctx, { kind: 'summon', summonerId: wave.summonerEnemyId, enemyIds: spawned.map((e) => e.id) });
  }
}
