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
import type { BattleSample, CombatRating, ElementKind, Enemy, EnemySlotId, Item, PartyMember, Scene, SceneSlot, Skill, WeaponKind } from './types';
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
  /** 中文災害屬性（闇/金/土/無/木…），逐字取自內容包 monster.json，不是 ElementKind enum
   *  ——見 migration 176 檔頭「資料來源」段落的理由。 */
  attribute: string;
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

/** id/name/rank/attribute/size/race 逐字取自各 monster.json；倍率為契約 §2 給的草案值。 */
export const RPG_MONSTERS: MonsterRow[] = [
  { id: 'DOR-MON-A-67000200001', name: '幽暗食人花首領', rank: 'A', attribute: '闇', size: '大型', race: '植物', posterUrl: monsterPoster('DOR-MON-A-67000200001'), hpMult: 7.0, atkMult: 1.6, defMult: 1.5, speedMult: 1.1, threat: 100, isBoss: true, sortOrder: 1 },
  { id: 'DOR-MON-B-0089', name: '鋼鐵巨鉗蟹', rank: 'B', attribute: '金', size: '大型', race: '魚貝', posterUrl: monsterPoster('DOR-MON-B-0089'), hpMult: 1.8, atkMult: 1.25, defMult: 1.35, speedMult: 1.15, threat: 40, isBoss: false, sortOrder: 2 },
  { id: 'DOR-MON-C-0229', name: '沙塵骷髏騎士', rank: 'C', attribute: '土', size: '中型', race: '不死', posterUrl: monsterPoster('DOR-MON-C-0229'), hpMult: 1.3, atkMult: 1.15, defMult: 1.2, speedMult: 1.05, threat: 30, isBoss: false, sortOrder: 3 },
  { id: 'DOR-MON-D-0182', name: '灰白獸人', rank: 'D', attribute: '無', size: '中型', race: '人形', posterUrl: monsterPoster('DOR-MON-D-0182'), hpMult: 0.9, atkMult: 1.0, defMult: 1.0, speedMult: 1.0, threat: 20, isBoss: false, sortOrder: 4 },
  { id: 'DOR-MON-E-0052', name: '荊棘毒蛾', rank: 'E', attribute: '木', size: '小型', race: '昆蟲', posterUrl: monsterPoster('DOR-MON-E-0052'), hpMult: 0.6, atkMult: 0.8, defMult: 0.8, speedMult: 0.9, threat: 10, isBoss: false, sortOrder: 5 },
];

/** 全部欄位沿用 sampleBattle.ts 既有的 5 個技能（is_default=TRUE）。 */
export const RPG_SKILLS: SkillRow[] = [
  { id: 'slash', name: '斬擊', iconId: 'icon_skill_slash', kind: 'damage', target: 'enemy', weapon: 'sword', element: 'neutral', mpCost: 5, cooldownMs: 4000, coefficient: 1.6, flat: 20, castMs: 300, isDefault: true, sortOrder: 1 },
  { id: 'fireball', name: '火球', iconId: 'icon_skill_fireball', kind: 'damage', target: 'enemy', weapon: 'staff', element: 'fire', mpCost: 25, cooldownMs: 12000, coefficient: 2.4, flat: 60, castMs: 600, isDefault: true, sortOrder: 2 },
  { id: 'heal', name: '治療', iconId: 'icon_skill_heal', kind: 'heal', target: 'ally', weapon: 'staff', element: 'light', mpCost: 20, cooldownMs: 8000, coefficient: 2.0, flat: 80, castMs: 500, isDefault: true, sortOrder: 3 },
  { id: 'ice_lance', name: '冰槍', iconId: 'icon_skill_ice_lance', kind: 'damage', target: 'enemy', weapon: 'staff', element: 'water', mpCost: 15, cooldownMs: 6000, coefficient: 2.0, flat: 30, castMs: 400, isDefault: true, sortOrder: 4 },
  { id: 'shield', name: '護盾', iconId: 'icon_skill_shield', kind: 'shield', target: 'self', weapon: 'staff', element: 'light', mpCost: 15, cooldownMs: 10000, coefficient: 1.5, flat: 60, castMs: 300, isDefault: true, sortOrder: 5 },
];

/** amount 由 TUNE 依 BALANCE.md §5 調整（hp_potion 300→150、mp_potion 120→80，與 migration 176
 * 逐項一致）；default_quantity 維持契約 §2 給定值。理由見 migration 176 同一段落註解。 */
export const RPG_ITEMS: ItemRow[] = [
  { id: 'hp_potion', name: '紅藥水', iconId: 'icon_item_hp_potion', kind: 'hp', amount: 150, defaultQuantity: 3, sortOrder: 1 },
  { id: 'mp_potion', name: '藍藥水', iconId: 'icon_item_mp_potion', kind: 'mp', amount: 80, defaultQuantity: 2, sortOrder: 2 },
  { id: 'revive_feather', name: '復甦羽毛', iconId: 'icon_item_revive_feather', kind: 'revive', amount: 50, defaultQuantity: 1, sortOrder: 3 },
];

/** char_xiaojing 是玩家頭像（D4：is_player_portrait=TRUE，本身不列入隊友清單）。 */
export const RPG_COMPANIONS: CompanionRow[] = [
  { id: 'char_xiaojing', name: '小井', portraitId: 'char_xiaojing', role: '', weapon: 'sword', levelOffset: 0, hpMult: 1, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: [], isPlayerPortrait: true, sortOrder: 0 },
  { id: 'char_xiaomi', name: '小咪', portraitId: 'char_xiaomi', role: '治療', weapon: 'staff', levelOffset: 0, hpMult: 0.7, mpMult: 1, atkMult: 1, matkMult: 1.2, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: ['heal'], isPlayerPortrait: false, sortOrder: 1 },
  { id: 'char_xiaoyou', name: '小優', portraitId: 'char_xiaoyou', role: '游擊', weapon: 'bow', levelOffset: 0, hpMult: 1, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: [], isPlayerPortrait: false, sortOrder: 2 },
  { id: 'char_aguang', name: '阿光', portraitId: 'char_aguang', role: '劍士', weapon: 'sword', levelOffset: 0, hpMult: 1, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1, skillIds: [], isPlayerPortrait: false, sortOrder: 3 },
  { id: 'char_ashen', name: '阿深', portraitId: 'char_ashen', role: '重裝', weapon: 'greatsword', levelOffset: 0, hpMult: 1.3, mpMult: 1, atkMult: 1, matkMult: 1, defMult: 1, mdefMult: 1, actIntervalMult: 1.25, skillIds: [], isPlayerPortrait: false, sortOrder: 4 },
];

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
const FIXTURE_PLAYER_RATING: CombatRating = { hit: 92, flee: 8, critPct: 12, critShield: 4, aspd: 165, castReductionPct: 15 };

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
 */
function monsterRating(monster: MonsterRow): CombatRating {
  const cfg = DEFAULT_BATTLE_CONFIG;
  const lv = FIXTURE_PLAYER_LEVEL;
  // 與 Go 的 MonsterRating 一致：夾在 monsterHitMax 之下，避免高等級玩家的 AGI 迴避失效。
  const rawHit = lv * cfg.monsterHitPerLevel + cfg.monsterHitBase;
  return {
    hit: cfg.monsterHitMax > 0 ? Math.min(rawHit, cfg.monsterHitMax) : rawHit,
    flee: (lv * cfg.monsterFleePerLevel + cfg.monsterFleeBase) * monster.speedMult,
    critPct: cfg.monsterCritPct,
    critShield: cfg.monsterCritShieldBase * monster.defMult,
    aspd: cfg.aspdReference,
    castReductionPct: 0,
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
  };
}

/**
 * 依契約 §1 D2 與上面的 seed 鏡像，離線組一份 BattleSample（給 /dev/dorpg 沒有 API/DB 時用）。
 * opts 只開放 playerAtk/playerHp（契約明定簽章）；DEF/MP/等級一律用內部固定值，不接受外部覆寫
 * ——理由見 FIXTURE_PLAYER_* 常數上方註解。
 */
export function buildFixtureSample(code: string, opts?: { playerAtk?: number; playerHp?: number }): FixtureBundle {
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

  const enemies: Enemy[] = encounterMonsters.map((em) => {
    const share = em.monster.atkMult / atkMultSum;
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
      };
    });

  // 2026-09-14 P2 修正第1輪：flat/amount 套參考 HP/MP 縮放（見上面 scaleSkillFlat/scaleItemAmount）
  // ——playerHp 已經套過 battlePlayerMinHp 保底（上面那行），跟 Go 端「保底先套用再進縮放公式」的
  // 順序一致。
  const skills: (Skill | null)[] = [...RPG_SKILLS]
    .filter((s) => s.isDefault)
    .sort((a, b) => a.sortOrder - b.sortOrder)
    .map((s) => toSkill({ ...s, flat: scaleSkillFlat(cfg, playerHp, s) }));
  while (skills.length < 8) skills.push(null);

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
