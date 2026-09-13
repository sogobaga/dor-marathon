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
};

export type Skill = {
  id: string;
  name: string;
  iconUrl: string;
  cooldownMs: number;
  /** P1：技能效果種類。 */
  kind: 'damage' | 'heal' | 'shield';
  /** P1：目標規則；'ally' 需經 chooseAlly 選隊友（含自己）。 */
  target: 'enemy' | 'ally' | 'self' | 'allAllies';
  mpCost: number;
  /** 規格 §2：raw = floor((攻擊力×coefficient + flat)×element×charge)；治療 = floor(MATK×coefficient + flat)。 */
  coefficient: number;
  flat: number;
  element?: ElementKind;
  /** 特效／音效組。 */
  weapon: WeaponKind;
  /** 施放時間（casting 行為鎖），缺省 config.defaultCastMs。 */
  castMs?: number;
};

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
  /** 固定 8 格裝備欄，未裝備補 null；畫面一次只顯示 5 格（offset 0–3）。 */
  skills: (Skill | null)[];
  items: Item[];
  initialTargetId: string;
  /** P1：BGM 用（boss 場播 BOSS_BGM）；缺省 normal。 */
  sceneKind?: 'normal' | 'boss';
  /** P1：逃跑成功率（規格展示值 0.35）；缺省 0.35。 */
  escapeChance?: number;
};
