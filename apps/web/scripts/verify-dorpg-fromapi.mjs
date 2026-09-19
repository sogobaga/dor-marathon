// 審查 dorpg_p5 修正輪 #1(d)：驗證 apps/web/src/lib/dorpg/fromApi.ts 的 sampleFromBootstrap()/
// mapSkill() 能正確把「後端 wire JSON」轉成引擎吃的 Skill——直接鎖 fromApi.ts 這一層轉換，而不是
// 只測 engine 本身（verify-dorpg-engine.mjs）：審查抓到的兩個根因缺陷
//   ① battle.go toWireSkillLeveled 過去頂層 coefficient/flat/mpCost 沒有依 level 展開、也沒有
//     頂層 hits 欄位（見 battle_test.go 的 Go 端回歸測試）
//   ② api.ts 曾經誤把 wireSkill.effect 的裸資料型別宣告成 camelCase（durationMs/mpCost），但實際
//     wire JSON 是 snake_case（duration_ms/mp_cost，對齊 WIRE.md／後端 skills.go EffectAtLevel）
// 都發生在「wire JSON → Skill」這一層轉換，verify-dorpg-engine.mjs 的測試全部直接手造 Skill 物件
// 餵給 engine，從來不會經過 fromApi.ts，完全測不到這兩個問題——所以另外開一支腳本，直接偽造
// battle.go 修好後會送出的 wire JSON 格式，跑過 sampleFromBootstrap() 再進 engine 驗證。
//
// 執行方式（apps/web 目錄下）：
//   node --experimental-strip-types scripts/verify-dorpg-fromapi.mjs
//
// fromApi.ts 只有一個真正的執行期匯入（其餘都是 `import type`，Node 的 TS type-stripping 會把
// type-only import 整段削掉，完全不需要真的 resolve，跟 verify-dorpg-engine.mjs 檔頭註解說的
// 是同一件事）：`import { DEFAULT_BATTLE_CONFIG } from '@/lib/dorpg/engine'`。純 Node ESM 不吃
// tsconfig 的 `@/* -> src/*` path alias，且 `@/lib/dorpg/engine` 實際指到一個目錄（index.ts），
// 這裡的 loader hook 補這兩件事：① 把 `@/` 開頭的 specifier 換算成 src 目錄下的絕對檔案路徑；
// ② 解析失敗時依序補 .ts/.tsx/index.ts/index.tsx 重試（比 verify-dorpg-engine.mjs 既有的 loader
// 多了目錄 index 這個候選，因為既有 loader從來不需要處理「specifier 其實是個目錄」的情況）。
import { register } from 'node:module'

const SRC_DIR = new URL('../src/', import.meta.url).href
const loaderSrc = `
const SRC_DIR = ${JSON.stringify(SRC_DIR)}
export async function resolve(specifier, context, nextResolve) {
  let spec = specifier
  if (spec.startsWith('@/')) spec = SRC_DIR + spec.slice(2)
  try {
    return await nextResolve(spec, context)
  } catch (err) {
    // ERR_MODULE_NOT_FOUND：一般缺副檔名（.ts/.tsx）。
    // ERR_UNSUPPORTED_DIR_IMPORT：specifier 其實指到一個目錄（例如 @/lib/dorpg/engine 對應
    // engine/index.ts）——Node 認得出目錄存在，但純 ESM 不做目錄 index 解析，要另外補 /index.ts。
    if (err && (err.code === 'ERR_MODULE_NOT_FOUND' || err.code === 'ERR_UNSUPPORTED_DIR_IMPORT')) {
      for (const suffix of ['.ts', '.tsx', '/index.ts', '/index.tsx']) {
        try { return await nextResolve(spec + suffix, context) } catch {}
      }
    }
    throw err
  }
}
`
register('data:text/javascript,' + encodeURIComponent(loaderSrc), import.meta.url)

const { sampleFromBootstrap } = await import(new URL('../src/lib/dorpg/fromApi.ts', import.meta.url).href)
const { createBattle, dispatch, tick, inGuardianState } = await import(new URL('../src/lib/dorpg/engine/index.ts', import.meta.url).href)

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

/** 最小可用 raw bootstrap sample（模擬 GET /rpg/battle/bootstrap 的 sample 區塊）：1 玩家 + 1
 *  血量超厚的假人，skills 陣列由呼叫端傳入（固定 10 格，未用到的槽位補 null）。 */
function makeRawSample(skills) {
  return {
    party: [{
      id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100,
      portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword',
    }],
    enemies: [{
      id: 'e1', name: '測試假人', level: 50, hp: 99999, hpMax: 99999, slot: 'front_center',
      imageUrl: '', canEscape: true, stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 },
    }],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills,
    items: [],
    initialTargetId: 'e1',
  }
}
const pad10 = (s) => [s, null, null, null, null, null, null, null, null, null]

// ── 1) toWireSkillLeveled 修好後，battle.go 送出的頂層欄位＝ExpandEffect 展開值——這裡直接偽造
//      「Lv1」與「Lv5」兩份 wire JSON（模擬後端在兩個等級各自算出的展開結果），驗證
//      sampleFromBootstrap()→mapSkill() 對這批頂層欄位／effect 是原樣照抄，Lv1/Lv5 應該不同。 ──
{
  const base = {
    id: 'combo_strike', name: '連擊', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy',
    weapon: 'sword', castMs: 300, dmgType: 'physical', maxLevel: 10, displayText: '連續斬擊', implemented: true,
  }
  const rawLv1 = {
    ...base, level: 1, mpCost: 10, coefficient: 1.0, flat: 5, hits: 1,
    effect: { kind: 'damage', coef: 1.0, flat: 5, hits: 1, target: 'enemy', mp_cost: 10 },
  }
  const rawLv5 = {
    ...base, level: 5, mpCost: 16, coefficient: 1.8, flat: 13, hits: 3,
    effect: { kind: 'damage', coef: 1.8, flat: 13, hits: 3, target: 'enemy', mp_cost: 16 },
  }
  const skill1 = sampleFromBootstrap(makeRawSample(pad10(rawLv1))).skills[0]
  const skill5 = sampleFromBootstrap(makeRawSample(pad10(rawLv5))).skills[0]

  ok(skill1.coefficient !== skill5.coefficient, 'Lv1/Lv5 頂層 coefficient 不同（審查#1：曾經恆等於 Lv1 基準值，升級沒有任何效果）')
  ok(skill1.flat !== skill5.flat, 'Lv1/Lv5 頂層 flat 不同')
  ok(skill1.mpCost !== skill5.mpCost, 'Lv1/Lv5 頂層 mpCost 不同')
  eq(skill5.hits, 3, 'Lv5 的頂層 hits 正確讀到 3（連段技能；曾經沒有頂層 hits 欄位，永遠只能讀到 undefined→1）')
  eq(skill5.coefficient, 1.8, 'Lv5 的頂層 coefficient 正確讀到展開值 1.8')
  eq(skill5.flat, 13, 'Lv5 的頂層 flat 正確讀到展開值 13')
  eq(skill5.mpCost, 16, 'Lv5 的頂層 mpCost 正確讀到展開值 16')
}

// ── 2) 連段技能（hits=3）經過 fromApi 轉換後，實際跑進 engine 仍然產生 3 段獨立命中——不只是
//      型別欄位對了，戰鬥結算也真的吃到（呼應 verify-dorpg-engine.mjs #53，這次從 wire JSON
//      出發，涵蓋 fromApi.ts 這一層轉換）。 ──
{
  const rawLv5 = {
    id: 'combo_strike', name: '連擊', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy',
    weapon: 'sword', castMs: 100, dmgType: 'physical', maxLevel: 10, displayText: '', implemented: true,
    level: 5, mpCost: 16, coefficient: 1.0, flat: 0, hits: 3,
    effect: { kind: 'damage', coef: 1.0, flat: 0, hits: 3, target: 'enemy', mp_cost: 16 },
  }
  const sample = sampleFromBootstrap(makeRawSample(pad10(rawLv5)))
  const cfg = {
    enemyActIntervalMs: [999999, 999999], allyActIntervalMs: [999999, 999999],
    baseMissPct: 0, missMinPct: 0, missMaxPct: 0, critRate: 0, monsterCritPct: 0, monsterCritShieldBase: 0,
  }
  let s = createBattle(sample, { now: 0, config: cfg })
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'combo_strike' }, 0)
  s = tick(s, 120) // castMs=100 被 castMinMs(120) 夾到 120
  const hitEvents = s.events.filter((e) => e.kind === 'attack' && e.actorId === 'player')
  eq(hitEvents.length, 3, '經 fromApi 轉換後的 hits=3 技能，實戰仍產生 3 筆獨立 attack 事件')
}

// ── 3) 審查#1(b) 命名對齊修正：effect 的裸資料是 snake_case（duration_ms/mp_cost，對齊 WIRE.md／
//      後端 skills.go EffectAtLevel 的 json tag），api.ts 曾經誤宣告成 camelCase（durationMs/
//      mpCost）——若這裡讀到 undefined，buff/debuff 的持續時間會恆為 0（不會壞在型別檢查，只會
//      在執行期悄悄遺失資料）。 ──
{
  const raw = {
    id: 'war_cry', name: '戰吼', iconUrl: '', cooldownMs: 0, kind: 'buff', target: 'self',
    mpCost: 15, coefficient: 0, flat: 0, weapon: 'sword', castMs: 100,
    level: 3, maxLevel: 5, displayText: '', dmgType: 'physical', implemented: true,
    effect: { kind: 'buff', stat: 'atk_pct', value: 20, duration_ms: 8000, target: 'self', mp_cost: 15 },
  }
  const skill = sampleFromBootstrap(makeRawSample(pad10(raw))).skills[0]
  eq(skill.effect?.durationMs, 8000, 'effect.duration_ms（真實 wire 格式，snake_case）正確映射成引擎用的 durationMs（曾因型別誤植成 durationMs 而永遠讀到 undefined）')
  eq(skill.effect?.mpCost, 15, 'effect.mp_cost 同理正確映射成 mpCost')
  eq(skill.effect?.value, 20, 'effect.value 本來就同名，確認沒有被上面的修正連帶弄壞')
}

// ── 4) 缺 effect（既有 5 個一般技能／舊版後端）時，mapSkill 仍照舊只讀頂層欄位，不因為新增的
//      深度防禦邏輯而壞掉。 ──
{
  const raw = {
    id: 'slash', name: '斬擊', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy',
    mpCost: 5, coefficient: 1.6, flat: 20, weapon: 'sword', castMs: 300,
  }
  const skill = sampleFromBootstrap(makeRawSample(pad10(raw))).skills[0]
  eq(skill.coefficient, 1.6, '沒有 effect 時，coefficient 落回頂層欄位')
  eq(skill.flat, 20, '沒有 effect 時，flat 落回頂層欄位')
  eq(skill.mpCost, 5, '沒有 effect 時，mpCost 落回頂層欄位')
  ok(skill.hits === undefined, '沒有 effect 也沒有頂層 hits 時，hits 維持 undefined（交給 Skill 型別的缺省語意＝1）')
}

// ── 5) P6（CONTRACT §3.2／WIRE「戰鬥 bootstrap」）：party member 的 skills/presetName 正確映射；
//      wireSkill.tier 正確映射到 Skill.tier，且真的能餵給隊友 AI 挑出 tier 最高的技能施放
//      （呼應 verify-dorpg-engine.mjs 36 號斷言，這次從 wire JSON 出發，涵蓋 fromApi.ts 這一層）。 ──
{
  const raw = {
    party: [
      { id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword' },
      {
        id: 'companion', name: '傭兵', level: 56, hp: 500, hpMax: 500, mp: 50, mpMax: 50, portraitUrl: null,
        stats: { hpMax: 500, mpMax: 50, atk: 60, matk: 60, def: 20, mdef: 20 }, weapon: 'bow',
        presetName: '示範腳本',
        skills: [
          { id: 'dmg_a', name: '技能甲', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.0, flat: 0, weapon: 'bow', castMs: 0, tier: 1 },
          { id: 'dmg_b', name: '技能乙', iconUrl: '', cooldownMs: 0, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 2.0, flat: 0, weapon: 'bow', castMs: 0, tier: 2 },
        ],
      },
    ],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 99999, hpMax: 99999, slot: 'front_center', imageUrl: '', canEscape: true, stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } }],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: pad10(null),
    items: [],
    initialTargetId: 'e1',
  }
  const sample = sampleFromBootstrap(raw)
  eq(sample.party[1].presetName, '示範腳本', 'party member 的 presetName 原樣透傳')
  eq(sample.party[1].skills.length, 2, 'party member 的 skills 陣列正確映射（2 顆技能）')
  eq(sample.party[1].skills[0].id, 'dmg_a', '第一顆技能 id 正確')
  eq(sample.party[1].skills[0].tier, 1, 'wireSkill.tier 正確映射到 Skill.tier')
  eq(sample.party[1].skills[1].tier, 2, '第二顆技能 tier 也正確映射')
  eq(sample.party[0].skills.length, 0, '玩家本人（party[0]）沒有 skills 欄位時，映射結果是空陣列（不是 undefined）')
  ok(sample.party[0].presetName === undefined, '玩家本人沒有 presetName（未提供時保持 undefined）')

  const cfg = {
    enemyActIntervalMs: [999999, 999999], allyActIntervalMs: [0, 0],
    baseMissPct: 0, missMinPct: 0, missMaxPct: 0, critRate: 0, monsterCritPct: 0, monsterCritShieldBase: 0,
  }
  let s = createBattle(sample, { now: 0, config: cfg })
  s = tick(s, 0)
  const cast = s.events.find((e) => e.kind === 'skillCast' && e.actorId === 'companion')
  eq(cast?.skillId, 'dmg_b', '經 fromApi 轉換後，隊友 AI 仍正確挑出 tier 最高（tier:2）的 dmg_b 施放')
}

// ── 6) DORPG P7（CONTRACT §2/§3、WIRE「戰鬥 bootstrap」）：party member 的 weapon 若已經是
//      BACKEND／INTEGRATOR 未來會送的富物件形狀（{id,name,typeId,visual,profile}），
//      mapPartyMember() 的 asEquippedWeapon() 要能正確解析成 PartyMember.equippedWeapon，
//      且這份資料真的能餵進 engine 產生武器效果（本例用雙劍 hits=2 驗證端到端）。
//      api.ts 目前的 RpgBootstrapPartyMemberRaw.weapon 型別仍是舊的 `string`（見任務回報），
//      這裡直接餵未來形狀的裸 JSON 給 sampleFromBootstrap()，證明 fromApi.ts 讀取邏輯不依賴
//      該型別宣告、一旦 INTEGRATOR 補上正式型別就能立刻生效，不需要改動 fromApi.ts。 ──
{
  const dualSwordWire = {
    id: 'lk_dual_t5', name: '風城雙刃‧伍式', typeId: 'lk_dual', visual: 'sword',
    profile: {
      atk: 40, matk: 0, hits: 2, hitMul: 0.6, extraHitChancePct: 0, intervalPct: 0,
      chargeTimeMul: 1, chargeDmgMul: 1, splashPct: 0, sizeBonus: { small: 0, medium: 0, large: 0 },
      critPct: 0, critDmgPct: 0, elementResistPct: 0, magicSkillPct: 0, element: 'neutral',
    },
  }
  const raw = {
    party: [{
      id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100,
      portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 100, matk: 80, def: 35, mdef: 28 },
      weapon: dualSwordWire,
    }],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 99999, hpMax: 99999, slot: 'front_center', imageUrl: '', canEscape: true, stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 35, mdef: 10 } }],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: pad10(null),
    items: [],
    initialTargetId: 'e1',
  }
  const sample = sampleFromBootstrap(raw)
  ok(!!sample.party[0].equippedWeapon, 'wire 送富物件形狀的 weapon 時，mapPartyMember 正確解析出 equippedWeapon（不是 null）')
  eq(sample.party[0].equippedWeapon?.profile.hits, 2, 'equippedWeapon.profile.hits 正確映射成 2（雙劍二刀流）')
  eq(sample.party[0].weapon, undefined, '既有的 PartyMember.weapon（視覺特效組字串欄位）不受影響——這批 wire 沒有送舊格式的字串，維持 undefined')

  const cfg = {
    enemyActIntervalMs: [999999, 999999], allyActIntervalMs: [999999, 999999],
    baseMissPct: 0, missMinPct: 0, missMaxPct: 0, critRate: 0, monsterCritPct: 0, monsterCritShieldBase: 0,
  }
  let s = createBattle(sample, { now: 0, config: cfg })
  s = dispatch(s, { type: 'ATTACK_BEGIN' }, 0)
  s = dispatch(s, { type: 'ATTACK_RELEASE' }, 0)
  const hitEvents = s.events.filter((e) => e.kind === 'attack' && e.actorId === 'player')
  eq(hitEvents.length, 2, '經 fromApi 轉換後的雙劍武器，實戰普攻仍產生 2 段獨立 attack 事件（hits=2）')
  ok(hitEvents.every((e) => e.damage === Math.floor(100 * 0.6) - 35), '雙劍每段傷害＝floor(100×hitMul0.6)-def35=25，經完整 wire→fromApi→engine 管線驗證')
}

// ── 7) DORPG P8（CONTRACT §2、WIRE「戰鬥 bootstrap」）：party member 的 equipmentEffects 正確
//      映射（缺欄位/整包缺失時退回中性值 0；有給值時逐欄照抄），且真的能餵進 engine 產生效果
//      （呼應 verify-dorpg-engine.mjs 的 MP 減免測試，這次從 wire JSON 出發，涵蓋 fromApi.ts
//      這一層轉換）。 ──
{
  // 7a）沒有送這個欄位（舊版後端）：六欄位全部退回 0。
  const rawNoEquip = {
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword' }],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 99999, hpMax: 99999, slot: 'front_center', imageUrl: '', canEscape: true, stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } }],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: pad10(null),
    items: [],
    initialTargetId: 'e1',
  }
  const memberNoEquip = sampleFromBootstrap(rawNoEquip).party[0]
  eq(
    memberNoEquip.equipmentEffects,
    { intervalPct: 0, mpCostReducePct: 0, hpRegenPctPer5s: 0, mpRegenPctPer5s: 0, damageTakenPct: 0, elementResistPct: 0 },
    '沒有送 equipmentEffects 欄位（舊版後端）時，映射結果六欄位全部是中性值 0',
  )

  // 7b）送了完整物件：逐欄照抄，且真的能餵進 engine 讓 MP 消耗打折（檢查與扣除一致）。
  const rawWithEquip = {
    party: [{
      id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 3, mpMax: 100, portraitUrl: null,
      stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword',
      equipmentEffects: { intervalPct: -8, mpCostReducePct: 60, hpRegenPctPer5s: 2, mpRegenPctPer5s: 2, damageTakenPct: -10, elementResistPct: 10 },
    }],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 99999, hpMax: 99999, slot: 'front_center', imageUrl: '', canEscape: true, stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } }],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: pad10({ id: 'slash', name: '斬擊', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.6, flat: 20, weapon: 'sword', castMs: 300 }),
    items: [],
    initialTargetId: 'e1',
  }
  const sample = sampleFromBootstrap(rawWithEquip)
  eq(
    sample.party[0].equipmentEffects,
    { intervalPct: -8, mpCostReducePct: 60, hpRegenPctPer5s: 2, mpRegenPctPer5s: 2, damageTakenPct: -10, elementResistPct: 10 },
    '送了完整 equipmentEffects 物件時，六欄位逐欄照抄',
  )

  const cfg = {
    enemyActIntervalMs: [999999, 999999], allyActIntervalMs: [999999, 999999],
    baseMissPct: 0, missMinPct: 0, missMaxPct: 0, critRate: 0, monsterCritPct: 0, monsterCritShieldBase: 0,
  }
  let s = createBattle(sample, { now: 0, config: cfg })
  // slash 的 mpCost=5，原始需求超過玩家現有 mp=3；mpCostReducePct=60% → effectiveMpCost=floor(5×0.4)=2，mp=3 足夠。
  s = dispatch(s, { type: 'USE_SKILL', skillId: 'slash' }, 0)
  ok(s.party[0].action === 'casting', '經 fromApi 轉換後的 mpCostReducePct=60%，讓 mp=3 的玩家仍能施放原始 mpCost=5 的技能（打折後只需 2）')
  eq(s.party[0].mp, 1, '經 fromApi 轉換後，實際扣除的也是打折後的成本 2（3-2=1）')
}

// ── 8) DORPG P10（CONTRACT §3、WIRE「戰鬥 bootstrap」）：kind='taunt' 技能的 taunt 展開值、
//      party member 的 guardTaunt／jobTraits，且真的能餵進 engine 產生守護狀態（端到端）。 ──
{
  // 8a）kind='taunt' 的 wire skill：taunt 展開值正確映射（camelCase 直接照抄，非 taunt 技能
  //     即使誤送這個欄位也一律忽略）。
  const rawTaunt = {
    id: 'hk_c1', name: '挑釁', iconUrl: '', cooldownMs: 6000, kind: 'taunt', target: 'self',
    mpCost: 8, coefficient: 0, flat: 0, weapon: 'greatsword', castMs: 300, implemented: true,
    taunt: { durationMs: 5000, damageTakenPct: 0, retarget: true },
  }
  const rawNonTaunt = {
    id: 'slash', name: '斬擊', iconUrl: '', cooldownMs: 4000, kind: 'damage', target: 'enemy',
    mpCost: 5, coefficient: 1.6, flat: 20, weapon: 'sword', castMs: 300,
    taunt: { durationMs: 9999, damageTakenPct: -50, retarget: true }, // 誤送，應被忽略。
  }
  const skills = sampleFromBootstrap(makeRawSample([rawTaunt, rawNonTaunt, null, null, null, null, null, null, null, null])).skills
  eq(skills[0].kind, 'taunt', 'kind="taunt" 正確映射')
  eq(skills[0].taunt, { durationMs: 5000, damageTakenPct: 0, retarget: true }, 'taunt 展開值（durationMs/damageTakenPct/retarget）逐欄照抄')
  ok(skills[1].taunt === undefined, '非 taunt 技能即使 wire 誤送 taunt 欄位也一律忽略（kind="damage" 的 slash）')
}
{
  // 8b）taunt 缺 durationMs（核心欄位）→ 整包視為無效，退回 undefined（跟 asEquippedWeapon()
  //     對 profile 缺失的「核心欄位一壞全丟」判斷同一個精神）。
  const raw = {
    id: 'hk_c1', name: '挑釁', iconUrl: '', cooldownMs: 6000, kind: 'taunt', target: 'self',
    mpCost: 8, coefficient: 0, flat: 0, weapon: 'greatsword', castMs: 300,
    taunt: { damageTakenPct: 0, retarget: true },
  }
  const skill = sampleFromBootstrap(makeRawSample(pad10(raw))).skills[0]
  ok(skill.taunt === undefined, 'taunt 缺 durationMs（核心欄位）→ 整包退回 undefined')
}
{
  // 8c）party member 的 guardTaunt／jobTraits：缺欄位退回中性值（false／undefined），
  //     有送值時正確映射。
  const rawMissing = {
    party: [{ id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null, stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'sword' }],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 99999, hpMax: 99999, slot: 'front_center', imageUrl: '', canEscape: true, stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } }],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: pad10(null),
    items: [],
    initialTargetId: 'e1',
  }
  const memberMissing = sampleFromBootstrap(rawMissing).party[0]
  eq(memberMissing.guardTaunt, false, '沒有送 guardTaunt（舊版後端）時，映射結果是 false（沒有這個被動）')
  ok(memberMissing.jobTraits === undefined, '沒有送 jobTraits（舊版後端／該職業沒有 traits）時，映射結果是 undefined（角色頁不顯示這一行）')

  const rawWithP10 = {
    ...rawMissing,
    party: [{ ...rawMissing.party[0], guardTaunt: true, jobTraits: { damageTakenPct: -15 } }],
  }
  const memberWithP10 = sampleFromBootstrap(rawWithP10).party[0]
  eq(memberWithP10.guardTaunt, true, 'guardTaunt=true 正確映射')
  eq(memberWithP10.jobTraits, { damageTakenPct: -15 }, 'jobTraits.damageTakenPct 正確映射')
}
{
  // 8d）端到端：guardTaunt=true 的玩家經完整 wire→fromApi→engine 管線，GUARD_BEGIN 後真的進入
  //     守護狀態（inGuardianState）——不是只有型別欄位對了，戰鬥中的仇恨規則也真的吃到。
  const raw = {
    party: [{
      id: 'player', name: '玩家', level: 56, hp: 800, hpMax: 800, mp: 100, mpMax: 100, portraitUrl: null,
      stats: { hpMax: 800, mpMax: 100, atk: 135, matk: 80, def: 35, mdef: 28 }, weapon: 'greatsword',
      guardTaunt: true,
    }],
    enemies: [{ id: 'e1', name: '測試假人', level: 50, hp: 99999, hpMax: 99999, slot: 'front_center', imageUrl: '', canEscape: true, stats: { hpMax: 99999, mpMax: 0, atk: 1, matk: 1, def: 10, mdef: 10 } }],
    scene: { id: 's', name: 's', imageUrl: '', slots: [] },
    skills: pad10(null),
    items: [],
    initialTargetId: 'e1',
  }
  const sample = sampleFromBootstrap(raw)
  const cfg = {
    enemyActIntervalMs: [999999, 999999], allyActIntervalMs: [999999, 999999],
    baseMissPct: 0, missMinPct: 0, missMaxPct: 0, critRate: 0, monsterCritPct: 0, monsterCritShieldBase: 0,
  }
  let s = createBattle(sample, { now: 0, config: cfg })
  eq(s.party[0].guardTaunt, true, '經 fromApi 轉換後，PartyActor.guardTaunt 正確為 true')
  s = dispatch(s, { type: 'GUARD_BEGIN' }, 0)
  ok(s.party[0].action === 'guarding', '經 fromApi 轉換後，GUARD_BEGIN 仍正常生效')
  eq(s.party[0].tauntUntil, 0, 'GUARD_BEGIN 來源的守護狀態不需要寫入 tauntUntil（維持 0）')
  ok(inGuardianState(s.party[0], 0), '經完整 wire→fromApi→engine 管線，GUARD_BEGIN 後 inGuardianState 判定為 true（仇恨規則真的吃到，不只是型別欄位對了）')
}

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
