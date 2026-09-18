// 隊友／敵人 AI，每個 tick 呼叫一次；直接改 Ctx（呼叫端保證是本次呼叫的工作副本）。
//
// P6（CONTRACT §3.2）改版：隊友 AI 不再借用 BattleState.skills（玩家自己的技能欄）、也不再靠
// 「固定挑第一位非玩家隊員當輔助 AI」的簡化規則（P1 時期的權宜寫法，見本檔案舊版註解）——
// 每位隊友改用自己的 PartyActor.skills（wire 已展開，只含非 passive 且 implemented=true 的技能，
// 見 engine/types.ts PartyActor.skills 型別註解），依五段優先序自行決定要不要用技能：
//   ①heal：自己有 heal 技能可用，且有隊友（含自己）HP<50% → 治療最低比例者（allAllies 版本全體）
//   ②buff/shield：有可用技能，且候選目標身上還沒有同一種效果 → 施放
//   ③damage：有可用技能且 MP 足夠 → 用「tier 最高」的那顆
//   ④debuff：有可用技能，且敵人身上還沒有同一種效果 → 施放
//   ⑤都不成立 → 普攻（沒有 skills 的隊友，例如小優／阿深，恆走這一段——跟 P1 舊行為相容）
// 技能結算直接呼叫 combat.ts 的 resolveCastEffect（tick.ts 對玩家施法完成時用的同一支函式）：
// AI 的動作不走 casting 狀態（跟玩家的施法時間不同，見下方 castNow 的註解），直接在本次 tick
// 結算，不需要另外幫 4 個 AI 角色各自追蹤 pendingCasts。
// 技能冷卻仍記在 ctx.aiSkillReadyAt[actorId]（跟玩家的 skillReadyAt 分開兩把鎖——審查修復 #2：
// 兩者是不同角色在用同一份技能「定義」，AI 用掉技能不該把玩家 UI 上顯示的冷卻也一起打斷）。
import type { Skill } from '../types';
import { applyPartyDamage, resolveCastEffect, resolveWeaponAttack } from './combat';
import type { Ctx } from './context';
import { pushEvent, pushLog } from './context';
import { activeStatSum, effectiveRating, rollCritMultiplier } from './effects';
import { computeDamage, critChance, floorInt, missChance, NEUTRAL_WEAPON_PROFILE, pickWeightedAliveTarget, randRange } from './formulas';
import type { ActiveEffect, EnemyActor, PartyActor, PendingCast } from './types';

/** 該效果目前是否已經套用在這個目標身上（依 stat 判斷，不看來源技能——契約原文「目標身上沒有
 *  同 stat 效果」，同一個 stat 不管是誰打上去的都算數，跟 combat.ts applyStatusEffect 的疊加鍵
 *  (stat, sourceSkillId) 是不同的判斷維度：那裡管「要不要疊加」，這裡管「AI 要不要重複施放」）。 */
function hasStat(target: { activeEffects: ActiveEffect[] }, stat: string): boolean {
  return target.activeEffects.some((e) => e.stat === stat);
}

/** 挑治療目標：一般情況選比例最低者；但若「比例最低者」正好是治療者自己、且還有別的隊友也在
 *  門檻以下，審查要求優先救別人（自己還撐得住），除非治療者自己已經 <15%（快死了，先救自己）。
 *  （P6 沿用 P1 既有規則，門檻本身從舊版的 <40% 改成契約 §3.2 的 <50%，見呼叫端 tryHeal。） */
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

/** 支援/傷害/削弱技能候選的挑選順序（player 優先、其次自己、其餘隊友按陣列順序）——契約
 *  §3.2「buff 優先給玩家或自己依 target」的具體做法：target='ally' 沒有指定是哪一位隊友時，
 *  由這個順序決定第一個「還沒有效果」的人選。 */
function allyCandidateOrder(ctx: Ctx, actor: PartyActor): PartyActor[] {
  const player = ctx.party.find((p) => p.id === ctx.playerId);
  const rest = ctx.party.filter((p) => p.id !== ctx.playerId && p.id !== actor.id);
  return [player, actor, ...rest].filter((p): p is PartyActor => !!p);
}

/** 技能是否目前可用：不在冷卻中、MP 足夠。跟玩家 USE_SKILL 共用同一套「冷卻/MP」規則（契約
 *  §3.2：「冷卻與 MP 與玩家同規則」），只是查表用 ctx.aiSkillReadyAt 而不是 ctx.skillReadyAt。 */
function isAiSkillReady(ctx: Ctx, actor: PartyActor, skill: Skill): boolean {
  const readyAt = ctx.aiSkillReadyAt[actor.id]?.[skill.id] ?? 0;
  return readyAt <= ctx.now && actor.mp >= skill.mpCost;
}

/**
 * 立即結算一顆 AI 技能：扣 MP、進冷卻（記在 aiSkillReadyAt，不動玩家的 skillReadyAt）、推
 * skillCast 事件、呼叫 combat.ts 的 resolveCastEffect 結算效果。
 * AI 的動作不走 casting 狀態（跟玩家的施法時間不同），直接結算，行為鎖只用 recovering 表示忙碌，
 * 這是為了不用另外幫 4 個 AI 角色各自追蹤 pendingCasts 而做的簡化（P1 時期就是這個設計，P6 沿用）。
 */
function castNow(ctx: Ctx, actor: PartyActor, skill: Skill, targetId: string | 'ALL' | 'ALL_ENEMIES'): void {
  actor.mp = floorInt(actor.mp - skill.mpCost); // P6（CONTRACT §1）：MP 整數不變式。
  ctx.aiSkillReadyAt[actor.id] = { ...ctx.aiSkillReadyAt[actor.id], [skill.id]: ctx.now + skill.cooldownMs };
  pushEvent(ctx, {
    kind: 'skillCast',
    actorId: actor.id,
    skillId: skill.id,
    targetId: targetId === 'ALL' || targetId === 'ALL_ENEMIES' ? null : targetId,
  });
  const pending: PendingCast = { skillId: skill.id, targetId };
  resolveCastEffect(ctx, actor, skill, pending);
}

/** ①heal：契約 §3.2「有 heal 技能可用且有隊友 HP<50%（含自己）→ 治療最低者（allAllies 版本則
 *  全體）」。同時有多顆 heal 技能時挑陣列裡第一顆可用的（heal 之間沒有強弱之分，不套用 tier 規則
 *  ——tier 規則只用在③damage，見契約原文）。target='self' 的 heal 只有在「治療者自己」正好也在
 *  低血名單內才有意義，否則跳過（自療救不到別人），讓後面的 tier 或其它 heal 技能有機會接手。 */
function tryHeal(ctx: Ctx, actor: PartyActor, skills: Skill[]): boolean {
  const healSkills = skills.filter((s) => s.kind === 'heal');
  if (healSkills.length === 0) return false;
  const lowHp = ctx.party.filter((p) => p.hp > 0 && p.hp / p.stats.hpMax < 0.5);
  if (lowHp.length === 0) return false;
  for (const skill of healSkills) {
    if (!isAiSkillReady(ctx, actor, skill)) continue;
    if (skill.target === 'allAllies') {
      castNow(ctx, actor, skill, 'ALL');
      return true;
    }
    if (skill.target === 'self') {
      if (!lowHp.some((p) => p.id === actor.id)) continue;
      castNow(ctx, actor, skill, actor.id);
      return true;
    }
    // target === 'ally'：任何隊友（含自己）都可能是目標，走既有的優先序規則。
    const target = pickHealTarget(actor, lowHp);
    castNow(ctx, actor, skill, target.id);
    return true;
  }
  return false;
}

/** ②buff/shield：契約 §3.2「有 shield／buff 技能可用且目標身上沒有同 stat 效果 → 施放」。
 *  shield 沒有 stat 詞彙（見 types.ts BuffDebuffStat 只給 buff/debuff 用），這裡把「目標身上有
 *  沒有同效果」換成「目標的 shield 是否 >0」——語意上是同一件事：避免疊加式重複施放同一種保護。 */
function tryBuffOrShield(ctx: Ctx, actor: PartyActor, skills: Skill[]): boolean {
  const candidates = skills.filter((s) => s.kind === 'buff' || s.kind === 'shield');
  for (const skill of candidates) {
    if (!isAiSkillReady(ctx, actor, skill)) continue;
    if (skill.kind === 'shield') {
      if (skill.target === 'self') {
        if (actor.shield > 0) continue;
        castNow(ctx, actor, skill, actor.id);
        return true;
      }
      const target = allyCandidateOrder(ctx, actor).find((p) => p.hp > 0 && p.shield <= 0);
      if (!target) continue;
      castNow(ctx, actor, skill, target.id);
      return true;
    }
    // buff
    const stat = skill.effect?.stat;
    if (!stat) continue; // 資料不完整（理論上不該發生），安全跳過，交給下一顆候選或下一段 tier。
    if (skill.target === 'allAllies') {
      const anyMissing = ctx.party.some((p) => p.hp > 0 && !hasStat(p, stat));
      if (!anyMissing) continue;
      castNow(ctx, actor, skill, 'ALL');
      return true;
    }
    if (skill.target === 'self') {
      if (hasStat(actor, stat)) continue;
      castNow(ctx, actor, skill, actor.id);
      return true;
    }
    // target === 'ally'：契約「buff 優先給玩家或自己依 target」——沒有指定是哪一位隊友時，
    // 從 allyCandidateOrder（玩家優先→自己→其餘）挑第一個還沒有這個 stat 的人。
    const target = allyCandidateOrder(ctx, actor).find((p) => p.hp > 0 && !hasStat(p, stat));
    if (!target) continue;
    castNow(ctx, actor, skill, target.id);
    return true;
  }
  return false;
}

/**
 * ③damage：契約 §3.2「有 damage 技能可用且 MP 足夠 → 用 tier 最高的」。tier 由 Skill.tier 決定
 * （數字越大越高階）；缺省時退回陣列位置當代理值——P5 WIRE 明講「已選職業技能依 path→tier 排序
 * 填入」技能欄，陣列本身順序已經是 tier 遞增序，索引越後面代表 tier 越高（見 types.ts Skill.tier
 * 型別註解）。target='allEnemies' 的 damage 技能不需要 ctx.targetId（結算時會自己篩存活敵人）。
 */
function pickHighestTierSkill(skills: Skill[]): Skill {
  let best = skills[0];
  let bestScore = best.tier ?? 0;
  for (let i = 1; i < skills.length; i++) {
    const s = skills[i];
    const score = s.tier ?? i;
    if (score > bestScore) {
      best = s;
      bestScore = score;
    }
  }
  return best;
}

function tryDamage(ctx: Ctx, actor: PartyActor, skills: Skill[]): boolean {
  const candidates = skills.filter((s) => s.kind === 'damage' && isAiSkillReady(ctx, actor, s));
  if (candidates.length === 0) return false;
  const best = pickHighestTierSkill(candidates);
  if (best.target === 'allEnemies') {
    castNow(ctx, actor, best, 'ALL_ENEMIES');
    return true;
  }
  if (!ctx.targetId) return false; // 沒有鎖定目標，單體傷害技能沒有地方打，讓後面的 tier 接手。
  castNow(ctx, actor, best, ctx.targetId);
  return true;
}

/** ④debuff：契約 §3.2「有 debuff 技能可用且敵人身上沒有 → 施放」。跟②buff同一套「目標沒有同
 *  stat 效果才施放」規則，只是換成查敵方 activeEffects。 */
function tryDebuff(ctx: Ctx, actor: PartyActor, skills: Skill[]): boolean {
  const candidates = skills.filter((s) => s.kind === 'debuff');
  for (const skill of candidates) {
    if (!isAiSkillReady(ctx, actor, skill)) continue;
    const stat = skill.effect?.stat;
    if (!stat) continue;
    if (skill.target === 'allEnemies') {
      const anyMissing = ctx.enemies.some((e) => e.hp > 0 && !hasStat(e, stat));
      if (!anyMissing) continue;
      castNow(ctx, actor, skill, 'ALL_ENEMIES');
      return true;
    }
    // target === 'enemy'：debuff 只打目前鎖定的目標（跟隊友普攻/damage 技能一致，不另外選目標）。
    const target = ctx.enemies.find((e) => e.id === ctx.targetId && e.hp > 0);
    if (!target || hasStat(target, stat)) continue;
    castNow(ctx, actor, skill, target.id);
    return true;
  }
  return false;
}

/** 行動結束的共同收尾：進 recovering、排下一次可行動時間（跟舊版 P1 邏輯一致）。 */
function finishAllyAction(ctx: Ctx, actor: PartyActor): void {
  actor.action = 'recovering';
  actor.actionUntil = ctx.now + ctx.cfg.recoveryMs;
  actor.attackReadyAt = ctx.now + randRange(ctx.rng, ctx.cfg.allyActIntervalMs);
}

/**
 * 隊友 AI（最多 4 位非玩家）：依契約 §3.2 五段優先序決定這一次行動要做什麼，都不成立就對目前
 * 目標普攻（不蓄氣，chargeMul=1）。沒有 skills（例如示範隊伍裡沒配技能的傭兵）的隊友，前四段
 * 恆為 false，直接落到普攻，跟 P1 時期的行為完全相容。
 */
export function advanceAllyAI(ctx: Ctx, actor: PartyActor): void {
  if (actor.isPlayer || actor.hp <= 0) return;
  if (actor.action !== 'idle' || actor.attackReadyAt > ctx.now) return;

  const skills = actor.skills;
  if (skills.length > 0) {
    if (tryHeal(ctx, actor, skills)) return finishAllyAction(ctx, actor);
    if (tryBuffOrShield(ctx, actor, skills)) return finishAllyAction(ctx, actor);
    if (tryDamage(ctx, actor, skills)) return finishAllyAction(ctx, actor);
    if (tryDebuff(ctx, actor, skills)) return finishAllyAction(ctx, actor);
  }

  // ⑤普攻 fallback。P7：隊友普攻改走 resolveWeaponAttack（跟玩家共用同一套 hits/extraHit/splash
  // 邏輯）；本輪傭兵尚未開放裝備，actor.weaponProfile 恆為 null → NEUTRAL_WEAPON_PROFILE，行為
  // 跟 P1～P6 完全相容。
  if (!ctx.targetId) return; // 沒目標可打，這次不消耗行動（下個 tick 再試）。
  resolveWeaponAttack(ctx, {
    actorId: actor.id,
    attackerStats: actor.stats,
    attackerEffects: actor.activeEffects,
    attackerRating: actor.rating,
    weaponVisual: actor.weapon,
    weaponProfile: actor.weaponProfile ?? NEUTRAL_WEAPON_PROFILE,
    targetEnemyId: ctx.targetId,
    chargeMul: 1,
    charged: false,
  });
  finishAllyAction(ctx, actor);
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
        // SPEC §5：敵人打隊友也套同一套 missChance 公式（攻方=怪物、守方=隊友），AGI 的迴避
        // 才真的有防禦意義；miss 時 damage=0、不扣盾（不呼叫 applyPartyDamage，盾牌完全不動）。
        // P5：怪物可能身上有 debuff（atk_pct/aspd/flee/hit）、隊友可能有 buff（def_pct/flee/hit/…），
        // 一律用 effectiveRating 疊加後的評級判定，不再直接讀 base rating。
        const attackerRating = effectiveRating(enemy.rating, enemy.activeEffects);
        const defenderRating = effectiveRating(target.rating, target.activeEffects);
        const missPct = missChance(attackerRating, defenderRating, ctx.cfg);
        const isHit = ctx.rng() >= missPct / 100;
        if (!isHit) {
          pushEvent(ctx, { kind: 'enemyAttack', enemyId: enemy.id, targetId: target.id, damage: 0, guarded, result: 'miss' });
          pushLog(ctx, `${enemy.name} 的攻擊被 ${target.name} 閃避`);
        } else {
          // 怪物暴擊率預設 0%（monsterCritPct），但仍走同一套 critChance 公式，方便後台調高。
          // critMul 改吃 P5 的浮動抽樣（見 effects.ts rollCritMultiplier），沿用既有 computeDamage
          // 的 chargeMul 參數位置疊乘——跟玩家普攻同一個做法（見 combat.ts 對 chargeMul／critMul
          // 疊加位置的註解）。
          const critPct = critChance(attackerRating, defenderRating, ctx.cfg);
          const isCrit = ctx.rng() < critPct / 100;
          // 審查#5：跟 combat.ts resolveAttackOrDamageSkill 同一份修法——攻擊者的 critDmgPct 疊在
          // 浮動暴擊倍率之上。怪物目前恆為 0（MonsterRating() 不設這個欄位），這裡加上只是讓兩條
          // 平行的暴擊結算路徑維持一致、不互相漂移，不影響現有數值。
          const effectiveMul = isCrit ? rollCritMultiplier(ctx.rng, ctx.cfg) * (1 + (attackerRating.critDmgPct ?? 0) / 100) : 1;
          // P5：套用怪物 atk_pct debuff／隊友 def_pct buff（見 effects.ts activeStatSum）。
          const effAtk = Math.round(enemy.stats.atk * (1 + activeStatSum(enemy.activeEffects, 'atk_pct') / 100));
          const effDef = Math.round(target.stats.def * (1 + activeStatSum(target.activeEffects, 'def_pct') / 100));
          const damage = computeDamage(effAtk, 1, 0, 1, effectiveMul, effDef, guarded, ctx.cfg);
          const result: 'normal' | 'critical' = isCrit ? 'critical' : 'normal';
          pushEvent(ctx, { kind: 'enemyAttack', enemyId: enemy.id, targetId: target.id, damage, guarded, result });
          pushLog(ctx, `${enemy.name} 攻擊 ${target.name}，造成 ${damage} 點傷害`);
          // P7（CONTRACT §1「怪物攻擊帶自己的屬性」）：怪物普攻視為攻擊屬性＝其 attribute（缺省視為
          // neutral），供 applyPartyDamage 判斷是否套用受擊方鍊系武器的 elementResistPct。
          applyPartyDamage(ctx, target, damage, enemy.attribute);
        }
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
