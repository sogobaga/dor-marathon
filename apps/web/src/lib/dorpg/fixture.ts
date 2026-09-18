// DORPG P2 內容資料層的 TS 鏡像（給 migrations/176 的 seed 在前端離線使用）。
// 用途：`/dev/dorpg` 沒有 API/DB 時的預覽，以及不依賴後端的自動化測試——所有數值必須與
// services/api/migrations/176_rpg_battle_content.sql 的 seed 逐項一致，TUNE 調參時兩邊要
// 同步修改，否則離線預覽/測試看到的手感會跟正式環境兜不起來（見 CONTRACT.md §0 TUNE 分工）。
//
// 型別刻意不放進 lib/dorpg/types.ts——那個檔案的所有權屬於 FE_MEMBER（CONTRACT.md §0），
// 這裡新增的「內容列」型別只服務本檔與呼叫 buildFixtureSample() 的人；FE_MEMBER 之後接 API
// 會在 fromApi.ts 另外定義後端回應形狀的型別，兩邊不必共用。
//
// 戰鬥節奏（攻速/蓄氣/引導時間等）完全不動——P1 engine 的 DEFAULT_BATTLE_CONFIG 已經跟契約
// §3.1 的 battle_attack_cooldown_ms 等欄位數字一致，本檔不重複鏡像那組設定；只鏡像 P2 新增的
// 「內容」與「D2 縮放公式」。

import { charPortrait, kitAsset, monsterPoster, sceneImage } from './assets';
import type {
  BattleSample,
  CombatRating,
  DmgType,
  EffectAtLevel,
  ElementKind,
  Enemy,
  EnemySlotId,
  EquipmentEffectsWire,
  EquippedWeaponWire,
  Item,
  PartyMember,
  Scene,
  SceneSlot,
  Skill,
  WeaponKind,
  WeaponProfileWire,
} from './types';
import { SKILL_SLOTS } from './types';
// P2（暴擊／Miss／無效攻擊）：只借用 engine 已凍結匯出的預設常數算怪物評級基準，不是改動 engine
// 本身——這裡是純消費端（跟 BattleScreen.tsx 呼叫 engine 的方式一樣），避免在本檔重複硬寫一份
// monsterHitBase 等數字、之後 engine 那邊調預設值卻忘記回頭同步這裡。
import { DEFAULT_BATTLE_CONFIG } from './engine';

// ---------------------------------------------------------------------------
// 內容列型別（鏡像 migration 176 的表；只保留 buildFixtureSample 實際用得到的欄位——
// is_active/created_at/updated_at 這類純資料庫簿記欄位在離線 fixture 沒有意義，故略去）。
// ---------------------------------------------------------------------------

export type MonsterRank = 'A' | 'B' | 'C' | 'D' | 'E';

export interface MonsterRow {
  id: string;
  name: string;
  rank: MonsterRank;
  /**
   * P7（CONTRACT §1）更新：這裡改存 ElementKind 英文字面值（metal/wood/water/fire/earth/light/
   * dark/neutral），對齊 rpg_monsters.attribute 欄位本來就宣告的 domain 註解（migration 176
   * `attribute TEXT ... -- metal|wood|water|fire|earth|light|dark|neutral`）與本輪 P7 CONTRACT
   * §1 的五行相剋表——engine 的 elementMultiplier() 用英文字面值跟 ELEMENT_BEATS 表比對，中文
   * 沒有意義（永遠落到「不相干」的 1.0）。
   * ⚠️ 已知落差（見任務回報）：正式環境 migration 176 seed 實際 INSERT 的是中文字串（'闇'/'金'/
   * '土'/'無'/'木'），跟欄位自己宣告的英文 domain 不符——這是 P2 時期刻意的歷史決策（該檔頭有
   * 說明理由：純顯示，當時還沒有屬性相剋機制）。本檔（fixture.ts，離線預覽/驗證腳本專用鏡像）
   * 改成英文是為了讓 P7 的五行系統在離線環境有意義地被展示/測試，*不是*正式 DB 資料已經同步
   * 遷移——需要 BACKEND 另外出一個 migration 把 rpg_monsters.attribute 的既有 5 筆資料轉成英文
   * （中文→英文對照：闇→dark、金→metal、土→earth、無→neutral、木→wood），五行相剋表在此之前
   * 對正式怪物是 no-op（weakElements／管理者覆寫兩條路徑不受影響，本來就是英文）。
   */
  attribute: string;
  /**
   * P7（CONTRACT §1）更新：同 attribute，改存英文字面值（small/medium/large），對齊
   * rpg_monsters.size 欄位宣告的 domain（migration 176 同一段 `size TEXT ... -- small|medium|
   * large`）——engine 的武器 sizeBonus[enemy.size] 只認得這三個英文字面值，中文（'大型'/'中型'/
   * '小型'）不會命中任何 key，永遠是 0 加成。同上，正式 DB 現況仍是中文，需要 BACKEND 另外遷移
   * （大型→large、中型→medium、小型→small）。
   */
  size: string;
  race: string;
  posterUrl: string;
  hpMult: number;
  atkMult: number;
  defMult: number;
  speedMult: number;
  threat: number;
  isBoss: boolean;
  sortOrder: number;
  /** P5（CONTRACT §6）：弱點屬性桶，鏡射 migration 180 預計加的 rpg_monsters.weak_elements
   *  （尚未套用，見任務回報）；離線 fixture 先依各怪物既有 attribute 給一組合理值示範相剋機制。 */
  weakElements: ElementKind[];
}

export interface SkillRow {
  id: string;
  name: string;
  iconId: string;
  kind: Skill['kind'];
  target: Skill['target'];
  weapon: WeaponKind;
  element: ElementKind;
  mpCost: number;
  cooldownMs: number;
  coefficient: number;
  flat: number;
  castMs: number;
  isDefault: boolean;
  sortOrder: number;
  /** P5：kind='damage' 專用，一次施放命中次數；缺省 1（見 Skill.hits 型別註解）。 */
  hits?: number;
  /** P5：kind='damage' 專用傷害屬性；缺省 'physical'。 */
  dmgType?: DmgType;
  /** P5：職業技能等級（示範技能固定給 1，離線預覽不做配點）；一般技能（既有 5 個）不填。 */
  level?: number;
  maxLevel?: number;
  /** P5：角色頁/技能欄要顯示的效果說明文字。 */
  displayText?: string;
  /** P5：special 詞彙——本輪引擎未實裝；缺省視為 true（可用）。 */
  implemented?: boolean;
  /** P5：buff/debuff 專屬——展開後的即時數值（離線示範技能固定給 level=1 的數值，不做等級縮放）。 */
  effect?: EffectAtLevel;
}

export interface ItemRow {
  id: string;
  name: string;
  iconId: string;
  kind: Item['kind'];
  amount: number;
  defaultQuantity: number;
  sortOrder: number;
}

export interface SceneRow {
  id: string;
  name: string;
  imageUrl: string;
  slots: SceneSlot[];
  sortOrder: number;
}

export interface CompanionRow {
  id: string;
  name: string;
  portraitId: string;
  role: string;
  weapon: WeaponKind;
  levelOffset: number;
  hpMult: number;
  mpMult: number;
  atkMult: number;
  matkMult: number;
  defMult: number;
  mdefMult: number;
  actIntervalMult: number;
  skillIds: string[];
  isPlayerPortrait: boolean;
  sortOrder: number;
}

export interface EncounterMonsterRow {
  slot: EnemySlotId;
  monsterId: string;
  /** 個別再乘的 power_scale（草案全部 1，TUNE 可調單一槽位）。 */
  powerScale: number;
}

export interface EncounterRow {
  code: string;
  title: string;
  subtitle: string;
  sceneId: string;
  sceneKind: 'normal' | 'boss';
  difficulty: number;
  powerScale: number;
  escapeChance: number;
  canEscape: boolean;
  monsters: EncounterMonsterRow[];
  /** P6（CONTRACT §2）：這場的怪物等級 N，鏡射 migration 181 要加的 rpg_encounters.monster_level
   *  （尚未套用，見任務回報）；離線 fixture 先依契約給定的六場數字示範 level 模式縮放。 */
  monsterLevel: number;
}

// ---------------------------------------------------------------------------
// Seed 鏡像（與 migrations/176_rpg_battle_content.sql 逐項對齊；改一邊記得改另一邊）。
// ---------------------------------------------------------------------------

/** 六張場景目前共用同一組 monsterSlots（逐字取自各 scene.json，六份內容完全相同）。 */
const SHARED_SCENE_SLOTS: SceneSlot[] = [
  { id: 'rear_left', x: 0.27, y: 0.53, scale: 0.25, row: 'rear' },
  { id: 'rear_right', x: 0.68, y: 0.54, scale: 0.38, row: 'rear' },
  { id: 'front_left', x: 0.18, y: 0.88, scale: 0.33, row: 'front' },
  { id: 'front_center', x: 0.5, y: 0.89, scale: 0.36, row: 'front' },
  { id: 'front_right', x: 0.82, y: 0.88, scale: 0.33, row: 'front' },
];

export const RPG_SCENES: SceneRow[] = [
  { id: 'scene_taipei_101', name: '台北101', imageUrl: sceneImage('scene_taipei_101'), slots: SHARED_SCENE_SLOTS, sortOrder: 1 },
  { id: 'scene_fuhe_bridge', name: '福和橋', imageUrl: sceneImage('scene_fuhe_bridge'), slots: SHARED_SCENE_SLOTS, sortOrder: 2 },
  { id: 'scene_taipei_stadium', name: '台北田徑場', imageUrl: sceneImage('scene_taipei_stadium'), slots: SHARED_SCENE_SLOTS, sortOrder: 3 },
  { id: 'scene_ximending', name: '西門町', imageUrl: sceneImage('scene_ximending'), slots: SHARED_SCENE_SLOTS, sortOrder: 4 },
  { id: 'scene_tamsui_estuary', name: '淡水河口', imageUrl: sceneImage('scene_tamsui_estuary'), slots: SHARED_SCENE_SLOTS, sortOrder: 5 },
  { id: 'scene_jiannan_mountain', name: '劍南山', imageUrl: sceneImage('scene_jiannan_mountain'), slots: SHARED_SCENE_SLOTS, sortOrder: 6 },
];

/**
 * id/name/rank/attribute/size/race 逐字取自各 monster.json；倍率為契約 §2 給的草案值。
 * weakElements（P5 CONTRACT §6）：離線 fixture 先依各怪物既有中文 attribute 給一組合理弱點桶
 * （沿用 P2 時期 elementChart 的相剋直覺——金弱火、木弱火、土弱水、闇弱光；無屬性視為真中性、
 * 不給弱點），只是資料表達方式從「全域 elementChart」搬到「個別怪物 weakElements」；真正的正式值
 * 由 migration 180 決定（本輪未套用，見任務回報），這裡不是權威資料來源。
 */
export const RPG_MONSTERS: MonsterRow[] = [
  { id: 'DOR-MON-A-67000200001', name: '幽暗食人花首領', rank: 'A', attribute: 'dark', size: 'large', race: '植物', posterUrl: monsterPoster('DOR-MON-A-67000200001'), hpMult: 7.0, atkMult: 1.6, defMult: 1.5, speedMult: 1.1, threat: 100, isBoss: true, sortOrder: 1, weakElements: ['light'] },
  { id: 'DOR-MON-B-0089', name: '鋼鐵巨鉗蟹', rank: 'B', attribute: 'metal', size: 'large', race: '魚貝', posterUrl: monsterPoster('DOR-MON-B-0089'), hpMult: 1.8, atkMult: 1.25, defMult: 1.35, speedMult: 1.15, threat: 40, isBoss: false, sortOrder: 2, weakElements: ['fire'] },
  { id: 'DOR-MON-C-0229', name: '沙塵骷髏騎士', rank: 'C', attribute: 'earth', size: 'medium', race: '不死', posterUrl: monsterPoster('DOR-MON-C-0229'), hpMult: 1.3, atkMult: 1.15, defMult: 1.2, speedMult: 1.05, threat: 30, isBoss: false, sortOrder: 3, weakElements: ['water'] },
  { id: 'DOR-MON-D-0182', name: '灰白獸人', rank: 'D', attribute: 'neutral', size: 'medium', race: '人形', posterUrl: monsterPoster('DOR-MON-D-0182'), hpMult: 0.9, atkMult: 1.0, defMult: 1.0, speedMult: 1.0, threat: 20, isBoss: false, sortOrder: 4, weakElements: [] },
  { id: 'DOR-MON-E-0052', name: '荊棘毒蛾', rank: 'E', attribute: 'wood', size: 'small', race: '昆蟲', posterUrl: monsterPoster('DOR-MON-E-0052'), hpMult: 0.6, atkMult: 0.8, defMult: 0.8, speedMult: 0.9, threat: 10, isBoss: false, sortOrder: 5, weakElements: ['fire'] },
];

/**
 * 前 5 個欄位沿用 sampleBattle.ts 既有的技能（is_default=TRUE）。
 * P5（CONTRACT §5）新增 4 個示範技能，涵蓋 damage/heal/shield 以外的 4 種新 kind 各一個
 * （buff/debuff/passive/special——任務要求「各 kind 至少一個」讓 /dev/dorpg 能預覽這些新機制）：
 *   - war_cry（buff）：自己 atk_pct +20%，持續 8 秒。
 *   - armor_break（debuff）：對單一敵人 def_pct −20%，持續 6 秒。
 *   - fortitude（passive）：不進技能欄（見下面 buildFixtureSample 的 tray 篩選），純資料展示——
 *     正式環境這種技能的數值由後端直接算進玩家 stats，離線 fixture 不做這層轉換（範圍見任務回報）。
 *   - merchants_intuition（special）：implemented=false，示範「尚未實裝」的 UI 呈現。
 * 這 4 個是全新示範技能，不對應任何既有 sampleBattle.ts 技能，數值落在既有技能同一個量級
 * （mpCost 10–15、cooldown 6–12 秒、cast 300–500ms，跟現有 5 個技能一致）。
 */
export const RPG_SKILLS: SkillRow[] = [
  { id: 'slash', name: '斬擊', iconId: 'icon_skill_slash', kind: 'damage', target: 'enemy', weapon: 'sword', element: 'neutral', mpCost: 5, cooldownMs: 4000, coefficient: 1.6, flat: 20, castMs: 300, isDefault: true, sortOrder: 1 },
  { id: 'fireball', name: '火球', iconId: 'icon_skill_fireball', kind: 'damage', target: 'enemy', weapon: 'staff', element: 'fire', mpCost: 25, cooldownMs: 12000, coefficient: 2.4, flat: 60, castMs: 600, isDefault: true, sortOrder: 2 },
  { id: 'heal', name: '治療', iconId: 'icon_skill_heal', kind: 'heal', target: 'ally', weapon: 'staff', element: 'light', mpCost: 20, cooldownMs: 8000, coefficient: 2.0, flat: 80, castMs: 500, isDefault: true, sortOrder: 3 },
  { id: 'ice_lance', name: '冰槍', iconId: 'icon_skill_ice_lance', kind: 'damage', target: 'enemy', weapon: 'staff', element: 'water', mpCost: 15, cooldownMs: 6000, coefficient: 2.0, flat: 30, castMs: 400, isDefault: true, sortOrder: 4 },
  { id: 'shield', name: '護盾', iconId: 'icon_skill_shield', kind: 'shield', target: 'self', weapon: 'staff', element: 'light', mpCost: 15, cooldownMs: 10000, coefficient: 1.5, flat: 60, castMs: 300, isDefault: true, sortOrder: 5 },
  {
    id: 'war_cry', name: '戰吼', iconId: 'icon_skill_slash', kind: 'buff', target: 'self', weapon: 'sword', element: 'neutral',
    mpCost: 15, cooldownMs: 12000, coefficient: 0, flat: 0, castMs: 400, isDefault: true, sortOrder: 6,
    level: 1, maxLevel: 5, displayText: '提升自身物理攻擊力 20%，持續 8 秒。',
    effect: { kind: 'buff', stat: 'atk_pct', value: 20, durationMs: 8000, target: 'self', mpCost: 15 },
  },
  {
    id: 'armor_break', name: '破甲箭', iconId: 'icon_skill_ice_lance', kind: 'debuff', target: 'enemy', weapon: 'bow', element: 'neutral',
    mpCost: 12, cooldownMs: 10000, coefficient: 0, flat: 0, castMs: 350, isDefault: true, sortOrder: 7,
    level: 1, maxLevel: 5, displayText: '降低目標防禦力 20%，持續 6 秒。',
    effect: { kind: 'debuff', stat: 'def_pct', value: -20, durationMs: 6000, target: 'enemy', mpCost: 12 },
  },
  {
    id: 'fortitude', name: '堅毅', iconId: 'icon_skill_shield', kind: 'passive', target: 'self', weapon: 'sword', element: 'neutral',
    mpCost: 0, cooldownMs: 0, coefficient: 0, flat: 0, castMs: 0, isDefault: true, sortOrder: 8,
    level: 1, maxLevel: 5, displayText: '被動提升防禦力 10%（後端已算進角色 stats，不進技能欄）。',
    effect: { kind: 'passive', stat: 'def_pct', value: 10, target: 'self', mpCost: 0 },
  },
  {
    id: 'merchants_intuition', name: '商人的直覺', iconId: 'icon_skill_heal', kind: 'special', target: 'self', weapon: 'sword', element: 'neutral',
    mpCost: 10, cooldownMs: 0, coefficient: 0, flat: 0, castMs: 0, isDefault: true, sortOrder: 9,
    level: 1, maxLevel: 5, displayText: '提升掉落品質（尚未實裝）。', implemented: false,
  },
];

/** amount 由 TUNE 依 BALANCE.md §5 調整（hp_potion 300→150、mp_potion 120→80，與 migration 176
 * 逐項一致）；default_quantity 維持契約 §2 給定值。理由見 migration 176 同一段落註解。 */
export const RPG_ITEMS: ItemRow[] = [
  { id: 'hp_potion', name: '紅藥水', iconId: 'icon_item_hp_potion', kind: 'hp', amount: 150, defaultQuantity: 3, sortOrder: 1 },
  { id: 'mp_potion', name: '藍藥水', iconId: 'icon_item_mp_potion', kind: 'mp', amount: 80, defaultQuantity: 2, sortOrder: 2 },
  { id: 'revive_feather', name: '復甦羽毛', iconId: 'icon_item_revive_feather', kind: 'revive', amount: 50, defaultQuantity: 1, sortOrder: 3 },
];

/**
 * char_xiaojing 是玩家頭像（D4：is_player_portrait=TRUE，本身不列入隊友清單）。
 * P6（CONTRACT §3.2／任務 4「示範隊伍＝小咪＋一位傭兵各帶 2–3 個示範技能」）：skillIds 對到
 * 上面 RPG_SKILLS 的 id，buildFixtureSample 會把它們展開成 PartyActor.skills 餵給隊友 AI（見
 * toCompanionSkills）——只有小咪（heal/shield，示範①②段：治療優先、護盾不重複）與阿光
 * （war_cry/armor_break/slash，示範②③④段：buff 不重複、damage 選 tier 最高、debuff 不重複）
 * 帶技能；小優／阿深維持 []，走⑤普攻 fallback（跟 P1 舊行為相容）。
 */
export const RPG_COMPANIONS: CompanionRow[] = [
  { id: 'char_xiaojing', name: '小井', portraitId: 'char_xiaojing', role: '', weapon: 'sword', levelOffset: 0, hpMult: 1, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: [], isPlayerPortrait: true, sortOrder: 0 },
  { id: 'char_xiaomi', name: '小咪', portraitId: 'char_xiaomi', role: '治療', weapon: 'staff', levelOffset: 0, hpMult: 0.7, mpMult: 1, atkMult: 1, matkMult: 1.2, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: ['heal', 'shield'], isPlayerPortrait: false, sortOrder: 1 },
  { id: 'char_xiaoyou', name: '小優', portraitId: 'char_xiaoyou', role: '游擊', weapon: 'bow', levelOffset: 0, hpMult: 1, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: [], isPlayerPortrait: false, sortOrder: 2 },
  { id: 'char_aguang', name: '阿光', portraitId: 'char_aguang', role: '劍士', weapon: 'sword', levelOffset: 0, hpMult: 1, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: ['war_cry', 'armor_break', 'slash'], isPlayerPortrait: false, sortOrder: 3 },
  { id: 'char_ashen', name: '阿深', portraitId: 'char_ashen', role: '重裝', weapon: 'greatsword', levelOffset: 0, hpMult: 1.3, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1.25, skillIds: [], isPlayerPortrait: false, sortOrder: 4 },
];

// ---------------------------------------------------------------------------
// P7（DORPG_P7 CONTRACT §2/§3、WIRE「戰鬥 bootstrap」）：武器系統的離線示範資料——正式的 18 類型
// ×390 件武器表由另一個 workflow 產生 seed（migration 183），本檔只鏡像三件有代表性的示範武器
// （雙劍／斧／鍊，各自展示一種契約 §3 的機制），供 /dev/dorpg 離線預覽與 verify 腳本切換測試，
// 不是完整的武器內容庫。
// ---------------------------------------------------------------------------

export interface WeaponFixtureRow {
  id: string;
  name: string;
  typeId: string;
  visual: WeaponKind;
  profile: WeaponProfileWire;
}

/** 中性基準之外的欄位維持 0/1，只填每件示範武器真正要展示的機制（見各筆註解）。 */
export const RPG_WEAPON_FIXTURES: Record<string, WeaponFixtureRow> = {
  // 輕騎士．雙劍：CONTRACT §3「裝備後是二刀流，攻擊次數變兩次，攻擊力下降為 60%」。
  demo_dual_sword: {
    id: 'demo_dual_sword',
    name: '風城雙刃‧壹式',
    typeId: 'lk_dual',
    visual: 'sword',
    profile: {
      atk: 40, matk: 0, hits: 2, hitMul: 0.6, extraHitChancePct: 0, intervalPct: 0,
      chargeTimeMul: 1, chargeDmgMul: 1, splashPct: 0, sizeBonus: { small: 0, medium: 0, large: 0 },
      critPct: 0, critDmgPct: 0, elementResistPct: 0, magicSkillPct: 0, element: 'neutral',
    },
  },
  // 重騎士．斧：CONTRACT §3「增加物理攻擊的間隔時間，會造成範圍傷害...對於大體型的怪物會有額外
  // 傷害 5%」——intervalPct 正值＝變慢、splashPct 濺射同排相鄰、sizeBonus.large。
  demo_axe: {
    id: 'demo_axe',
    name: '劍南斷木斧',
    typeId: 'hk_axe',
    visual: 'greatsword',
    profile: {
      atk: 55, matk: 0, hits: 1, hitMul: 1, extraHitChancePct: 0, intervalPct: 25,
      chargeTimeMul: 1, chargeDmgMul: 1, splashPct: 40, sizeBonus: { small: 0, medium: 0, large: 5 },
      critPct: 0, critDmgPct: 0, elementResistPct: 0, magicSkillPct: 0, element: 'neutral',
    },
  },
  // 聖職者．鍊：CONTRACT §3「增加魔法抗性（屬性效果減免）」——element_resist_pct 減免非 neutral
  // 怪物造成的傷害（見 combat.ts applyPartyDamage）；element 給 light 只是示範風格，不影響機制
  // 本身（鍊不主動攻擊，element 欄位在這個範例裡是裝飾用的顯示資訊）。
  demo_chain: {
    id: 'demo_chain',
    name: '福和聖鍊',
    typeId: 'cl_chain',
    visual: 'staff',
    profile: {
      atk: 5, matk: 30, hits: 1, hitMul: 1, extraHitChancePct: 0, intervalPct: 0,
      chargeTimeMul: 1, chargeDmgMul: 1, splashPct: 0, sizeBonus: { small: 0, medium: 0, large: 0 },
      critPct: 0, critDmgPct: 0, elementResistPct: 15, magicSkillPct: 0, element: 'light',
    },
  },
};

// ---------------------------------------------------------------------------
// P8（DORPG_P8 CONTRACT §2/§3、WIRE「戰鬥 bootstrap」）：裝備（防具＋飾品）效果彙總的離線示範
// 資料——正式的 300 件防具＋90 件飾品由另一個 workflow 產生 seed（migration 184），本檔只鏡像一組
// 「多欄位同時非零」的示範組合，供 /dev/dorpg 離線預覽與 verify 腳本切換測試「這幾個效果同時生效」
// 的端到端行為，不是完整的裝備內容庫（跟 RPG_WEAPON_FIXTURES 對武器系統的角色定位一致）。
// ---------------------------------------------------------------------------

/**
 * 示範數值刻意都給有感、但不誇張的量（跟契約 §3 飾品 t3「大致等於原提案值」的量級對齊）：
 * intervalPct=-8（攻擊間隔縮短 8%）、mpCostReducePct=20（技能 MP 消耗打 8 折）、hpRegenPctPer5s／
 * mpRegenPctPer5s=2（每 5 秒回復 2% 上限）、damageTakenPct=-10（少受 10% 傷害）、
 * elementResistPct=10（額外 10% 屬性抗性）——每個欄位在 /dev/dorpg 預覽時都應該看得出效果，不是
 * 為了展示邊界值（clamp 邊界另外在 verify-dorpg-engine.mjs 用手造極端值測試，不靠這份 fixture）。
 */
export const RPG_EQUIPMENT_EFFECTS_FIXTURES: Record<string, EquipmentEffectsWire> = {
  demo_full_set: {
    intervalPct: -8,
    mpCostReducePct: 20,
    hpRegenPctPer5s: 2,
    mpRegenPctPer5s: 2,
    damageTakenPct: -10,
    elementResistPct: 10,
  },
};

/**
 * 六場遭遇。第 1/2/3/6 場編組逐字取自契約 §2；第 4（淡水河口）/第 5（劍南山步道）場契約只給了
 * 「難度＋power_scale＋隻數」，編組與 migration 176 的同一份設計同步（見該檔
 * rpg_encounter_monsters 區塊註解）。
 *
 * powerScale 由 TUNE 依 BALANCE.md 全面下修並實測微調（理由與逐場數值見 migration 176 對應
 * INSERT 前的註解，兩邊逐項一致，不在此重複）；tamsui_dusk/jiannan_trail 的編組也依 BALANCE 的
 * 模擬版本（scratchpad/dorpg_p2/sim_lib.mjs ENCOUNTER_DEFS）校正過一個槽位，理由同上。
 */
export const RPG_ENCOUNTERS: EncounterRow[] = [
  {
    code: 'training_ground', title: '訓練場', subtitle: '入門教學．熟悉操作手感',
    sceneId: 'scene_taipei_stadium', sceneKind: 'normal', difficulty: 1, powerScale: 0.60,
    escapeChance: 0.35, canEscape: true,
    monsterLevel: 10,
    monsters: [
      { slot: 'front_left', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
      { slot: 'front_center', monsterId: 'DOR-MON-E-0052', powerScale: 1 },
      { slot: 'front_right', monsterId: 'DOR-MON-E-0052', powerScale: 1 },
    ],
  },
  {
    code: 'ximen_night', title: '西門町夜巡', subtitle: '夜巡邊界．小怪成群',
    sceneId: 'scene_ximending', sceneKind: 'normal', difficulty: 2, powerScale: 0.73,
    escapeChance: 0.35, canEscape: true,
    monsterLevel: 20,
    monsters: [
      { slot: 'rear_left', monsterId: 'DOR-MON-E-0052', powerScale: 1 },
      { slot: 'front_left', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
      { slot: 'front_center', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
      { slot: 'front_right', monsterId: 'DOR-MON-C-0229', powerScale: 1 },
    ],
  },
  {
    code: 'fuhe_bridge', title: '福和橋下', subtitle: '橋下盤據．小心巨鉗',
    sceneId: 'scene_fuhe_bridge', sceneKind: 'normal', difficulty: 2, powerScale: 0.60,
    escapeChance: 0.35, canEscape: true,
    monsterLevel: 30,
    monsters: [
      { slot: 'rear_right', monsterId: 'DOR-MON-C-0229', powerScale: 1 },
      { slot: 'front_left', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
      { slot: 'front_center', monsterId: 'DOR-MON-B-0089', powerScale: 1 },
      { slot: 'front_right', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
    ],
  },
  {
    code: 'tamsui_dusk', title: '淡水河口', subtitle: '河口起霧．敵勢漸強',
    sceneId: 'scene_tamsui_estuary', sceneKind: 'normal', difficulty: 3, powerScale: 0.55,
    escapeChance: 0.35, canEscape: true,
    monsterLevel: 40,
    monsters: [
      { slot: 'rear_left', monsterId: 'DOR-MON-C-0229', powerScale: 1 },
      { slot: 'rear_right', monsterId: 'DOR-MON-C-0229', powerScale: 1 },
      { slot: 'front_left', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
      { slot: 'front_center', monsterId: 'DOR-MON-B-0089', powerScale: 1 },
      { slot: 'front_right', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
    ],
  },
  {
    code: 'jiannan_trail', title: '劍南山步道', subtitle: '登山惡鬥．狹路難退',
    sceneId: 'scene_jiannan_mountain', sceneKind: 'normal', difficulty: 4, powerScale: 0.52,
    escapeChance: 0.35, canEscape: true,
    monsterLevel: 50,
    monsters: [
      { slot: 'rear_left', monsterId: 'DOR-MON-B-0089', powerScale: 1 },
      { slot: 'rear_right', monsterId: 'DOR-MON-B-0089', powerScale: 1 },
      { slot: 'front_left', monsterId: 'DOR-MON-C-0229', powerScale: 1 },
      { slot: 'front_center', monsterId: 'DOR-MON-C-0229', powerScale: 1 },
      { slot: 'front_right', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
    ],
  },
  {
    code: 'taipei101_boss', title: '台北101首領戰', subtitle: '首領現身．無法逃跑',
    sceneId: 'scene_taipei_101', sceneKind: 'boss', difficulty: 5, powerScale: 0.46,
    escapeChance: 0.0, canEscape: false,
    monsterLevel: 60,
    monsters: [
      { slot: 'rear_left', monsterId: 'DOR-MON-E-0052', powerScale: 1 },
      { slot: 'rear_right', monsterId: 'DOR-MON-A-67000200001', powerScale: 1 },
      { slot: 'front_left', monsterId: 'DOR-MON-D-0182', powerScale: 1 },
      { slot: 'front_center', monsterId: 'DOR-MON-C-0229', powerScale: 1 },
      { slot: 'front_right', monsterId: 'DOR-MON-B-0089', powerScale: 1 },
    ],
  },
];

// ---------------------------------------------------------------------------
// D2 縮放公式（契約 §1）。務必與 services/api/internal/rpg/scaling.go 的 ScaleMonster 逐項對齊
// ——BACKEND 完成後，兩邊對同一組輸入必須算出一模一樣的整數，TUNE 調參時兩邊要一起改。
// 2026-09-14 P2 修正第1輪追加「參考 HP/MP 縮放」（scaleSkillFlat/scaleItemAmount，見下方定義）：
// 原本這裡只縮放怪物數值，玩家自己的治療技能/補品卻是絕對值，兩者不成比例——同一份根因分析見
// review/data.md 缺陷1。
// ---------------------------------------------------------------------------

/**
 * §3.1 config 欄位中，D2 縮放公式實際用得到的子集（其餘約 70 個 RO 素質欄位跟怪物縮放無關，
 * 只有 internal/rpg/config.go 與後台 rpgMeta.ts 需要，不在此複製）。
 */
export interface FixtureScaleConfig {
  battleMobHits: number;
  battleMobDefRatio: number;
  battleEnemyDpsRatio: number;
  battlePlayerMinAtk: number;
  battlePlayerMinHp: number;
  battleEnemyActMinMs: number;
  battleEnemyActMaxMs: number;
  /** 2026-09-14 P2 修正第1輪（data.md 缺陷1，根因修正）：技能 flat／道具 amount 是 DB 存的絕對值，
   *  語意上「參考」哪個玩家 HPMax／MPMax 才算數——不隨玩家等級縮放的話，正式庫 Base Lv 27／
   *  HPMax 919 的擁有者一瓶原本設計補 50%（以 HPMax=300 為參考）的紅藥水只補 16%，體感完全跑掉。
   *  見下面 scaleSkillFlat/scaleItemAmount 與 services/api/internal/rpg/scaling.go 的 ScaleSkill/
   *  ScaleItem（Go/TS 兩邊必須算出同一個整數，逐項對齊）。 */
  battleReferenceHp: number;
  battleReferenceMp: number;
}

/** TUNE 套用 BALANCE.md 建議後的最終值，與 services/api/internal/rpg/config.go DefaultConfig()
 * 逐項一致（battleMobHits 6→56、battleEnemyDpsRatio 0.01→0.2；理由見該檔同名欄位上方註解，
 * 不在此重複）。battleReferenceHp/battleReferenceMp＝契約 SPEC 給的預設 300/100（與最初 BALANCE
 * 假設的「玩家 Base Lv=5／HP=300」剛好同一個錨點，讓既有 seed 的 flat/amount 不必重填）。 */
export const DEFAULT_SCALE_CONFIG: FixtureScaleConfig = {
  battleMobHits: 56,
  battleMobDefRatio: 0.25,
  battleEnemyDpsRatio: 0.2,
  battlePlayerMinAtk: 30,
  battlePlayerMinHp: 300,
  battleEnemyActMinMs: 2500,
  battleEnemyActMaxMs: 4500,
  battleReferenceHp: 300,
  battleReferenceMp: 100,
};

/** 縮放比例上下限：避免極端角色（例如之後 HP 破萬）讓一瓶藥水補到荒謬的量，也避免全新角色
 *  （HPMax 遠低於參考值）被夾到補不了血——0.25～8 是 SPEC 給定的安全帶，跟 Go 端逐項對齊。 */
const REFERENCE_RATIO_MIN = 0.25;
const REFERENCE_RATIO_MAX = 8;

/** ratioHP／ratioMP 共用的 clamp 邏輯（SPEC：`clamp(actual / reference, 0.25, 8)`）。reference 理論上
 *  恆為正數（DEFAULT 300/100，Validate() 會擋 ≤0），`|| 1` 只是防呆，不代表真的會遇到 0。 */
function referenceRatio(actual: number, reference: number): number {
  const raw = actual / (reference || 1);
  return Math.min(REFERENCE_RATIO_MAX, Math.max(REFERENCE_RATIO_MIN, raw));
}

/**
 * SPEC ScaleSkill 的 TS 鏡像：只有 heal/shield 的 flat 隨玩家 HPMax 縮放（回復量的絕對值語意上是
 * 「參考玩家（HPMax=battleReferenceHp）身上補多少」）；coefficient/mpCost/cooldown/castMs 一律不動
 * （coefficient 乘的是 MATK，本來就會隨角色成長）；kind='damage' 的 flat 完全不動——它是加在玩家 ATK
 * 上的固定值，ATK 已經有自己的保底與縮放，重複縮放會算兩次。
 */
function scaleSkillFlat(cfg: FixtureScaleConfig, playerHp: number, skill: SkillRow): number {
  if (skill.kind !== 'heal' && skill.kind !== 'shield') return skill.flat;
  return Math.max(1, Math.round(skill.flat * referenceRatio(playerHp, cfg.battleReferenceHp)));
}

/**
 * SPEC ScaleItem 的 TS 鏡像：hp 道具依玩家 HPMax、mp 道具依玩家 MPMax 縮放；revive 不縮放
 * ——它的 amount 語意本來就是「復活後 HP 佔 hpMax 的百分比」，已經會隨玩家等級自動放大。
 */
function scaleItemAmount(cfg: FixtureScaleConfig, playerHp: number, playerMp: number, item: ItemRow): number {
  if (item.kind === 'hp') return Math.max(1, Math.round(item.amount * referenceRatio(playerHp, cfg.battleReferenceHp)));
  if (item.kind === 'mp') return Math.max(1, Math.round(item.amount * referenceRatio(playerMp, cfg.battleReferenceMp)));
  return item.amount;
}

/**
 * 離線預覽沒有真正的玩家 MP/DEF/等級來源（那些出自登入後 /rpg/me 的 derived 數值）；這三個值
 * 只在 fixture 內部使用、刻意不開放成 opts（契約明定 buildFixtureSample 簽章只給 playerAtk/
 * playerHp）。DEF 選 0 而不是套一個「看起來合理」的估算式，是為了不讓人誤以為這是某種官方
 * 保底規則——契約 §3.1 的保底清單裡本來就沒有 battle_player_min_def 這一項。
 */
const FIXTURE_PLAYER_MP = 100;
const FIXTURE_PLAYER_DEF = 0;
/** 純展示用的預覽等級（只影響怪物血條旁顯示的數字，不進 D2 公式）；正式 Base Lv 由玩家 exp 推導。 */
const FIXTURE_PLAYER_LEVEL = 60;
/** 怪物顯示等級＝玩家 Base Lv（boss +5），契約 §3.2 明講的規則。 */
const BOSS_LEVEL_BONUS = 5;

/**
 * P2（暴擊／Miss／無效攻擊）：離線預覽沒有登入後的角色配點資料，沒辦法像正式環境一樣呼叫
 * internal/rpg Compute 算出真正的 Hit/Flee/CritPct/CritShield（那需要 DEX/AGI/LUK 等配點與
 * Base Lv）——這裡給一組 fixture 專屬的固定值，刻意不是「完全命中／永不暴擊」的中性數字，
 * 才能讓 /dev/dorpg 預覽實際看到三個新機制發生；不代表任何真實玩家的配點結果。
 * 隊友沿用同一份值（SPEC §1 D1：「沒有〔對應倍率〕的話沿用玩家值」——CompanionRow 本來就沒有
 * 這批倍率欄位，且依規則不得新增 migration，即玩家值就是最終值，不是偷懶省略），但 aspd／
 * castReductionPct 例外：見下面 FIXTURE_COMPANION_RATING 的說明。
 *
 * P3（AGI 攻速／DEX 詠唱縮減，審查 dorpg_p3 r5）：aspd/castReductionPct 刻意給高於中性值
 * （aspdReference=150、0）的示範數字，讓 /dev/dorpg 預覽能實際看到「攻擊間隔變快」「詠唱時間
 * 縮短」這兩個新機制發生，跟 hit/flee/critPct/critShield 選非中性值同一份精神——不代表任何
 * 真實玩家的配點結果。
 */
const FIXTURE_PLAYER_RATING: CombatRating = { hit: 92, flee: 8, critPct: 12, critShield: 4, aspd: 165, castReductionPct: 15, critDmgPct: 0 };

/**
 * 隊友評級（審查 dorpg_p3 r5 §D、對齊 services/api/internal/rpg/scaling.go CompanionRating）：
 * hit/flee/critPct/critShield 沿用玩家值（理由見 FIXTURE_PLAYER_RATING 上方註解），但 aspd／
 * castReductionPct 強制覆寫為中性值——後端 CompanionRating 明講「AGI/DEX 效果只限玩家本人，
 * 隊友的攻擊冷卻/施放時間固定走 config 值，不隨玩家配點連動加速，避免隊友 AI 節奏被打亂」，
 * 離線鏡像若讓隊友沿用玩家的示範性 aspd:165/castReductionPct:15，會跟正式環境的實際行為
 * （隊友固定 aspdReference/0）不一致，看起來像「隊友也變快了」的誤導假象。
 */
const FIXTURE_COMPANION_RATING: CombatRating = { ...FIXTURE_PLAYER_RATING, aspd: DEFAULT_BATTLE_CONFIG.aspdReference, castReductionPct: 0 };

/**
 * 怪物命中/暴擊評級（審查 dorpg_p3 r5 §A 修復，對齊 services/api/internal/rpg MonsterRating()）：
 * 命中/迴避改成跟著「玩家等級基線」走（不再是跟等級無關的絕對常數）——
 *   monsterHit  = playerBaseLevel × monsterHitPerLevel  + monsterHitBase
 *   monsterFlee = (playerBaseLevel × monsterFleePerLevel + monsterFleeBase) × speedMult
 * critShield 乘該怪 defMult（同一組倍率也用在下面 scaleMonster 的 D2 縮放公式，語意一致：
 * 越靈活的怪越難打中、越硬的怪越扛得住暴擊）。基準值直接讀 engine 的 DEFAULT_BATTLE_CONFIG
 * ——離線預覽沒有後台 rpg_config 可讀，這是它唯一的資料來源；正式環境改吃 bootstrap 回應的
 * config（見 fromApi.ts configFromBootstrap）。playerBaseLevel 離線預覽沒有真正登入資料可用，
 * 沿用本檔既有的 FIXTURE_PLAYER_LEVEL 常數（跟怪物顯示等級同一個錨點，理由見該常數上方註解，
 * 不另外新增一個常數）。aspd/castReductionPct 給 cfg.aspdReference/0——怪物在正式環境本來就
 * 不吃這兩個欄位（節奏固定走 ActMinMs/ActMaxMs，不受配點影響），純粹填滿 CombatRating 型別，
 * 避免零值造成「這隻怪 aspd=0」的誤解（對齊後端 MonsterRating 同一個理由）。
 *
 * P6（CONTRACT §2）：power 模式的「等級基線」原本恆用 FIXTURE_PLAYER_LEVEL（玩家戰力縮放跟等級
 * 無關，只是拿玩家等級當顯示錨點）；level 模式改用「怪物自己的等級 N」（契約原文：「hit/flee
 * 沿用『等級基線』公式但改用怪物自己的等級 N」）——`level` 參數化後兩種模式共用同一支函式，
 * 呼叫端決定要傳哪個等級（見下方 buildFixtureSample）。
 */
function monsterRating(monster: MonsterRow, level: number = FIXTURE_PLAYER_LEVEL): CombatRating {
  const cfg = DEFAULT_BATTLE_CONFIG;
  const lv = level;
  // 與 Go 的 MonsterRating 一致：夾在 monsterHitMax 之下，避免高等級玩家的 AGI 迴避失效。
  const rawHit = lv * cfg.monsterHitPerLevel + cfg.monsterHitBase;
  return {
    hit: cfg.monsterHitMax > 0 ? Math.min(rawHit, cfg.monsterHitMax) : rawHit,
    flee: (lv * cfg.monsterFleePerLevel + cfg.monsterFleeBase) * monster.speedMult,
    critPct: cfg.monsterCritPct,
    critShield: cfg.monsterCritShieldBase * monster.defMult,
    aspd: cfg.aspdReference,
    castReductionPct: 0,
    critDmgPct: 0,
  };
}

interface ScaledMonsterStats {
  hpMax: number;
  atk: number;
  matk: number;
  def: number;
  mdef: number;
}

/** 契約 §1 D2 公式的 TS 鏡像；atkMultShare＝該怪 atk_mult ÷ 同場全部怪 atk_mult 總和。 */
function scaleMonster(
  cfg: FixtureScaleConfig,
  playerAtk: number,
  playerHp: number,
  playerDef: number,
  monster: MonsterRow,
  encounterScale: number,
  slotScale: number,
  atkMultShare: number,
): ScaledMonsterStats {
  const mobDef = playerAtk * cfg.battleMobDefRatio * monster.defMult * encounterScale;
  const hitDamage = Math.max(1, playerAtk - mobDef);
  const hpMax = Math.max(1, Math.round(hitDamage * cfg.battleMobHits * monster.hpMult * encounterScale * slotScale));
  const actSec = ((cfg.battleEnemyActMinMs + cfg.battleEnemyActMaxMs) / 2000) * monster.speedMult;
  const atk = Math.round(playerDef + playerHp * cfg.battleEnemyDpsRatio * atkMultShare * actSec * encounterScale);
  return {
    hpMax,
    atk,
    matk: atk, // 契約 D2：「mobMatk = mobAtk（P2 敵人不分物魔）」
    def: Math.round(mobDef),
    mdef: Math.round(mobDef), // 契約 D2：「mobMdef = mobDef」
  };
}

// ---------------------------------------------------------------------------
// P6（CONTRACT §2）：怪物等級制（battle_scale_mode="level"）——「資料鏡像」，不移植 Compute。
// BACKEND 用 Go 的 RefPlayerStats(cfg, N) 產生 apps/web/src/lib/dorpg/refPlayerTable.json
// （N=1..99 的 Ref 衍生值，逐位元對齊 Go 端），本檔只負責「讀表＋套契約 §2 給的怪物縮放公式」，
// 不重新推導六圍配點或 Compute() 本身（那條規則只活在 Go 裡，見 CONTRACT.md §2「不移植 Compute；
// 改為資料鏡像」）。
// ---------------------------------------------------------------------------

/**
 * refPlayerTable.json 單列的假設形狀（camelCase，比照本檔其餘 wire/鏡像慣例）：等級 N 的參考玩家
 * （六圍依「輪流 +1 給目前最低且成本負擔得起、未達 cap」演算法配完、無職業、無 passive）套用
 * P5 Compute 後的衍生值子集——只挑怪物縮放公式（見下方 scaleMonsterFromRef）用得到的 9 個欄位，
 * Derived 其餘欄位（Resists／PerfectDodge…）跟怪物縮放無關，不在這裡重複。
 * ⚠️ 本輪撰寫時 BACKEND 尚未產出這份檔案（見任務回報「需要 BACKEND 產生 refPlayerTable.json」），
 * 這裡是依契約文字＋既有 wire 欄位命名慣例的假設；一旦檔案出現，若實際欄位名不同，只需要調整
 * 這個介面與 loadRefPlayerTable() 的存取路徑，不影響呼叫端（buildFixtureSample opts.mode='level'）
 * 的介面。
 */
export interface RefPlayerEntry {
  level: number;
  hpMax: number;
  mpMax: number;
  atk: number;
  matk: number;
  def: number;
  mdef: number;
  hit: number;
  flee: number;
  aspd: number;
}

export type RefPlayerTable = RefPlayerEntry[];

/**
 * 動態載入 refPlayerTable.json——刻意不寫成靜態 `import table from './refPlayerTable.json'`：
 * 這份檔案由 BACKEND 產生（ENGINE 不可寫，見任務分工），本輪撰寫時檔案還不存在，靜態 import
 * 會讓 tsc/`node --experimental-strip-types` 在檔案出現前直接編譯失敗，擋死所有跟這個檔案完全
 * 無關的驗證。改用「specifier 存進變數再動態 import」：TypeScript 對非字面量的 import() 引數
 * 不做模組解析（型別退化成 any），檔案不存在時只有「真的呼叫這支函式」才會在執行期拋錯，
 * 由呼叫端 catch 掉退回 null——一旦 BACKEND 產出檔案，這裡不需要改任何程式碼就會自動讀到。
 * 用 module-level cache 避免重複 import（動態 import 本身有快取，這裡的 cache 只是省一次
 * await/catch 的開銷）。
 *
 * INTEGRATOR 核對（2026-09-18）：Node 24（本專案 verify 腳本的執行環境）的原生 ESM loader
 * 規定動態 import 一個 `.json` 檔必須帶 `with { type: 'json' }` import attribute，否則丟
 * TypeError（`needs an import attribute of "type: json"`）——這個錯誤會被下面的 catch 吞掉，
 * 於是「檔案真的不存在」跟「檔案存在但沒帶 attribute」兩種情況表面上長得一模一樣（都是
 * refPlayerTableCache=null 悄悄退回 power 模式），曾經讓 refPlayerTable.json 產生之後
 * verify #40 仍然一直印 SKIP。Next.js 的 webpack/turbopack 打包器對 `.json` 動態 import
 * 有自己的處理、不吃這條原生 ESM 規則，所以瀏覽器端本來就不受影響；這裡補上 attribute 純粹是
 * 讓 `node --experimental-strip-types` 直接執行 .ts 時也能吃到同一份程式碼。
 */
let refPlayerTableCache: RefPlayerTable | null | undefined;
export async function loadRefPlayerTable(): Promise<RefPlayerTable | null> {
  if (refPlayerTableCache !== undefined) return refPlayerTableCache;
  const specifier = './refPlayerTable.json';
  try {
    const mod: unknown = await import(specifier, { with: { type: 'json' } });
    const table = (mod as { default?: unknown }).default ?? mod;
    refPlayerTableCache = Array.isArray(table) ? (table as RefPlayerTable) : null;
  } catch {
    refPlayerTableCache = null; // 檔案還不存在，或格式不是預期的陣列——呼叫端退回 power 模式。
  }
  return refPlayerTableCache;
}

/** 從表中找出指定等級那一列；找不到就丟錯——契約 §2 講明 N=1..99 全部有值，缺列代表表本身
 *  沒產完整，讓呼叫端及早發現而不是悄悄用到 undefined。 */
export function refPlayerAt(table: RefPlayerTable, level: number): RefPlayerEntry {
  const row = table.find((r) => r.level === level);
  if (!row) throw new Error(`dorpg fixture: refPlayerTable.json 缺少 level=${level} 這一列`);
  return row;
}

/** 契約 §2 給的四個新 config 欄位（怪物 HP/ATK/DEF/MDEF 相對 RefPlayer(N) 的比例）。 */
export interface LevelScaleConfig {
  battleLvlHpRatio: number;
  battleLvlAtkRatio: number;
  battleLvlDefRatio: number;
  battleLvlMdefRatio: number;
}

/**
 * SIM 校準（2026-09-18，逐位元對齊 services/api/internal/rpg/config.go DefaultConfig() 同一組
 * 數字，理由見該檔欄位上方註解——不在這裡重複，改一邊要記得改另一邊）：真引擎模擬跑六場
 * monster_level 10..60（Lv27 輕騎士玩家＋小咪／小咪+阿深）發現 1.0/1.0/1.0/1.0 雖然單調，但
 * Lv10–40 全部 100% 勝率、Lv60 首領戰主要是 timeout（雙方打不死對方）而非乾脆的 defeat。調整
 * HP→0.8（同比例壓低怪物總血量，讓高等級戰鬥能在時限內分出勝負）、ATK→1.25（提高威脅，把
 * 「單一 companion 隊伍在 Lv50 幾乎必勝」拉近五五波、Lv60 從 timeout 轉成乾脆的 defeat）；
 * DEF/MDEF 維持 1.0（見 Go 端註解：問題出在血量總量與時限的關係，不是打不動）。詳細六場勝率／
 * 時長表見 scratchpad/dorpg_p6/sim/RESULT.md。
 */
export const DEFAULT_LEVEL_SCALE_CONFIG: LevelScaleConfig = {
  battleLvlHpRatio: 0.8,
  battleLvlAtkRatio: 1.25,
  battleLvlDefRatio: 1.0,
  battleLvlMdefRatio: 1.0,
};

/**
 * 契約 §2 level 模式怪物公式的 TS 鏡像：
 *   hp   = floor(Ref.HPMax × hp_mult × battle_lvl_hp_ratio  × power_scale × slotScale)
 *   atk  = floor(Ref.ATK   × atk_mult × battle_lvl_atk_ratio × power_scale)
 *   def  = floor(Ref.DEF   × def_mult × battle_lvl_def_ratio × power_scale)
 *   mdef = floor(Ref.MDEF  × mdef_mult × battle_lvl_mdef_ratio × power_scale)
 *   matk（怪物施法用）= Ref.MATK × atk_mult
 * 決策（契約 §3.1 沒有另外定義 monster.mdefMult 欄位，migration 181 清單也沒有新增這一欄）：
 * 「mdef_mult」沿用 monster.defMult——跟舊版 power 模式 scaleMonster()「mobMdef = mobDef」是
 * 同一個precedent（同一組倍率同時決定 DEF 與 MDEF），不是漏看契約，是刻意延續既有資料模型
 * （見任務回報，若 BACKEND 之後真的加了獨立的 mdef_mult 欄位，這裡要跟著改）。matk 契約原文
 * 沒有寫 floor，但為了維持 ActorStats 全欄位皆整數的既有慣例（跟 hpMax/atk/def/mdef 一致），
 * 這裡仍套用 floorInt——不在 CONTRACT §1 的「hp/mp 整數不變式」清單內，純粹是額外的一致性選擇，
 * 已在此註解與任務回報中列為文件化的決策。slotScale 沿用既有 power 模式的前後排邏輯（呼叫端
 * 傳入，跟 scaleMonster() 的 slotScale 同一個概念、同一個數字來源）。
 */
function scaleMonsterFromRef(
  ref: RefPlayerEntry,
  monster: MonsterRow,
  levelCfg: LevelScaleConfig,
  powerScale: number,
  slotScale: number,
): ScaledMonsterStats {
  const hpMax = Math.max(1, Math.floor(ref.hpMax * monster.hpMult * levelCfg.battleLvlHpRatio * powerScale * slotScale));
  const atk = Math.floor(ref.atk * monster.atkMult * levelCfg.battleLvlAtkRatio * powerScale);
  const def = Math.floor(ref.def * monster.defMult * levelCfg.battleLvlDefRatio * powerScale);
  const mdef = Math.floor(ref.mdef * monster.defMult * levelCfg.battleLvlMdefRatio * powerScale);
  const matk = Math.floor(ref.matk * monster.atkMult); // 見上方註解：契約沒有明講 floor，這裡是文件化的一致性決策。
  return { hpMax, atk, matk, def, mdef };
}

/** 怪物自己等級 N 的命中/迴避評級（level 模式）：直接重用 monsterRating()，把 level 換成 N
 *  （見 monsterRating 型別註解「level 模式改用怪物自己的等級 N」）。 */
function monsterRatingAtLevel(monster: MonsterRow, level: number): CombatRating {
  return monsterRating(monster, level);
}

// ---------------------------------------------------------------------------
// buildFixtureSample：離線組一份 BattleSample + 遭遇摘要，供 /dev/dorpg 與自動化測試使用。
// ---------------------------------------------------------------------------

export interface FixtureEncounterMeta {
  code: string;
  title: string;
  subtitle: string;
  sceneKind: 'normal' | 'boss';
  difficulty: number;
  canEscape: boolean;
}

export interface FixtureBundle {
  sample: BattleSample;
  encounter: FixtureEncounterMeta;
}

/** 敵人固定站位序（跟 engine/formulas.ts 的 SLOT_ORDER 同一份規則，逐字比照，不 import 凍結檔）。 */
const SLOT_ORDER: EnemySlotId[] = ['rear_left', 'rear_right', 'front_left', 'front_center', 'front_right'];

function monsterById(id: string): MonsterRow {
  const row = RPG_MONSTERS.find((m) => m.id === id);
  if (!row) throw new Error(`dorpg fixture: unknown monster id "${id}"`);
  return row;
}

function skillRowById(id: string): SkillRow {
  const row = RPG_SKILLS.find((s) => s.id === id);
  if (!row) throw new Error(`dorpg fixture: unknown skill id "${id}"`);
  return row;
}

/** P6（CONTRACT §3.2／WIRE「member.skills…不含 passive、只含 implemented=true」）：把
 *  CompanionRow.skillIds 展開成隊友 AI 可用的技能陣列——跟玩家技能欄的 toSkill() 共用同一份
 *  轉換，只是多一層 kind/implemented 篩選（玩家技能欄反而是在 buildFixtureSample 主體另外篩，
 *  兩處篩選條件不同，故不合併成一支共用函式）。 */
function toCompanionSkills(c: CompanionRow): Skill[] {
  return c.skillIds
    .map(skillRowById)
    .filter((row) => row.kind !== 'passive' && row.implemented !== false)
    .map(toSkill);
}

function sceneById(id: string): SceneRow {
  const row = RPG_SCENES.find((s) => s.id === id);
  if (!row) throw new Error(`dorpg fixture: unknown scene id "${id}"`);
  return row;
}

function toSkill(row: SkillRow): Skill {
  return {
    id: row.id,
    name: row.name,
    iconUrl: kitAsset(row.iconId),
    cooldownMs: row.cooldownMs,
    kind: row.kind,
    target: row.target,
    mpCost: row.mpCost,
    coefficient: row.coefficient,
    flat: row.flat,
    element: row.element,
    weapon: row.weapon,
    castMs: row.castMs,
    hits: row.hits,
    dmgType: row.dmgType,
    level: row.level,
    maxLevel: row.maxLevel,
    displayText: row.displayText,
    implemented: row.implemented,
    effect: row.effect,
  };
}

/**
 * 依契約 §1 D2／§2 與上面的 seed 鏡像，離線組一份 BattleSample（給 /dev/dorpg 沒有 API/DB 時用）。
 * opts.playerAtk/playerHp 只影響 power 模式（契約明定簽章）；DEF/MP/等級一律用內部固定值，不接受
 * 外部覆寫——理由見 FIXTURE_PLAYER_* 常數上方註解。
 * P6：新增 opts.mode（預設 'power'，向下相容既有呼叫端——contract §2「保留 power 模式分支供
 * 對照」）與 opts.refTable（level 模式要用的 RefPlayer 表，呼叫端自行 `await loadRefPlayerTable()`
 * 後傳入；buildFixtureSample 本身維持同步函式簽章，不強迫既有呼叫端改成 async）。mode='level'
 * 但沒有給 refTable（或表載入失敗回傳 null）時安全退回 power 模式並印一行 console.warn——離線
 * 預覽在 refPlayerTable.json 出現前仍然可以正常運作，不會整頁掛掉。
 */
export function buildFixtureSample(
  code: string,
  opts?: {
    playerAtk?: number;
    playerHp?: number;
    mode?: 'power' | 'level';
    refTable?: RefPlayerTable | null;
    /** P7：離線預覽/測試切換玩家武器——key 對到 RPG_WEAPON_FIXTURES，缺省/null＝空手（中性）。 */
    weaponId?: keyof typeof RPG_WEAPON_FIXTURES | null;
    /** P8：離線預覽/測試切換玩家裝備效果——key 對到 RPG_EQUIPMENT_EFFECTS_FIXTURES，缺省/null＝
     *  空裝（中性，PartyMember.equipmentEffects 維持 undefined，由 engine 端 fallback 成
     *  NEUTRAL_EQUIPMENT_EFFECTS，見 engine/index.ts toPartyActor）。 */
    equipmentEffectsId?: keyof typeof RPG_EQUIPMENT_EFFECTS_FIXTURES | null;
  },
): FixtureBundle {
  const encounter = RPG_ENCOUNTERS.find((e) => e.code === code);
  if (!encounter) throw new Error(`dorpg fixture: unknown encounter code "${code}"`);
  const scene = sceneById(encounter.sceneId);
  const cfg = DEFAULT_SCALE_CONFIG;

  // 保底套用在「進公式之前」（契約 §3.2：p.Atk = max(p.Atk, cfg.min)），即使呼叫端沒傳值也一樣。
  const playerAtk = Math.max(opts?.playerAtk ?? 0, cfg.battlePlayerMinAtk);
  const playerHp = Math.max(opts?.playerHp ?? 0, cfg.battlePlayerMinHp);
  const playerDef = FIXTURE_PLAYER_DEF;

  const encounterMonsters = encounter.monsters.map((em) => ({ ...em, monster: monsterById(em.monsterId) }));
  // 理論上 atkMult 恆為正數（DEFAULT 1、草案最低 0.8），這裡的 || 1 只是防呆，不代表真的會遇到 0。
  const atkMultSum = encounterMonsters.reduce((sum, em) => sum + em.monster.atkMult, 0) || 1;

  const requestedMode = opts?.mode ?? 'power';
  const refTable = requestedMode === 'level' ? (opts?.refTable ?? null) : null;
  if (requestedMode === 'level' && !refTable) {
    // eslint-disable-next-line no-console -- 離線預覽/驗證腳本需要看到這個訊號，不是靜默降級。
    console.warn(`dorpg fixture: mode='level' 但沒有可用的 refPlayerTable（呼叫端未傳 refTable，或 refPlayerTable.json 尚未由 BACKEND 產生）——退回 power 模式`);
  }
  const effectiveMode: 'power' | 'level' = refTable ? 'level' : 'power';

  const enemies: Enemy[] = encounterMonsters.map((em) => {
    const share = em.monster.atkMult / atkMultSum;
    if (effectiveMode === 'level' && refTable) {
      // P6（CONTRACT §2）：level 模式怪物等級恆為 encounter.monsterLevel（六場固定 10/20/30/40/50/60，
      // 契約沒有另外定義 boss 加成——跟 power 模式的 BOSS_LEVEL_BONUS 是兩套規則，不套用在這裡）。
      const ref = refPlayerAt(refTable, encounter.monsterLevel);
      const scaled = scaleMonsterFromRef(ref, em.monster, DEFAULT_LEVEL_SCALE_CONFIG, encounter.powerScale, em.powerScale);
      return {
        id: `enemy_${em.slot}`,
        name: em.monster.name,
        level: encounter.monsterLevel,
        hp: scaled.hpMax,
        hpMax: scaled.hpMax,
        slot: em.slot,
        imageUrl: em.monster.posterUrl,
        rank: em.monster.rank,
        attribute: em.monster.attribute,
        size: em.monster.size,
        race: em.monster.race,
        stats: { hpMax: scaled.hpMax, mpMax: 0, atk: scaled.atk, matk: scaled.matk, def: scaled.def, mdef: scaled.mdef },
        threatPriority: em.monster.threat,
        canEscape: encounter.canEscape,
        rating: monsterRatingAtLevel(em.monster, encounter.monsterLevel),
        weakElements: em.monster.weakElements,
      };
    }
    const scaled = scaleMonster(cfg, playerAtk, playerHp, playerDef, em.monster, encounter.powerScale, em.powerScale, share);
    const level = FIXTURE_PLAYER_LEVEL + (em.monster.isBoss ? BOSS_LEVEL_BONUS : 0);
    return {
      // 同場同槽位唯一即可（PK 是 encounter_id+slot）；不能拿 monsterId 當實例鍵，
      // 因為同一隻怪在同一場可能出現在不只一個槽位（見 ximen_night 的兩隻 D）。
      id: `enemy_${em.slot}`,
      name: em.monster.name,
      level,
      hp: scaled.hpMax,
      hpMax: scaled.hpMax,
      slot: em.slot,
      imageUrl: em.monster.posterUrl,
      rank: em.monster.rank,
      attribute: em.monster.attribute,
      size: em.monster.size,
      race: em.monster.race,
      stats: { hpMax: scaled.hpMax, mpMax: 0, atk: scaled.atk, matk: scaled.matk, def: scaled.def, mdef: scaled.mdef },
      threatPriority: em.monster.threat,
      // 2026-09-14 P2 修正第1輪 審查1（CONFIRMED）：這裡漏掉這個欄位，會讓 engine 的
      // hasUnescapableEnemy（見 engine/index.ts）永遠判定「可以逃跑」——BOSS 場「無法逃跑」規則
      // 在離線預覽／verify_p2.mjs 這條路徑完全沒被套用，只有真打 API 才生效。逐場的值就是
      // encounter.canEscape（同一場所有敵人共用，非逐怪欄位），對齊 battle.go 的 wireEnemy.CanEscape。
      canEscape: encounter.canEscape,
      rating: monsterRating(em.monster),
      // P5（CONTRACT §6）：見 MonsterRow.weakElements 型別註解——離線示範值，非權威資料。
      weakElements: em.monster.weakElements,
    };
  });

  // 初始鎖定目標：存活敵人中最高 threatPriority、同序取槽位順序最小者（跟 engine/formulas.ts
  // 的 selectAliveByThreat 同一份規則）。這裡只是資料組裝、還沒有 EnemyActor 可用，重新寫這 5 行
  // 比多繞一層轉換清楚，且不必 import 凍結中的 engine 檔案。
  const initialTargetId =
    [...enemies].sort((a, b) => {
      const pa = a.threatPriority ?? 0;
      const pb = b.threatPriority ?? 0;
      if (pb !== pa) return pb - pa;
      return SLOT_ORDER.indexOf(a.slot) - SLOT_ORDER.indexOf(b.slot);
    })[0]?.id ?? '';

  const portraitRow = RPG_COMPANIONS.find((c) => c.isPlayerPortrait);
  if (!portraitRow) throw new Error('dorpg fixture: no companion row has isPlayerPortrait=true');

  const player: PartyMember = {
    id: portraitRow.id,
    // 離線預覽用佔位名；正式顯示名一律 COALESCE(users.name, handle)，由後端組裝（全站顯示名規則）。
    name: '玩家',
    level: FIXTURE_PLAYER_LEVEL,
    hp: playerHp,
    hpMax: playerHp,
    mp: FIXTURE_PLAYER_MP,
    mpMax: FIXTURE_PLAYER_MP,
    portraitUrl: charPortrait(portraitRow.portraitId, 256),
    rating: FIXTURE_PLAYER_RATING,
    // P7：離線預覽可切換玩家武器（見 opts.weaponId 型別註解）；缺省/未知 id 一律空手。
    equippedWeapon: opts?.weaponId ? (RPG_WEAPON_FIXTURES[opts.weaponId] as EquippedWeaponWire | undefined) ?? null : null,
    // P8：離線預覽可切換玩家裝備效果（見 opts.equipmentEffectsId 型別註解）；缺省/未知 id 維持
    // undefined（不是塞一份全零物件——跟 equippedWeapon 缺省給 null 是同一種「沒有指定就不裝」的
    // 表達方式，由 engine 端各自的中性 fallback 常數接手）。
    equipmentEffects: opts?.equipmentEffectsId ? RPG_EQUIPMENT_EFFECTS_FIXTURES[opts.equipmentEffectsId] : undefined,
  };

  const companions: PartyMember[] = RPG_COMPANIONS.filter((c) => !c.isPlayerPortrait)
    .sort((a, b) => a.sortOrder - b.sortOrder)
    .slice(0, 4)
    .map((c) => {
      const hpMax = Math.round(playerHp * c.hpMult);
      const mpMax = Math.round(FIXTURE_PLAYER_MP * c.mpMult);
      return {
        id: c.id,
        name: c.name,
        level: FIXTURE_PLAYER_LEVEL + c.levelOffset,
        hp: hpMax,
        hpMax,
        mp: mpMax,
        mpMax,
        portraitUrl: charPortrait(c.portraitId, 256),
        stats: {
          hpMax,
          mpMax,
          atk: Math.round(playerAtk * c.atkMult),
          // 簡化假設 playerMatk=playerAtk（契約只開放 opts.playerAtk，沒有獨立的 playerMatk）。
          matk: Math.round(playerAtk * c.matkMult),
          def: Math.round(playerDef * c.defMult),
          mdef: Math.round(playerDef * c.mdefMult),
        },
        weapon: c.weapon,
        // SPEC §1 D1：沒有對應倍率欄位就沿用玩家值，但 aspd/castReductionPct 固定中性值
        // （見 FIXTURE_COMPANION_RATING 上方註解，對齊後端 CompanionRating）。
        rating: FIXTURE_COMPANION_RATING,
        // P6（CONTRACT §3.2／任務 4）：見 RPG_COMPANIONS 上方註解與 toCompanionSkills——只有
        // 小咪／阿光帶示範技能，其餘維持 []（普攻 fallback）。presetName 純展示，沒有技能的
        // 傭兵不掛名稱（沒有腳本可言）。
        skills: toCompanionSkills(c),
        presetName: c.skillIds.length > 0 ? `${c.name} 示範腳本` : undefined,
      };
    });

  // 2026-09-14 P2 修正第1輪：flat/amount 套參考 HP/MP 縮放（見上面 scaleSkillFlat/scaleItemAmount）
  // ——playerHp 已經套過 battlePlayerMinHp 保底（上面那行），跟 Go 端「保底先套用再進縮放公式」的
  // 順序一致。
  // P5（CONTRACT §5：「passive 不出現在技能欄」）：即使 isDefault=true 也排除 passive——它的
  // 效果理論上由後端直接算進玩家 stats，技能欄本身不該有這一格可按。技能欄容量 8→SKILL_SLOTS（10）。
  const skills: (Skill | null)[] = [...RPG_SKILLS]
    .filter((s) => s.isDefault && s.kind !== 'passive')
    .sort((a, b) => a.sortOrder - b.sortOrder)
    .map((s) => toSkill({ ...s, flat: scaleSkillFlat(cfg, playerHp, s) }));
  while (skills.length < SKILL_SLOTS) skills.push(null);

  const items: Item[] = RPG_ITEMS.filter((i) => i.defaultQuantity > 0)
    .sort((a, b) => a.sortOrder - b.sortOrder)
    .map((i) => ({
      id: i.id,
      name: i.name,
      iconUrl: kitAsset(i.iconId),
      quantity: i.defaultQuantity,
      kind: i.kind,
      amount: scaleItemAmount(cfg, playerHp, FIXTURE_PLAYER_MP, i),
    }));

  const sceneForSample: Scene = { id: scene.id, name: scene.name, imageUrl: scene.imageUrl, slots: scene.slots };

  const sample: BattleSample = {
    party: [player, ...companions],
    enemies,
    scene: sceneForSample,
    skills,
    items,
    initialTargetId,
    sceneKind: encounter.sceneKind,
    escapeChance: encounter.escapeChance,
  };

  return {
    sample,
    encounter: {
      code: encounter.code,
      title: encounter.title,
      subtitle: encounter.subtitle,
      sceneKind: encounter.sceneKind,
      difficulty: encounter.difficulty,
      canEscape: encounter.canEscape,
    },
  };
}
