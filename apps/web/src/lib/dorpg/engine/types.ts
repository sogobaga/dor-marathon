// DORPG 戰鬥引擎型別（P1，契約 §2）。純資料型別 + 一個預設設定常數，無 React/DOM 依賴。
// 與 src/lib/dorpg/types.ts（P0/P1 共用的「戰前資料」契約：PartyMember/Enemy/Skill/Item/BattleSample）
// 分屬不同層——這裡是「戰鬥中資料」（PartyActor/EnemyActor/BattleState），createBattle() 負責轉換。
//
// import type 的東西在 Node 的 TS type-stripping 下會整段被削掉、完全不會嘗試 resolve，
// 所以這裡引用 ../types（純型別檔）不影響 verify-dorpg-engine.mjs 用 node 直接執行本檔。
import type { ActorStats, BuffDebuffStat, CombatRating, EnemySlotId, EquipmentEffectsWire, Item, Skill, SummonWave, TrayMode, WeaponKind, WeaponProfileWire } from '../types';

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
  /**
   * P6（CONTRACT §3.2）：這位隊友（非玩家）AI 可用的技能，從 PartyMember.skills 原樣帶入戰鬥狀態
   * ——engine/ai.ts 的 advanceAllyAI 依五段優先序從這裡挑技能施放，取代 P1 時期借用
   * BattleState.skills（玩家技能欄）的簡化寫法。玩家（isPlayer=true）這個陣列恆為 []（玩家自己
   * 的技能走 USE_SKILL 指令與 BattleState.skills，不經過這裡）。createBattle 一律填好陣列
   * （PartyMember.skills 有給就用，沒給就 []），不會是 undefined。
   */
  skills: Skill[];
  /** P6：純顯示用，見 PartyMember.presetName 型別註解；engine 的任何戰鬥數值計算都不讀這個欄位。 */
  presetName: string | null;
  /**
   * P7（CONTRACT §2/§3）：目前裝備武器的戰鬥效果；null＝沒有裝備（傭兵本輪恆為 null，見
   * PartyMember.equippedWeapon 型別註解）。engine/combat.ts 的攻擊/技能結算一律用
   * `actor.weaponProfile ?? NEUTRAL_WEAPON_PROFILE`（見 formulas.ts）取得可安全套用的具體數值，
   * 不必在每個呼叫點各自處理 null 分支。
   */
  weaponProfile: WeaponProfileWire | null;
  /**
   * P8（CONTRACT §2／WIRE「戰鬥 bootstrap」）：目前裝備（防具＋飾品）的戰鬥效果彙總；恆有值
   * （createBattle 用 `pm.equipmentEffects ?? NEUTRAL_EQUIPMENT_EFFECTS` 保底，見 engine/index.ts
   * toPartyActor）——跟 weaponProfile 刻意可為 null（表達「有沒有裝備」這件事本身）不同，這裡
   * 沒有這種區分的必要：契約明講「傭兵一律零值物件」，永遠是一份完整、只是數值可能全零的物件，
   * 呼叫端（combat.ts／dispatch.ts／tick.ts）因此不需要處理「equipmentEffects 不存在」的分支，
   * 永遠讀 actor.equipmentEffects.xxx 就是安全的。
   */
  equipmentEffects: EquipmentEffectsWire;
  /**
   * P8（WIRE「引擎」：「每 5000ms 依戰鬥時鐘...」）：這位隊員下一次裝備定時回復（hpRegenPctPer5s／
   * mpRegenPctPer5s）觸發的時間點，createBattle 初始化為 `now + 5000`（見 toPartyActor）。跟
   * hp_regen_pct buff 的 nextTickAt（1000ms 週期、記在個別 ActiveEffect 上）是完全獨立的兩套排程
   * ——不同來源（裝備 vs. 技能施放的暫時性效果）、不同週期（5000ms vs. 1000ms），混用同一個時間戳
   * 會讓「這個 next 到底是哪一種回復的排程」變得曖昧，故各自獨立一個欄位/機制。tick.ts 的
   * applyEquipmentRegen() 用 while 迴圈追趕（同 pruneAndRegenEffects 的既有手法），死亡（hp≤0）
   * 期間整個跳過、不推進這個時間點（跟 hp_regen_pct 對死亡角色的既有處理一致）。
   */
  nextEquipRegenAt: number;
  /**
   * P9（CONTRACT §1／WIRE「引擎」）：這位角色目前套用的 AI 策略 id，原樣從 PartyMember.strategyId
   * 帶入（缺省 'balanced'，見 engine/index.ts toPartyActor）——刻意不在建場當下就正規化成白名單
   * 字面值，因為 DB 覆寫（ctx.cfg.aiStrategies）跟「這個 id 存不存在」的判斷都收斂在
   * `resolveStrategy(actor.strategyId, ctx.cfg.aiStrategies)` 這一個地方（ai.ts／autopilot.ts
   * 每次要決策時才呼叫），未知 id 一律退回 balanced；PartyActor 這裡只是原始值的容器。玩家與
   * 隊友都有這個欄位——玩家自己的策略同時也是自動戰鬥（autopilot.ts）decideAction 的依據，
   * 隊友（advanceAllyAI）亦然，兩者共用同一份 registry。
   */
  strategyId: string;
  /**
   * P10（DORPG_P10 CONTRACT §2/§3、WIRE「引擎」）：這位角色目前的挑釁/守護到期時間（戰鬥時鐘
   * 毫秒，0＝無/已過期）——只由 engine/combat.ts resolveTaunt 寫入（`tauntUntil = max(現值,
   * now+durationMs)`，重複施放取較晚的到期時間，不會被更短的新效果縮短）；createBattle 一律
   * 初始化 0（開場沒有人在挑釁）。formulas.ts inGuardianState() 用 `tauntUntil > now` 判斷這個
   * 來源的守護狀態是否仍然有效——過期後不需要另外清成 0，比較式本身已經失效。
   */
  tauntUntil: number;
  /**
   * P10（DORPG_P10 CONTRACT §2/§3、WIRE「引擎」）：這位角色是否學過「守護本能」被動（heavy_knight
   * 專屬 hk_c2，≥1 級）——只有這樣的角色在 `action==='guarding'` 期間才算進入守護狀態（見
   * formulas.ts inGuardianState）。從 PartyMember.guardTaunt 原樣帶入（createBattle 缺省
   * false，見 index.ts toPartyActor）。AI 隊友目前不能按防禦（CONTRACT §1「隊友（AI）目前不能
   * 按防禦」），這個欄位對隊友完全是 no-op——傭兵想靠這條路線吸引仇恨要用挑釁／守護姿態（taunt
   * 技能寫入的 tauntUntil），不是這個被動。
   */
  guardTaunt: boolean;
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
  /**
   * P12（CONTRACT §1「排位＝既有槽位」、WIRE「enemy 新增 slot、row」）：這隻怪目前是前排還是
   * 後排，只影響武器排位加成（formulas.ts rowBonusMultiplier）與槍系貫穿的候選後排位置
   * （combat.ts resolveWeaponAttack）。可選欄位——ENGINE 不擁有 createBattle（engine/index.ts）
   * 的寫入權，無法保證這裡一定會被填值；`slot` 才是必填、唯一保證存在的權威資料，row 永遠可以
   * 從它推導（formulas.ts rowOfSlot），這裡開一個欄位純粹是讓「上游（BACKEND／INTEGRATOR）已經
   * 算好、直接送 row」的情況可以省一次推導，兩者算出來的值必須一致（都是 slot 的單純函式）。
   * 所有讀取端一律用 `enemy.row ?? rowOfSlot(enemy.slot)` 取得有效排位，不會因為這個欄位缺席
   * 而遺漏排位加成/貫穿機制。
   */
  row?: EnemyRow;
  /**
   * P11（DORPG_P11 CONTRACT §1「怪物強度九級」／WIRE「enemy 新增 rank、rankLabel、badgeColor、
   * isSummoned」）：人類可讀的強度標籤與徽章顏色，純顯示用途（見 dorpg/types.ts Enemy.rankLabel／
   * badgeColor 型別註解——engine 的戰鬥數值計算完全不讀這兩個欄位，逐字原樣透傳）。
   */
  rankLabel?: string;
  badgeColor?: string;
  /**
   * P11（CONTRACT §1「召喚（A 以上）」、WIRE「引擎」）：這隻怪是不是本場戰鬥中途被召喚出來的
   * ——純顯示用途，engine 的戰鬥規則（鎖定/攻擊/勝負判定）完全不因為這個欄位而有任何分支。
   * createBattle 建立的初始敵人一律 false（見 formulas.ts toEnemyActor）；engine/summon.ts 的
   * advanceSummons() 對自己放進場的敵人一律強制設成 true，是這個欄位唯一的權威寫入來源。
   */
  isSummoned?: boolean;
}

/** P12：怪物排位——前排（front_left/center/right）／後排（rear_left/right），見 EnemyActor.row
 *  型別註解與 formulas.ts rowOfSlot()。 */
export type EnemyRow = 'front' | 'rear';

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

  /**
   * P6（CONTRACT §2／WIRE「戰鬥 bootstrap」）：目前這場戰鬥的怪物數值算法，只給 FRONTEND 顯示／
   * 除錯用（例如角落標一行「等級制」）——engine 的任何戰鬥數值計算完全不讀這個欄位，怪物的
   * hp/atk/def/mdef/rating 一律由 createBattle 收到的 Enemy.stats/rating 決定（不管那組數字是
   * BACKEND 用哪一套公式算出來的）。'level'＝新的怪物等級制（CONTRACT §2，本輪起的正式預設）；
   * 'power' ＝舊的玩家戰力縮放（P2 D2 公式，保留供對照／回退）；'fixed' 為契約保留的第三種模式，
   * 本輪未實作，engine 一樣只是原樣透傳顯示。
   */
  scaleMode: 'level' | 'power' | 'fixed';

  // ---- P7（DORPG_P7 CONTRACT §1、WIRE「戰鬥 bootstrap」）新增：五行＋光暗相剋倍率的三個可調
  // 係數，全部走 rpg_config JSON，不新增 migration。查無 battle_element_chart 管理者覆寫時，
  // formulas.ts elementMultiplier() 的五行相剋表退回這三個係數換算倍率（見該函式型別註解）。----

  /** 攻擊屬性「剋」怪物屬性（五行相剋方向或光闇互剋）時的倍率加成 %；預設 25（×1.25）。 */
  elementAdvantagePct: number;
  /** 攻擊屬性「被」怪物屬性剋制時的倍率減損 %；預設 −25（×0.75）。 */
  elementDisadvantagePct: number;
  /** 攻擊屬性與怪物屬性相同（同五行或同光/同闇）時的倍率減損 %；預設 −25（×0.75，同屬性最沒有
   *  效率，跟兩者相剋時的懲罰同一個量級——拍板值，理由見 CONTRACT §1）。 */
  elementSamePct: number;

  /**
   * P9（CONTRACT §1／WIRE「戰鬥 bootstrap」：「config 新增 aiStrategies」）：AI 策略 registry
   * 的資料庫覆寫——key＝策略 id，value.params 淺合併蓋過 engine/strategies.ts 的 defaultParams
   * （見 resolveStrategy）。只含 is_active 的 id（後端負責篩選，engine 這一層對「查無某個 id」
   * 與「查有但 params 是空物件」一視同仁，兩者都等同「完全採用 registry 預設值」）。預設 {}
   * （沒有任何覆寫，等同 P9 上線前的行為——所有策略都用 registry 寫死的預設參數）。
   */
  aiStrategies: Record<string, { params?: Record<string, unknown> }>;
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

  // P6：純顯示欄位，預設跟正式環境的新預設（CONTRACT §2：「battle_scale_mode 預設改 level」）一致；
  // engine 不讀這個值做任何運算，改哪個字串都不影響既有測試（見型別註解）。
  scaleMode: 'level',

  // P7（CONTRACT §1 拍板值）：五行＋光暗相剋倍率的三個係數預設 25/−25/−25。
  elementAdvantagePct: 25,
  elementDisadvantagePct: -25,
  elementSamePct: -25,

  // P9：預設沒有任何 DB 覆寫——STRATEGY_IDS 全部採用 engine/strategies.ts registry 寫死的
  // defaultParams（見該檔）。
  aiStrategies: {},
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
  | { type: 'TRY_ESCAPE' }
  /**
   * P9（CONTRACT §1／WIRE「引擎」）：切換玩家自動戰鬥／策略，本地立即生效（不驗證/不打 API）
   * ——持久化是前端另外呼叫 `PUT /rpg/auto-battle` 的事，跟這裡的本地即時狀態是兩件事（本地
   * 這份純粹是給 BattleState 用，讓 tick() 知道要不要跑 autopilot.ts）。strategyId 未知時
   * dispatch.ts 用 resolveStrategy() 正規化成 balanced，不會把非法字串存進 PartyActor。
   */
  | { type: 'SET_AUTO_BATTLE'; enabled: boolean; strategyId: string };

/**
 * P9（CONTRACT §4／WIRE「引擎」）：`decideAction(ctx, actor, strategy) → Decision`——AI 策略與
 * 玩家自動戰鬥共用的「決定要做什麼」純函式回傳值，本身不含任何執行邏輯（見 ai.ts decideAction
 * 型別註解）。
 * kind 對齊 WIRE 逐字定義：'guard'／'item' 是為了讓這個型別完整涵蓋「玩家自動戰鬥可能做的所有
 * 事」（型別簽章需要跟 WIRE 一致，供 FRONTEND 對照），但目前的 decideAction 實作永遠不會回傳
 * 這兩種——防禦（windup 鎖定＋auto_guard）與吃藥（HP%<30）是 autopilot.ts 在呼叫 decideAction
 * 之前就先攔下的獨立判斷（見該檔），理由：這兩件事只對玩家有意義（隊友沒有防禦/藥水機制），
 * 混進 decideAction 本體會需要額外的 `actor.isPlayer` 分支，徒增「balanced 對隊友的既有 375
 * 條斷言會不會被新分支影響」的稽核面——拆開後 decideAction 對隊友/玩家永遠是同一套五段式邏輯，
 * 差別只在讀哪一份技能欄/冷卻表（見 ai.ts actorSkills／skillReadyAt 輔助函式）。
 * targetId 允許 'ALL'／'ALL_ENEMIES' 兩個 sentinel（跟 PendingCast.targetId 同一組字面值），
 * 對應 allAllies／allEnemies 目標技能——執行端（ai.ts castNow／autopilot.ts 轉換成 Command）
 * 直接照樣傳遞，不需要另外轉譯。
 */
export interface Decision {
  /**
   * P10（DORPG_P10 CONTRACT §3、WIRE「引擎」）新增 'taunt'：施放 kind='taunt' 的守護/挑釁技能
   * （見 ai.ts decideGuardStance／decideProtectGuardStance）——目標恆為施放者自己（self），跟
   * 'buff' 分開一個字面值只是讓「這是守護判斷選出來的動作」在型別上一眼可辨，執行端
   * （ai.ts applyAllyDecision／autopilot.ts applyDecisionForPlayer）把它跟 heal/buff/damage/
   * debuff 併在同一段「查表→castNow」執行邏輯，不需要額外分支。
   */
  kind: 'heal' | 'buff' | 'damage' | 'debuff' | 'attack' | 'guard' | 'item' | 'taunt' | 'wait';
  skillId?: string;
  targetId?: string | 'ALL' | 'ALL_ENEMIES';
}

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
      /** P7（CONTRACT §3 斧／WIRE「事件帶 splash:true 供浮字區分」）：這筆傷害是濺射到相鄰怪物
       *  的次要命中，不是玩家/隊友點的主要目標——FRONTEND 可用它決定浮字樣式跟主擊有所區別。
       *  缺省 undefined（既有事件一律視為非濺射，跟明確給 false 語意相同，只是不強迫每個既有
       *  呼叫點都補這個欄位）。 */
      splash?: boolean;
      /**
       * P12（CONTRACT §1 槍／WIRE「事件 attack 加 pierce:true」，比照 splash 的做法）：這筆傷害
       * 是普攻貫穿到對應後排位置的波及命中，不是主要目標——FRONTEND 據此顯示「貫穿」浮字（與
       * 濺射同樣式、不同字）。splash/pierce 兩個旗標互斥（同一筆事件不會同時是濺射又是貫穿——
       * 濺射的主目標一定是主擊本身、貫穿的目標一定是後排，見 combat.ts applySplashDamage／
       * tryPierce 各自獨立 pushEvent，不會共用同一筆）。缺省 undefined＝非貫穿。
       */
      pierce?: boolean;
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
  | { seq: number; at: number; kind: 'statusExpired'; targetId: string; stat: BuffDebuffStat }
  // ---- P10（DORPG_P10 CONTRACT §3、WIRE「戰鬥 bootstrap」）新增：挑釁強制拉怪。 ----
  /**
   * retarget=true 的 taunt 技能（挑釁 hk_c1）施放當下發出，僅在 retarget 為 true 時推（見
   * engine/combat.ts resolveTaunt）——retarget=false 的守護姿態（hk_c3）不會產生這個事件，
   * 它只透過 tauntUntil 影響「之後」的選目標，沒有「立即拉怪」這個視覺事件可言。enemyIds＝
   * 這一下被強制改鎖 actorId 的存活敵人清單（含原本正在 windup 蓄力鎖定別人的敵人），供
   * FRONTEND 顯示浮字「挑釁！」與敵人被拉扯的視覺（若有）。
   */
  | { seq: number; at: number; kind: 'taunt'; actorId: string; enemyIds: string[] }
  // ---- P11（DORPG_P11 CONTRACT §1「召喚（A 以上）」、WIRE「引擎」）新增：召喚新敵人進場。 ----
  /**
   * engine/summon.ts 的 advanceSummons() 在召喚者 HP% 跨越門檻、成功放入至少一隻新敵人時發出
   * （一隻都放不下——契約「無空位略過」——就不推這個事件，見該檔）。enemyIds＝這一波實際放進場
   * 的敵人 id（可能少於 wave.enemies 原本的數量：空位不夠時只放得下的部分會被放入，見該檔型別
   * 註解對「部分放入」的決策說明），供 FRONTEND 顯示浮字「召喚！」與新敵人進場的視覺。
   */
  | { seq: number; at: number; kind: 'summon'; summonerId: string; enemyIds: string[] };

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
  /**
   * P9（CONTRACT §1／WIRE「引擎」）：玩家自動戰鬥開關——tick() 只在這裡是 true 且玩家可行動時
   * 才呼叫 autopilot.ts 的 advanceAutoBattle（見 tick.ts）；SET_AUTO_BATTLE 指令本地即時切換
   * （見 dispatch.ts），持久化交給前端另外呼叫 REST。createBattle 預設 false（見 index.ts）。
   */
  autoBattle: boolean;
  /**
   * P9（CONTRACT §4「集中火力」）：focus_fire 策略全隊共用的目標——每個 tick 開頭由
   * ai.ts 的 refreshFocusTarget 更新（「目標死亡才換」，見該函式型別註解），採其它策略的角色
   * 完全不讀寫這個欄位。createBattle 初始化 null（尚未挑過目標，第一個 tick 才會補上）。
   */
  focusTargetId: string | null;
  /**
   * P11（DORPG_P11 CONTRACT §1／WIRE「戰鬥 bootstrap」）：本場戰鬥的怪物數值算法標籤，純顯示／
   * 除錯用——跟 P6 的 config.scaleMode 是同一種「engine 完全不讀，只是帶著走給 FRONTEND 顯示」
   * 精神（見該欄位型別註解）：怪物實際的 hp/atk/def/mdef 一律由 BACKEND／fixture.ts 已經算好
   * 送進 Enemy.stats 的具體數字決定，不是這個標籤本身。createBattle 缺省 'legacy'（既有六場
   * 劇情場景、未上線本輪功能時的行為）。
   */
  scalingMode: 'legacy' | 'rank';
  /** 同上，純顯示——'player' 時 monsterLevel 才有意義（怪物等級跟隨玩家有效等級的強度挑戰對戰
   *  列表）。createBattle 缺省 'fixed'。 */
  levelMode: 'fixed' | 'player';
  /** level_mode='player' 時，bootstrap 已經算好、實際套用的怪物等級 N；'fixed' 模式或缺省時為
   *  null（沒有「跟隨」這件事可言，各怪物自己的 EnemyActor.level 才是權威）。 */
  monsterLevel: number | null;
  /**
   * P11（CONTRACT §1「召喚（A 以上）」、WIRE「引擎」）：bootstrap 已經算好的召喚波次表（見
   * dorpg/types.ts SummonWave 型別註解）；engine/summon.ts 的 advanceSummons(ctx) 在召喚者 HP%
   * 跨越 atHpPct 門檻時把對應波次的敵人放進場。createBattle 缺省 []（無召喚機制的既有場景，這
   * 個機制對它們完全是 no-op）。這份資料本身在整場戰鬥中永遠不會被修改（只有讀取／查詢有沒有
   * 觸發過），不像 party/enemies 那樣是「戰鬥中資料」，比較接近 skills/items 這種「bootstrap
   * 給定、戰鬥中只查閱」的靜態表。
   */
  summonPool: SummonWave[];
  /**
   * 已觸發過的波次 key 集合，key＝`${summonerEnemyId}:${atHpPct}`（見 engine/summon.ts）。刻意
   * 用 `string[]`（可序列化）而不是 `Set<string>`——BattleState 整體要能被 JSON 序列化/還原
   * （例如存檔、跨 tick 傳遞），`Set` 沒有原生 JSON 表示法，往返序列化會整個遺失內容。用
   * `includes()` 判斷是否已觸發：這個陣列在整場戰鬥中最多只會累積到 summonPool 的波數（通常
   * 個位數），線性搜尋的成本可以忽略，不值得為此換更複雜的資料結構。createBattle 缺省 []。
   */
  summonedWaves: string[];
}
