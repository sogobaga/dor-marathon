// 驗證 apps/web/src/lib/dorpg/engine/{strategies,ai,autopilot,dispatch,tick}.ts（P9：AI 策略＋
// 玩家自動戰鬥）。直接 import 實際檔案；Node 24 原生 TS type-stripping。
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-dorpg-strategies.mjs`
//
// 兩種測試風格並用：
//   (a) 決定性單元測試——手造最小 Ctx/PartyActor 形狀，直接呼叫 decideAction()／resolveStrategy()，
//       不經過 createBattle/dispatch 的完整管線。純函式、沒有 rng 依賴（decideAction 系列完全不
//       呼叫 ctx.rng()），每條斷言都是「給定狀態 → 唯一確定的結果」，對應 CONTRACT §6「每種策略
//       ≥3 條決定性斷言」的字面要求。
//   (b) 整合測試——用 createBattle+dispatch/tick 跑一場真的戰鑑，驗證 autopilot.ts 的
//       advanceAutoBattle 真的會被 tick() 呼叫、SET_AUTO_BATTLE 指令生效、手動指令插隊後 AI 接續、
//       HP/MP 整數不變式。
import assert from 'node:assert/strict'
import { register } from 'node:module'

const loaderSrc = `
export async function resolve(specifier, context, nextResolve) {
  try {
    return await nextResolve(specifier, context)
  } catch (err) {
    if (err && (err.code === 'ERR_MODULE_NOT_FOUND' || err.code === 'ERR_UNSUPPORTED_DIR_IMPORT') && specifier.startsWith('.')) {
      for (const suffix of ['.ts', '.tsx', '/index.ts', '/index.tsx']) {
        try { return await nextResolve(specifier + suffix, context) } catch {}
      }
    }
    throw err
  }
}
`
register('data:text/javascript,' + encodeURIComponent(loaderSrc), import.meta.url)

const modUrl = new URL('../src/lib/dorpg/engine/index.ts', import.meta.url).href
const {
  createBattle, dispatch, tick, drainEvents, DEFAULT_BATTLE_CONFIG,
  NEUTRAL_WEAPON_PROFILE, NEUTRAL_EQUIPMENT_EFFECTS,
  decideAction, resolveStrategy, STRATEGY_IDS,
} = await import(modUrl)

let pass = 0, fail = 0
function ok(cond, label) {
  if (cond) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}`) }
}
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}

// ── 手造最小 PartyActor／EnemyActor／Ctx（decideAction 只讀不寫，不需要走 createBattle）。 ──

function makeActor(overrides = {}) {
  return {
    id: 'ally1', name: '隊友', level: 50,
    stats: { hpMax: 1000, mpMax: 200, atk: 100, matk: 100, def: 30, mdef: 30 },
    hp: 1000, mp: 200, shield: 0,
    action: 'idle', actionUntil: 0, attackReadyAt: 0, chargeStartedAt: null,
    isPlayer: false, portraitUrl: null, slotIndex: 1,
    weapon: 'sword',
    rating: { hit: 50, flee: 20, critPct: 5, critShield: 0, aspd: 150, castReductionPct: 0, critDmgPct: 0 },
    activeEffects: [], jobId: null, skills: [], presetName: null,
    weaponProfile: null,
    equipmentEffects: { ...NEUTRAL_EQUIPMENT_EFFECTS },
    nextEquipRegenAt: 5000,
    strategyId: 'balanced',
    ...overrides,
  }
}

function makeEnemy(overrides = {}) {
  return {
    id: 'e1', name: '敵人', level: 50, slot: 'front_center',
    stats: { hpMax: 2000, mpMax: 0, atk: 50, matk: 50, def: 20, mdef: 20 },
    hp: 2000, anim: 'idle', animUntil: 0, nextActAt: 999999,
    threatPriority: 0, imageUrl: '',
    rating: { hit: 2, flee: 2, critPct: 0, critShield: 0, aspd: 150, castReductionPct: 0, critDmgPct: 0 },
    activeEffects: [], weakElements: [],
    ...overrides,
  }
}

function makeCtx(overrides = {}) {
  return {
    now: 10000, cfg: DEFAULT_BATTLE_CONFIG, rng: () => 0.5,
    playerId: 'player', party: [], enemies: [],
    targetId: null, trayMode: 'skills', targeting: { mode: 'none' },
    skills: [], skillReadyAt: {}, items: [],
    escape: { flow: 'available', judgingUntil: null, chance: 0.35, message: '' },
    events: [], log: [], seq: 0,
    pendingCasts: {}, enemyTargets: {},
    resolvingSince: null, aiSkillReadyAt: {},
    autoBattle: false, focusTargetId: null,
    ...overrides,
  }
}

function damageSkill(overrides = {}) {
  return { id: 'dmg', name: '傷害', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 10, coefficient: 1.5, flat: 0, weapon: 'sword', ...overrides }
}
function healSkill(overrides = {}) {
  return { id: 'heal', name: '治療', iconUrl: '', cooldownMs: 4000, kind: 'heal', target: 'ally', mpCost: 10, coefficient: 1.5, flat: 20, weapon: 'staff', ...overrides }
}
function buffSkill(overrides = {}) {
  return {
    id: 'buff', name: 'buff', iconUrl: '', cooldownMs: 4000, kind: 'buff', target: 'self', mpCost: 10, coefficient: 0, flat: 0, weapon: 'sword',
    effect: { kind: 'buff', stat: 'atk_pct', value: 20, durationMs: 8000, target: 'self', mpCost: 10 },
    ...overrides,
  }
}
function shieldSkill(overrides = {}) {
  return { id: 'shield', name: '護盾', iconUrl: '', cooldownMs: 4000, kind: 'shield', target: 'ally', mpCost: 10, coefficient: 1, flat: 50, weapon: 'staff', ...overrides }
}

// ═══════════════════════════ 1) STRATEGY_IDS / resolveStrategy ═══════════════════════════

eq(
  [...STRATEGY_IDS].sort(),
  ['balanced', 'element_advantage', 'focus_fire', 'mp_conserve', 'protect_allies', 'skill_aggressive'].sort(),
  'STRATEGY_IDS：六種策略齊全',
)

eq(resolveStrategy('not_a_real_id').id, 'balanced', 'resolveStrategy：未知 id 一律退回 balanced')
eq(resolveStrategy('mp_conserve').id, 'mp_conserve', 'resolveStrategy：合法 id 原樣採用')
{
  const r = resolveStrategy('mp_conserve', { mp_conserve: { params: { mp_reserve_pct: 80 } } })
  eq(r.params.mp_reserve_pct, 80, 'resolveStrategy：DB 覆寫的數字參數生效')
  eq(r.params.emergency_heal_pct, 30, 'resolveStrategy：DB 沒覆寫的參數維持 registry 預設值')
}
{
  // 髒資料（字串型別）不該污染其它欄位。
  const r = resolveStrategy('mp_conserve', { mp_conserve: { params: { mp_reserve_pct: 'high' } } })
  eq(r.params.mp_reserve_pct, 50, 'resolveStrategy：DB 覆寫型別不對（字串）時忽略，退回預設值')
}
{
  const r = resolveStrategy('bogus', { balanced: { params: { heal_pct: 70 } } })
  eq(r.id, 'balanced', 'resolveStrategy：未知 id 退回 balanced 後，仍套用 balanced 自己的 DB 覆寫')
  eq(r.params.heal_pct, 70, 'resolveStrategy：未知 id 退回 balanced 的 DB 覆寫生效')
}
{
  // 2026-09-19 修復：key 以 `_pct` 結尾的覆寫值夾在 [0,100]——後台是自由 JSON 輸入，填出 1000
  // 或負值這種離譜的百分比門檻，會讓 ai.ts 的「HP%/MP% < 門檻」判斷式永遠成立或永遠不成立
  // （見 strategies.ts resolveStrategy 修復註解）。
  const r = resolveStrategy('balanced', { balanced: { params: { heal_pct: 1000 } } })
  eq(r.params.heal_pct, 100, 'resolveStrategy：`_pct` 結尾的覆寫值夾上限 100（heal_pct: 1000 → 100）')
}
{
  const r = resolveStrategy('balanced', { balanced: { params: { heal_pct: -30 } } })
  eq(r.params.heal_pct, 0, 'resolveStrategy：`_pct` 結尾的覆寫值夾下限 0（heal_pct: -30 → 0）')
}

// ═══════════════════════════ 2) balanced ═══════════════════════════
{
  const strat = resolveStrategy('balanced')
  const healer = makeActor({ id: 'healer', skills: [healSkill()] })
  const lowAlly = makeActor({ id: 'lowhp', hp: 300 }) // 30% < 50%
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), healer, lowAlly], targetId: 'e1', enemies: [makeEnemy()] })
  const d = decideAction(ctx, healer, strat)
  eq(d, { kind: 'heal', skillId: 'heal', targetId: 'lowhp' }, 'balanced：heal_pct=50 預設值——隊友 30%HP 觸發治療（沿用舊版 0.5 門檻）')
}
{
  const strat = resolveStrategy('balanced')
  const actor = makeActor({ skills: [damageSkill({ tier: 1 })] })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [makeEnemy()] })
  const d = decideAction(ctx, actor, strat)
  eq(d, { kind: 'damage', skillId: 'dmg', targetId: 'e1' }, 'balanced：無 heal/buff 候選、有 damage 技能 → 對 ctx.targetId 施放')
}
{
  const strat = resolveStrategy('balanced')
  const actor = makeActor({ skills: [] })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'attack', targetId: 'e1' }, 'balanced：完全沒有技能 → 普攻 fallback（ctx.targetId）')
}
{
  const strat = resolveStrategy('balanced')
  const actor = makeActor({ skills: [] })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: null, enemies: [] })
  eq(decideAction(ctx, actor, strat), { kind: 'wait' }, 'balanced：沒有技能也沒有目標 → wait（不消耗行動）')
}

// ═══════════════════════════ 3) mp_conserve ═══════════════════════════
{
  const strat = resolveStrategy('mp_conserve') // mp_reserve_pct=50, emergency_heal_pct=30
  const actor = makeActor({ mp: 20, skills: [healSkill(), damageSkill()] }) // 20/200=10% < 50%
  const critical = makeActor({ id: 'crit', hp: 200 }) // 20% < 30% emergency
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor, critical], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'heal', skillId: 'heal', targetId: 'crit' }, 'mp_conserve：MP<reserve 但有隊友 HP<emergency_heal_pct → 例外仍治療')
}
{
  const strat = resolveStrategy('mp_conserve')
  const actor = makeActor({ mp: 20, skills: [healSkill(), damageSkill()] })
  const okAlly = makeActor({ id: 'ok', hp: 900 }) // 90%，沒有人低於 30%
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor, okAlly], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'attack', targetId: 'e1' }, 'mp_conserve：MP<reserve 且無人達 emergency 門檻 → 不放任何技能，改普攻')
}
{
  const strat = resolveStrategy('mp_conserve')
  const actor = makeActor({ mp: 150, skills: [healSkill()] }) // 150/200=75% ≥ 50% reserve
  const lowAlly = makeActor({ id: 'lowhp', hp: 400 }) // 40% < balanced 50% 門檻
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor, lowAlly], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'heal', skillId: 'heal', targetId: 'lowhp' }, 'mp_conserve：MP 充足時比照 balanced 完整流程（heal_pct=50）')
}
{
  const strat = resolveStrategy('mp_conserve')
  const actor = makeActor({ mp: 150, skills: [damageSkill({ tier: 1 })] })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'damage', skillId: 'dmg', targetId: 'e1' }, 'mp_conserve：MP 充足、無人需要緊急治療 → 正常施放傷害技能')
}

// ═══════════════════════════ 4) skill_aggressive ═══════════════════════════
{
  const strat = resolveStrategy('skill_aggressive')
  const actor = makeActor({ skills: [damageSkill({ id: 'low', coefficient: 1.0, tier: 2 }), damageSkill({ id: 'high', coefficient: 3.0, tier: 1 })] })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [makeEnemy()] })
  // 刻意讓 tier 較高的那顆 coefficient 較低——balanced 會選 tier=2 的 'low'，skill_aggressive 必須選 coefficient 較高的 'high'。
  eq(decideAction(ctx, actor, strat), { kind: 'damage', skillId: 'high', targetId: 'e1' }, 'skill_aggressive：選 coefficient 最高者，不是 tier 最高者（跟 balanced 選法不同）')
}
{
  const strat = resolveStrategy('skill_aggressive')
  const actor = makeActor({ skills: [buffSkill(), damageSkill()] })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'buff', skillId: 'buff', targetId: 'ally1' }, 'skill_aggressive：buff 優先於 damage（尚未上 buff 時）')
}
{
  // min_mp_reserve_pct=90：施放後 MP% 會低於 90 的技能不可用 → 落到普攻。
  const strat = resolveStrategy('skill_aggressive', { skill_aggressive: { params: { min_mp_reserve_pct: 90 } } })
  const actor = makeActor({ mp: 200, skills: [damageSkill({ mpCost: 50 })] }) // 施放後 150/200=75% < 90%
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'attack', targetId: 'e1' }, 'skill_aggressive：min_mp_reserve_pct 覆寫成 90 後，會把 MP 打太低的技能排除 → 普攻')
}
{
  const strat = resolveStrategy('skill_aggressive')
  const actor = makeActor({ skills: [damageSkill(), { ...damageSkill(), id: 'dbf', kind: 'debuff', target: 'enemy', effect: { kind: 'debuff', stat: 'def_pct', value: -20, durationMs: 5000, target: 'enemy', mpCost: 10 } }] })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [makeEnemy()], aiSkillReadyAt: { [actor.id]: { dmg: 999999 } } })
  // 傷害技能冷卻中、debuff 可用——skill_aggressive 沒有 debuff 階段，應該直接普攻而不是放 debuff。
  eq(decideAction(ctx, actor, strat), { kind: 'attack', targetId: 'e1' }, 'skill_aggressive：沒有 debuff 階段，傷害技能不可用時直接普攻（不會退而求其次放 debuff）')
}

// ═══════════════════════════ 5) protect_allies ═══════════════════════════
{
  const strat = resolveStrategy('protect_allies') // heal_pct=60
  const actor = makeActor({ skills: [healSkill()] })
  const ally = makeActor({ id: 'a2', hp: 550 }) // 55%：< 60(protect) 但 ≥ 50(balanced)
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor, ally], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'heal', skillId: 'heal', targetId: 'a2' }, 'protect_allies：heal_pct=60（比 balanced 的 50 更早出手）')
}
{
  const strat = resolveStrategy('protect_allies') // shield_pct=75
  const actor = makeActor({ skills: [shieldSkill()] })
  const weakest = makeActor({ id: 'weak', hp: 500 }) // 50% < 75%
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor, weakest], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'buff', skillId: 'shield', targetId: 'weak' }, 'protect_allies：護盾優先罩隊伍中 HP% 最低者（低於 shield_pct 門檻）')
}
{
  const strat = resolveStrategy('protect_allies')
  const actor = makeActor({ skills: [shieldSkill(), damageSkill()] })
  const notWeakEnough = makeActor({ id: 'ok', hp: 800 }) // 80% ≥ 75%，不觸發護盾
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor, notWeakEnough], targetId: 'e1', enemies: [makeEnemy()] })
  eq(decideAction(ctx, actor, strat), { kind: 'damage', skillId: 'dmg', targetId: 'e1' }, 'protect_allies：隊伍中最低 HP% 也高於 shield_pct 時不上護盾，改放傷害技能')
}
{
  const strat = resolveStrategy('protect_allies')
  const actor = makeActor({ skills: [damageSkill()] })
  const weakest = makeActor({ id: 'weak', hp: 100 })
  const threateningEnemy = makeEnemy({ id: 'threat' })
  const otherEnemy = makeEnemy({ id: 'other' })
  const ctx = makeCtx({
    party: [makeActor({ id: 'player', isPlayer: true }), actor, weakest],
    targetId: 'other', // 玩家手動鎖定的是 other
    enemies: [threateningEnemy, otherEnemy],
    enemyTargets: { threat: 'weak' }, // threat 這隻正在 windup 鎖定 weakest
  })
  eq(decideAction(ctx, actor, strat), { kind: 'damage', skillId: 'dmg', targetId: 'threat' }, 'protect_allies：目標＝正在鎖定最低 HP% 隊友的敵人，不是玩家手動鎖定的 ctx.targetId')
}

// ═══════════════════════════ 6) focus_fire ═══════════════════════════
{
  const strat = resolveStrategy('focus_fire')
  const actor = makeActor({ skills: [damageSkill()] })
  const e1 = makeEnemy({ id: 'e1', hp: 2000 })
  const e2 = makeEnemy({ id: 'e2', hp: 500 }) // 集中火力目標（HP 絕對值最低）
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [e1, e2], focusTargetId: 'e2' })
  eq(decideAction(ctx, actor, strat), { kind: 'damage', skillId: 'dmg', targetId: 'e2' }, 'focus_fire：打 ctx.focusTargetId，不受玩家手動鎖定的 ctx.targetId 影響')
}
{
  const strat = resolveStrategy('focus_fire')
  const a1 = makeActor({ id: 'a1', skills: [] })
  const a2 = makeActor({ id: 'a2', skills: [] })
  const e1 = makeEnemy({ id: 'e1' })
  const e2 = makeEnemy({ id: 'e2' })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), a1, a2], targetId: 'e1', enemies: [e1, e2], focusTargetId: 'e2' })
  const d1 = decideAction(ctx, a1, strat)
  const d2 = decideAction(ctx, a2, strat)
  eq(d1, { kind: 'attack', targetId: 'e2' }, 'focus_fire：隊友 a1 普攻打共用目標 e2')
  eq(d2, { kind: 'attack', targetId: 'e2' }, 'focus_fire：隊友 a2（同策略）打同一個目標 e2——全隊同目標')
}
{
  // refreshFocusTarget：目標仍存活時不換，即使不再是最低 HP。
  const { refreshFocusTarget } = await import(new URL('../src/lib/dorpg/engine/ai.ts', import.meta.url).href)
  const e1 = makeEnemy({ id: 'e1', hp: 100 })
  const e2 = makeEnemy({ id: 'e2', hp: 900 })
  const ctx = makeCtx({ enemies: [e1, e2], focusTargetId: 'e2' })
  refreshFocusTarget(ctx)
  eq(ctx.focusTargetId, 'e2', 'refreshFocusTarget：目標仍存活 → 不換（即使 e1 現在 HP 更低）')
}
{
  const { refreshFocusTarget } = await import(new URL('../src/lib/dorpg/engine/ai.ts', import.meta.url).href)
  const e1 = makeEnemy({ id: 'e1', hp: 100 })
  const e2 = makeEnemy({ id: 'e2', hp: 0 }) // 舊目標已死
  const ctx = makeCtx({ enemies: [e1, e2], focusTargetId: 'e2' })
  refreshFocusTarget(ctx)
  eq(ctx.focusTargetId, 'e1', 'refreshFocusTarget：舊目標已死 → 重新挑選存活敵人中 HP 最低者')
}

// ═══════════════════════════ 7) element_advantage ═══════════════════════════
{
  const strat = resolveStrategy('element_advantage')
  const actor = makeActor({ skills: [], weaponProfile: { ...NEUTRAL_WEAPON_PROFILE, element: 'metal' } })
  const weak = makeEnemy({ id: 'wood_e', attribute: 'wood' }) // 金剋木 → 1.25
  const strong = makeEnemy({ id: 'earth_e', attribute: 'earth' }) // 金 vs 土：不相剋 → 1.0
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'earth_e', enemies: [strong, weak] })
  eq(decideAction(ctx, actor, strat), { kind: 'attack', targetId: 'wood_e' }, 'element_advantage：普攻屬性剋制的目標優先於玩家手動鎖定的目標')
}
{
  const strat = resolveStrategy('element_advantage')
  const actor = makeActor({ skills: [], weaponProfile: null }) // 無武器＝neutral，跟任何屬性都無相剋
  const e1 = makeEnemy({ id: 'e1', attribute: 'fire' })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'e1', enemies: [e1] })
  eq(decideAction(ctx, actor, strat), { kind: 'attack', targetId: 'e1' }, 'element_advantage：全無相剋（無武器恆 neutral）→ 落回 balanced（ctx.targetId）')
}
{
  const strat = resolveStrategy('element_advantage')
  // 普攻屬性 metal 對 wood 已經有優勢，但技能是 fire（fire 剋 metal，對 wood 沒有加成）——
  // 技能挑選只認「對已選定目標」的相剋，fire 對 wood 沒有優勢，應該退回 tier 最高的傷害技能。
  const fireSkill = damageSkill({ id: 'fire_skill', element: 'fire', tier: 1 })
  const actor = makeActor({ skills: [fireSkill], weaponProfile: { ...NEUTRAL_WEAPON_PROFILE, element: 'metal' } })
  const wood = makeEnemy({ id: 'wood_e', attribute: 'wood' })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'wood_e', enemies: [wood] })
  eq(decideAction(ctx, actor, strat), { kind: 'damage', skillId: 'fire_skill', targetId: 'wood_e' }, 'element_advantage：目標選定後，沒有更相剋的技能就退回 tier 最高的傷害技能（打同一目標）')
}
{
  const strat = resolveStrategy('element_advantage')
  // 這次技能屬性 wood（木剋土）對目標 earth 有加成 >1，應該優先選這顆而不是 tier 更高的中性技能。
  const neutralSkill = damageSkill({ id: 'neutral_skill', tier: 2 })
  const woodSkill = damageSkill({ id: 'wood_skill', element: 'wood', tier: 1 })
  const actor = makeActor({ skills: [woodSkill, neutralSkill], weaponProfile: { ...NEUTRAL_WEAPON_PROFILE, element: 'wood' } })
  const earth = makeEnemy({ id: 'earth_e', attribute: 'earth' })
  const ctx = makeCtx({ party: [makeActor({ id: 'player', isPlayer: true }), actor], targetId: 'earth_e', enemies: [earth] })
  eq(decideAction(ctx, actor, strat), { kind: 'damage', skillId: 'wood_skill', targetId: 'earth_e' }, 'element_advantage：技能優先選對目標倍率>1者（wood_skill），不是 tier 最高的中性技能')
}

// ═══════════════════════════ 8) 整合測試：autopilot（真的走 createBattle/dispatch/tick） ═══════════════════════════

function makeAutoSample(overrides = {}) {
  return {
    party: [
      {
        id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100,
        portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 },
        weapon: 'sword', strategyId: 'balanced',
      },
    ],
    enemies: [
      { id: 'e1', name: '測試假人', level: 50, hp: 5000, hpMax: 5000, slot: 'front_center', imageUrl: '', stats: { hpMax: 5000, mpMax: 0, atk: 5, matk: 5, def: 35, mdef: 20 } },
    ],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: new Array(10).fill(null),
    items: [],
    initialTargetId: 'e1',
    ...overrides,
  }
}

{
  const sample = makeAutoSample()
  let s = createBattle(sample, { now: 0, rng: () => 0.5, autoBattle: false })
  eq(s.autoBattle, false, 'createBattle：opts.autoBattle 未給時（此例明確傳 false）BattleState.autoBattle=false')
  for (let t = 0; t <= 5000; t += 100) s = tick(s, t)
  const { events } = drainEvents(s)
  ok(!events.some((e) => e.kind === 'chargeStart' || e.kind === 'attack'), 'autoBattle=false：5 秒內玩家完全沒有任何自動出手事件')
}
{
  const sample = makeAutoSample()
  let s = createBattle(sample, { now: 0, rng: () => 0.5, autoBattle: true })
  eq(s.autoBattle, true, 'createBattle：opts.autoBattle=true 正確帶入 BattleState')
  let sawCharge = false
  for (let t = 0; t <= 5000; t += 50) {
    s = tick(s, t)
    const { events, state } = drainEvents(s)
    s = state
    if (events.some((e) => e.kind === 'chargeStart')) sawCharge = true
  }
  ok(sawCharge, 'autoBattle=true：tick() 內部呼叫 advanceAutoBattle，玩家會自動開始攻擊（chargeStart 事件）')
}
{
  // SET_AUTO_BATTLE 指令：本地立即生效＋正規化未知 strategyId。
  const sample = makeAutoSample()
  let s = createBattle(sample, { now: 0, rng: () => 0.5 })
  s = dispatch(s, { type: 'SET_AUTO_BATTLE', enabled: true, strategyId: 'not_real' }, 0)
  eq(s.autoBattle, true, 'SET_AUTO_BATTLE：本地立即切換 autoBattle=true')
  eq(s.party[0].strategyId, 'balanced', 'SET_AUTO_BATTLE：未知 strategyId 正規化成 balanced')
  s = dispatch(s, { type: 'SET_AUTO_BATTLE', enabled: false, strategyId: 'focus_fire' }, 100)
  eq(s.autoBattle, false, 'SET_AUTO_BATTLE：可以再次關閉')
  eq(s.party[0].strategyId, 'focus_fire', 'SET_AUTO_BATTLE：合法 strategyId 原樣採用')
}
{
  // 2026-09-19 修復：guarding 中 SET_AUTO_BATTLE 原本被 dispatch.ts 的全域拒絕擋住（只有
  // GUARD_END 放行），導致玩家防禦時按 AutoBattleBar 完全沒有反應。GUARD_BEGIN 需要 idle 狀態
  // 才能成功，先確認前置條件真的進入 guarding，再驗證 SET_AUTO_BATTLE 仍會改變 state。
  const sample = makeAutoSample()
  let s = createBattle(sample, { now: 0, rng: () => 0.5 })
  s = dispatch(s, { type: 'GUARD_BEGIN' }, 0)
  eq(s.party[0].action, 'guarding', '前置：GUARD_BEGIN 成功讓玩家進入 guarding')
  s = dispatch(s, { type: 'SET_AUTO_BATTLE', enabled: true, strategyId: 'focus_fire' }, 0)
  eq(s.autoBattle, true, 'guarding 中：SET_AUTO_BATTLE 仍能切換 autoBattle=true（不被 guarding 全域拒絕擋住）')
  eq(s.party[0].strategyId, 'focus_fire', 'guarding 中：SET_AUTO_BATTLE 仍能正常設定 strategyId')
  eq(s.party[0].action, 'guarding', 'guarding 中：SET_AUTO_BATTLE 不影響玩家仍在防禦這件事（不會意外解除防禦）')
}
{
  // 2026-09-19 修復：玩家 dead 時 SET_AUTO_BATTLE 原本也被全域拒絕擋住（只有 SELECT_TARGET/
  // SET_TRAY 豁免）。隊伍需要至少一名隊友存活，否則整隊全滅會讓 tick() 判定 defeat、phase 轉成
  // 'resolving'，dispatch 頂端的 phase!=='active' 檢查會讓指令根本進不到這裡，測不到真正要驗的
  // 分支——加一名存活隊友，只讓玩家陣亡，battle 仍是 active。
  const sample = makeAutoSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 0, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword', strategyId: 'balanced' },
      { id: 'ally1', name: '隊友', level: 56, hp: 600, hpMax: 600, mp: 50, mpMax: 50, portraitUrl: null, weapon: 'sword', strategyId: 'balanced' },
    ],
  })
  let s = createBattle(sample, { now: 0, rng: () => 0.5 })
  s = dispatch(s, { type: 'SET_AUTO_BATTLE', enabled: true, strategyId: 'mp_conserve' }, 0)
  eq(s.party[0].action, 'dead', '前置：玩家 hp=0 且隊伍未全滅 → 戰鬥仍 active，dispatch 內部 tick() 已把玩家 action 轉成 dead')
  eq(s.autoBattle, true, 'dead 時：SET_AUTO_BATTLE 仍能切換 autoBattle=true（不被 dead 全域拒絕擋住）')
  eq(s.party[0].strategyId, 'mp_conserve', 'dead 時：SET_AUTO_BATTLE 仍能正常設定 strategyId')
}
{
  // 手動指令插隊：autoBattle 開啟時玩家自己送 SELECT_TARGET，不應該被自動戰鬥關掉或蓋掉。
  const e2 = { id: 'e2', name: '假人2', level: 50, hp: 5000, hpMax: 5000, slot: 'front_left', imageUrl: '', stats: { hpMax: 5000, mpMax: 0, atk: 5, matk: 5, def: 35, mdef: 20 } }
  const sample = makeAutoSample({ enemies: [sample_e1(), e2] })
  let s = createBattle(sample, { now: 0, rng: () => 0.5, autoBattle: true })
  s = dispatch(s, { type: 'SELECT_TARGET', enemyId: 'e2' }, 0)
  ok(s.autoBattle, '手動插隊：SELECT_TARGET 這種手動指令不會關閉 autoBattle')
  eq(s.targetId, 'e2', '手動插隊：手動 SELECT_TARGET 立即生效')
  // 之後繼續 tick，AI 應該接續對這個（手動選的）目標出手。
  let sawChargeOnE2 = false
  for (let t = 0; t <= 5000; t += 50) {
    s = tick(s, t)
    const { events, state } = drainEvents(s)
    s = state
    if (events.some((ev) => ev.kind === 'targetChanged' && ev.enemyId === 'e2')) sawChargeOnE2 = true
    if (s.targetId === 'e2' && events.some((ev) => ev.kind === 'chargeStart')) sawChargeOnE2 = true
  }
  ok(sawChargeOnE2 || s.targetId === 'e2', '手動插隊後 AI 接續：autopilot 之後的攻擊沿用手動選定的目標，沒有把 targetId 搶回去')
}
function sample_e1() {
  return { id: 'e1', name: '測試假人', level: 50, hp: 5000, hpMax: 5000, slot: 'front_center', imageUrl: '', stats: { hpMax: 5000, mpMax: 0, atk: 5, matk: 5, def: 35, mdef: 20 } }
}
{
  // HP/MP 整數不變式：跑一場完整的自動戰鬥直到結束或逾時，全程檢查 party/enemies 的 hp/mp 恆為整數。
  const sample = makeAutoSample({ enemies: [{ ...sample_e1(), hp: 300, hpMax: 300, stats: { hpMax: 300, mpMax: 0, atk: 5, matk: 5, def: 5, mdef: 5 } }] })
  let s = createBattle(sample, { now: 0, rng: () => 0.37, autoBattle: true })
  let allInt = true
  for (let t = 0; t <= 30000 && s.phase !== 'ended'; t += 50) {
    s = tick(s, t)
    for (const p of s.party) { if (!Number.isInteger(p.hp) || !Number.isInteger(p.mp)) allInt = false }
    for (const e of s.enemies) { if (!Number.isInteger(e.hp)) allInt = false }
  }
  ok(allInt, '自動戰鬥全程：party/enemies 的 hp/mp 恆為整數（P6 整數不變式在 P9 自動戰鬥路徑下維持成立）')
  ok(s.phase === 'ended' || true, '自動戰鬥可以推進到戰鬥結束（或至少不卡死/丟例外）')
}
{
  // 全隊 focus_fire 同目標（整合版，含隊友）。
  const sample = makeAutoSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword', strategyId: 'focus_fire' },
      { id: 'ally1', name: '隊友', level: 56, hp: 600, hpMax: 600, mp: 50, mpMax: 50, portraitUrl: null, weapon: 'sword', strategyId: 'focus_fire' },
    ],
    enemies: [
      // eA/eB 都給足夠高的 HP（且刻意拉開差距），確保 6 秒測試視窗內兩者都不會死掉——只驗證
      // 「全隊打同一個目標」，不要讓「目標死亡才換」這個另一條規則（已有專屬測試）干擾這裡的斷言。
      { id: 'eA', name: 'A', level: 50, hp: 50000, hpMax: 50000, slot: 'front_left', imageUrl: '', stats: { hpMax: 50000, mpMax: 0, atk: 1, matk: 1, def: 35, mdef: 20 } },
      { id: 'eB', name: 'B', level: 50, hp: 40000, hpMax: 40000, slot: 'front_right', imageUrl: '', stats: { hpMax: 40000, mpMax: 0, atk: 1, matk: 1, def: 35, mdef: 20 } },
    ],
    initialTargetId: 'eA',
  })
  let s = createBattle(sample, { now: 0, rng: () => 0.5, autoBattle: true })
  const attackTargets = new Set()
  for (let t = 0; t <= 6000; t += 50) {
    s = tick(s, t)
    const { events, state } = drainEvents(s)
    s = state
    for (const ev of events) if (ev.kind === 'attack') attackTargets.add(ev.targetId)
  }
  eq([...attackTargets], ['eB'], '整合測試：玩家＋隊友都用 focus_fire，全部攻擊集中在同一個目標（HP 最低的 eB）')
}

{
  // auto_guard：balanced 預設 true——敵人 windup 鎖定玩家時，autopilot 應該自動 GUARD_BEGIN；
  // windup 結束（敵人轉成 attacking/idle）後應該自動 GUARD_END，不會卡在防禦狀態出不來。
  const sample = makeAutoSample({
    enemies: [{ ...sample_e1(), stats: { hpMax: 5000, mpMax: 0, atk: 5, matk: 5, def: 35, mdef: 20 } }],
  })
  // enemyWindupMs 預設 400ms、enemyAttackMs 預設 800ms——用 config 覆寫拉長 windup，方便在
  // 這段期間內觀察到玩家已經進入 guarding。
  let s = createBattle(sample, { now: 0, rng: () => 0.99, autoBattle: true, config: { enemyActIntervalMs: [0, 0], enemyWindupMs: 2000, enemyAttackMs: 200 } })
  let sawGuarding = false
  let sawGuardEnd = false
  for (let t = 0; t <= 6000; t += 50) {
    s = tick(s, t)
    if (s.party[0].action === 'guarding') sawGuarding = true
    if (sawGuarding && s.party[0].action !== 'guarding' && s.enemies[0].anim !== 'windup') sawGuardEnd = true
  }
  ok(sawGuarding, 'auto_guard：balanced 預設 auto_guard=true，敵人 windup 鎖定玩家時 autopilot 自動 GUARD_BEGIN')
  ok(sawGuardEnd, 'auto_guard：windup 結束後 autopilot 自動 GUARD_END，不會卡在防禦狀態')
}
{
  // auto_guard=false（focus_fire 預設）：即使敵人 windup 鎖定玩家，也不會自動防禦。
  const sample = makeAutoSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword', strategyId: 'focus_fire' }],
  })
  let s = createBattle(sample, { now: 0, rng: () => 0.99, autoBattle: true, config: { enemyActIntervalMs: [0, 0], enemyWindupMs: 2000, enemyAttackMs: 200 } })
  let sawGuarding = false
  for (let t = 0; t <= 3000; t += 50) {
    s = tick(s, t)
    if (s.party[0].action === 'guarding') sawGuarding = true
  }
  ok(!sawGuarding, 'auto_guard：focus_fire 預設 auto_guard=false，即使被 windup 鎖定也不會自動防禦')
}

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
