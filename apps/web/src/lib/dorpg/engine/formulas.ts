// 引擎公式與目標挑選（純函式，無 React/DOM，無 Date.now()）。
// 型別引用在 Node type-stripping 下整段消失，不影響本檔被 node 直接 import 執行。
import type { ActorStats, CombatRating, EnemySlotId, WeaponProfileWire } from '../types';
import type { BattleConfig, BattleState, EnemyActor } from './types';

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

/**
 * P6（CONTRACT §1）：HP/MP 整數不變式共用的取整方式——一律 Math.floor（向下取整），不是
 * Math.round（就近取整）：契約原文逐項點名「傷害向下取整、治療向下取整、上限向下取整」，
 * 用同一個具名函式取代散落各處的 Math.round，讓「這裡是刻意配合契約的 floor，不是隨手選的
 * 取整方式」這件事在呼叫端一眼可見（也方便之後要稽核『所有 hp/mp 相關運算是否都改對了』時
 * grep 這個函式名即可，不必每個 Math.round/Math.floor 呼叫點都重新判斷語意）。只用在
 * hp/mp/hpMax/mpMax/shield 這條「整數不變式」清單內的欄位——ATK/DEF/MATK/MDEF 等戰鬥屬性
 * 不在契約要求範圍內，沿用原本的 Math.round（見 combat.ts/effects.ts 對這兩類欄位的取整方式
 * 刻意不同的說明）。
 */
export function floorInt(v: number): number {
  return Math.floor(v);
}

/**
 * 規格 §2：蓄氣倍率 = 1 + (max-1) × clamp((hold-chargeMinMs)/chargeFullMs, 0, 1)。
 * 用 cfg 欄位泛化（不寫死 300/1200/1.5/2.5），滿蓄時間 = chargeMinMs + chargeFullMs（預設 1500ms）。
 * P7（CONTRACT §3 巨劍）：新增可選參數 chargeTimeMul（預設 1，武器 profile.chargeTimeMul）——
 * 巨劍「增加物理攻擊的蓄氣時間」＝把 chargeFullMs 整個拉長，滿蓄要等更久；缺省 1 時算式跟 P1
 * 完全相同，不影響任何既有測試。武器對「傷害倍率」的額外加成（charge_dmg_mul）不在這裡套用
 * ——那是疊在這支函式回傳值之上的第二層調整，見呼叫端 dispatch.ts ATTACK_RELEASE 的
 * `1 + (rawChargeMul-1)×chargeDmgMul` 算式，理由是這支函式的回傳值同時也被拿去算 chargeRatio
 * 的等價分母，不該混進「傷害倍率」這個跟蓄氣進度無關的語意。
 */
export function chargeMultiplier(holdMs: number, cfg: BattleConfig, chargeTimeMul = 1): number {
  const fullMs = cfg.chargeFullMs * chargeTimeMul;
  const t = clamp((holdMs - cfg.chargeMinMs) / fullMs, 0, 1);
  return 1 + (cfg.chargeMaxMultiplier - 1) * t;
}

/** UI 蓄氣條用的 0–1 比例；跟 chargeMultiplier 共用同一個 clamp 區間與 chargeTimeMul 參數，只是
 *  不做倍率換算。 */
export function chargeRatio(holdMs: number, cfg: BattleConfig, chargeTimeMul = 1): number {
  return clamp((holdMs - cfg.chargeMinMs) / (cfg.chargeFullMs * chargeTimeMul), 0, 1);
}

/**
 * 規格 §2 傷害公式：raw = floor((atk×coef+flat)×elem×charge)；減甲 = max(1, raw-def)；
 * 防禦者再乘 guardDamageMultiplier 並四捨五入——順序不可換（規格明講先 max(1,...) 才乘防禦倍率），
 * 這樣測試角色 ATK135/DEF35/coef1/flat0 才會如規格算出 100，防禦時 40。
 */
export function computeDamage(
  atk: number,
  coefficient: number,
  flat: number,
  elementMul: number,
  chargeMul: number,
  def: number,
  guarded: boolean,
  cfg: BattleConfig,
): number {
  const raw = Math.floor((atk * coefficient + flat) * elementMul * chargeMul);
  let dmg = Math.max(1, raw - def);
  if (guarded) dmg = Math.round(dmg * cfg.guardDamageMultiplier);
  return dmg;
}

/** 規格 §2：治療 = floor(MATK×coefficient+flat)；封頂 hpMax 的動作交給呼叫端（這裡只算原始量）。 */
export function computeHeal(matk: number, coefficient: number, flat: number): number {
  return Math.floor(matk * coefficient + flat);
}

// ---- P2（暴擊／Miss／無效攻擊）新增：命中/暴擊評級公式、屬性倍率查表、不套底限的傷害公式。 ----

/**
 * SPEC §2：missPct = clamp(baseMissPct + (defender.flee − attacker.hit) × hitFleeScale, missMin, missMax)。
 * 攻守雙方通用——玩家/隊友打怪、怪物打隊友都呼叫這支，只是 attacker/defender 對調。
 */
export function missChance(attacker: CombatRating, defender: CombatRating, cfg: BattleConfig): number {
  const raw = cfg.baseMissPct + (defender.flee - attacker.hit) * cfg.hitFleeScale;
  return clamp(raw, cfg.missMinPct, cfg.missMaxPct);
}

/**
 * SPEC §2：critPct = max(0, attacker.critPct + battle_crit_rate×100 − defender.critShield)。
 * cfg.critRate（json tag 沿用 battle_crit_rate）語意已改為「全體基礎暴擊率」：所有戰鬥雙方
 * 都吃得到這個基準，不是只有玩家——LUK=0 的怪物一樣有這個基礎暴擊率（見 BattleConfig.critRate
 * 的型別註解）。下限 0：critShield 扣過頭時只是「不會被暴擊」，不會變成負機率。
 */
export function critChance(attacker: CombatRating, defender: CombatRating, cfg: BattleConfig): number {
  return Math.max(0, attacker.critPct + cfg.critRate * 100 - defender.critShield);
}

/**
 * P7（CONTRACT §1）：五行＋光暗「A 剋 M」有向表——key 剋 value。金→木、木→土、土→水、水→火、
 * 火→金（五行相剋方向，不是相生）；光↔闇互剋，各自佔一列（兩個方向都要在表裡各寫一次，不能只
 * 靠對稱推導，因為呼叫端是單向查表）。字面值對齊 ElementKind（不含 'neutral'——neutral 由
 * elementCycleMultiplier 開頭直接短路成 1，不會走到這張表）。
 */
const ELEMENT_BEATS: Readonly<Record<string, string>> = {
  metal: 'wood',
  wood: 'earth',
  earth: 'water',
  water: 'fire',
  fire: 'metal',
  light: 'dark',
  dark: 'light',
};

/**
 * P7（CONTRACT §1／BACKEND element_cases.json 的 chinese_* 系列案例）：現網 rpg_monsters.attribute
 * 舊資料仍是中文（migration 176 seed 刻意存中文原文，見 fixture.ts MonsterRow.attribute 型別
 * 註解），在 BACKEND 出遷移把既有資料轉成英文之前，五行相剋表要先認得這 8 個中文字面值，否則
 * 全部退化成「不相干→1.0」對正式怪物是 no-op。跟 Go 端（BACKEND ElementMultiplier 鏡像實作）
 * 用同一份對照表，兩邊對同一批中文輸入必須算出同一個結果（見 element_cases.json 的
 * chinese_metal／chinese_dark_light_counter／chinese_same_element／chinese_neutral 四個案例）。
 */
const CHINESE_ELEMENT_ALIASES: Readonly<Record<string, string>> = {
  金: 'metal',
  木: 'wood',
  水: 'water',
  火: 'fire',
  土: 'earth',
  光: 'light',
  闇: 'dark',
  無: 'neutral',
};

/** 中文別名→英文 ElementKind；已經是英文（或任何非表列字面值）原樣通過。
 *  審查#1【中】：原本只在本檔內部（elementCycleMultiplier／elementMultiplier 的 weakElements
 *  比對）使用，combat.ts applyPartyDamage 判斷「攻擊方是否為非 neutral 怪物」時卻直接比較
 *  attackerElement 原始字串，對正式庫「無」（中文中性別名）會被誤判成非中性、錯誤套用
 *  elementResistPct 減免——這裡改為 export，讓 combat.ts 能在比較前先正規化，兩處判斷「是否為
 *  neutral」的邏輯統一走同一份別名表，不再各自維護一份判斷標準。 */
export function normalizeElementAlias(v: string): string {
  return CHINESE_ELEMENT_ALIASES[v] ?? v;
}

/**
 * P7（INTEGRATOR 補：CONTRACT §1/§3「sizeBonus 對小/大型 +5%」對齊 attribute 的既有中文別名
 * 手法）：現網 rpg_monsters.size 跟 attribute 一樣是 migration 176 seed 的中文原文
 * （'大型'/'中型'/'小型'），combat.ts 原本只認英文字面值 small/medium/large，對正式怪物資料
 * 會恆是 0 加成（no-op）——跟 elementCycleMultiplier 對 attribute 中文字面值的處理是同一個問題、
 * 同一種解法，這裡補上對稱的別名表，在 BACKEND 出遷移把 rpg_monsters.size 轉成英文之前先讓
 * sizeBonus 對正式內容也生效；BACKEND 若日後把資料轉成英文，這裡的別名表原樣相容（英文值查表
 * 落空、直接回傳原值）。
 */
const CHINESE_SIZE_ALIASES: Readonly<Record<string, string>> = {
  大型: 'large',
  中型: 'medium',
  小型: 'small',
};

/** 中文體型別名→英文 size 字面值；已經是英文（或任何非表列字面值）原樣通過。 */
export function normalizeSizeAlias(v: string | undefined): string | undefined {
  if (v === undefined) return undefined;
  return CHINESE_SIZE_ALIASES[v] ?? v;
}

/**
 * P7（CONTRACT §1）：五行＋光暗相剋倍率——任一方 neutral（或缺值/空字串，正規化後才判斷）→ 1
 * （不看表，兩者都要是具體屬性才有相剋可言）；A===M（同屬性，非 neutral）→ elementSamePct；
 * A 剋 M（A 是 M 的剋星，查 ELEMENT_BEATS[A]===M）→ elementAdvantagePct；M 剋 A（A 反被 M 剋，
 * 查 ELEMENT_BEATS[M]===A）→ elementDisadvantagePct；其餘（例如金 vs 水，五行表中彼此不相剋也
 * 不相生）→ 1（不加成也不吃虧，跟 P5 時期「查無相剋關係」的中性語意一致）。兩個參數都先過
 * normalizeElementAlias（見上方型別註解）——中文舊資料因此也能正確走五行表，不只是英文枚舉。
 */
function elementCycleMultiplier(cfg: BattleConfig, attackElement: string, monsterAttribute: string): number {
  const a = normalizeElementAlias(attackElement || 'neutral');
  const m = normalizeElementAlias(monsterAttribute || 'neutral');
  if (a === 'neutral' || m === 'neutral') return 1;
  if (a === m) return 1 + cfg.elementSamePct / 100;
  if (ELEMENT_BEATS[a] === m) return 1 + cfg.elementAdvantagePct / 100;
  if (ELEMENT_BEATS[m] === a) return 1 + cfg.elementDisadvantagePct / 100;
  return 1;
}

/**
 * P7（CONTRACT §1，取代 P5 版本）：屬性倍率改成三層規則——
 *   1. battle_element_chart 管理者覆寫優先：查有 [enemy.attribute][attackElement] 這組 key 就
 *      直接用（可以是 0＝完全無效，呼叫端據此判 'immune'）——跟 P5 版本完全相同，不變。
 *   2. 查無覆寫 → 五行＋光暗相剋表（elementCycleMultiplier）算出一個基準倍率。
 *   3. 若 attackElement ∈ Enemy.weakElements（弱點桶）→ 跟步驟 2 的結果取「較大值」（弱點桶保證
 *      至少有 1+weaknessBonusPct/100 這麼多，即使五行表本身算出來是劣勢或無關）。
 * P5 時期「查無 chart 覆寫 → 只看 weakElements，否則 1.0」的規則被步驟 2/3 取代／擴充——弱點桶
 * 沒有被拿掉，只是不再是唯一的非覆寫規則，改成跟五行表並存取大（見 CONTRACT §1 原文「另：A ∈
 * 怪物 weak_elements → 至少 1+battle_weakness_bonus_pct/100（取兩者較大）」）。
 * 簽章對齊 WIRE.md「elementMultiplier(cfg, attackElement, enemy)」——cfg 打頭陣的既有慣例不變，
 * attackElement/enemy 對調順序（P5 版本是 (cfg, enemy, skillElement)，呼叫端已同步更新）。
 */
export function elementMultiplier(
  cfg: BattleConfig,
  attackElement: string,
  enemy: { attribute?: string; weakElements?: string[] } | undefined,
): number {
  const attribute = enemy?.attribute;
  if (attribute) {
    const row = cfg.elementChart[attribute];
    const v = row?.[attackElement];
    if (v !== undefined) return v;
  }
  let mul = elementCycleMultiplier(cfg, attackElement, attribute ?? 'neutral');
  // 審查#4【低】：Go 端 elements.go 對 weakElements 逐項 canonicalElement 正規化後才比對，這裡原本
  // 用原始字串 .includes(attackElement)——正式庫 weakElements 若存中文（例如 ['火']），attackElement
  // 傳進來的英文 'fire' 永遠比不中，弱點桶對中文資料整組失效。改成兩邊都先過 normalizeElementAlias
  // 再比對，跟 Go 端行為對齊（元素別名表本來就是中英雙邊都要能命中，不只是攻擊方）。
  const normalizedAttack = normalizeElementAlias(attackElement);
  if (enemy?.weakElements?.some((w) => normalizeElementAlias(w) === normalizedAttack)) {
    mul = Math.max(mul, 1 + cfg.weaknessBonusPct / 100);
  }
  return mul;
}

/**
 * P7（CONTRACT §3「無武器＝全部中性」）：所有數值欄位都是「不改變任何既有行為」的中性值——
 * hits=1/hitMul=1 等同單擊全額傷害、interval/charge 相關的 *Mul=1 等同不調整、*Pct=0 等同沒有
 * 額外效果、element='neutral' 等同 P1～P6 普攻／物理技能原本恆用的隱性屬性。engine/combat.ts 在
 * `actor.weaponProfile` 是 null 時一律 fallback 到這份常數，讓「沒有武器系統資料」與「明確裝備
 * 一把中性武器」在戰鬥數值上完全等價，也是既有（P7 之前）測試在沒有任何改動下仍應全數通過的
 * 根本原因——這份物件的每一個欄位都必須維持中性語意，日後若要改動這裡的預設值，等同改變「沒有
 * 武器」時的戰鬥手感，需要另外評估。
 */
export const NEUTRAL_WEAPON_PROFILE: WeaponProfileWire = {
  atk: 0,
  matk: 0,
  hits: 1,
  hitMul: 1,
  extraHitChancePct: 0,
  intervalPct: 0,
  chargeTimeMul: 1,
  chargeDmgMul: 1,
  splashPct: 0,
  sizeBonus: { small: 0, medium: 0, large: 0 },
  critPct: 0,
  critDmgPct: 0,
  elementResistPct: 0,
  magicSkillPct: 0,
  element: 'neutral',
};

/**
 * SPEC §4：raw = floor((atk×coef+flat) × elementMul × chargeMul × critMul)；net = raw − def。
 * 刻意不套 max(1,...)——net≤0 要能被呼叫端看出「完全被擋下」判成 'immune'，這是它跟既有
 * computeDamage（保留不動，仍給其他呼叫端與測試用）唯一但關鍵的差異。
 * ⚠️ 跟 deriveDefaultEnemyStats／fixture.ts 的 D2 怪物 HP 縮放公式 max(1, playerAtk−mobDef) 是
 * 兩回事、刻意不同：那支是「用預期打法估算血量池」（血量規劃不能因為 0/負值而失真），這支才是
 * 「實戰結算」（要能真的打出無效攻擊這個玩家看得到的戰鬥事件）。不要為了「看起來像同一條公式」
 * 而合併兩者。
 */
export function computeRawDamage(
  atk: number,
  coefficient: number,
  flat: number,
  elementMul: number,
  chargeMul: number,
  critMul: number,
  def: number,
): number {
  const raw = Math.floor((atk * coefficient + flat) * elementMul * chargeMul * critMul);
  return raw - def;
}

// ---- P3（AGI 攻速／DEX 詠唱縮減，審查 dorpg_p3 r5 使用者當面要求）新增。 ----

/**
 * 使用者當面要求：AGI→攻速(aspd)→攻擊冷卻，沿用 RO 的換算（攻擊間隔與 200−ASPD 成正比）：
 *   attackCooldownMs = clamp( cfg.attackCooldownMs × (200−aspd) / (200−cfg.aspdReference),
 *                              cfg.attackCooldownMinMs, cfg.attackCooldownMs )
 * aspd 等於 aspdReference 時算出的比例恰好是 1，冷卻＝base——「完全不配 AGI/DEX 的角色」維持
 * 上一輪就有的手感，只有真的配點才會感覺到差異。上界故意夾在 base（不是無限大）：aspd 低於
 * reference（配了負面效果或後台把 reference 設太高）時，攻擊絕不會比「沒有這個機制以前」更慢，
 * 只有 aspd 高於 reference 時才會變快。
 * 只套用在玩家身上（見 dispatch.ts 對 ATTACK_RELEASE 的呼叫點）；隊友的節奏固定吃
 * cfg.allyActIntervalMs、怪物固定吃 cfg.enemyActIntervalMs，兩者都不呼叫這支函式，AI 手感不受
 * 玩家配點影響——即使呼叫端不慎把很高的 rating.aspd 塞進隊友/怪物的 CombatRating，也不會有
 * 任何效果，因為根本沒有程式碼路徑會拿它們的 rating.aspd 來算冷卻。
 * rating.aspd 缺省（呼叫端手動組的 CombatRating 沒填這個新欄位，例如舊測試資料）時退回
 * cfg.aspdReference，等同中性表現，不會算出 NaN。
 *
 * P7（CONTRACT §3 細劍/長弓/弩/斧「interval_pct 乘在 attackCooldownFor 結果」）：新增可選參數
 * intervalPct（預設 0，武器 profile.intervalPct），在 AGI/DEX 換算出的冷卻（含 min/max clamp）
 * 之上再乘一次 `1+intervalPct/100`——負值（細劍/短弓/弩）變快、正值（長弓/斧）變慢。刻意放在
 * clamp 之後才乘（不是把 intervalPct 摻進 clamp 之前的算式），對齊契約原文「乘在...結果」的字面
 * 順序；預設 0 時這行等同 ×1，不影響任何既有測試。
 */
export function attackCooldownFor(rating: CombatRating, cfg: BattleConfig, intervalPct = 0): number {
  const aspd = rating.aspd ?? cfg.aspdReference;
  const denom = 200 - cfg.aspdReference;
  const base =
    denom <= 0 ? cfg.attackCooldownMs : clamp((cfg.attackCooldownMs * (200 - aspd)) / denom, cfg.attackCooldownMinMs, cfg.attackCooldownMs); // 防呆：aspdReference 誤設 ≥200 時避免除以零/負值把方向算反
  return base * (1 + intervalPct / 100);
}

/**
 * 使用者當面要求：DEX（經 internal/rpg Compute 算出的 CastReductionPct）→技能施放時間縮減：
 *   castMs_effective = max(cfg.castMinMs, round(baseCastMs × (1 − castReductionPct/100)))
 * 只套用在玩家身上（見 dispatch.ts commitCast 的呼叫點——AI 隊友的技能結算走 ai.ts 的
 * resolveSupportSkill，完全不經過 casting 狀態/commitCast，這支函式不會被拿去算隊友的施法時間）。
 * rating.castReductionPct 缺省時退回 0（不縮減），等同中性表現。
 */
export function effectiveCastMs(baseCastMs: number, rating: CombatRating, cfg: BattleConfig): number {
  const pct = rating.castReductionPct ?? 0;
  const reduced = baseCastMs * (1 - pct / 100);
  return Math.max(cfg.castMinMs, Math.round(reduced));
}

/**
 * 玩家/隊友沒有外部（internal/rpg Compute）評級資料時的後備評級（規格 §6：「battle_hit_rate
 * 保留但改為只在沒有評級資料時的後備路徑使用」）：hit 直接沿用舊欄位 cfg.hitRate（0–1 換算成
 * 0–100 的百分比制），flee/critPct/critShield 給 0——沒有更多資訊可以推導這幾項，給 0 代表
 * 「不額外加成也不額外扣分」，讓 missChance/critChance 的結果完全由 baseMissPct/critRate 這些
 * 全域基準決定（等同還沒有配點系統資料時的中性表現）。一旦呼叫端（PartyMember.rating）真的
 * 帶了 internal/rpg Compute 算出的評級，createBattle 就直接採用那份真實資料，這支函式完全不會
 * 被呼叫到。
 */
export function deriveDefaultPartyRating(cfg: BattleConfig): CombatRating {
  // P3：aspd 給 cfg.aspdReference（attackCooldownFor 在這個值算出的冷卻恰好是 base，等於
  // 「沒有配 AGI/DEX 加成」的中性表現）、castReductionPct 給 0（不縮短施法時間）——跟
  // hit/flee/critPct/critShield 給 0 是同一個精神：沒有更多資訊可以推導，就當作中性、不加成也不扣分。
  return { hit: cfg.hitRate * 100, flee: 0, critPct: 0, critShield: 0, aspd: cfg.aspdReference, castReductionPct: 0, critDmgPct: 0 };
}

/**
 * 怪物沒有個別覆寫時的預設評級：由 config 係數直接推導、跟等級無關（規格 §1 與既有
 * deriveDefaultEnemyStats「同款作法」的精神一致，但這裡刻意不做等級縮放——規格原文的
 * speed_mult／def_mult 是內容層/後端才有的怪物屬性資料，engine 這一層沒有這些資訊，
 * 交給呼叫端透過 Enemy.rating 直接帶入真實值來套用；engine 只保底最單純的 config 基準版本）。
 */
export function deriveDefaultMonsterRating(cfg: BattleConfig): CombatRating {
  return {
    hit: cfg.monsterHitBase,
    flee: cfg.monsterFleeBase,
    critPct: cfg.monsterCritPct,
    critShield: cfg.monsterCritShieldBase,
    // P3：怪物沒有 attackCooldownFor/effectiveCastMs 可套（敵人節奏固定吃 enemyActIntervalMs，
    // 不看 rating.aspd），這兩個欄位只是把 CombatRating 填滿成完整值，語意上等同「跟玩家
    // aspdReference 打平、不縮減施法」的中性表現。
    aspd: cfg.aspdReference,
    castReductionPct: 0,
    critDmgPct: 0,
  };
}

/** 敵人固定站位序（規格 §1：同 threatPriority 取槽位順序最小者）。 */
const SLOT_ORDER: EnemySlotId[] = ['rear_left', 'rear_right', 'front_left', 'front_center', 'front_right'];

/** 存活敵人中挑最高 threatPriority、同序取槽位順序最小者；共用於初始選取與死亡後換目標。 */
export function selectAliveByThreat(enemies: EnemyActor[]): string | null {
  const alive = enemies.filter((e) => e.hp > 0);
  if (alive.length === 0) return null;
  alive.sort((a, b) => {
    if (b.threatPriority !== a.threatPriority) return b.threatPriority - a.threatPriority;
    return SLOT_ORDER.indexOf(a.slot) - SLOT_ORDER.indexOf(b.slot);
  });
  return alive[0].id;
}

/** 載入完成的初始目標挑選（呼叫端若有 sample.initialTargetId 應優先採用，只有它無效時才落到這裡）。 */
export function pickInitialTarget(enemies: EnemyActor[]): string | null {
  return selectAliveByThreat(enemies);
}

/** 目標死亡後自動換下一個；規則與初始選取相同，只是簽章換成整個 BattleState 方便呼叫端直接傳。 */
export function pickNextTarget(state: BattleState): string | null {
  return selectAliveByThreat(state.enemies);
}

/** 在 [min,max] 均勻取一個整數毫秒（敵人/隊友 AI 排程用；rng 由呼叫端注入以利測試用固定序列）。 */
export function randRange(rng: () => number, range: readonly [number, number]): number {
  const [min, max] = range;
  return Math.round(min + rng() * (max - min));
}

/**
 * 缺省隊員數值（本次任務指定）：規格測試角色 ATK135/MATK80/DEF35/MDEF28 依 level/56 線性縮放並四捨五入；
 * hpMax/mpMax 直接取樣本自己的值（樣本的血量本來就是依該等級設計的展示值，不需要再縮放一次）。
 */
export function deriveDefaultActorStats(level: number, hpMax: number, mpMax: number): ActorStats {
  const scale = level / 56;
  return {
    // P6（CONTRACT §1）：hpMax/mpMax 一律 floor——呼叫端（PartyMember.hpMax/mpMax）理論上已經是
    // 整數（Go Derived.MaxHP/MaxMP 本身是 int），這裡只是最後一道防線，不讓任何上游浮點誤差
    // （或未來資料來源）流進戰鬥狀態變成小數 HP。
    hpMax: floorInt(hpMax),
    mpMax: floorInt(mpMax),
    atk: Math.round(135 * scale),
    matk: Math.round(80 * scale),
    def: Math.round(35 * scale),
    mdef: Math.round(28 * scale),
  };
}

/**
 * 缺省敵人數值（本次任務指定）：atk=matk=18+level×2.2、def=round(level×0.9)、mdef=round(level×0.7)；
 * atk/matk 一併四捨五入成整數（規格的角色數值向來是整數）。敵人不施法，mpMax 給 0。
 */
export function deriveDefaultEnemyStats(level: number, hpMax: number): ActorStats {
  return {
    // P6（CONTRACT §1）：同 deriveDefaultActorStats，floor 是最後一道防線。
    hpMax: floorInt(hpMax),
    mpMax: 0,
    atk: Math.round(18 + level * 2.2),
    matk: Math.round(18 + level * 2.2),
    def: Math.round(level * 0.9),
    mdef: Math.round(level * 0.7),
  };
}

/** 敵人挑目標：隨機存活隊員、玩家權重 2（規格 §2 敵人 AI 規則）。 */
export function pickWeightedAliveTarget(
  party: { id: string; isPlayer: boolean; hp: number }[],
  rng: () => number,
): string | null {
  const weighted: string[] = [];
  for (const p of party) {
    if (p.hp <= 0) continue;
    weighted.push(p.id);
    if (p.isPlayer) weighted.push(p.id);
  }
  if (weighted.length === 0) return null;
  const idx = Math.min(weighted.length - 1, Math.floor(rng() * weighted.length));
  return weighted[idx];
}
