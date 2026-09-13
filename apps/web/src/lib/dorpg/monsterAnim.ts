// DORPG 怪物動畫資料（P1 ASSETS）。
// 數值逐字抄自 source/ui/08_DORPG_Content_Pack_v1/assets/monsters/<id>/monster.json 的 frameSize/anchor/animations，
// 只拿掉 frames[].png（內容包本機預覽用的單格 PNG 相對路徑）——正式遊戲改用 sheet 座標從 CDN 圖集裁切，
// 圖集 URL 由 cdn.ts::monsterSheetUrl(id, action) 給，不寫死在這裡（同一隻怪四動作各是一張獨立圖集）。
// 5 隻怪物動作結構雖然相同（3 欄 × 2 列、6 格、512² frame、anchor 0.5 / 0.87890625），
// 但 attack 動作的 attack_release 事件時間點（frameIndex/atMs）與各格 durationMs 逐隻不同，故不共用單一物件、照實抄好每隻怪的資料。

export type MonsterAction = 'idle' | 'attack' | 'hit' | 'death';

export interface MonsterAnimFrame {
  index: number;
  x: number;
  y: number;
  w: number;
  h: number;
  durationMs: number;
}

export interface MonsterAnimEvent {
  /** 對應 frames 的第幾格（僅供除錯／預覽用；正式判定一律用 atMs）。 */
  frameIndex: number;
  /** 動作開始起算的毫秒數，sprite-player 用來在播放中觸發 onEvent。 */
  atMs: number;
  /** 目前內容包只有 'attack_release'（純視覺同步用，傷害判定仍由 engine 決定，不得反向依賴這個事件）。 */
  event: string;
}

export interface MonsterAnim {
  columns: number;
  rows: number;
  frameCount: number;
  /** 整段動作總時長（ms），等於 frames[].durationMs 加總。 */
  durationMs: number;
  loop: boolean;
  /** 播完後自動接的動作；death 為 null（holdLast=true，停在末格不再接播）。 */
  next: MonsterAction | null;
  holdLast: boolean;
  events: MonsterAnimEvent[];
  frames: MonsterAnimFrame[];
}

export interface MonsterAnimSet {
  frameSize: { width: number; height: number };
  /** 腳點錨點（畫布 512×512 上的 0–1 比例，非像素）；與 lib/dorpg/assets.ts 的 MONSTER_ANCHOR 數值相同，來源各自獨立故不互相 import。 */
  anchor: { x: number; y: number };
  animations: Record<MonsterAction, MonsterAnim>;
}

/** key = 怪物 id（如 'DOR-MON-A-67000200001'），對應 sampleBattle.ts / Enemy.id。 */
export const MONSTER_ANIMS: Record<string, MonsterAnimSet> = {
  'DOR-MON-A-67000200001': {
    frameSize: { width: 512, height: 512 },
    anchor: { x: 0.5, y: 0.87890625 },
    animations: {
      idle: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1400,
        loop: true,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 260 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 200 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 220 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 240 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 220 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 260 },
        ],
      },
      attack: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 800,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [
          { frameIndex: 3, atMs: 360, event: 'attack_release' },
        ],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 180 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 120 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 170 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 150 },
        ],
      },
      hit: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 600,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 50 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 100 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 140 },
        ],
      },
      death: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1580,
        loop: false,
        next: null,
        holdLast: true,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 150 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 210 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 240 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 250 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 280 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 450 },
        ],
      },
    },
  },
  'DOR-MON-B-0089': {
    frameSize: { width: 512, height: 512 },
    anchor: { x: 0.5, y: 0.87890625 },
    animations: {
      idle: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1400,
        loop: true,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 260 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 200 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 220 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 240 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 220 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 260 },
        ],
      },
      attack: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 800,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [
          { frameIndex: 2, atMs: 270, event: 'attack_release' },
        ],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 180 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 120 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 170 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 150 },
        ],
      },
      hit: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 600,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 50 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 100 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 140 },
        ],
      },
      death: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1580,
        loop: false,
        next: null,
        holdLast: true,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 150 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 210 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 240 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 250 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 280 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 450 },
        ],
      },
    },
  },
  'DOR-MON-C-0229': {
    frameSize: { width: 512, height: 512 },
    anchor: { x: 0.5, y: 0.87890625 },
    animations: {
      idle: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1400,
        loop: true,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 260 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 200 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 220 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 240 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 220 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 260 },
        ],
      },
      attack: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 800,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [
          { frameIndex: 2, atMs: 270, event: 'attack_release' },
        ],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 180 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 120 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 170 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 150 },
        ],
      },
      hit: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 600,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 50 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 100 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 140 },
        ],
      },
      death: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1580,
        loop: false,
        next: null,
        holdLast: true,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 150 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 210 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 240 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 250 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 280 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 450 },
        ],
      },
    },
  },
  'DOR-MON-D-0182': {
    frameSize: { width: 512, height: 512 },
    anchor: { x: 0.5, y: 0.87890625 },
    animations: {
      idle: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1400,
        loop: true,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 260 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 200 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 220 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 240 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 220 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 260 },
        ],
      },
      attack: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 800,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [
          { frameIndex: 2, atMs: 270, event: 'attack_release' },
        ],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 180 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 120 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 170 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 150 },
        ],
      },
      hit: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 600,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 50 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 100 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 140 },
        ],
      },
      death: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1580,
        loop: false,
        next: null,
        holdLast: true,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 150 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 210 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 240 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 250 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 280 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 450 },
        ],
      },
    },
  },
  'DOR-MON-E-0052': {
    frameSize: { width: 512, height: 512 },
    anchor: { x: 0.5, y: 0.87890625 },
    animations: {
      idle: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1400,
        loop: true,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 260 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 200 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 220 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 240 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 220 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 260 },
        ],
      },
      attack: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 800,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [
          { frameIndex: 2, atMs: 270, event: 'attack_release' },
        ],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 180 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 120 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 170 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 150 },
        ],
      },
      hit: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 600,
        loop: false,
        next: 'idle',
        holdLast: false,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 50 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 90 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 100 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 140 },
        ],
      },
      death: {
        columns: 3,
        rows: 2,
        frameCount: 6,
        durationMs: 1580,
        loop: false,
        next: null,
        holdLast: true,
        events: [],
        frames: [
          { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 150 },
          { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 210 },
          { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 240 },
          { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 250 },
          { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 280 },
          { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 450 },
        ],
      },
    },
  },
};
