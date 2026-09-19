// P9（DORPG_P9 CONTRACT §1／§4、WIRE「引擎」）：AI 策略 registry——「id → 定義（每策略的預設
// 參數）」。行為本身（要不要治療/上buff/打誰/選哪顆技能）寫死在 ai.ts 的 decide* 函式與
// autopilot.ts，這裡只管「有哪些合法 id、每個 id 的預設門檻/開關是什麼」；後台 rpg_ai_strategies
// 只能覆寫這裡列出的參數與顯示欄位（名稱/說明/啟用/排序），不能新增這裡沒有的行為或 id
// ——CONTRACT §1「新增行為＝在 registry 加一項＋seed 一列」，本輪只加六種既有行為，未來要擴充
// 新策略時，兩邊（這裡的 STRATEGY_IDS/STRATEGIES＋migration 186 的 seed）都要同步新增一列。

/** CONTRACT §2：`rpg_ai_strategies.id` 的合法值，唯一的權威清單——DB／wire 送來的字串只跟這份
 *  清單比對，不存在的一律退回 'balanced'（見 resolveStrategy）。 */
export const STRATEGY_IDS = [
  'balanced',
  'mp_conserve',
  'skill_aggressive',
  'protect_allies',
  'focus_fire',
  'element_advantage',
] as const;

export type StrategyId = (typeof STRATEGY_IDS)[number];

/**
 * decide* 函式讀取的門檻／開關集合：數字＝百分比門檻（heal_pct 等），布林＝開關（auto_guard，
 * 只有 autopilot.ts 的玩家自動戰鬥會讀）。刻意用寬鬆的 Record（不逐一列舉每個策略各自的 key），
 * 因為 DB 覆寫是自由 JSON——讀取端一律用 numParam/boolParam 帶預設值安全取值，缺欄位或型別跑掉
 * 不會讓其它欄位一起壞掉（跟 fromApi.ts 一貫的「單一欄位壞掉不連累整包」防禦風格一致）。
 */
export type StrategyParams = Record<string, number | boolean>;

export interface StrategyDef {
  id: StrategyId;
  defaultParams: StrategyParams;
}

export interface ResolvedStrategy {
  id: StrategyId;
  params: StrategyParams;
}

/**
 * CONTRACT §4：balanced 的 heal_pct=50／self_heal_floor_pct=15 就是 ai.ts 原本寫死的 50%/15%，
 * 本輪只是把常數搬進這裡當「可被 DB 覆寫的預設值」——balanced 本身的實際行為數字完全不變
 * （零改動、既有 375 條斷言必須維持通過，見 ai.ts decideBalanced）。
 * CONTRACT §5：`auto_guard` 是每個策略都有、只給玩家自動戰鬥用的「windup 鎖定自己時要不要自動
 * 防禦」開關——balanced／protect_allies（求穩型策略）預設 true，其餘（積極輸出/專注型）預設
 * false，免得自動防禦打斷輸出節奏。
 */
const STRATEGIES: Record<StrategyId, StrategyDef> = {
  balanced: { id: 'balanced', defaultParams: { heal_pct: 50, self_heal_floor_pct: 15, auto_guard: true } },
  mp_conserve: { id: 'mp_conserve', defaultParams: { mp_reserve_pct: 50, emergency_heal_pct: 30, auto_guard: false } },
  skill_aggressive: { id: 'skill_aggressive', defaultParams: { min_mp_reserve_pct: 0, auto_guard: false } },
  protect_allies: { id: 'protect_allies', defaultParams: { heal_pct: 60, shield_pct: 75, auto_guard: true } },
  focus_fire: { id: 'focus_fire', defaultParams: { auto_guard: false } },
  element_advantage: { id: 'element_advantage', defaultParams: { auto_guard: false } },
};

function isStrategyId(id: string): id is StrategyId {
  return (STRATEGY_IDS as readonly string[]).includes(id);
}

/**
 * id → 完整策略（CONTRACT §1「未知 id 一律退回 balanced」）；DB 覆寫
 * （`aiStrategies[resolvedId].params`）淺合併蓋過 registry 預設值，只接受數字／布林（其餘型別
 * 視為後台 JSON 編輯出的髒資料，忽略該欄位、其它欄位不受影響——同 fromApi.ts 的防禦風格）。
 * 這是整個 P9 AI 系統唯一做「id 正規化」的地方：PartyActor.strategyId／SET_AUTO_BATTLE 的
 * strategyId 都只是原始字串，真正決策/執行前一定要先經過這支函式。
 */
export function resolveStrategy(id: string, aiStrategies?: Record<string, { params?: Record<string, unknown> }>): ResolvedStrategy {
  const resolvedId: StrategyId = isStrategyId(id) ? id : 'balanced';
  const def = STRATEGIES[resolvedId];
  const override = aiStrategies?.[resolvedId]?.params;
  const params: StrategyParams = { ...def.defaultParams };
  if (override && typeof override === 'object') {
    for (const [k, v] of Object.entries(override)) {
      if (typeof v === 'number' && Number.isFinite(v)) {
        // 2026-09-19 修復：key 以 `_pct` 結尾的一律是百分比門檻（heal_pct/mp_reserve_pct/…，
        // ai.ts decide* 直接拿去跟「目前血量/MP 佔上限的百分比」比大小）——後台是自由 JSON 輸入，
        // 沒有夾限的話填個 1000 或 -50 會讓判斷式永遠成立或永遠不成立（例如 heal_pct=1000
        // 等同「血量必定低於門檻」，heal_pct=-50 等同「永遠不治療」），屬於髒資料而非合理的策略
        // 調校。其餘數值（非 _pct，例如純數量/開關性質的門檻）維持原本只驗 finite，不額外收斂。
        params[k] = k.endsWith('_pct') ? Math.min(100, Math.max(0, v)) : v;
      } else if (typeof v === 'boolean') params[k] = v;
    }
  }
  return { id: resolvedId, params };
}

/** 安全讀取數字參數，缺欄位/型別跑掉一律退回 fallback（呼叫端不必自己判斷 typeof）。 */
export function numParam(params: StrategyParams, key: string, fallback: number): number {
  const v = params[key];
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback;
}

/** 安全讀取布林參數，缺欄位/型別跑掉一律退回 fallback。 */
export function boolParam(params: StrategyParams, key: string, fallback: boolean): boolean {
  const v = params[key];
  return typeof v === 'boolean' ? v : fallback;
}
