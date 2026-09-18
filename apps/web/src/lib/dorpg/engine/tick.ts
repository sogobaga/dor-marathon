// tick(state, now)：推進施法/硬直計時、隊友 AI、敵人 AI、逃跑判定、勝負結算。
// 純函式：輸入的 state（及其巢狀物件）不會被動到，永遠回傳一份新的（toCtx 已淺拷貝過一輪）。
import { advanceAllyAI, advanceEnemyAI, advanceEnemyDeath } from './ai';
import { applyHealToTarget, resolveCastEffect } from './combat';
import type { Ctx } from './context';
import { fromCtx, pushEvent, toCtx } from './context';
import { pruneAndRegenEffects } from './effects';
import type { BattleOutcome, BattleState } from './types';

/** casting 完成→結算效果轉 recovering；recovering 到時→idle。單次呼叫只推進一步，理由同 ai.ts。 */
function resolvePartyTimers(ctx: Ctx): void {
  for (const actor of ctx.party) {
    if (actor.hp <= 0) {
      if (actor.action !== 'dead') actor.action = 'dead';
      delete ctx.pendingCasts[actor.id];
      continue;
    }
    if (actor.action === 'casting' && ctx.now >= actor.actionUntil) {
      const pending = ctx.pendingCasts[actor.id];
      const skill = pending ? ctx.skills.find((s) => s?.id === pending.skillId) : undefined;
      if (pending && skill) resolveCastEffect(ctx, actor, skill, pending);
      delete ctx.pendingCasts[actor.id];
      if (actor.hp > 0) {
        actor.action = 'recovering';
        actor.actionUntil = ctx.now + ctx.cfg.recoveryMs;
      }
    } else if (actor.action === 'recovering' && ctx.now >= actor.actionUntil) {
      actor.action = 'idle';
    }
  }
}

/**
 * P5（CONTRACT §5）：每個 tick 清掉雙方到期的 buff/debuff，並處理 hp_regen_pct 的定時回復。
 * 死掉的角色（hp<=0）整個跳過——買狀態沒有意義，且死亡隊友的 activeEffects 留著不會造成任何
 * 效果（effectiveStats/effectiveRating 只在計算戰鬥數值時才會被讀取），等復活後續下一次
 * tick 會自然接著清理，不需要在死亡當下另外清空。
 */
function pruneAllEffects(ctx: Ctx): void {
  for (const actor of ctx.party) {
    if (actor.hp <= 0) continue;
    pruneAndRegenEffects(
      ctx.now,
      actor,
      (amount) => {
        if (amount > 0) applyHealToTarget(ctx, actor.id, actor.id, amount);
      },
      (expired) => pushEvent(ctx, { kind: 'statusExpired', targetId: actor.id, stat: expired.stat }),
    );
  }
  for (const enemy of ctx.enemies) {
    if (enemy.hp <= 0) continue;
    // 怪物只會有 debuff（見 BuffDebuffStat 型別註解），詞彙表裡沒有 hp_regen_pct 這一項，
    // onRegen 永遠不會被呼叫到——仍要傳一個空函式滿足簽章。
    pruneAndRegenEffects(
      ctx.now,
      enemy,
      () => {},
      (expired) => pushEvent(ctx, { kind: 'statusExpired', targetId: enemy.id, stat: expired.stat }),
    );
  }
}

/** 回傳 true 代表本次 tick 判定出逃跑成功（tick() 會因此直接把整場結束）。 */
function resolveEscape(ctx: Ctx): boolean {
  if (ctx.escape.flow !== 'judging' || ctx.escape.judgingUntil === null) return false;
  if (ctx.now < ctx.escape.judgingUntil) return false;
  ctx.escape.judgingUntil = null;
  const player = ctx.party.find((p) => p.id === ctx.playerId);
  if (!player || player.hp <= 0) {
    // 理論上 applyPartyDamage 已經在玩家倒下當下就把 flow 標成 failed 並提早 return，
    // 這裡進得來代表那條路徑沒攔到，當保險再標一次（例如未來改動 tick 內部順序時的安全網）。
    ctx.escape.flow = 'failed';
    ctx.escape.message = '逃跑失敗';
    pushEvent(ctx, { kind: 'escapeFailed' });
    return false;
  }
  if (ctx.rng() < ctx.escape.chance) {
    // 'unavailable' 借用來表示「已經逃走、逃跑機制不再相關」；outcome='escaped' 才是權威訊號，
    // 戰鬥結束後 UI 不會再需要靠 escape.flow 分辨這跟 BOSS 不可逃的初始狀態有什麼不同。
    ctx.escape.flow = 'unavailable';
    ctx.escape.message = '成功逃離';
    pushEvent(ctx, { kind: 'escaped' });
    return true;
  }
  ctx.escape.flow = 'failed';
  ctx.escape.message = '逃跑失敗';
  pushEvent(ctx, { kind: 'escapeFailed' });
  return false;
}

/**
 * 匯出給 dispatch.ts 共用：玩家自己的攻擊／技能就可能一擊終結戰鬥，如果只有 tick() 會檢查勝負，
 * 那要等「下一次」呼叫（下一幀 rAF 的 tick）才會反映成 ended，直接呼叫 dispatch() 的呼叫端（例如
 * 這支驗證腳本）會看到明明敵人已經死光卻還是 phase:'active' 的過渡態。讓 dispatch 每個指令分支
 * 結束前也跑一次同樣的判定，兩邊共用同一份邏輯，行為才會一致。
 */
export function computeVictoryDefeatDraw(ctx: Ctx): 'victory' | 'defeat' | 'draw' | null {
  const enemiesDown = ctx.enemies.length > 0 && ctx.enemies.every((e) => e.hp <= 0);
  const partyDown = ctx.party.length > 0 && ctx.party.every((p) => p.hp <= 0);
  if (enemiesDown && partyDown) return 'draw';
  if (enemiesDown) return 'victory';
  if (partyDown) return 'defeat';
  return null;
}

/**
 * 審查修復 #1：敵人全滅那一刻直接 ended 會把死亡動畫（dying→removed）蓋掉。改成先進 'resolving'
 * （outcome 先記，但還不 push 'ended'），這裡負責決定什麼時候才真的可以轉成 'ended'：
 * - victory：等所有 hp≤0 的敵人都播完死亡動畫（anim==='removed'）——tick() 在 resolving 期間
 *   仍會呼叫這裡繼續推進 dying→removed，但不會再跑 windup/attacking（結果已定，沒有意義）。
 * - defeat/draw：隊伍全倒沒有動畫好等，改用 resolvingSince + config.resolveDelayMs 的固定延遲。
 * 逃跑成功不經過這裡（resolveEscape 命中時直接 ended，維持即時，因為那本來就沒有「死亡動畫」要等）。
 */
function advanceResolving(ctx: Ctx, outcome: Exclude<BattleOutcome, 'escaped'>): BattleState {
  if (outcome === 'victory') {
    for (const enemy of ctx.enemies) {
      if (enemy.hp <= 0) advanceEnemyDeath(enemy, ctx.now);
    }
    const allRemoved = ctx.enemies.length === 0 || ctx.enemies.every((e) => e.anim === 'removed');
    if (allRemoved) {
      pushEvent(ctx, { kind: 'ended', outcome });
      return fromCtx(ctx, 'ended', outcome);
    }
    return fromCtx(ctx, 'resolving', outcome);
  }
  const since = ctx.resolvingSince ?? ctx.now;
  if (ctx.now >= since + ctx.cfg.resolveDelayMs) {
    pushEvent(ctx, { kind: 'ended', outcome });
    return fromCtx(ctx, 'ended', outcome);
  }
  return fromCtx(ctx, 'resolving', outcome);
}

/** 匯出給 dispatch.ts：玩家自己這一下攻擊/技能判定出勝負時，也要走同一套「先 resolving 再 ended」流程。 */
export function beginResolving(ctx: Ctx, outcome: Exclude<BattleOutcome, 'escaped'>): BattleState {
  ctx.resolvingSince = ctx.now;
  return advanceResolving(ctx, outcome);
}

export function tick(state: BattleState, now: number): BattleState {
  if (state.phase === 'ended' || state.phase === 'loading') return state; // 契約：「ended 後 tick 不再推進」。
  const ctx = toCtx(state, now);

  if (state.phase === 'resolving') {
    // resolving 期間指令一律拒絕（dispatch 的頂端已檔：phase!=='active' 直接原樣回傳），
    // 但 tick 仍要推進——只做「等結案」這件事，不跑 ally/enemy 的一般 AI、也不判逃跑。
    const outcome = state.outcome;
    if (!outcome || outcome === 'escaped') return state; // 不該發生（resolving 只會因 victory/defeat/draw 進來），防呆。
    return advanceResolving(ctx, outcome);
  }

  resolvePartyTimers(ctx);
  pruneAllEffects(ctx); // P5：buff/debuff 到期清除＋hp_regen_pct 定時回復，跑在 AI 出手之前。

  // P6（CONTRACT §3.2）：每位隊友用自己的 skills 決定要不要用技能，不再需要指定「哪一位是輔助」
  // ——advanceAllyAI 內部依五段優先序自行判斷（見 ai.ts 檔頭註解）。
  for (const actor of ctx.party) {
    if (!actor.isPlayer) advanceAllyAI(ctx, actor);
  }
  for (const enemy of ctx.enemies) {
    advanceEnemyAI(ctx, enemy);
  }

  if (resolveEscape(ctx)) {
    pushEvent(ctx, { kind: 'ended', outcome: 'escaped' });
    return fromCtx(ctx, 'ended', 'escaped');
  }

  const outcome = computeVictoryDefeatDraw(ctx);
  if (outcome) {
    return beginResolving(ctx, outcome);
  }
  return fromCtx(ctx, 'active', null);
}
