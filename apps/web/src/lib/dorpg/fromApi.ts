// DORPG P2：把 GET /rpg/battle/bootstrap 的回應轉成引擎吃的 BattleSample／Partial<BattleConfig>。
// 只做「形狀驗證＋防禦」，不做任何戰鬥規則判斷——規則永遠在 engine 裡（P1 引擎已凍結，這裡不碰它）。
//
// 2026-09-14 已對照 services/api/internal/rpg/battle.go 的 wire* 型別原始碼核對過（不是憑空假設）：
// 該檔案頭明講命名慣例——「sample」／「config」子物件直接對齊 apps/web/src/lib/dorpg/types.ts／
// engine/types.ts 的 camelCase 欄位名（不是全站 snake_case 慣例），而且 portraitUrl／imageUrl／iconUrl
// 後端已經算好完整路徑，不是素材 id。這代表 wire 格式幾乎已經是 BattleSample 本身的形狀，
// 這裡的 map* 函式主要工作是「驗證 enum 字面量、給缺欄位一個安全預設」，不是重新命名欄位。
//
// 對照表（wire 欄位 → BattleSample 欄位；沒列出的是同名直接沿用）：
//   skills[].kind/target、items[].kind、enemies[].slot、scene.slots[].id  → 驗證是否落在合法字面量集合，
//     不合法就退回安全預設（避免 DB 誤填的字串讓畫面／引擎壞掉，見下面 as*() 系列函式）。
//   skills[].weapon（必填）→ 驗證失敗退回 'sword'；party[].weapon（選填，AI 隊友用）→ 驗證失敗給 undefined
//     （PartyMember.weapon 本來就選填，undefined 由 engine 自己預設 'sword'，見 engine/index.ts toPartyActor）。
import type {
  RpgBootstrapConfigRaw,
  RpgBootstrapEffectRaw,
  RpgBootstrapEnemyRaw,
  RpgBootstrapEquipmentEffectsRaw,
  RpgBootstrapItemRaw,
  RpgBootstrapPartyMemberRaw,
  RpgBootstrapRatingRaw,
  RpgBootstrapSampleRaw,
  RpgBootstrapSkillRaw,
} from '@/lib/api';
import type {
  BattleSample,
  BuffDebuffStat,
  CombatRating,
  DmgType,
  EffectAtLevel,
  ElementKind,
  Enemy,
  EnemySlotId,
  EquipmentEffectsWire,
  EquippedWeaponWire,
  Item,
  PartyMember,
  Skill,
  WeaponKind,
  WeaponProfileWire,
} from '@/lib/dorpg/types';
import type { BattleConfig } from '@/lib/dorpg/engine';
// P3：asRating() 用它的 aspdReference 當 rating.aspd 缺欄位時的中性後備值（見該函式註解）——
// 只借用這個已凍結匯出的常數，不是改動 engine 本身，跟 fixture.ts 借用同一個常數的方式一致。
// P9：STRATEGY_IDS 用來驗證 wire 送來的 strategyId 是否落在合法字面量集合（同一招 as*() 系列
// 防禦邏輯，見 asStrategyId）。
import { DEFAULT_BATTLE_CONFIG, STRATEGY_IDS } from '@/lib/dorpg/engine';

const WEAPON_KINDS: readonly WeaponKind[] = ['sword', 'staff', 'bow', 'greatsword'];
// 兩個多載：技能的 weapon 必填（給 fallback 時回傳一定是 WeaponKind，不含 undefined）；
// 隊友的 weapon 選填（不給 fallback 時可能回傳 undefined，交給 engine 自己預設 'sword'）。
function asWeapon(w: string | undefined): WeaponKind | undefined;
function asWeapon(w: string | undefined, fallback: WeaponKind): WeaponKind;
function asWeapon(w: string | undefined, fallback?: WeaponKind): WeaponKind | undefined {
  return (WEAPON_KINDS as readonly string[]).includes(w ?? '') ? (w as WeaponKind) : fallback;
}

const ELEMENT_KINDS: readonly ElementKind[] = ['metal', 'wood', 'water', 'fire', 'earth', 'light', 'dark', 'neutral'];
function asElement(e: string | undefined): ElementKind | undefined {
  return (ELEMENT_KINDS as readonly string[]).includes(e ?? '') ? (e as ElementKind) : undefined;
}

const ENEMY_SLOTS: readonly EnemySlotId[] = ['rear_left', 'rear_right', 'front_left', 'front_center', 'front_right'];
/** 未知槽位保底站 front_center，至少不會讓 BattleStage 因為查無此槽位而整個崩掉。 */
function asSlot(s: string): EnemySlotId {
  return (ENEMY_SLOTS as readonly string[]).includes(s) ? (s as EnemySlotId) : 'front_center';
}

function isFiniteNumber(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v);
}

/**
 * P2（暴擊／Miss／無效攻擊）：bootstrap 的 rating 子物件缺欄位、型別跑掉（例如 DB 髒資料把某個
 * 欄位存成字串）就整包丟棄退回 undefined——engine 的 toPartyActor/toEnemyActor 看到 undefined
 * 會自己呼叫 deriveDefaultPartyRating/deriveDefaultMonsterRating 補一份用 config 推導的後備值
 * （見 engine/index.ts），這比塞一份 NaN 混雜的 CombatRating 進去安全：NaN 會讓 missChance／
 * critChance 的 clamp/比較全部壞掉，變成「看似有算，其實每次都命中或都不命中」的隱性 bug。
 *
 * P3（AGI 攻速／DEX 詠唱縮減）：aspd/castReductionPct 是本輪新增欄位，舊版後端（本輪部署前）
 * 可能還沒送——這兩個缺欄位／型別跑掉時只退回中性預設值（等同「沒有攻速/詠唱加成」的表現，
 * 跟 engine/formulas.ts deriveDefaultPartyRating／deriveDefaultMonsterRating 的中性語意一致），
 * 不影響 hit/flee/critPct/critShield 已經驗證過的資料整包被丟棄。刻意跟核心 4 欄位「一壞全丟」
 * 的策略不同：核心 4 欄位少一個會讓 missChance/critChance 算出 NaN，這兩個新欄位少了只是
 * attackCooldownFor/effectiveCastMs 沒有加成可套用，不會讓其餘已驗證的評級資料一起陪葬。
 */
function asRating(r: RpgBootstrapRatingRaw | undefined): CombatRating | undefined {
  if (!r) return undefined;
  const { hit, flee, critPct, critShield, aspd, castReductionPct, critDmgPct } = r;
  if (!isFiniteNumber(hit) || !isFiniteNumber(flee) || !isFiniteNumber(critPct) || !isFiniteNumber(critShield)) {
    return undefined;
  }
  return {
    hit, flee, critPct, critShield,
    aspd: isFiniteNumber(aspd) ? aspd : DEFAULT_BATTLE_CONFIG.aspdReference,
    castReductionPct: isFiniteNumber(castReductionPct) ? castReductionPct : 0,
    // 審查#5：跟 aspd/castReductionPct 同一個策略——新欄位缺欄位／型別跑掉只給中性預設值 0
    // （沒有暴擊傷害加成），不連累核心 4 欄位「一壞全丟」的判斷。
    critDmgPct: isFiniteNumber(critDmgPct) ? critDmgPct : 0,
  };
}

/** P6：party member 的 skills（AI 可用技能）→ Skill[]，重用 mapSkill 逐項驗證／給預設值，
 *  跟頂層 sample.skills（玩家技能欄）走同一套轉換規則，只是這裡不需要保留 null（沒有「10 格
 *  裝備欄」的概念，AI 只在乎「有哪些技能可以挑」，見 engine/ai.ts 的挑選邏輯）。缺欄位（BACKEND
 *  尚未送、或本來就是沒有 skills 的舊資料）一律回傳 []，對齊 PartyMember.skills 型別註解的
 *  缺省語意。 */
function mapPartyMemberSkills(skills: RpgBootstrapSkillRaw[] | undefined): Skill[] {
  if (!Array.isArray(skills)) return [];
  return skills.map(mapSkill).filter((s): s is Skill => s !== null);
}

/**
 * P7（CONTRACT §2/§3、WIRE「戰鬥 bootstrap」）：wire 送的武器 profile（camelCase，戰鬥時真正
 * 被 engine 讀取套用的子集——atk/matk/critPct/critDmgPct 只是原樣透傳供顯示，見
 * dorpg/types.ts WeaponProfileWire 型別註解）。用 `unknown` 讀取、逐欄防禦驗證：武器系統的 wire
 * 資料來自後台自由編輯的 JSON（跟既有 asEffect()／isElementChart() 的防禦精神一致，即使 api.ts
 * 型別已經正確宣告，執行期資料仍可能因為後台編輯出的髒 JSON 而缺欄位/型別跑掉）。任何欄位缺失
 * 或型別不符都給中性預設值（不是整包丟棄）——武器 profile 本來就允許只設定部分效果（例如單手劍
 * 只給 atk_pct，其餘維持中性），這跟 asRating() 的「核心欄位一壞全丟」策略不同，是刻意的選擇。
 */
function asWeaponProfile(raw: unknown): WeaponProfileWire | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const num = (v: unknown, fallback: number) => (isFiniteNumber(v) ? v : fallback);
  const sb = r.sizeBonus && typeof r.sizeBonus === 'object' ? (r.sizeBonus as Record<string, unknown>) : {};
  return {
    atk: num(r.atk, 0),
    matk: num(r.matk, 0),
    hits: num(r.hits, 1),
    hitMul: num(r.hitMul, 1),
    extraHitChancePct: num(r.extraHitChancePct, 0),
    intervalPct: num(r.intervalPct, 0),
    chargeTimeMul: num(r.chargeTimeMul, 1),
    chargeDmgMul: num(r.chargeDmgMul, 1),
    splashPct: num(r.splashPct, 0),
    sizeBonus: { small: num(sb.small, 0), medium: num(sb.medium, 0), large: num(sb.large, 0) },
    critPct: num(r.critPct, 0),
    critDmgPct: num(r.critDmgPct, 0),
    elementResistPct: num(r.elementResistPct, 0),
    magicSkillPct: num(r.magicSkillPct, 0),
    element: asElement(typeof r.element === 'string' ? r.element : undefined) ?? 'neutral',
  };
}

/** P7：wire 送的整個裝備武器物件；profile 驗證失敗（缺失或形狀不對）視為整把武器資料無效，
 *  整包退回 null——沒有可信的戰鬥數值，裝備一把「不知道有什麼效果」的武器沒有意義。 */
function asEquippedWeapon(raw: unknown): EquippedWeaponWire | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  if (typeof r.id !== 'string' || typeof r.name !== 'string' || typeof r.typeId !== 'string') return null;
  const profile = asWeaponProfile(r.profile);
  if (!profile) return null;
  return {
    id: r.id,
    name: r.name,
    typeId: r.typeId,
    visual: asWeapon(typeof r.visual === 'string' ? r.visual : undefined, 'sword'),
    profile,
  };
}

/**
 * P8（DORPG_P8 CONTRACT §2、WIRE「戰鬥 bootstrap」）：wire 送的裝備效果彙總（camelCase，
 * INTEGRATOR 已在 api.ts RpgBootstrapEquipmentEffectsRaw 宣告六個必填數字欄位——見該介面型別
 * 註解）。逐欄防禦驗證，跟 asWeaponProfile() 同一個理由——即使 api.ts 型別已經正確宣告，執行期
 * 資料仍可能因為舊版後端尚未送這個欄位（p 本身是 undefined）或型別跑掉而缺欄位；任何欄位缺失或
 * 非有限數字都退回中性值 0（不是整包丟棄），讓「這個裝備效果只有部分欄位異常」不會連累其餘正常
 * 欄位失效。缺整個物件（undefined）時六欄全部退回 0，等同 NEUTRAL_EQUIPMENT_EFFECTS（不在這裡
 * import 那個常數，避免這個純轉換函式跟 engine 的執行期匯出產生不必要的耦合——兩者的「六個 0」
 * 語意上是同一份中性值，寫兩次比多一條 import 更符合 fromApi.ts 一貫的獨立防禦風格）。
 */
function asEquipmentEffects(raw: RpgBootstrapEquipmentEffectsRaw | undefined): EquipmentEffectsWire {
  const r = (raw ?? {}) as Partial<RpgBootstrapEquipmentEffectsRaw>;
  const num = (v: unknown, fallback: number) => (isFiniteNumber(v) ? v : fallback);
  return {
    intervalPct: num(r.intervalPct, 0),
    mpCostReducePct: num(r.mpCostReducePct, 0),
    hpRegenPctPer5s: num(r.hpRegenPctPer5s, 0),
    mpRegenPctPer5s: num(r.mpRegenPctPer5s, 0),
    damageTakenPct: num(r.damageTakenPct, 0),
    elementResistPct: num(r.elementResistPct, 0),
  };
}

/**
 * P9（DORPG_P9 CONTRACT §1／WIRE「戰鬥 bootstrap」：「每位 party member 新增 strategyId」）：
 * 缺欄位或不在 STRATEGY_IDS 白名單一律退回 'balanced'——跟 engine 內部
 * resolveStrategy(actor.strategyId, ...) 的「未知 id 退回 balanced」是同一個規則重複套用兩次
 * （這裡先擋一次髒字串，engine 那層是最後一道防線），並不衝突：這裡擋掉的是「wire 送了一個
 * STRATEGY_IDS 沒有的字串」這種情況，讓 PartyActor.strategyId 從一開始就是合法值。
 * ⚠️ RpgBootstrapPartyMemberRaw（api.ts）尚未宣告這個欄位（P9 INTEGRATOR 待補）——用區域擴充
 * 型別（`& { strategyId?: string }`）讀取，屬性缺省是安全的可選欄位，不需要 unknown-cast；
 * INTEGRATOR 之後把欄位補進 api.ts 本體時，這裡的擴充型別仍然相容（多一層不影響任何行為）。
 */
type PartyMemberWithStrategy = RpgBootstrapPartyMemberRaw & { strategyId?: string };
function asStrategyId(v: string | undefined): string {
  return typeof v === 'string' && (STRATEGY_IDS as readonly string[]).includes(v) ? v : 'balanced';
}

function mapPartyMember(pRaw: RpgBootstrapPartyMemberRaw): PartyMember {
  const p: PartyMemberWithStrategy = pRaw;
  return {
    id: p.id,
    name: p.name,
    level: p.level,
    hp: p.hp,
    hpMax: p.hpMax,
    mp: p.mp,
    mpMax: p.mpMax,
    portraitUrl: p.portraitUrl,
    stats: p.stats,
    // P1～P6 遺留的純視覺字串讀法：只有在 p.weapon 真的是合法 WeaponKind 字串時才會成功（見
    // asWeapon() 實作），對正式後端目前一律送物件/null 的 wire 是安全的 no-op（結果 undefined，
    // 由 engine/index.ts 的 `equippedWeapon?.visual ?? weapon ?? 'sword'` 三層 fallback接手）。
    weapon: asWeapon(typeof p.weapon === 'string' ? p.weapon : undefined),
    rating: asRating(p.rating),
    // P5：純透傳供 FRONTEND 顯示職業徽章用（見 PartyMember.jobId 型別註解），engine 戰鬥邏輯不讀它；
    // 缺欄位（舊版後端／api.ts 尚未補上）一律當「未選職業」。
    jobId: p.jobId ?? null,
    // P6（CONTRACT §3.2）：AI 可用技能／腳本名稱，見上方 mapPartyMemberSkills。
    skills: mapPartyMemberSkills(p.skills),
    presetName: p.presetName,
    // P7（CONTRACT §3、WIRE §戰鬥 bootstrap）：INTEGRATOR 已把 api.ts 的 `weapon` 欄位型別放寬成
    // 物件｜字串｜null（見該檔型別註解），這裡不再需要 unknown-cast——asEquippedWeapon() 只在
    // p.weapon 真的是合法物件形狀時才會成功，對字串/null 輸入直接短路回 null，跟上面 asWeapon()
    // 對物件輸入直接短路回 undefined 是同一種「兩個讀法互斥、各自防禦」設計，任何一種 wire 形狀
    // 都不會讓另一邊誤讀出垃圾值。
    equippedWeapon: asEquippedWeapon(p.weapon),
    // P8（CONTRACT §2／WIRE「戰鬥 bootstrap」）：見 asEquipmentEffects() 型別註解——缺欄位/整包
    // 缺失時安全退回中性值，不是 undefined（PartyMember.equipmentEffects 雖然型別上允許 undefined，
    // 但 fromApi.ts 這一層一律填好完整物件，讓後續 engine 端不必再處理「wire 到底有沒有送」的分支）。
    equipmentEffects: asEquipmentEffects(p.equipmentEffects),
    // P9：見上方 PartyMemberWithStrategy／asStrategyId 型別註解。
    strategyId: asStrategyId(p.strategyId),
  };
}

/** P5：weakElements 陣列裡混進非法字面值（DB 髒資料）就整個丟掉那一項，不讓 elementMultiplier()
 *  在執行期拿到非 ElementKind 字串——跟本檔其餘 as*() 系列函式「寧可丟棄也不塞髒資料」同一個原則。
 *  沿用檔案上方 asElement() 已經宣告的 ELEMENT_KINDS，不重複宣告一份。 */
function asElementList(v: string[] | undefined): ElementKind[] | undefined {
  if (!Array.isArray(v)) return undefined;
  return v.filter((x): x is ElementKind => (ELEMENT_KINDS as readonly string[]).includes(x));
}

function mapEnemy(e: RpgBootstrapEnemyRaw): Enemy {
  return {
    id: e.id,
    name: e.name,
    level: e.level,
    hp: e.hp,
    hpMax: e.hpMax,
    slot: asSlot(e.slot),
    imageUrl: e.imageUrl,
    rank: e.rank,
    attribute: e.attribute,
    size: e.size,
    race: e.race,
    stats: e.stats,
    threatPriority: e.threatPriority,
    // 2026-09-14 P2 修正第1輪 審查5：api.ts RpgBootstrapEnemyRaw.canEscape 已收斂成必有的 boolean
    // （後端 battle.go 的 json tag 本來就沒有 omitempty，此欄位恆為 true/false），不再需要防禦性地
    // 猜測 undefined 語意。
    canEscape: e.canEscape,
    rating: asRating(e.rating),
    // P5（CONTRACT §6）：缺欄位／型別跑掉一律當「無弱點」（[]），跟 engine/index.ts toEnemyActor
    // 的 `e.weakElements ?? []` 後備值語意一致。
    weakElements: asElementList(e.weakElements),
  };
}

const SKILL_KINDS: readonly Skill['kind'][] = ['damage', 'heal', 'shield', 'buff', 'debuff', 'passive', 'special'];
function asSkillKind(k: string): Skill['kind'] {
  return (SKILL_KINDS as readonly string[]).includes(k) ? (k as Skill['kind']) : 'damage';
}

const SKILL_TARGETS: readonly Skill['target'][] = ['enemy', 'ally', 'self', 'allAllies', 'allEnemies'];
function asSkillTarget(t: string): Skill['target'] {
  return (SKILL_TARGETS as readonly string[]).includes(t) ? (t as Skill['target']) : 'enemy';
}

const BUFF_DEBUFF_STATS: readonly BuffDebuffStat[] = [
  'atk_pct', 'matk_pct', 'def_pct', 'mdef_pct', 'aspd', 'crit_pct', 'flee', 'hit', 'hp_regen_pct', 'damage_taken_pct',
];
function asBuffDebuffStat(s: string | undefined): BuffDebuffStat | undefined {
  return s !== undefined && (BUFF_DEBUFF_STATS as readonly string[]).includes(s) ? (s as BuffDebuffStat) : undefined;
}

function asDmgType(d: string | undefined): DmgType | undefined {
  return d === 'magic' || d === 'physical' ? d : undefined;
}

/**
 * P5：wireSkill.effect（WIRE.md：「已依 level 展開」的即時數值）→ EffectAtLevel。kind/target 缺欄位
 * 時退回呼叫端傳入的技能本身 kind/target（跟頂層欄位保持一致，而不是塞一個可能對不上的預設值）；
 * mpCost 缺欄位時給 0（buff/debuff/passive 的 mpCost 理論上一定會有，這裡只是防禦寫法）。
 *
 * ⚠️ 審查#1(b) 修正：裸資料 e 的欄位是 snake_case（e.duration_ms/e.mp_cost，對齊後端 skills.go
 * EffectAtLevel 的 json tag／WIRE.md 逐字定義），輸出給 engine 用的才是 camelCase
 * （durationMs/mpCost，見 dorpg/types.ts EffectAtLevel）——這裡曾經兩邊都當 camelCase 讀，
 * 讓 e.durationMs/e.mpCost 永遠讀到 undefined，buff/debuff 的持續時間因此恆為 0。
 */
function asEffect(e: RpgBootstrapEffectRaw | undefined, fallbackKind: Skill['kind'], fallbackTarget: Skill['target']): EffectAtLevel | undefined {
  if (!e) return undefined;
  return {
    kind: e.kind !== undefined ? asSkillKind(e.kind) : fallbackKind,
    stat: asBuffDebuffStat(e.stat),
    value: isFiniteNumber(e.value) ? e.value : undefined,
    durationMs: isFiniteNumber(e.duration_ms) ? e.duration_ms : undefined,
    coef: isFiniteNumber(e.coef) ? e.coef : undefined,
    flat: isFiniteNumber(e.flat) ? e.flat : undefined,
    hits: isFiniteNumber(e.hits) ? e.hits : undefined,
    target: e.target !== undefined ? asSkillTarget(e.target) : fallbackTarget,
    mpCost: isFiniteNumber(e.mp_cost) ? e.mp_cost : 0,
  };
}

function mapSkill(s: RpgBootstrapSkillRaw | null): Skill | null {
  if (!s) return null;
  const kind = asSkillKind(s.kind);
  const target = asSkillTarget(s.target);
  const eff = s.effect;
  // 審查#1(b) 深度防禦：coefficient/flat/hits/mpCost 優先取 effect 展開值，其次才落到頂層 wire
  // 欄位——battle.go toWireSkillLeveled 已經修好讓頂層欄位等於展開值（見該函式回歸測試
  // battle_test.go），這裡再疊一層保險，萬一後端未來又出現「頂層沒展開、只有 effect 展開」的
  // 迴歸，戰鬥結算仍然正確，不會重演本輪審查抓到的 #1 缺陷。
  const coefficient = isFiniteNumber(eff?.coef) ? eff.coef : s.coefficient;
  const flat = isFiniteNumber(eff?.flat) ? eff.flat : s.flat;
  const hits = isFiniteNumber(eff?.hits) ? eff.hits : isFiniteNumber(s.hits) ? s.hits : undefined;
  const mpCost = isFiniteNumber(eff?.mp_cost) ? eff.mp_cost : s.mpCost;
  return {
    id: s.id,
    name: s.name,
    iconUrl: s.iconUrl,
    cooldownMs: s.cooldownMs,
    kind,
    target,
    mpCost,
    coefficient,
    flat,
    element: asElement(s.element),
    weapon: asWeapon(s.weapon, 'sword'),
    castMs: s.castMs,
    // P5 新欄位；hits 已在上面用深度防禦邏輯算好。缺欄位（舊版後端、既有 5 個一般技能）時維持
    // undefined，由 Skill 型別的缺省語意接手（dmgType→physical、implemented→true，見 types.ts
    // 各欄位註解）。
    hits,
    dmgType: asDmgType(s.dmgType),
    level: isFiniteNumber(s.level) ? s.level : undefined,
    maxLevel: isFiniteNumber(s.maxLevel) ? s.maxLevel : undefined,
    displayText: s.displayText,
    implemented: s.implemented,
    effect: asEffect(eff, kind, target),
    // P6（CONTRACT §3.2）：INTEGRATOR 已在 battle.go／api.ts 補上正式的 tier 欄位（2026-09-18，
    // 見下方型別註解），未選職業的既有 5 個技能仍缺這欄（後端 omitempty 送 0 或不送），
    // ai.ts 的 pickHighestTierSkill 對這些技能一樣退回陣列位置代理值。
    tier: isFiniteNumber(s.tier) ? s.tier : undefined,
  };
}

function mapItem(it: RpgBootstrapItemRaw): Item {
  return {
    id: it.id,
    name: it.name,
    iconUrl: it.iconUrl,
    quantity: it.quantity,
    kind: (it.kind === 'mp' || it.kind === 'revive' ? it.kind : 'hp') as Item['kind'],
    amount: it.amount,
  };
}

/** bootstrap 回應的 sample 區塊 → 引擎吃的 BattleSample（見檔頭對照表）。 */
export function sampleFromBootstrap(raw: RpgBootstrapSampleRaw): BattleSample {
  return {
    party: raw.party.map(mapPartyMember),
    enemies: raw.enemies.map(mapEnemy),
    scene: {
      id: raw.scene.id,
      name: raw.scene.name,
      imageUrl: raw.scene.imageUrl,
      slots: raw.scene.slots.map((s) => ({ id: asSlot(s.id), x: s.x, y: s.y, scale: s.scale, row: s.row })),
    },
    skills: raw.skills.map(mapSkill),
    items: raw.items.map(mapItem),
    initialTargetId: raw.initialTargetId,
    sceneKind: raw.sceneKind,
    escapeChance: raw.escapeChance,
  };
}

function isMsRange(v: unknown): v is [number, number] {
  return Array.isArray(v) && v.length === 2 && typeof v[0] === 'number' && typeof v[1] === 'number';
}

/**
 * P9（CONTRACT §1／WIRE「戰鬥 bootstrap」：「config 新增 aiStrategies」）：形狀比 [min,max] 複雜
 * ——巢狀物件（策略 id → { params: 自由 JSON }）。跟 isElementChart() 同一個防禦精神：整份驗證，
 * 有一層不對就整欄丟棄退回 DEFAULT_BATTLE_CONFIG.aiStrategies（空物件，等同「沒有任何 DB
 * 覆寫」），比讓 resolveStrategy() 在執行期讀到形狀不對的 params 物件安全——resolveStrategy 本身
 * 也會逐欄過濾非 number/boolean 的值，這裡只需要擋住「整包不是物件」這種更粗的錯誤。
 */
function isAiStrategiesMap(v: unknown): v is Record<string, { params?: Record<string, unknown> }> {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return false;
  return Object.values(v as Record<string, unknown>).every((row) => {
    if (row === null || typeof row !== 'object' || Array.isArray(row)) return false;
    const params = (row as Record<string, unknown>).params;
    return params === undefined || (params !== null && typeof params === 'object' && !Array.isArray(params));
  });
}

/**
 * P2 新增：battle_element_chart 是巢狀 map（怪物 attribute 中文 → 技能 element 英文 → 倍率），
 * 形狀比 [min,max] 複雜得多——後台「進階 JSON 編輯」或未來 DB 資料都可能塞出非預期形狀（例如某個
 * attribute 對到陣列而不是物件）。這裡整份驗證，只要有一層不對就整欄丟棄退回 DEFAULT_BATTLE_CONFIG
 * 的預設表，比讓 elementMultiplier() 在執行期查到非數字值安全。
 */
function isElementChart(v: unknown): v is Record<string, Record<string, number>> {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return false;
  return Object.values(v as Record<string, unknown>).every((row) => {
    if (row === null || typeof row !== 'object' || Array.isArray(row)) return false;
    return Object.values(row as Record<string, unknown>).every((n) => typeof n === 'number' && Number.isFinite(n));
  });
}

/**
 * bootstrap 回應的 config 區塊 → Partial<BattleConfig>。契約 §3.4：這個子物件直接對齊 engine 欄位名
 * （camelCase），理論上不需要轉換；這裡只做防禦——[min,max] 這種陣列欄位若形狀不對就整欄丟掉，交給
 * createBattle() 的 `{...DEFAULT_BATTLE_CONFIG, ...config}` 合併補上預設值，不會讓 engine 的
 * randRange() 吃到半殘資料而壞掉。缺欄位（包含整個 config 是 undefined）一律比照辦理。
 */
export function configFromBootstrap(raw: RpgBootstrapConfigRaw | null | undefined): Partial<BattleConfig> {
  if (!raw) return {};
  const cfg: Partial<BattleConfig> = { ...raw };
  if (!isMsRange(cfg.enemyActIntervalMs)) delete cfg.enemyActIntervalMs;
  if (!isMsRange(cfg.allyActIntervalMs)) delete cfg.allyActIntervalMs;
  if (cfg.elementChart !== undefined && !isElementChart(cfg.elementChart)) delete cfg.elementChart;
  if (cfg.aiStrategies !== undefined && !isAiStrategiesMap(cfg.aiStrategies)) delete cfg.aiStrategies;
  return cfg;
}
