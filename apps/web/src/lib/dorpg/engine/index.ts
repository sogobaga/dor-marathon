// DORPG 戰鬥引擎入口（P1，契約 §2）：純函式、無 React、時間一律由呼叫端傳入、RNG 可注入。
// 對外的公開介面只有這裡列出的具名 export；內部切成 types/context/formulas/combat/ai/tick/dispatch
// 方便維護，其他工作者只需要依賴這個檔名匯出的東西。
import type { BattleSample, Enemy, PartyMember } from '../types';
import {
  deriveDefaultActorStats,
  deriveDefaultEnemyStats,
  deriveDefaultMonsterRating,
  deriveDefaultPartyRating,
  floorInt,
  NEUTRAL_EQUIPMENT_EFFECTS,
  pickInitialTarget,
  randRange,
} from './formulas';
import type { BattleConfig, BattleEvent, BattleState, EnemyActor, PartyActor } from './types';
import { DEFAULT_BATTLE_CONFIG } from './types';

export * from './types';
export {
  attackCooldownFor,
  chargeMultiplier,
  chargeRatio,
  computeDamage,
  computeHeal,
  computeRawDamage,
  critChance,
  effectiveCastMs,
  elementMultiplier,
  missChance,
  // P7（CONTRACT §3「無武器＝全部中性」）：外部（fixture.ts／verify 腳本）偶爾需要直接引用這個
  // 中性常數（例如比對「沒有武器」跟「明確裝備一把中性武器」是否真的算出同一組數值），一併匯出。
  NEUTRAL_WEAPON_PROFILE,
  // P8：同上精神，外部（fromApi.ts／fixture.ts／verify 腳本）需要一份中性裝備效果基準時直接引用。
  NEUTRAL_EQUIPMENT_EFFECTS,
  effectiveMpCost,
  combineIntervalPct,
  combineElementResistPct,
  pickInitialTarget,
  pickNextTarget,
} from './formulas';
// P5：buff/debuff 狀態效果的查詢／套用／暴擊倍率抽樣（見 effects.ts）；跟 formulas.ts 分開匯出檔案
// 但一樣攤平在引擎的公開介面上，呼叫端不需要知道內部是哪個檔案實作的。
export {
  activeStatSum,
  applyStatusEffect,
  damageTakenMultiplier,
  effectiveRating,
  effectiveStats,
  pruneAndRegenEffects,
  rollCritMultiplier,
} from './effects';
export { dispatch } from './dispatch';
export { tick } from './tick';
// P9（CONTRACT §1／WIRE「引擎」）：AI 策略 registry 與決策入口——匯出給 FRONTEND（策略選單/
// 顯示）與驗證腳本（每種策略的決定性斷言）直接使用，不必各自重新 import 內部模組路徑。
export { boolParam, numParam, resolveStrategy, STRATEGY_IDS } from './strategies';
export type { ResolvedStrategy, StrategyDef, StrategyId, StrategyParams } from './strategies';
export { decideAction } from './ai';
export { advanceAutoBattle } from './autopilot';

function toPartyActor(pm: PartyMember, index: number, now: number, cfg: BattleConfig, rng: () => number): PartyActor {
  const isPlayer = index === 0; // 規格：玩家只操控自己的角色，其他隊員由 AI 控制——約定 party[0] 是玩家。
  return {
    id: pm.id,
    name: pm.name,
    level: pm.level,
    // P6（CONTRACT §1）：「createBattle 時把 wire 進來的數值也 floor」——stats 有給（wire 送整包
    // ActorStats）就地把 hpMax/mpMax 也 floor 一次；沒給就交給 deriveDefaultActorStats（該函式
    // 內部已經 floorInt 過，見 formulas.ts）。
    stats: pm.stats ? { ...pm.stats, hpMax: floorInt(pm.stats.hpMax), mpMax: floorInt(pm.stats.mpMax) } : deriveDefaultActorStats(pm.level, pm.hpMax, pm.mpMax),
    hp: floorInt(pm.hp),
    mp: floorInt(pm.mp),
    shield: 0,
    action: 'idle',
    actionUntil: now,
    // 玩家一上場就能打；AI 隊友的首次行動時間錯開，避免四人同一刻一起出手。
    attackReadyAt: isPlayer ? now : now + randRange(rng, cfg.allyActIntervalMs),
    chargeStartedAt: null,
    isPlayer,
    portraitUrl: pm.portraitUrl,
    slotIndex: index,
    // P7（CONTRACT §3「武器視覺＝type.visual（覆蓋職業預設）」）：equippedWeapon 存在時視覺特效組
    // 改由武器類型的 visual 決定，否則落回既有的 pm.weapon（職業/隊友預設視覺字串），缺省 'sword'
    // ——三層 fallback 完全對齊 PartyMember.equippedWeapon 型別註解描述的規則。
    weapon: pm.equippedWeapon?.visual ?? pm.weapon ?? 'sword',
    // P7：武器戰鬥效果；沒有裝備（undefined/null）一律 null，combat.ts 各處讀取時再 fallback 成
    // NEUTRAL_WEAPON_PROFILE（見該常數型別註解），這裡不預先展開中性值——保持「有沒有裝備」這件事
    // 在 PartyActor 層級仍然可以被明確分辨（例如未來裝備頁想顯示「目前沒有武器」的空狀態）。
    weaponProfile: pm.equippedWeapon?.profile ?? null,
    // P8：裝備（防具＋飾品）效果彙總；跟 weaponProfile 不同，這裡在 PartyActor 層級直接展開成
    // 具體的中性值（不保留 null 分支）——見 PartyActor.equipmentEffects 型別註解，契約明講
    // 「傭兵一律零值物件」，沒有「有沒有裝備」這件事需要被區分。
    equipmentEffects: pm.equipmentEffects ?? NEUTRAL_EQUIPMENT_EFFECTS,
    // P8（WIRE「引擎」）：每 5000ms 戰鬥時鐘觸發一次裝備定時回復，第一次排在建場後的 +5000ms
    // （跟 hp_regen_pct buff 的「套用時刻+1000ms」同一個「不要一開場/一套用就立刻回一次」精神）。
    nextEquipRegenAt: now + 5000,
    // P2：有真實評級（internal/rpg Compute 算出來的）就直接用，沒有就退回 config 推導的後備值。
    rating: pm.rating ?? deriveDefaultPartyRating(cfg),
    // P5：戰鬥開始時沒有任何 buff（buff 只能在戰鬥中靠技能施放取得，不存在「開場自帶」的設計）。
    activeEffects: [],
    jobId: pm.jobId ?? null,
    // P6（CONTRACT §3.2）：見 PartyActor.skills/presetName 型別註解；玩家沒有這個欄位（stays []）。
    skills: pm.skills ?? [],
    presetName: pm.presetName ?? null,
    // P9（CONTRACT §1）：原始字串直接帶入，未知/缺省一律先給 'balanced'——真正的白名單正規化
    // （含 DB 覆寫）留給 decideAction 呼叫前的 resolveStrategy(actor.strategyId, ctx.cfg.
    // aiStrategies) 統一處理（見該函式與 PartyActor.strategyId 型別註解）。
    strategyId: pm.strategyId ?? 'balanced',
  };
}

function toEnemyActor(e: Enemy, now: number, cfg: BattleConfig, rng: () => number): EnemyActor {
  return {
    id: e.id,
    name: e.name,
    level: e.level,
    slot: e.slot,
    // P6（CONTRACT §1）：同 toPartyActor，wire 進來的 hpMax/mpMax 也 floor 一次。
    stats: e.stats ? { ...e.stats, hpMax: floorInt(e.stats.hpMax), mpMax: floorInt(e.stats.mpMax) } : deriveDefaultEnemyStats(e.level, e.hpMax),
    hp: floorInt(e.hp),
    // P1 沒有召喚機制，略過 spawning；hp<=0 直接給 removed（例如測試用的「一開場就平手」樣本資料）
    // ——不然會卡在 idle 永遠等不到 applyEnemyDamage 幫它轉場成 dying，resolving 階段就無法判斷
    // 「所有敵人的死亡動畫都播完了」（見 tick.ts 的 advanceResolving）。
    anim: e.hp > 0 ? 'idle' : 'removed',
    animUntil: now,
    nextActAt: now + randRange(rng, cfg.enemyActIntervalMs),
    threatPriority: e.threatPriority ?? 0,
    imageUrl: e.imageUrl,
    rank: e.rank,
    attribute: e.attribute,
    size: e.size,
    race: e.race,
    // P2：怪物沒有個別覆寫就用 config 係數推導的預設（跟等級無關，見 formulas.ts 的註解）。
    rating: e.rating ?? deriveDefaultMonsterRating(cfg),
    // P5：戰鬥開始時沒有任何 debuff；weakElements 缺省 []（無弱點）。
    activeEffects: [],
    weakElements: e.weakElements ?? [],
  };
}

export function createBattle(
  sample: BattleSample,
  opts: {
    now: number;
    rng?: () => number;
    config?: Partial<BattleConfig>;
    /**
     * P9（CONTRACT §1／WIRE「戰鬥 bootstrap」：「頂層新增 autoBattle: boolean」）：bootstrap
     * 回應的 autoBattle 是整個回應的頂層欄位（跟 sample/config 平行，不在 sample 裡面），
     * 跟既有的 opts.config 是同一種「wire 頂層欄位經由 opts 傳進 createBattle」的做法——呼叫端
     * （FRONTEND）直接把 `bootstrap.autoBattle` 傳進來即可，不需要額外的 fromApi.ts 轉換函式
     * （純布林，缺省/型別不對就當 false，呼叫端用 `Boolean(bootstrap.autoBattle)` 或 `??false`
     * 保底）。缺省 false（離線預覽／舊版後端尚未送這個欄位時，自動戰鬥預設關閉）。
     */
    autoBattle?: boolean;
  },
): BattleState {
  const { now } = opts;
  const rng = opts.rng ?? Math.random;
  const config: BattleConfig = { ...DEFAULT_BATTLE_CONFIG, ...opts.config };

  const party = sample.party.map((pm, i) => toPartyActor(pm, i, now, config, rng));
  const enemies = sample.enemies.map((e) => toEnemyActor(e, now, config, rng));

  const declaredInitial = enemies.find((e) => e.id === sample.initialTargetId && e.hp > 0);
  const targetId = declaredInitial ? declaredInitial.id : pickInitialTarget(enemies);

  // 規格：「BOSS 可設 canEscape=false（逃跑鈕直接停用）」——只要有一隻不可逃，這場戰鬥就永遠不能逃。
  const hasUnescapableEnemy = sample.enemies.some((e) => e.canEscape === false);

  const events: BattleEvent[] = [];
  return {
    phase: 'active', // P1 無伺服器載入步驟，createBattle 完成即視為 active。
    outcome: null,
    now,
    seq: 0,
    playerId: party[0].id,
    party,
    enemies,
    targetId,
    trayMode: 'skills',
    targeting: { mode: 'none' },
    skills: sample.skills,
    skillReadyAt: {},
    items: sample.items.map((def) => ({ def, quantity: def.quantity })),
    escape: {
      flow: hasUnescapableEnemy ? 'unavailable' : 'available',
      judgingUntil: null,
      chance: sample.escapeChance ?? 0.35,
      message: '',
    },
    config,
    events,
    log: ['戰鬥開始'],
    rng,
    pendingCasts: {},
    enemyTargets: {},
    resolvingSince: null,
    aiSkillReadyAt: {},
    autoBattle: opts.autoBattle ?? false,
    focusTargetId: null,
  };
}

/** 把累積的事件交給 UI 消化並清空；純消費動作，不算戰鬥狀態變化，不動 seq。 */
export function drainEvents(state: BattleState): { events: BattleEvent[]; state: BattleState } {
  return { events: state.events, state: { ...state, events: [] } };
}
