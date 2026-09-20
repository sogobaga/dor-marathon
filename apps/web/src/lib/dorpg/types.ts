// DORPG 戰鬥畫面共用型別（P0：純靜態畫面、只有本地 UI 狀態，不接 API/DB）。
// 這是元件、素材輔助與範例資料之間的共同契約，各元件代理只能依賴這裡的名字，不得各自另定。

/** 主操作按鈕（道具／防禦／攻擊）四態；對應 kit 的 button_<type>_<state> 四張同尺寸圖。 */
export type BtnState = 'normal' | 'pressed' | 'disabled' | 'active';

/** 逃跑鈕只有三態（kit 沒有 button_escape_active）。 */
export type EscapeState = 'normal' | 'pressed' | 'disabled';

// ---- P1（本機互動手感）新增：戰鬥數值與技能/道具定義。P0 欄位語意不變，只加可選欄位。 ----

/** 武器種類：對應特效音效包的四組攻擊特效／音效（sword/staff/bow/greatsword）。 */
export type WeaponKind = 'sword' | 'staff' | 'bow' | 'greatsword';

/** 規格 §2 的八屬性；初版相剋倍率全為 1.0（未定相剋前不自行推導）。 */
export type ElementKind = 'metal' | 'wood' | 'water' | 'fire' | 'earth' | 'light' | 'dark' | 'neutral';

/** 規格 §2 ActorDefinition.baseStats：hpMax/mpMax/ATK/MATK/DEF/MDEF。 */
export type ActorStats = {
  hpMax: number;
  mpMax: number;
  atk: number;
  matk: number;
  def: number;
  mdef: number;
};

/**
 * P2（暴擊／Miss／無效攻擊）新增，P3（AGI 攻速／DEX 詠唱縮減，見審查 dorpg_p3 r5）擴充：
 * 命中／暴擊／攻速評級。
 * ⚠️ 審查抓到舊註解在這裡宣稱「統一用百分比制（0–100 尺度，非 0–1）」是錯的：只有
 * critPct/critShield 是 0–100 的機率百分比制。hit/flee 是「評級數值空間」的原始分數，對齊
 * internal/rpg Compute() 的 Derived.Hit/Flee——隨 Base Lv 與 DEX/AGI 線性成長、完全不封頂在
 * 100（玩家 flee 另外套用 flee_cap_pct=95 的上限，但那是設計上的完全迴避門檻，不是「這是機率」
 * 的證據）；missChance() 只吃兩者的「差」乘上 hitFleeScale 換算成百分比，數值本身從來不是機率。
 * aspd 同理是 RO 式攻速評級原始值（0~aspdCap，非機率）。
 * 玩家/隊友的真實值來自 internal/rpg Compute（DEX→命中 hit、AGI→迴避 flee、LUK→暴擊 critPct/
 * critShield、AGI/DEX→攻速 aspd、DEX/INT→詠唱縮減 castReductionPct），隊友再乘 companion 對應
 * 倍率——這一段換算發生在 engine 之外（呼叫端／後端），engine 只負責「有給就用、沒給就用 config
 * 推導預設」，見 engine/formulas.ts 的 deriveDefaultPartyRating／deriveDefaultMonsterRating。
 */
export type CombatRating = {
  /** 命中評級（攻擊方用；換算進 missChance 的 attacker.hit）。非 0–100 機率制，隨等級/DEX 線性成長。 */
  hit: number;
  /** 迴避評級（防守方用；換算進 missChance 的 defender.flee）。非 0–100 機率制，隨等級/AGI 線性成長。 */
  flee: number;
  /** 暴擊率（攻擊方用；換算進 critChance 的 attacker.critPct）。0–100 的機率百分比制。 */
  critPct: number;
  /** 暴擊迴避率（防守方用；從對方 critPct 扣減）。0–100 的機率百分比制。 */
  critShield: number;
  /**
   * P3 新增：攻擊速度評級（RO 式 aspd，0~aspdCap，非機率）。只有玩家會拿它換算攻擊冷卻
   * （engine/formulas.ts 的 attackCooldownFor）；隊友/怪物固定吃 cfg.aspdReference，節奏
   * 維持原本的 config 固定值不受影響（見 deriveDefaultMonsterRating、ai.ts 對節奏的既有設計）。
   */
  aspd: number;
  /**
   * P3 新增：變動詠唱時間縮減 %（0~100，DEX/INT 效果，對齊 internal/rpg Derived.CastReductionPct）。
   * 只有玩家會拿它換算施法時間（engine/formulas.ts 的 effectiveCastMs）；隊友/怪物固定給 0
   * （AI 的技能結算本來就不走 casting 狀態，見 ai.ts 開頭註解，這個欄位對它們無意義）。
   */
  castReductionPct: number;
  /**
   * P5 修正（審查#5【低・PLAUSIBLE】）：被動技能 crit_dmg_pct 加成的總和（%，對齊 internal/rpg
   * Derived.CritDmgPct），疊在浮動暴擊倍率（rollCritMultiplier）之上——engine/combat.ts 的
   * resolveAttackOrDamageSkill 在判定暴擊成立時，會把這個值換算成 ×(1+critDmgPct/100) 疊乘
   * 進 critMul。可選欄位，缺省視為 0（沒有這個被動加成時完全不影響既有暴擊倍率計算），怪物/
   * 沒有真實評級資料的隊友一律中性值 0（見 engine/formulas.ts deriveDefault*Rating）。
   */
  critDmgPct?: number;
};

/**
 * P7（DORPG_P7 CONTRACT §2/§3、WIRE「戰鬥 bootstrap」）：wire 送的武器戰鬥時效果（camelCase）。
 * 這是 Go 端 rpg_weapons.profile（snake_case，含 atk/matk/atk_pct/def_pct/int_bonus/mp_pct/
 * flee_bonus/crit_pct 等純被動加成欄位）在「internal/rpg Compute() 已經把這些被動加成算進玩家
 * stats/rating」之後，剩給 ENGINE 在戰鬥「當下」即時運算的子集——atk/matk/critPct/critDmgPct
 * 這幾個欄位雖然仍隨 wire 送來，但只給角色頁／裝備頁顯示用（WIRE.md 原文：「Compute 已吃掉的
 * int_bonus/mp_pct/atk_pct/matk_pct/def_pct/mdef_pct/flee_bonus/crit_pct 不再送；critPct 仍送
 * 供除錯顯示」——crit_dmg_pct 同一個道理，Compute()《P7 CONTRACT §3》已經把它疊進
 * CombatRating.critDmgPct），ENGINE 的傷害/治療/命中/暴擊計算完全不重複套用 atk/matk/critPct/
 * critDmgPct 這 4 個欄位——重複套用會跟 Compute() 已經算進 PartyMember.stats/rating 的效果疊兩次。
 * 真正被 engine 讀取套用的只有：hits/hitMul/extraHitChancePct/intervalPct/chargeTimeMul/
 * chargeDmgMul/splashPct/sizeBonus/elementResistPct/magicSkillPct/element（見 engine/combat.ts
 * resolveWeaponAttack／resolveAttackOrDamageSkill／resolveCastEffect／applyPartyDamage），
 * P12（CONTRACT §1/§3、WIRE「引擎」）新增 rowBonusFrontPct/rowBonusRearPct/pierceChancePct/
 * pierceDmgPct 四欄後同樣加入這份「真的被讀」清單（見 formulas.ts rowBonusMultiplier／
 * combat.ts resolveWeaponAttack）。
 *
 * P12 新增四欄（by rpg_weapon_types.traits 的 row_bonus_front_pct／row_bonus_rear_pct／
 * pierce_chance_pct／pierce_dmg_pct 合併進武器 profile，型別層跟既有其餘欄位一樣是「後端已經
 * 合併好的最終值」，不在這裡重新查 traits）：
 *   - rowBonusFrontPct／rowBonusRearPct：對「前排／後排」怪物的物理傷害加成 %（弓對後排、
 *     鈍器對前排），見 formulas.ts rowBonusMultiplier()。
 *   - pierceChancePct／pierceDmgPct：普攻命中前排目標時，這個機率讓攻擊「貫穿」到對應後排
 *     位置造成 pierceDmgPct% 波及傷害（見 combat.ts resolveWeaponAttack 的貫穿判定），只有
 *     普攻會判定，物理技能不會。
 * 四欄缺省 0（中性值，等同沒有這個機制），見 formulas.ts NEUTRAL_WEAPON_PROFILE。
 */
export interface WeaponProfileWire {
  atk: number;
  matk: number;
  hits: number;
  hitMul: number;
  extraHitChancePct: number;
  intervalPct: number;
  chargeTimeMul: number;
  chargeDmgMul: number;
  splashPct: number;
  sizeBonus: { small: number; medium: number; large: number };
  critPct: number;
  critDmgPct: number;
  elementResistPct: number;
  magicSkillPct: number;
  element: ElementKind;
  /** P12：對前排怪物的物理傷害加成 %（鈍器 mc_hammer/mc_mallet/mc_club，鈍器打前排肉搏更順手）。 */
  rowBonusFrontPct: number;
  /** P12：對後排怪物的物理傷害加成 %（弓 ar_longbow/ar_shortbow/ar_crossbow，弓箭本來就利於打後排）。 */
  rowBonusRearPct: number;
  /** P12：槍（hk_spear）普攻命中前排目標時的貫穿機率 %；0＝這把武器沒有貫穿機制。 */
  pierceChancePct: number;
  /** P12：貫穿觸發時，對應後排目標受到的波及傷害＝floor(本段實傷×這個百分比/100)。 */
  pierceDmgPct: number;
}

/**
 * P7：目前裝備的武器（wire 頂層新欄位，WIRE.md：「玩家 party member 新增
 * `weapon: { id, name, typeId, visual, profile: WeaponProfileWire }|null`」）。刻意不覆蓋既有的
 * `PartyMember.weapon`（那是 P1 起沿用至今、外部消費者（BattleEvent.weapon／CombatFxLayer）都
 * 認得的「視覺特效組」簡單字串，見該欄位型別註解，且 api.ts 目前的 RpgBootstrapPartyMemberRaw.
 * weapon 仍是純字串，INTEGRATOR 尚未把它擴充成物件）——這裡另開一個欄位 `equippedWeapon` 承接
 * 武器系統的完整資料，engine/index.ts 的 toPartyActor 用
 * `equippedWeapon?.visual ?? weapon ?? 'sword'` 決定最終顯示用的視覺特效組（CONTRACT §3：
 * 「武器視覺＝type.visual（覆蓋職業預設）」），兩個欄位並存、互不衝突，也不要求本檔的 domain
 * 型別欄位名稱跟 wire JSON 逐字一致（fromApi.ts 負責兩者之間的映射，跟本檔其餘 wire→domain
 * 轉換的既有做法一致，例如 EffectAtLevel 的 duration_ms/mp_cost→durationMs/mpCost）。
 */
export interface EquippedWeaponWire {
  id: string;
  name: string;
  typeId: string;
  visual: WeaponKind;
  profile: WeaponProfileWire;
}

/**
 * P8（DORPG_P8 CONTRACT §2、WIRE「戰鬥 bootstrap」）：wire 送的裝備（防具＋飾品）戰鬥時效果彙總
 * （camelCase）——已經是「防具＋飾品全部加總、部分欄位 clamp 過」的最終結果，只給引擎在戰鬥
 * 「當下」即時運算用；六素質 flat／def／hp_pct／mp_pct／atk_pct／matk_pct／crit_pct／crit_dmg_pct
 * 這些防具彙總已經在 internal/rpg Compute 階段吃進玩家 stats/rating，不會也不應該再送到這裡
 * 重複套用（同 WeaponProfileWire 型別註解「Compute 已吃掉的欄位不再送」的精神）。武器仍走自己
 * 的 WeaponProfileWire，不含在這份彙總內——兩者在需要相加的欄位（intervalPct／elementResistPct）
 * 各自帶著武器與裝備兩份數字，由 engine 呼叫端相加（見 engine/formulas.ts combineIntervalPct／
 * combineElementResistPct）。各欄位套用位置：
 *   - intervalPct：與 weapon.intervalPct 相加（combineIntervalPct，clamp ≥ −50）套進
 *     attackCooldownFor（見 dispatch.ts ATTACK_RELEASE）。
 *   - mpCostReducePct：技能 MP 消耗 max(1, floor(mpCost×(1−pct/100)))（見 formulas.ts
 *     effectiveMpCost），同時用於 dispatch.ts 的 MP 足夠檢查與實際扣除。
 *   - hpRegenPctPer5s／mpRegenPctPer5s：每 5000ms 戰鬥時鐘回復 floor(hpMax/mpMax×pct/100)
 *     （見 tick.ts applyEquipmentRegen），不超上限、死亡不回。
 *   - damageTakenPct：與 buff 的 damage_taken_pct 相加後 clamp ≥ −60（effects.ts
 *     damageTakenMultiplier）。
 *   - elementResistPct：與武器（鍊）elementResistPct 相加後 clamp ≤ 60（combineElementResistPct），
 *     套用位置同既有的 applyPartyDamage。
 */
export interface EquipmentEffectsWire {
  intervalPct: number;
  mpCostReducePct: number;
  hpRegenPctPer5s: number;
  mpRegenPctPer5s: number;
  damageTakenPct: number;
  elementResistPct: number;
}

export type PartyMember = {
  id: string;
  name: string;
  level: number;
  hp: number;
  hpMax: number;
  mp: number;
  mpMax: number;
  /** null = 沒有頭像時保留空白插槽（kit 說明：資料為 null 顯示空白而非佈景假圖）。 */
  portraitUrl: string | null;
  /** P1：戰鬥數值；缺省由 engine 依等級推導（見 engine/createBattle）。 */
  stats?: ActorStats;
  /** P1：隊友 AI 的武器（決定特效/音效組）；缺省 sword。 */
  weapon?: WeaponKind;
  /** P2：命中/暴擊評級；缺省由 engine 依 cfg.hitRate 推導後備值（見 CombatRating 型別註解）。 */
  rating?: CombatRating;
  /**
   * P5（DORPG_P5 CONTRACT §1）：目前職業 id；只影響武器視覺／物攻分支／技能集合，不影響 HP/MP
   * 係數，engine 的戰鬥數值計算完全不讀這個欄位——單純透傳給 FRONTEND 顯示職業徽章用。
   * 未選職業＝null（沿用現行行為：全域預設武器、無職業技能）。
   */
  jobId?: string | null;
  /**
   * P6（CONTRACT §3.2/WIRE「戰鬥 bootstrap」）：這位隊友（非玩家）AI 可用的技能——後端已展開
   * （不含 passive、只含 implemented=true），由 engine/ai.ts 依五段優先序挑選施放，不再借用
   * BattleState.skills（那是玩家自己裝備的技能欄，P1 時期 AI 的簡化借用寫法已被取代，見 ai.ts
   * 檔頭註解）。玩家（party[0]）不會有這個欄位——玩家的技能走 BattleSample.skills 技能欄與
   * USE_SKILL 指令，跟這裡完全是兩條路徑。缺省 []（沒有可用技能時 AI 只會普攻，見 ai.ts）。
   */
  skills?: Skill[];
  /**
   * P6（CONTRACT §3.4「腳本」）：這位隊友目前套用的腳本名稱，純透傳供 FRONTEND 顯示（例如隊伍
   * 畫面卡片上的「腳本名／Lv」），engine 的戰鬥數值計算完全不讀這個欄位。缺省 undefined＝沒有
   * 腳本資訊可顯示（例如舊版後端、或未來的系統預設腳本理論上一定會有名稱，這裡選填只是防禦）。
   */
  presetName?: string;
  /**
   * P7（CONTRACT §2/§5）：目前裝備的武器；null＝明確卸下、undefined＝wire 尚未送這個欄位
   * （BACKEND／INTEGRATOR 尚未上線武器系統前的舊資料，或本輪傭兵尚未開放裝備）。engine 一律用
   * `equippedWeapon?.profile ?? null` 當作 PartyActor.weaponProfile，缺省時全部戰鬥效果退化成
   * 中性（見 engine/formulas.ts NEUTRAL_WEAPON_PROFILE），不影響任何既有斷言。
   */
  equippedWeapon?: EquippedWeaponWire | null;
  /**
   * P8（CONTRACT §2／WIRE「戰鬥 bootstrap」）：這位玩家目前裝備（防具＋飾品）的戰鬥效果彙總；
   * 缺省（undefined，舊版後端尚未上線 P8）engine 一律 fallback 成 NEUTRAL_EQUIPMENT_EFFECTS
   * （見 engine/formulas.ts），讓「沒有這個欄位」與「明確送一份全零的物件」在戰鬥數值上完全
   * 等價——WIRE.md 明講「傭兵一律零值物件」，本輪傭兵理論上會收到全零的完整物件而不是缺欄位，
   * 但 fromApi.ts 的 asEquipmentEffects() 對兩種情況一視同仁，都會得出同一份中性值，跟 P7
   * equippedWeapon 缺省時 fallback 成 NEUTRAL_WEAPON_PROFILE 同一個精神。
   */
  equipmentEffects?: EquipmentEffectsWire;
  /**
   * P9（DORPG_P9 CONTRACT §1／WIRE「戰鬥 bootstrap」）：這位角色目前套用的 AI 策略 id
   * （balanced｜mp_conserve｜skill_aggressive｜protect_allies｜focus_fire｜element_advantage）；
   * 玩家＝player_characters.auto_strategy_id，傭兵＝preset.strategy_id。engine 端一律經
   * `resolveStrategy(strategyId, config.aiStrategies)` 正規化（未知/缺欄位一律退回 balanced，
   * 見 engine/strategies.ts），故這裡刻意維持寬鬆的 string、不在型別層收斂成字面量聯集——
   * 避免舊版後端／未升級的資料送出這裡還沒收錄的新策略 id 時讓整包型別檢查失敗，缺省交給
   * fromApi.ts／fixture.ts 給 'balanced' 預設值。
   */
  strategyId?: string;
  /**
   * P10（DORPG_P10 CONTRACT §2/§3、WIRE「戰鬥 bootstrap」）：這位角色是否學過「守護本能」被動
   * （heavy_knight 專屬 hk_c2，≥1 級）——後端已經算好（不是原始技能等級，engine 完全不重新推導），
   * 只有 true 時 GUARD_BEGIN 期間才算進入守護狀態（見 engine/formulas.ts inGuardianState）。
   * AI 隊友目前不能按防禦（CONTRACT §1），這個欄位對非玩家隊員是 no-op；缺省 undefined 由
   * engine/index.ts toPartyActor 的 `pm.guardTaunt ?? false` 接手。
   */
  guardTaunt?: boolean;
  /**
   * P10（CONTRACT §3、WIRE「戰鬥 bootstrap」）：職業天生特性中的「受到傷害」百分比調整（目前只有
   * 重騎士 traits.damage_taken_pct=-15）。bootstrap 已經把它加總進 `equipmentEffects.damageTakenPct`
   * ——這裡只是原樣「另送供顯示」的一份拷貝（角色頁「職業特性：受到傷害 −15%」），engine 的戰鬥
   * 數值計算完全不讀這個欄位（避免透過 PartyMember 重複套用一次已經算進 equipmentEffects 的效果）。
   * 缺省 undefined＝沒有職業特性可顯示（舊版後端，或該職業沒有 traits）。
   */
  jobTraits?: { damageTakenPct: number };
};

/** 場景五個怪物站位 ID，與 content pack scene.json 的 monsterSlots[].id 同名。 */
export type EnemySlotId =
  | 'rear_left'
  | 'rear_right'
  | 'front_left'
  | 'front_center'
  | 'front_right';

/**
 * P12（CONTRACT §1「排位＝既有槽位」）：怪物前排／後排，跟 engine/types.ts 的 EnemyRow 是同一份
 * 字面聯集——兩邊各自宣告一次（不跨檔 import）是刻意的：engine/types.ts 已經 `import type {
 * ..., EnemySlotId, ... } from '../types'`（本檔是 engine 型別的上游），這裡若反過來 import
 * engine/types.ts 的 EnemyRow 會形成循環依賴；字面聯集只有兩個值，重複宣告的維護成本遠低於
 * 拆檔案打破循環的成本。
 */
export type EnemyRow = 'front' | 'rear';

export type SceneSlot = {
  id: EnemySlotId;
  /** 腳點在場景中的 0–1 正規化 x。 */
  x: number;
  /** 腳點在場景中的 0–1 正規化 y。 */
  y: number;
  /** 怪物 512 畫布顯示寬佔場景寬的比例（displayWidth = scale × sceneWidth）。 */
  scale: number;
  /** 前排要疊在後排之上（z-index），故顯式標記排別。 */
  row: 'rear' | 'front';
};

export type Scene = {
  id: string;
  name: string;
  imageUrl: string;
  slots: SceneSlot[];
};

export type Enemy = {
  id: string;
  name: string;
  level: number;
  hp: number;
  hpMax: number;
  slot: EnemySlotId;
  /** 怪物 poster（idle 第 0 格，512×512 含 alpha）。 */
  imageUrl: string;
  rank?: string;
  attribute?: string;
  size?: string;
  race?: string;
  /** P1：戰鬥數值；缺省由 engine 依等級推導。 */
  stats?: ActorStats;
  /** P1：初始目標挑選用（規格 §1：最高 threatPriority 存活敵人，同序取槽位順序最小）；缺省 0。 */
  threatPriority?: number;
  /** P1：BOSS 可設 false（逃跑鈕直接停用）；缺省 true。 */
  canEscape?: boolean;
  /** P2：命中/暴擊評級；缺省由 engine 依 config 的 battle_monster_* 係數推導。 */
  rating?: CombatRating;
  /**
   * P5（CONTRACT §6）：這隻怪的弱點屬性桶（DOR 8 桶英文代碼，與 ElementKind 同一個詞彙表）；
   * 技能 element 命中其中之一 → 傷害 ×(1+weaknessBonusPct/100)，見 engine/formulas.ts
   * elementMultiplier() 的「chart 覆寫優先，否則 weakElements 命中」規則。缺省 []（無弱點）。
   */
  weakElements?: ElementKind[];
  /**
   * P12（CONTRACT §1／WIRE「戰鬥 bootstrap」：「enemies[].row」）：這隻怪目前是前排還是後排；
   * 缺省 undefined（舊版後端尚未送這個欄位，或 fromApi.ts 驗證失敗）由 engine/index.ts 的
   * toEnemyActor 直接透傳給 EnemyActor.row，engine 內部一律用 `enemy.row ?? rowOfSlot(enemy.slot)`
   * 取得有效排位（見 engine/formulas.ts rowOfSlot 型別註解），不會因為這個欄位缺席而漏掉武器排位
   * 加成／槍系貫穿機制。
   */
  row?: EnemyRow;
};

// ---- P5（DORPG_P5 CONTRACT §5/§6）新增：技能等級、傷害屬性、buff/debuff/passive/special 詞彙。 ----

/** CONTRACT §6：`dmg_type` 決定傷害公式用哪組攻防——physical 用 ATK 扣 DEF（沿用既有行為），
 *  magic 用 MATK 扣 MDEF。Skill.dmgType 缺省視為 'physical'（既有 5 個技能沒有這個欄位，向下相容）。 */
export type DmgType = 'physical' | 'magic';

/**
 * CONTRACT §5 buff/debuff/passive 共用的 stat 詞彙表（僅列出 engine 這輪要認得的字面值；
 * 各 kind 實際允許的子集見 CONTRACT §5，engine 不在型別層另外收窄——只在套用時各自只讀取
 * buff/debuff 目標陣營真正會出現的那幾種，多餘的字面值單純被忽略，不會壞掉）。
 */
export type BuffDebuffStat =
  | 'atk_pct'
  | 'matk_pct'
  | 'def_pct'
  | 'mdef_pct'
  | 'aspd'
  | 'crit_pct'
  | 'flee'
  | 'hit'
  | 'hp_regen_pct'
  | 'damage_taken_pct';

/**
 * WIRE.md 引擎要吃的新欄位：wireSkill.effect，「已依 level 展開」的即時數值——base+per_level×(lv-1)
 * 的計算永遠由後端（或 fixture.ts 的離線鏡像）做好，engine 只管讀最終值，不重新推導等級公式。
 * 刻意用同一個扁平形狀涵蓋全部 7 種 kind（跟 WIRE.md 給的字面定義逐欄對齊），而不是每個 kind
 * 各自一個判別聯集：buff/debuff/passive 的欄位（stat/value/durationMs）跟 damage/heal/shield 的欄位
 * （coef/flat/hits）本來就不會同時有意義，讓消費端（resolveBuffDebuff 等）自己只挑需要的欄位讀，
 * 比維護 7 條判別聯集成員更貼近 WIRE 契約字面、也更不容易在雙方各自實作時對不上形狀。
 */
export interface EffectAtLevel {
  kind: Skill['kind'];
  stat?: BuffDebuffStat;
  value?: number;
  durationMs?: number;
  coef?: number;
  flat?: number;
  hits?: number;
  target: Skill['target'];
  mpCost: number;
  /**
   * P10（DORPG_P10 WIRE.md「REST」：「passive 展開新增 guard_taunt: boolean」）：只有 kind='passive'
   * 的「守護本能」（hk_c2）會帶這個旗標＝true——純粹是技能目錄／角色頁展示用（例如標示「此被動
   * 提供守護狀態」），engine 完全不讀它：真正決定戰鬥中守護狀態的是 PartyMember.guardTaunt／
   * PartyActor.guardTaunt（後端已經依「這位角色是否學過這顆被動」算好的彙總布林，見該欄位型別
   * 註解），不是重新解析技能欄裡每一顆被動的 effect。缺省 undefined＝不是這個特殊被動。
   */
  guardTaunt?: boolean;
}

export type Skill = {
  id: string;
  name: string;
  iconUrl: string;
  cooldownMs: number;
  /**
   * P1：damage/heal/shield。P5（CONTRACT §5）新增 buff/debuff/passive/special：
   * buff/debuff＝暫時性 stat 加成／減成（見 BuffDebuffStat），passive＝不進技能欄（後端已算進
   * stats，engine 完全忽略），special＝本輪引擎未實裝的技能（UI 顯示但不可用，見 `implemented`）。
   * P10（DORPG_P10 CONTRACT §3、WIRE「引擎」）新增 taunt＝重騎士「守護」路線專屬：施放後把自己
   * 導入守護狀態（吸引怪物攻擊），展開後的即時數值見下面 `taunt` 欄位、結算見 engine/combat.ts
   * resolveTaunt。
   */
  kind: 'damage' | 'heal' | 'shield' | 'buff' | 'debuff' | 'passive' | 'special' | 'taunt';
  /**
   * P1：目標規則；'ally' 需經 chooseAlly 選隊友（含自己）。P5 新增 'allEnemies'（damage/debuff
   * 專用：一次打全體敵人，跟既有 'allAllies' 對稱）。
   */
  target: 'enemy' | 'ally' | 'self' | 'allAllies' | 'allEnemies';
  mpCost: number;
  /** 規格 §2：raw = floor((攻擊力×coefficient + flat)×element×charge)；治療 = floor(MATK×coefficient + flat)。
   *  P5：buff/debuff/passive/special 不使用這兩個欄位（該讀 `effect`），維持 0 即可。 */
  coefficient: number;
  flat: number;
  element?: ElementKind;
  /** 特效／音效組。 */
  weapon: WeaponKind;
  /** 施放時間（casting 行為鎖），缺省 config.defaultCastMs。 */
  castMs?: number;
  /** P5：kind='damage' 專用，一次施放命中次數；缺省 1（既有技能沒有這個欄位，向下相容）。
   *  >1 時每段各自獨立跑 miss/crit/傷害結算與各自的 'attack' 事件（見 engine/combat.ts）。 */
  hits?: number;
  /** P5：kind='damage' 專用傷害屬性；缺省 'physical'（見 DmgType 型別註解）。 */
  dmgType?: DmgType;
  /** P5：職業技能的目前等級／等級上限；一般技能（既有 5 個預設技能）沒有這兩個欄位，
   *  代表「數值已經是最終值，沒有等級可言」。 */
  level?: number;
  maxLevel?: number;
  /**
   * P6（CONTRACT §3.2：隊友 AI「damage 技能可用且 MP 足夠→用 tier 最高的」）：技能所屬路線內的
   * 順位（1 起算，數字越大代表越後期／越強的技能）。P5 WIRE 已經說明「已選職業技能依 path→tier
   * 排序填入」技能欄，故陣列順序本身就是 tier 遞增序——這個欄位是給 ai.ts 挑「tier 最高」時可以
   * 直接比較數字，不必依賴陣列順序（陣列順序在 fixture/測試手造資料時不見得可靠）。缺省 undefined
   * 時 ai.ts 的挑選邏輯退回「陣列中較後面的即視為較高 tier」（見 engine/ai.ts pickHighestTierSkill
   * 的型別註解）。BACKEND 的 wireSkill 目前尚未送這個欄位（見 fromApi.ts mapPartyMemberSkills 的
   * 型別擴充註解與本輪回報「需要 BACKEND 增加」）。
   */
  tier?: number;
  /** P5：後台/設計填的效果說明文字，角色頁技能區塊與技能欄 tooltip 直接顯示，engine 不解析。 */
  displayText?: string;
  /**
   * P5：CONTRACT §5 special 詞彙——本輪引擎未實裝的技能（賺錢效益、掉落品質、免疫異常、反擊…）。
   * 缺省 true（既有技能與已實裝的新 kind 一律可用）；false 時 dispatch 的 USE_SKILL 直接拒絕並發
   * 'skillUnavailable' 事件（見 engine/dispatch.ts），戰鬥中完全不可用，但仍會出現在技能欄
   * （UI 顯示成不可按，讓玩家看得到「這技能存在，只是還沒做」）。
   */
  implemented?: boolean;
  /**
   * P5：依目前等級展開後的即時數值（WIRE.md：「已依 level 展開」）。damage/heal/shield 的實際戰鬥
   * 結算仍讀上面的頂層 coefficient/flat/hits/target（跟既有 5 個技能的用法保持一致，不重新繞一層），
   * 這個欄位對它們只是鏡射方便 FRONTEND 顯示；buff/debuff 的 stat/value/durationMs 沒有對應的頂層
   * 欄位可放，`effect` 是它們唯一的權威資料來源（見 engine/combat.ts 的 resolveBuffDebuff）。
   */
  effect?: EffectAtLevel;
  /**
   * P10（DORPG_P10 CONTRACT §3、WIRE「戰鬥 bootstrap」）：kind='taunt' 專屬、已依目前等級展開的
   * 即時數值（跟 `effect` 之於 buff/debuff 是同一種「WIRE 已展開，engine 只管讀最終值」的關係，
   * 兩者分開放是因為 taunt 的欄位形狀跟 EffectAtLevel 對不上——它沒有 stat/value，多了
   * retarget 這個 buff/debuff 詞彙表沒有的布林）。durationMs＝這次守護/挑釁持續多久（戰鬥時鐘
   * 毫秒）；damageTakenPct＝套用的 damage_taken_pct buff 值（0＝這顆技能不附帶減傷，例如挑釁）；
   * retarget＝true 時施放當下立刻把所有存活敵人（含 windup 中）的鎖定目標改成施放者（見
   * engine/combat.ts resolveTaunt），false 時只影響之後的選目標（守護姿態）。非 kind='taunt'
   * 的技能恆為 undefined。
   */
  taunt?: { durationMs: number; damageTakenPct: number; retarget: boolean };
};

/** P5（CONTRACT §6）：技能欄容量 8→10（兩排各 5）。FRONTEND 的 SkillTray/CommandBar 應改讀這個常數，
 *  不要沿用原本寫死的 8（見 SkillTray.tsx 的 LOADOUT_SIZE）。 */
export const SKILL_SLOTS = 10;

export type Item = {
  id: string;
  name: string;
  iconUrl: string;
  quantity: number;
  /** P1：hp/mp 回復量（絕對值）；revive 為復活後 HP 佔 hpMax 的百分比（0–100）。 */
  kind: 'hp' | 'mp' | 'revive';
  amount: number;
};

export type TrayMode = 'skills' | 'items';

export type BattleSample = {
  party: PartyMember[];
  enemies: Enemy[];
  scene: Scene;
  /** P5：固定 SKILL_SLOTS（10）格裝備欄（兩排各 5），未裝備補 null；畫面一次只顯示 5 格（offset）。 */
  skills: (Skill | null)[];
  items: Item[];
  initialTargetId: string;
  /** P1：BGM 用（boss 場播 BOSS_BGM）；缺省 normal。 */
  sceneKind?: 'normal' | 'boss';
  /** P1：逃跑成功率（規格展示值 0.35）；缺省 0.35。 */
  escapeChance?: number;
};
