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
//
// P9（DORPG_P9 CONTRACT §4／WIRE「引擎」）改版：「決策與執行分離」——原本 tryHeal/tryBuffOrShield/
// tryDamage/tryDebuff 四段直接呼叫 castNow 執行；本輪拆成 decideHeal/decideBuffOrShield/
// decideDamage/decideDebuff 四個「純函式，只回傳 Decision、不動 ctx」，由 decideAction(ctx, actor,
// strategy) 依策略組裝成完整的優先序，advanceAllyAI 再依 Decision.kind 呼叫既有的
// castNow／resolveWeaponAttack 執行。balanced 策略的組裝（decideBalanced）逐項對齊舊版
// tryHeal→tryBuffOrShield→tryDamage→tryDebuff→普攻 的執行順序與判斷式，唯二的參數化（heal_pct/
// self_heal_floor_pct）預設值等於舊版寫死的 50%/15%——既有 375 條斷言零改動。
// decideAction 同時服務隊友（advanceAllyAI）與玩家自動戰鬥（autopilot.ts advanceAutoBattle）：
// 兩者的差異（玩家的技能欄在 ctx.skills、隊友的在 actor.skills；玩家的冷卻在 ctx.skillReadyAt、
// 隊友的在 ctx.aiSkillReadyAt）由 actorSkills()／skillReadyAtFor() 兩個小型輔助函式吸收，
// decide* 系列函式本身不需要知道呼叫者是誰。
import type { Skill } from '../types';
import { applyPartyDamage, resolveCastEffect, resolveWeaponAttack } from './combat';
import type { Ctx } from './context';
import { pushEvent, pushLog } from './context';
import { activeStatSum, effectiveRating, rollCritMultiplier } from './effects';
import {
  computeDamage,
  critChance,
  effectiveMpCost,
  elementMultiplier,
  floorInt,
  inGuardianState,
  missChance,
  NEUTRAL_WEAPON_PROFILE,
  pickEnemyTarget,
  randRange,
} from './formulas';
import { numParam, resolveStrategy } from './strategies';
import type { ResolvedStrategy, StrategyParams } from './strategies';
import type { ActiveEffect, Decision, EnemyActor, PartyActor, PendingCast } from './types';

/** 該效果目前是否已經套用在這個目標身上（依 stat 判斷，不看來源技能——契約原文「目標身上沒有
 *  同 stat 效果」，同一個 stat 不管是誰打上去的都算數，跟 combat.ts applyStatusEffect 的疊加鍵
 *  (stat, sourceSkillId) 是不同的判斷維度：那裡管「要不要疊加」，這裡管「AI 要不要重複施放」）。 */
function hasStat(target: { activeEffects: ActiveEffect[] }, stat: string): boolean {
  return target.activeEffects.some((e) => e.stat === stat);
}

/** 挑治療目標：一般情況選比例最低者；但若「比例最低者」正好是治療者自己、且還有別的隊友也在
 *  門檻以下，審查要求優先救別人（自己還撐得住），除非治療者自己已經 <selfHealFloorRatio（快死
 *  了，先救自己）。P9：門檻從舊版寫死的 0.15 改成參數（見 decideHeal 呼叫端），balanced 預設值
 *  不變。 */
function pickHealTarget(actor: PartyActor, lowHp: PartyActor[], selfHealFloorRatio: number): PartyActor {
  const worst = lowHp.reduce((w, p) => (p.hp / p.stats.hpMax < w.hp / w.stats.hpMax ? p : w));
  if (worst.id !== actor.id) return worst;
  const selfRatio = actor.hp / actor.stats.hpMax;
  const others = lowHp.filter((p) => p.id !== actor.id);
  if (others.length > 0 && selfRatio >= selfHealFloorRatio) {
    return others.reduce((w, p) => (p.hp / p.stats.hpMax < w.hp / w.stats.hpMax ? p : w));
  }
  return actor; // 沒有別人可救，或自己已經低於門檻——先救自己。
}

/** 支援/傷害/削弱技能候選的挑選順序（player 優先、其次自己、其餘隊友按陣列順序）——契約
 *  §3.2「buff 優先給玩家或自己依 target」的具體做法：target='ally' 沒有指定是哪一位隊友時，
 *  由這個順序決定第一個「還沒有效果」的人選。 */
function allyCandidateOrder(ctx: Ctx, actor: PartyActor): PartyActor[] {
  const player = ctx.party.find((p) => p.id === ctx.playerId);
  const rest = ctx.party.filter((p) => p.id !== ctx.playerId && p.id !== actor.id);
  return [player, actor, ...rest].filter((p): p is PartyActor => !!p);
}

/**
 * P9：decideAction 的技能來源要看是誰在決策——隊友（isPlayer=false）用自己的 PartyActor.skills
 * （P6 既有規則不變）；玩家（isPlayer=true，只有 autopilot.ts 的自動戰鬥會把玩家傳進 decideAction）
 * 的技能欄其實是 BattleState.skills（10 格固定裝備欄，含 null 空格），跟隊友完全是兩個資料來源
 * ——PartyActor.skills 對玩家恆為 []（見該欄位型別註解），若 decide* 系列函式直接讀
 * `actor.skills` 而不經過這支函式，玩家自動戰鬥會永遠看到「沒有任何技能」，只會普攻。
 */
function actorSkills(ctx: Ctx, actor: PartyActor): Skill[] {
  if (actor.isPlayer) return ctx.skills.filter((s): s is Skill => s !== null);
  return actor.skills;
}

/**
 * P9：技能冷卻表跟技能來源一樣，玩家跟隊友分屬不同的兩張表——玩家的冷卻記在 ctx.skillReadyAt
 * （flat map，只有一個玩家不需要按 actorId 分），隊友的記在 ctx.aiSkillReadyAt[actorId]（P6
 * 審查修復 #2，見檔頭註解）。decide* 系列函式只做「唯讀查詢」（isAiSkillReady），真正的冷卻寫入
 * 仍分別由 castNow（隊友）與 dispatch.ts 的 commitCast（玩家）各自負責，這支函式不寫入任何東西。
 */
function skillReadyAtFor(ctx: Ctx, actor: PartyActor, skillId: string): number {
  if (actor.isPlayer) return ctx.skillReadyAt[skillId] ?? 0;
  return ctx.aiSkillReadyAt[actor.id]?.[skillId] ?? 0;
}

/** 技能是否目前可用：不在冷卻中、MP 足夠（含裝備 mpCostReducePct 折扣，見 formulas.ts
 *  effectiveMpCost——P9 起傭兵也可能有真實裝備，MP 減免必須跟玩家同一條路徑套用，見 CONTRACT
 *  §3「傭兵在戰鬥中的...equipmentEffects（回復、減傷、MP 減免）全部與玩家相同路徑生效」；
 *  equipmentEffects 缺省的隊友一律 NEUTRAL_EQUIPMENT_EFFECTS，mpCostReducePct=0 時算出來的
 *  effectiveMpCost 恆等於原始 mpCost，既有測試不受影響）。 */
function isAiSkillReady(ctx: Ctx, actor: PartyActor, skill: Skill): boolean {
  const readyAt = skillReadyAtFor(ctx, actor, skill.id);
  return readyAt <= ctx.now && actor.mp >= effectiveMpCost(skill.mpCost, actor.equipmentEffects.mpCostReducePct);
}

/**
 * 立即結算一顆 AI 技能（只給隊友用；玩家自動戰鬥的技能執行走 autopilot.ts→USE_SKILL 指令→
 * dispatch.ts commitCast，不經過這裡）：扣 MP（同上套用 mpCostReducePct）、進冷卻（記在
 * aiSkillReadyAt，不動玩家的 skillReadyAt）、推 skillCast 事件、呼叫 combat.ts 的
 * resolveCastEffect 結算效果。
 * AI 的動作不走 casting 狀態（跟玩家的施法時間不同），直接結算，行為鎖只用 recovering 表示忙碌，
 * 這是為了不用另外幫 4 個 AI 角色各自追蹤 pendingCasts 而做的簡化（P1 時期就是這個設計，P6 沿用）。
 */
function castNow(ctx: Ctx, actor: PartyActor, skill: Skill, targetId: string | 'ALL' | 'ALL_ENEMIES'): void {
  const mpCost = effectiveMpCost(skill.mpCost, actor.equipmentEffects.mpCostReducePct);
  actor.mp = floorInt(actor.mp - mpCost); // P6（CONTRACT §1）：MP 整數不變式。
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

// ──────────────────────────────────────────────────────────────────────────
// P9：decide* 系列——純函式，只回傳 Decision，不呼叫 castNow／不動 ctx。跟舊版 try* 系列一一對應，
// 邏輯逐行相同（只是把「執行」換成「回傳描述」），balanced 用預設參數組裝出來的結果因此跟舊版
// try* 系列的執行結果必須一致。
// ──────────────────────────────────────────────────────────────────────────

/** 對應舊版 tryHeal。healPct/selfHealFloorPct 為百分比（0–100），balanced 預設 50/15 等於舊版
 *  寫死的 0.5/0.15。 */
function decideHeal(ctx: Ctx, actor: PartyActor, skills: Skill[], healPct: number, selfHealFloorPct: number): Decision | null {
  const healSkills = skills.filter((s) => s.kind === 'heal');
  if (healSkills.length === 0) return null;
  const threshold = healPct / 100;
  const lowHp = ctx.party.filter((p) => p.hp > 0 && p.hp / p.stats.hpMax < threshold);
  if (lowHp.length === 0) return null;
  for (const skill of healSkills) {
    if (!isAiSkillReady(ctx, actor, skill)) continue;
    if (skill.target === 'allAllies') return { kind: 'heal', skillId: skill.id, targetId: 'ALL' };
    if (skill.target === 'self') {
      if (!lowHp.some((p) => p.id === actor.id)) continue;
      return { kind: 'heal', skillId: skill.id, targetId: actor.id };
    }
    // target === 'ally'：任何隊友（含自己）都可能是目標，走既有的優先序規則。
    const target = pickHealTarget(actor, lowHp, selfHealFloorPct / 100);
    return { kind: 'heal', skillId: skill.id, targetId: target.id };
  }
  return null;
}

/** 對應舊版 tryBuffOrShield（balanced／mp_conserve／skill_aggressive／element_advantage 共用；
 *  protect_allies 的 shield 目標選取規則不同，見 decideProtectShieldOrBuff）。 */
function decideBuffOrShield(ctx: Ctx, actor: PartyActor, skills: Skill[]): Decision | null {
  const candidates = skills.filter((s) => s.kind === 'buff' || s.kind === 'shield');
  for (const skill of candidates) {
    if (!isAiSkillReady(ctx, actor, skill)) continue;
    if (skill.kind === 'shield') {
      if (skill.target === 'self') {
        if (actor.shield > 0) continue;
        return { kind: 'buff', skillId: skill.id, targetId: actor.id };
      }
      const target = allyCandidateOrder(ctx, actor).find((p) => p.hp > 0 && p.shield <= 0);
      if (!target) continue;
      return { kind: 'buff', skillId: skill.id, targetId: target.id };
    }
    // buff
    const stat = skill.effect?.stat;
    if (!stat) continue;
    if (skill.target === 'allAllies') {
      const anyMissing = ctx.party.some((p) => p.hp > 0 && !hasStat(p, stat));
      if (!anyMissing) continue;
      return { kind: 'buff', skillId: skill.id, targetId: 'ALL' };
    }
    if (skill.target === 'self') {
      if (hasStat(actor, stat)) continue;
      return { kind: 'buff', skillId: skill.id, targetId: actor.id };
    }
    const target = allyCandidateOrder(ctx, actor).find((p) => p.hp > 0 && !hasStat(p, stat));
    if (!target) continue;
    return { kind: 'buff', skillId: skill.id, targetId: target.id };
  }
  return null;
}

/** 挑「tier 最高」的傷害技能，見型別註解（缺省時退回陣列位置代理值）。 */
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

/** 挑「coefficient 最高」的傷害技能——skill_aggressive 專用選法（CONTRACT §4：「選預期傷害
 *  （coef）最高者」），跟 balanced 的 tier 優先不同。 */
function pickHighestCoefSkill(skills: Skill[]): Skill {
  let best = skills[0];
  for (let i = 1; i < skills.length; i++) {
    if (skills[i].coefficient > best.coefficient) best = skills[i];
  }
  return best;
}

/** 對應舊版 tryDamage，targetId 參數化（balanced/mp_conserve 用 ctx.targetId；focus_fire 用
 *  ctx.focusTargetId；protect_allies/element_advantage 用各自算出的目標）。 */
function decideDamage(ctx: Ctx, actor: PartyActor, skills: Skill[], targetId: string | null): Decision | null {
  const candidates = skills.filter((s) => s.kind === 'damage' && isAiSkillReady(ctx, actor, s));
  if (candidates.length === 0) return null;
  const best = pickHighestTierSkill(candidates);
  if (best.target === 'allEnemies') return { kind: 'damage', skillId: best.id, targetId: 'ALL_ENEMIES' };
  if (!targetId) return null; // 沒有鎖定目標，單體傷害技能沒有地方打，讓後面接手。
  return { kind: 'damage', skillId: best.id, targetId };
}

/** skill_aggressive 專用：effectiveMpCost 折算後、施放完仍剩多少 MP%（給 min_mp_reserve_pct
 *  門檻比較用）。 */
function mpPctAfterCast(actor: PartyActor, mpCost: number): number {
  if (actor.stats.mpMax <= 0) return 100;
  return ((actor.mp - mpCost) / actor.stats.mpMax) * 100;
}

/** CONTRACT §4 skill_aggressive：「選預期傷害（coef）最高者」，並保留 min_mp_reserve_pct 的
 *  MP 底線（施放後不會低於這個比例；預設 0＝可以花到只剩 0）。沒有 debuff 階段（契約原文只提
 *  damage/普攻，跳過 debuff）。 */
function decideDamageHighestCoef(ctx: Ctx, actor: PartyActor, skills: Skill[], targetId: string | null, minMpReservePct: number): Decision | null {
  const candidates = skills.filter((s) => {
    if (s.kind !== 'damage' || !isAiSkillReady(ctx, actor, s)) return false;
    const cost = effectiveMpCost(s.mpCost, actor.equipmentEffects.mpCostReducePct);
    return mpPctAfterCast(actor, cost) >= minMpReservePct;
  });
  if (candidates.length === 0) return null;
  const best = pickHighestCoefSkill(candidates);
  if (best.target === 'allEnemies') return { kind: 'damage', skillId: best.id, targetId: 'ALL_ENEMIES' };
  if (!targetId) return null;
  return { kind: 'damage', skillId: best.id, targetId };
}

/** 對應舊版 tryDebuff，targetId 參數化（同 decideDamage）。 */
function decideDebuff(ctx: Ctx, actor: PartyActor, skills: Skill[], targetId: string | null): Decision | null {
  const candidates = skills.filter((s) => s.kind === 'debuff');
  for (const skill of candidates) {
    if (!isAiSkillReady(ctx, actor, skill)) continue;
    const stat = skill.effect?.stat;
    if (!stat) continue;
    if (skill.target === 'allEnemies') {
      const anyMissing = ctx.enemies.some((e) => e.hp > 0 && !hasStat(e, stat));
      if (!anyMissing) continue;
      return { kind: 'debuff', skillId: skill.id, targetId: 'ALL_ENEMIES' };
    }
    const target = ctx.enemies.find((e) => e.id === targetId && e.hp > 0);
    if (!target || hasStat(target, stat)) continue;
    return { kind: 'debuff', skillId: skill.id, targetId: target.id };
  }
  return null;
}

/** 對應舊版「都不成立 → 普攻」；沒有目標時回傳 'wait'（不消耗這次行動，跟舊版 `if (!ctx.targetId)
 *  return` 語意相同）。 */
function decideAttackFallback(targetId: string | null): Decision {
  if (!targetId) return { kind: 'wait' };
  return { kind: 'attack', targetId };
}

// ──────────────────────────────────────────────────────────────────────────
// P10（DORPG_P10 CONTRACT §3、WIRE「引擎」）：重騎士「守護」路線的守護判斷——插在每個策略「heal
// 之後、buff 之前」（CONTRACT 原文「在 buff 階段前新增守護判斷」）。沒有 kind='taunt' 技能的角色
// （所有既有測試角色）tauntCandidates 恆為 []，decideGuardStance/decideProtectGuardStance 恆回傳
// null，對既有決策序列零改動——這是「沒有 taunt 技能的角色決策序列零改動」這條要求的實作方式：
// 不是額外判斷「這個角色有沒有 taunt 技能」再決定要不要插這一步，而是讓這一步本身在沒有候選時
// 自然是 no-op。
// ──────────────────────────────────────────────────────────────────────────

/** 目前可用（冷卻/MP 皆足夠）的 taunt 技能候選。 */
function tauntCandidates(ctx: Ctx, actor: PartyActor, skills: Skill[]): Skill[] {
  return skills.filter((s) => s.kind === 'taunt' && isAiSkillReady(ctx, actor, s));
}

/** CONTRACT §3「優先 retarget=false 的守護姿態，其次挑釁」——retarget 展開值缺欄位（理論上不會
 *  發生，防呆）視為 false（守護姿態優先序更高，不是更低）。 */
function pickPreferredTaunt(cands: Skill[]): Skill | undefined {
  return cands.find((s) => !s.taunt?.retarget) ?? cands[0];
}

/** balanced／mp_conserve／skill_aggressive／focus_fire／element_advantage 共用：自己不在守護狀態
 *  且有可用 taunt 技能 → 施放（優先守護姿態，其次挑釁）。 */
function decideGuardStance(ctx: Ctx, actor: PartyActor, skills: Skill[]): Decision | null {
  if (inGuardianState(actor, ctx.now)) return null;
  const cands = tauntCandidates(ctx, actor, skills);
  if (cands.length === 0) return null;
  const skill = pickPreferredTaunt(cands);
  if (!skill) return null;
  return { kind: 'taunt', skillId: skill.id, targetId: actor.id };
}

/** CONTRACT §4 protect_allies 目標規則（protectAlliesTargetId）同一份精神：掃描 ctx.enemyTargets
 *  找「正在 windup 蓄力、且鎖定某個存活隊友」的敵人是否存在——不看鎖定的是不是「HP% 最低者」，
 *  這裡只在乎「有沒有隊友正被蓄力中的敵人瞄準」這件事本身（CONTRACT §3 原文「任一隊友正被 ≥1
 *  個 windup 中的敵人鎖定」）。 */
function anyAllyTargetedByWindupEnemy(ctx: Ctx): boolean {
  return ctx.enemies.some((e) => e.hp > 0 && e.anim === 'windup' && ctx.party.some((p) => p.hp > 0 && p.id === ctx.enemyTargets[e.id]));
}

/** protect_allies 專用守護判斷：一般情況跟其它策略一樣（守護姿態優先、其次挑釁），但「任一隊友
 *  正被 windup 中的敵人鎖定且挑釁可用」時，不管手上有沒有守護姿態，一律立即改放挑釁（retarget）
 *  把那隻敵人拉過來——CONTRACT §3「protect_allies 額外：...→ 立即挑釁」，這是比「優先守護姿態」
 *  更高的優先序，只在這個策略才有。 */
function decideProtectGuardStance(ctx: Ctx, actor: PartyActor, skills: Skill[]): Decision | null {
  if (inGuardianState(actor, ctx.now)) return null;
  const cands = tauntCandidates(ctx, actor, skills);
  if (cands.length === 0) return null;
  if (anyAllyTargetedByWindupEnemy(ctx)) {
    const retargetSkill = cands.find((s) => s.taunt?.retarget);
    if (retargetSkill) return { kind: 'taunt', skillId: retargetSkill.id, targetId: actor.id };
  }
  const skill = pickPreferredTaunt(cands);
  return skill ? { kind: 'taunt', skillId: skill.id, targetId: actor.id } : null;
}

/** CONTRACT §4 balanced：現行五段優先序，目標一律用 ctx.targetId（玩家鎖定的目標）；P10 在
 *  heal 之後、buff 之前插入守護判斷——既有（無 taunt 技能）角色的 375 條斷言必須維持通過。 */
function decideBalanced(ctx: Ctx, actor: PartyActor, params: StrategyParams): Decision {
  const skills = actorSkills(ctx, actor);
  const healPct = numParam(params, 'heal_pct', 50);
  const selfHealFloorPct = numParam(params, 'self_heal_floor_pct', 15);
  const heal = decideHeal(ctx, actor, skills, healPct, selfHealFloorPct);
  if (heal) return heal;
  const guard = decideGuardStance(ctx, actor, skills);
  if (guard) return guard;
  const buff = decideBuffOrShield(ctx, actor, skills);
  if (buff) return buff;
  const damage = decideDamage(ctx, actor, skills, ctx.targetId);
  if (damage) return damage;
  const debuff = decideDebuff(ctx, actor, skills, ctx.targetId);
  if (debuff) return debuff;
  return decideAttackFallback(ctx.targetId);
}

/** CONTRACT §4 mp_conserve：「MP 比例 < mp_reserve_pct 時不放任何技能改普攻；例外：有治療技能
 *  且任一隊友 HP% < emergency_heal_pct 仍治療」；MP 充足時比照 balanced 完整流程（heal 門檻沿用
 *  balanced 的 50/15，因為 mp_conserve 的 params 沒有另外覆寫這兩個 key）。
 *  P10（CONTRACT §3「mp_conserve 受 MP 門檻限制（緊急治療例外不含挑釁）」）：守護判斷只插在
 *  「MP 充足」分支（跟 balanced 同一個位置），MP<reserve 的節流分支（含 emergency 治療例外）
 *  一律不放 taunt——即使隊友命在旦夕，這個策略的定位是省 MP，不會為了守護反而多花 MP。 */
function decideMpConserve(ctx: Ctx, actor: PartyActor, params: StrategyParams): Decision {
  const skills = actorSkills(ctx, actor);
  const reservePct = numParam(params, 'mp_reserve_pct', 50);
  const emergencyPct = numParam(params, 'emergency_heal_pct', 30);
  const mpPct = actor.stats.mpMax > 0 ? (actor.mp / actor.stats.mpMax) * 100 : 100;
  if (mpPct < reservePct) {
    const emergencyHeal = decideHeal(ctx, actor, skills, emergencyPct, 15);
    if (emergencyHeal) return emergencyHeal;
    return decideAttackFallback(ctx.targetId);
  }
  const heal = decideHeal(ctx, actor, skills, 50, 15);
  if (heal) return heal;
  const guard = decideGuardStance(ctx, actor, skills);
  if (guard) return guard;
  const buff = decideBuffOrShield(ctx, actor, skills);
  if (buff) return buff;
  const damage = decideDamage(ctx, actor, skills, ctx.targetId);
  if (damage) return damage;
  const debuff = decideDebuff(ctx, actor, skills, ctx.targetId);
  if (debuff) return debuff;
  return decideAttackFallback(ctx.targetId);
}

/** CONTRACT §4 skill_aggressive：治療沿用 balanced 門檻（最優先）；P10 守護判斷插在其次；然後
 *  buff；然後傷害技能挑 coefficient 最高者（受 min_mp_reserve_pct 節流）；沒有候選才普攻——跳過
 *  debuff 階段（契約原文沒有提到 debuff）。 */
function decideSkillAggressive(ctx: Ctx, actor: PartyActor, params: StrategyParams): Decision {
  const skills = actorSkills(ctx, actor);
  const heal = decideHeal(ctx, actor, skills, 50, 15);
  if (heal) return heal;
  const guard = decideGuardStance(ctx, actor, skills);
  if (guard) return guard;
  const buff = decideBuffOrShield(ctx, actor, skills);
  if (buff) return buff;
  const minMpReservePct = numParam(params, 'min_mp_reserve_pct', 0);
  const damage = decideDamageHighestCoef(ctx, actor, skills, ctx.targetId, minMpReservePct);
  if (damage) return damage;
  return decideAttackFallback(ctx.targetId);
}

/** CONTRACT §4 protect_allies 的 buff/shield 階段：護盾/減傷/嘲諷類技能只保護「隊伍中目前 HP%
 *  最低者（含自己）」，且只有那個人的 HP% 低於 shield_pct 才出手——跟 balanced 的「找任一個還
 *  沒有這個效果的隊友」刻意不同（優先保護最需要的人，不是廣灑）；一般 buff（非 shield）仍沿用
 *  balanced 的 allyCandidateOrder 規則。 */
function decideProtectShieldOrBuff(ctx: Ctx, actor: PartyActor, skills: Skill[], shieldPct: number): Decision | null {
  const candidates = skills.filter((s) => s.kind === 'buff' || s.kind === 'shield');
  if (candidates.length === 0) return null;
  const alive = ctx.party.filter((p) => p.hp > 0);
  if (alive.length === 0) return null;
  const weakest = alive.reduce((w, p) => (p.hp / p.stats.hpMax < w.hp / w.stats.hpMax ? p : w));
  const weakestRatio = weakest.hp / weakest.stats.hpMax;
  for (const skill of candidates) {
    if (!isAiSkillReady(ctx, actor, skill)) continue;
    if (skill.kind === 'shield') {
      if (weakestRatio >= shieldPct / 100 || weakest.shield > 0) continue;
      if (skill.target === 'self' && weakest.id !== actor.id) continue; // self-only 護盾只能罩自己。
      return { kind: 'buff', skillId: skill.id, targetId: weakest.id };
    }
    const stat = skill.effect?.stat;
    if (!stat) continue;
    if (skill.target === 'allAllies') {
      const anyMissing = ctx.party.some((p) => p.hp > 0 && !hasStat(p, stat));
      if (!anyMissing) continue;
      return { kind: 'buff', skillId: skill.id, targetId: 'ALL' };
    }
    if (skill.target === 'self') {
      if (hasStat(actor, stat)) continue;
      return { kind: 'buff', skillId: skill.id, targetId: actor.id };
    }
    const target = allyCandidateOrder(ctx, actor).find((p) => p.hp > 0 && !hasStat(p, stat));
    if (!target) continue;
    return { kind: 'buff', skillId: skill.id, targetId: target.id };
  }
  return null;
}

/** CONTRACT §4 protect_allies 的目標規則：「正在鎖定最低 HP% 隊友的敵人」——掃描
 *  ctx.enemyTargets（敵人 windup 當下鎖定的目標，見 tick.ts advanceEnemyAI），找出哪隻敵人鎖定
 *  隊伍中 HP% 最低者，把那隻敵人當攻擊目標（先解決威脅）；找不到（沒有敵人在 windup 中鎖定
 *  那個人）就落回 ctx.targetId（balanced 的玩家鎖定目標）。 */
function protectAlliesTargetId(ctx: Ctx): string | null {
  const alive = ctx.party.filter((p) => p.hp > 0);
  if (alive.length > 0) {
    const weakest = alive.reduce((w, p) => (p.hp / p.stats.hpMax < w.hp / w.stats.hpMax ? p : w));
    for (const enemy of ctx.enemies) {
      if (enemy.hp > 0 && ctx.enemyTargets[enemy.id] === weakest.id) return enemy.id;
    }
  }
  return ctx.targetId;
}

function decideProtectAllies(ctx: Ctx, actor: PartyActor, params: StrategyParams): Decision {
  const skills = actorSkills(ctx, actor);
  const healPct = numParam(params, 'heal_pct', 60);
  const shieldPct = numParam(params, 'shield_pct', 75);
  const heal = decideHeal(ctx, actor, skills, healPct, 15);
  if (heal) return heal;
  const guard = decideProtectGuardStance(ctx, actor, skills);
  if (guard) return guard;
  const protect = decideProtectShieldOrBuff(ctx, actor, skills, shieldPct);
  if (protect) return protect;
  const targetId = protectAlliesTargetId(ctx);
  const damage = decideDamage(ctx, actor, skills, targetId);
  if (damage) return damage;
  const debuff = decideDebuff(ctx, actor, skills, targetId);
  if (debuff) return debuff;
  return decideAttackFallback(targetId);
}

/**
 * P9（CONTRACT §4 focus_fire「目標死亡才換」）：每個 tick 開頭跑一次（tick.ts 排在隊友 AI／
 * 玩家自動戰鬥之前呼叫），全隊共用同一個 ctx.focusTargetId——只有目前記錄的目標消失（死亡/
 * 不存在）時才重新挑選，避免每個用 focus_fire 的角色各自為政地不斷切換目標。挑選規則：存活敵人
 * 中 HP 絕對值最低者，同分取陣列索引小者（`<` 嚴格小於天然保留先出現者，不需要額外 tie-break）。
 * 沒有用 focus_fire 的角色完全不讀寫這個欄位，這支函式因此每個 tick 都能無害地跑一次，不影響
 * balanced/其它策略的行為。
 */
export function refreshFocusTarget(ctx: Ctx): void {
  const current = ctx.focusTargetId ? ctx.enemies.find((e) => e.id === ctx.focusTargetId && e.hp > 0) : undefined;
  if (current) return;
  const alive = ctx.enemies.filter((e) => e.hp > 0);
  if (alive.length === 0) {
    ctx.focusTargetId = null;
    return;
  }
  let lowest = alive[0];
  for (let i = 1; i < alive.length; i++) {
    if (alive[i].hp < lowest.hp) lowest = alive[i];
  }
  ctx.focusTargetId = lowest.id;
}

function decideFocusFire(ctx: Ctx, actor: PartyActor, params: StrategyParams): Decision {
  void params; // 目前沒有自己的門檻覆寫；auto_guard 只給玩家自動戰鬥（autopilot.ts）用。
  const skills = actorSkills(ctx, actor);
  const heal = decideHeal(ctx, actor, skills, 50, 15);
  if (heal) return heal;
  const guard = decideGuardStance(ctx, actor, skills);
  if (guard) return guard;
  const buff = decideBuffOrShield(ctx, actor, skills);
  if (buff) return buff;
  const targetId = ctx.focusTargetId;
  const damage = decideDamage(ctx, actor, skills, targetId);
  if (damage) return damage;
  const debuff = decideDebuff(ctx, actor, skills, targetId);
  if (debuff) return debuff;
  return decideAttackFallback(targetId);
}

/** CONTRACT §4 element_advantage：「自身普攻屬性＝武器屬性；技能屬性＝技能 element」——修法前
 *  這裡只評估武器屬性選目標，武器是 neutral（法師的杖／未裝備）時 elementMultiplier 對任何敵人
 *  恆為 1，直接落回 balanced，即使手上的技能本身有火／冰等屬性也完全沒用到（法師套這條策略等於
 *  白選）。修法：對每個存活敵人同時評估「武器倍率」與「每顆冷卻好、MP 夠的傷害技能倍率」，取
 *  全場最大值 > 1 者當目標；若那個最大值的來源是技能，就直接用該技能打該目標（技能屬性本身已經
 *  是「對這個目標 > 1」，不需要再走一次「技能優先」的次選邏輯）；來源是武器（含打平，武器優先）
 *  則完全沿用舊版流程不動，維持既有斷言（技能優先選對目標 > 1 者，否則退回 tier 最高）。
 *  沒有裝備（weaponProfile=null）一律 NEUTRAL_WEAPON_PROFILE.element='neutral'，沒有任何傷害
 *  技能時 damageCandidates=[]，兩種情況疊加起來就是舊版「全無相剋→balanced」的行為，不受影響。 */
function decideElementAdvantage(ctx: Ctx, actor: PartyActor, params: StrategyParams): Decision {
  void params;
  const skills = actorSkills(ctx, actor);
  const heal = decideHeal(ctx, actor, skills, 50, 15);
  if (heal) return heal;
  const guard = decideGuardStance(ctx, actor, skills);
  if (guard) return guard;
  const buff = decideBuffOrShield(ctx, actor, skills);
  if (buff) return buff;

  const attackElement: string = actor.weaponProfile?.element ?? 'neutral';
  const damageCandidates = skills.filter((s) => s.kind === 'damage' && isAiSkillReady(ctx, actor, s));
  const alive = ctx.enemies.filter((e) => e.hp > 0);
  let bestTargetId: string | null = null;
  let bestMul = 1;
  let bestSkill: Skill | null = null; // 目前最佳倍率若來自技能就記下是哪一顆；武器（或打平）維持 null。
  for (const e of alive) {
    const weaponMul = elementMultiplier(ctx.cfg, attackElement, e);
    if (weaponMul > bestMul) {
      bestMul = weaponMul;
      bestTargetId = e.id;
      bestSkill = null;
    }
    for (const skill of damageCandidates) {
      const skillMul = elementMultiplier(ctx.cfg, skill.element ?? 'neutral', e);
      if (skillMul > bestMul) {
        bestMul = skillMul;
        bestTargetId = e.id;
        bestSkill = skill;
      }
    }
  }
  if (!bestTargetId) {
    // 「全無相剋 → balanced」：含目標選取，落回 ctx.targetId。
    const damage = decideDamage(ctx, actor, skills, ctx.targetId);
    if (damage) return damage;
    const debuff = decideDebuff(ctx, actor, skills, ctx.targetId);
    if (debuff) return debuff;
    return decideAttackFallback(ctx.targetId);
  }

  if (bestSkill) {
    // 最佳倍率的來源就是這顆技能本身（技能屬性＝技能 element），不需要再走武器分支的次選邏輯。
    const targetId = bestSkill.target === 'allEnemies' ? 'ALL_ENEMIES' : bestTargetId;
    return { kind: 'damage', skillId: bestSkill.id, targetId };
  }

  // 最佳倍率來源是武器：沿用舊版流程——「技能優先選對該目標倍率 > 1 的傷害技能」，找不到才退回
  // tier 最高（balanced 規則）。
  const targetEnemy = ctx.enemies.find((e) => e.id === bestTargetId);
  const advantageSkill = damageCandidates.find((s) => elementMultiplier(ctx.cfg, s.element ?? 'neutral', targetEnemy) > 1);
  if (advantageSkill) {
    const targetId = advantageSkill.target === 'allEnemies' ? 'ALL_ENEMIES' : bestTargetId;
    return { kind: 'damage', skillId: advantageSkill.id, targetId };
  }
  const damage = decideDamage(ctx, actor, skills, bestTargetId);
  if (damage) return damage;
  const debuff = decideDebuff(ctx, actor, skills, bestTargetId);
  if (debuff) return debuff;
  return decideAttackFallback(bestTargetId);
}

/**
 * P9（CONTRACT §4／WIRE「引擎」）：AI 策略與玩家自動戰鬥共用的決策入口，純函式（不改任何 ctx/
 * actor 欄位，只讀取＋回傳 Decision）。呼叫端（advanceAllyAI／autopilot.ts advanceAutoBattle）
 * 各自負責把 Decision 轉成實際執行（隊友＝castNow／resolveWeaponAttack；玩家＝轉成 Command 經
 * applyCommandOnCtx，見 dispatch.ts）。
 * 型別上允許回傳 'guard'/'item'（見 types.ts Decision 型別註解），但這支函式的實作永遠不會產生
 * 這兩種——防禦（windup 鎖定＋auto_guard）與吃藥（HP%<30）只對玩家有意義，且必須在「呼叫
 * decideAction 之前」就決定好（不然這個 tick 到底要防禦還是要按契約排的優先序打技能會沒有明確
 * 答案），因此拆給 autopilot.ts 在呼叫這支函式之前自己先判斷、判斷不成立才落到這裡。
 */
export function decideAction(ctx: Ctx, actor: PartyActor, strategy: ResolvedStrategy): Decision {
  switch (strategy.id) {
    case 'mp_conserve':
      return decideMpConserve(ctx, actor, strategy.params);
    case 'skill_aggressive':
      return decideSkillAggressive(ctx, actor, strategy.params);
    case 'protect_allies':
      return decideProtectAllies(ctx, actor, strategy.params);
    case 'focus_fire':
      return decideFocusFire(ctx, actor, strategy.params);
    case 'element_advantage':
      return decideElementAdvantage(ctx, actor, strategy.params);
    case 'balanced':
    default:
      return decideBalanced(ctx, actor, strategy.params);
  }
}

/** 隊友執行 Decision：heal/buff/taunt/damage/debuff 走 castNow（技能查表用 actor.skills，跟
 *  decideAction 讀技能的來源一致）；attack 走既有的 resolveWeaponAttack 普攻路徑。 */
function applyAllyDecision(ctx: Ctx, actor: PartyActor, decision: Decision): void {
  switch (decision.kind) {
    case 'heal':
    case 'buff':
    case 'taunt':
    case 'damage':
    case 'debuff': {
      const skill = actor.skills.find((s) => s.id === decision.skillId);
      if (!skill || !decision.targetId) return;
      castNow(ctx, actor, skill, decision.targetId);
      return;
    }
    case 'attack': {
      if (!decision.targetId || decision.targetId === 'ALL' || decision.targetId === 'ALL_ENEMIES') return;
      // P7：隊友普攻走 resolveWeaponAttack（跟玩家共用同一套 hits/extraHit/splash 邏輯）；沒有
      // 裝備武器（actor.weaponProfile 為 null，本輪多數傭兵仍是如此）時退化成 NEUTRAL_WEAPON_
      // PROFILE，跟 P1～P8 舊行為完全等價。
      resolveWeaponAttack(ctx, {
        actorId: actor.id,
        attackerStats: actor.stats,
        attackerEffects: actor.activeEffects,
        attackerRating: actor.rating,
        weaponVisual: actor.weapon,
        weaponProfile: actor.weaponProfile ?? NEUTRAL_WEAPON_PROFILE,
        targetEnemyId: decision.targetId,
        chargeMul: 1,
        charged: false,
      });
      return;
    }
    default:
      return; // 'wait'/'guard'/'item'：隊友的 decideAction 呼叫不會產生這些（見型別註解），防呆保留。
  }
}

/** 行動結束的共同收尾：進 recovering、排下一次可行動時間（跟舊版 P1 邏輯一致）。 */
function finishAllyAction(ctx: Ctx, actor: PartyActor): void {
  actor.action = 'recovering';
  actor.actionUntil = ctx.now + ctx.cfg.recoveryMs;
  actor.attackReadyAt = ctx.now + randRange(ctx.rng, ctx.cfg.allyActIntervalMs);
}

/**
 * 隊友 AI（最多 4 位非玩家）：先用 decideAction 依這位隊友目前套用的策略（P9：
 * resolveStrategy(actor.strategyId, ctx.cfg.aiStrategies)，未知 id 退回 balanced）算出這次要做
 * 什麼，再依 Decision.kind 執行。沒有可行動的目標（decision.kind==='wait'）時不消耗這次行動、
 * 也不呼叫 finishAllyAction，跟舊版「沒有 skills 或沒有 ctx.targetId 時直接 return」語意相同。
 */
export function advanceAllyAI(ctx: Ctx, actor: PartyActor): void {
  if (actor.isPlayer || actor.hp <= 0) return;
  if (actor.action !== 'idle' || actor.attackReadyAt > ctx.now) return;

  const strategy = resolveStrategy(actor.strategyId, ctx.cfg.aiStrategies);
  const decision = decideAction(ctx, actor, strategy);
  if (decision.kind === 'wait') return;
  applyAllyDecision(ctx, actor, decision);
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
      // P10（CONTRACT §3／WIRE「引擎」）：守護狀態優先於加權隨機——見 formulas.ts pickEnemyTarget。
      const targetId = pickEnemyTarget(ctx, ctx.rng);
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
        // P10：目標死亡 fallback 同樣改走 pickEnemyTarget（見上面 idle 分支的理由）。
        const fallbackId = pickEnemyTarget(ctx, ctx.rng);
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
