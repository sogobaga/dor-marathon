// dispatch(state, cmd, now)：先 tick 到 now（推進計時/AI），再套用玩家指令。純函式，兩段都各自
// 透過 toCtx/fromCtx 操作工作副本，不會動到傳入的 state。
import type { Skill } from '../types';
import { resolveAttackOrDamageSkill } from './combat';
import type { Ctx } from './context';
import { fromCtx, pushEvent, pushLog, toCtx } from './context';
import { effectiveRating } from './effects';
import { attackCooldownFor, chargeMultiplier, effectiveCastMs } from './formulas';
import type { BattleState, Command, PartyActor } from './types';
import { beginResolving, computeVictoryDefeatDraw, tick } from './tick';

/**
 * 每個指令分支結束前的共同出口：玩家自己這一下攻擊/技能就可能是壓垮敵人的最後一擊，
 * 如果只有 tick() 會檢查勝負，直接呼叫 dispatch() 的呼叫端要等「下一次」呼叫才會看到 resolving/ended，
 * 中間會有一個「敵人明明死光了但 phase 還是 active」的過渡態。這裡跟 tick() 共用同一份判定與同一套
 * 「先 resolving 再 ended」流程（審查修復 #1：勝利不能立刻蓋掉死亡動畫）。
 */
function finish(ctx: Ctx): BattleState {
  const outcome = computeVictoryDefeatDraw(ctx);
  if (outcome) return beginResolving(ctx, outcome);
  return fromCtx(ctx, 'active', null);
}

function reject(ctx: Ctx, reason: string): BattleState {
  pushLog(ctx, `[拒絕] ${reason}`);
  return finish(ctx);
}

/**
 * 開始施法：扣 MP、技能進冷卻（規格：「冷卻從施放起算」）、記下 pendingCast 等 tick 到 actionUntil
 * 時結算效果。commitCast 只有 dispatch 的 USE_SKILL 分支會呼叫、actor 一律是玩家（AI 隊友的技能
 * 走 ai.ts 的 resolveSupportSkill，完全不經過這裡）——所以 effectiveCastMs 套用 actor.rating 的
 * castReductionPct（P3：DEX→詠唱縮減）在這裡永遠只影響玩家，不需要另外分支判斷。
 */
function commitCast(ctx: Ctx, actor: PartyActor, skill: Skill, targetId: string | 'ALL' | 'ALL_ENEMIES'): BattleState {
  actor.mp -= skill.mpCost;
  ctx.skillReadyAt[skill.id] = ctx.now + skill.cooldownMs;
  actor.action = 'casting';
  // P5：玩家身上的 buff 可能有 castReductionPct 加成（DEX/INT 效果之外的額外來源），套 effectiveRating
  // 才會反映在施法時間上。
  actor.actionUntil = ctx.now + effectiveCastMs(skill.castMs ?? ctx.cfg.defaultCastMs, effectiveRating(actor.rating, actor.activeEffects), ctx.cfg);
  ctx.pendingCasts[actor.id] = { skillId: skill.id, targetId };
  ctx.targeting = { mode: 'none' };
  pushEvent(ctx, {
    kind: 'skillCast',
    actorId: actor.id,
    skillId: skill.id,
    targetId: targetId === 'ALL' || targetId === 'ALL_ENEMIES' ? null : targetId,
  });
  return finish(ctx);
}

function applyCommand(state: BattleState, cmd: Command, now: number): BattleState {
  if (state.phase !== 'active') return state; // 契約：「ended 後所有指令拒絕」。
  const ctx = toCtx(state, now);
  const player = ctx.party.find((p) => p.id === ctx.playerId);
  if (!player) return finish(ctx); // 理論上不會發生（playerId 一定指向 party[0]）。

  // 規格：「guarding 中所有其它指令被拒（回同 state，記 log）」——GUARD_END 是唯一的解除手段，例外放行。
  if (player.action === 'guarding' && cmd.type !== 'GUARD_END') {
    return reject(ctx, '防禦中，其它指令一律無效');
  }
  // 規格：「玩家 dead：所有指令拒絕（除 SELECT_TARGET/SET_TRAY）」。
  if (player.action === 'dead' && cmd.type !== 'SELECT_TARGET' && cmd.type !== 'SET_TRAY') {
    return reject(ctx, '玩家已倒下，指令無效');
  }

  switch (cmd.type) {
    case 'ATTACK_BEGIN': {
      if (player.action !== 'idle') return reject(ctx, '非待命狀態不能開始攻擊');
      if (player.attackReadyAt > ctx.now) return reject(ctx, '攻擊冷卻中');
      const target = ctx.enemies.find((e) => e.id === ctx.targetId && e.hp > 0);
      if (!target) return reject(ctx, '沒有有效目標');
      player.action = 'charging';
      player.chargeStartedAt = ctx.now;
      pushEvent(ctx, { kind: 'chargeStart', actorId: player.id });
      return finish(ctx);
    }

    case 'ATTACK_RELEASE': {
      if (player.action !== 'charging') return reject(ctx, '沒有正在蓄力的攻擊');
      const holdMs = Math.max(0, ctx.now - (player.chargeStartedAt ?? ctx.now));
      const chargeMul = chargeMultiplier(holdMs, ctx.cfg);
      const charged = holdMs >= ctx.cfg.chargeMinMs;
      if (ctx.targetId) {
        resolveAttackOrDamageSkill(ctx, {
          actorId: player.id,
          attackerStats: player.stats,
          attackerEffects: player.activeEffects,
          coefficient: 1,
          flat: 0,
          weapon: player.weapon,
          targetEnemyId: ctx.targetId,
          chargeMul,
          charged,
          attackerRating: player.rating, // 普攻不傳 element，combat.ts 內定 'neutral'。
        });
      }
      player.action = 'recovering';
      player.actionUntil = ctx.now + ctx.cfg.recoveryMs;
      // P3：AGI→攻速→攻擊冷卻，只套用在玩家身上（隊友的節奏在 ai.ts 用 allyActIntervalMs 排程，
      // 完全不呼叫 attackCooldownFor，不受這裡的改動影響）。P5：套 effectiveRating 讓玩家身上的
      // aspd buff 也能反映在攻擊冷卻上。
      player.attackReadyAt = ctx.now + attackCooldownFor(effectiveRating(player.rating, player.activeEffects), ctx.cfg);
      player.chargeStartedAt = null;
      return finish(ctx);
    }

    case 'HOLD_CANCEL': {
      // 規格：拖出/失焦/隱藏取消 → 回 idle、不進 CD、不出傷害（attackReadyAt 完全不動）。
      if (player.action !== 'charging') return reject(ctx, '沒有正在蓄力的攻擊可取消');
      player.action = 'idle';
      player.chargeStartedAt = null;
      pushEvent(ctx, { kind: 'chargeCancel', actorId: player.id });
      return finish(ctx);
    }

    case 'GUARD_BEGIN': {
      if (player.action !== 'idle') return reject(ctx, '非待命狀態不能開始防禦');
      player.action = 'guarding';
      player.actionUntil = Number.POSITIVE_INFINITY; // 防禦沒有自動到期，只能靠 GUARD_END 解除。
      return finish(ctx);
    }

    case 'GUARD_END': {
      if (player.action !== 'guarding') return reject(ctx, '目前沒有在防禦');
      player.action = 'idle';
      return finish(ctx);
    }

    case 'SELECT_TARGET': {
      const enemy = ctx.enemies.find((e) => e.id === cmd.enemyId && e.hp > 0);
      if (!enemy) return reject(ctx, '目標不存在或已消滅');
      ctx.targetId = enemy.id;
      pushEvent(ctx, { kind: 'targetChanged', enemyId: enemy.id });
      return finish(ctx);
    }

    case 'SET_TRAY': {
      ctx.trayMode = cmd.mode;
      return finish(ctx);
    }

    case 'USE_SKILL': {
      const skill = ctx.skills.find((s) => s?.id === cmd.skillId) ?? null;
      if (!skill) return reject(ctx, '技能未裝備');
      // P5（CONTRACT §5 special 詞彙）：implemented=false 的技能一律直接拒絕，且發專屬事件
      // 讓 FRONTEND 能跳「尚未實裝」提示，跟其它拒絕只留一行 log 不同——這個檢查刻意放在最前面，
      // 不管冷卻/MP/action 狀態如何，這種技能永遠不能用。
      if (skill.implemented === false) {
        pushEvent(ctx, { kind: 'skillUnavailable', actorId: player.id, skillId: skill.id });
        return reject(ctx, `${skill.name} 尚未實裝`);
      }
      // 防呆：passive 依規則不該出現在技能欄（後端已把它算進玩家 stats），正常情況下不會走到這裡；
      // 萬一離線資料/測試資料誤塞了一顆，明確拒絕比讓它落進下面的 target 判斷、意外套用一次 buff/
      // debuff 邏輯要安全。
      if (skill.kind === 'passive') return reject(ctx, '被動技能不會出現在技能欄，不可主動施放');
      if ((ctx.skillReadyAt[skill.id] ?? 0) > ctx.now) return reject(ctx, '技能冷卻中');
      if (player.mp < skill.mpCost) return reject(ctx, 'MP 不足');
      if (player.action !== 'idle') return reject(ctx, '非待命狀態不能施放技能');

      if (skill.target === 'ally') {
        // 規格：「需要選隊友的技能：第一次無 targetId → targeting=chooseAlly；再送一次帶 targetId」。
        if (cmd.targetId === undefined) {
          ctx.targeting = { mode: 'chooseAlly', skillId: skill.id };
          return finish(ctx);
        }
        const ally = ctx.party.find((p) => p.id === cmd.targetId);
        if (!ally || ally.hp <= 0) return reject(ctx, '無效的隊友目標'); // 死亡隊友只能靠道具復甦，技能不能選
        return commitCast(ctx, player, skill, ally.id);
      }
      if (skill.target === 'enemy') {
        if (!ctx.targetId || !ctx.enemies.find((e) => e.id === ctx.targetId && e.hp > 0)) {
          return reject(ctx, '沒有有效目標');
        }
        return commitCast(ctx, player, skill, ctx.targetId);
      }
      if (skill.target === 'self') {
        return commitCast(ctx, player, skill, player.id);
      }
      // P5：target='allEnemies'（damage/debuff 專用）跟既有 'allAllies' 對稱，同樣不要求事先選好
      // 目標——資源結算時 (resolveCastEffect/resolveBuffDebuff) 才即時篩選存活敵人。
      if (skill.target === 'allEnemies') {
        return commitCast(ctx, player, skill, 'ALL_ENEMIES');
      }
      return commitCast(ctx, player, skill, 'ALL'); // allAllies
    }

    case 'USE_ITEM': {
      if (player.action !== 'idle') return reject(ctx, '非待命狀態不能使用道具');
      const entry = ctx.items.find((i) => i.def.id === cmd.itemId);
      if (!entry || entry.quantity <= 0) return reject(ctx, '道具數量不足');
      // 規格：「USE_ITEM → 需選隊友（chooseAlly）」，跟技能的兩段式流程對稱。
      if (cmd.targetId === undefined) {
        ctx.targeting = { mode: 'chooseAlly', itemId: entry.def.id };
        return finish(ctx);
      }
      const target = ctx.party.find((p) => p.id === cmd.targetId);
      const isRevive = entry.def.kind === 'revive';
      const validTarget = !!target && (isRevive ? target.hp <= 0 : target.hp > 0);
      if (!validTarget || !target) {
        // 規格：「失敗（無效目標/數量0）→維持items模式」；targeting 清掉讓玩家能重新選，trayMode 不動。
        ctx.targeting = { mode: 'none' };
        return reject(ctx, '無效的道具目標');
      }
      entry.quantity -= 1;
      if (isRevive) {
        target.hp = Math.round(target.stats.hpMax * (entry.def.amount / 100));
        target.action = 'idle';
        pushEvent(ctx, { kind: 'actorRevive', actorId: target.id });
      } else if (entry.def.kind === 'hp') {
        target.hp = Math.min(target.stats.hpMax, target.hp + entry.def.amount);
      } else {
        target.mp = Math.min(target.stats.mpMax, target.mp + entry.def.amount);
      }
      pushEvent(ctx, { kind: 'itemUsed', itemId: entry.def.id, targetId: target.id, amount: entry.def.amount });
      ctx.trayMode = 'skills';
      ctx.targeting = { mode: 'none' };
      player.action = 'recovering';
      player.actionUntil = ctx.now + ctx.cfg.recoveryMs;
      return finish(ctx);
    }

    case 'CANCEL_TARGETING': {
      if (ctx.targeting.mode === 'none') return reject(ctx, '目前沒有在選擇目標');
      ctx.targeting = { mode: 'none' };
      return finish(ctx);
    }

    case 'TRY_ESCAPE': {
      if (player.action !== 'idle') return reject(ctx, '非待命狀態不能嘗試逃跑');
      if (ctx.escape.flow !== 'available') return reject(ctx, '目前無法逃跑');
      ctx.escape.flow = 'judging';
      ctx.escape.judgingUntil = ctx.now + ctx.cfg.escapeJudgeMs;
      ctx.escape.message = '';
      pushEvent(ctx, { kind: 'escapeJudging' });
      return finish(ctx);
    }

    default: {
      const _exhaustive: never = cmd;
      return _exhaustive;
    }
  }
}

export function dispatch(state: BattleState, cmd: Command, now: number): BattleState {
  const ticked = tick(state, now);
  return applyCommand(ticked, cmd, now);
}
