// DORPG 戰鬥引擎型別（P1，契約 §2）。純資料型別 + 一個預設設定常數，無 React/DOM 依賴。
// 與 src/lib/dorpg/types.ts（P0/P1 共用的「戰前資料」契約：PartyMember/Enemy/Skill/Item/BattleSample）
// 分屬不同層——這裡是「戰鬥中資料」（PartyActor/EnemyActor/BattleState），createBattle() 負責轉換。
//
// import type 的東西在 Node 的 TS type-stripping 下會整段被削掉、完全不會嘗試 resolve，
// 所以這裡引用 ../types（純型別檔）不影響 verify-dorpg-engine.mjs 用 node 直接執行本檔。
import type { ActorStats, EnemySlotId, Item, Skill, TrayMode, WeaponKind } from '../types';

export type ActorActionState = 'idle' | 'charging' | 'guarding' | 'casting' | 'recovering' | 'dead';
export type EnemyAnimState = 'spawning' | 'idle' | 'windup' | 'attacking' | 'hitReaction' | 'dying' | 'removed';
export type BattlePhase = 'loading' | 'active' | 'resolving' | 'ended';
export type BattleOutcome = 'victory' | 'defeat' | 'draw' | 'escaped';
export type TargetingMode = 'none' | 'chooseAlly' | 'chooseEnemy';
export type EscapeFlow = 'available' | 'judging' | 'failed' | 'unavailable';

export interface PartyActor {
  id: string;
  name: string;
  level: number;
  stats: ActorStats;
  hp: number;
  mp: number;
  shield: number;
  action: ActorActionState;
  actionUntil: number;
  attackReadyAt: number;
  chargeStartedAt: number | null;
  isPlayer: boolean;
  portraitUrl: string | null;
  slotIndex: number;
  /**
   * 契約 §2 逐字列出的 PartyActor 欄位沒有這個，但 'attack' 事件必須帶 weapon 才能決定特效/音效組
   * （契約 §1：PartyMember.weapon「隊友 AI 的武器」）。engine 得把它從 PartyMember 帶進戰鬥狀態才有
   * 地方查——這是功能上必要的新增欄位，不是隨意擴充契約。
   */
  weapon: WeaponKind;
}

export interface EnemyActor {
  id: string;
  name: string;
  level: number;
  slot: EnemySlotId;
  stats: ActorStats;
  hp: number;
  anim: EnemyAnimState;
  animUntil: number;
  nextActAt: number;
  threatPriority: number;
  imageUrl: string;
  rank?: string;
  attribute?: string;
  size?: string;
  race?: string;
  /**
   * 規格：「hitReaction 不中斷自己的 windup 計時」。做法：真正驅動 AI 的計時器仍是 anim/animUntil，
   * 受擊當下把「原本要恢復的狀態」先存進這兩格、顯示層暫時蓋成 hitReaction；enemyHitMs 過後照存的值
   * 復原（如果復原當下時間已經超過原本的到期點，下個 tick 立刻按正常規則繼續推進，
   * 等同該計時器從未被打斷過）。純內部簿記欄位，UI 不需要理會。
   */
  resumeAnim?: EnemyAnimState;
  resumeAnimUntil?: number;
}

export interface BattleConfig {
  attackCooldownMs: number;
  chargeMinMs: number;
  chargeFullMs: number;
  chargeMaxMultiplier: number;
  guardDamageMultiplier: number;
  recoveryMs: number;
  defaultCastMs: number;
  escapeJudgeMs: number;
  enemyActIntervalMs: [number, number];
  enemyWindupMs: number;
  enemyAttackMs: number;
  enemyHitMs: number;
  enemyDeathMs: number;
  allyActIntervalMs: [number, number];
  hitRate: number;
  critRate: number;
  critMultiplier: number;
  /**
   * 審查修復（見 §1）：勝利瞬間結案會把敵人死亡動畫蓋掉，改成先進 'resolving' 再等待。
   * 戰敗/平手沒有動畫可等，改用這個固定延遲（不是等敵人 anim，敵人也可能還活著）。
   */
  resolveDelayMs: number;
}

/** 契約 §2 給的預設值，逐字照抄；可被 createBattle 的 opts.config 局部覆寫。 */
export const DEFAULT_BATTLE_CONFIG: BattleConfig = {
  attackCooldownMs: 1500,
  chargeMinMs: 300,
  chargeFullMs: 1200,
  chargeMaxMultiplier: 2.5,
  guardDamageMultiplier: 0.4,
  recoveryMs: 400,
  defaultCastMs: 500,
  escapeJudgeMs: 1200,
  enemyActIntervalMs: [2500, 4500],
  enemyWindupMs: 400,
  enemyAttackMs: 800,
  enemyHitMs: 600,
  enemyDeathMs: 1580,
  allyActIntervalMs: [2200, 3600],
  hitRate: 1,
  critRate: 0,
  critMultiplier: 2,
  resolveDelayMs: 800,
};

/** 施法中尚未結算的技能，key=actorId；'ALL' 代表 allAllies（單一 targetId 欄位放不下「全體」語意）。 */
export interface PendingCast {
  skillId: string;
  targetId: string | 'ALL' | null;
}

export type Command =
  | { type: 'ATTACK_BEGIN' }
  | { type: 'ATTACK_RELEASE' }
  | { type: 'HOLD_CANCEL' }
  | { type: 'GUARD_BEGIN' }
  | { type: 'GUARD_END' }
  | { type: 'SELECT_TARGET'; enemyId: string }
  | { type: 'SET_TRAY'; mode: TrayMode }
  | { type: 'USE_SKILL'; skillId: string; targetId?: string }
  | { type: 'USE_ITEM'; itemId: string; targetId?: string }
  | { type: 'CANCEL_TARGETING' }
  | { type: 'TRY_ESCAPE' };

export type BattleEvent =
  | { seq: number; at: number; kind: 'chargeStart' | 'chargeCancel'; actorId: string }
  | {
      seq: number;
      at: number;
      kind: 'attack';
      actorId: string;
      targetId: string;
      weapon: WeaponKind;
      result: 'normal' | 'critical' | 'miss' | 'immune';
      damage: number;
      charged: boolean;
    }
  | { seq: number; at: number; kind: 'skillCast'; actorId: string; skillId: string; targetId: string | null }
  | { seq: number; at: number; kind: 'heal' | 'shield'; actorId: string; targetId: string; amount: number }
  | { seq: number; at: number; kind: 'enemyWindup'; enemyId: string; targetId: string }
  | {
      seq: number;
      at: number;
      kind: 'enemyAttack';
      enemyId: string;
      targetId: string;
      damage: number;
      guarded: boolean;
    }
  | { seq: number; at: number; kind: 'enemyDeath'; enemyId: string }
  | { seq: number; at: number; kind: 'actorDown' | 'actorRevive'; actorId: string }
  | { seq: number; at: number; kind: 'itemUsed'; itemId: string; targetId: string; amount: number }
  | { seq: number; at: number; kind: 'escapeJudging' | 'escapeFailed' | 'escaped' }
  | { seq: number; at: number; kind: 'targetChanged'; enemyId: string | null }
  | { seq: number; at: number; kind: 'ended'; outcome: BattleOutcome };

export interface BattleState {
  phase: BattlePhase;
  outcome: BattleOutcome | null;
  now: number;
  /** 已發生事件總數，同時是每個 BattleEvent 的流水號：只有 pushEvent 會讓它 +1（拒絕指令等
   *  沒有實際戰鬥效果的呼叫不會動它），所以永遠單調不遞減，且只在「真的發生了什麼」時前進。 */
  seq: number;
  playerId: string;
  party: PartyActor[];
  enemies: EnemyActor[];
  targetId: string | null;
  trayMode: TrayMode;
  targeting: { mode: TargetingMode; skillId?: string; itemId?: string };
  skills: (Skill | null)[];
  skillReadyAt: Record<string, number>;
  items: { def: Item; quantity: number }[];
  escape: { flow: EscapeFlow; judgingUntil: number | null; chance: number; message: string };
  config: BattleConfig;
  events: BattleEvent[];
  log: string[];
  /** dispatch/tick 都是 (state, ...) → state 的純函式，沒有另外的參數通道傳 rng，只能隨 state 一起帶著走。 */
  rng: () => number;
  /** 施法中尚未結算的技能（見 PendingCast）。 */
  pendingCasts: Record<string, PendingCast>;
  /** 敵人 windup 當下鎖定的目標，key=enemyId；attacking 結算完或死亡時清除。 */
  enemyTargets: Record<string, string>;
  /**
   * 審查修復 #1：進入 'resolving' 的時間點。victory 用來等敵人死亡動畫播完（不靠這個算時間，
   * 只是留紀錄）；defeat/draw 沒有動畫可等，用這個 + config.resolveDelayMs 算多久後才真的 ended。
   */
  resolvingSince: number | null;
  /**
   * 審查修復 #2：skillReadyAt 是「玩家」的技能冷卻（UI 契約既有欄位，語意不變）；AI 隊友改記在
   * 這裡（key=actorId，值是該隊友自己的 skillId→readyAt），避免 AI 用掉治療技能時把玩家 UI 上
   * 顯示的冷卻也一起打斷（兩者本來就是不同角色在用同一個技能定義，不該共用同一把鎖）。
   */
  aiSkillReadyAt: Record<string, Record<string, number>>;
}
