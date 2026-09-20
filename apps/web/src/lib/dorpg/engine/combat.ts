// 傷害/治療/護盾/buff/debuff 的效果套用（純函式風格：吃 Ctx 直接在裡面改，呼叫端保證是本次呼叫的
// 工作副本）。dispatch（玩家普攻/技能）、ai（隊友普攻/治療）、tick（敵人攻擊、施法完成結算）三邊
// 共用，避免同一段「算傷害→套用→處理死亡/受擊」邏輯抄三次、規則跑掉。
import type { ActorStats, BuffDebuffStat, CombatRating, DmgType, ElementKind, EnemySlotId, Skill, WeaponKind, WeaponProfileWire } from '../types';
import type { Ctx } from './context';
import { pushEvent, pushLog } from './context';
import { activeStatSum, applyStatusEffect, damageTakenMultiplier, effectiveRating, rollCritMultiplier } from './effects';
import { combineElementResistPct, computeHeal, computeRawDamage, critChance, elementMultiplier, floorInt, missChance, NEUTRAL_WEAPON_PROFILE, normalizeElementAlias, normalizeSizeAlias, rowBonusMultiplier, rowOfSlot, selectAliveByThreat } from './formulas';
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
 *
 * P7 變更（CONTRACT §3、WIRE「引擎」）：
 *   - 新增必填 opts.attackerWeapon（呼叫端一律傳 `actor.weaponProfile ?? NEUTRAL_WEAPON_PROFILE`，
 *     見 formulas.ts）：sizeBonus[enemy.size] 乘在最終傷害上（普攻與技能傷害皆適用，武器對「這個
 *     體型比較好打」的加成不分攻擊手段來源）；element 未宣告或宣告 neutral 的 physical 傷害改帶
 *     武器 element（magic 傷害恆用自己宣告的 element，不被武器覆蓋——見下方 element 判斷式）。
 *   - 回傳 { result, damage } 給呼叫端（目前只有 resolveWeaponAttack 會用到，算斧的濺射基準）；
 *     既有呼叫點（resolveCastEffect）忽略回傳值，行為不變。
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
    /** P7：攻擊者目前裝備武器的戰鬥效果；沒有武器時呼叫端一律傳 NEUTRAL_WEAPON_PROFILE。 */
    attackerWeapon: WeaponProfileWire;
  },
): { result: 'normal' | 'critical' | 'miss' | 'immune'; damage: number } {
  const enemy = ctx.enemies.find((e) => e.id === opts.targetEnemyId);
  if (!enemy || enemy.hp <= 0) {
    pushLog(ctx, `${opts.actorId} 的攻擊目標已消失，落空`);
    return { result: 'miss', damage: 0 };
  }
  const dmgType: DmgType = opts.dmgType ?? 'physical';
  // P7（CONTRACT §3：「普攻與 element=neutral 的物理技能帶武器 element（魔法技能維持自身屬性）」）：
  // magic 傷害恆用技能自己宣告的 element；physical 傷害（含普攻，opts.element 恆為 undefined）
  // 只有在沒宣告或宣告 neutral 時才吃武器 element，已明確宣告非中性屬性的物理技能維持自己的宣告。
  const element: string =
    dmgType === 'magic'
      ? (opts.element ?? 'neutral')
      : !opts.element || opts.element === 'neutral'
        ? opts.attackerWeapon.element
        : opts.element;
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
    return { result: 'miss', damage: 0 };
  }

  const elementMul = elementMultiplier(ctx.cfg, element, enemy);
  if (elementMul === 0) {
    emitAttack('immune', 0);
    pushLog(ctx, `${opts.actorId} 對 ${enemy.name} 的攻擊完全無效（屬性剋制）`);
    return { result: 'immune', damage: 0 };
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

  const rawNet = computeRawDamage(atk, opts.coefficient, opts.flat, elementMul, opts.chargeMul, critMul, def);
  // P7（CONTRACT §3／§4「對於大/小體型的怪物會有額外傷害」；INTEGRATOR 補正規化，見 formulas.ts
  // normalizeSizeAlias 檔頭說明）：先過中文別名表（現網 rpg_monsters.size 實際是中文原文）再判斷是
  // 否為三個英文字面值之一——兩者皆非（未知字串、缺欄位）視為 0 加成，不猜測對應關係。
  const normalizedSize = normalizeSizeAlias(enemy.size);
  const sizeKey = normalizedSize === 'small' || normalizedSize === 'medium' || normalizedSize === 'large' ? normalizedSize : undefined;
  const sizeBonusPct = sizeKey ? opts.attackerWeapon.sizeBonus[sizeKey] : 0;
  // P12（CONTRACT §1「套用範圍：以武器造成的物理傷害＝普攻＋物理技能...乘在最終傷害上（在
  // 暴擊、屬性、體型之後、floor 之前）」）：排位加成（弓打後排/鈍器打前排）只在 dmgType==='physical'
  // 時套用——普攻恆為 physical（見 resolveWeaponAttack），物理技能同樣是 physical，魔法技能
  // （dmgType==='magic'）不吃，跟契約「魔法技能不吃」逐字對齊。row 優先讀 enemy.row（若上游已經
  // 直接送這個欄位），沒有的話用 rowOfSlot(enemy.slot) 推導（見該函式型別註解——slot 是
  // EnemyActor 必填欄位，永遠推得出來，不依賴 createBattle/engine/index.ts（非 ENGINE 所有權）
  // 是否額外寫入 row）。
  const rowMul = dmgType === 'physical' ? rowBonusMultiplier(opts.attackerWeapon, enemy.row ?? rowOfSlot(enemy.slot)) : 1;
  const sizeMul = 1 + sizeBonusPct / 100;
  // 體型與排位兩個乘數合併在同一次 floor——兩者皆中性（sizeMul===1 且 rowMul===1，P7 之前／
  // 沒有排位加成的武器打中性體型怪物的既有情況）時直接沿用 rawNet，跟舊版「sizeBonusPct 為 0
  // 就不多算一次 floor」完全等價，不引入既有斷言看不到差異的浮點運算。
  const net = sizeMul === 1 && rowMul === 1 ? rawNet : Math.floor(rawNet * sizeMul * rowMul);
  if (net <= 0) {
    emitAttack('immune', 0);
    pushLog(ctx, `${opts.actorId} 對 ${enemy.name} 的攻擊被完全擋下`);
    return { result: 'immune', damage: 0 };
  }

  const result: 'normal' | 'critical' = isCrit ? 'critical' : 'normal';
  emitAttack(result, net);
  pushLog(ctx, `${opts.actorId} 對 ${enemy.name}${isCrit ? '爆擊，' : ''}造成 ${net} 點傷害`);
  applyEnemyDamage(ctx, enemy, net);
  return { result, damage: net };
}

/** P7（CONTRACT §3 斧）：同排相鄰站位表——front 排三格互為鄰居（左右各鄰中間，中間鄰左右兩側），
 *  rear 排只有兩格、互為彼此的唯一鄰居；兩排之間不相鄰（濺射不跨排）。逐字對齊 WIRE.md「front:
 *  left/center/right；rear: left/right」的站位順序。 */
const ROW_NEIGHBORS: Readonly<Record<EnemySlotId, readonly EnemySlotId[]>> = {
  rear_left: ['rear_right'],
  rear_right: ['rear_left'],
  front_left: ['front_center'],
  front_center: ['front_left', 'front_right'],
  front_right: ['front_center'],
};

/**
 * P7（CONTRACT §3 斧：「造成範圍傷害，左右兩側的怪物會受到波及造成 splashPct% 的傷害」）：主目標
 * 這一下「已經結算完」的傷害 × splashPct/100，獨立 floor，直接套用在同排相鄰的存活敵人身上——
 * 契約原文明講「不觸發 miss/crit 重算」，所以完全不呼叫 missChance/critChance/elementMultiplier，
 * 也不會因為濺射目標的屬性/迴避而被閃掉或免疫，單純是主擊傷害的固定比例分潤。事件另外推一筆
 * （不是把濺射傷害加進主擊事件裡），並標記 `splash:true` 供 FRONTEND 浮字區分主擊/濺射。
 */
function applySplashDamage(
  ctx: Ctx,
  actorId: string,
  weaponVisual: WeaponKind,
  primarySlot: EnemySlotId,
  primaryDamage: number,
  splashPct: number,
): void {
  const neighbors = ROW_NEIGHBORS[primarySlot] ?? [];
  for (const slotId of neighbors) {
    const target = ctx.enemies.find((e) => e.slot === slotId && e.hp > 0);
    if (!target) continue;
    const dmg = Math.floor(primaryDamage * (splashPct / 100));
    if (dmg <= 0) continue;
    pushEvent(ctx, { kind: 'attack', actorId, targetId: target.id, weapon: weaponVisual, result: 'normal', damage: dmg, charged: false, splash: true });
    pushLog(ctx, `${actorId} 的攻擊波及 ${target.name}，造成 ${dmg} 點濺射傷害`);
    applyEnemyDamage(ctx, target, dmg);
  }
}

/**
 * P12（CONTRACT §1 槍：「攻擊前排的怪物，有機會造成貫穿的傷害，讓對應位置的後排怪物也受到...
 * 波及傷害」）：前排槽位→候選後排槽位（依序嘗試，取第一個存活的）。front_left/front_right
 * 各自只有一個固定對應（沒有 fallback——契約原文逐字列出，沒有備選）；front_center 兩側都能
 * 打到，先試 rear_left、死亡或不存在才試 rear_right。rear_left/rear_right 本身（怪物已經是
 * 後排）回傳 []——貫穿只從前排目標發動，這裡沒有「後排再貫穿到更後面」的機制。
 */
const PIERCE_CANDIDATES: Readonly<Record<EnemySlotId, readonly EnemySlotId[]>> = {
  front_left: ['rear_left'],
  front_right: ['rear_right'],
  front_center: ['rear_left', 'rear_right'],
  rear_left: [],
  rear_right: [],
};

/**
 * P12（CONTRACT §1／WIRE「引擎」：「resolveWeaponAttack 在前排目標命中後依 pierceChancePct
 * （rng）對映後排目標加波及；事件 attack 加 pierce?: true」）：只有普攻（resolveWeaponAttack）
 * 呼叫這支函式，物理技能不會（契約「只有普攻會判定」）——多段普攻（雙劍/槍）每段各自獨立呼叫，
 * 不是整次普攻共用一次判定結果。是否要嘗試貫穿（含要不要消耗 rng）由呼叫端
 * （resolveWeaponAttack）的 `wp.pierceChancePct > 0 && ctx.rng() < ...` 決定，這支函式只管
 * 「機率已經判定成功之後」找目標、算傷害、套用——找不到存活的候選後排（PIERCE_CANDIDATES 回傳
 * []，或候選槽位都沒有存活怪物）一律靜默不貫穿，不算錯誤、不推事件。
 * 波及傷害＝floor(主擊「已經結算完」的實際傷害 × pierceDmgPct/100)，跟 applySplashDamage 同一個
 * 精神：不重新判定 miss/crit/屬性/體型/排位，不扣後排怪的 DEF，直接呼叫 applyEnemyDamage——這裡
 * 只呼叫 applyEnemyDamage、不會遞迴呼叫 resolveWeaponAttack/resolveAttackOrDamageSkill，天然滿足
 * 契約「貫穿不觸發二次貫穿」；也不呼叫 applySplashDamage，滿足「貫穿與濺射互不影響」。
 */
function tryPierce(
  ctx: Ctx,
  actorId: string,
  weaponVisual: WeaponKind,
  primarySlot: EnemySlotId,
  primaryDamage: number,
  pierceDmgPct: number,
): void {
  const candidates = PIERCE_CANDIDATES[primarySlot] ?? [];
  let target: EnemyActor | undefined;
  for (const slotId of candidates) {
    target = ctx.enemies.find((e) => e.slot === slotId && e.hp > 0);
    if (target) break;
  }
  if (!target) return;
  const dmg = Math.floor(primaryDamage * (pierceDmgPct / 100));
  if (dmg <= 0) return;
  pushEvent(ctx, { kind: 'attack', actorId, targetId: target.id, weapon: weaponVisual, result: 'normal', damage: dmg, charged: false, pierce: true });
  pushLog(ctx, `${actorId} 的攻擊貫穿，波及 ${target.name}，造成 ${dmg} 點貫穿傷害`);
  applyEnemyDamage(ctx, target, dmg);
}

/**
 * P7（CONTRACT §3／WIRE「普攻」）：武器化的普攻——取代 P1～P6「普攻恆單擊、coefficient=1」的
 * 簡化寫法，統一給 dispatch.ts（玩家 ATTACK_RELEASE）與 ai.ts（隊友普攻 fallback）共用：
 *   - hits 段各自獨立呼叫一次 resolveAttackOrDamageSkill（各自獨立 miss/crit/浮字），每段
 *     coefficient=weaponProfile.hitMul（雙劍每段 60%、槍每段 50%，不是「總傷害不變、只是拆成
 *     幾段」）。
 *   - extraHitChancePct>0 時消耗一次 ctx.rng() 決定要不要多打一段（槍系「有機率發動成 3 連擊」）
 *     ——沒有這個機制的武器（含 NEUTRAL_WEAPON_PROFILE）完全不消耗這次 rng()，既有測試的 rng
 *     序列因此不受影響。
 *   - 每段命中後若 weaponProfile.splashPct>0 觸發濺射（斧）。
 *   - P12（CONTRACT §1 槍）：每段命中後若 weaponProfile.pierceChancePct>0 再消耗一次 ctx.rng()
 *     判定要不要貫穿到對應後排（tryPierce）——濺射與貫穿是兩個獨立判定，互不影響。
 * 無武器（weaponProfile===NEUTRAL_WEAPON_PROFILE）時 totalHits=1、coefficient=1、不消耗額外
 * rng()、不觸發濺射／貫穿，跟 P1～P6 舊行為完全等價（既有斷言因此全數維持不變）。
 */
export function resolveWeaponAttack(
  ctx: Ctx,
  opts: {
    actorId: string;
    attackerStats: ActorStats;
    attackerEffects: ActiveEffect[];
    attackerRating: CombatRating;
    weaponVisual: WeaponKind;
    weaponProfile: WeaponProfileWire;
    targetEnemyId: string;
    chargeMul: number;
    charged: boolean;
  },
): void {
  const wp = opts.weaponProfile;
  const extra = wp.extraHitChancePct > 0 && ctx.rng() < wp.extraHitChancePct / 100 ? 1 : 0;
  const totalHits = Math.max(1, wp.hits) + extra;
  for (let i = 0; i < totalHits; i++) {
    const target = ctx.enemies.find((e) => e.id === opts.targetEnemyId);
    if (!target || target.hp <= 0) break; // 目標中途死亡（多段命中時），後續段落自然停止，不打空氣。
    const outcome = resolveAttackOrDamageSkill(ctx, {
      actorId: opts.actorId,
      attackerStats: opts.attackerStats,
      attackerEffects: opts.attackerEffects,
      coefficient: wp.hitMul,
      flat: 0,
      weapon: opts.weaponVisual,
      targetEnemyId: opts.targetEnemyId,
      chargeMul: opts.chargeMul,
      charged: opts.charged,
      attackerRating: opts.attackerRating,
      dmgType: 'physical',
      attackerWeapon: wp,
    });
    if (wp.splashPct > 0 && (outcome.result === 'normal' || outcome.result === 'critical')) {
      applySplashDamage(ctx, opts.actorId, opts.weaponVisual, target.slot, outcome.damage, wp.splashPct);
    }
    // P12（CONTRACT §1「貫穿只在普攻命中前排目標時判定」）：主目標為後排（rear_left/rear_right）
    // 時完全不該判定貫穿——PIERCE_CANDIDATES 對 rear 槽位本來就回傳 []，tryPierce 找不到候選會
    // 靜默不做事，但少了 isFront 短路的話，呼叫前的 `ctx.rng() < ...` 仍然會白白消耗一次 rng()，
    // 把後續（下一段普攻的 miss/crit、或再往後的其它判定）序列整個打亂一格。isFront 用
    // `target.row ?? rowOfSlot(target.slot)` 判斷（target 已在迴圈上方取得，跟 combat.ts 其餘
    // 讀取 enemy.row 的既有寫法一致），跟 `wp.pierceChancePct > 0` 一樣擋在 `ctx.rng()` 之前短路。
    // 跟 extraHitChancePct 的既有寫法同一個精神——pierceChancePct===0（沒有貫穿機制的武器，含
    // NEUTRAL_WEAPON_PROFILE）或主目標非前排時完全不呼叫 ctx.rng()，既有測試的 rng 序列因此不受
    // 影響。命中結果非 normal/critical（miss/immune）沒有「已結算的傷害」可言，同樣不判定貫穿、
    // 不消耗 rng。
    const isFront = (target.row ?? rowOfSlot(target.slot)) === 'front';
    if (isFront && wp.pierceChancePct > 0 && (outcome.result === 'normal' || outcome.result === 'critical') && ctx.rng() < wp.pierceChancePct / 100) {
      tryPierce(ctx, opts.actorId, opts.weaponVisual, target.slot, outcome.damage, wp.pierceDmgPct);
    }
  }
}

/**
 * 敵人打隊友：先扣盾再扣 HP（規格 §2）；打死當場記 actorDown，且若玩家正在逃跑判定中則立即取消判定
 * 記失敗。P5：進來的第一步先套 damage_taken_pct（CONTRACT §5 buff 詞彙——只有隊伍側會有這個 buff，
 * debuff 詞彙表沒有這一項，見 BuffDebuffStat 型別註解），這樣不管傷害是從哪個路徑算出來的
 * （combat.ts 直接命中、ai.ts 敵人普攻），只要最終都走 applyPartyDamage 就一定會套到，不必在每個
 * 呼叫端各自記得套一次。
 * P8（CONTRACT §2／WIRE「引擎」）：damageTakenMultiplier 新增 actor.equipmentEffects.damageTakenPct
 * 參數——與 buff 相加後 clamp ≥ −60（見該函式型別註解，取代舊版「clamp 最終倍率下限 0」的規則）；
 * elementResistPct 改用 combineElementResistPct 把武器（鍊）與裝備（防具/飾品彙總）兩份數字相加後
 * clamp ≤ 60，取代舊版只讀武器單一來源。
 */
export function applyPartyDamage(ctx: Ctx, actor: PartyActor, rawDamage: number, attackerElement?: string): void {
  const dtMul = damageTakenMultiplier(actor.activeEffects, actor.equipmentEffects.damageTakenPct);
  // P7（CONTRACT §1「elementResistPct 減免非 neutral 怪物造成的傷害」）：attackerElement 由呼叫端
  // 傳入（目前只有 ai.ts 的怪物普攻會傳 enemy.attribute），只有具體、非 'neutral' 時才用
  // 武器＋裝備合計的抗性折算減免——玩家沒有「防禦屬性」可言，這是裝備給的固定抗性，不查五行
  // 相剋表。
  // 審查#1【中】根因修復：enemy.attribute 現網是中文原文（見 elementCycleMultiplier 檔頭說明），
  // 灰白獸人等怪物的「無」字面值原本直接跟 'neutral' 比較永遠不相等，被誤判成「非中性攻擊」而錯誤
  // 套用 elementResistPct 減免——先過 normalizeElementAlias 轉成英文枚舉再判斷是否為 neutral。
  const normalizedAttackerElement = attackerElement ? normalizeElementAlias(attackerElement) : undefined;
  const resistPct =
    normalizedAttackerElement && normalizedAttackerElement !== 'neutral'
      ? combineElementResistPct(actor.weaponProfile?.elementResistPct ?? 0, actor.equipmentEffects.elementResistPct)
      : 0;
  const resistMul = Math.max(0, 1 - resistPct / 100);
  // P6（CONTRACT §1）：damage_taken_pct 明講「在套用前 floor」——改 Math.round 為 floorInt，
  // 兩者在 dtMul<1（減傷，最常見的用法）時會算出不同的整數，floor 是契約指定的方向（見
  // formulas.ts floorInt 型別註解）。P7：resistMul 併入同一次 floor，跟 dtMul 是同一種「傷害減免
  // 乘數」性質，沒有理由分兩次取整。
  let dmg = Math.max(0, floorInt(rawDamage * dtMul * resistMul));
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
 *
 * P7（CONTRACT §3 書系武器「magic_skill_pct 乘在...heal 的 coef 上」）：新增可選參數
 * magicSkillPct（預設 0），只乘進 heal 的 coefficient，不影響 shield——契約原文只點名 heal，
 * shield 沒有另外的百分比加成可套。
 */
export function resolveSupportSkill(
  ctx: Ctx,
  casterId: string,
  skill: Skill,
  targetId: string | 'ALL' | 'ALL_ENEMIES' | null,
  magicSkillPct = 0,
): void {
  const caster = ctx.party.find((p) => p.id === casterId);
  if (!caster) return;
  const matk = Math.round(caster.stats.matk * (1 + activeStatSum(caster.activeEffects, 'matk_pct') / 100));
  const coefficient = skill.kind === 'heal' ? skill.coefficient * (1 + magicSkillPct / 100) : skill.coefficient;
  const amount = computeHeal(matk, coefficient, skill.flat);
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
 * P10（DORPG_P10 CONTRACT §3、WIRE「引擎」）：taunt 技能結算——`skill.taunt` 是已依目前等級展開
 * 的即時數值（跟 resolveBuffDebuff 讀 `skill.effect` 同一種「WIRE 已展開，engine 只管讀最終值」
 * 精神，不在這裡重新推導等級公式）：
 *   1. `tauntUntil = max(現值, now+durationMs)`——重複施放（例如冷卻轉完又按一次）取較晚的到期
 *      時間，不會被更短的新效果縮短守護時間。
 *   2. `damageTakenPct !== 0` 時套一筆 `damage_taken_pct` ActiveEffect（沿用既有 buff 疊加規則
 *      ——applyStatusEffect 對同一顆技能重複施放是「刷新」不是「疊加」，見 effects.ts）；hk_c1
 *      挑釁的 damageTakenPct 恆為 0，這裡天然是 no-op。
 *   3. `retarget=true` 時（挑釁 hk_c1）把所有存活敵人（含 windup 中蓄力鎖定別人的）的
 *      `ctx.enemyTargets` 立刻改成施放者，並推 taunt 事件（浮字「挑釁！」）；`retarget=false`
 *      （守護姿態 hk_c3）只靠上面第 1 步寫入的 tauntUntil 影響「之後」的選目標
 *      （formulas.ts pickEnemyTarget），這裡不動 enemyTargets、不推事件（CONTRACT §3「不
 *      retarget 的只影響之後的選目標」）。
 * `skill.taunt` 缺欄位（理論上不會發生——dispatch/ai 只會對 kind='taunt' 的技能呼叫這支，且
 * BACKEND／fixture 一律會展開這個欄位）時安全跳過，不拋例外，跟 resolveBuffDebuff 對缺 effect
 * 的防呆是同一個精神。
 */
export function resolveTaunt(ctx: Ctx, caster: PartyActor, skill: Skill): void {
  const taunt = skill.taunt;
  if (!taunt) {
    pushLog(ctx, `${caster.name} 施放的 ${skill.name} 缺少 taunt 展開資料，略過結算`);
    return;
  }
  caster.tauntUntil = Math.max(caster.tauntUntil, ctx.now + taunt.durationMs);
  if (taunt.damageTakenPct !== 0) {
    const effect: ActiveEffect = {
      stat: 'damage_taken_pct',
      value: taunt.damageTakenPct,
      expiresAt: ctx.now + taunt.durationMs,
      sourceSkillId: skill.id,
      kind: 'buff',
    };
    applyStatusEffect(caster, effect);
  }
  if (taunt.retarget) {
    const enemyIds: string[] = [];
    for (const enemy of ctx.enemies) {
      if (enemy.hp <= 0) continue;
      ctx.enemyTargets[enemy.id] = caster.id;
      enemyIds.push(enemy.id);
    }
    pushEvent(ctx, { kind: 'taunt', actorId: caster.id, enemyIds });
    pushLog(ctx, `${caster.name} 挑釁，敵人的攻擊全部轉向 ${caster.name}`);
  } else {
    pushLog(ctx, `${caster.name} 進入守護姿態，持續 ${taunt.durationMs}ms`);
  }
}

/**
 * casting 完成時的效果結算（tick.ts 呼叫）：damage 打 pending.targetId 的敵人（P5：支援 hits>1 多段
 * 命中與 target='allEnemies' 打全體）、heal/shield 走 resolveSupportSkill、buff/debuff 走
 * resolveBuffDebuff、taunt（P10）走 resolveTaunt。passive 不會進技能欄（後端已把 stat 算進玩家
 * stats，engine 完全不處理），special 在 dispatch 階段就已經被拒絕（implemented=false），兩者理論
 * 上都不會有 pendingCast 走到這裡——沒有對應分支，遇到的話單純什麼都不做（防呆，不拋例外）。
 *
 * P7（CONTRACT §3 書系武器「magic_skill_pct 乘在 dmg_type=magic 技能與 heal 的 coef 上」）：
 * damage 分支只在 dmgType==='magic' 時套 magicSkillPct（物理技能不受書系武器影響）；heal/shield
 * 分支把 weapon.magicSkillPct 轉給 resolveSupportSkill（shield 內部會忽略，見該函式型別註解）。
 * attackerWeapon 一律傳 `actor.weaponProfile ?? NEUTRAL_WEAPON_PROFILE`，讓 sizeBonus/element
 * 覆蓋規則（見 resolveAttackOrDamageSkill）對技能傷害跟普攻一視同仁——武器「打大型怪比較痛」是
 * 裝備本身的物理特性，不分是揮出去的是普攻還是技能。
 */
export function resolveCastEffect(ctx: Ctx, actor: PartyActor, skill: Skill, pending: PendingCast): void {
  const weapon = actor.weaponProfile ?? NEUTRAL_WEAPON_PROFILE;
  if (skill.kind === 'damage') {
    const hits = skill.hits ?? 1;
    const dmgType = skill.dmgType ?? 'physical';
    const magicMul = dmgType === 'magic' ? 1 + weapon.magicSkillPct / 100 : 1;
    const coefficient = skill.coefficient * magicMul;
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
          coefficient,
          flat: skill.flat,
          weapon: skill.weapon,
          targetEnemyId: tId,
          chargeMul: 1,
          charged: false,
          attackerRating: actor.rating,
          element: skill.element,
          dmgType,
          attackerWeapon: weapon,
        });
      }
    }
    return;
  }
  if (skill.kind === 'heal' || skill.kind === 'shield') {
    resolveSupportSkill(ctx, actor.id, skill, pending.targetId, weapon.magicSkillPct);
    return;
  }
  if (skill.kind === 'taunt') {
    resolveTaunt(ctx, actor, skill);
    return;
  }
  if (skill.kind === 'buff' || skill.kind === 'debuff') {
    resolveBuffDebuff(ctx, actor.id, skill, pending);
  }
}
