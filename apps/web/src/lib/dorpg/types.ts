// DORPG 戰鬥畫面共用型別（P0：純靜態畫面、只有本地 UI 狀態，不接 API/DB）。
// 這是元件、素材輔助與範例資料之間的共同契約，各元件代理只能依賴這裡的名字，不得各自另定。

/** 主操作按鈕（道具／防禦／攻擊）四態；對應 kit 的 button_<type>_<state> 四張同尺寸圖。 */
export type BtnState = 'normal' | 'pressed' | 'disabled' | 'active';

/** 逃跑鈕只有三態（kit 沒有 button_escape_active）。 */
export type EscapeState = 'normal' | 'pressed' | 'disabled';

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
};

export type Skill = {
  id: string;
  name: string;
  iconUrl: string;
  cooldownMs: number;
};

export type Item = {
  id: string;
  name: string;
  iconUrl: string;
  quantity: number;
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
};
