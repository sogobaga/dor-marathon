// DORPG 戰鬥引擎型別（P1，契約 §2）。純資料型別 + 一個預設設定常數，無 React/DOM 依賴。
// 與 src/lib/dorpg/types.ts（P0/P1 共用的「戰前資料」契約：PartyMember/Enemy/Skill/Item/BattleSample）
// 分屬不同層——這裡是「戰鬥中資料」（PartyActor/EnemyActor/BattleState），createBattle() 負責轉換。
//
// import type 的東西在 Node 的 TS type-stripping 下會整段被削掉、完全不會嘗試 resolve，
// 所以這裡引用 ../types（純型別檔）不影響 verify-dorpg-engine.mjs 用 node 直接執行本檔。
import type { ActorStats, BuffDebuffStat, CombatRating, EnemySlotId, Item, Skill, TrayMode, WeaponKind } from '../types';

export type ActorActionState = 'idle' | 'charging' | 'guarding' | 'casting' | 'recovering' | 'dead';
export type EnemyAnimState = 'spawning' | 'idle' | 'windup' | 'attacking' | 'hitReaction' | 'dying' | 'removed';
export type BattlePhase = 'loading' | 'active' | 'resolving' | 'ended';
export type BattleOutcome = 'victory' | 'defeat' | 'draw' | 'escaped';
export type TargetingMode = 'none' | 'chooseAlly' | 'chooseEnemy';
export type EscapeFlow = 'available' | 'judging' | 'failed' | 'unavailable';

/**
 * P5（CONTRACT §5）：套用中的 buff/debuff 狀態（戰鬥中資料，跟 PartyActor/EnemyActor 一樣只活在
 * BattleState 裡）。「同 stat 同來源刷新不疊加」的疊加規則（見 engine/effects.ts applyStatusEffect）
 * 靠 (stat, sourceSkillId) 這組複合鍵判斷是否已有同一筆——不同技能即使打同一個 stat 也視為各自獨立、
 * 可以共存疊加（value 加總，見 activeStatSum）。
 * hp_regen_pct 專用：nextTickAt 記錄下一次觸發回復的時間點（每 1000ms 一次，見
 * engine/effects.ts pruneAndRegenEffects）；其餘 stat 不使用這個欄位。
 */
export interface ActiveEffect {
  stat: BuffDebuffStat;
  value: number;
  expiresAt: number;
  sourceSkillId: string;
  kind: 'buff' | 'debuff';
  nextTickAt?: number;
}

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
  /**
   * P2：命中/暴擊評級。跟 stats 一樣是「戰鬥中資料」，createBattle 一定會填好一份（PartyMember.rating
   * 有給就直接用，沒給就呼叫 deriveDefaultPartyRating 推導後備值）——engine 內部運算（missChance/
   * critChance）因此不用處理「rating 不存在」的分支，永遠當作已定義的具體數值使用。
   */
  rating: CombatRating;
  /** P5：目前套用中的 buff（buff 詞彙表只打隊伍側，見 BuffDebuffStat 型別註解）；createBattle 一律
   *  初始化成 []，不會是 undefined。 */
  activeEffects: ActiveEffect[];
  /**
   * P5（CONTRACT §1）：目前職業 id，純透傳供 FRONTEND 顯示用（見 PartyMember.jobId 型別註解）；
   * engine 的任何戰鬥數值計算都不讀這個欄位。
   */
  jobId: string | null;
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
  /** P2：命中/暴擊評級。Enemy.rating 有給就直接用，沒給就呼叫 deriveDefaultMonsterRating 推導。 */
  rating: CombatRating;
  /** P5：目前套用中的 debuff（debuff 詞彙表只打敵方側，見 BuffDebuffStat 型別註解）；createBattle
   *  一律初始化成 []，不會是 undefined。 */
  activeEffects: ActiveEffect[];
  /** P5（CONTRACT §6）：弱點屬性桶，缺省 []；見 Enemy.weakElements 型別註解與 formulas.ts
   *  elementMultiplier() 的「chart 覆寫優先，否則 weakElements 命中」規則。 */
  weakElements: string[];
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
  /**
   * P2 暴擊／Miss／無效攻擊上線後，這個欄位的角色改變了：不再是「isHit 的直接機率」，而是
   * 只在攻擊方完全沒有 CombatRating 資料時，充當 deriveDefaultPartyRating 推導後備評級的
   * hit 來源（× 100 換算成百分比制）——一旦呼叫端（PartyMember.rating）真的帶了評級，這個
   * 欄位就不會再被用到，命中判定改吃 formulas.ts 的 missChance()。保留預設值 1（＝後備 hit=100，
   * 跟沒有這個機制以前的「恆定命中」等價），維持舊資料相容。
   */
  hitRate: number;
  /**
   * P2 起語意改變：不再是「isCrit 的直接機率」，而是「全體基礎暴擊率」——攻守雙方（含 LUK=0
   * 的怪物）在 critChance() 裡都會加上這個基準值，讓玩家一開始配一點 LUK 就看得到暴擊發生。
   * 預設由 0 改成 0.08（＝8%），實際數值交給 BALANCE 之後調整（見 SPEC §6）。
   */
  critRate: number;
  /** P5：不再直接用於傷害結算（見 effects.ts rollCritMultiplier 改吃 critMultMin/critMultMax
   *  的浮動區間）；保留欄位＋預設值只為向下相容舊資料／WIRE.md「既有 critMultiplier 可保留但
   *  前端不再使用」的說明，engine 內部（combat.ts／ai.ts）已經沒有任何地方讀它。 */
  critMultiplier: number;
  /** P5（CONTRACT §6）：暴擊倍率浮動區間下限；每次暴擊在 [critMultMin, critMultMax] 均勻抽樣
   *  （見 effects.ts rollCritMultiplier），取代舊的固定 critMultiplier。 */
  critMultMin: number;
  /** 暴擊倍率浮動區間上限，見 critMultMin 型別註解。 */
  critMultMax: number;
  /** P5（CONTRACT §6）：技能 element 命中怪物 weakElements 時的傷害加成百分比（預設 25＝+25%）；
   *  見 formulas.ts elementMultiplier()。 */
  weaknessBonusPct: number;
  /**
   * 審查修復（見 §1）：勝利瞬間結案會把敵人死亡動畫蓋掉，改成先進 'resolving' 再等待。
   * 戰敗/平手沒有動畫可等，改用這個固定延遲（不是等敵人 anim，敵人也可能還活著）。
   */
  resolveDelayMs: number;

  // ---- P2（暴擊／Miss／無效攻擊）新增：全部走 rpg_config JSON，不新增 migration。 ----

  /** missChance() 的基礎落空率（%）。 */
  baseMissPct: number;
  /** missChance() 每 1 點 (defender.flee − attacker.hit) 差額，換算成多少 % 落空率。 */
  hitFleeScale: number;
  /** missChance() clamp 下限（%）——即使命中遠高於迴避，也保留一點基本落空率讓機制看得見。 */
  missMinPct: number;
  /** missChance() clamp 上限（%）——即使迴避遠高於命中，也不能高到玩家覺得打不到東西。 */
  missMaxPct: number;
  /**
   * 怪物沒有個別 rating 覆寫時，deriveDefaultMonsterRating 用的命中基準。
   * ⚠️ P3（審查 dorpg_p3 r5 缺陷1/2 修復）語意分裂記錄：後端 internal/rpg MonsterRating() 已把
   * 這個係數從「絕對基準值」改成「相對玩家等級基線的偏移」（monsterHit = playerBaseLevel ×
   * monsterHitPerLevel + monsterHitBase，讓怪物命中跟著玩家等級基線走，AGI/DEX 配點才會一直
   * 有意義），預設值同步 100→2。engine 這一層的 deriveDefaultMonsterRating 沒有 playerBaseLevel
   * 可用（toEnemyActor 沒有拿到玩家等級），維持舊語意「跟等級無關的絕對基準」原樣不變——這是
   * engine 在完全沒有真實 rating 資料時的最後備援路徑（正常戰鬥一律由後端算好帶入 Enemy.rating，
   * 這支函式不會被呼叫到），只是把預設數字同步成 2，不讓這個很少用到的路徑跟後端新語意的數字
   * （2）對不上，造成閱讀時的誤導。
   */
  monsterHitBase: number;
  /** 怪物沒有個別 rating 覆寫時，deriveDefaultMonsterRating 用的迴避基準；語意分裂與預設值變動理由同上（monsterHitBase）。 */
  monsterFleeBase: number;
  /** 怪物沒有個別 rating 覆寫時，deriveDefaultMonsterRating 用的暴擊率基準。 */
  monsterCritPct: number;
  /** 怪物沒有個別 rating 覆寫時，deriveDefaultMonsterRating 用的暴擊迴避基準。 */
  monsterCritShieldBase: number;
  /**
   * 屬性相剋表：key＝怪物 attribute（中文，資料庫現況存的就是中文字串，例如「金」「木」），
   * value 是「技能/攻擊 element（英文，ElementKind）→ 倍率」的表。
   * P5（CONTRACT §0/§6）語意改變：這張表現在只是「管理者覆寫」——查有 [attribute][element] 這組
   * key 才用（可以是 0.0＝完全無效，elementMultiplier() 用來判 'immune'）；查無 key 一律落到
   * Enemy.weakElements 規則（命中弱點桶 → 1+weaknessBonusPct/100，否則 1.0），不再是「查無就當
   * 1.0」的單純預設表。DEFAULT_BATTLE_CONFIG 的預設值也跟著清空成 {}（見下方常數），弱點改由
   * 個別怪物的 weakElements 資料表達，不再靠這張全域表模擬。
   */
  elementChart: Record<string, Record<string, number>>;

  // ---- P3（AGI 攻速／DEX 詠唱縮減，審查 dorpg_p3 r5）新增：全部走 rpg_config JSON，不新增 migration。 ----

  /**
   * 後端 internal/rpg MonsterRating() 的命中基準隨玩家 Base Lv 增加的係數（monsterHit公式見
   * monsterHitBase 註解）。engine 這一層沒有 playerBaseLevel 可用，不會實際拿這個欄位做運算
   * ——留在這裡純粹是讓 BattleConfig 的形狀跟後端 Config／後台設定頁完整鏡射，供型別檢查與
   * opts.config 局部覆寫時使用（呼叫端可以放心传入這個欄位不會被型別擋下）。
   */
  monsterHitPerLevel: number;
  /** 怪物命中的絕對上限：必須低於 rpg_config 的 flee_cap_pct，否則高等級玩家的 AGI 迴避永遠卡在下限。 */
  monsterHitMax: number;
  /** 同上，迴避基準隨玩家 Base Lv 增加的係數；engine 同樣不直接使用，理由同 monsterHitPerLevel。 */
  monsterFleePerLevel: number;
  /**
   * AGI/DEX 攻速→冷卻／詠唱縮減公式的參考基準值（對齊 internal/rpg Config.AspdBase，預設 150）：
   * 攻擊者的 rating.aspd 剛好等於這個值時，attackCooldownFor() 算出來的冷卻恰好等於
   * attackCooldownMs（base）——這樣「完全不配 AGI/DEX 的角色」維持上一輪就有的 1500ms 手感，
   * 不會因為這次上線 P3 而變動，只有真的配了 AGI/DEX 的角色才會感覺到差異。
   */
  aspdReference: number;
  /** attackCooldownFor() 的攻擊冷卻下限（clamp 下界）：避免攻速堆到極端值時攻擊間隔快到失控。 */
  attackCooldownMinMs: number;
  /** effectiveCastMs() 的施法時間下限（clamp 下界）：避免詠唱縮減堆到 100% 時變成瞬間出招，破壞技能本來該有的節奏感。 */
  castMinMs: number;
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
  critRate: 0.08,
  critMultiplier: 2,
  // P5（CONTRACT §0/§6 使用者拍板）：暴擊倍率固定 2.0 改成 [1.75, 2.25] 均勻抽樣，「要 RO 的爽感：
  // 暴擊＋高速連擊」——固定倍率每次都一樣沒有驚喜，浮動區間讓暴擊本身也有大小之分。中點 (1.75+2.25)/2
  // 剛好等於舊的固定值 2，數值感覺維持在同一個量級，只是加了隨機性，不是整體加強或減弱。
  critMultMin: 1.75,
  critMultMax: 2.25,
  // CONTRACT §6：技能 element 命中怪物 weakElements → +25% 傷害，拍板值。
  weaknessBonusPct: 25,
  resolveDelayMs: 800,

  // baseMissPct 由 SPEC §6 預設值 8 改為 0（TUNE 套用 BALANCE 建議，見
  // scratchpad/dorpg_p2/REBALANCE_CRIT.md §3）：8% 固定基礎值對低等級玩家（hit 小、未被
  // missMinPct 下限保護）殺傷力遠大於高等級玩家（早被下限吃掉），會讓 Lv5 勝率腰斬；調到 0 後
  // Lv5 回到既有基準、Lv27/Lv50 不受影響。三處（此檔／fixture.ts 經由此檔匯入／後端
  // config.go DefaultConfig）必須保持一致。
  baseMissPct: 0,
  hitFleeScale: 0.35,
  missMinPct: 2,
  missMaxPct: 35,
  // 100→2、8→2：語意從「絕對基準值」改成後端「相對玩家等級基線的偏移」（見型別註解），
  // engine 這一層的後備路徑維持絕對值語意，但數字同步改小，不讓兩邊的 2 對不上。
  monsterHitBase: 2,
  monsterFleeBase: 2,
  monsterCritPct: 0,
  monsterCritShieldBase: 0,
  // P5（CONTRACT §0/§6 使用者拍板）：「既有 battle_element_chart 保留為管理者覆寫...預設表清空」
  // ——P2 那組示範表（金/木/土/闇/無）已被 Enemy.weakElements（見 fixture.ts RPG_MONSTERS 的
  // weakElements 欄位）取代，DEFAULT 不再預先塞任何 attribute，只有後台真的手動覆寫時才會非空。
  elementChart: {},

  // P3（AGI 攻速／DEX 詠唱縮減）新增，對齊 internal/rpg Config 的 DefaultConfig()：
  monsterHitPerLevel: 1.0,
  monsterHitMax: 75,
  monsterFleePerLevel: 1.0,
  aspdReference: 150, // 對齊 internal/rpg AspdBase 預設值
  attackCooldownMinMs: 700,
  castMinMs: 120,
};

/** 施法中尚未結算的技能，key=actorId；'ALL' 代表 allAllies、'ALL_ENEMIES' 代表 allEnemies
 *  （P5 新增；單一 targetId 欄位放不下「全體」語意，兩個方向各自一個 sentinel）。 */
export interface PendingCast {
  skillId: string;
  targetId: string | 'ALL' | 'ALL_ENEMIES' | null;
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
      /** P2：怪物打隊友現在也套 missChance/critChance（SPEC §5），沒有 'immune'——怪物普攻
       *  element 恆 neutral，沒有屬性相剋表可以讓它變無效，只有 miss/一般/暴擊三態。 */
      result: 'normal' | 'critical' | 'miss';
    }
  | { seq: number; at: number; kind: 'enemyDeath'; enemyId: string }
  | { seq: number; at: number; kind: 'actorDown' | 'actorRevive'; actorId: string }
  | { seq: number; at: number; kind: 'itemUsed'; itemId: string; targetId: string; amount: number }
  | { seq: number; at: number; kind: 'escapeJudging' | 'escapeFailed' | 'escaped' }
  | { seq: number; at: number; kind: 'targetChanged'; enemyId: string | null }
  | { seq: number; at: number; kind: 'ended'; outcome: BattleOutcome }
  // ---- P5（CONTRACT §5）新增：special 拒絕、buff/debuff 套用與到期。 ----
  /** special（implemented=false）技能被 USE_SKILL 指到時發出（見 dispatch.ts），跟一般拒絕的差異是
   *  這個有專屬事件可以讓 FRONTEND 顯示「尚未實裝」提示，不是只有 log 那一行。 */
  | { seq: number; at: number; kind: 'skillUnavailable'; actorId: string; skillId: string }
  /** buff/debuff 技能命中目標、真的套用了一筆 ActiveEffect 時發出（見 engine/combat.ts
   *  resolveBuffDebuff）。effectKind 跟外層 kind:'statusApplied' 是兩件事——外層 kind 是「這是什麼
   *  事件」，effectKind 是「套用的是 buff 還是 debuff」，取不同名字避免混淆判別聯集的 kind 欄位。 */
  | {
      seq: number;
      at: number;
      kind: 'statusApplied';
      actorId: string;
      targetId: string;
      effectKind: 'buff' | 'debuff';
      stat: BuffDebuffStat;
      value: number;
      durationMs: number;
    }
  /** 到期自動移除時發出（見 effects.ts pruneAndRegenEffects），供 FRONTEND 收掉狀態圖示用；
   *  非必要（引擎內部一定會清除，這個事件只是給 UI 知道「什麼時候清除的」）。 */
  | { seq: number; at: number; kind: 'statusExpired'; targetId: string; stat: BuffDebuffStat };

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
