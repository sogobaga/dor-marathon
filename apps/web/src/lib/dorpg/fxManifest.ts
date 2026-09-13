// DORPG 戰鬥特效／音效資料（P1 ASSETS）。
// 數值逐字抄自 source/music/Battle_FX/DORPG_Combat_FX_Pack_v12/manifest.json（schemaVersion 1.2.0 / packId
// dorpg-combat-fx-v12 / revision 12.0.0），只做兩件事：
//   (1) 圖片路徑（effects[].atlas、labels[].image/atlas、font.atlas）改寫成 R2 絕對網址（scripts/dorpg-upload/
//       manifest.json 上傳後回傳的 url，即 img.dor.tw/dorpg/fx/...）；
//   (2) 音效路徑（audio[].web）改寫成同源絕對路徑 /ui/dorpg/audio/<id>.wav（18 個 .web.wav 24kHz 版本，
//       放 public/ 免 CORS；48kHz 的 .wav 原始檔不出貨，runtime 本來就只吃 .web 版本）。
//
// runtime/dorpg-combat-fx.js 的 url(path) 是 `new URL(path, this.baseURL).href`——WHATWG URL 對「path 已是絕對
// URL」時會直接回傳該絕對 URL、忽略 baseURL（規格如此，非本檔猜測）。所以這裡把 atlas/image/web 都寫成
// https://... 或 /ui/... 開頭的絕對位址後，CombatFxLayer 用 `new DORPGCombatFX({ canvas, manifest: FX_MANIFEST,
// baseURL: '' })` 建構時，無論 baseURL 傳什麼都會直接命中這些絕對網址，不會被拼接壞掉。
//
// 拿掉的欄位：atlasPng/animatedWebp/basePath（本機預覽與 PSD 溯源用，正式遊戲不讀）、
// effects[].frames[].file（單格 PNG 路徑，正式改吃 atlas 裁切）、font.glyphs[].image/sourceOffsetX（同理，
// 正式只讀 atlas）、audio[].wav（48kHz 原始檔，未出貨）、audio[].origin/description/sourceRefs/accentAtMs/
// damageEvents/blastAtMs/sequence/baseAudioId/baseDelayMs/blastDurationMs/energy95AtMs/peakDbFS/rmsDbFS（版權/
// 混音備忘用途，非 runtime 需要）。

export type FxWeapon = 'sword' | 'staff' | 'bow' | 'greatsword';
export type FxResult = 'normal' | 'critical' | 'miss' | 'immune';

// 底下多個介面尾巴都加了 `[key: string]: unknown`：這不是為了描述真的有動態欄位，而是對齊
// vendor/dorpgCombatFx.d.ts（AUDIO_FX 工作者所寫）裡 DORPGFx*Def 系列型別的同款尾巴——TypeScript
// 對「具名介面之間」的指派會要求兩邊都宣告索引簽章才放行（即使右邊型別的每個欄位其實都滿足
// `unknown`），少了這行會在 CombatFxLayer.tsx 把 FX_MANIFEST 傳給 `new DORPGCombatFX({ manifest })`
// 時被 tsc 擋下（「Index signature for type 'string' is missing」）。兩邊 .d.ts 檔頭都寫明了這個
// 對齊用意，故非各自巧合。

export interface FxFrame {
  index: number;
  x: number;
  y: number;
  w: number;
  h: number;
  durationMs: number;
  [key: string]: unknown;
}

/** miss 特效的標籤/位移規則；runtime 沒有 targetBounds 時走 fallbackOffsetCssPx。 */
export interface FxPlacement {
  targetBoundsAnchor: { x: number; y: number };
  fallbackOffsetCssPx: { x: number; y: number };
  labelOffsetCssPx: { x: number; y: number };
  labelRiseCssPx: number;
  labelRiseSpeedCssPxPerMs: number;
}

export interface FxEffect {
  id: string;
  weapon: FxWeapon;
  result: FxResult;
  name: string;
  /** R2 絕對網址（1536×1024 圖集，3 欄 × 2 列 × 512² frame）；runtime 用 image(path) 讀取後依 frames[].{x,y,w,h} 裁切畫。 */
  atlas: string;
  frameSize: { w: number; h: number };
  atlasSize: { w: number; h: number };
  anchor: { x: number; y: number };
  loop: boolean;
  durationMs: number;
  impactAtMs: number;
  hasContact: boolean;
  audioId: string;
  displayWidthCssPx: number;
  frames: FxFrame[];
  /** 只有 result==='miss' 的效果才有（4 種武器各一）。 */
  placement?: FxPlacement;
  [key: string]: unknown;
}

export interface FxLabel {
  id: string;
  text: string;
  /** R2 絕對網址：完整標籤圖，runtime 直接整張畫（不切格）。 */
  image: string;
  /** R2 絕對網址：舊版逐格 pop 動畫圖集；目前 runtime v12 的 play() 不讀這個欄位，保留供未來版本/預覽頁使用。 */
  atlas: string;
  width: number;
  height: number;
  durationMs: number;
  frameDurationMs: number[];
  anchor: { x: number; y: number };
  audioId: string;
  style?: string;
  popScaleMax?: number;
  palette?: Record<string, string>;
  family?: string;
  [key: string]: unknown;
}

export interface FxFontGlyph {
  x: number;
  y: number;
  w: number;
  h: number;
  xadvance: number;
  [key: string]: unknown;
}

export interface FxFont {
  id: string;
  characters: string;
  /** R2 絕對網址：0–9 字圖集，number() 依 glyphs[].{x,y,w,h} 從這張裁切畫傷害數字。 */
  atlas: string;
  cellWidth: number;
  cellHeight: number;
  baseline: number;
  advance: number;
  columns: number;
  rows: number;
  spacing: number;
  systemFont: boolean;
  glyphs: Record<string, FxFontGlyph>;
  layoutMode?: string;
  minimumInkGap?: number;
  /** 兩字元組合鍵（如 '10' = '1'+'0' 相鄰）的視覺化間距微調 px；number() 找不到組合鍵時退回 xadvance。 */
  pairAdvance?: Record<string, number>;
  [key: string]: unknown;
}

export interface FxAudio {
  id: string;
  weapon: string;
  result: string;
  /** 同源絕對路徑 /ui/dorpg/audio/<id>.wav（24kHz .web.wav，放 public/ 免 CORS）；runtime 的 buffer() 只 fetch 這個欄位。 */
  web: string;
  durationMs: number;
  impactAtMs: number;
  loop: boolean;
  /** runtime v12 目前不讀這個值（sound() 呼叫端寫死 0.75/0.18），保留給 battleAudio.playSfx 之類的呼叫端自訂預設音量用。 */
  defaultGain: number;
  [key: string]: unknown;
}

export interface FxRuntimeParams {
  maxEffects: number;
  maxAudioVoices: number;
  maxVoicesPerTarget: number;
  defaultMasterGain: number;
  defaultVoiceGain: number;
  damageRange: [number, number];
  authoritativeDamage: boolean;
  missPlacement: FxPlacement;
}

/** 對齊 runtime/dorpg-combat-fx.js 建構子 `{ canvas, manifest, baseURL }` 期望的 manifest 形狀。 */
export interface FxManifest {
  effects: FxEffect[];
  labels: FxLabel[];
  font: FxFont;
  audio: FxAudio[];
  runtime: FxRuntimeParams;
  [key: string]: unknown;
}

export const FX_MANIFEST: FxManifest = {
  effects: [
  {
    id: 'fx_sword_normal',
    weapon: 'sword',
    result: 'normal',
    name: '劍 基礎攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/sword/normal/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 360,
    impactAtMs: 100,
    hasContact: true,
    audioId: 'sfx_sword_normal',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 70 },
    ],
  },
  {
    id: 'fx_sword_critical',
    weapon: 'sword',
    result: 'critical',
    name: '劍 爆擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/sword/critical/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 520,
    impactAtMs: 105,
    hasContact: true,
    audioId: 'sfx_sword_critical',
    displayWidthCssPx: 155,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 95 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 100 },
    ],
  },
  {
    id: 'fx_sword_miss',
    weapon: 'sword',
    result: 'miss',
    name: '劍 Miss',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/sword/miss/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 300,
    impactAtMs: 80,
    hasContact: false,
    audioId: 'sfx_sword_miss',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 45 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 55 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 55 },
    ],
    placement:
      {
        targetBoundsAnchor: { x: 0.5, y: 0.62 },
        fallbackOffsetCssPx: { x: 0, y: 32 },
        labelOffsetCssPx: { x: 0, y: 18 },
        labelRiseCssPx: 48,
        labelRiseSpeedCssPxPerMs: 0.09,
      },
  },
  {
    id: 'fx_sword_immune',
    weapon: 'sword',
    result: 'immune',
    name: '劍 無效攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/sword/immune/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 340,
    impactAtMs: 85,
    hasContact: true,
    audioId: 'sfx_sword_immune',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 50 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 70 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 60 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 60 },
    ],
  },
  {
    id: 'fx_staff_normal',
    weapon: 'staff',
    result: 'normal',
    name: '杖 基礎攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/staff/normal/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 360,
    impactAtMs: 100,
    hasContact: true,
    audioId: 'sfx_staff_normal',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 70 },
    ],
  },
  {
    id: 'fx_staff_critical',
    weapon: 'staff',
    result: 'critical',
    name: '杖 爆擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/staff/critical/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 520,
    impactAtMs: 105,
    hasContact: true,
    audioId: 'sfx_staff_critical',
    displayWidthCssPx: 155,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 95 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 100 },
    ],
  },
  {
    id: 'fx_staff_miss',
    weapon: 'staff',
    result: 'miss',
    name: '杖 Miss',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/staff/miss/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 300,
    impactAtMs: 80,
    hasContact: false,
    audioId: 'sfx_staff_miss',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 45 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 55 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 55 },
    ],
    placement:
      {
        targetBoundsAnchor: { x: 0.5, y: 0.62 },
        fallbackOffsetCssPx: { x: 0, y: 32 },
        labelOffsetCssPx: { x: 0, y: 18 },
        labelRiseCssPx: 48,
        labelRiseSpeedCssPxPerMs: 0.09,
      },
  },
  {
    id: 'fx_staff_immune',
    weapon: 'staff',
    result: 'immune',
    name: '杖 無效攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/staff/immune/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 340,
    impactAtMs: 85,
    hasContact: true,
    audioId: 'sfx_staff_immune',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 50 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 70 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 60 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 60 },
    ],
  },
  {
    id: 'fx_bow_normal',
    weapon: 'bow',
    result: 'normal',
    name: '弓 基礎攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/bow/normal/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 360,
    impactAtMs: 100,
    hasContact: true,
    audioId: 'sfx_bow_normal',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 70 },
    ],
  },
  {
    id: 'fx_bow_critical',
    weapon: 'bow',
    result: 'critical',
    name: '弓 爆擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/bow/critical/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 520,
    impactAtMs: 105,
    hasContact: true,
    audioId: 'sfx_bow_critical',
    displayWidthCssPx: 155,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 95 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 100 },
    ],
  },
  {
    id: 'fx_bow_miss',
    weapon: 'bow',
    result: 'miss',
    name: '弓 Miss',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/bow/miss/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 300,
    impactAtMs: 80,
    hasContact: false,
    audioId: 'sfx_bow_miss',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 45 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 55 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 55 },
    ],
    placement:
      {
        targetBoundsAnchor: { x: 0.5, y: 0.62 },
        fallbackOffsetCssPx: { x: 0, y: 32 },
        labelOffsetCssPx: { x: 0, y: 18 },
        labelRiseCssPx: 48,
        labelRiseSpeedCssPxPerMs: 0.09,
      },
  },
  {
    id: 'fx_bow_immune',
    weapon: 'bow',
    result: 'immune',
    name: '弓 無效攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/bow/immune/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 340,
    impactAtMs: 85,
    hasContact: true,
    audioId: 'sfx_bow_immune',
    displayWidthCssPx: 130,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 50 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 70 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 60 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 60 },
    ],
  },
  {
    id: 'fx_greatsword_normal',
    weapon: 'greatsword',
    result: 'normal',
    name: '重劍 基礎攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/greatsword/normal/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 360,
    impactAtMs: 100,
    hasContact: true,
    audioId: 'sfx_greatsword_normal',
    displayWidthCssPx: 155,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 60 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 65 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 70 },
    ],
  },
  {
    id: 'fx_greatsword_critical',
    weapon: 'greatsword',
    result: 'critical',
    name: '重劍 爆擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/greatsword/critical/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 520,
    impactAtMs: 105,
    hasContact: true,
    audioId: 'sfx_greatsword_critical',
    displayWidthCssPx: 180,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 40 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 95 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 110 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 100 },
    ],
  },
  {
    id: 'fx_greatsword_miss',
    weapon: 'greatsword',
    result: 'miss',
    name: '重劍 Miss',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/greatsword/miss/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 300,
    impactAtMs: 80,
    hasContact: false,
    audioId: 'sfx_greatsword_miss',
    displayWidthCssPx: 155,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 45 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 55 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 55 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 55 },
    ],
    placement:
      {
        targetBoundsAnchor: { x: 0.5, y: 0.62 },
        fallbackOffsetCssPx: { x: 0, y: 32 },
        labelOffsetCssPx: { x: 0, y: 18 },
        labelRiseCssPx: 48,
        labelRiseSpeedCssPxPerMs: 0.09,
      },
  },
  {
    id: 'fx_greatsword_immune',
    weapon: 'greatsword',
    result: 'immune',
    name: '重劍 無效攻擊',
    atlas: 'https://img.dor.tw/dorpg/fx/effects/greatsword/immune/atlas.webp',
    frameSize: { w: 512, h: 512 },
    atlasSize: { w: 1536, h: 1024 },
    anchor: { x: 0.5, y: 0.5 },
    loop: false,
    durationMs: 340,
    impactAtMs: 85,
    hasContact: true,
    audioId: 'sfx_greatsword_immune',
    displayWidthCssPx: 155,
    frames: [
      { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 35 },
      { index: 1, x: 512, y: 0, w: 512, h: 512, durationMs: 50 },
      { index: 2, x: 1024, y: 0, w: 512, h: 512, durationMs: 65 },
      { index: 3, x: 0, y: 512, w: 512, h: 512, durationMs: 70 },
      { index: 4, x: 512, y: 512, w: 512, h: 512, durationMs: 60 },
      { index: 5, x: 1024, y: 512, w: 512, h: 512, durationMs: 60 },
    ],
  },
  ],
  labels: [
  {
    id: 'label_critical',
    text: 'Critical',
    image: 'https://img.dor.tw/dorpg/fx/labels/critical/label.webp',
    atlas: 'https://img.dor.tw/dorpg/fx/labels/critical/atlas.webp',
    width: 768,
    height: 320,
    durationMs: 720,
    frameDurationMs: [65, 85, 140, 170, 180, 80],
    anchor: { x: 0.5, y: 0.5 },
    audioId: 'sfx_label_critical',
    style: 'gold_block_text_only',
    popScaleMax: 1.08,
    palette: { highlight: '#FFF7CD', face: '#F4C83F', shade: '#A76C0A', outline: '#080C31' },
    family: 'miss_companion',
  },
  {
    id: 'label_miss',
    text: 'Miss',
    image: 'https://img.dor.tw/dorpg/fx/labels/miss/label.webp',
    atlas: 'https://img.dor.tw/dorpg/fx/labels/miss/atlas.webp',
    width: 512,
    height: 256,
    durationMs: 720,
    frameDurationMs: [65, 85, 140, 170, 180, 80],
    anchor: { x: 0.5, y: 0.5 },
    audioId: 'sfx_label_miss',
    style: 'ice_blue_text_only',
    popScaleMax: 1.08,
  },
  ],
  font: {
  id: 'digits_ivory_gold',
  characters: '0123456789',
  atlas: 'https://img.dor.tw/dorpg/fx/digits/atlas.webp',
  cellWidth: 128,
  cellHeight: 160,
  baseline: 144,
  advance: 104,
  columns: 5,
  rows: 2,
  spacing: 0,
  systemFont: false,
  glyphs: {
    '0': { x: 10, y: 0, w: 108, h: 160, xadvance: 110 },
    '1': { x: 153, y: 0, w: 78, h: 160, xadvance: 80 },
    '2': { x: 266, y: 0, w: 108, h: 160, xadvance: 110 },
    '3': { x: 394, y: 0, w: 108, h: 160, xadvance: 110 },
    '4': { x: 522, y: 0, w: 108, h: 160, xadvance: 110 },
    '5': { x: 10, y: 160, w: 108, h: 160, xadvance: 110 },
    '6': { x: 138, y: 160, w: 108, h: 160, xadvance: 110 },
    '7': { x: 266, y: 160, w: 108, h: 160, xadvance: 110 },
    '8': { x: 394, y: 160, w: 108, h: 160, xadvance: 110 },
    '9': { x: 522, y: 160, w: 108, h: 160, xadvance: 110 },
  },
  layoutMode: 'optical-pairs',
  minimumInkGap: 2,
  pairAdvance: {
    '10': 67,
    '11': 76,
    '12': 64,
    '13': 66,
    '14': 67,
    '15': 66,
    '16': 66,
    '17': 66,
    '18': 67,
    '19': 65,
    '20': 102,
    '21': 110,
    '22': 98,
    '23': 101,
    '24': 101,
    '25': 101,
    '26': 101,
    '27': 101,
    '28': 101,
    '29': 100,
    '30': 101,
    '31': 110,
    '32': 97,
    '33': 100,
    '34': 101,
    '35': 100,
    '36': 100,
    '37': 100,
    '38': 101,
    '39': 99,
    '40': 108,
    '41': 105,
    '42': 104,
    '43': 106,
    '44': 108,
    '45': 106,
    '46': 107,
    '47': 97,
    '48': 108,
    '49': 106,
    '50': 101,
    '51': 108,
    '52': 97,
    '53': 99,
    '54': 101,
    '55': 99,
    '56': 100,
    '57': 98,
    '58': 101,
    '59': 99,
    '60': 101,
    '61': 109,
    '62': 97,
    '63': 100,
    '64': 101,
    '65': 100,
    '66': 101,
    '67': 99,
    '68': 101,
    '69': 100,
    '70': 99,
    '71': 108,
    '72': 93,
    '73': 97,
    '74': 89,
    '75': 97,
    '76': 98,
    '77': 99,
    '78': 99,
    '79': 98,
    '80': 101,
    '81': 110,
    '82': 97,
    '83': 100,
    '84': 101,
    '85': 100,
    '86': 100,
    '87': 100,
    '88': 101,
    '89': 100,
    '90': 101,
    '91': 110,
    '92': 96,
    '93': 99,
    '94': 101,
    '95': 99,
    '96': 100,
    '97': 100,
    '98': 101,
    '99': 99,
    '00': 101,
    '01': 110,
    '02': 97,
    '03': 99,
    '04': 101,
    '05': 99,
    '06': 100,
    '07': 100,
    '08': 101,
    '09': 99,
  },
  },
  audio: [
  {
    id: 'sfx_sword_normal',
    weapon: 'sword',
    result: 'normal',
    web: '/ui/dorpg/audio/sfx_sword_normal.wav',
    durationMs: 360,
    impactAtMs: 100,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_sword_critical',
    weapon: 'sword',
    result: 'critical',
    web: '/ui/dorpg/audio/sfx_sword_critical.wav',
    durationMs: 740,
    impactAtMs: 105,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_sword_miss',
    weapon: 'sword',
    result: 'miss',
    web: '/ui/dorpg/audio/sfx_sword_miss.wav',
    durationMs: 200,
    impactAtMs: 80,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_sword_immune',
    weapon: 'sword',
    result: 'immune',
    web: '/ui/dorpg/audio/sfx_sword_immune.wav',
    durationMs: 330,
    impactAtMs: 85,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_staff_normal',
    weapon: 'staff',
    result: 'normal',
    web: '/ui/dorpg/audio/sfx_staff_normal.wav',
    durationMs: 340,
    impactAtMs: 100,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_staff_critical',
    weapon: 'staff',
    result: 'critical',
    web: '/ui/dorpg/audio/sfx_staff_critical.wav',
    durationMs: 380,
    impactAtMs: 105,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_staff_miss',
    weapon: 'staff',
    result: 'miss',
    web: '/ui/dorpg/audio/sfx_staff_miss.wav',
    durationMs: 210,
    impactAtMs: 80,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_staff_immune',
    weapon: 'staff',
    result: 'immune',
    web: '/ui/dorpg/audio/sfx_staff_immune.wav',
    durationMs: 330,
    impactAtMs: 85,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_bow_normal',
    weapon: 'bow',
    result: 'normal',
    web: '/ui/dorpg/audio/sfx_bow_normal.wav',
    durationMs: 260,
    impactAtMs: 100,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_bow_critical',
    weapon: 'bow',
    result: 'critical',
    web: '/ui/dorpg/audio/sfx_bow_critical.wav',
    durationMs: 380,
    impactAtMs: 105,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_bow_miss',
    weapon: 'bow',
    result: 'miss',
    web: '/ui/dorpg/audio/sfx_bow_miss.wav',
    durationMs: 190,
    impactAtMs: 80,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_bow_immune',
    weapon: 'bow',
    result: 'immune',
    web: '/ui/dorpg/audio/sfx_bow_immune.wav',
    durationMs: 330,
    impactAtMs: 85,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_greatsword_normal',
    weapon: 'greatsword',
    result: 'normal',
    web: '/ui/dorpg/audio/sfx_greatsword_normal.wav',
    durationMs: 670,
    impactAtMs: 100,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_greatsword_critical',
    weapon: 'greatsword',
    result: 'critical',
    web: '/ui/dorpg/audio/sfx_greatsword_critical.wav',
    durationMs: 860,
    impactAtMs: 105,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_greatsword_miss',
    weapon: 'greatsword',
    result: 'miss',
    web: '/ui/dorpg/audio/sfx_greatsword_miss.wav',
    durationMs: 250,
    impactAtMs: 80,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_greatsword_immune',
    weapon: 'greatsword',
    result: 'immune',
    web: '/ui/dorpg/audio/sfx_greatsword_immune.wav',
    durationMs: 390,
    impactAtMs: 85,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_label_critical',
    weapon: 'label',
    result: 'label',
    web: '/ui/dorpg/audio/sfx_label_critical.wav',
    durationMs: 220,
    impactAtMs: 0,
    loop: false,
    defaultGain: 0.75,
  },
  {
    id: 'sfx_label_miss',
    weapon: 'label',
    result: 'label',
    web: '/ui/dorpg/audio/sfx_label_miss.wav',
    durationMs: 180,
    impactAtMs: 0,
    loop: false,
    defaultGain: 0.75,
  },
  ],
  runtime: {
  maxEffects: 12,
  maxAudioVoices: 8,
  maxVoicesPerTarget: 2,
  defaultMasterGain: 0.6,
  defaultVoiceGain: 0.75,
  damageRange: [0, 999999999],
  authoritativeDamage: true,
  missPlacement:
    {
      targetBoundsAnchor: { x: 0.5, y: 0.62 },
      fallbackOffsetCssPx: { x: 0, y: 32 },
      labelOffsetCssPx: { x: 0, y: 18 },
      labelRiseCssPx: 48,
      labelRiseSpeedCssPxPerMs: 0.09,
    },
  },
};
