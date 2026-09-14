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
import assert from 'node:assert/strict'
import { register } from 'node:module'

const loaderSrc = `
export async function resolve(specifier, context, nextResolve) {
  try {
    return await nextResolve(specifier, context)
  } catch (err) {
    if (err && err.code === 'ERR_MODULE_NOT_FOUND' && specifier.startsWith('.')) {
      for (const ext of ['.ts', '.tsx']) {
        try { return await nextResolve(specifier + ext, context) } catch {}
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

// ── 19) 隊友 AI：普攻與依血量改用治療 ──
{
  const sample = makeSample({
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 } },
      { id: 'healer', name: '輔助', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 50, matk: 80, def: 20, mdef: 20 }, weapon: 'staff' },
    ],
  })
  // 普攻：allyActIntervalMs 設 [0,0] 讓 healer 一開始就能行動；血量都健康所以走普攻分支。
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s = tick(s, 0)
  const allyAtk = s.events.find((e) => e.kind === 'attack' && e.actorId === 'healer')
  ok(!!allyAtk, '隊友 AI 對目前目標自動普攻')
  eq(allyAtk.charged, false, '隊友普攻不蓄氣')

  // 治療分支：把 player 打到 <40% hp，輔助 AI（第一位非玩家隊員）改放 heal。
  let s2 = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s2.party[0].hp = 100 // 800 的 12.5%，< 40%
  s2 = tick(s2, 0)
  const healEv = s2.events.find((e) => e.kind === 'heal' && e.actorId === 'healer')
  ok(!!healEv && healEv.targetId === 'player', '隊友血量 <40% 時輔助 AI 改用 heal 技能')
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
      { id: 'healer', name: '輔助', level: 56, hp: 100, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 50, matk: 80, def: 20, mdef: 20 }, weapon: 'staff' },
    ],
  })
  let s = createBattle(sample, { now: 0, config: { ...FAR_CONFIG, allyActIntervalMs: [0, 0] } })
  s = tick(s, 0) // healer 血量 100/800=12.5%<40%，觸發治療（用掉的是 aiSkillReadyAt）
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
        { id: 'healer', name: '輔助', level: 56, hp: healerHp, hpMax: 100, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 100, mpMax: 100, atk: 50, matk: 80, def: 20, mdef: 20 }, weapon: 'staff' },
        { id: 'ally2', name: '夥伴二', level: 56, hp: ally2Hp, hpMax: 100, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 100, mpMax: 100, atk: 50, matk: 50, def: 20, mdef: 20 } },
      ],
    })
  }
  {
    // healer 20%（最低但 ≥15%），ally2 35%，兩者都 <40% 門檻：規則要求優先救「非自己」的 ally2。
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

// ── 34) elementMultiplier：查表、未列出＝1.0、0＝完全無效，查無 attribute/元素都安全回退 ──
{
  const cfg = DEFAULT_BATTLE_CONFIG
  eq(elementMultiplier(cfg, '金', 'water'), 0, '金屬性怪物被 water 攻擊 → 0 倍（規格範例：無效攻擊）')
  eq(elementMultiplier(cfg, '金', 'fire'), 1.25, '金屬性怪物被 fire 攻擊 → 1.25 倍（吃剋）')
  eq(elementMultiplier(cfg, '木', 'fire'), 1.6, '木屬性怪物被 fire 攻擊 → 1.6 倍')
  eq(elementMultiplier(cfg, '闇', 'dark'), 0, '闇屬性怪物被 dark 攻擊 → 0 倍')
  eq(elementMultiplier(cfg, '金', 'wood'), 1, '表中查無的組合（金×wood）預設 1.0，不相剋也不吃虧')
  eq(elementMultiplier(cfg, '無', 'fire'), 1, '「無」屬性怪物的表是空物件，任何 element 都落回 1.0')
  eq(elementMultiplier(cfg, undefined, 'water'), 1, '怪物沒有填 attribute → 視為中性，直接回 1.0')
  eq(elementMultiplier(cfg, '不存在的屬性', 'fire'), 1, '整個屬性都不在表中 → 回 1.0')
}

// ── 35) 端到端：屬性剋制造成 'immune'（技能 element='water' 打 attribute='金' 的怪物） ──
{
  const sample = makeSample({
    enemies: [
      {
        id: 'e1', name: '鋼鐵巨鉗蟹', level: 1, hp: 999, hpMax: 999, slot: 'front_center', imageUrl: '',
        attribute: '金', stats: { hpMax: 999, mpMax: 0, atk: 1, matk: 1, def: 0, mdef: 0 },
      },
    ],
    skills: [
      { id: 'icebolt', name: '冰槍術', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 2, flat: 0, weapon: 'staff', element: 'water', castMs: 100 },
      null, null, null, null, null, null, null,
    ],
  })
  // 不需要固定 rng：FAR_CONFIG 已把 missPct 封死在 0（必中），elementMul=0 在算暴擊前就 return，
  // 所以這條路徑完全不吃 rng 的實際數值，用 Math.random 也一樣確定。
  let s = createBattle(sample, { now: 0, config: FAR_CONFIG })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'icebolt' }, 0)
  // P3 新增 castMinMs（預設 120）：即使玩家 castReductionPct=0（這裡沒帶 rating，退回中性後備值），
  // effectiveCastMs 仍會把低於 120ms 的 castMs 夾到 120——這支測試技能的 castMs=100 本來就低於
  // 這個新地板，改成 tick 到 120 才會命中 actionUntil，行為改變合理（P3 刻意設計的全域下限，不是
  // 這個測試原本要驗證的屬性剋制邏輯跑掉）。
  s = tick(s, 120)
  const ev = s.events.find((e) => e.kind === 'attack' && e.actorId === 'player')
  ok(!!ev && ev.result === 'immune' && ev.damage === 0, '冰屬性技能打金屬性怪物 → elementMultiplier=0 → result=immune，damage=0')
  eq(s.enemies[0].hp, 999, 'immune 攻擊完全不扣血')
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
    { hit: 90, flee: 0, critPct: 0, critShield: 0, aspd: 150, castReductionPct: 0 },
    '沒有帶 rating 的隊員：後備評級的 hit 沿用 cfg.hitRate(0.9→90)、aspd 沿用 cfg.aspdReference(150)，其餘給 0（沒有更多資訊可推）',
  )
  eq(
    s.enemies[0].rating,
    { hit: 77, flee: 12, critPct: 3, critShield: 4, aspd: 150, castReductionPct: 0 },
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

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
