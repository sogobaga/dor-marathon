// DORPG P0 靜態戰鬥畫面的範例資料。數值來源：
// - 隊伍：07_DORPG_UI_Kit_v1/sample-data.json（頭像改接 08 content pack 的 char_* 肖像）
// - 敵人：08_DORPG_Content_Pack_v1/examples/encounter-demo.json（站位／等級）＋各 monster.json（名稱／災害級／屬性／體型／種族）
// - 場景：08_DORPG_Content_Pack_v1/assets/scenes/scene_taipei_101/scene.json（monsterSlots）
// - 道具數量：kit preview.js 的 itemQty=[3,2,1]（sample-data.json 本身沒有數量欄位）
// 這裡只是介面展示資料，不是戰鬥權威；正式接 API 時整份換掉即可。
import type { BattleSample, Enemy, PartyMember, Scene, Skill, Item } from '@/lib/dorpg/types';
import { charPortrait, kitAsset, monsterPoster, sceneImage } from '@/lib/dorpg/assets';

const party: PartyMember[] = [
  { id: 'char_xiaojing', name: '小井', level: 58, hp: 780, hpMax: 820, mp: 88, mpMax: 160, portraitUrl: charPortrait('char_xiaojing', 256) },
  // 小咪 40/300 = 13.3% → 觸發瀕死（橘 HP＋紅框脈動）的示範
  { id: 'char_xiaomi', name: '小咪', level: 56, hp: 40, hpMax: 300, mp: 260, mpMax: 260, portraitUrl: charPortrait('char_xiaomi', 256) },
  { id: 'char_xiaoyou', name: '小優', level: 55, hp: 510, hpMax: 550, mp: 230, mpMax: 260, portraitUrl: charPortrait('char_xiaoyou', 256) },
  { id: 'char_aguang', name: '阿光', level: 57, hp: 620, hpMax: 660, mp: 140, mpMax: 320, portraitUrl: charPortrait('char_aguang', 256) },
  { id: 'char_ashen', name: '阿深', level: 56, hp: 900, hpMax: 920, mp: 75, mpMax: 140, portraitUrl: charPortrait('char_ashen', 256) },
];

// id 採 encounter-demo.json 的 instanceId（同一隻怪可能多次出場，不能拿 monsterId 當實例鍵）。
const enemies: Enemy[] = [
  { id: 'enemy_1', name: '幽暗食人花首領', level: 61, hp: 10000, hpMax: 10000, slot: 'rear_right', imageUrl: monsterPoster('DOR-MON-A-67000200001'), rank: 'A', attribute: '闇', size: '大型', race: '植物' },
  { id: 'enemy_2', name: '鋼鐵巨鉗蟹', level: 52, hp: 4400, hpMax: 4400, slot: 'front_center', imageUrl: monsterPoster('DOR-MON-B-0089'), rank: 'B', attribute: '金', size: '大型', race: '魚貝' },
  { id: 'enemy_3', name: '沙塵骷髏騎士', level: 43, hp: 2100, hpMax: 2100, slot: 'front_left', imageUrl: monsterPoster('DOR-MON-C-0229'), rank: 'C', attribute: '土', size: '中型', race: '不死' },
  { id: 'enemy_4', name: '灰白獸人', level: 18, hp: 600, hpMax: 600, slot: 'front_right', imageUrl: monsterPoster('DOR-MON-D-0182'), rank: 'D', attribute: '無', size: '中型', race: '人形' },
  { id: 'enemy_5', name: '荊棘毒蛾', level: 8, hp: 120, hpMax: 120, slot: 'rear_left', imageUrl: monsterPoster('DOR-MON-E-0052'), rank: 'E', attribute: '木', size: '小型', race: '昆蟲' },
];

// 數值逐字取自 scene_taipei_101/scene.json 的 monsterSlots（六張場景目前都同一組）。
const scene: Scene = {
  id: 'scene_taipei_101',
  name: '台北101',
  imageUrl: sceneImage('scene_taipei_101'),
  slots: [
    { id: 'rear_left', x: 0.27, y: 0.53, scale: 0.25, row: 'rear' },
    { id: 'rear_right', x: 0.68, y: 0.54, scale: 0.38, row: 'rear' },
    { id: 'front_left', x: 0.18, y: 0.88, scale: 0.33, row: 'front' },
    { id: 'front_center', x: 0.5, y: 0.89, scale: 0.36, row: 'front' },
    { id: 'front_right', x: 0.82, y: 0.88, scale: 0.33, row: 'front' },
  ],
};

// 裝備欄固定 8 格；示範裝五個原畫面技能，其餘三格 null 保留空白。
// P1 戰鬥欄位（規格 §2 公式：damage raw=floor((atk×coef+flat)×elem×charge)、heal=floor(matk×coef+flat)）：
// 係數為示範值、非平衡後正式數值（規格明講展示 HP/傷害皆測試資料）。
const skills: (Skill | null)[] = [
  { id: 'slash', name: '斬擊', iconUrl: kitAsset('icon_skill_slash'), cooldownMs: 4000, kind: 'damage', target: 'enemy', mpCost: 5, coefficient: 1.6, flat: 20, element: 'neutral', weapon: 'sword', castMs: 300 },
  { id: 'fireball', name: '火球', iconUrl: kitAsset('icon_skill_fireball'), cooldownMs: 12000, kind: 'damage', target: 'enemy', mpCost: 25, coefficient: 2.4, flat: 60, element: 'fire', weapon: 'staff', castMs: 600 },
  { id: 'heal', name: '治療', iconUrl: kitAsset('icon_skill_heal'), cooldownMs: 8000, kind: 'heal', target: 'ally', mpCost: 20, coefficient: 2.0, flat: 80, element: 'light', weapon: 'staff', castMs: 500 },
  { id: 'ice_lance', name: '冰槍', iconUrl: kitAsset('icon_skill_ice_lance'), cooldownMs: 6000, kind: 'damage', target: 'enemy', mpCost: 15, coefficient: 2.0, flat: 30, element: 'water', weapon: 'staff', castMs: 400 },
  { id: 'shield', name: '護盾', iconUrl: kitAsset('icon_skill_shield'), cooldownMs: 10000, kind: 'shield', target: 'self', mpCost: 15, coefficient: 1.5, flat: 60, element: 'light', weapon: 'staff', castMs: 300 },
  null,
  null,
  null,
];

// 道具一律對隊友（含自己）；revive 的 amount 為復活後 HP 佔 hpMax 的百分比。
const items: Item[] = [
  { id: 'hp_potion', name: '紅藥水', iconUrl: kitAsset('icon_item_hp_potion'), quantity: 3, kind: 'hp', amount: 300 },
  { id: 'mp_potion', name: '藍藥水', iconUrl: kitAsset('icon_item_mp_potion'), quantity: 2, kind: 'mp', amount: 120 },
  { id: 'revive_feather', name: '復甦羽毛', iconUrl: kitAsset('icon_item_revive_feather'), quantity: 1, kind: 'revive', amount: 50 },
];

export const SAMPLE_BATTLE: BattleSample = {
  party,
  enemies,
  scene,
  skills,
  items,
  // 預設鎖定 A 級首領（幽暗食人花）——與 kit 測試資料組裝預覽圖一致。
  initialTargetId: 'enemy_1',
};
