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
  RpgBootstrapEnemyRaw,
  RpgBootstrapItemRaw,
  RpgBootstrapPartyMemberRaw,
  RpgBootstrapRatingRaw,
  RpgBootstrapSampleRaw,
  RpgBootstrapSkillRaw,
} from '@/lib/api';
import type { BattleSample, CombatRating, ElementKind, Enemy, EnemySlotId, Item, PartyMember, Skill, WeaponKind } from '@/lib/dorpg/types';
import type { BattleConfig } from '@/lib/dorpg/engine';
// P3：asRating() 用它的 aspdReference 當 rating.aspd 缺欄位時的中性後備值（見該函式註解）——
// 只借用這個已凍結匯出的常數，不是改動 engine 本身，跟 fixture.ts 借用同一個常數的方式一致。
import { DEFAULT_BATTLE_CONFIG } from '@/lib/dorpg/engine';

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
  const { hit, flee, critPct, critShield, aspd, castReductionPct } = r;
  if (!isFiniteNumber(hit) || !isFiniteNumber(flee) || !isFiniteNumber(critPct) || !isFiniteNumber(critShield)) {
    return undefined;
  }
  return {
    hit, flee, critPct, critShield,
    aspd: isFiniteNumber(aspd) ? aspd : DEFAULT_BATTLE_CONFIG.aspdReference,
    castReductionPct: isFiniteNumber(castReductionPct) ? castReductionPct : 0,
  };
}

function mapPartyMember(p: RpgBootstrapPartyMemberRaw): PartyMember {
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
    weapon: asWeapon(p.weapon),
    rating: asRating(p.rating),
  };
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
  };
}

const SKILL_TARGETS: readonly Skill['target'][] = ['enemy', 'ally', 'self', 'allAllies'];
function asSkillTarget(t: string): Skill['target'] {
  return (SKILL_TARGETS as readonly string[]).includes(t) ? (t as Skill['target']) : 'enemy';
}

function mapSkill(s: RpgBootstrapSkillRaw | null): Skill | null {
  if (!s) return null;
  return {
    id: s.id,
    name: s.name,
    iconUrl: s.iconUrl,
    cooldownMs: s.cooldownMs,
    kind: (s.kind === 'heal' || s.kind === 'shield' ? s.kind : 'damage') as Skill['kind'],
    target: asSkillTarget(s.target),
    mpCost: s.mpCost,
    coefficient: s.coefficient,
    flat: s.flat,
    element: asElement(s.element),
    weapon: asWeapon(s.weapon, 'sword'),
    castMs: s.castMs,
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
  return cfg;
}
