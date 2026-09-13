// 引擎內部工作狀態：dispatch/tick 在一次呼叫內把 BattleState 拆成可變動的工作物件（Ctx）直接改，
// 呼叫結束再重新組裝成一份新的 BattleState 回傳——對外仍是純函式：toCtx 已把 party/enemies/items 等
// 陣列與內層物件都各自淺拷貝一份，所以本次呼叫怎麼改都不會動到傳入的 state 或它的巢狀物件。
import type { Item, Skill, TrayMode } from '../types';
import type { BattleConfig, BattleEvent, BattleState, EnemyActor, PartyActor, PendingCast, TargetingMode } from './types';

/**
 * BattleEvent 是判別聯集（discriminated union），但 TS 內建的 Omit<Union, K> 其實是
 * Pick<Union, Exclude<keyof Union, K>>——而 keyof 一個聯集只會拿到「每個成員都有的鍵」的交集
 * （這裡只剩 kind），會把 actorId/enemyId 這些各成員專屬欄位整個丟光。改成先用條件型別把聯集
 * distribute 開、每個成員各自 Omit 再合回聯集，才能保留每種事件各自的欄位形狀。
 */
type DistributiveOmit<T, K extends keyof T> = T extends unknown ? Omit<T, K> : never;

export interface Ctx {
  now: number;
  cfg: BattleConfig;
  rng: () => number;
  playerId: string;
  party: PartyActor[];
  enemies: EnemyActor[];
  targetId: string | null;
  trayMode: TrayMode;
  targeting: { mode: TargetingMode; skillId?: string; itemId?: string };
  skills: (Skill | null)[];
  skillReadyAt: Record<string, number>;
  items: { def: Item; quantity: number }[];
  escape: BattleState['escape'];
  events: BattleEvent[];
  log: string[];
  seq: number;
  pendingCasts: Record<string, PendingCast>;
  enemyTargets: Record<string, string>;
  resolvingSince: number | null;
  aiSkillReadyAt: Record<string, Record<string, number>>;
}

/** 把外部傳入的 state 拆成本次呼叫可安全改動的工作副本。 */
export function toCtx(state: BattleState, now: number): Ctx {
  return {
    now,
    cfg: state.config,
    rng: state.rng,
    playerId: state.playerId,
    party: state.party.map((p) => ({ ...p })),
    enemies: state.enemies.map((e) => ({ ...e })),
    targetId: state.targetId,
    trayMode: state.trayMode,
    targeting: { ...state.targeting },
    skills: state.skills,
    skillReadyAt: { ...state.skillReadyAt },
    items: state.items.map((i) => ({ ...i })),
    escape: { ...state.escape },
    events: [...state.events],
    log: [...state.log],
    seq: state.seq,
    pendingCasts: { ...state.pendingCasts },
    enemyTargets: { ...state.enemyTargets },
    resolvingSince: state.resolvingSince,
    aiSkillReadyAt: Object.fromEntries(Object.entries(state.aiSkillReadyAt).map(([id, m]) => [id, { ...m }])),
  };
}

/** 把工作副本組回一份新的 BattleState；phase/outcome 由呼叫端算好傳入（tick 可能結束戰鬥，dispatch 不會）。 */
export function fromCtx(ctx: Ctx, phase: BattleState['phase'], outcome: BattleState['outcome']): BattleState {
  return {
    phase,
    outcome,
    now: ctx.now,
    seq: ctx.seq,
    playerId: ctx.playerId,
    party: ctx.party,
    enemies: ctx.enemies,
    targetId: ctx.targetId,
    trayMode: ctx.trayMode,
    targeting: ctx.targeting,
    skills: ctx.skills,
    skillReadyAt: ctx.skillReadyAt,
    items: ctx.items,
    escape: ctx.escape,
    config: ctx.cfg,
    events: ctx.events,
    log: ctx.log,
    rng: ctx.rng,
    pendingCasts: ctx.pendingCasts,
    enemyTargets: ctx.enemyTargets,
    resolvingSince: ctx.resolvingSince,
    aiSkillReadyAt: ctx.aiSkillReadyAt,
  };
}

/** 推一筆事件：seq/at 在這裡自動補上，seq 只在真的有事件發生時才會前進（見 types.ts 對 BattleState.seq 的說明）。 */
export function pushEvent(ctx: Ctx, ev: DistributiveOmit<BattleEvent, 'seq' | 'at'>): void {
  ctx.seq += 1;
  ctx.events.push({ ...ev, seq: ctx.seq, at: ctx.now } as BattleEvent);
}

/** 人類可讀一行，供除錯/測試用；跟 pushEvent 分開呼叫，拒絕指令等沒有 BattleEvent 的情況也能留一行紀錄。 */
export function pushLog(ctx: Ctx, line: string): void {
  ctx.log.push(line);
}
