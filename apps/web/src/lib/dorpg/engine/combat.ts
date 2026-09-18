// 傷害/治療/護盾/buff/debuff 的效果套用（純函式風格：吃 Ctx 直接在裡面改，呼叫端保證是本次呼叫的
// 工作副本）。dispatch（玩家普攻/技能）、ai（隊友普攻/治療）、tick（敵人攻擊、施法完成結算）三邊
// 共用，避免同一段「算傷害→套用→處理死亡/受擊」邏輯抄三次、規則跑掉。
import type { ActorStats, BuffDebuffStat, CombatRating, DmgType, ElementKind, Skill, WeaponKind } from '../types';
import type { Ctx } from './context';
import { pushEvent, pushLog } from './context';
import { activeStatSum, applyStatusEffect, damageTakenMultiplier, effectiveRating, rollCritMultiplier } from './effects';
import { computeHeal, computeRawDamage, critChance, elementMultiplier, floorInt, missChance, selectAliveByThreat } from './formulas';
import type { ActiveEffect, EnemyActor, PartyActor, PendingCast } from './types';

/** 對敵人造成傷害後的死亡/受擊處理：死亡→dying+enemyDeath+目標自動換人；存活→hitReaction 覆蓋層。 */
function applyEnemyDamage(ctx: Ctx, enemy: EnemyActor, damage: number): void {
  // P6（CONTRACT §1）：damage 呼叫端（computeRawDamage/computeDamage）已經是整數，這裡 floorInt
  // 只是最後一道防線——跟 applyPartyDamage 的寫法保持對稱，稽核「hp 相關賦值都經過 floorInt」時
  // 不必記得哪幾個賦值點可以跳過。
  enemy.hp = floorInt(Math.max(0, enemy.hp - damage));
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
 * P2（暴擊／Miss／無效攻擊）結算順序照 SPEC §4：
 *   1. 命中判定（missChance）→ 沒中：'miss'，damage=0。
 *   2. 屬性倍率（elementMultiplier）→ 0 倍：'immune'，damage=0（不必再算暴擊/傷害）。
 *   3. 暴擊判定（critChance）→ critMul 疊在跟蓄氣倍率同一個「命中後、扣防禦前」的乘數位置。
 *   4. computeRawDamage 算出 net（刻意不套 max(1,...)）→ net≤0：也是 'immune'，damage=0；
 *      否則才是真正的 'normal'/'critical' 傷害。
 * 這支函式本身沒有「no rating」分支：opts.attackerRating 與 enemy.rating 一定是具體數值
 * （createBattle 已經幫忙補好，見 formulas.ts 的 deriveDefaultPartyRating/deriveDefaultMonsterRating）。
 *
 * P5 變更：
 *   - opts.atk（單一數字）改成 opts.attackerStats + opts.attackerEffects：攻擊者的 atk_pct/matk_pct
 *     buff 與 rating 的 aspd/crit_pct/flee/hit buff 都要在「命中判定當下」即時套用，集中在這裡算一次，
 *     不要求 dispatch.ts/ai.ts 三個呼叫端各自重複同一段疊加邏輯（見 effects.ts effectiveRating/
 *     activeStatSum）。
 *   - opts.dmgType：'magic' 用 MATK 扣 MDEF（含防禦方的 mdef_pct debuff），'physical'（預設）用
 *     ATK 扣 DEF（含 def_pct debuff）——CONTRACT §6。
 *   - elementMultiplier 改吃整個 enemy（attribute+weakElements），不再只傳 attribute 字串。
 *   - critMul 改成 rollCritMultiplier() 浮動抽樣，不再是固定的 ctx.cfg.critMultiplier。
 */
export function resolveAttackOrDamageSkill(
  ctx: Ctx,
  opts: {
    actorId: string;
    attackerStats: ActorStats;
    attackerEffects: ActiveEffect[];
    coefficient: number;
    flat: number;
    weapon: WeaponKind;
    targetEnemyId: string;
    chargeMul: number;
    charged: boolean;
    /** 攻擊者基礎評級（命中/暴擊判定用，尚未套 attackerEffects）；呼叫端一律從 actor.rating 帶入。 */
    attackerRating: CombatRating;
    /** 技能的 element；不傳＝普攻，固定 'neutral'（SPEC §4）。 */
    element?: ElementKind;
    /** P5：傷害屬性，不傳＝'physical'（普攻與既有技能的既有行為）。 */
    dmgType?: DmgType;
  },
): void {
  const enemy = ctx.enemies.find((e) => e.id === opts.targetEnemyId);
  if (!enemy || enemy.hp <= 0) {
    pushLog(ctx, `${opts.actorId} 的攻擊目標已消失，落空`);
    return;
  }
  const element: string = opts.element ?? 'neutral';
  const dmgType: DmgType = opts.dmgType ?? 'physical';
  const emitAttack = (result: 'normal' | 'critical' | 'miss' | 'immune', damage: number) =>
    pushEvent(ctx, {
      kind: 'attack',
      actorId: opts.actorId,
      targetId: enemy.id,
      weapon: opts.weapon,
      result,
      damage,
      charged: opts.charged,
    });

  const attackerRating = effectiveRating(opts.attackerRating, opts.attackerEffects);
  const defenderRating = effectiveRating(enemy.rating, enemy.activeEffects);

  const missPct = missChance(attackerRating, defenderRating, ctx.cfg);
  const isHit = ctx.rng() >= missPct / 100;
  if (!isHit) {
    emitAttack('miss', 0);
    pushLog(ctx, `${opts.actorId} 對 ${enemy.name} 揮空`);
    return;
  }

  const elementMul = elementMultiplier(ctx.cfg, enemy, element);
  if (elementMul === 0) {
    emitAttack('immune', 0);
    pushLog(ctx, `${opts.actorId} 對 ${enemy.name} 的攻擊完全無效（屬性剋制）`);
    return;
  }

  const critPct = critChance(attackerRating, defenderRating, ctx.cfg);
  const isCrit = ctx.rng() < critPct / 100;
  // 審查#5【低・PLAUSIBLE】根因修復：Derived.CritDmgPct 過去只進 Compute()／角色頁顯示，從未接進
  // 戰鬥的實際暴擊倍率計算——攻擊者身上的 crit_dmg_pct 被動加成疊在浮動暴擊倍率（rollCritMultiplier
  // 抽出的 [critMultMin,critMultMax]）之上，只有在真的判定出暴擊時才有意義（沒有暴擊當然沒有
  // 「暴擊傷害加成」可言）。
  const critMul = isCrit ? rollCritMultiplier(ctx.rng, ctx.cfg) * (1 + (attackerRating.critDmgPct ?? 0) / 100) : 1;

  const atkPctStat: BuffDebuffStat = dmgType === 'magic' ? 'matk_pct' : 'atk_pct';
  const atkBase = dmgType === 'magic' ? opts.attackerStats.matk : opts.attackerStats.atk;
  const atk = Math.round(atkBase * (1 + activeStatSum(opts.attackerEffects, atkPctStat) / 100));

  const defPctStat: BuffDebuffStat = dmgType === 'magic' ? 'mdef_pct' : 'def_pct';
  const defBase = dmgType === 'magic' ? enemy.stats.mdef : enemy.stats.def;
  const def = Math.round(defBase * (1 + activeStatSum(enemy.activeEffects, defPctStat) / 100));

  const net = computeRawDamage(atk, opts.coefficient, opts.flat, elementMul, opts.chargeMul, critMul, def);
  if (net <= 0) {
    emitAttack('immune', 0);
    pushLog(ctx, `${opts.actorId} 對 ${enemy.name} 的攻擊被完全擋下`);
    return;
  }

  const result: 'normal' | 'critical' = isCrit ? 'critical' : 'normal';
  emitAttack(result, net);
  pushLog(ctx, `${opts.actorId} 對 ${enemy.name}${isCrit ? '爆擊，' : ''}造成 ${net} 點傷害`);
  applyEnemyDamage(ctx, enemy, net);
}

/**
 * 敵人打隊友：先扣盾再扣 HP（規格 §2）；打死當場記 actorDown，且若玩家正在逃跑判定中則立即取消判定
 * 記失敗。P5：進來的第一步先套 damage_taken_pct（CONTRACT §5 buff 詞彙——只有隊伍側會有這個 buff，
 * debuff 詞彙表沒有這一項，見 BuffDebuffStat 型別註解），這樣不管傷害是從哪個路徑算出來的
 * （combat.ts 直接命中、ai.ts 敵人普攻），只要最終都走 applyPartyDamage 就一定會套到，不必在每個
 * 呼叫端各自記得套一次。clamp 下限 0（Math.max(0,...)）：不允許減傷疊過頭變成「倒扣血」。
 */
export function applyPartyDamage(ctx: Ctx, actor: PartyActor, rawDamage: number): void {
  const dtMul = damageTakenMultiplier(actor.activeEffects);
  // P6（CONTRACT §1）：damage_taken_pct 明講「在套用前 floor」——改 Math.round 為 floorInt，
  // 兩者在 dtMul<1（減傷，最常見的用法）時會算出不同的整數，floor 是契約指定的方向（見
  // formulas.ts floorInt 型別註解）。
  let dmg = Math.max(0, floorInt(rawDamage * dtMul));
  if (actor.shield > 0) {
    const absorbed = Math.min(actor.shield, dmg);
    actor.shield = floorInt(actor.shield - absorbed);
    dmg -= absorbed;
  }
  actor.hp = floorInt(Math.max(0, actor.hp - dmg));
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

/** 匯出給 tick.ts 的 hp_regen_pct 定時回復共用（見 pruneAllEffects）：跟技能治療共用同一段
 *  「封頂 hpMax＋推 heal 事件＋記 log」邏輯，不必為了自我回血再抄一次。 */
export function applyHealToTarget(ctx: Ctx, casterId: string, targetId: string, amount: number): void {
  const target = ctx.party.find((p) => p.id === targetId);
  if (!target || target.hp <= 0) return;
  // P6（CONTRACT §1）：amount 呼叫端（computeHeal）已經 floor 過，這裡的 floorInt 只是最後一道
  // 防線，跟 hp 相關的其它賦值點一致（見 applyEnemyDamage 同樣的理由）。
  target.hp = floorInt(Math.min(target.stats.hpMax, target.hp + amount));
  pushEvent(ctx, { kind: 'heal', actorId: casterId, targetId, amount });
  pushLog(ctx, `${casterId} 治療 ${target.name} ${amount} 點`);
}

function applyShieldToTarget(ctx: Ctx, casterId: string, targetId: string, amount: number): void {
  const target = ctx.party.find((p) => p.id === targetId);
  if (!target || target.hp <= 0) return;
  target.shield = floorInt(target.shield + amount);
  pushEvent(ctx, { kind: 'shield', actorId: casterId, targetId, amount });
  pushLog(ctx, `${casterId} 為 ${target.name} 加上 ${amount} 點護盾`);
}

/**
 * heal/shield 技能結算：矩形都用 computeHeal(MATK,...) 算量——規格只明講治療公式用 MATK，
 * shield 沒有另外給公式，這裡把它當同一種「輔助類、依 MATK 算量」的效果套用同一條公式（文件內決策，
 * 已在回報中列為偏離契約之處）。targetId==='ALL' 時對所有存活隊員各推一筆事件。
 * P5：MATK 套用施法者自己的 matk_pct buff（跟 resolveAttackOrDamageSkill 對攻擊者 atk_pct 的處理
 * 對稱）；targetId 型別放寬到含 'ALL_ENEMIES'（P5 新 sentinel）純粹是為了跟 PendingCast 的型別對齊，
 * heal/shield 的 dispatch 路由永遠不會真的產生這個值，落到這裡的話 `[targetId]` 找不到對應隊員、
 * 安全地什麼事都不做。
 */
export function resolveSupportSkill(ctx: Ctx, casterId: string, skill: Skill, targetId: string | 'ALL' | 'ALL_ENEMIES' | null): void {
  const caster = ctx.party.find((p) => p.id === casterId);
  if (!caster) return;
  const matk = Math.round(caster.stats.matk * (1 + activeStatSum(caster.activeEffects, 'matk_pct') / 100));
  const amount = computeHeal(matk, skill.coefficient, skill.flat);
  const targets =
    targetId === 'ALL' ? ctx.party.filter((p) => p.hp > 0).map((p) => p.id) : targetId ? [targetId] : [];
  for (const tId of targets) {
    if (skill.kind === 'heal') applyHealToTarget(ctx, casterId, tId, amount);
    else if (skill.kind === 'shield') applyShieldToTarget(ctx, casterId, tId, amount);
  }
}

/**
 * P5（CONTRACT §5）：buff/debuff 技能結算——buff 只打隊伍（self/ally/allAllies），debuff 只打敵方
 * （enemy/allEnemies），詞彙表本來就分屬兩個不相交的集合（見 types.ts BuffDebuffStat 型別註解），
 * 這裡按 skill.kind 分流找目標，不看 skill.target 字面值本身（target 只決定 dispatch.ts 的
 * targeting 流程要怎麼收集 pending.targetId，這裡只管把收集好的 targetId 換算成實際的目標物件）。
 */
function resolveBuffDebuffTargets(ctx: Ctx, skill: Skill, targetId: PendingCast['targetId']): (PartyActor | EnemyActor)[] {
  if (skill.kind === 'buff') {
    if (targetId === 'ALL') return ctx.party.filter((p) => p.hp > 0);
    if (typeof targetId === 'string' && targetId !== 'ALL_ENEMIES') {
      const p = ctx.party.find((x) => x.id === targetId && x.hp > 0);
      return p ? [p] : [];
    }
    return [];
  }
  // debuff
  if (targetId === 'ALL_ENEMIES') return ctx.enemies.filter((e) => e.hp > 0);
  if (typeof targetId === 'string' && targetId !== 'ALL') {
    const e = ctx.enemies.find((x) => x.id === targetId && x.hp > 0);
    return e ? [e] : [];
  }
  return [];
}

/**
 * buff/debuff 技能結算本體：把 skill.effect 展開後的 stat/value/durationMs 套用到目標身上，
 * 疊加規則見 effects.ts applyStatusEffect（同 stat 同來源刷新不疊加）。effect 資料不完整（缺
 * stat/value，理論上不該發生——dispatch 允許進入 casting 前不驗證這件事，因為那是資料層的責任，
 * 不是玩家操作能觸發的錯誤）時安全跳過，不讓引擎因為離線資料/測試資料缺欄位而丟例外。
 */
export function resolveBuffDebuff(ctx: Ctx, casterId: string, skill: Skill, pending: PendingCast): void {
  const eff = skill.effect;
  if (!eff || !eff.stat || eff.value === undefined) {
    pushLog(ctx, `${casterId} 施放的 ${skill.name} 缺少 effect 資料，略過結算`);
    return;
  }
  const stat = eff.stat;
  const value = eff.value;
  const durationMs = eff.durationMs ?? 0;
  const kind: 'buff' | 'debuff' = skill.kind === 'debuff' ? 'debuff' : 'buff';
  const targets = resolveBuffDebuffTargets(ctx, skill, pending.targetId);
  for (const target of targets) {
    const newEffect: ActiveEffect = { stat, value, expiresAt: ctx.now + durationMs, sourceSkillId: skill.id, kind };
    // hp_regen_pct 專用：明確把第一次回復時機定在「套用時刻 + 1000ms」，不是「套用當下立刻回一次」
    // ——不設這個欄位的話，pruneAndRegenEffects 會在第一次看到這筆效果時（剛好就是本次 tick，因為
    // resolvePartyTimers 剛結算完 cast、pruneAllEffects 緊接著在同一個 tick 呼叫）把 nextTickAt 預設
    //成「現在」，導致套用當下立刻多回一次血，跟「每 1000ms 回一次」的直覺不符。其餘 stat 不需要
    // 這個欄位（pruneAndRegenEffects 只在 stat==='hp_regen_pct' 時才會讀它），刻意不設，讓
    // ActiveEffect 保持最小形狀。
    if (stat === 'hp_regen_pct') newEffect.nextTickAt = ctx.now + 1000;
    applyStatusEffect(target, newEffect);
    pushEvent(ctx, { kind: 'statusApplied', actorId: casterId, targetId: target.id, effectKind: kind, stat, value, durationMs });
    pushLog(ctx, `${casterId} 對 ${target.name} 施加 ${skill.name}（${stat} ${value > 0 ? '+' : ''}${value}，持續 ${durationMs}ms）`);
  }
}

/**
 * casting 完成時的效果結算（tick.ts 呼叫）：damage 打 pending.targetId 的敵人（P5：支援 hits>1 多段
 * 命中與 target='allEnemies' 打全體）、heal/shield 走 resolveSupportSkill、buff/debuff 走
 * resolveBuffDebuff。passive 不會進技能欄（後端已把 stat 算進玩家 stats，engine 完全不處理），
 * special 在 dispatch 階段就已經被拒絕（implemented=false），兩者理論上都不會有 pendingCast 走到
 * 這裡——沒有對應分支，遇到的話單純什麼都不做（防呆，不拋例外）。
 */
export function resolveCastEffect(ctx: Ctx, actor: PartyActor, skill: Skill, pending: PendingCast): void {
  if (skill.kind === 'damage') {
    const hits = skill.hits ?? 1;
    const dmgType = skill.dmgType ?? 'physical';
    const targetIds =
      pending.targetId === 'ALL_ENEMIES'
        ? ctx.enemies.filter((e) => e.hp > 0).map((e) => e.id)
        : typeof pending.targetId === 'string' && pending.targetId !== 'ALL'
          ? [pending.targetId]
          : [];
    for (const tId of targetIds) {
      for (let i = 0; i < hits; i++) {
        resolveAttackOrDamageSkill(ctx, {
          actorId: actor.id,
          attackerStats: actor.stats,
          attackerEffects: actor.activeEffects,
          coefficient: skill.coefficient,
          flat: skill.flat,
          weapon: skill.weapon,
          targetEnemyId: tId,
          chargeMul: 1,
          charged: false,
          attackerRating: actor.rating,
          element: skill.element,
          dmgType,
        });
      }
    }
    return;
  }
  if (skill.kind === 'heal' || skill.kind === 'shield') {
    resolveSupportSkill(ctx, actor.id, skill, pending.targetId);
    return;
  }
  if (skill.kind === 'buff' || skill.kind === 'debuff') {
    resolveBuffDebuff(ctx, actor.id, skill, pending);
  }
}
