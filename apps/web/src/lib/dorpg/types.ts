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
};

/** 場景五個怪物站位 ID，與 content pack scene.json 的 monsterSlots[].id 同名。 */
export type EnemySlotId =
  | 'rear_left'
  | 'rear_right'
  | 'front_left'
  | 'front_center'
  | 'front_right';

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
   */
  kind: 'damage' | 'heal' | 'shield' | 'buff' | 'debuff' | 'passive' | 'special';
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
