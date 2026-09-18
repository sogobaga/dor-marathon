// 驗證 apps/web/src/lib/dorpg/engine/**（直接 import 實際檔案；Node 24 原生 TS type-stripping）。
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-dorpg-engine.mjs`
//
// engine 內部依契約切成 types/context/formulas/combat/ai/tick/dispatch 多個檔案，彼此用一般的
// extensionless 相對匯入（例如 `from './formulas'`）——這是 tsc（moduleResolution: bundler，
// 沒開 allowImportingTsExtensions）唯一能接受、Next.js webpack 打包也認得的寫法。但 Node 的 ESM
// resolver 對「.」開頭的相對匯入不會自動幫忙補副檔名（實測 extensionless 會直接 ERR_MODULE_NOT_FOUND），
// 只有 import type（型別，會被整段削掉不留執行期蹤跡）才不受影響。
// 折衷做法：只在這支驗證腳本裡註冊一個小型 ESM resolve hook——遇到相對匯入解析失敗時，依序補
// .ts/.tsx 副檔名重試——完全不用改 tsconfig.json 或把任何 import 路徑寫死成 .ts（那些檔案不在
// ENGINE 的可寫清單內）。data: URL 內嵌 loader 原始碼，不需要另外一個實體檔案。
// P6：新增載入 fixture.ts（見下方 buildFixtureSample/loadRefPlayerTable 匯入）——它內部
// `from './engine'` 匯入的是一個目錄（對應 engine/index.ts），純 ESM 不做目錄 index 解析會丟
// ERR_UNSUPPORTED_DIR_IMPORT，補上跟 verify-dorpg-fromapi.mjs 同款的 /index.ts 候選重試。
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
  createBattle, dispatch, tick, drainEvents, chargeMultiplier, chargeRatio, computeDamage, computeHeal,
  pickInitialTarget, pickNextTarget, DEFAULT_BATTLE_CONFIG,
  // P2（暴擊／Miss／無效攻擊）新增匯出：
  missChance, critChance, elementMultiplier, computeRawDamage,
  // P3（AGI 攻速／DEX 詠唱縮減）新增匯出：
  attackCooldownFor, effectiveCastMs,
  // P5（職業／配點／技能）新增匯出：
  rollCritMultiplier, activeStatSum, effectiveRating, effectiveStats, damageTakenMultiplier,
  // P7（武器系統＋怪物體型/屬性）新增匯出：
  NEUTRAL_WEAPON_PROFILE,
} = await import(modUrl)

const typesUrl = new URL('../src/lib/dorpg/types.ts', import.meta.url).href
const { SKILL_SLOTS } = await import(typesUrl)

// P6（CONTRACT §2）：level 模式怪物縮放公式住在 fixture.ts（離線鏡像），不是 engine 本身——這裡
// 額外載入 fixture.ts 驗證 buildFixtureSample(..., {mode:'level', refTable}) 對 refPlayerTable
// 某幾列算出的怪物 hp/atk/def/mdef/matk 精確值（見下方 39/40 號區塊）。
const fixtureUrl = new URL('../src/lib/dorpg/fixture.ts', import.meta.url).href
const { buildFixtureSample, loadRefPlayerTable } = await import(fixtureUrl)

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

// ── 測試用最小 fixture：1 玩家 + 1 敵人，明確指定 stats（不吃預設推導），方便手算傷害。 ──
// enemyActIntervalMs/allyActIntervalMs 刻意設超長，讓大多數測試視窗內 AI 完全不出手，
// 只觀察「這個測試在乎的那個」指令/計時器結果，不被其他 AI 的 rng 消耗打亂。
function makeSample(overrides = {}) {
  return {
    party: [
      {
        id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100,
        portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 },
        weapon: 'sword',
      },
    ],
    enemies: [
      {
        id: 'e1', name: '測試假人', level: 50, hp: 5000, hpMax: 5000, slot: 'front_center',
        imageUrl: '', stats: { hpMax: 5000, mpMax: 0, atk: 50, matk: 50, def: 35, mdef: 20 },
      },
    ],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: [
      { id: 'slash', name: '斬擊', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.6, flat: 20, weapon: 'sword', castMs: 300 },
      { id: 'heal', name: '治療', iconUrl: '', cooldownMs: 8000, kind: 'heal', target: 'ally', mpCost: 20, coefficient: 2.0, flat: 80, weapon: 'staff', castMs: 500 },
      { id: 'shield', name: '護盾', iconUrl: '', cooldownMs: 10000, kind: 'shield', target: 'self', mpCost: 15, coefficient: 1.5, flat: 60, weapon: 'staff', castMs: 300 },
    ],
    items: [
      { id: 'hp_potion', name: '紅藥水', iconUrl: '', quantity: 2, kind: 'hp', amount: 300 },
      { id: 'revive', name: '復甦羽毛', iconUrl: '', quantity: 1, kind: 'revive', amount: 50 },
    ],
    initialTargetId: 'e1',
    ...overrides,
  }
}

const NEVER = [999999, 999999] // AI 排程區間設成永遠不到，等於這場戰鬥停用該 AI。
// P2（暴擊／Miss／無效攻擊）上線後，DEFAULT_BATTLE_CONFIG 的 baseMissPct/critRate 不再是 0，
// 會讓原本假設「恆定命中、恆不暴擊」的舊測試（1–30 號區塊）變成靠 rng 決定、不再確定。
// 這裡把舊測試共用的 FAR_CONFIG 一併把新機制的機率全部歸零，等價於機制上線前的 hitRate=1/
// critRate=0：missMaxPct=0 讓 missChance() 的 clamp(...) 不管算出什麼都封死在 0（不用去追每個
// 假人的 rating 細節），crit 同理靠 critRate/monsterCritPct/monsterCritShieldBase 三者歸零讓
// critChance() 恆為 0。已知全部 30 個舊區塊都是透過 `{ ...FAR_CONFIG, ... }` 或直接用 FAR_CONFIG
// 建立戰鬥（grep 過，沒有例外），所以這裡改一次就能讓舊斷言全部維持原本的行為與期望值，
// 不用逐一去改每個測試——新機制的測試（31 號之後）另外在各自區塊用自訂 config 覆寫這幾個欄位。
const FAR_CONFIG = {
  enemyActIntervalMs: NEVER,
  allyActIntervalMs: NEVER,
  baseMissPct: 0,
  missMinPct: 0,
  missMaxPct: 0,
  critRate: 0,
  monsterCritPct: 0,
  monsterCritShieldBase: 0,
}

function fixedRng(seq) {
  let i = 0
  return () => seq[i++ % seq.length]
}

// ── 1) 蓄氣倍率邊界 ──
{
  eq(chargeMultiplier(0, DEFAULT_BATTLE_CONFIG), 1, '蓄氣 0ms → 倍率 1')
  eq(chargeMultiplier(299, DEFAULT_BATTLE_CONFIG), 1, '蓄氣 <300ms → 倍率 1（未達門檻）')
  eq(chargeMultiplier(1500, DEFAULT_BATTLE_CONFIG), 2.5, '蓄氣恰 1500ms（滿蓄）→ 倍率 2.5')
  eq(chargeMultiplier(9999, DEFAULT_BATTLE_CONFIG), 2.5, '蓄氣遠超過滿蓄 → 封頂 2.5')
  ok(chargeRatio(300, DEFAULT_BATTLE_CONFIG) === 0 && chargeRatio(1500, DEFAULT_BATTLE_CONFIG) === 1, 'chargeRatio 0–1 邊界')
}

// ── 2) 傷害公式：規格範例 ATK135/DEF35/coef1/flat0 → 100；防禦者 → 40 ──
{
  eq(computeDamage(135, 1, 0, 1, 1, 35, false, DEFAULT_BATTLE_CONFIG), 100, '傷害公式規格範例 → 100')
  eq(computeDamage(135, 1, 0, 1, 1, 35, true, DEFAULT_BATTLE_CONFIG), 40, '同一擊被防禦 → ×0.4 = 40')
  eq(computeHeal(80, 2.0, 80), 240, '治療公式 floor(MATK×coef+flat) = floor(80*2+80) = 240')
}

// ── 3) 缺省數值推導：level=56 對齊規格測試角色；level=28 驗證線性縮放與四捨五入 ──
{
  const s56 = createBattle(makeSample({
    party: [{ id: 'p', name: '無數值', level: 56, hp: 1, hpMax: 500, mp: 1, mpMax: 200, portraitUrl: null }],
  }), { now: 0, config: FAR_CONFIG })
  eq(s56.party[0].stats, { hpMax: 500, mpMax: 200, atk: 135, matk: 80, def: 35, mdef: 28 }, '缺省隊員數值 level=56 對齊規格測試角色')

  const s28 = createBattle(makeSample({
    party: [{ id: 'p', name: '半級', level: 28, hp: 1, hpMax: 500, mp: 1, mpMax: 200, portraitUrl: null }],
  }), { now: 0, config: FAR_CONFIG })
  eq(s28.party[0].stats, { hpMax: 500, mpMax: 200, atk: 68, matk: 40, def: 18, mdef: 14 }, '缺省隊員數值 level=28 線性縮放四捨五入')

  const eDefault = createBattle(makeSample({
    enemies: [{ id: 'e1', name: '無數值敵人', level: 10, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '' }],
  }), { now: 0, config: FAR_CONFIG })
  eq(eDefault.enemies[0].stats, { hpMax: 999, mpMax: 0, atk: 40, matk: 40, def: 9, mdef: 7 }, '缺省敵人數值 level=10')
}

// ── 4) HOLD_CANCEL：不進 CD、不出傷害 ──
{
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG, rng: fixedRng([0]) })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'HOLD_CANCEL' }, 500)
  ok(s.party[0].action === 'idle', 'HOLD_CANCEL 後回到 idle')
  eq(s.party[0].attackReadyAt, 0, 'HOLD_CANCEL 不進攻擊 CD（attackReadyAt 不變）')
  eq(s.enemies[0].hp, 5000, 'HOLD_CANCEL 目標血量不變')
}

// ── 5) 完整蓄力攻擊：1500ms 放開，傷害吃到 2.5 倍蓄氣 ──
{
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG, rng: fixedRng([0]) })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 1500)
  // raw = floor(135*1*1*2.5) = 337；337-35=302
  eq(s.enemies[0].hp, 5000 - 302, '滿蓄攻擊傷害 = computeDamage(135,1,0,1,2.5,35) = 302')
  eq(s.party[0].attackReadyAt, 1500 + DEFAULT_BATTLE_CONFIG.attackCooldownMs, '攻擊後進入正常 CD')
  const atkEvent = s.events.find((e) => e.kind === 'attack')
  ok(!!atkEvent && atkEvent.charged === true, '滿蓄攻擊事件 charged=true')
}

// ── 6) 防禦互斥：guarding 中攻擊/技能/道具一律被拒 ──
{
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'GUARD_BEGIN' }, 0)
  ok(s.party[0].action === 'guarding', '進入防禦狀態')
  const beforeHp = s.enemies[0].hp
  const s2 = dispatch(s, { type: 'ATTACK_BEGIN' }, 100)
  ok(s2.party[0].action === 'guarding', '防禦中 ATTACK_BEGIN 被拒，仍是 guarding')
  const s3 = dispatch(s, { type: 'USE_ITEM', itemId: 'hp_potion' }, 100)
  eq(s3.targeting.mode, 'none', '防禦中 USE_ITEM 被拒，targeting 不會被打開')
  eq(s3.enemies[0].hp, beforeHp, '防禦中沒有任何指令造成傷害')
  const s4 = dispatch(s, { type: 'GUARD_END' }, 200)
  ok(s4.party[0].action === 'idle', 'GUARD_END 是防禦中唯一放行的指令')
}

// ── 7) 防禦降低敵人傷害：guarding 時被攻擊套用 ×0.4 ──
{
  let s = createBattle(makeSample(), { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] }, rng: fixedRng([0]) })
  s = dispatch(s, { type: 'GUARD_BEGIN' }, 0)
  s = tick(s, 0) // idle→windup（nextActAt=0 已到）
  const windupEv = s.events.find((e) => e.kind === 'enemyWindup')
  ok(!!windupEv, '敵人開始 windup')
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking，結算傷害
  const atkEv = s.events.find((e) => e.kind === 'enemyAttack')
  // computeDamage(50,1,0,1,1,35,guarded=true) = max(1,50-35)=15 → round(15*0.4)=6
  ok(!!atkEv && atkEv.guarded === true && atkEv.damage === 6, `防禦中被攻擊傷害套用 ×0.4（實際 ${atkEv && atkEv.damage}）`)
}

// ── 8) 技能冷卻與 MP ──
{
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG })
  const before = s.party[0].mp
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'slash' }, 0)
  eq(s.party[0].mp, before - 5, '施放技能立刻扣 MP')
  ok(s.party[0].action === 'casting', '施放技能進入 casting')
  const s2 = dispatch(s, { type: 'USE_SKILL', skillId: 'slash' }, 10)
  ok(s2.log.some((l) => l.includes('拒絕')), '施法中不能再開始新技能（非 idle）')
  s = tick(s, 300) // castMs=300 到，效果結算
  const dmgEv = s.events.find((e) => e.kind === 'attack' && e.actorId === 'player')
  // raw=floor(135*1.6+20)=floor(236)=236；236-35=201
  ok(!!dmgEv && dmgEv.damage === 201, `斬擊技能傷害 = floor(135*1.6+20)-35 = 201（實際 ${dmgEv && dmgEv.damage}）`)
  ok(s.party[0].action === 'recovering', '技能結算後進入 recovering')
  s = tick(s, 300 + DEFAULT_BATTLE_CONFIG.recoveryMs)
  const s3 = dispatch(s, { type: 'USE_SKILL', skillId: 'slash' }, 300 + DEFAULT_BATTLE_CONFIG.recoveryMs + 1)
  ok(s3.log.some((l) => l.includes('拒絕')) && s3.party[0].action !== 'casting', '技能冷卻中重複施放被拒')
}

// ── 9) 需要選隊友的技能：兩段式 targeting 流程 ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'ally', name: '小夥伴', level: 56, hp: 100, hpMax: 500, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 500, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 }, weapon: 'staff' },
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'heal' }, 0)
  eq(s.targeting, { mode: 'chooseAlly', skillId: 'heal' }, '無 targetId 的 ally 技能先進 chooseAlly')
  ok(s.party[0].action === 'idle', '進 targeting 前玩家還沒開始施法（可以 CANCEL_TARGETING）')
  const s2 = dispatch(s, { type: 'CANCEL_TARGETING' }, 10)
  eq(s2.targeting, { mode: 'none' }, 'CANCEL_TARGETING 清空選取')
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'heal', targetId: 'ally' }, 20)
  ok(s.party[0].action === 'casting', '帶 targetId 才真正開始施法')
  s = tick(s, 20 + 500)
  const healEv = s.events.find((e) => e.kind === 'heal')
  // floor(80*2+80)=240，封頂 hpMax=500，100+240=340
  eq(healEv && healEv.targetId, 'ally', '治療事件目標正確')
  eq(s.party[1].hp, 340, '治療量 floor(MATK*coef+flat) 套用在指定隊友身上')
}

// ── 10) 道具目標流程：USE_ITEM 選隊友、成功扣量、trayMode 回 skills；失敗保留 items ──
{
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'SET_TRAY', mode: 'items' }, 0)
  s = dispatch(s, { type: 'USE_ITEM', itemId: 'hp_potion' }, 10)
  eq(s.targeting, { mode: 'chooseAlly', itemId: 'hp_potion' }, '無 targetId 的道具先進 chooseAlly')
  s.party[0].hp = 500 // 手動扣血好驗證回復量
  s = dispatch(s, { type: 'USE_ITEM', itemId: 'hp_potion', targetId: 'player' }, 20)
  eq(s.party[0].hp, 800, '紅藥水回復量套用且封頂 hpMax')
  eq(s.items.find((i) => i.def.id === 'hp_potion').quantity, 1, '成功使用後數量 -1')
  eq(s.trayMode, 'skills', '成功使用後 trayMode 回到 skills')

  // 失敗案例：對滿血隊友用 hp 藥水（無效目標）
  const s2 = dispatch(s, { type: 'SET_TRAY', mode: 'items' }, 30)
  const s3 = dispatch(s2, { type: 'USE_ITEM', itemId: 'hp_potion', targetId: 'player' }, 40)
  eq(s3.items.find((i) => i.def.id === 'hp_potion').quantity, 1, '對滿血目標使用失敗，數量不變')
  eq(s3.trayMode, 'items', '失敗維持 items 模式')
}

// ── 11) 復甦：只對倒下的隊友有效，恢復到 hpMax 的百分比且 action 回 idle；對還活著的隊友使用則失敗 ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'ally', name: '倒下的夥伴', level: 56, hp: 0, hpMax: 500, mp: 0, mpMax: 100, portraitUrl: null, stats: { hpMax: 500, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 } },
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s.party[1].action = 'dead'
  const failOnPlayer = dispatch(s, { type: 'USE_ITEM', itemId: 'revive', targetId: 'player' }, 0)
  eq(failOnPlayer.items.find((i) => i.def.id === 'revive').quantity, 1, '對還活著的隊友用復甦羽毛失敗，數量不變')
  s = dispatch(s, { type: 'USE_ITEM', itemId: 'revive', targetId: 'ally' }, 0)
  eq(s.party[1].hp, 250, '復甦羽毛恢復到 hpMax 的 amount%（500×50%=250）')
  eq(s.party[1].action, 'idle', '復甦後 action 回到 idle')
  ok(s.events.some((e) => e.kind === 'actorRevive' && e.actorId === 'ally'), 'actorRevive 事件正確')
}

// ── 12) 敵人死亡後自動換目標 ──
{
  const sample = makeSample({
    enemies: [
      { id: 'e1', name: '弱怪', level: 1, hp: 1, hpMax: 1, slot: 'front_left', imageUrl: '', stats: { hpMax: 1, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 }, threatPriority: 5 },
      { id: 'e2', name: '強怪', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', stats: { hpMax: 999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 }, threatPriority: 1 },
    ],
    initialTargetId: 'e1',
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  ok(s.enemies.find((e) => e.id === 'e1').hp <= 0, 'e1 被打死')
  eq(s.targetId, 'e2', '目標死亡後自動換成僅存的 e2')
  const changedEv = s.events.find((e) => e.kind === 'targetChanged')
  ok(!!changedEv && changedEv.enemyId === 'e2', 'targetChanged 事件正確')
}

// ── 13) pickInitialTarget/pickNextTarget：最高 threatPriority、同序取槽位順序最小 ──
{
  const sample = makeSample({
    enemies: [
      { id: 'a', name: 'a', level: 1, hp: 10, hpMax: 10, slot: 'front_right', imageUrl: '', threatPriority: 3 },
      { id: 'b', name: 'b', level: 1, hp: 10, hpMax: 10, slot: 'rear_left', imageUrl: '', threatPriority: 3 },
      { id: 'c', name: 'c', level: 1, hp: 10, hpMax: 10, slot: 'front_center', imageUrl: '', threatPriority: 1 },
    ],
    initialTargetId: 'not_exist', // 逼 createBattle 走 pickInitialTarget 這條路
  })
  const s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  eq(s.targetId, 'b', '同 threatPriority 取槽位順序最小（rear_left 排在 front_right 前面）')
  eq(pickInitialTarget(s.enemies), 'b', 'pickInitialTarget 匯出函式同樣結果')
  eq(pickNextTarget(s), 'b', 'pickNextTarget 對同一個 state 給一致結果')
}

// ── 14) 逃跑：成功／失敗永久停用／BOSS 不可逃 ──
{
  // 成功：judging 到期時 rng() < chance(0.35)
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG, rng: fixedRng([0.1]) })
  s = dispatch(s, { type: 'TRY_ESCAPE' }, 0)
  ok(s.escape.flow === 'judging', 'TRY_ESCAPE 進入 judging')
  s = tick(s, DEFAULT_BATTLE_CONFIG.escapeJudgeMs)
  eq(s.phase, 'ended', '逃跑成功結束戰鬥')
  eq(s.outcome, 'escaped', '逃跑成功 outcome=escaped')

  // 失敗且永久停用：rng() >= chance
  let f = createBattle(makeSample(), { now: 0, config: FAR_CONFIG, rng: fixedRng([0.9]) })
  f = dispatch(f, { type: 'TRY_ESCAPE' }, 0)
  f = tick(f, DEFAULT_BATTLE_CONFIG.escapeJudgeMs)
  eq(f.escape.flow, 'failed', '逃跑失敗後 flow=failed')
  eq(f.phase, 'active', '逃跑失敗不結束戰鬥')
  const retry = dispatch(f, { type: 'TRY_ESCAPE' }, DEFAULT_BATTLE_CONFIG.escapeJudgeMs + 100)
  eq(retry.escape.flow, 'failed', '逃跑失敗後本場永久停用，重試仍是 failed')

  // BOSS 不可逃：canEscape=false 從一開始就 unavailable
  const boss = createBattle(makeSample({ enemies: [{ id: 'e1', name: 'BOSS', level: 50, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', canEscape: false }] }), { now: 0, config: FAR_CONFIG })
  eq(boss.escape.flow, 'unavailable', 'canEscape=false 的敵人讓逃跑一開始就 unavailable')
  const bossTry = dispatch(boss, { type: 'TRY_ESCAPE' }, 0)
  eq(bossTry.escape.flow, 'unavailable', 'unavailable 狀態下 TRY_ESCAPE 被拒')
}

// ── 15) 判定中倒下：取消判定但仍記失敗 ──
{
  const sample = makeSample({
    enemies: [{ id: 'e1', name: '重拳怪', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', stats: { hpMax: 999, mpMax: 0, atk: 9999, matk: 0, def: 0, mdef: 0 } }],
  })
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] }, rng: fixedRng([0]) })
  s = dispatch(s, { type: 'TRY_ESCAPE' }, 0)
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // idle→windup 在 now=0 已觸發（下一次 tick 才推進；這裡先跑一次讓 windup 開始）
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking，重拳直接打死玩家
  ok(s.party[0].hp <= 0, '玩家被打死')
  eq(s.escape.flow, 'failed', '判定中若玩家倒下，立刻標記逃跑失敗（不等 judging 計時器）')
  // 審查修復 #1：defeat 沒有動畫可等，先進 resolving、outcome 已經記錄，要等 resolveDelayMs 才 ended。
  eq(s.phase, 'resolving', '玩家倒下先進 resolving（不是立刻 ended）')
  eq(s.outcome, 'defeat', '倒下的結局是 defeat 而非 escaped（outcome 在 resolving 期間就已經記錄）')
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs + DEFAULT_BATTLE_CONFIG.resolveDelayMs)
  eq(s.phase, 'ended', 'resolveDelayMs 過後才真的 ended')
}

// ── 16) 勝利／戰敗／平手：都先進 resolving，victory 等敵人死亡動畫播完，defeat/draw 等 resolveDelayMs ──
{
  let win = createBattle(makeSample({ enemies: [{ id: 'e1', name: '待宰', level: 1, hp: 1, hpMax: 1, slot: 'front_center', imageUrl: '', stats: { hpMax: 1, mpMax: 0, atk: 0, matk: 0, def: 0, mdef: 0 } }] }), { now: 0, config: FAR_CONFIG })
  win = dispatch(win, { type: 'ATTACK_BEGIN' }, 0)
  win = dispatch(win, { type: 'ATTACK_RELEASE' }, 0)
  eq(win.phase, 'resolving', '敵人全滅先進 resolving，死亡動畫還沒播完（不會蓋掉 dying）')
  eq(win.outcome, 'victory', 'resolving 期間 outcome 已經是 victory')
  eq(win.enemies[0].anim, 'dying', '敵人此刻在播死亡動畫')
  ok(!win.events.some((e) => e.kind === 'ended'), 'resolving 期間還沒有 ended 事件')
  win = tick(win, DEFAULT_BATTLE_CONFIG.enemyDeathMs - 1)
  eq(win.phase, 'resolving', '死亡動畫沒播完，仍是 resolving')
  win = tick(win, DEFAULT_BATTLE_CONFIG.enemyDeathMs)
  eq(win.phase, 'ended', '死亡動畫（enemyDeathMs）播完後才 ended')
  eq(win.enemies[0].anim, 'removed', '敵人動畫已經是 removed')
  ok(win.events.some((e) => e.kind === 'ended' && e.outcome === 'victory'), 'ended 事件帶正確 outcome')

  let lose = createBattle(makeSample({ party: [{ id: 'player', name: '紙人', level: 1, hp: 0, hpMax: 100, mp: 0, mpMax: 0, portraitUrl: null }] }), { now: 0, config: FAR_CONFIG })
  lose = tick(lose, 0)
  eq(lose.phase, 'resolving', '玩家一開始就倒下先進 resolving')
  eq(lose.outcome, 'defeat', '玩家一開始就倒下→defeat')
  lose = tick(lose, DEFAULT_BATTLE_CONFIG.resolveDelayMs)
  eq(lose.phase, 'ended', 'defeat 沒有動畫可等，resolveDelayMs 過後直接 ended')

  let draw = createBattle(makeSample({
    party: [{ id: 'player', name: '紙人', level: 1, hp: 0, hpMax: 100, mp: 0, mpMax: 0, portraitUrl: null }],
    enemies: [{ id: 'e1', name: '同歸於盡', level: 1, hp: 0, hpMax: 1, slot: 'front_center', imageUrl: '' }],
  }), { now: 0, config: FAR_CONFIG })
  draw = tick(draw, 0)
  eq(draw.phase, 'resolving', '雙方同時倒下先進 resolving')
  eq(draw.outcome, 'draw', '雙方同時倒下→draw')
  draw = tick(draw, DEFAULT_BATTLE_CONFIG.resolveDelayMs)
  eq(draw.phase, 'ended', 'draw 也是比照 defeat 用 resolveDelayMs，不等敵人動畫')
}

// ── 17) resolving 期間指令一律拒絕（跟 ended 一樣），要等真正 ended 才會停止推進 ──
{
  let s = createBattle(makeSample({ enemies: [{ id: 'e1', name: '待宰', level: 1, hp: 1, hpMax: 1, slot: 'front_center', imageUrl: '', stats: { hpMax: 1, mpMax: 0, atk: 0, matk: 0, def: 0, mdef: 0 } }] }), { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const resolvingState = s
  eq(resolvingState.phase, 'resolving', '（前提）此刻是 resolving')
  // resolving 跟 ended 不同：tick 仍會推進死亡動畫，所以物件參照不會完全相等（每次都重新組裝），
  // 但指令本身一定被拒——用內容比對確認玩家沒有真的變成 guarding。
  const afterCmdWhileResolving = dispatch(s, { type: 'GUARD_BEGIN' }, 50)
  eq(afterCmdWhileResolving.phase, 'resolving', 'resolving 期間指令被拒，phase 仍是 resolving')
  eq(afterCmdWhileResolving.party[0].action, resolvingState.party[0].action, 'resolving 期間 GUARD_BEGIN 被拒，玩家 action 沒有變成 guarding')

  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyDeathMs) // 死亡動畫播完，真正轉 ended
  const endedState = s
  eq(endedState.phase, 'ended', '死亡動畫播完後才是真正的 ended')
  const afterCmd = dispatch(s, { type: 'GUARD_BEGIN' }, 100)
  ok(afterCmd === endedState, 'ended 後 dispatch 直接原樣回傳（同一個物件參照）')
  const afterTick = tick(s, 100)
  ok(afterTick === endedState, 'ended 後 tick 直接原樣回傳，不再推進')
}

// ── 18) seq 單調遞增（只在真的發生事件時前進，drainEvents 不影響 seq） ──
{
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG })
  const seq0 = s.seq
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  const seq1 = s.seq
  ok(seq1 > seq0, 'ATTACK_BEGIN 產生事件，seq 前進')
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const seq2 = s.seq
  ok(seq2 > seq1, 'ATTACK_RELEASE 產生事件，seq 繼續前進')
  const drained = drainEvents(s)
  eq(drained.state.seq, seq2, 'drainEvents 不改變 seq')
  eq(drained.state.events, [], 'drainEvents 清空 events')
  eq(drained.events.length > 0, true, 'drainEvents 回傳先前累積的事件')
}

// P6（CONTRACT §3.2）：隊友 AI 改吃自己的 PartyMember.skills（不再借用 BattleState.skills），
// 下面的 HEAL_SKILL 對應 makeSample() 頂層 skills 陣列裡同一顆 'heal' 定義，供 19/29/30 號測試
// 的 healer 掛在自己身上。
const HEAL_SKILL = { id: 'heal', name: '治療', iconUrl: '', cooldownMs: 8000, kind: 'heal', target: 'ally', mpCost: 20, coefficient: 2.0, flat: 80, weapon: 'staff', castMs: 500 }

// ── 19) 隊友 AI：普攻與依血量改用治療（P6：改吃 healer 自己的 skills，見上方 HEAL_SKILL） ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'healer', name: '輔助', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 50, matk: 80, def: 20, mdef: 20 }, weapon: 'staff', skills: [HEAL_SKILL] },
    ],
  })
  // 普攻：allyActIntervalMs 設 [0,0] 讓 healer 一開始就能行動；血量都健康所以走普攻分支。
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s = tick(s, 0)
  const allyAtk = s.events.find((e) => e.kind === 'attack' && e.actorId === 'healer')
  ok(!!allyAtk, '隊友 AI 對目前目標自動普攻')
  eq(allyAtk.charged, false, '隊友普攻不蓄氣')

  // 治療分支（P6 CONTRACT §3.2 tier①：<50% 觸發）：把 player 打到 <50% hp，healer 改放 heal。
  let s2 = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s2.party[0].hp = 100 // 800 的 12.5%，< 50%
  s2 = tick(s2, 0)
  const healEv = s2.events.find((e) => e.kind === 'heal' && e.actorId === 'healer')
  ok(!!healEv && healEv.targetId === 'player', '隊友血量 <50% 時，有 heal 技能的隊友改用 heal（P6 五段優先序①）')
}

// ── 20) 敵人 AI 完整時序：windup→attacking→idle 排程下一次 ──
{
  let s = createBattle(makeSample({ enemies: [{ id: 'e1', name: '假人', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', stats: { hpMax: 999, mpMax: 0, atk: 10, matk: 10, def: 0, mdef: 0 } }] }), {
    now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] }, rng: fixedRng([0]),
  })
  eq(s.enemies[0].anim, 'idle', '創建時敵人是 idle')
  s = tick(s, 0)
  eq(s.enemies[0].anim, 'windup', 'nextActAt 到達→windup')
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs - 1)
  eq(s.enemies[0].anim, 'windup', 'windup 時間未到，仍在 windup')
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs)
  eq(s.enemies[0].anim, 'attacking', 'windup 時間到→attacking，同時結算傷害')
  ok(s.events.some((e) => e.kind === 'enemyAttack'), 'attacking 轉場當下有 enemyAttack 事件')
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs + DEFAULT_BATTLE_CONFIG.enemyAttackMs)
  eq(s.enemies[0].anim, 'idle', 'attacking 時間到→回 idle')
  // enemyActIntervalMs 這裡設 [0,0] 只是為了讓 idle→windup 立刻觸發；回 idle 當下排下一次行動時間
  // 一樣是 now + rand([0,0]) = now，所以是「等於」而不是「大於」（跟隨機區間本身無關，純粹是排程邏輯）。
  eq(s.enemies[0].nextActAt, DEFAULT_BATTLE_CONFIG.enemyWindupMs + DEFAULT_BATTLE_CONFIG.enemyAttackMs, '排好下一次行動時間 = now + rand(enemyActIntervalMs)')
}

// ── 21) hitReaction 不中斷 windup 計時 ──
{
  // hp=200，玩家單擊 def=0 造成 135 傷害，一擊不會死（需要兩擊），方便觀察「受擊但存活」的 hitReaction。
  let s = createBattle(makeSample({ enemies: [{ id: 'e1', name: '假人', level: 1, hp: 200, hpMax: 200, slot: 'front_center', imageUrl: '', stats: { hpMax: 200, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } }] }), {
    now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [1000, 1000] },
  })
  // 模擬敵人已經在 windup 中，離 windup 結束還很久（2000ms），確保受擊當下絕對還沒到期。
  s.enemies[0].anim = 'windup'
  s.enemies[0].animUntil = 2000
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 500)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 500) // holdMs=0，傷害=135，200-135=65，存活
  eq(s.enemies[0].hp, 65, '第一擊未致命，敵人存活')
  eq(s.enemies[0].anim, 'hitReaction', '被攻擊後顯示 hitReaction')
  eq(s.enemies[0].resumeAnimUntil, 2000, 'hitReaction 底下仍記著原本 windup 的到期時間（2000ms），沒被打斷')

  s = tick(s, 500 + DEFAULT_BATTLE_CONFIG.enemyHitMs) // hitReaction(enemyHitMs=600) 結束於 1100，windup 到期是 2000，尚未到
  eq(s.enemies[0].anim, 'windup', 'hitReaction 結束後恢復成原本的 windup（尚未到期，不會提早跳去 attacking）')
  eq(s.enemies[0].animUntil, 2000, 'windup 到期時間維持原本的 2000ms，沒有因為受擊被重算')

  s = tick(s, 2000) // 現在真的到期，才轉場成 attacking——證明計時器全程沒被受擊打斷過。
  eq(s.enemies[0].anim, 'attacking', 'windup 到期時間一到，準時轉場成 attacking')
}

// ── 22) 敵人死亡：dying 播完 enemyDeathMs 後轉 removed。留一隻高血量的 e2 陪打，
//        避免打死 e1 後戰鬥立刻 ended（ended 後 tick 不再推進，看不到 dying→removed 的過程）。 ──
{
  let s = createBattle(makeSample({
    enemies: [
      { id: 'e1', name: '待宰', level: 1, hp: 100, hpMax: 100, slot: 'front_center', imageUrl: '', stats: { hpMax: 100, mpMax: 0, atk: 0, matk: 0, def: 0, mdef: 0 } },
      { id: 'e2', name: '陪打', level: 1, hp: 99999, hpMax: 99999, slot: 'front_left', imageUrl: '', stats: { hpMax: 99999, mpMax: 0, atk: 0, matk: 0, def: 0, mdef: 0 } },
    ],
    initialTargetId: 'e1',
  }), { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  eq(s.phase, 'active', 'e2 還活著，戰鬥不會 ended')
  const e1 = () => s.enemies.find((e) => e.id === 'e1')
  eq(e1().hp, 0, '一擊打死 e1（135 傷害 > 100 血量）')
  eq(e1().anim, 'dying', '死亡當下進 dying')
  ok(s.events.some((e) => e.kind === 'enemyDeath'), '死亡當下立刻推 enemyDeath 事件（不等動畫播完）')
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyDeathMs - 1)
  eq(e1().anim, 'dying', 'enemyDeathMs 未到，仍是 dying')
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyDeathMs)
  eq(e1().anim, 'removed', 'dying 播完（enemyDeathMs）→removed')
}

// ── 23) 攻擊 CD 中仍可 GUARD_BEGIN / USE_SKILL / USE_ITEM（冷卻不是行為鎖，只有 action 才是） ──
{
  // recoveryMs(400) < attackCooldownMs(1500)：攻擊後 400ms 就回 idle，但 attackReadyAt 還要等到 1500ms，
  // 這段「idle 但攻擊還在冷卻」的窗口正是本測試要驗證的情境。
  function attackThenIdleWhileOnCooldown() {
    let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG })
    s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
    s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
    s = tick(s, DEFAULT_BATTLE_CONFIG.recoveryMs) // 回 idle
    ok(s.party[0].action === 'idle', '（前提）recoveryMs 過後回 idle')
    ok(s.party[0].attackReadyAt > DEFAULT_BATTLE_CONFIG.recoveryMs, '（前提）攻擊仍在冷卻中')
    return s
  }
  {
    const s = dispatch(attackThenIdleWhileOnCooldown(), { type: 'GUARD_BEGIN' }, DEFAULT_BATTLE_CONFIG.recoveryMs + 1)
    eq(s.party[0].action, 'guarding', '攻擊冷卻中仍可以 GUARD_BEGIN（冷卻不是行為鎖）')
  }
  {
    const s = dispatch(attackThenIdleWhileOnCooldown(), { type: 'USE_SKILL', skillId: 'slash' }, DEFAULT_BATTLE_CONFIG.recoveryMs + 1)
    eq(s.party[0].action, 'casting', '攻擊冷卻中仍可以 USE_SKILL')
  }
  {
    const s = dispatch(attackThenIdleWhileOnCooldown(), { type: 'USE_ITEM', itemId: 'hp_potion', targetId: 'player' }, DEFAULT_BATTLE_CONFIG.recoveryMs + 1)
    eq(s.items.find((i) => i.def.id === 'hp_potion').quantity, 1, '攻擊冷卻中仍可以 USE_ITEM（成功扣量）')
  }
}

// ── 24) USE_SKILL：MP 不足被拒 ──
{
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG })
  s.party[0].mp = 2 // slash 的 mpCost=5，不夠
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'slash' }, 0)
  eq(s.party[0].mp, 2, 'MP 不足時 USE_SKILL 被拒，MP 不會被扣')
  eq(s.party[0].action, 'idle', 'MP 不足時不會進入 casting')
  ok(s.log.some((l) => l.includes('拒絕')), '有留下拒絕紀錄')
}

// ── 25) target 'self'（shield）與 'allAllies' 分支 ──
{
  // 'self'：shield 技能不需要 targeting，直接 commit；套用在施法者自己身上。
  let s = createBattle(makeSample(), { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'shield' }, 0) // target:'self'，不用帶 targetId
  eq(s.party[0].action, 'casting', 'self 技能不需要 targeting，直接開始施法')
  s = tick(s, 300) // shield 的 castMs=300
  // floor(matk80×1.5+60) = floor(180) = 180
  eq(s.party[0].shield, 180, 'self 技能結算在施法者自己身上：floor(MATK×coef+flat)=180')
  ok(s.events.some((e) => e.kind === 'shield' && e.actorId === 'player' && e.targetId === 'player'), 'shield 事件 actorId/targetId 都是施法者自己')
}
{
  // 'allAllies'：對全體存活隊員各套一次效果；用自訂技能列驗證（樣本原本的 8 格沒有 allAllies 技能）。
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'ally1', name: '夥伴一', level: 56, hp: 100, hpMax: 500, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 500, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 } },
      { id: 'ally2', name: '夥伴二', level: 56, hp: 200, hpMax: 400, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 400, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 } },
    ],
    skills: [
      { id: 'groupheal', name: 'group', iconUrl: '', cooldownMs: 15000, kind: 'heal', target: 'allAllies', mpCost: 30, coefficient: 1.0, flat: 50, weapon: 'staff', castMs: 300 },
      null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'groupheal' }, 0) // allAllies 不需要 targeting
  eq(s.party[0].action, 'casting', 'allAllies 技能不需要 targeting，直接開始施法')
  s = tick(s, 300)
  const healEvents = s.events.filter((e) => e.kind === 'heal')
  eq(healEvents.length, 3, 'allAllies 對全體 3 名存活隊員各推一筆 heal 事件')
  // floor(matk80×1+50)=130，player 800 封頂不變（800+130 超過 hpMax 800）；ally1 100+130=230；ally2 200+130=330
  eq(s.party[0].hp, 800, 'allAllies 治療量封頂 hpMax（玩家原本已滿血）')
  eq(s.party[1].hp, 230, 'allAllies 治療套用在 ally1：100+floor(80*1+50)=230')
  eq(s.party[2].hp, 330, 'allAllies 治療套用在 ally2：200+floor(80*1+50)=330')
}

// ── 26) 先扣盾再扣 HP 的端到端（敵人攻擊有護盾的隊員） ──
{
  let s = createBattle(makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 500, hpMax: 500, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 500, mpMax: 100, atk: 135, matk: 80, def: 20, mdef: 28 } }],
    enemies: [{ id: 'e1', name: '打手', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', stats: { hpMax: 999, mpMax: 0, atk: 100, matk: 0, def: 0, mdef: 0 } }],
  }), { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] } })
  s.party[0].shield = 50
  s = tick(s, 0) // idle→windup
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking，結算傷害
  // raw damage = max(1, 100-20) = 80；shield 50 先擋，剩 30 才扣 HP。
  const atkEv = s.events.find((e) => e.kind === 'enemyAttack')
  eq(atkEv.damage, 80, '傷害公式本身不知道盾牌的事，還是算出完整 80')
  eq(s.party[0].shield, 0, '護盾先被打消耗完（50 全部吸收）')
  eq(s.party[0].hp, 500 - 30, '剩餘 30 點才真的扣到 HP（80-50=30）')
}

// ── 27) SELECT_TARGET 切到另一隻存活敵人 ──
{
  const sample = makeSample({
    enemies: [
      { id: 'e1', name: '甲', level: 1, hp: 10, hpMax: 10, slot: 'front_left', imageUrl: '' },
      { id: 'e2', name: '乙', level: 1, hp: 10, hpMax: 10, slot: 'front_right', imageUrl: '' },
    ],
    initialTargetId: 'e1',
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  eq(s.targetId, 'e1', '（前提）初始目標是 e1')
  s = dispatch(s, { type: 'SELECT_TARGET', enemyId: 'e2' }, 0)
  eq(s.targetId, 'e2', 'SELECT_TARGET 切換到另一隻存活敵人')
  ok(s.events.some((e) => e.kind === 'targetChanged' && e.enemyId === 'e2'), 'targetChanged 事件正確')
}

// ── 28) 防禦中 SELECT_TARGET/SET_TRAY/USE_SKILL/TRY_ESCAPE 各自被拒 ──
{
  const sample = makeSample({
    enemies: [
      { id: 'e1', name: '甲', level: 1, hp: 10, hpMax: 10, slot: 'front_left', imageUrl: '' },
      { id: 'e2', name: '乙', level: 1, hp: 10, hpMax: 10, slot: 'front_right', imageUrl: '' },
    ],
    initialTargetId: 'e1',
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'GUARD_BEGIN' }, 0)

  const afterSelect = dispatch(s, { type: 'SELECT_TARGET', enemyId: 'e2' }, 10)
  eq(afterSelect.targetId, 'e1', '防禦中 SELECT_TARGET 被拒，目標不變')

  const afterTray = dispatch(s, { type: 'SET_TRAY', mode: 'items' }, 10)
  eq(afterTray.trayMode, 'skills', '防禦中 SET_TRAY 被拒，trayMode 不變')

  const afterSkill = dispatch(s, { type: 'USE_SKILL', skillId: 'slash' }, 10)
  eq(afterSkill.party[0].action, 'guarding', '防禦中 USE_SKILL 被拒，仍是 guarding（不會變成 casting）')

  const afterEscape = dispatch(s, { type: 'TRY_ESCAPE' }, 10)
  eq(afterEscape.escape.flow, 'available', '防禦中 TRY_ESCAPE 被拒，escape.flow 不變')
}

// ── 29) 審查修復 #2：AI 的技能冷卻（aiSkillReadyAt）跟玩家的 skillReadyAt 是分開兩把鎖 ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'healer', name: '輔助', level: 56, hp: 100, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 50, matk: 80, def: 20, mdef: 20 }, weapon: 'staff', skills: [HEAL_SKILL] },
    ],
  })
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s = tick(s, 0) // healer 血量 100/800=12.5%<50%，觸發治療（用掉的是 aiSkillReadyAt）
  ok(s.events.some((e) => e.kind === 'heal' && e.actorId === 'healer'), '（前提）healer 這一步真的觸發了治療')
  eq(s.skillReadyAt['heal'] ?? 0, 0, 'AI 用掉治療技能，不會動到玩家的 skillReadyAt（UI 契約既有欄位）')
  ok((s.aiSkillReadyAt['healer']?.['heal'] ?? 0) > 0, 'AI 自己的冷卻記在 aiSkillReadyAt')

  // 玩家自己也去對 healer 施放同一顆 heal 技能：兩把鎖互不影響，玩家可以正常施放。
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'heal', targetId: 'healer' }, 10)
  eq(s.party[0].action, 'casting', '玩家施放 heal 不受 AI 那把鎖影響，正常進入 casting')
  s = tick(s, 10 + DEFAULT_BATTLE_CONFIG.defaultCastMs)
  ok((s.skillReadyAt['heal'] ?? 0) > 0, '玩家施放後，換玩家自己的 skillReadyAt 進冷卻')
}

// ── 30) 審查修復 #3：治療 AI 自癒優先順序 ──
{
  function makeHealPrioritySample(healerHp, ally2Hp) {
    return makeSample({
      party: [
        { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
        { id: 'healer', name: '輔助', level: 56, hp: healerHp, hpMax: 100, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 100, mpMax: 100, atk: 50, matk: 80, def: 20, mdef: 20 }, weapon: 'staff', skills: [HEAL_SKILL] },
        { id: 'ally2', name: '夥伴二', level: 56, hp: ally2Hp, hpMax: 100, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 100, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 } },
      ],
    })
  }
  {
    // healer 20%（最低但 ≥15%），ally2 35%，兩者都 <50% 門檻：規則要求優先救「非自己」的 ally2。
    let s = createBattle(makeHealPrioritySample(20, 35), { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
    s = tick(s, 0)
    const healEv = s.events.find((e) => e.kind === 'heal' && e.actorId === 'healer')
    ok(!!healEv && healEv.targetId === 'ally2', 'healer 自己是最低比例但 ≥15%、另有隊友也低於門檻 → 優先救別人')
  }
  {
    // healer 10%（<15%，快死了）：即使 ally2 也低於門檻，還是先救自己。
    let s = createBattle(makeHealPrioritySample(10, 35), { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
    s = tick(s, 0)
    const healEv = s.events.find((e) => e.kind === 'heal' && e.actorId === 'healer')
    ok(!!healEv && healEv.targetId === 'healer', 'healer 自己 <15% 時，優先救自己')
  }
}

// ════════════════════════════════════════════════════════════════════════════
// P2（暴擊／Miss／無效攻擊）新增測試：31 號起。
// ════════════════════════════════════════════════════════════════════════════

// ── 31) missChance：clamp 上下限 + flee/hit 方向性（DEFAULT_BATTLE_CONFIG：base=0, scale=0.35, min=2, max=35） ──
// baseMissPct 由 TUNE 套用 BALANCE 建議從 SPEC 預設 8 改成 0（見 config.go/engine/types.ts 同一處
// 註解），下面期望值跟著改用 base=0 重新算過；higherFlee/higherHit 的方向性（迴避高於命中的那個
// 方向落空率較高）在 base 改變後依然成立，只是 higherHit 這組現在直接落在下限 2（見各行算式）。
{
  eq(
    missChance({ hit: 100, flee: 0, critPct: 0, critShield: 0 }, { hit: 0, flee: 0, critPct: 0, critShield: 0 }, DEFAULT_BATTLE_CONFIG),
    2,
    'missChance：0+(0-100)*0.35=-35，算出負值時封頂在 missMinPct=2（下限）',
  )
  eq(
    missChance({ hit: 0, flee: 0, critPct: 0, critShield: 0 }, { hit: 0, flee: 200, critPct: 0, critShield: 0 }, DEFAULT_BATTLE_CONFIG),
    35,
    'missChance：0+(200-0)*0.35=70，超過時封頂在 missMaxPct=35（上限）',
  )
  const higherFlee = missChance({ hit: 60, flee: 0, critPct: 0, critShield: 0 }, { hit: 0, flee: 70, critPct: 0, critShield: 0 }, DEFAULT_BATTLE_CONFIG)
  const higherHit = missChance({ hit: 70, flee: 0, critPct: 0, critShield: 0 }, { hit: 0, flee: 60, critPct: 0, critShield: 0 }, DEFAULT_BATTLE_CONFIG)
  eq(higherFlee, 3.5, 'missChance：defender.flee(70) > attacker.hit(60) → 0+(70-60)*0.35=3.5')
  eq(higherHit, 2, 'missChance：attacker.hit(70) > defender.flee(60) → 0+(60-70)*0.35=-3.5，封頂在下限 2')
  ok(higherFlee > higherHit, 'missChance：defender 迴避高於 attacker 命中的那個方向，落空率確實比反過來高')
}

// ── 32) critChance：全體基礎暴擊率（cfg.critRate）+ attacker.critPct − defender.critShield，下限 0 ──
{
  const cfg = { ...DEFAULT_BATTLE_CONFIG, critRate: 0.08 }
  eq(
    critChance({ hit: 0, flee: 0, critPct: 10, critShield: 0 }, { hit: 0, flee: 0, critPct: 0, critShield: 5 }, cfg),
    13,
    'critChance：10(attacker.critPct) + 8(全體基礎) - 5(defender.critShield) = 13',
  )
  eq(
    critChance({ hit: 0, flee: 0, critPct: 0, critShield: 0 }, { hit: 0, flee: 0, critPct: 0, critShield: 50 }, cfg),
    0,
    'critChance：critShield 扣過頭時封底在 0（不會變成負機率）',
  )
}

// ── 33) computeRawDamage：crit 倍率疊在跟蓄氣倍率同一個乘數位置；刻意不套 max(1,...) ──
{
  eq(computeRawDamage(100, 1, 0, 1, 2.5, 2, 0), 500, 'computeRawDamage：crit(×2) 疊在 chargeMul(×2.5) 上 → floor(100*2.5*2)-0=500')
  eq(computeRawDamage(100, 1, 0, 1, 2.5, 1, 0), 250, '（對照組）沒有暴擊時同一擊只有 250，證明 crit 確實把傷害翻倍')
  eq(computeRawDamage(10, 1, 0, 1, 1, 1, 10), 0, 'computeRawDamage：raw 剛好等於 def → net=0（immune 判定的邊界值）')
  eq(computeRawDamage(10, 1, 0, 1, 1, 1, 15), -5, 'computeRawDamage：刻意不套 max(1,...)，def 超過 raw 時允許回傳負值')
}

// ── 34) elementMultiplier：P5 改版——chart 覆寫優先、否則 weakElements 命中 +25%、否則 1.0；
//        DEFAULT_BATTLE_CONFIG.elementChart 改成清空（CONTRACT §0/§6，弱點改由 Enemy.weakElements
//        表達）；elementMultiplier() 簽章也從「(cfg, attribute, element)」改成「(cfg, enemy, element)」
//        ——這是本輪對既有 P2 測試刻意的行為變更，不是回歸，舊測試改用管理者覆寫 cfg 驗證同一段邏輯。
//        P7 再改一次簽章成「(cfg, attackElement, enemy)」（WIRE.md），下面呼叫點的第 2/3 個參數對調
//        ——這批測試用的都是 Chinese attribute（'金'/'木'/'無'），不會命中 P7 新增的英文五行相剋表
//        （見 formulas.ts ELEMENT_BEATS 只收 ElementKind 英文字面值），所以期望值原封不動；P7 新增
//        的五行相剋行為改在下面新的 P7 區塊用英文 attribute 另外驗證。──
{
  eq(Object.keys(DEFAULT_BATTLE_CONFIG.elementChart).length, 0, 'P5：DEFAULT_BATTLE_CONFIG.elementChart 預設清空成 {}（P2 那組金/木/土/闇示範表已移除）')

  const chartCfg = { ...DEFAULT_BATTLE_CONFIG, elementChart: { 金: { water: 0.0, fire: 1.6 } } }
  eq(elementMultiplier(chartCfg, 'water', { attribute: '金', weakElements: ['fire'] }), 0, 'chart 覆寫優先：金×water 查表命中 0（管理者覆寫可做免疫），即使 water 沒列在 weakElements')
  eq(elementMultiplier(chartCfg, 'fire', { attribute: '金', weakElements: ['fire'] }), 1.6, 'chart 覆寫優先：金×fire 查表命中 1.6，蓋過 weakElements 規則算出來的 1.25')
  eq(elementMultiplier(chartCfg, 'fire', { attribute: '木', weakElements: ['fire'] }), 1.25, '查無 chart 覆寫（木不在表中）→ 落到 weakElements 規則：命中弱點 → 1+weaknessBonusPct/100=1.25')
  eq(elementMultiplier(chartCfg, 'water', { attribute: '木', weakElements: ['fire'] }), 1, '查無 chart 覆寫、也沒命中 weakElements、Chinese attribute 也不會命中五行表 → 1.0（不相剋也不吃虧）')
  eq(elementMultiplier(chartCfg, 'fire', { attribute: '無', weakElements: [] }), 1, '沒有弱點的怪物（weakElements=[]）→ 恆 1.0')
  eq(elementMultiplier(chartCfg, 'water', undefined), 1, '怪物物件本身是 undefined → 視為中性，直接回 1.0')
  eq(elementMultiplier(DEFAULT_BATTLE_CONFIG, 'fire', { attribute: '金', weakElements: ['fire'] }), 1.25, 'DEFAULT config（沒有管理者覆寫，elementChart={}）：金×fire 命中 weakElements → 1.25')
  eq(elementMultiplier({ ...DEFAULT_BATTLE_CONFIG, weaknessBonusPct: 50 }, 'fire', { attribute: '金', weakElements: ['fire'] }), 1.5, 'weaknessBonusPct 可調整：改 50 之後命中弱點變成 ×1.5')
}

// ── 34b) P7（CONTRACT §1）：五行＋光暗相剋表——英文 ElementKind attribute 才會命中 ELEMENT_BEATS；
//        同屬性／相剋／被剋／neutral／不相干（金 vs 水）／weakElements 與五行表取大／管理者覆寫仍最優先。──
{
  const cfg = DEFAULT_BATTLE_CONFIG // elementAdvantagePct=25, elementDisadvantagePct=-25, elementSamePct=-25
  eq(elementMultiplier(cfg, 'metal', { attribute: 'wood' }), 1.25, '五行：金剋木（A=metal, M=wood）→ 1+25%=1.25')
  eq(elementMultiplier(cfg, 'wood', { attribute: 'metal' }), 0.75, '五行：木被金剋（A=wood, M=metal）→ 1-25%=0.75')
  eq(elementMultiplier(cfg, 'wood', { attribute: 'earth' }), 1.25, '五行：木剋土 → 1.25')
  eq(elementMultiplier(cfg, 'earth', { attribute: 'water' }), 1.25, '五行：土剋水 → 1.25')
  eq(elementMultiplier(cfg, 'water', { attribute: 'fire' }), 1.25, '五行：水剋火 → 1.25')
  eq(elementMultiplier(cfg, 'fire', { attribute: 'metal' }), 1.25, '五行：火剋金 → 1.25')
  eq(elementMultiplier(cfg, 'light', { attribute: 'dark' }), 1.25, '光闇互剋：光剋闇 → 1.25')
  eq(elementMultiplier(cfg, 'dark', { attribute: 'light' }), 1.25, '光闇互剋：闇剋光 → 1.25（雙向都是剋，不是「光贏闇輸」單向）')
  eq(elementMultiplier(cfg, 'fire', { attribute: 'fire' }), 0.75, '同屬性：火×火 → 1+elementSamePct(-25%)=0.75')
  eq(elementMultiplier(cfg, 'light', { attribute: 'light' }), 0.75, '同屬性：光×光 → 0.75')
  eq(elementMultiplier(cfg, 'metal', { attribute: 'water' }), 1, '五行表中不相剋也不相生的組合（金 vs 水）→ 1.0（中性）')
  eq(elementMultiplier(cfg, 'neutral', { attribute: 'fire' }), 1, '攻擊屬性 neutral → 恆 1.0，不查五行表')
  eq(elementMultiplier(cfg, 'fire', { attribute: 'neutral' }), 1, '怪物屬性 neutral → 恆 1.0，不查五行表')
  eq(elementMultiplier(cfg, 'fire', { attribute: undefined }), 1, '怪物沒有 attribute 欄位 → 視為 neutral，恆 1.0')
  // weakElements 與五行表取大：木被金剋(0.75) 但木在弱點桶 → 取 max(0.75, 1.25)=1.25。
  eq(elementMultiplier(cfg, 'wood', { attribute: 'metal', weakElements: ['wood'] }), 1.25, 'weakElements 與五行表取大：被剋的 0.75 vs 弱點桶 1.25 → 取 1.25（弱點桶保底）')
  // 反過來：本來就相剋(1.25) 且同時也在弱點桶(1.25) → 取大結果不變，仍是 1.25，不會疊加成 1.5625。
  eq(elementMultiplier(cfg, 'metal', { attribute: 'wood', weakElements: ['metal'] }), 1.25, '同時符合五行相剋與弱點桶 → 取大不疊加，仍是 1.25（不是兩者相乘）')
  // 可調整係數：改預設值後同一組輸入算出不同倍率。
  const tuned = { ...cfg, elementAdvantagePct: 50, elementDisadvantagePct: -50, elementSamePct: -50 }
  eq(elementMultiplier(tuned, 'metal', { attribute: 'wood' }), 1.5, '五行係數可調整：elementAdvantagePct 改 50 → 剋制倍率變 1.5')
  eq(elementMultiplier(tuned, 'wood', { attribute: 'metal' }), 0.5, 'elementDisadvantagePct 改 -50 → 被剋倍率變 0.5')
  eq(elementMultiplier(tuned, 'fire', { attribute: 'fire' }), 0.5, 'elementSamePct 改 -50 → 同屬性倍率變 0.5')
  // 管理者覆寫仍是最優先，蓋過五行表本身算出來的相剋結果。
  const overrideCfg = { ...cfg, elementChart: { wood: { metal: 9.9 } } }
  eq(elementMultiplier(overrideCfg, 'metal', { attribute: 'wood' }), 9.9, '管理者覆寫優先於五行表：金×木本來剋制算 1.25，但被 chart 覆寫成 9.9')
}

// ── 35) 端到端：chart 覆寫可以做出 'immune'（0 倍） ──
{
  const sample = makeSample({
    enemies: [
      {
        id: 'e1', name: '鋼鐵巨鉗蟹', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '',
        attribute: '金', weakElements: ['fire'], stats: { hpMax: 999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 },
      },
    ],
    skills: [
      { id: 'icebolt', name: '冰槍術', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 2, flat: 0, weapon: 'staff', element: 'water', castMs: 100 },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  // 不需要固定 rng：FAR_CONFIG 已把 missPct 封死在 0（必中），elementMul=0 在算暴擊前就 return，
  // 所以這條路徑完全不吃 rng 的實際數值，用 Math.random 也一樣確定。
  const immuneCfg = { ...FAR_CONFIG, elementChart: { 金: { water: 0.0 } } } // 管理者覆寫示範：金免疫 water
  let s = createBattle(sample, { now: 0, config: immuneCfg })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'icebolt' }, 0)
  // P3 新增 castMinMs（預設 120）：即使玩家 castReductionPct=0（這裡沒帶 rating，退回中性後備值），
  // effectiveCastMs 仍會把低於 120ms 的 castMs 夾到 120——這支測試技能的 castMs=100 本來就低於
  // 這個新地板，改成 tick 到 120 才會命中 actionUntil，行為改變合理（P3 刻意設計的全域下限，不是
  // 這個測試原本要驗證的屬性剋制邏輯跑掉）。
  s = tick(s, 120)
  const ev = s.events.find((e) => e.kind === 'attack' && e.actorId === 'player')
  ok(!!ev && ev.result === 'immune' && ev.damage === 0, 'chart 覆寫金×water=0 → elementMultiplier=0 → result=immune，damage=0')
  eq(s.enemies[0].hp, 999, 'immune 攻擊完全不扣血')
}

// ── 35b) 端到端：weakElements 命中（沒有 chart 覆寫時的預設行為）→ +25% 傷害，非 immune ──
{
  const sample = makeSample({
    enemies: [
      {
        id: 'e1', name: '鋼鐵巨鉗蟹', level: 1, hp: 5000, hpMax: 5000, slot: 'front_center', imageUrl: '',
        attribute: '金', weakElements: ['fire'], stats: { hpMax: 5000, mpMax: 0, atk: 1, matk: 1, def: 35, mdef: 0 },
      },
    ],
    skills: [
      { id: 'fireskill', name: '火球術', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1, flat: 0, weapon: 'staff', element: 'fire', castMs: 100 },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  // FAR_CONFIG 的 elementChart 沿用 DEFAULT_BATTLE_CONFIG（P5 起已清空成 {}），所以這裡真的是在測
  // weakElements 規則本身、不是被管理者覆寫表蓋掉。
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'fireskill' }, 0)
  s = tick(s, 120)
  const ev = s.events.find((e) => e.kind === 'attack' && e.actorId === 'player')
  ok(!!ev, '（前提）技能有造成攻擊事件')
  // raw = floor(135×1×1.25×1×1) = 168；168-35=133（135 是 makeSample 玩家的預設 ATK）。
  eq(ev && ev.damage, Math.floor(135 * 1.25) - 35, '火屬性命中金屬性怪物的 weakElements → ×1.25，傷害符合 +25% 加成（非 immune）')
  ok(ev && ev.result !== 'immune', 'weakElements 命中只是加成，不是 immune（跟 chart 覆寫 0 倍的語意不同）')
}

// ── 36) 端到端：net≤0（非屬性剋制，純數值被完全擋下）也判 'immune' ──
{
  const sample = makeSample({
    party: [{ id: 'player', name: '弱雞', level: 1, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 10, matk: 10, def: 10, mdef: 10 } }],
    enemies: [{ id: 'e1', name: '銅牆鐵壁', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', stats: { hpMax: 999, mpMax: 0, atk: 1, matk: 1, def: 50, mdef: 50 } }],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0) // holdMs=0 → chargeMul=1；raw=floor(10*1)=10，net=10-50=-40≤0
  const ev = s.events.find((e) => e.kind === 'attack')
  ok(!!ev && ev.result === 'immune' && ev.damage === 0, 'atk 遠低於 def（非屬性剋制）時 net≤0 也判 immune')
  eq(s.enemies[0].hp, 999, 'immune 攻擊完全不扣血（net≤0 這條路徑也一樣）')
}

// ── 37) 端到端：missPct=100% 時玩家攻擊必定 miss，不扣血 ──
{
  const cfg = { ...FAR_CONFIG, baseMissPct: 100, missMinPct: 100, missMaxPct: 100 }
  let s = createBattle(makeSample(), { now: 0, config: cfg, rng: fixedRng([0]) })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const ev = s.events.find((e) => e.kind === 'attack')
  ok(!!ev && ev.result === 'miss' && ev.damage === 0, 'missPct=100% 時玩家攻擊必定 miss，damage=0')
  eq(s.enemies[0].hp, 5000, 'miss 攻擊完全不扣血')
}

// ── 38) 端到端：critRate=100% 時玩家攻擊必定 critical，傷害吃到 critMultiplier ──
{
  const cfg = { ...FAR_CONFIG, critRate: 1 } // critPct = 0(attacker.critPct，後備評級無資料) + 1*100 - 0 = 100
  let s = createBattle(makeSample(), { now: 0, config: cfg, rng: fixedRng([0.5]) })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const ev = s.events.find((e) => e.kind === 'attack')
  ok(!!ev && ev.result === 'critical', 'critRate=100% 時攻擊必定 critical')
  // raw = floor(135*1*1*1*2) = 270（critMultiplier 預設 2）；net = 270-35 = 235
  eq(ev.damage, 235, '暴擊傷害＝floor(135*1*2)-35=235（critMul 疊在跟蓄氣倍率同一個乘數位置）')
  eq(s.enemies[0].hp, 5000 - 235, '暴擊傷害正確套用在敵人血量上')
}

// ── 39) 端到端：怪物攻擊 miss 時不扣 HP、也不扣護盾（規格 §5：「miss 時 damage=0、不扣盾」） ──
{
  const cfg = { ...FAR_CONFIG, enemyActIntervalMs: [0, 0], baseMissPct: 100, missMinPct: 100, missMaxPct: 100 }
  let s = createBattle(makeSample(), { now: 0, config: cfg, rng: fixedRng([0]) })
  s.party[0].shield = 50
  s = tick(s, 0) // idle→windup
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking，結算（強制 miss）
  const ev = s.events.find((e) => e.kind === 'enemyAttack')
  ok(!!ev && ev.result === 'miss' && ev.damage === 0, '怪物攻擊 miss：enemyAttack 事件 result=miss、damage=0')
  eq(s.party[0].hp, 800, '怪物攻擊 miss 不扣 HP')
  eq(s.party[0].shield, 50, '怪物攻擊 miss 也不扣護盾（applyPartyDamage 完全沒被呼叫）')
}

// ── 40) 端到端：monsterCritPct=100% 時怪物攻擊必定 critical，傷害吃到 critMultiplier ──
{
  const cfg = { ...FAR_CONFIG, enemyActIntervalMs: [0, 0], monsterCritPct: 100 }
  let s = createBattle(makeSample(), { now: 0, config: cfg, rng: fixedRng([0.5]) })
  s = tick(s, 0) // idle→windup
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking
  const ev = s.events.find((e) => e.kind === 'enemyAttack')
  ok(!!ev && ev.result === 'critical', '怪物 monsterCritPct=100 時攻擊必定 critical')
  // computeDamage(50,1,0,elementMul=1,critMul=2,def=35,guarded=false) = max(1, 50*2-35) = 65
  eq(ev.damage, 65, '怪物暴擊傷害＝computeDamage 用 chargeMul 位置疊 critMultiplier＝max(1,100-35)=65')
}

// ── 41) createBattle 的 rating 後備推導：沒帶 rating 的隊員/怪物分別用 hitRate／monster_* 係數推導 ──
{
  const cfg = { ...FAR_CONFIG, hitRate: 0.9, monsterHitBase: 77, monsterFleeBase: 12, monsterCritPct: 3, monsterCritShieldBase: 4 }
  const s = createBattle(makeSample(), { now: 0, config: cfg })
  // P3：cfg 沒覆寫 aspdReference，沿用 DEFAULT_BATTLE_CONFIG 的 150——後備評級的 aspd/
  // castReductionPct 也要一併核對，不然 P3 把這兩個新欄位漏接（例如忘記在 deriveDefault*Rating
  // 補上）不會被任何既有斷言抓到。
  eq(
    s.party[0].rating,
    // P5 修正（審查#5）：deriveDefaultPartyRating 新增 critDmgPct:0（中性值，見該函式註解）。
    { hit: 90, flee: 0, critPct: 0, critShield: 0, aspd: 150, castReductionPct: 0, critDmgPct: 0 },
    '沒有帶 rating 的隊員：後備評級的 hit 沿用 cfg.hitRate(0.9→90)、aspd 沿用 cfg.aspdReference(150)，其餘給 0（沒有更多資訊可推）',
  )
  eq(
    s.enemies[0].rating,
    // P5 修正（審查#5）：deriveDefaultMonsterRating 新增 critDmgPct:0（怪物沒有這個加成來源）。
    { hit: 77, flee: 12, critPct: 3, critShield: 4, aspd: 150, castReductionPct: 0, critDmgPct: 0 },
    '沒有帶 rating 的怪物：由 config 係數直接推導（deriveDefaultMonsterRating，跟等級無關），aspd 同樣沿用 cfg.aspdReference',
  )
}

// ── 42) createBattle：呼叫端真的帶了 rating 時直接採用，不會被後備推導覆蓋 ──
{
  const customPartyRating = { hit: 55, flee: 66, critPct: 77, critShield: 88, aspd: 175, castReductionPct: 20 }
  const customMonsterRating = { hit: 11, flee: 22, critPct: 33, critShield: 44, aspd: 150, castReductionPct: 0 }
  const sample = makeSample({
    party: [
      {
        id: 'player', name: '有評級的玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null,
        stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, rating: customPartyRating,
      },
    ],
    enemies: [
      { id: 'e1', name: '有評級的怪物', level: 50, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', rating: customMonsterRating },
    ],
  })
  const s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  eq(s.party[0].rating, customPartyRating, '呼叫端帶了真實 rating 時，createBattle 直接採用，不覆蓋成後備預設')
  eq(s.enemies[0].rating, customMonsterRating, '怪物帶了真實 rating 時同樣直接採用，不覆蓋成 config 推導的預設')
}

// ════════════════════════════════════════════════════════════════════════════
// P3（AGI 攻速／DEX 詠唱縮減，審查 dorpg_p3 r5 使用者當面要求）新增區塊。
// DEFAULT_BATTLE_CONFIG 相關常數：attackCooldownMs=1500、aspdReference=150、
// attackCooldownMinMs=700、castMinMs=120。
// ════════════════════════════════════════════════════════════════════════════

// ── 43) attackCooldownFor：aspd=reference→冷卻恰為 base；高於/低於 reference 的方向性＋上下 clamp；rating.aspd 缺省的後備 ──
{
  const cfg = DEFAULT_BATTLE_CONFIG
  const r = (aspd) => ({ hit: 0, flee: 0, critPct: 0, critShield: 0, aspd, castReductionPct: 0 })
  eq(attackCooldownFor(r(150), cfg), 1500, 'attackCooldownFor：aspd=aspdReference(150) → 冷卻恰為 base=1500，不配 AGI 的人手感不變')
  eq(attackCooldownFor(r(175), cfg), 750, 'attackCooldownFor：aspd=175(高於 reference) → 1500*(200-175)/(200-150)=750，變快')
  eq(attackCooldownFor(r(199), cfg), 700, 'attackCooldownFor：aspd=199(逼近上限) → 算出 30ms，封頂在 attackCooldownMinMs=700（下限）')
  eq(attackCooldownFor(r(100), cfg), 1500, 'attackCooldownFor：aspd=100(低於 reference) → 算出 3000ms，上夾封頂在 base=1500（不會比不配點還慢）')
  eq(attackCooldownFor(r(0), cfg), 1500, 'attackCooldownFor：aspd=0(掉到底) 同樣被上夾封頂在 base=1500')
  eq(
    attackCooldownFor({ hit: 0, flee: 0, critPct: 0, critShield: 0 }, cfg),
    1500,
    'attackCooldownFor：rating.aspd 缺省（呼叫端沒填這個新欄位）→ 視為中性 aspdReference，退回 base 冷卻，不是 NaN',
  )
}

// ── 44) effectiveCastMs：castReductionPct 0/50/100 三檔＋min clamp＋rating.castReductionPct 缺省的後備 ──
{
  const cfg = DEFAULT_BATTLE_CONFIG
  const r = (pct) => ({ hit: 0, flee: 0, critPct: 0, critShield: 0, aspd: cfg.aspdReference, castReductionPct: pct })
  eq(effectiveCastMs(400, r(0), cfg), 400, 'effectiveCastMs：castReductionPct=0 → 完全不縮減')
  eq(effectiveCastMs(400, r(50), cfg), 200, 'effectiveCastMs：castReductionPct=50 → round(400*0.5)=200')
  eq(effectiveCastMs(400, r(100), cfg), 120, 'effectiveCastMs：castReductionPct=100 → 算出 0，封頂在 castMinMs=120（下限）')
  eq(effectiveCastMs(200, r(50), cfg), 120, 'effectiveCastMs：castReductionPct=50 但 baseCastMs 較小 → round(200*0.5)=100，仍被 min 夾到 120')
  eq(
    effectiveCastMs(300, { hit: 0, flee: 0, critPct: 0, critShield: 0 }, cfg),
    300,
    'effectiveCastMs：rating.castReductionPct 缺省（呼叫端沒填這個新欄位）→ 視為 0，不縮減，不是 NaN',
  )
}

// ── 45) 端到端：玩家 rating.aspd 真的透過 dispatch 的 ATTACK_RELEASE 換算成攻擊冷卻（驗證 dispatch.ts 接線，不是只測公式本身） ──
{
  const sample = makeSample({
    party: [
      {
        id: 'player', name: '快手玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null,
        stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 },
        rating: { hit: 100, flee: 0, critPct: 0, critShield: 0, aspd: 175, castReductionPct: 0 },
      },
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  eq(s.party[0].attackReadyAt, 750, 'dispatch ATTACK_RELEASE：玩家 rating.aspd=175 透過 attackCooldownFor 算出冷卻 750ms（不再是寫死的 1500）')
}

// ── 46) 端到端：玩家 rating.castReductionPct 真的透過 dispatch 的 commitCast 換算成施法時間（驗證 dispatch.ts 接線） ──
{
  const sample = makeSample({
    party: [
      {
        id: 'player', name: '快嘴玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null,
        stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 },
        rating: { hit: 100, flee: 0, critPct: 0, critShield: 0, aspd: 150, castReductionPct: 50 },
      },
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'slash' }, 0) // slash castMs=300
  eq(s.party[0].actionUntil, 150, 'dispatch USE_SKILL：玩家 rating.castReductionPct=50 透過 effectiveCastMs 把 300ms 縮成 150ms')
}

// ── 47) 端到端：隊友即使 rating.aspd 極端高，出手節奏仍固定吃 allyActIntervalMs，不套 attackCooldownFor（審查要求「隊友不受影響」） ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      {
        id: 'ally1', name: '快腿隊友', level: 56, hp: 500, hpMax: 500, mp: 50, mpMax: 50, portraitUrl: null,
        stats: { hpMax: 500, mpMax: 50, atk: 100, matk: 50, def: 20, mdef: 15 },
        rating: { hit: 100, flee: 0, critPct: 0, critShield: 0, aspd: 199, castReductionPct: 0 }, // 極端高攻速
      },
    ],
  })
  const cfg = { ...FAR_CONFIG, allyActIntervalMs: [1000, 1000] }
  let s = createBattle(sample, { now: 0, config: cfg })
  s = tick(s, 1000) // ally1 的 attackReadyAt(=1000) 到，出手一次
  const ally = s.party.find((p) => p.id === 'ally1')
  ok(s.events.some((e) => e.kind === 'attack' && e.actorId === 'ally1'), '（前提）隊友這次 tick 真的出手了')
  eq(ally.attackReadyAt, 2000, '隊友 rating.aspd=199 極端值不影響節奏：下次出手時間固定用 allyActIntervalMs=1000（1000+1000=2000），不套 attackCooldownFor')
}

// ── 48) 端到端：怪物即使 rating.aspd 極端高，行動節奏仍固定吃 enemyActIntervalMs，不套 attackCooldownFor（審查要求「怪物不受影響」） ──
{
  // 沿用區塊 20（敵人 AI 完整時序）已驗證過的 enemyActIntervalMs=[0,0] 設定與 tick 序列，唯一差異
  // 只有加上一個帶極端高 aspd 的 rating——如果最後算出的 nextActAt 跟區塊 20 的基準值（沒有這個
  // rating 欄位時）完全相同，就證明 rating.aspd 對怪物的行動排程完全沒有作用。
  let s = createBattle(makeSample({
    enemies: [{
      id: 'e1', name: '假人', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '',
      stats: { hpMax: 999, mpMax: 0, atk: 10, matk: 10, def: 0, mdef: 0 },
      rating: { hit: 100, flee: 0, critPct: 0, critShield: 0, aspd: 199, castReductionPct: 0 }, // 極端高攻速
    }],
  }), { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] }, rng: fixedRng([0]) })
  s = tick(s, 0) // idle→windup（nextActAt=now+rand([0,0])=0，立刻觸發）
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs + DEFAULT_BATTLE_CONFIG.enemyAttackMs) // attacking→idle，排下一次行動
  eq(
    s.enemies[0].nextActAt,
    DEFAULT_BATTLE_CONFIG.enemyWindupMs + DEFAULT_BATTLE_CONFIG.enemyAttackMs,
    '怪物 rating.aspd=199 極端值不影響節奏：算出的 nextActAt 跟區塊20沒有 rating.aspd 時的基準值完全相同，不套 attackCooldownFor',
  )
}

// ════════════════════════════════════════════════════════════════════════════
// P5（DORPG_P5：職業／配點／技能——ENGINE 角色）新增測試：49 號起。
// ════════════════════════════════════════════════════════════════════════════

// ── 49) effects.ts 純函式單元測試：activeStatSum／effectiveRating／damageTakenMultiplier／effectiveStats ──
{
  const effects = [
    { stat: 'atk_pct', value: 10, expiresAt: 999999, sourceSkillId: 'a', kind: 'buff' },
    { stat: 'atk_pct', value: 5, expiresAt: 999999, sourceSkillId: 'b', kind: 'buff' },
    { stat: 'def_pct', value: -20, expiresAt: 999999, sourceSkillId: 'c', kind: 'debuff' },
  ]
  eq(activeStatSum(effects, 'atk_pct'), 15, 'activeStatSum：同 stat 不同來源各自疊加（10+5=15）')
  eq(activeStatSum(effects, 'flee'), 0, 'activeStatSum：查無該 stat → 0')

  eq(
    effectiveRating({ hit: 10, flee: 5, critPct: 2, critShield: 1, aspd: 150, castReductionPct: 0 }, effects),
    // P5 修正（審查#5）：effectiveRating 新增 critDmgPct 原樣通過（base 沒給，缺省視為 0）。
    { hit: 10, flee: 5, critPct: 2, critShield: 1, aspd: 150, castReductionPct: 0, critDmgPct: 0 },
    'effectiveRating：這組 effects 沒有 hit/flee/crit_pct/aspd，評級完全不變',
  )
  eq(
    effectiveRating({ hit: 10, flee: 5, critPct: 2, critShield: 1, aspd: 150, castReductionPct: 0 }, [
      { stat: 'hit', value: 20, expiresAt: 1, sourceSkillId: 'x', kind: 'buff' },
      { stat: 'aspd', value: -30, expiresAt: 1, sourceSkillId: 'y', kind: 'debuff' },
    ]),
    { hit: 30, flee: 5, critPct: 2, critShield: 1, aspd: 120, castReductionPct: 0, critDmgPct: 0 },
    'effectiveRating：hit/aspd 各自疊加對應的 buff/debuff，critShield/castReductionPct 沒有對應詞彙、原樣通過',
  )

  eq(damageTakenMultiplier([]), 1, 'damageTakenMultiplier：沒有效果時＝1（中性）')
  eq(damageTakenMultiplier([{ stat: 'damage_taken_pct', value: -30, expiresAt: 1, sourceSkillId: 'z', kind: 'buff' }]), 0.7, 'damageTakenMultiplier：-30% → 0.7 倍')
  eq(damageTakenMultiplier([{ stat: 'damage_taken_pct', value: -200, expiresAt: 1, sourceSkillId: 'z', kind: 'buff' }]), 0, 'damageTakenMultiplier：clamp 下限 0，不會變成負數（倒扣血）')

  eq(
    effectiveStats({ hpMax: 100, mpMax: 50, atk: 100, matk: 80, def: 20, mdef: 10 }, [{ stat: 'atk_pct', value: 50, expiresAt: 1, sourceSkillId: 'a', kind: 'buff' }]),
    { hpMax: 100, mpMax: 50, atk: 150, matk: 80, def: 20, mdef: 10 },
    'effectiveStats：atk_pct+50% → atk 從 100 變 150，其餘欄位不變',
  )
}

// ── 50) SKILL_SLOTS：技能欄容量 8→10；engine 對技能陣列長度無主張（由呼叫端決定），只驗證常數值
//        與 createBattle 原樣保留呼叫端傳入的長度（不截斷/不強制補滿）。 ──
{
  eq(SKILL_SLOTS, 10, 'SKILL_SLOTS 常數＝10（CONTRACT §6：技能欄容量 8→10，兩排各 5）')
  const tenSlots = [
    { id: 'slash', name: '斬擊', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.6, flat: 20, weapon: 'sword', castMs: 300 },
    null, null, null, null, null, null, null, null, null,
  ]
  eq(tenSlots.length, 10, '（前提）測試資料本身就是 10 格')
  const s = createBattle(makeSample({ skills: tenSlots }), { now: 0, config: FAR_CONFIG })
  eq(s.skills.length, 10, 'createBattle 原樣保留呼叫端傳入的 10 格技能陣列，engine 本身不寫死 8')
}

// ── 51) rollCritMultiplier：抽樣落在 [critMultMin, critMultMax]（固定種子多次涵蓋全區間），邊界值精確 ──
{
  const cfg = DEFAULT_BATTLE_CONFIG
  eq(rollCritMultiplier(fixedRng([0]), cfg), 1.75, 'rng()=0 → 落在下限 critMultMin=1.75')
  eq(rollCritMultiplier(fixedRng([1]), cfg), 2.25, 'rng()=1（邊界測試值，Math.random() 實際不會回傳 1）→ 落在上限 critMultMax=2.25')
  eq(rollCritMultiplier(fixedRng([0.5]), cfg), 2.0, 'rng()=0.5（中點）→ 剛好等於舊的固定值 2.0（拍板值刻意的中點設計）')
  const samples = Array.from({ length: 21 }, (_, i) => i / 20) // 0, 0.05, ..., 1，覆蓋整個 [0,1] 區間
  const mults = samples.map((v) => rollCritMultiplier(fixedRng([v]), cfg))
  ok(mults.every((m) => m >= cfg.critMultMin && m <= cfg.critMultMax), `21 次抽樣（固定種子涵蓋 [0,1]）全部落在 [1.75,2.25] 內（實際：${mults.join(',')}）`)
  eq(rollCritMultiplier(fixedRng([0]), { ...cfg, critMultMin: 3, critMultMax: 5 }), 3, '自訂區間下限也正確換算（不是寫死 1.75/2.25）')
  eq(rollCritMultiplier(fixedRng([1]), { ...cfg, critMultMin: 3, critMultMax: 5 }), 5, '自訂區間上限也正確換算')
}

// ── 52) 端到端：每次暴擊各自抽樣暴擊倍率（不再是固定 2.0）——同樣必定暴擊，不同 rng 序列 → 不同傷害 ──
{
  const cfg = { ...FAR_CONFIG, critRate: 1 } // critPct 恆 100%，攻擊必定暴擊
  // rng 消耗順序：createBattle 建立敵人時 toEnemyActor 呼叫 randRange(rng, enemyActIntervalMs) 算
  // nextActAt——即使 min===max（FAR_CONFIG 的 NEVER=[999999,999999]）randRange 內部還是會呼叫一次
  // rng()（結果不受影響，但序列往前消耗了一格，這是既有 P1 行為，不是本輪改動），所以「第一次攻擊」
  // 實際消耗的是序列的第 2～4 個值，不是第 1～3 個——用長度 4 的序列，第 0 格墊給這次建場消耗掉。
  // 第 1 格＝miss 判定（值不重要，missPct=0 恆命中）、第 2 格＝crit 判定（<1 恆真）、第 3 格＝crit 倍率抽樣。
  let low = createBattle(makeSample(), { now: 0, config: cfg, rng: fixedRng([0, 0, 0, 0]) })
  low = dispatch(low, { type: 'ATTACK_BEGIN' }, 0)
  low = dispatch(low, { type: 'ATTACK_RELEASE' }, 0)
  const lowEv = low.events.find((e) => e.kind === 'attack')
  eq(lowEv.damage, Math.floor(135 * 1.75) - 35, 'rng 恆 0 時暴擊倍率抽到下限 1.75，傷害對應變小（不再是固定 235）')

  let high = createBattle(makeSample(), { now: 0, config: cfg, rng: fixedRng([0, 0, 0, 0.999999]) })
  high = dispatch(high, { type: 'ATTACK_BEGIN' }, 0)
  high = dispatch(high, { type: 'ATTACK_RELEASE' }, 0)
  const highEv = high.events.find((e) => e.kind === 'attack')
  ok(highEv.damage > lowEv.damage, '不同 rng 序列抽到不同暴擊倍率 → 傷害不同（證明暴擊倍率確實是浮動抽樣，不是常數）')
}

// ── 53) 端到端：kind='damage' 的 hits>1 每段各自獨立結算與事件（多段命中） ──
{
  const sample = makeSample({
    enemies: [{ id: 'e1', name: '沙包', level: 1, hp: 99999, hpMax: 99999, slot: 'front_center', imageUrl: '', stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } }],
    skills: [
      { id: 'triple_slash', name: '三連斬', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 10, coefficient: 1, flat: 0, weapon: 'sword', castMs: 100, hits: 3 },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG }) // 必中必不暴擊，方便算出每段固定傷害
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'triple_slash' }, 0)
  s = tick(s, 120) // castMs=100 被 castMinMs=120 夾到 120
  const hitEvents = s.events.filter((e) => e.kind === 'attack' && e.actorId === 'player')
  eq(hitEvents.length, 3, 'hits=3 的技能命中後產生 3 筆各自獨立的 attack 事件')
  ok(hitEvents.every((e) => e.damage === 135 - 10), '每段各自獨立結算，傷害一致：floor(135×1)-10=125')
  eq(s.enemies[0].hp, 99999 - 3 * (135 - 10), '三段傷害各自扣血，總扣血量＝3×單段傷害')
}

// ── 54) 端到端：target='allEnemies' 打全體存活敵人，已死亡的敵人不會被打到 ──
{
  const sample = makeSample({
    enemies: [
      { id: 'e1', name: '甲（已死）', level: 1, hp: 0, hpMax: 100, slot: 'front_left', imageUrl: '', stats: { hpMax: 100, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } },
      { id: 'e2', name: '乙', level: 1, hp: 500, hpMax: 500, slot: 'front_center', imageUrl: '', stats: { hpMax: 500, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } },
      { id: 'e3', name: '丙', level: 1, hp: 500, hpMax: 500, slot: 'front_right', imageUrl: '', stats: { hpMax: 500, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } },
    ],
    initialTargetId: 'e2',
    skills: [
      { id: 'meteor', name: '流星火雨', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'allEnemies', mpCost: 20, coefficient: 1, flat: 0, weapon: 'staff', castMs: 100 },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'meteor' }, 0)
  s = tick(s, 120)
  const hitEvents = s.events.filter((e) => e.kind === 'attack' && e.actorId === 'player')
  eq(hitEvents.length, 2, 'allEnemies 只打存活的 2 隻（e1 已死不算），各命中一次')
  eq(hitEvents.map((e) => e.targetId).sort(), ['e2', 'e3'], 'allEnemies 命中的正是兩隻存活敵人，e1（已死）沒被打到')
}

// ── 55) 端到端：dmgType='magic' 用 MATK 扣 MDEF，不是 ATK 扣 DEF ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '法師', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 10, matk: 200, def: 35, mdef: 28 } },
    ],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 5000, hpMax: 5000, slot: 'front_center', imageUrl: '', stats: { hpMax: 5000, mpMax: 0, atk: 50, matk: 50, def: 999, mdef: 20 } }],
    skills: [
      { id: 'magic_bolt', name: '奧術飛彈', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 10, coefficient: 1, flat: 0, weapon: 'staff', castMs: 100, dmgType: 'magic' },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'magic_bolt' }, 0)
  s = tick(s, 120)
  const ev = s.events.find((e) => e.kind === 'attack' && e.actorId === 'player')
  // 若誤用 ATK(10)-DEF(999) 會是負值判 immune；用 MATK(200)-MDEF(20)=180 才會是正常命中。
  ok(!!ev && ev.result !== 'immune', 'dmgType=magic 用 MATK/MDEF 結算，不會被巨大的 DEF(999) 誤判成 immune')
  eq(ev.damage, 200 - 20, 'dmgType=magic：傷害＝floor(MATK×1)-MDEF＝200-20=180')
}

// ── 56) 端到端：buff（atk_pct）套用生效、同 stat 同來源刷新不疊加、生效期間普攻吃到加成、到期自動移除 ──
{
  const sample = makeSample({
    skills: [
      { id: 'slash', name: '斬擊', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1, flat: 0, weapon: 'sword', castMs: 100 },
      {
        id: 'war_cry', name: '戰吼', iconUrl: '', cooldownMs: 0, kind: 'buff', target: 'self', mpCost: 15, coefficient: 0, flat: 0, weapon: 'sword', castMs: 100,
        effect: { kind: 'buff', stat: 'atk_pct', value: 20, durationMs: 1000, target: 'self', mpCost: 15 },
      },
      null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'war_cry' }, 0)
  s = tick(s, 120) // castMs 被夾到 120，效果在這一刻結算
  eq(s.party[0].activeEffects.length, 1, '施放 buff 後 party[0] 身上多一筆 activeEffects')
  eq(
    s.party[0].activeEffects[0],
    { stat: 'atk_pct', value: 20, expiresAt: 1120, sourceSkillId: 'war_cry', kind: 'buff' },
    'ActiveEffect 內容正確（expiresAt＝施放完成時間 120＋durationMs 1000）',
  )
  ok(s.events.some((e) => e.kind === 'statusApplied' && e.stat === 'atk_pct' && e.value === 20), '推了 statusApplied 事件')

  // 同一顆技能（同 sourceSkillId）在到期前重複命中同一個 stat → 刷新（取代），不會變成兩筆疊加。
  s = tick(s, 520) // recovering(400ms) 過後回 idle
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'war_cry' }, 521)
  s = tick(s, 521 + 120)
  eq(s.party[0].activeEffects.length, 1, '同一顆技能重複命中同一個 stat → 刷新成一筆，不疊加成兩筆')
  eq(s.party[0].activeEffects[0].expiresAt, 521 + 120 + 1000, '刷新後 expiresAt 更新成最新一次施放的到期時間')

  // 生效期間：普攻傷害要吃到 atk_pct+20% 加成（即使剛才刷新過，仍只有一份 20%，不是疊成 40%）。
  s = tick(s, 521 + 120 + DEFAULT_BATTLE_CONFIG.recoveryMs)
  const beforeHp = s.enemies[0].hp
  const atkAt = 521 + 120 + DEFAULT_BATTLE_CONFIG.recoveryMs + 1
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, atkAt)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, atkAt)
  eq(beforeHp - s.enemies[0].hp, Math.round(135 * 1.2) - 35, 'buff 生效期間（即使剛刷新過）普攻仍只吃到單一份 atk_pct+20% 加成')

  // 到期：tick 到期前一刻仍在，到期當下自動移除並推 statusExpired 事件。
  const expiresAt = s.party[0].activeEffects[0].expiresAt
  const before = tick(s, expiresAt - 1)
  ok(before.party[0].activeEffects.some((e) => e.stat === 'atk_pct'), 'buff 到期前一刻仍在')
  const after = tick(before, expiresAt)
  eq(after.party[0].activeEffects.length, 0, 'buff 到期後自動移除')
  ok(after.events.some((e) => e.kind === 'statusExpired' && e.stat === 'atk_pct'), '到期時推了 statusExpired 事件')
}

// ── 57) 端到端：debuff（def_pct）套用在敵人身上使其承受更多傷害；debuff target='allEnemies' 命中全體存活敵人 ──
{
  const sample = makeSample({
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 5000, hpMax: 5000, slot: 'front_center', imageUrl: '', stats: { hpMax: 5000, mpMax: 0, atk: 50, matk: 50, def: 100, mdef: 20 } }],
    skills: [
      { id: 'slash', name: '斬擊', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1, flat: 0, weapon: 'sword', castMs: 100 },
      {
        id: 'armor_break', name: '破甲箭', iconUrl: '', cooldownMs: 0, kind: 'debuff', target: 'enemy', mpCost: 12, coefficient: 0, flat: 0, weapon: 'bow', castMs: 100,
        effect: { kind: 'debuff', stat: 'def_pct', value: -50, durationMs: 5000, target: 'enemy', mpCost: 12 },
      },
      null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'armor_break' }, 0) // target='enemy' 直接吃 ctx.targetId
  s = tick(s, 120)
  eq(s.enemies[0].activeEffects.length, 1, 'debuff 命中後敵人身上多一筆 activeEffects')
  eq(
    s.enemies[0].activeEffects[0],
    { stat: 'def_pct', value: -50, expiresAt: 5120, sourceSkillId: 'armor_break', kind: 'debuff' }, // 120＋durationMs(5000)
    'ActiveEffect 內容正確',
  )

  s = tick(s, 120 + DEFAULT_BATTLE_CONFIG.recoveryMs)
  const beforeHp = s.enemies[0].hp
  const atkAt = 120 + DEFAULT_BATTLE_CONFIG.recoveryMs + 1
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, atkAt)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, atkAt)
  // def 套 -50% → round(100*0.5)=50；raw=floor(135×1)=135；135-50=85（沒有 debuff 的話會是 135-100=35）。
  eq(beforeHp - s.enemies[0].hp, 135 - 50, 'debuff def_pct=-50% 讓敵人有效防禦減半，承受更多傷害')
}
{
  const sample = makeSample({
    enemies: [
      { id: 'e1', name: '甲', level: 1, hp: 500, hpMax: 500, slot: 'front_left', imageUrl: '', stats: { hpMax: 500, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } },
      { id: 'e2', name: '乙', level: 1, hp: 500, hpMax: 500, slot: 'front_center', imageUrl: '', stats: { hpMax: 500, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } },
    ],
    initialTargetId: 'e1',
    skills: [
      {
        id: 'war_drums', name: '戰鼓', iconUrl: '', cooldownMs: 0, kind: 'debuff', target: 'allEnemies', mpCost: 20, coefficient: 0, flat: 0, weapon: 'sword', castMs: 100,
        effect: { kind: 'debuff', stat: 'atk_pct', value: -30, durationMs: 5000, target: 'allEnemies', mpCost: 20 },
      },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'war_drums' }, 0)
  s = tick(s, 120)
  ok(s.enemies.every((e) => e.activeEffects.some((x) => x.stat === 'atk_pct' && x.value === -30)), 'debuff target=allEnemies 命中全體存活敵人')
  eq(s.events.filter((e) => e.kind === 'statusApplied').length, 2, 'allEnemies debuff 對 2 隻敵人各推一筆 statusApplied 事件')
}

// ── 58) 端到端：special（implemented=false）技能被直接拒絕並發 skillUnavailable 事件；
//        passive 若誤入技能欄，USE_SKILL 同樣被拒絕（防呆分支，正常情況不會發生） ──
{
  const sample = makeSample({
    skills: [
      {
        id: 'merchants_intuition', name: '商人的直覺', iconUrl: '', cooldownMs: 0, kind: 'special', target: 'self', mpCost: 10, coefficient: 0, flat: 0, weapon: 'sword', castMs: 0,
        implemented: false,
      },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  const beforeMp = s.party[0].mp
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'merchants_intuition' }, 0)
  eq(s.party[0].mp, beforeMp, 'implemented=false 的技能被拒絕，不扣 MP')
  eq(s.party[0].action, 'idle', 'implemented=false 的技能被拒絕，不會進入 casting')
  ok(s.events.some((e) => e.kind === 'skillUnavailable' && e.skillId === 'merchants_intuition'), '推了 skillUnavailable 事件')
  ok(s.log.some((l) => l.includes('拒絕')), '也留了一般拒絕 log')
}
{
  const sample = makeSample({
    skills: [
      { id: 'fortitude', name: '堅毅', iconUrl: '', cooldownMs: 0, kind: 'passive', target: 'self', mpCost: 0, coefficient: 0, flat: 0, weapon: 'sword', castMs: 0 },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'fortitude' }, 0)
  eq(s.party[0].action, 'idle', 'passive 技能萬一出現在技能欄，USE_SKILL 一樣被拒絕（防呆分支）')
}

// ── 59) 端到端：buff（hp_regen_pct）每 1000ms 定時回復一次，不是套用當下立刻回一次 ──
{
  const sample = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 100, hpMax: 1000, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 1000, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } }],
    skills: [
      {
        id: 'regen_aura', name: '生命之泉', iconUrl: '', cooldownMs: 0, kind: 'buff', target: 'self', mpCost: 20, coefficient: 0, flat: 0, weapon: 'staff', castMs: 100,
        effect: { kind: 'buff', stat: 'hp_regen_pct', value: 10, durationMs: 3000, target: 'self', mpCost: 20 },
      },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'regen_aura' }, 0)
  s = tick(s, 120) // 效果套用時刻＝120，hp_regen_pct 的 nextTickAt 從「120+1000」開始算，不是套用當下
  eq(s.party[0].hp, 100, '套用當下還沒到第一次回復時機，血量不變')

  s = tick(s, 120 + 999)
  eq(s.party[0].hp, 100, '未滿 1000ms 前不會回復（此刻在 1119，nextTickAt=1120 還沒到）')

  s = tick(s, 120 + 1000)
  eq(s.party[0].hp, 100 + Math.round(1000 * 0.1), '滿 1000ms（nextTickAt=1120）→ 回復一次，量＝round(hpMax×10%)=100')
  ok(s.events.some((e) => e.kind === 'heal' && e.actorId === 'player' && e.targetId === 'player'), '回復透過既有的 heal 事件通知（自我回復，actorId=targetId）')

  s = tick(s, 120 + 2000)
  eq(s.party[0].hp, 100 + 2 * Math.round(1000 * 0.1), '第二次 1000ms 窗口再回復一次，累計回復兩次')
}

// ── 60) 端到端：buff（damage_taken_pct）降低承受傷害（詞彙表只在 buff 那邊列出，debuff 沒有這一項，見 BuffDebuffStat 型別註解） ──
{
  const sample = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 0, mdef: 28 } }],
    enemies: [{ id: 'e1', name: '打手', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', stats: { hpMax: 999, mpMax: 0, atk: 100, matk: 0, def: 0, mdef: 0 } }],
    skills: [
      {
        id: 'iron_skin', name: '鐵壁之心', iconUrl: '', cooldownMs: 0, kind: 'buff', target: 'self', mpCost: 15, coefficient: 0, flat: 0, weapon: 'sword', castMs: 100,
        effect: { kind: 'buff', stat: 'damage_taken_pct', value: -50, durationMs: 5000, target: 'self', mpCost: 15 },
      },
      null, null, null, null, null, null, null, null, null,
    ],
  })
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] } })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'iron_skin' }, 0)
  s = tick(s, 120) // 施法完成套用 buff；同一個 tick 也讓敵人 idle→windup（nextActAt=0 早已到）
  s = tick(s, 120 + DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking，結算傷害
  const ev = s.events.find((e) => e.kind === 'enemyAttack')
  // raw damage = max(1, 100-0) = 100；enemyAttack 事件記錄的是「套用 damage_taken_pct 之前」的完整傷害
  // （damage_taken_pct 是在 applyPartyDamage 才套用，屬於「扣血」那一步，不是「算傷害」那一步）。
  eq(ev.damage, 100, 'enemyAttack 事件本身仍是完整傷害（damage_taken_pct 不影響事件記錄的原始傷害數字）')
  eq(s.party[0].hp, 800 - 50, 'damage_taken_pct=-50% 讓玩家實際扣血減半（100→round(100×0.5)=50）')
}

// ── 61) 端到端（審查#5【低・PLAUSIBLE】回歸）：CombatRating.critDmgPct 疊在浮動暴擊倍率之上——
//        暴擊發生時傷害對應變大，且落在 [critMultMin×(1+critDmgPct/100), critMultMax×(1+critDmgPct/100)] ──
{
  const cfg = { ...FAR_CONFIG, critRate: 1 } // critPct 恆 100%，攻擊必定暴擊
  const sample = makeSample({
    party: [
      {
        id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100,
        portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 },
        weapon: 'sword',
        // critDmgPct=50 → 暴擊倍率額外 ×1.5，跟 rollCritMultiplier 抽到的 [1.75,2.25] 疊乘。
        rating: { hit: 999, flee: 0, critPct: 100, critShield: 0, aspd: 150, castReductionPct: 0, critDmgPct: 50 },
      },
    ],
  })
  // rng 消耗順序同第 52 號測試：第 0 格墊給建場消耗、第 1 格 miss 判定、第 2 格 crit 判定、
  // 第 3 格暴擊倍率抽樣。
  let low = createBattle(sample, { now: 0, config: cfg, rng: fixedRng([0, 0, 0, 0]) })
  low = dispatch(low, { type: 'ATTACK_BEGIN' }, 0)
  low = dispatch(low, { type: 'ATTACK_RELEASE' }, 0)
  const lowEv = low.events.find((e) => e.kind === 'attack')
  // rng=0 → rollCritMultiplier 抽到下限 1.75；critDmgPct=50 → 總倍率 1.75×1.5=2.625。
  eq(lowEv.damage, Math.floor(135 * 1.75 * 1.5) - 35, 'critDmgPct=50 疊在暴擊倍率下限 1.75 之上（×1.5），傷害對應變大於沒有這個加成時的 201（floor(135×1.75)-35）')

  let high = createBattle(sample, { now: 0, config: cfg, rng: fixedRng([0, 0, 0, 0.999999]) })
  high = dispatch(high, { type: 'ATTACK_BEGIN' }, 0)
  high = dispatch(high, { type: 'ATTACK_RELEASE' }, 0)
  const highEv = high.events.find((e) => e.kind === 'attack')
  // rng≈1 → rollCritMultiplier 抽到上限 2.25；critDmgPct=50 → 總倍率 2.25×1.5=3.375。
  eq(highEv.damage, Math.floor(135 * 2.25 * 1.5) - 35, 'critDmgPct=50 疊在暴擊倍率上限 2.25 之上（×1.5）')

  ok(
    lowEv.damage >= Math.floor(135 * 1.75 * 1.5) - 35 && highEv.damage <= Math.floor(135 * 2.25 * 1.5) - 35,
    '整場傷害落在 [critMultMin×1.5, critMultMax×1.5] 對應的傷害區間內',
  )

  // 對照組：critDmgPct=0（未提供該欄位，effectiveRating 缺省視為 0）時應回到沒有加成的既有傷害。
  const sampleNoBonus = makeSample({
    party: [
      {
        id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100,
        portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 },
        weapon: 'sword', rating: { hit: 999, flee: 0, critPct: 100, critShield: 0, aspd: 150, castReductionPct: 0 },
      },
    ],
  })
  let noBonus = createBattle(sampleNoBonus, { now: 0, config: cfg, rng: fixedRng([0, 0, 0, 0]) })
  noBonus = dispatch(noBonus, { type: 'ATTACK_BEGIN' }, 0)
  noBonus = dispatch(noBonus, { type: 'ATTACK_RELEASE' }, 0)
  const noBonusEv = noBonus.events.find((e) => e.kind === 'attack')
  eq(noBonusEv.damage, Math.floor(135 * 1.75) - 35, '沒有 critDmgPct 的對照組回到既有傷害（跟第 52 號測試一致），確認新加成不是恆定套用')
}

// ════════════════════════════════════════════════════════════════════════════
// P6（DORPG 第六輪：HP/MP 整數不變式、隊友 AI 五段優先序、level 模式怪物公式）新增測試：34 號起。
// ════════════════════════════════════════════════════════════════════════════

// ── 34) CONTRACT §1：HP/MP 整數不變式——pct buff／hp_regen／heal coef／multi-hit／
//        damage_taken_pct 混雜的隨機序列跑完一整場戰鬥後，所有 party/enemy 的
//        hp/mp/hpMax/mpMax/shield 全部仍是整數（Number.isInteger）。 ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 777, hpMax: 777, mp: 50, mpMax: 50, portraitUrl: null, stats: { hpMax: 777, mpMax: 50, atk: 135, matk: 83, def: 35, mdef: 28 } },
      {
        id: 'ally', name: '夥伴', level: 56, hp: 555, hpMax: 555, mp: 50, mpMax: 50, portraitUrl: null,
        stats: { hpMax: 555, mpMax: 50, atk: 41, matk: 83, def: 20, mdef: 20 }, weapon: 'staff',
        skills: [
          // heal：coefficient/flat 刻意用非整十數字，逼 computeHeal 的 floor 派上用場。
          { id: 'heal2', name: '雜訊治療', iconUrl: '', cooldownMs: 0, kind: 'heal', target: 'ally', mpCost: 3, coefficient: 0.31, flat: 11, weapon: 'staff', castMs: 0 },
          // buff：hp_regen_pct（pct buff／regen）。
          { id: 'regen_buff', name: '回氣', iconUrl: '', cooldownMs: 5000, kind: 'buff', target: 'self', mpCost: 2, coefficient: 0, flat: 0, weapon: 'staff', castMs: 0, effect: { kind: 'buff', stat: 'hp_regen_pct', value: 7, durationMs: 100000, target: 'self', mpCost: 2 } },
          // buff：damage_taken_pct（打到 player 身上，逼 applyPartyDamage 的 floor(rawDamage×dtMul) 派上用場）。
          { id: 'dt_shield', name: '減傷姿態', iconUrl: '', cooldownMs: 5000, kind: 'buff', target: 'ally', mpCost: 2, coefficient: 0, flat: 0, weapon: 'staff', castMs: 0, effect: { kind: 'buff', stat: 'damage_taken_pct', value: -33, durationMs: 100000, target: 'ally', mpCost: 2 } },
          // multi-hit damage：coefficient 非整數，逼 computeRawDamage 的 floor 逐段派上用場。
          { id: 'combo', name: '連段', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 3, coefficient: 0.37, flat: 7, weapon: 'sword', castMs: 0, hits: 3 },
        ],
      },
    ],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 5000, hpMax: 5000, slot: 'front_center', imageUrl: '', stats: { hpMax: 5000, mpMax: 0, atk: 31, matk: 31, def: 13, mdef: 9 } }],
  })
  const cfg = {
    attackCooldownMs: 1500, chargeMinMs: 300, chargeFullMs: 1200, chargeMaxMultiplier: 2.5, guardDamageMultiplier: 0.4,
    recoveryMs: 400, defaultCastMs: 500, escapeJudgeMs: 1200,
    enemyActIntervalMs: [300, 700], enemyWindupMs: 100, enemyAttackMs: 100, enemyHitMs: 100, enemyDeathMs: 100,
    allyActIntervalMs: [300, 700],
    hitRate: 1, critRate: 0.3, critMultiplier: 2, critMultMin: 1.6, critMultMax: 2.4, weaknessBonusPct: 25, resolveDelayMs: 100,
    baseMissPct: 5, hitFleeScale: 0.35, missMinPct: 2, missMaxPct: 35,
    monsterHitBase: 2, monsterFleeBase: 2, monsterCritPct: 5, monsterCritShieldBase: 0,
    elementChart: {}, monsterHitPerLevel: 1, monsterHitMax: 75, monsterFleePerLevel: 1,
    aspdReference: 150, attackCooldownMinMs: 700, castMinMs: 120, scaleMode: 'power',
  }
  // 偽隨機序列（0..1 均勻分布，跨多個 tick 循環使用，足夠覆蓋 miss/crit/暴擊倍率抽樣等各種分支）。
  const seq = [0.02, 0.91, 0.33, 0.58, 0.77, 0.14, 0.49, 0.66, 0.05, 0.88, 0.21, 0.44, 0.7, 0.09, 0.95, 0.37, 0.62, 0.18, 0.83, 0.5]
  let s = createBattle(sample, { now: 0, config: cfg, rng: fixedRng(seq) })
  let allInts = true
  const checkInts = (state) => {
    for (const p of state.party) {
      if (!Number.isInteger(p.hp) || !Number.isInteger(p.mp) || !Number.isInteger(p.stats.hpMax) || !Number.isInteger(p.stats.mpMax) || !Number.isInteger(p.shield)) allInts = false
    }
    for (const e of state.enemies) {
      if (!Number.isInteger(e.hp) || !Number.isInteger(e.stats.hpMax)) allInts = false
    }
  }
  checkInts(s)
  for (let t = 0; t < 60 && s.phase === 'active'; t++) {
    s = tick(s, (t + 1) * 250)
    checkInts(s)
  }
  ok(allInts, 'P6 CONTRACT §1：pct buff／hp_regen／heal coef／multi-hit／damage_taken_pct 混合的隨機序列跑完後，party/enemy 的 hp/mp/hpMax/mpMax/shield 全部是整數')
  ok(s.seq > 0, '（前提）這場戰鬥真的發生過事件，不是整場都卡在拒絕指令的空轉')
}

// ── 35) CONTRACT §3.2 五段優先序②：buff 已生效就不重複施放，改走⑤普攻 fallback ──
{
  const buffSkill = { id: 'atkup', name: '鼓舞', iconUrl: '', cooldownMs: 0, kind: 'buff', target: 'self', mpCost: 5, coefficient: 0, flat: 0, weapon: 'sword', castMs: 0, effect: { kind: 'buff', stat: 'atk_pct', value: 20, durationMs: 8000, target: 'self', mpCost: 5 } }
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'buffer', name: '輔助', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 }, weapon: 'sword', skills: [buffSkill] },
    ],
  })
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s = tick(s, 0)
  const firstCast = s.events.find((e) => e.kind === 'skillCast' && e.actorId === 'buffer')
  ok(!!firstCast && firstCast.skillId === 'atkup', 'buffer 身上還沒有 atk_pct 時，施放 buff')
  ok(s.party.find((p) => p.id === 'buffer').activeEffects.some((e) => e.stat === 'atk_pct'), 'buff 真的套用在自己身上')

  s = tick(s, 400) // recoveryMs=400 結束→idle，同一個 tick 內緊接著讓 AI 再次判斷。
  const repeatCasts = s.events.filter((e) => e.kind === 'skillCast' && e.actorId === 'buffer' && e.skillId === 'atkup')
  eq(repeatCasts.length, 1, 'buff 已經生效中（且 cooldownMs=0，冷卻早就歸零）就不會重複施放')
  ok(s.events.some((e) => e.kind === 'attack' && e.actorId === 'buffer'), '不重複施放後改走⑤普攻 fallback（沒有其它可用技能）')
}

// ── 36) CONTRACT §3.2 五段優先序③：damage 技能都可用時挑「tier 最高」的那顆 ──
{
  // 36a：兩顆都帶明確 tier，數字大的優先（跟陣列順序無關——dmg_low 放前面，tier 卻比較小）。
  const dmgLow = { id: 'dmg_low', name: '弱擊', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.0, flat: 0, weapon: 'sword', castMs: 0, tier: 1 }
  const dmgHigh = { id: 'dmg_high', name: '強擊', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 2.0, flat: 0, weapon: 'sword', castMs: 0, tier: 2 }
  const sampleA = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'dps', name: '輸出', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 80, matk: 80, def: 20, mdef: 20 }, weapon: 'sword', skills: [dmgLow, dmgHigh] },
    ],
  })
  let sA = createBattle(sampleA, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  sA = tick(sA, 0)
  const castA = sA.events.find((e) => e.kind === 'skillCast' && e.actorId === 'dps')
  eq(castA?.skillId, 'dmg_high', 'tier:2 的 dmg_high 優先於 tier:1 的 dmg_low，即使 dmg_low 排在陣列前面')

  // 36b：都沒有明確 tier 時，退回陣列位置代理值（陣列越後面視為越高 tier）。
  const dmgFirst = { id: 'dmg_first', name: '第一顆', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.0, flat: 0, weapon: 'sword', castMs: 0 }
  const dmgSecond = { id: 'dmg_second', name: '第二顆', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.5, flat: 0, weapon: 'sword', castMs: 0 }
  const sampleB = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'dps2', name: '輸出二', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 80, matk: 80, def: 20, mdef: 20 }, weapon: 'sword', skills: [dmgFirst, dmgSecond] },
    ],
  })
  let sB = createBattle(sampleB, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  sB = tick(sB, 0)
  const castB = sB.events.find((e) => e.kind === 'skillCast' && e.actorId === 'dps2')
  eq(castB?.skillId, 'dmg_second', '都沒有 tier 欄位時，退回陣列位置代理（陣列後面的 dmg_second 視為 tier 較高）')
}

// ── 37) CONTRACT §3.2 五段優先序④：debuff 已生效就不重複施放，改走⑤普攻 fallback ──
{
  const debuffSkill = { id: 'weaken', name: '破防', iconUrl: '', cooldownMs: 0, kind: 'debuff', target: 'enemy', mpCost: 5, coefficient: 0, flat: 0, weapon: 'bow', castMs: 0, effect: { kind: 'debuff', stat: 'def_pct', value: -20, durationMs: 8000, target: 'enemy', mpCost: 5 } }
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'debuffer', name: '削弱', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 }, weapon: 'bow', skills: [debuffSkill] },
    ],
  })
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s = tick(s, 0)
  const firstCast = s.events.find((e) => e.kind === 'skillCast' && e.actorId === 'debuffer')
  ok(!!firstCast && firstCast.skillId === 'weaken', '敵人身上還沒有 def_pct 時，debuffer 施放 debuff')
  ok(s.enemies[0].activeEffects.some((e) => e.stat === 'def_pct'), 'debuff 真的套用在敵人身上')

  s = tick(s, 400)
  const repeatCasts = s.events.filter((e) => e.kind === 'skillCast' && e.actorId === 'debuffer' && e.skillId === 'weaken')
  eq(repeatCasts.length, 1, 'debuff 已經生效中就不會重複施放')
  ok(s.events.some((e) => e.kind === 'attack' && e.actorId === 'debuffer'), '不重複施放後改走⑤普攻 fallback')
}

// ── 38) CONTRACT §3.2 五段優先序⑤：skills=[]（沒有可用技能）的隊友直接普攻 fallback ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'plain', name: '雜兵', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 }, weapon: 'sword', skills: [] },
    ],
  })
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s = tick(s, 0)
  ok(s.events.some((e) => e.kind === 'attack' && e.actorId === 'plain'), 'skills=[]（沒有可用技能）的隊友直接普攻 fallback，跟 P1 舊行為相容')
}

// ── 39) CONTRACT §2：level 模式怪物公式——用自造的 RefPlayerTable（不等真正的 refPlayerTable.json，
//        見下方 40 號區塊）鎖定 scaleMonsterFromRef 的精確算式：floor 順序／各倍率相乘順序。
//        數值本身由本測試獨立算好寫死（不是從 fixture.ts 抄一份運算式），若實作漂移會被抓到。 ──
{
  const refTable = [{ level: 10, hpMax: 999, mpMax: 199, atk: 101, matk: 77, def: 53, mdef: 41, hit: 88, flee: 12, aspd: 140 }]
  // training_ground（monsterLevel=10, powerScale=0.60）：front_left=DOR-MON-D-0182(hpMult.9/atkMult1.0/defMult1.0)，
  // front_center/front_right=DOR-MON-E-0052(hpMult.6/atkMult0.8/defMult0.8)，三隻 powerScale 皆 1。
  const bundle = buildFixtureSample('training_ground', { mode: 'level', refTable })
  const byId = Object.fromEntries(bundle.sample.enemies.map((e) => [e.id, e]))

  eq(byId['enemy_front_left'].level, 10, 'level 模式：怪物顯示等級＝encounter.monsterLevel（不套用 power 模式的 BOSS 等級加成）')
  eq(
    { hp: byId['enemy_front_left'].hpMax, atk: byId['enemy_front_left'].stats.atk, def: byId['enemy_front_left'].stats.def, mdef: byId['enemy_front_left'].stats.mdef, matk: byId['enemy_front_left'].stats.matk },
    { hp: 431, atk: 75, def: 31, mdef: 24, matk: 77 },
    // 2026-09-18 SIM 校準後 DEFAULT_LEVEL_SCALE_CONFIG＝hp 0.8／atk 1.25／def 1.0／mdef 1.0（config.go DefaultConfig 同步），期望值依此重算。
    'DOR-MON-D-0182（hpMult.9/atkMult1.0/defMult1.0）：floor(999×.9×.8×.6)=431、floor(101×1×1.25×.6)=75、floor(53×1×1×.6)=31、floor(41×1×1×.6)=24、floor(77×1)=77',
  )
  eq(
    { hp: byId['enemy_front_center'].hpMax, atk: byId['enemy_front_center'].stats.atk, def: byId['enemy_front_center'].stats.def, mdef: byId['enemy_front_center'].stats.mdef, matk: byId['enemy_front_center'].stats.matk },
    { hp: 287, atk: 60, def: 25, mdef: 19, matk: 61 },
    'DOR-MON-E-0052（hpMult.6/atkMult0.8/defMult0.8）：floor(999×.6×.8×.6)=287、floor(101×.8×1.25×.6)=60、floor(53×.8×1×.6)=25、floor(41×.8×1×.6)=19、floor(77×.8)=61',
  )
  ok(Number.isInteger(byId['enemy_front_left'].hpMax) && Number.isInteger(byId['enemy_front_center'].hpMax), 'level 模式算出的怪物 hp 也是整數（跟 CONTRACT §1 的整數不變式一致）')

  // 對照：沒有 refTable 時 mode='level' 安全退回 power 模式（不會 throw、不會用到 undefined 的 ref）。
  const fallback = buildFixtureSample('training_ground', { mode: 'level', refTable: null })
  ok(fallback.sample.enemies.length === 3, "mode='level' 但沒有 refTable 時安全退回 power 模式（不 throw）")
}

// ── 40) 若 refPlayerTable.json 已經由 BACKEND 產生，額外核對表本身的基本形狀（N=1/27/99 存在、
//        數值遞增且為正）；檔案還不存在時印一行訊息略過，不算失敗（見任務回報）。 ──
{
  const table = await loadRefPlayerTable()
  if (!table) {
    console.log('SKIP 40) refPlayerTable.json 尚未由 BACKEND 產生，略過真表核對（不計入 pass/fail）')
  } else {
    const lv1 = table.find((r) => r.level === 1)
    const lv27 = table.find((r) => r.level === 27)
    const lv99 = table.find((r) => r.level === 99)
    ok(!!lv1 && !!lv27 && !!lv99, 'refPlayerTable.json 涵蓋 N=1/27/99（契約 §2：N=1..99 全部有值）')
    if (lv1 && lv27 && lv99) {
      ok(lv27.hpMax > lv1.hpMax && lv99.hpMax > lv27.hpMax, '等級越高 HPMax 越大（單調遞增）')
      ok(lv1.hpMax > 0 && lv1.atk >= 0 && lv1.def >= 0, 'Lv1 的衍生值都是非負數')
    }
  }
}

// ════════════════════════════════════════════════════════════════════════════
// P7（DORPG_P7 CONTRACT：武器系統＋怪物體型/屬性——ENGINE 角色）新增測試：本節從 1) 重新編號
// （跟前面 P6 區塊的編號規則一致，各大階段各自一組序號，見檔案上方 P6 區塊同款做法）。
// 共用小工具：weaponFixture(overrides) 從 NEUTRAL_WEAPON_PROFILE 疊上要測試的欄位，包成
// PartyMember.equippedWeapon 期待的形狀，避免每個測試都要手key 15 個欄位。
// ════════════════════════════════════════════════════════════════════════════

function weaponFixture(typeId, visual, overrides) {
  return { id: `${typeId}_test`, name: typeId, typeId, visual, profile: { ...NEUTRAL_WEAPON_PROFILE, ...overrides } }
}

// ── 1) 雙劍：hits=2、hitMul=0.6——攻擊次數變兩次，每段獨立造成 60% 傷害（不是總傷害不變只是拆段）。──
{
  const dual = weaponFixture('lk_dual', 'sword', { hits: 2, hitMul: 0.6 })
  const sample = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 }, equippedWeapon: dual }],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const hits = s.events.filter((e) => e.kind === 'attack' && e.actorId === 'player')
  eq(hits.length, 2, '雙劍：二刀流，一次普攻產生 2 段獨立 attack 事件')
  const expected = Math.floor(100 * 0.6) - 35 // computeRawDamage(100,0.6,0,1,1,1,35)
  ok(hits.every((h) => h.damage === expected), `雙劍：每段各自用 hitMul=0.6 算傷害＝${expected}（實際：${hits.map((h) => h.damage).join(',')}）`)
  eq(s.enemies[0].hp, 5000 - expected * 2, '雙劍：兩段傷害各自扣血，總扣血量＝2×單段傷害')
  ok(Number.isInteger(s.enemies[0].hp), '雙劍：扣血後仍是整數（整數不變式）')
}

// ── 2) 槍：hits=2/hitMul=0.5，且 extraHitChancePct 決定第三段——固定種子下可預期第三段觸發與否。──
{
  const spear = (extraPct) => weaponFixture('hk_spear', 'sword', { hits: 2, hitMul: 0.5, extraHitChancePct: extraPct })
  const sample = (extraPct) =>
    makeSample({
      party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 }, equippedWeapon: spear(extraPct) }],
    })
  // rng 消耗順序（FAR_CONFIG：missPct 恆 0／critPct 恆 0，見區塊 52 同款說明）：
  //   idx0＝createBattle 敵人 nextActAt 的 randRange（NEVER 區間，值不重要）
  //   idx1＝resolveWeaponAttack 的「要不要多打一段」判定：rng()<extraHitChancePct/100
  //   idx2/3＝第 1 段 miss/crit 判定，idx4/5＝第 2 段，（若觸發第三段）idx6/7＝第 3 段
  let triggered = createBattle(sample(30), { now: 0, config: FAR_CONFIG, rng: fixedRng([0, 0.1, 0, 0, 0, 0, 0, 0]) })
  triggered = dispatch(triggered, { type: 'ATTACK_BEGIN' }, 0)
  triggered = dispatch(triggered, { type: 'ATTACK_RELEASE' }, 0)
  const hitsA = triggered.events.filter((e) => e.kind === 'attack' && e.actorId === 'player')
  eq(hitsA.length, 3, '槍：固定種子 rng=0.1 < extraHitChancePct(30%) → 觸發第三段，共 3 段命中')
  const expectedSpear = Math.floor(100 * 0.5) - 35
  ok(hitsA.every((h) => h.damage === expectedSpear), `槍：每段 hitMul=0.5，傷害皆為 ${expectedSpear}`)

  let notTriggered = createBattle(sample(30), { now: 0, config: FAR_CONFIG, rng: fixedRng([0, 0.9, 0, 0, 0, 0]) })
  notTriggered = dispatch(notTriggered, { type: 'ATTACK_BEGIN' }, 0)
  notTriggered = dispatch(notTriggered, { type: 'ATTACK_RELEASE' }, 0)
  const hitsB = notTriggered.events.filter((e) => e.kind === 'attack' && e.actorId === 'player')
  eq(hitsB.length, 2, '槍：固定種子 rng=0.9 ≥ extraHitChancePct(30%) → 不觸發第三段，只有 2 段命中')

  // extraHitChancePct=0（雙劍／一般武器）完全不消耗這格 rng，既有測試序列不受擾動。
  let neutral = createBattle(makeSample(), { now: 0, config: FAR_CONFIG, rng: fixedRng([0, 0]) })
  neutral = dispatch(neutral, { type: 'ATTACK_BEGIN' }, 0)
  neutral = dispatch(neutral, { type: 'ATTACK_RELEASE' }, 0)
  eq(neutral.events.filter((e) => e.kind === 'attack').length, 1, '無 extraHitChancePct 的武器（含無武器）完全不消耗額外一次 rng()，單擊行為不變')
}

// ── 3) 斧：intervalPct 拉長冷卻＋splashPct 濺射同排相鄰存活敵人，且不濺後排。──
{
  const axe = weaponFixture('hk_axe', 'greatsword', { intervalPct: 25, splashPct: 40 })
  const sample = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 }, equippedWeapon: axe }],
    enemies: [
      { id: 'center', name: '中', level: 1, hp: 9999, hpMax: 9999, slot: 'front_center', imageUrl: '', stats: { hpMax: 9999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } },
      { id: 'left', name: '左', level: 1, hp: 9999, hpMax: 9999, slot: 'front_left', imageUrl: '', stats: { hpMax: 9999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } },
      { id: 'right', name: '右', level: 1, hp: 9999, hpMax: 9999, slot: 'front_right', imageUrl: '', stats: { hpMax: 9999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } },
      { id: 'rearL', name: '後左', level: 1, hp: 9999, hpMax: 9999, slot: 'rear_left', imageUrl: '', stats: { hpMax: 9999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } },
    ],
    initialTargetId: 'center',
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const byId = Object.fromEntries(s.enemies.map((e) => [e.id, e]))
  eq(9999 - byId.center.hp, 100, '斧：主擊傷害＝floor(100*1)-0=100')
  eq(9999 - byId.left.hp, 40, '斧濺射：front_left（同排相鄰）受主擊 40%＝floor(100*0.4)=40')
  eq(9999 - byId.right.hp, 40, '斧濺射：front_right（同排相鄰）受主擊 40%＝floor(100*0.4)=40')
  eq(byId.rearL.hp, 9999, '斧濺射不濺後排：rear_left 完全不受影響（前後排不相鄰）')
  ok(
    Number.isInteger(byId.center.hp) && Number.isInteger(byId.left.hp) && Number.isInteger(byId.right.hp),
    '斧：主擊與濺射扣血後皆為整數（整數不變式）',
  )
  const splashEvents = s.events.filter((e) => e.kind === 'attack' && e.splash === true)
  eq(splashEvents.length, 2, '濺射各自推一筆獨立事件（左右各一）且帶 splash:true 供浮字區分')
  ok(
    s.events.some((e) => e.kind === 'attack' && e.actorId === 'player' && e.targetId === 'center' && !e.splash),
    '主擊事件本身沒有 splash 旗標（只有濺射命中才標記）',
  )
  eq(
    attackCooldownFor({ hit: 0, flee: 0, critPct: 0, critShield: 0, aspd: DEFAULT_BATTLE_CONFIG.aspdReference, castReductionPct: 0 }, DEFAULT_BATTLE_CONFIG, 25),
    DEFAULT_BATTLE_CONFIG.attackCooldownMs * 1.25,
    '斧：intervalPct=+25（間隔拉長）→ 冷卻變成 base×1.25',
  )
}

// ── 4) 細劍／長弓：intervalPct 縮短/拉長攻擊冷卻——直接驗證公式，再用細劍端到端驗證 dispatch 真的接線。──
{
  const neutralRating = { hit: 0, flee: 0, critPct: 0, critShield: 0, aspd: DEFAULT_BATTLE_CONFIG.aspdReference, castReductionPct: 0 }
  eq(
    attackCooldownFor(neutralRating, DEFAULT_BATTLE_CONFIG, -20),
    DEFAULT_BATTLE_CONFIG.attackCooldownMs * 0.8,
    '細劍：intervalPct=-20（傳說級稀有度）→ 冷卻縮短成 base×0.8',
  )
  eq(
    attackCooldownFor(neutralRating, DEFAULT_BATTLE_CONFIG, 20),
    DEFAULT_BATTLE_CONFIG.attackCooldownMs * 1.2,
    '長弓：intervalPct=+20 → 冷卻拉長成 base×1.2',
  )
  eq(attackCooldownFor(neutralRating, DEFAULT_BATTLE_CONFIG, 0), DEFAULT_BATTLE_CONFIG.attackCooldownMs, 'intervalPct=0（無武器/預設）→ 冷卻不變，跟舊行為一致')

  const rapier = weaponFixture('lk_rapier', 'sword', { intervalPct: -20, sizeBonus: { small: 5, medium: 0, large: 0 } })
  const sample = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, equippedWeapon: rapier }],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  eq(s.party[0].attackReadyAt, DEFAULT_BATTLE_CONFIG.attackCooldownMs * 0.8, '端到端：裝備細劍後 dispatch 算出的 attackReadyAt 真的反映縮短的冷卻，不只是公式本身')
}

// ── 5) 巨劍：chargeTimeMul 拉長滿蓄時間、chargeDmgMul 加倍蓄氣倍率超出 1 的部分。──
{
  eq(chargeMultiplier(1500, DEFAULT_BATTLE_CONFIG, 1.5), 2, '巨劍：chargeTimeMul=1.5 讓蓄氣時間變長，同樣持有 1500ms 只蓄到原本 2/3 進度 → 倍率降為 2.0（不是無武器時的 2.5）')
  eq(chargeMultiplier(300 + 1200 * 1.5, DEFAULT_BATTLE_CONFIG, 1.5), 2.5, '巨劍：等到真正的滿蓄時間（chargeMinMs+chargeFullMs×1.5=2100ms）才蓄滿 2.5 倍')

  const greatsword = weaponFixture('hk_greatsword', 'greatsword', { chargeTimeMul: 1.5, chargeDmgMul: 2, sizeBonus: { small: 0, medium: 0, large: 5 } })
  const sample = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 }, equippedWeapon: greatsword }],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 300 + 1200 * 1.5) // 巨劍自己的滿蓄時間（2100ms）
  const ev = s.events.find((e) => e.kind === 'attack')
  // rawChargeMul(滿蓄,chargeTimeMul=1.5)=2.5；weapon-adjusted=1+(2.5-1)*chargeDmgMul(2)=4
  // net=floor(100*1*1*4)-35=365
  eq(ev.damage, 365, '巨劍：滿蓄倍率 1+(rawChargeMul-1)×chargeDmgMul = 1+(2.5-1)×2=4 → floor(100*4)-35=365')
  ok(ev.charged === true, '巨劍：滿蓄攻擊仍正確標記 charged=true')
}

// ── 6) 體型：sizeBonus[enemy.size] 支援 small/medium/large 三個英文字面值，以及對齊 attribute
//    既有中文別名手法的 大型/中型/小型（INTEGRATOR 補：現網 rpg_monsters.size 實際是中文原文，
//    見 formulas.ts normalizeSizeAlias 檔頭說明）；真正無法辨識的字串仍視為 0 加成。──
{
  const rapier = weaponFixture('lk_rapier', 'sword', { sizeBonus: { small: 5, medium: 0, large: 5 } })
  function vsSize(size) {
    return makeSample({
      party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 }, equippedWeapon: rapier }],
      enemies: [{ id: 'e1', name: 'e', level: 1, hp: 9999, hpMax: 9999, slot: 'front_center', imageUrl: '', size, stats: { hpMax: 9999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } }],
    })
  }
  let sSmall = createBattle(vsSize('small'), { now: 0, config: FAR_CONFIG })
  sSmall = dispatch(sSmall, { type: 'ATTACK_BEGIN' }, 0)
  sSmall = dispatch(sSmall, { type: 'ATTACK_RELEASE' }, 0)
  eq(sSmall.events.find((e) => e.kind === 'attack').damage, Math.floor(100 * 1.05), '對小體型：sizeBonus.small=5% → floor(100*1.05)=105（細劍對小體型額外傷害）')

  let sMedium = createBattle(vsSize('medium'), { now: 0, config: FAR_CONFIG })
  sMedium = dispatch(sMedium, { type: 'ATTACK_BEGIN' }, 0)
  sMedium = dispatch(sMedium, { type: 'ATTACK_RELEASE' }, 0)
  eq(sMedium.events.find((e) => e.kind === 'attack').damage, 100, '對中體型：sizeBonus.medium=0 → 沒有額外加成')

  let sChineseLarge = createBattle(vsSize('大型'), { now: 0, config: FAR_CONFIG })
  sChineseLarge = dispatch(sChineseLarge, { type: 'ATTACK_BEGIN' }, 0)
  sChineseLarge = dispatch(sChineseLarge, { type: 'ATTACK_RELEASE' }, 0)
  eq(sChineseLarge.events.find((e) => e.kind === 'attack').damage, Math.floor(100 * 1.05), '現網中文舊資料 "大型" 經 normalizeSizeAlias 對應到 large → sizeBonus.large=5% 正確套用（不再是 no-op）')

  let sUnknown = createBattle(vsSize('giant'), { now: 0, config: FAR_CONFIG })
  sUnknown = dispatch(sUnknown, { type: 'ATTACK_BEGIN' }, 0)
  sUnknown = dispatch(sUnknown, { type: 'ATTACK_RELEASE' }, 0)
  eq(sUnknown.events.find((e) => e.kind === 'attack').damage, 100, '真正無法辨識的字串（既非英文三態、也不在中文別名表）→ 視為 0 加成，不猜測對應關係')
}

// ── 7) 武器屬性五行：普攻帶武器 element，套進 elementMultiplier 的五行相剋表。──
{
  const metalSword = weaponFixture('lk_sword', 'sword', { element: 'metal' })
  const sample = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 }, equippedWeapon: metalSword }],
    enemies: [{ id: 'e1', name: '木怪', level: 1, hp: 9999, hpMax: 9999, slot: 'front_center', imageUrl: '', attribute: 'wood', stats: { hpMax: 9999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } }],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const ev = s.events.find((e) => e.kind === 'attack')
  eq(ev.damage, Math.floor(100 * 1.25), '普攻帶武器 element=metal，對 attribute=wood 的怪物：金剋木 → ×1.25＝floor(100*1.25)=125')

  // 對照組：無武器（普攻恆 neutral）打同一隻怪，完全沒有屬性加成。
  const bare = makeSample({
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 } }],
    enemies: [{ id: 'e1', name: '木怪', level: 1, hp: 9999, hpMax: 9999, slot: 'front_center', imageUrl: '', attribute: 'wood', stats: { hpMax: 9999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 } }],
  })
  let s2 = createBattle(bare, { now: 0, config: FAR_CONFIG })
  s2 = dispatch(s2, { type: 'ATTACK_BEGIN' }, 0)
  s2 = dispatch(s2, { type: 'ATTACK_RELEASE' }, 0)
  eq(s2.events.find((e) => e.kind === 'attack').damage, 100, '對照組：沒有武器時普攻恆 neutral，對 wood 怪物沒有相剋加成（跟舊行為一致）')
}

// ── 8) 鍊：elementResistPct 減免非 neutral 怪物造成的傷害（事件記錄的仍是折算前的原始傷害）。──
{
  const chain = weaponFixture('cl_chain', 'staff', { elementResistPct: 15 })
  function makeVsFireMonster(equippedWeapon) {
    return makeSample({
      party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 0, mdef: 28 }, equippedWeapon }],
      enemies: [{ id: 'e1', name: '火怪', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', attribute: 'fire', stats: { hpMax: 999, mpMax: 0, atk: 100, matk: 1, def: 0, mdef: 0 } }],
    })
  }
  let s = createBattle(makeVsFireMonster(chain), { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] } })
  s = tick(s, 0) // idle→windup
  s = tick(s, DEFAULT_BATTLE_CONFIG.enemyWindupMs) // windup→attacking，結算傷害
  const ev = s.events.find((e) => e.kind === 'enemyAttack')
  eq(ev.damage, 100, 'enemyAttack 事件本身仍是完整原始傷害（100）——elementResistPct 只在 applyPartyDamage 才折算，不改事件記錄')
  eq(s.party[0].hp, 800 - 85, '鍊 elementResistPct=15%：非 neutral 怪物（fire）造成的傷害打 85 折＝floor(100*0.85)=85')

  // 對照組：沒有鍊（無武器），同一隻火怪打出完整傷害，不折算。
  let s2 = createBattle(makeVsFireMonster(undefined), { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] } })
  s2 = tick(s2, 0)
  s2 = tick(s2, DEFAULT_BATTLE_CONFIG.enemyWindupMs)
  eq(s2.party[0].hp, 800 - 100, '對照組：沒有鍊時沒有抗性可言，火怪的攻擊完整扣血 100')
  ok(Number.isInteger(s.party[0].hp) && Number.isInteger(s2.party[0].hp), '鍊：折算後 HP 仍是整數（整數不變式）')

  // 審查#1【中】回歸測試：現網 rpg_monsters.attribute 是中文原文，applyPartyDamage 判斷「是否為
  // neutral」必須先正規化中文別名才比較，否則「無」（中性）會被誤判成非中性而錯誤套用抗性、
  // 「火」也可能因為字面值不是英文 'fire' 而漏套（兩個方向都要驗證）。
  function makeVsMonster(attribute, equippedWeapon) {
    return makeSample({
      party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 0, mdef: 28 }, equippedWeapon }],
      enemies: [{ id: 'e1', name: '怪物', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '', attribute, stats: { hpMax: 999, mpMax: 0, atk: 100, matk: 1, def: 0, mdef: 0 } }],
    })
  }
  let sNoneCn = createBattle(makeVsMonster('無', chain), { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] } })
  sNoneCn = tick(sNoneCn, 0)
  sNoneCn = tick(sNoneCn, DEFAULT_BATTLE_CONFIG.enemyWindupMs)
  eq(sNoneCn.party[0].hp, 800 - 100, '中文別名「無」（正規化後＝neutral）的怪物打有 elementResistPct 的玩家：不減免，完整扣血 100')

  let sFireCn = createBattle(makeVsMonster('火', chain), { now: 0, config: { ...FAR_CONFIG, enemyActIntervalMs: [0, 0] } })
  sFireCn = tick(sFireCn, 0)
  sFireCn = tick(sFireCn, DEFAULT_BATTLE_CONFIG.enemyWindupMs)
  eq(sFireCn.party[0].hp, 800 - 85, '中文別名「火」（正規化後＝fire，非 neutral）的怪物打有 elementResistPct 的玩家：減免至 85（floor(100*0.85)）')
}

// ── 9) 書：magicSkillPct 乘在 dmg_type=magic 技能與 heal 的 coef 上（本例驗證 heal；damage 分支
//        同一段程式碼路徑，見 combat.ts resolveCastEffect 的 magicMul 算式）。──
{
  const book = weaponFixture('mg_book', 'staff', { magicSkillPct: 5 })
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, equippedWeapon: book },
      { id: 'ally', name: '小夥伴', level: 56, hp: 100, hpMax: 500, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 500, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 }, weapon: 'staff' },
    ],
  })
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'heal', targetId: 'ally' }, 0)
  s = tick(s, 500) // heal 的 castMs=500
  // 無武器基準（既有測試#9）：floor(80*2+80)=240；書 magicSkillPct=5% → coefficient=2*1.05=2.1
  // → floor(80*2.1+80)=floor(168+80)=248。
  eq(s.party[1].hp, 100 + 248, '書 magicSkillPct=5%：治療 coefficient 提高 5% → floor(MATK×2.1+80)=248（無武器基準是 240）')
}

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
