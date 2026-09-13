// DORPG 素材路徑、色票與版面常數。
// 所有座標數字皆逐字抄自 07_DORPG_UI_Kit_v1/asset-manifest.json（邏輯 CSS px），再除以該素材的邏輯尺寸成 0–1 比例，
// 這樣元件只要用百分比定位，就能在任何寬度（390 基準、480 封頂）等比縮放而不必碰 3× 的 PNG 像素數。

export const DORPG_ASSET_BASE = '/ui/dorpg';

/** kit 的 PNG/WebP 皆為邏輯尺寸的 3 倍（技能／道具插畫例外：固定 512²）。border-image-slice 等吃來源像素的屬性要乘這個值。 */
export const KIT_SCALE = 3;

/** 07 UI kit 元件（71 張 WebP）：/ui/dorpg/ui/<kitId>.webp */
export function kitAsset(id: string): string {
  return `${DORPG_ASSET_BASE}/ui/${id}.webp`;
}

/** 08 content pack 角色肖像（不透明深藍底、正方形）：/ui/dorpg/char/<charId>_256.webp 或 _512 */
export function charPortrait(id: string, size: 256 | 512 = 256): string {
  return `${DORPG_ASSET_BASE}/char/${id}_${size}.webp`;
}

/** 怪物 poster（idle 第 0 格，512×512 含 alpha）：/ui/dorpg/mon/<monsterId>.webp */
export function monsterPoster(id: string): string {
  return `${DORPG_ASSET_BASE}/mon/${id}.webp`;
}

/** 場景手機版背景（768×480，8:5）：/ui/dorpg/scene/<sceneId>.webp */
export function sceneImage(id: string): string {
  return `${DORPG_ASSET_BASE}/scene/${id}.webp`;
}

// ---------------------------------------------------------------------------
// 色票（規格書 §3 表4 的 13 個 token）。畫面根節點固定 data-skin="default" 暗色，不得改讀前台 skin 變數。
// ---------------------------------------------------------------------------
export const PALETTE = {
  surfaceBase: '#001523',
  surfacePanel: '#06283B',
  borderGold: '#F3BD62',
  textPrimary: '#FFF9EA',
  textSecondary: '#B8CCD6',
  hpNormal: '#35D878',
  hpCritical: '#FFAA1F',
  hpEnemy: '#FF5260',
  mpFill: '#18A9F5',
  criticalGlow: '#FF2438',
  targetGold: '#FFD46A',
  disabledText: '#93A4AE',
  overlayScrim: 'rgba(0,0,0,.72)',
} as const;

// ---------------------------------------------------------------------------
// 版面高度（規格書 §3 表3，W390 基準）。場景高 = H − 其餘五區合計。
// ---------------------------------------------------------------------------
export type LayoutBands = {
  toolbar: number;
  party: number;
  target: number;
  tray: number;
  commands: number;
};

export const LAYOUT = {
  designWidth: 390,
  maxWidth: 480,
  standard: { toolbar: 44, party: 144, target: 32, tray: 114, commands: 80 },
  compact: { toolbar: 44, party: 128, target: 32, tray: 104, commands: 80 },
  short: { toolbar: 44, party: 120, target: 32, tray: 96, commands: 76 },
} as const satisfies {
  designWidth: number;
  maxWidth: number;
  standard: LayoutBands;
  compact: LayoutBands;
  short: LayoutBands;
};

export type LayoutTier = 'standard' | 'compact' | 'short';

/** 依可用高度挑版面級別：≥620 標準、560–619 緊湊、其餘短屏（<520 仍回短屏，容器可捲動）。 */
export function layoutFor(height: number): LayoutTier {
  if (height >= 620) return 'standard';
  if (height >= 560) return 'compact';
  return 'short';
}

/** 場景區高度 = 總高 − 五個固定帶高。 */
export function sceneHeightFor(height: number, tier: LayoutTier = layoutFor(height)): number {
  const b: LayoutBands = LAYOUT[tier];
  return height - (b.toolbar + b.party + b.target + b.tray + b.commands);
}

// ---------------------------------------------------------------------------
// 0–1 比例矩形（相對於素材自身邏輯尺寸）。
// ---------------------------------------------------------------------------
export type FracRect = { x: number; y: number; w: number; h: number };

/** 把比例矩形轉成 position:absolute 的百分比 style（left/top/width/height）。 */
export function fracStyle(r: FracRect): { left: string; top: string; width: string; height: string } {
  return {
    left: `${r.x * 100}%`,
    top: `${r.y * 100}%`,
    width: `${r.w * 100}%`,
    height: `${r.h * 100}%`,
  };
}

/** 血條露出比例 → clip-path: inset(0 X% 0 0)。填色圖永遠鋪滿插槽，只裁右側。 */
export function barClipPath(value: number, max: number): string {
  const ratio = max > 0 ? Math.min(1, Math.max(0, value / max)) : 0;
  return `inset(0 ${(1 - ratio) * 100}% 0 0)`;
}

// ---------------------------------------------------------------------------
// 隊員卡 panel_party_empty（邏輯 72×144）slots
// ---------------------------------------------------------------------------
export const PARTY_SLOTS = {
  w: 72,
  h: 144,
  portrait: { x: 14.6 / 72, y: 7.6 / 144, w: 42.8 / 72, h: 43.8 / 144 },
  name: { x: 4 / 72, y: 56 / 144, w: 64 / 72, h: 15 / 144 },
  level: { x: 4 / 72, y: 73 / 144, w: 64 / 72, h: 10 / 144 },
  hp: { x: 25 / 72, y: 83 / 144, w: 41 / 72, h: 18 / 144 },
  hpFill: { x: 6 / 72, y: 102 / 144, w: 60 / 72, h: 5.5 / 144 },
  mp: { x: 25 / 72, y: 112 / 144, w: 41 / 72, h: 18 / 144 },
  mpFill: { x: 6 / 72, y: 131 / 144, w: 60 / 72, h: 5.5 / 144 },
} as const satisfies { w: number; h: number } & Record<'portrait' | 'name' | 'level' | 'hp' | 'hpFill' | 'mp' | 'mpFill', FracRect>;

// ---------------------------------------------------------------------------
// 怪物 Lv/HP 小面板 panel_enemy_empty（邏輯 82×25，kit 建議尺寸；manifest 只有 slots 沒有 contentRect）
// ---------------------------------------------------------------------------
export const ENEMY_PLATE = {
  w: 82,
  h: 25,
  level: { x: 44 / 82, y: 3.5 / 25, w: 26 / 82, h: 11 / 25 },
  hpFill: { x: 5 / 82, y: 16 / 25, w: 72 / 82, h: 4.5 / 25 },
} as const satisfies { w: number; h: number; level: FracRect; hpFill: FracRect };

// ---------------------------------------------------------------------------
// 目標列 panel_target_empty（邏輯 378×32；「目標」二字已烤進底圖，contentRect 是名稱＋Lv 的可寫區）
// ---------------------------------------------------------------------------
export const TARGET_PANEL = {
  w: 378,
  h: 32,
  contentRect: { x: 63 / 378, y: 7 / 32, w: 300 / 378, h: 18 / 32 },
} as const satisfies { w: number; h: number; contentRect: FracRect };

// ---------------------------------------------------------------------------
// 技能格 frame_skill_empty / frame_skill_overlay（邏輯 54×54，contentRect 3.3 內縮 → 47.4 內容區）
// ---------------------------------------------------------------------------
export const SKILL_SLOT = {
  size: 54,
  iconInset: 3.3 / 54,
  contentRect: { x: 3.3 / 54, y: 3.3 / 54, w: 47.4 / 54, h: 47.4 / 54 },
  /** 覆層尺寸（邏輯 px）：CD 圓底 40、CD 環 50、數量小標 24×15（內容區 {4,3,16,9}）。 */
  cooldownDisc: 40,
  cooldownRing: 50,
  quantityBadge: { w: 24, h: 15, contentRect: { x: 4 / 24, y: 3 / 15, w: 16 / 24, h: 9 / 15 } },
} as const;

// ---------------------------------------------------------------------------
// 技能／道具底板 panel_tray_skills / panel_tray_items（邏輯 390×114）
// manifest 只給 count（已裝備 n/8 文字區）與 content（五格＋箭頭的整體內容區）兩個 slot，
// 沒有逐格座標；五格排法由元件依 content 區內 flex 排：左右箭頭 28×44、其餘均分。
// ---------------------------------------------------------------------------
export const TRAY = {
  width: 390,
  height: 114,
  count: { x: 260 / 390, y: 8 / 114, w: 112 / 390, h: 17 / 114 },
  content: { x: 24 / 390, y: 35 / 114, w: 342 / 390, h: 73 / 114 },
  visibleCells: 5,
  /** 8 格裝備欄一次顯示 5 格，offset 0–3 不循環。 */
  maxOffset: 3,
  /** icon_arrow_left/right 邏輯 28×44；manifest hitArea {x:-8,y:0,w:44,h:44}，kit preview 的按鈕實作 44×54。 */
  arrow: { w: 28, h: 44, hitW: 44, hitH: 54 },
  /**
   * 五格＋箭頭的排法（抄自 kit preview/preview.css #skill-lane，讓畫面與「測試資料組裝預覽.png」對齊）：
   * 絕對定位 left/right 2px、top 35px、高 75px；grid-template-columns: 44px repeat(5, minmax(0,1fr)) 44px；gap 2px；
   * 每格為 54×74 的技能格（上 54×54 金框＝格高 72.973%，下方 20% 放技能名 12px/700）。
   */
  lane: { inset: 2, top: 35, height: 75, arrowCol: 44, gap: 2 },
  cell: { w: 54, h: 74, frameHeightRatio: 54 / 74, nameTop: 0.76, nameHeight: 0.2 },
} as const satisfies {
  width: number;
  height: number;
  count: FracRect;
  content: FracRect;
  visibleCells: number;
  maxOffset: number;
  arrow: { w: number; h: number; hitW: number; hitH: number };
  lane: { inset: number; top: number; height: number; arrowCol: number; gap: number };
  cell: { w: number; h: number; frameHeightRatio: number; nameTop: number; nameHeight: number };
};

// ---------------------------------------------------------------------------
// 按鈕邏輯尺寸
// ---------------------------------------------------------------------------
/** 道具／防禦／攻擊主按鈕 button_<type>_<state>（四態同畫布，整張換圖不裁切）。 */
export const COMMAND_BTN = { w: 122, h: 74 } as const;
/** 逃跑鈕 button_escape_<state>；視覺 76×36 但 manifest minimumHitSize 要求點擊區高 44。 */
export const ESCAPE_BTN = { w: 76, h: 36, minHit: 44 } as const;
/** 對話框確認／取消鈕 button_confirm / button_cancel。 */
export const DIALOG_BTN = { w: 112, h: 40 } as const;

// ---------------------------------------------------------------------------
// 九宮格（manifest nineSlice，單位邏輯 px）。CSS：border-image-slice = 值 × KIT_SCALE，border-image-width = 值 px。
// ---------------------------------------------------------------------------
export type NineSlice = { top: number; right: number; bottom: number; left: number };

export const NINE_SLICE = {
  frame_dialog: { slice: { top: 16, right: 16, bottom: 16, left: 16 }, logical: { w: 340, h: 260 }, contentRect: { x: 20, y: 20, w: 300, h: 220 } },
  frame_text_short: { slice: { top: 7, right: 9, bottom: 7, left: 9 }, logical: { w: 82, h: 25 }, contentRect: { x: 7, y: 5, w: 68, h: 15 } },
  frame_text_long: { slice: { top: 9, right: 15, bottom: 9, left: 15 }, logical: { w: 378, h: 32 }, contentRect: { x: 13, y: 7, w: 352, h: 18 } },
  frame_toast: { slice: { top: 10, right: 12, bottom: 10, left: 12 }, logical: { w: 270, h: 44 }, contentRect: { x: 12, y: 10, w: 246, h: 24 } },
  frame_actions_group: { slice: { top: 12, right: 15, bottom: 12, left: 15 }, logical: { w: 390, h: 80 }, contentRect: null },
} as const satisfies Record<string, { slice: NineSlice; logical: { w: number; h: number }; contentRect: { x: number; y: number; w: number; h: number } | null }>;

/** 直接可展開到 style 的九宮格 border-image 屬性組。 */
export function nineSliceStyle(id: keyof typeof NINE_SLICE): {
  borderStyle: 'solid';
  borderColor: 'transparent';
  borderWidth: string;
  borderImageSource: string;
  borderImageSlice: string;
  borderImageWidth: string;
  borderImageRepeat: 'stretch';
} {
  const s = NINE_SLICE[id].slice;
  return {
    borderStyle: 'solid',
    borderColor: 'transparent',
    borderWidth: `${s.top}px ${s.right}px ${s.bottom}px ${s.left}px`,
    borderImageSource: `url("${kitAsset(id)}")`,
    borderImageSlice: `${s.top * KIT_SCALE} ${s.right * KIT_SCALE} ${s.bottom * KIT_SCALE} ${s.left * KIT_SCALE} fill`,
    borderImageWidth: `${s.top}px ${s.right}px ${s.bottom}px ${s.left}px`,
    borderImageRepeat: 'stretch',
  };
}

// ---------------------------------------------------------------------------
// 其他常用 kit 元件的邏輯尺寸（manifest logicalSize），供元件直接以邏輯 px 設寬高。
// ---------------------------------------------------------------------------
export const KIT_SIZES = {
  panel_header_blank: { w: 390, h: 44 },
  logo_dorpg: { w: 144, h: 28 },
  ornament_divider: { w: 240, h: 24 },
  icon_settings: { w: 44, h: 44 },
  icon_target_reticle: { w: 32, h: 32 },
  target_chevrons: { w: 40, h: 36 },
  target_ground_ring: { w: 150, h: 36 },
  frame_portrait: { w: 48, h: 48 },
  bar_fill: { w: 120, h: 8 },
  label_skills: { w: 32, h: 18 },
  label_target: { w: 31, h: 18 },
  label_equipped: { w: 48, h: 18 },
} as const satisfies Record<string, { w: number; h: number }>;

/** frame_portrait 內容區（邏輯 48×48，內縮 2 → 44×44）。 */
export const PORTRAIT_FRAME = {
  size: 48,
  contentRect: { x: 2 / 48, y: 2 / 48, w: 44 / 48, h: 44 / 48 },
} as const satisfies { size: number; contentRect: FracRect };

// ---------------------------------------------------------------------------
// 怪物擺放（content pack manifest anchor，畫布 512 上的 (256,450)）：
// 左上角 = (groundX − displayWidth×0.5, groundY − displayWidth×0.87890625)；同隻四動作共用 displayWidth。
// ---------------------------------------------------------------------------
export const MONSTER_ANCHOR = { x: 0.5, y: 0.87890625 } as const;

/** 場景邏輯視窗（scene.json logicalViewport）；場景圖以 cover 填滿、objectPosition 50% 50%。 */
export const SCENE_VIEWPORT = { w: 390, h: 244 } as const;

// ---------------------------------------------------------------------------
// 動畫週期（kit 說明：瀕死紅光 1200ms；目標箭頭 translateY 上下漂動）。prefers-reduced-motion 時改靜態紅邊、箭頭不動。
// ---------------------------------------------------------------------------
export const ANIM = { criticalPulseMs: 1200, chevronBobMs: 1200 } as const;

/** 瀕死門檻：0 < hp/hpMax ≤ 0.2 → 橘 HP＋overlay_party_critical；hp = 0 → overlay_party_dead。 */
export const CRITICAL_RATIO = 0.2;
