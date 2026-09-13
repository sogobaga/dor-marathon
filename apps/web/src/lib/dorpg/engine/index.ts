// DORPG 戰鬥引擎入口（P1，契約 §2）：純函式、無 React、時間一律由呼叫端傳入、RNG 可注入。
// 對外的公開介面只有這裡列出的具名 export；內部切成 types/context/formulas/combat/ai/tick/dispatch
// 方便維護，其他工作者只需要依賴這個檔名匯出的東西。
import type { BattleSample, Enemy, PartyMember } from '../types';
import { deriveDefaultActorStats, deriveDefaultEnemyStats, pickInitialTarget, randRange } from './formulas';
import type { BattleConfig, BattleEvent, BattleState, EnemyActor, PartyActor } from './types';
import { DEFAULT_BATTLE_CONFIG } from './types';

export * from './types';
export { chargeMultiplier, chargeRatio, computeDamage, computeHeal, pickInitialTarget, pickNextTarget } from './formulas';
export { dispatch } from './dispatch';
export { tick } from './tick';

function toPartyActor(pm: PartyMember, index: number, now: number, cfg: BattleConfig, rng: () => number): PartyActor {
  const isPlayer = index === 0; // 規格：玩家只操控自己的角色，其他隊員由 AI 控制——約定 party[0] 是玩家。
  return {
    id: pm.id,
    name: pm.name,
    level: pm.level,
    stats: pm.stats ?? deriveDefaultActorStats(pm.level, pm.hpMax, pm.mpMax),
    hp: pm.hp,
    mp: pm.mp,
    shield: 0,
    action: 'idle',
    actionUntil: now,
    // 玩家一上場就能打；AI 隊友的首次行動時間錯開，避免四人同一刻一起出手。
    attackReadyAt: isPlayer ? now : now + randRange(rng, cfg.allyActIntervalMs),
    chargeStartedAt: null,
    isPlayer,
    portraitUrl: pm.portraitUrl,
    slotIndex: index,
    weapon: pm.weapon ?? 'sword',
  };
}

function toEnemyActor(e: Enemy, now: number, cfg: BattleConfig, rng: () => number): EnemyActor {
  return {
    id: e.id,
    name: e.name,
    level: e.level,
    slot: e.slot,
    stats: e.stats ?? deriveDefaultEnemyStats(e.level, e.hpMax),
    hp: e.hp,
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
  };
}

export function createBattle(
  sample: BattleSample,
  opts: { now: number; rng?: () => number; config?: Partial<BattleConfig> },
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
  };
}

/** 把累積的事件交給 UI 消化並清空；純消費動作，不算戰鬥狀態變化，不動 seq。 */
export function drainEvents(state: BattleState): { events: BattleEvent[]; state: BattleState } {
  return { events: state.events, state: { ...state, events: [] } };
}
