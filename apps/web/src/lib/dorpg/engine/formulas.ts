// 引擎公式與目標挑選（純函式，無 React/DOM，無 Date.now()）。
// 型別引用在 Node type-stripping 下整段消失，不影響本檔被 node 直接 import 執行。
import type { ActorStats, EnemySlotId } from '../types';
import type { BattleConfig, BattleState, EnemyActor } from './types';

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

/**
 * 規格 §2：蓄氣倍率 = 1 + (max-1) × clamp((hold-chargeMinMs)/chargeFullMs, 0, 1)。
 * 用 cfg 欄位泛化（不寫死 300/1200/1.5/2.5），滿蓄時間 = chargeMinMs + chargeFullMs（預設 1500ms）。
 */
export function chargeMultiplier(holdMs: number, cfg: BattleConfig): number {
  const t = clamp((holdMs - cfg.chargeMinMs) / cfg.chargeFullMs, 0, 1);
  return 1 + (cfg.chargeMaxMultiplier - 1) * t;
}

/** UI 蓄氣條用的 0–1 比例；跟 chargeMultiplier 共用同一個 clamp 區間，只是不做倍率換算。 */
export function chargeRatio(holdMs: number, cfg: BattleConfig): number {
  return clamp((holdMs - cfg.chargeMinMs) / cfg.chargeFullMs, 0, 1);
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
    hpMax,
    mpMax,
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
    hpMax,
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
