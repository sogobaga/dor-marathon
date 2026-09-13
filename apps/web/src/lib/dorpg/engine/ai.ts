// 隊友／敵人 AI，每個 tick 呼叫一次；直接改 Ctx（呼叫端保證是本次呼叫的工作副本）。
import { applyPartyDamage, resolveAttackOrDamageSkill, resolveSupportSkill } from './combat';
import type { Ctx } from './context';
import { pushEvent, pushLog } from './context';
import { computeDamage, pickWeightedAliveTarget, randRange } from './formulas';
import type { EnemyActor, PartyActor } from './types';

/** 挑治療目標：一般情況選比例最低者；但若「比例最低者」正好是治療者自己、且還有別的隊友也在
 *  門檻以下，審查要求優先救別人（自己還撐得住），除非治療者自己已經 <15%（快死了，先救自己）。 */
function pickHealTarget(actor: PartyActor, lowHp: PartyActor[]): PartyActor {
  const worst = lowHp.reduce((w, p) => (p.hp / p.stats.hpMax < w.hp / w.stats.hpMax ? p : w));
  if (worst.id !== actor.id) return worst;
  const selfRatio = actor.hp / actor.stats.hpMax;
  const others = lowHp.filter((p) => p.id !== actor.id);
  if (others.length > 0 && selfRatio >= 0.15) {
    return others.reduce((w, p) => (p.hp / p.stats.hpMax < w.hp / w.stats.hpMax ? p : w));
  }
  return actor; // 沒有別人可救，或自己已經 <15%——先救自己。
}

/**
 * 隊友 AI（4 位非玩家）：每 rand(allyActIntervalMs) 對目前目標普攻（不蓄氣，chargeMul=1）；
 * 若指定的 healerId 那位血量有隊友 <40%、技能列有 heal 技能、MP 夠、且該技能不在冷卻中，
 * 就改用治療——資料模型沒有「每個隊友自己的技能欄位」，這是契約允許的「簡單規則即可，documented」
 * 簡化：固定挑第一位非玩家隊員（樣本資料裡的小咪）當輔助 AI。
 * 技能冷卻記在 ctx.aiSkillReadyAt[actorId]（跟玩家的 skillReadyAt 分開兩把鎖——審查修復 #2：
 * 兩者是不同角色在用同一份技能「定義」，AI 用掉治療不該把玩家 UI 上顯示的冷卻也一起打斷）。
 * AI 的動作不走 casting 狀態（跟玩家的施法時間不同），直接結算，行為鎖只用 recovering 表示忙碌，
 * 這是為了不用另外幫 4 個 AI 角色各自追蹤 pendingCasts 而做的簡化。
 */
export function advanceAllyAI(ctx: Ctx, actor: PartyActor, healerId: string): void {
  if (actor.isPlayer || actor.hp <= 0) return;
  if (actor.action !== 'idle' || actor.attackReadyAt > ctx.now) return;

  const lowHp = ctx.party.filter((p) => p.hp > 0 && p.hp / p.stats.hpMax < 0.4);
  const healSkill = ctx.skills.find((s) => s?.kind === 'heal') ?? null;
  const healReady = healSkill ? (ctx.aiSkillReadyAt[actor.id]?.[healSkill.id] ?? 0) <= ctx.now : false;

  if (actor.id === healerId && healSkill && healReady && lowHp.length > 0 && actor.mp >= healSkill.mpCost) {
    const target = pickHealTarget(actor, lowHp);
    actor.mp -= healSkill.mpCost;
    ctx.aiSkillReadyAt[actor.id] = { ...ctx.aiSkillReadyAt[actor.id], [healSkill.id]: ctx.now + healSkill.cooldownMs };
    pushEvent(ctx, { kind: 'skillCast', actorId: actor.id, skillId: healSkill.id, targetId: target.id });
    resolveSupportSkill(ctx, actor.id, healSkill, target.id);
  } else if (ctx.targetId) {
    resolveAttackOrDamageSkill(ctx, {
      actorId: actor.id,
      atk: actor.stats.atk,
      coefficient: 1,
      flat: 0,
      weapon: actor.weapon,
      targetEnemyId: ctx.targetId,
      chargeMul: 1,
      charged: false,
    });
  } else {
    return; // 沒目標可打，這次不消耗行動（下個 tick 再試）。
  }
  actor.action = 'recovering';
  actor.actionUntil = ctx.now + ctx.cfg.recoveryMs;
  actor.attackReadyAt = ctx.now + randRange(ctx.rng, ctx.cfg.allyActIntervalMs);
}

/** 匯出給 tick.ts 的 'resolving' 階段共用：victory 結案時仍要繼續推進 dying→removed 的動畫，
 *  但不該再跑完整的 advanceEnemyAI（結果已定，不需要新的 windup/attacking）。 */
export function advanceEnemyDeath(enemy: EnemyActor, now: number): void {
  if (enemy.anim === 'dying' && now >= enemy.animUntil) {
    enemy.anim = 'removed';
  }
}

/**
 * 敵人 AI：idle（nextActAt 到）→windup→attacking（在轉場當下結算傷害）→idle 循環。
 * P1 沒有召喚機制，createBattle 直接給 idle，故不處理 spawning。
 * 單次呼叫最多推進一個狀態轉換（跟一般 rAF 迴圈小步距呼叫 tick 的用法一致；不支援單次 now
 * 跳過一大段時間就一次跨好幾個狀態——這點也記在 verify 腳本與回報裡）。
 */
export function advanceEnemyAI(ctx: Ctx, enemy: EnemyActor): void {
  if (enemy.hp <= 0) {
    advanceEnemyDeath(enemy, ctx.now);
    return;
  }

  if (enemy.anim === 'hitReaction') {
    if (ctx.now < enemy.animUntil) return;
    enemy.anim = enemy.resumeAnim ?? 'idle';
    enemy.animUntil = enemy.resumeAnimUntil ?? ctx.now;
    enemy.resumeAnim = undefined;
    enemy.resumeAnimUntil = undefined;
  }

  if (enemy.anim === 'idle') {
    if (ctx.now >= enemy.nextActAt) {
      const targetId = pickWeightedAliveTarget(ctx.party, ctx.rng);
      if (!targetId) return;
      ctx.enemyTargets[enemy.id] = targetId;
      enemy.anim = 'windup';
      enemy.animUntil = ctx.now + ctx.cfg.enemyWindupMs;
      pushEvent(ctx, { kind: 'enemyWindup', enemyId: enemy.id, targetId });
    }
    return;
  }

  if (enemy.anim === 'windup') {
    if (ctx.now >= enemy.animUntil) {
      const chosenId = ctx.enemyTargets[enemy.id];
      let target = ctx.party.find((p) => p.id === chosenId && p.hp > 0);
      if (!target) {
        const fallbackId = pickWeightedAliveTarget(ctx.party, ctx.rng);
        target = ctx.party.find((p) => p.id === fallbackId);
      }
      enemy.anim = 'attacking';
      enemy.animUntil = ctx.now + ctx.cfg.enemyAttackMs;
      if (target) {
        const guarded = target.action === 'guarding';
        const damage = computeDamage(enemy.stats.atk, 1, 0, 1, 1, target.stats.def, guarded, ctx.cfg);
        pushEvent(ctx, { kind: 'enemyAttack', enemyId: enemy.id, targetId: target.id, damage, guarded });
        pushLog(ctx, `${enemy.name} 攻擊 ${target.name}，造成 ${damage} 點傷害`);
        applyPartyDamage(ctx, target, damage);
      }
    }
    return;
  }

  if (enemy.anim === 'attacking') {
    if (ctx.now >= enemy.animUntil) {
      enemy.anim = 'idle';
      enemy.nextActAt = ctx.now + randRange(ctx.rng, ctx.cfg.enemyActIntervalMs);
      delete ctx.enemyTargets[enemy.id];
    }
  }
}
