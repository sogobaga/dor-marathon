// 傷害/治療/護盾的效果套用（純函式風格：吃 Ctx 直接在裡面改，呼叫端保證是本次呼叫的工作副本）。
// dispatch（玩家普攻/技能）、ai（隊友普攻/治療）、tick（敵人攻擊、施法完成結算）三邊共用，
// 避免同一段「算傷害→套用→處理死亡/受擊」邏輯抄三次、規則跑掉。
import type { Skill, WeaponKind } from '../types';
import type { Ctx } from './context';
import { pushEvent, pushLog } from './context';
import { computeDamage, computeHeal, selectAliveByThreat } from './formulas';
import type { EnemyActor, PartyActor, PendingCast } from './types';

/** 對敵人造成傷害後的死亡/受擊處理：死亡→dying+enemyDeath+目標自動換人；存活→hitReaction 覆蓋層。 */
function applyEnemyDamage(ctx: Ctx, enemy: EnemyActor, damage: number): void {
  enemy.hp = Math.max(0, enemy.hp - damage);
  if (enemy.hp <= 0) {
    enemy.anim = 'dying';
    enemy.animUntil = ctx.now + ctx.cfg.enemyDeathMs;
    enemy.resumeAnim = undefined;
    enemy.resumeAnimUntil = undefined;
    delete ctx.enemyTargets[enemy.id];
    pushEvent(ctx, { kind: 'enemyDeath', enemyId: enemy.id });
    pushLog(ctx, `${enemy.name} 被擊倒`);
    if (ctx.targetId === enemy.id) {
      const next = selectAliveByThreat(ctx.enemies);
      if (next !== ctx.targetId) {
        ctx.targetId = next;
        pushEvent(ctx, { kind: 'targetChanged', enemyId: next });
      }
    }
    return;
  }
  // 規格：「hitReaction 不中斷自己的 windup 計時」——把真正的計時點存進 resumeAnim/resumeAnimUntil，
  // 顯示層先蓋成 hitReaction；若已經在 hitReaction 中被連續打第二下，維持原本存的計時點不覆蓋。
  if (enemy.anim !== 'hitReaction') {
    enemy.resumeAnim = enemy.anim;
    enemy.resumeAnimUntil = enemy.animUntil;
  }
  enemy.anim = 'hitReaction';
  enemy.animUntil = ctx.now + ctx.cfg.enemyHitMs;
}

/**
 * 玩家/隊友對敵人造成傷害（含普攻與技能傷害，事件 kind 都是 'attack'，見契約 §2 event 定義的註解）。
 * hitRate/critRate 目前預設 100%/0%（規格：「待平衡後由 profile 開啟」），這裡仍照規則走 rng 兩次，
 * 好讓未來調高 critRate 或注入自訂 rng 的測試都能吃到一致的行為。
 */
export function resolveAttackOrDamageSkill(
  ctx: Ctx,
  opts: {
    actorId: string;
    atk: number;
    coefficient: number;
    flat: number;
    weapon: WeaponKind;
    targetEnemyId: string;
    chargeMul: number;
    charged: boolean;
  },
): void {
  const enemy = ctx.enemies.find((e) => e.id === opts.targetEnemyId);
  if (!enemy || enemy.hp <= 0) {
    pushLog(ctx, `${opts.actorId} 的攻擊目標已消失，落空`);
    return;
  }
  const isHit = ctx.rng() < ctx.cfg.hitRate;
  const isCrit = isHit && ctx.rng() < ctx.cfg.critRate;
  // crit 目前預設不會發生（critRate=0）；一旦開啟，設計上讓爆擊倍率跟蓄氣倍率一樣疊在 chargeMul 這個
  // 乘數位置——兩者概念上都是「命中後、扣防禦前」的加成，公式本身（computeDamage 簽章）沒有另外開 crit 參數。
  const effectiveChargeMul = opts.chargeMul * (isCrit ? ctx.cfg.critMultiplier : 1);
  const damage = isHit
    ? computeDamage(opts.atk, opts.coefficient, opts.flat, 1, effectiveChargeMul, enemy.stats.def, false, ctx.cfg)
    : 0;
  const result: 'normal' | 'critical' | 'miss' = !isHit ? 'miss' : isCrit ? 'critical' : 'normal';
  pushEvent(ctx, {
    kind: 'attack',
    actorId: opts.actorId,
    targetId: enemy.id,
    weapon: opts.weapon,
    result,
    damage,
    charged: opts.charged,
  });
  pushLog(ctx, `${opts.actorId} 對 ${enemy.name} ${result === 'miss' ? '揮空' : `造成 ${damage} 點傷害`}`);
  if (damage > 0) applyEnemyDamage(ctx, enemy, damage);
}

/** 敵人打隊友：先扣盾再扣 HP（規格 §2）；打死當場記 actorDown，且若玩家正在逃跑判定中則立即取消判定記失敗。 */
export function applyPartyDamage(ctx: Ctx, actor: PartyActor, rawDamage: number): void {
  let dmg = rawDamage;
  if (actor.shield > 0) {
    const absorbed = Math.min(actor.shield, dmg);
    actor.shield -= absorbed;
    dmg -= absorbed;
  }
  actor.hp = Math.max(0, actor.hp - dmg);
  if (actor.hp <= 0 && actor.action !== 'dead') {
    actor.action = 'dead';
    pushEvent(ctx, { kind: 'actorDown', actorId: actor.id });
    pushLog(ctx, `${actor.name} 倒下`);
    if (actor.id === ctx.playerId && ctx.escape.flow === 'judging') {
      // 規格：逃跑判定中若（玩家）倒下，取消判定但仍記為已嘗試——本場逃跑鈕永久停用。
      ctx.escape.flow = 'failed';
      ctx.escape.judgingUntil = null;
      ctx.escape.message = '逃跑失敗';
      pushEvent(ctx, { kind: 'escapeFailed' });
    }
  }
}

function applyHealToTarget(ctx: Ctx, casterId: string, targetId: string, amount: number): void {
  const target = ctx.party.find((p) => p.id === targetId);
  if (!target || target.hp <= 0) return;
  target.hp = Math.min(target.stats.hpMax, target.hp + amount);
  pushEvent(ctx, { kind: 'heal', actorId: casterId, targetId, amount });
  pushLog(ctx, `${casterId} 治療 ${target.name} ${amount} 點`);
}

function applyShieldToTarget(ctx: Ctx, casterId: string, targetId: string, amount: number): void {
  const target = ctx.party.find((p) => p.id === targetId);
  if (!target || target.hp <= 0) return;
  target.shield += amount;
  pushEvent(ctx, { kind: 'shield', actorId: casterId, targetId, amount });
  pushLog(ctx, `${casterId} 為 ${target.name} 加上 ${amount} 點護盾`);
}

/**
 * heal/shield 技能結算：矩形都用 computeHeal(MATK,...) 算量——規格只明講治療公式用 MATK，
 * shield 沒有另外給公式，這裡把它當同一種「輔助類、依 MATK 算量」的效果套用同一條公式（文件內決策，
 * 已在回報中列為偏離契約之處）。targetId==='ALL' 時對所有存活隊員各推一筆事件。
 */
export function resolveSupportSkill(ctx: Ctx, casterId: string, skill: Skill, targetId: string | 'ALL' | null): void {
  const caster = ctx.party.find((p) => p.id === casterId);
  if (!caster) return;
  const amount = computeHeal(caster.stats.matk, skill.coefficient, skill.flat);
  const targets =
    targetId === 'ALL' ? ctx.party.filter((p) => p.hp > 0).map((p) => p.id) : targetId ? [targetId] : [];
  for (const tId of targets) {
    if (skill.kind === 'heal') applyHealToTarget(ctx, casterId, tId, amount);
    else if (skill.kind === 'shield') applyShieldToTarget(ctx, casterId, tId, amount);
  }
}

/** casting 完成時的效果結算（tick.ts 呼叫）：damage 打 pending.targetId 的敵人，heal/shield 走 resolveSupportSkill。 */
export function resolveCastEffect(ctx: Ctx, actor: PartyActor, skill: Skill, pending: PendingCast): void {
  if (skill.kind === 'damage') {
    if (typeof pending.targetId === 'string' && pending.targetId !== 'ALL') {
      resolveAttackOrDamageSkill(ctx, {
        actorId: actor.id,
        atk: actor.stats.atk,
        coefficient: skill.coefficient,
        flat: skill.flat,
        weapon: skill.weapon,
        targetEnemyId: pending.targetId,
        chargeMul: 1,
        charged: false,
      });
    }
    return;
  }
  resolveSupportSkill(ctx, actor.id, skill, pending.targetId);
}
