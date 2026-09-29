// retro (復古 RPG) skin 專屬：原創 16×16 像素圖塊繪製（契約 §3／§4）。
//
// 授權澄清（硬性要求，見契約 §1「原創美術」）：本檔案的每一個圖塊都是逐像素手刻的原創設計，
// 只借用「8-bit／16-bit JRPG 俯視大地圖」這個類型語彙（點描草地、樹林、水波、石牆、石板路），
// 不描摹、不參考任何特定商業遊戲（含《勇者鬥惡龍》）的實際圖檔、調色盤配置或角色／怪物造型。
// 調色盤限定 16 色（NES 風格三原色＋灰階＋暖色），見 RETRO_PALETTE。
//
// 用法（給 RetroBackground.tsx 與 MAP 工人 app/track/retro/ 共用）：
//   ctx.save()
//   ctx.translate(col * 16, row * 16)  // 呼叫端先把要畫的格子原點移到 (0,0)
//   drawTile(ctx, 'grass')
//   ctx.restore()
// drawTile 一律在「目前 transform 原點」畫滿 16×16 個 1×1 的方塊（假設 1 canvas 單位＝1 像素格）。
// 要放大顯示（像素風格）由呼叫端對外層 canvas 做 devicePixelRatio 縮放＋CSS
// `image-rendering: pixelated`，本函式不處理縮放，只負責「畫對顏色與圖案」。
//
// frame 參數：目前只有 'water' 使用（0｜1，兩幀水波動畫，契約要求 500ms 切換一次，切換節奏由
// 呼叫端的 setInterval/rAF 控制，本檔案只是「給定 frame 編號畫出對應那一幀」的純函式）。
// 其餘 kind 忽略 frame（不論傳入什麼都畫同一張）。

export type TileKind = 'grass' | 'forest' | 'water' | 'wall' | 'cobble' | 'path' | 'sand' | 'rail'

// 16 色調色盤（契約 §4 逐字列出）。索引僅供本檔內部圖塊資料表使用，外部若要驗證「調色盤內顏色
// 占比」，直接拿這個陣列的色碼去比對像素顏色即可（大寫/小寫皆為小寫 hex，比對前記得正規化）。
export const RETRO_PALETTE = [
  '#000000', '#ffffff', '#3cbc3c', '#00a800',
  '#005800', '#80d010', '#0078f8', '#3cbcfc',
  '#a4e4fc', '#b8b8f8', '#f8b800', '#ac7c00',
  '#c84c0c', '#e45c10', '#a81000', '#7c7c7c',
  // 契約 §7（2026-09-27 使用者追加，原註解）：勇者＝小井像素版追加色（頭髮/膚色/裙/鎧甲）。
  // docs/skins/RETRO_CONTRACT_R4.md §3 起，地圖上的勇者角色已整段移除（改用 orb.ts 的像素魔法
  // 光點，理由見該檔檔頭），本段 6 色不再由任何角色 sprite 使用——但 `#fcfcfc`／`#fcd8a8` 兩色被
  // orb.ts 重新沿用為光點的「白色核心」／「淡黃」，其餘 4 色（`#fc7aa4`／`#c8406c`／`#24188c`／
  // `#bcbcbc`，原本的髮色/裙色/鎧甲色）目前沒有任何繪圖程式使用——刻意保留在這個陣列裡（不刪除／
  // 不重新編號），避免影響任何依本陣列做「像素是否落在調色盤內」分類的既有驗證程式（見契約 §3
  // 逐字「若已無人使用可保留並改註解」）。
  '#fc7aa4', '#c8406c', '#fcd8a8', '#24188c', '#fcfcfc', '#bcbcbc',
  // R2 FIX（原註解，現況：圍巾/劍柄護手紅，勇者移除後同樣沒有任何繪圖程式使用，保留理由同上）。
  '#e4462c',
] as const

// 圖塊資料表用的簡短代號 → 調色盤色碼，只在本檔內部使用，不對外匯出（外部一律透過 RETRO_PALETTE
// 比對顏色，不依賴這裡的代號字母，代號純粹是為了讓下面 16×16 的字串陣列排版時眼睛看得懂）。
const P = {
  _: RETRO_PALETTE[0], // 黑（陰影/描邊/夜晚縫隙）
  W: RETRO_PALETTE[1], // 白（高光）
  g: RETRO_PALETTE[2], // 草地亮綠
  G: RETRO_PALETTE[3], // 草地中綠
  d: RETRO_PALETTE[4], // 草地暗綠（陰影）
  l: RETRO_PALETTE[5], // 草地點描亮點
  b: RETRO_PALETTE[6], // 水藍（中）
  c: RETRO_PALETTE[7], // 水藍（亮）
  C: RETRO_PALETTE[8], // 水藍（最亮／泡沫）
  p: RETRO_PALETTE[9], // 淡紫灰（石牆亮面）
  y: RETRO_PALETTE[10], // 金黃（強調/沙地亮）
  o: RETRO_PALETTE[11], // 土黃暗（小徑）
  r: RETRO_PALETTE[12], // 磚紅（石板路）
  R: RETRO_PALETTE[13], // 磚紅亮（石板路高光）
  A: RETRO_PALETTE[14], // 暗紅（石板路縫）
  s: RETRO_PALETTE[15], // 灰（石牆/鐵軌）
} as const

type Row = string // 16 個字元，每個字元對應 P 的一個 key
type Frame = string // 16 行攤平後的 256 字元字串（一格 16×16 圖塊的單一幀）
type TileData = readonly [Frame, Frame] // [frame0, frame1]；靜態圖塊兩幀相同內容

// 把 16 行 × 16 字元的可讀資料表攤平成一個 256 字元字串，並在開發期驗證每行長度，
// 資料若打錯（例如漏字元）會在模組載入時就丟出例外，不會等到畫面畫歪才發現。
function flatten(rows: readonly Row[]): Frame {
  if (rows.length !== 16) throw new Error(`retro tile must have 16 rows, got ${rows.length}`)
  for (const r of rows) {
    if (r.length !== 16) throw new Error(`retro tile row must be 16 chars, got ${r.length}: ${r}`)
  }
  return rows.join('')
}

// 草地：以中綠為底，暗綠做隨機感的點描斑駁，亮綠+亮點做疏落小草點綴（JRPG 大地圖經典「草地紋理」語彙）。
const GRASS: readonly Row[] = [
  'GGGGdGGGGGGGGGGG',
  'GGgGGGGGdGGGGGGG',
  'GGGGGGGGGGGGlGGG',
  'GdGGGGgGGGGGGGGG',
  'GGGGGGGGGGdGGGGG',
  'GGGlGGGGGGGGGgGG',
  'GGGGGGdGGGGGGGGG',
  'GGGGGGGGGlGGGGGG',
  'GgGGGGGGGGGGGdGG',
  'GGGGdGGGGGGGGGGG',
  'GGGGGGGGgGGGGGGG',
  'GGGGGGGGGGGGGGGG',
  'GdGGGGGGGGlGGGGG',
  'GGGGGgGGGGGGGdGG',
  'GGGGGGGGGGGGGGGG',
  'GGGGGGdGGGGGGgGG',
]

// 森林：草地底上一棵原創圓冠針葉樹（三層漸縮的暗綠/中綠球狀樹冠＋短樹幹），非任何既有遊戲的樹造型。
const FOREST: readonly Row[] = [
  'GGGGGGGGGGGGGGGG',
  'GGGGGGddGGGGGGGG',
  'GGGGGdGGdgGGGGGG',
  'GGGGdGGGGGdGGGGG',
  'GGGdGGggGGGdGGGG',
  'GGdGGgGGgGGGdGGG',
  'GGdGgGGGGgGGdGGG',
  'GGGdGGGGGGdGGGGG',
  'GGGGdGoGodGGGGGG',
  'GGGGGGoGoGGGGGGG',
  'GGGGGGoGoGGGGGGG',
  'GGGGGGGGGGGGGGGG',
  'GGGGGGGGGGGGGGGG',
  'GGGlGGGGGGGGgGGG',
  'GGGGGGGGGGGGGGGG',
  'GGGGGgGGGGdGGGGG',
]

// 水域：兩幀波紋動畫（水平波浪線左右錯位），中藍底＋亮藍/最亮泡沫線。
const WATER_0: readonly Row[] = [
  'bbbbbbbbbbbbbbbb',
  'bbcbbbbcbbbbcbbb',
  'bbbcbbbbcbbbbcbb',
  'bbbbbbbbbbbbbbbb',
  'bCbbbbCbbbbCbbbb',
  'bbbbbbbbbbbbbbbb',
  'bbbcbbbbcbbbbcbb',
  'bbbbcbbbbcbbbbcb',
  'bbbbbbbbbbbbbbbb',
  'bbCbbbbCbbbbCbbb',
  'bbbbbbbbbbbbbbbb',
  'bbbbcbbbbcbbbbcb',
  'bbbbbcbbbbcbbbbc',
  'bbbbbbbbbbbbbbbb',
  'bCbbbbCbbbbCbbbb',
  'bbbbbbbbbbbbbbbb',
]
const WATER_1: readonly Row[] = [
  'bbbbbbbbbbbbbbbb',
  'bbbcbbbbcbbbbcbb',
  'bbbbcbbbbcbbbbcb',
  'bCbbbbCbbbbCbbbb',
  'bbbbbbbbbbbbbbbb',
  'bbbcbbbbcbbbbcbb',
  'bbbbbbbbbbbbbbbb',
  'bbCbbbbCbbbbCbbb',
  'bbbbcbbbbcbbbbcb',
  'bbbbbbbbbbbbbbbb',
  'bbbbbcbbbbcbbbbc',
  'bbbbbbbbbbbbbbbb',
  'bCbbbbCbbbbCbbbb',
  'bbbbcbbbbcbbbbcb',
  'bbbbbbbbbbbbbbbb',
  'bbbcbbbbcbbbbcbb',
]

// 石牆（城鎮建築）：灰磚底＋黑色描邊（fill-outline）＋淡紫灰磚縫高光，橫向磚塊錯縫排列。
const WALL: readonly Row[] = [
  '________________',
  '_ssssPssssPssss_',
  '_ssssPssssPssss_',
  '_ssssPssssPssss_',
  '________________',
  '_sPssssPssssPss_',
  '_sPssssPssssPss_',
  '_sPssssPssssPss_',
  '________________',
  '_ssssPssssPssss_',
  '_ssssPssssPssss_',
  '_ssssPssssPssss_',
  '________________',
  '_sPssssPssssPss_',
  '_sPssssPssssPss_',
  '________________',
]

// 石板路（紅色主幹道）：磚紅底＋暗紅縫線＋亮紅高光點，交錯棋盤縫，呼應參考圖「紅色石板路」。
const COBBLE: readonly Row[] = [
  'rrrrArrrrArrrrAr',
  'rRrrrrRrrrrRrrrr',
  'rrrrrrrrrrrrrrrr',
  'ArrrrArrrrArrrrA',
  'rrrRrrrrRrrrrRrr',
  'rrrrrrrrrrrrrrrr',
  'rrrrArrrrArrrrAr',
  'rRrrrrRrrrrRrrrr',
  'rrrrrrrrrrrrrrrr',
  'ArrrrArrrrArrrrA',
  'rrrRrrrrRrrrrRrr',
  'rrrrrrrrrrrrrrrr',
  'rrrrArrrrArrrrAr',
  'rRrrrrRrrrrRrrrr',
  'rrrrrrrrrrrrrrrr',
  'ArrrrArrrrArrrrA',
]

// 小徑（一般道路／步道）：土黃底＋暗土黃斑駁，質感較石板路樸素。
const PATH: readonly Row[] = [
  'oooooooooooooooo',
  'ooooAooooooAoooo',
  'oooooooooooooooo',
  'oAoooooAoooooAoo',
  'oooooooooooooooo',
  'oooooAoooooAoooo',
  'oooooooooooooooo',
  'ooAoooooAoooooAo',
  'oooooooooooooooo',
  'ooooAooooooAoooo',
  'oooooooooooooooo',
  'oAoooooAoooooAoo',
  'oooooooooooooooo',
  'oooooAoooooAoooo',
  'oooooooooooooooo',
  'ooAoooooAoooooAo',
]

// 沙地（沙灘／操場）：金黃底＋土黃斑點。
const SAND: readonly Row[] = [
  'yyyyyyoyyyyyyyyy',
  'yyoyyyyyyyoyyyyy',
  'yyyyyyyyyyyyyyyy',
  'yyyyoyyyyyyyoyyy',
  'yoyyyyyyyyyyyyoy',
  'yyyyyyyyoyyyyyyy',
  'yyyyyyyyyyyyyyyy',
  'yyyoyyyyyyoyyyyy',
  'yyyyyyyyyyyyyyyy',
  'yyyyyyoyyyyyyyyy',
  'yyoyyyyyyyoyyyyy',
  'yyyyyyyyyyyyyyyy',
  'yyyyoyyyyyyyoyyy',
  'yoyyyyyyyyyyyyoy',
  'yyyyyyyyoyyyyyyy',
  'yyyyyyyyyyyyyyyy',
]

// 鐵軌：灰黑枕木（黑底＋灰橫木）＋兩條縱向灰色鐵軌。
const RAIL: readonly Row[] = [
  '_ss__________ss_',
  '_ss__________ss_',
  'sssssssssssssss_',
  '_ss__________ss_',
  '_ss__________ss_',
  'sssssssssssssss_',
  '_ss__________ss_',
  '_ss__________ss_',
  'sssssssssssssss_',
  '_ss__________ss_',
  '_ss__________ss_',
  'sssssssssssssss_',
  '_ss__________ss_',
  '_ss__________ss_',
  'sssssssssssssss_',
  '_ss__________ss_',
]

const TILES: Record<TileKind, TileData> = {
  grass: [flatten(GRASS), flatten(GRASS)],
  forest: [flatten(FOREST), flatten(FOREST)],
  water: [flatten(WATER_0), flatten(WATER_1)],
  wall: [flatten(WALL), flatten(WALL)],
  cobble: [flatten(COBBLE), flatten(COBBLE)],
  path: [flatten(PATH), flatten(PATH)],
  sand: [flatten(SAND), flatten(SAND)],
  rail: [flatten(RAIL), flatten(RAIL)],
}

/**
 * 在目前 canvas transform 的原點畫一格 16×16 原創像素圖塊。
 * 呼叫端負責：(1) 先 translate 到目標格子的左上角、(2) 縮放（devicePixelRatio／放大倍率）與
 * `image-rendering: pixelated` 都在外層處理，本函式只填滿 16×16 個 1×1 單位方塊。
 *
 * @param ctx   目標 2D context（假設已 translate 到格子原點；本函式不 save/restore）
 * @param kind  圖塊種類，見 TileKind
 * @param frame 動畫幀（僅 'water' 使用 0｜1；其餘種類忽略）
 */
export function drawTile(ctx: CanvasRenderingContext2D, kind: TileKind, frame: 0 | 1 = 0): void {
  const data = TILES[kind]
  const flat = frame === 1 ? data[1] : data[0]
  for (let y = 0; y < 16; y++) {
    for (let x = 0; x < 16; x++) {
      const ch = flat[y * 16 + x] as keyof typeof P
      const color = P[ch]
      if (!color) continue // 理論上不會發生（資料表窮舉），防呆略過而非丟例外
      ctx.fillStyle = color
      ctx.fillRect(x, y, 1, 1)
    }
  }
}
