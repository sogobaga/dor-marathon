// 溫馨可愛（cute）GPS 地圖 — 原創路線裝飾圖示（CONTRACT_R2.md §4.4）：每公里「愛心徽章」、目標點
// 原創小禮物盒／完成打勾貼紙／一般打卡點小旗。全部程式逐筆 canvas 繪製，零外部素材、零吉伊卡哇
// 造型語彙（純幾何：愛心/矩形/圓角矩形），與 track/retro/sprites.ts 對應功能（drawKmFlag／
// drawTargetIcon）同等地位但改走手繪可愛風（平滑抗鋸齒，非像素風）。
//
// 色票：本檔逐字複製 CONTRACT_R2.md §2「草莓牛奶」色票中這幾個圖示會用到的 hex，刻意不 import
// components/cute/decor.ts 的 CUTE_PALETTE（理由同 orb.ts 頂端註解：THEME 工人正在同步改那份檔案）。
const SAKURA = '#ffc4dc' // 愛心徽章主色
const LINE = '#b5708a' // 描邊（取代第一輪的深可可墨線 INK，變柔）
const BERRY = '#5b2a3c' // 徽章內數字／深色文字（取代第一輪的墨色 INK）
const CANDY = '#ff9fc8' // 禮物盒蝴蝶結
const MINT = '#c8f2e1' // 禮物盒身
const SKY = '#cfe8ff' // 禮物盒蓋／緞帶、一般打卡點小旗
const GOLD = '#f6b73c' // 完成打勾貼紙（金色點綴，維持不變）

// 愛心路徑（幾何中心在原點，非頂點凹陷處），供 drawKmHeartBadge／drawStartDot 共用。
// 匯出（HISTORY_MAP_CONTRACT.md）：track/history/skinRouteIcons.ts 的終點小愛心圖示重用這個幾何，
// 不重新刻一份愛心路徑；純新增匯出，函式本身邏輯與既有呼叫端（同檔內）完全不變。
export function heartPath(ctx: CanvasRenderingContext2D, size: number) {
  const s = size / 2
  ctx.beginPath()
  ctx.moveTo(0, s * 0.5)
  ctx.bezierCurveTo(-s * 1.15, -s * 0.3, -s * 0.55, -s * 1.2, 0, -s * 0.5)
  ctx.bezierCurveTo(s * 0.55, -s * 1.2, s * 1.15, -s * 0.3, 0, s * 0.5)
  ctx.closePath()
}

// 每公里「愛心徽章」（docs/skins/CUTE_CONTRACT_R2b.md §D，取代 CONTRACT_R2.md §4.4 第一版）：FIX round2b 根因見
// CuteMap.tsx 的 drawKmMarks() 呼叫處註解——實測（scratchpad cute_skin/mapfix_repro.mjs）證實真正
// 的根因是「畫在面板下面」（位置被蓋住），不是位置算錯或尺寸 0；第一版 26px＋1.4px LINE(#b5708a)
// 細描邊＋白色貼紙邊只露出不到 3.5px，跟退淡前的粉色地圖同時存在也是低對比的次要因素——這裡把
// 尺寸放大、描邊改 2px `#ff6fae`（比 LINE 更深更飽和）、白色貼紙邊加粗、加柔和投影，讓徽章
// 一旦真的落在可見區內就足夠醒目（已實測驗證，見下方 CuteMap.tsx 註解）。sakura 粉色愛心＋白色貼紙
// 白邊＋#ff6fae 細描邊，愛心內 berry 粗體數字。大小固定，呼叫端只給 screen 座標，不疊加地圖 zoom
// 係數，故不隨 zoom 縮放。
//
// FIX（E2E R2b.badge-deltaE-visible 量到 29.72、門檻 ≥30、差距約 1%——根因見 CuteMap.tsx
// drawKmMarks() 呼叫處註解的量測結果：24x24px 量測框只有部分跟愛心輪廓重疊，量到的是「框內平均」
// 被框內背景像素稀釋，不是徽章本身對比不夠）：SIZE 由 30 調到 34、貼紙外框 lineWidth 由 6 調到
// 8（兩者都只是讓愛心＋白邊在同一個 24x24 量測框內佔的面積比例變大，契約原文「約 30px 寬」的
// 「約」本就容許這個級距的微調，不是改色號/改形狀）。用獨立探測腳本（scratchpad cute_skin/
// badge_deltaE_probe.mjs，逐字複製本函式＋跟 E2E 同一套 CIE76 ΔE 數學，在 land/park/water/
// building 四種契約色票背景各量一次)實測：SIZE=30/sticker=6（現行）在 land 背景下 ΔE=27.63；
// SIZE=34/sticker=8 在 land 背景下 ΔE=30.12（park=36.74／water=33.20），已單獨超過 30 門檻，
// 而 E2E 實測全景（有軌跡緞帶一起消失疊加）量到的 29.72 又已經比純背景基準略高，兩者相加後
// 有安全餘裕。
export function drawKmHeartBadge(ctx: CanvasRenderingContext2D, x: number, y: number, km: number, scale = 1) {
  const SIZE = 34
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(scale, scale)
  ctx.lineJoin = 'round'
  // 柔和投影（docs/skins/CUTE_CONTRACT_R2b.md §D 逐字）：掛在白色貼紙邊這一筆描邊上，讓陰影跟著最外層輪廓走，
  // 畫完立刻歸零，避免疊加到後面幾層描邊/填色上變成雙重陰影。
  ctx.save()
  ctx.shadowColor = 'rgba(214,69,127,.35)'
  ctx.shadowBlur = 6
  ctx.shadowOffsetY = 2
  // 白色貼紙外框：同形狀先畫一份加粗描邊墊底，之後被本體蓋掉一半、外側留下可見白邊（契約「白色貼紙
  // 外框」；lineWidth 8＝FIX 後數字，見上方函式頭註解）。
  ctx.strokeStyle = '#ffffff'
  ctx.lineWidth = 8
  heartPath(ctx, SIZE)
  ctx.stroke()
  ctx.restore()
  // 愛心本體：sakura 填色＋2px `#ff6fae` 描邊（比 R2 第一版的 LINE 更深，對比第 A 節退淡後的地圖
  // 更明顯，docs/skins/CUTE_CONTRACT_R2b.md §D 逐字色號）。
  ctx.fillStyle = SAKURA
  heartPath(ctx, SIZE)
  ctx.fill()
  ctx.strokeStyle = '#ff6fae'
  ctx.lineWidth = 2
  heartPath(ctx, SIZE)
  ctx.stroke()
  // 愛心內 berry 粗體數字（字級跟著 SIZE 微調同一比例放大：13/30 ≈ 15/34）。
  ctx.fillStyle = BERRY
  ctx.font = '800 15px "DORCute", "Noto Sans TC", sans-serif'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillText(String(km), 0, SIZE * 0.02)
  ctx.restore()
}

// 起點小圓點（原創、非必要裝飾，CONTRACT_R2.md §4.4）：白底＋line 細描邊小圓點，標示路線起點。
export function drawStartDot(ctx: CanvasRenderingContext2D, x: number, y: number, scale = 1) {
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(scale, scale)
  ctx.beginPath()
  ctx.arc(0, 0, 5, 0, Math.PI * 2)
  ctx.fillStyle = '#ffffff'
  ctx.fill()
  ctx.lineWidth = 1.6
  ctx.strokeStyle = LINE
  ctx.stroke()
  ctx.restore()
}

// 目標點（未完成）：原創小禮物盒——薄荷色盒身＋天空藍緞帶十字＋盒蓋一個小蝴蝶結，line 細描邊
// （CONTRACT_R2.md §4.4：「描邊 line、berry 取代舊墨色」——本圖示只有描邊沒有文字，故只換 line）。
export function drawGiftBox(ctx: CanvasRenderingContext2D, x: number, y: number, scale = 1) {
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(scale, scale)
  ctx.lineJoin = 'round'
  // 盒身
  ctx.fillStyle = MINT
  ctx.strokeStyle = LINE
  ctx.lineWidth = 1.4
  ctx.beginPath(); ctx.rect(-9, -6, 18, 13); ctx.fill(); ctx.stroke()
  // 盒蓋
  ctx.fillStyle = SKY
  ctx.beginPath(); ctx.rect(-10.5, -10, 21, 5); ctx.fill(); ctx.stroke()
  // 緞帶十字
  ctx.fillStyle = SKY
  ctx.fillRect(-2, -6, 4, 13)
  ctx.strokeRect(-2, -6, 4, 13)
  // 蝴蝶結
  ctx.beginPath()
  ctx.moveTo(0, -10)
  ctx.quadraticCurveTo(-6, -16, -1, -12)
  ctx.quadraticCurveTo(0, -10, 0, -10)
  ctx.quadraticCurveTo(1, -12, 6, -16)
  ctx.quadraticCurveTo(0, -10, 0, -10)
  ctx.closePath()
  ctx.fillStyle = CANDY
  ctx.fill()
  ctx.stroke()
  ctx.restore()
}

// 目標點完成：貼上一枚金色打勾貼紙（圓形貼紙＋白色勾勾，疊在禮物盒圖示上方一起呼叫）。
export function drawCheckSticker(ctx: CanvasRenderingContext2D, x: number, y: number, scale = 1) {
  ctx.save()
  ctx.translate(x, y - 4)
  ctx.scale(scale, scale)
  ctx.beginPath()
  ctx.arc(0, 0, 8, 0, Math.PI * 2)
  ctx.fillStyle = GOLD
  ctx.fill()
  ctx.lineWidth = 1.4
  ctx.strokeStyle = '#ffffff'
  ctx.stroke()
  ctx.lineWidth = 0.8
  ctx.strokeStyle = LINE
  ctx.stroke()
  ctx.beginPath()
  ctx.moveTo(-3.6, 0.2)
  ctx.lineTo(-1, 3)
  ctx.lineTo(4, -3.4)
  ctx.strokeStyle = '#ffffff'
  ctx.lineWidth = 1.8
  ctx.lineCap = 'round'
  ctx.lineJoin = 'round'
  ctx.stroke()
  ctx.restore()
}

// 一般打卡點（非目標焦點）：比禮物盒小一號的圓角小旗子（沿用 drawGiftBox 邏輯但簡化成單色小旗），
// kind==='checkpoint' 時使用；'boss'/'focus' 用禮物盒。
export function drawCheckpointFlag(ctx: CanvasRenderingContext2D, x: number, y: number, scale = 1) {
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(scale, scale)
  ctx.strokeStyle = LINE
  ctx.lineWidth = 1.2
  ctx.beginPath(); ctx.moveTo(0, 8); ctx.lineTo(0, -9); ctx.stroke()
  ctx.beginPath()
  ctx.moveTo(0, -9)
  ctx.lineTo(9, -6.5)
  ctx.lineTo(0, -4)
  ctx.closePath()
  ctx.fillStyle = SKY
  ctx.fill()
  ctx.stroke()
  ctx.restore()
}

export function drawTargetIcon(ctx: CanvasRenderingContext2D, x: number, y: number, kind: 'checkpoint' | 'boss' | 'focus', done: boolean, scale = 1) {
  if (kind === 'checkpoint') drawCheckpointFlag(ctx, x, y, scale)
  else drawGiftBox(ctx, x, y, scale)
  if (done) drawCheckSticker(ctx, x, y, scale)
}
