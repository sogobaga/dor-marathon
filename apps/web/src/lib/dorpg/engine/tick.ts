// tick(state, now)：推進施法/硬直計時、隊友 AI、敵人 AI、逃跑判定、勝負結算。
// 純函式：輸入的 state（及其巢狀物件）不會被動到，永遠回傳一份新的（toCtx 已淺拷貝過一輪）。
import { advanceAllyAI, advanceEnemyAI, advanceEnemyDeath, refreshFocusTarget } from './ai';
import { advanceAutoBattle } from './autopilot';
import { applyHealToTarget, resolveCastEffect } from './combat';
import type { Ctx } from './context';
import { fromCtx, pushEvent, toCtx } from './context';
import { pruneAndRegenEffects } from './effects';
import { floorInt } from './formulas';
import { advanceSummons } from './summon';
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

/**
 * P8（DORPG_P8 CONTRACT §2／WIRE「引擎」：「每 5000ms 依戰鬥時鐘對玩家回復...」）：裝備
 * （防具＋飾品彙總，不含武器）的定時回復——跟 hp_regen_pct buff 的 1000ms 節奏是完全獨立的兩套
 * 排程（見 PartyActor.nextEquipRegenAt 型別註解），對所有隊員一視同仁套用同一段邏輯：契約明講
 * 「傭兵一律零值物件」，傭兵的 equipmentEffects 恆為零值，算出來的回復量恆為 0，等同沒有效果，
 * 不需要另外用 isPlayer 分支排除。
 * HP 回復沿用既有的 applyHealToTarget（推 'heal' 事件，浮字沿用，見任務回報決策）；MP 回復目前
 * 沒有對應的「既有事件」可沿用（回顧全引擎，MP 的任何變化——技能消耗／道具回復——本來就是純
 * 狀態更新、不推專屬事件，MP 條本身就是靠讀 actor.mp 即時渲染），這裡比照同一慣例直接更新
 * actor.mp，不發明一個新的 BattleEvent kind（回報見任務回報，若 FRONTEND 之後需要 MP 回復的浮字，
 * 需要另外請 FRONTEND 決定要不要新增事件）。
 * while 迴圈同 pruneAndRegenEffects：一次 tick 若跨過不只一個 5000ms 窗口，要把該回的量一次全部
 * 補上，不能只回一次就把 nextEquipRegenAt 推到未來、丟掉中間應該發生的幾次回復；死亡（hp≤0）
 * 期間整個跳過、不推進這個時間點（跟 hp_regen_pct 對死亡角色的既有處理一致，見 pruneAllEffects）。
 */
function applyEquipmentRegen(ctx: Ctx): void {
  for (const actor of ctx.party) {
    if (actor.hp <= 0) continue; // 死亡不回（CONTRACT 明講）。
    const eff = actor.equipmentEffects;
    let next = actor.nextEquipRegenAt;
    while (next <= ctx.now) {
      if (eff.hpRegenPctPer5s > 0 && actor.hp > 0) {
        const hpAmt = floorInt(actor.stats.hpMax * (eff.hpRegenPctPer5s / 100));
        if (hpAmt > 0) applyHealToTarget(ctx, actor.id, actor.id, hpAmt); // 封頂 hpMax 由 applyHealToTarget 負責。
      }
      if (eff.mpRegenPctPer5s > 0) {
        const mpAmt = floorInt(actor.stats.mpMax * (eff.mpRegenPctPer5s / 100));
        if (mpAmt > 0) actor.mp = floorInt(Math.min(actor.stats.mpMax, actor.mp + mpAmt));
      }
      next += 5000;
    }
    actor.nextEquipRegenAt = next;
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
  applyEquipmentRegen(ctx); // P8：裝備（防具＋飾品）每 5000ms 定時回復，跟上面 buff 的 1000ms 節奏各自獨立。
  refreshFocusTarget(ctx); // P9：focus_fire 全隊共用目標，要在隊友 AI／玩家自動戰鬥之前就先算好。
  advanceAutoBattle(ctx); // P9（WIRE「引擎」：「tick 在隊友 AI 之前呼叫」）：玩家自動戰鬥。

  // P6（CONTRACT §3.2）：每位隊友用自己的 skills 決定要不要用技能，不再需要指定「哪一位是輔助」
  // ——advanceAllyAI 內部依五段優先序自行判斷（見 ai.ts 檔頭註解）。
  for (const actor of ctx.party) {
    if (!actor.isPlayer) advanceAllyAI(ctx, actor);
  }
  advanceSummons(ctx); // P11（CONTRACT §1「召喚（A 以上）」、WIRE「引擎」）：tick 內在敵人 AI 之前呼叫。
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
