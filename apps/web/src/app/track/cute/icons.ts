// 溫馨可愛（cute）GPS 地圖 — 原創路線裝飾圖示（docs/skins/CUTE_CONTRACT.md§4）：每 100m 小愛心、每公里
// 圓角小旗「NK」、目標點原創小禮物盒／完成打勾貼紙。全部程式逐筆 canvas 繪製，零外部素材、零吉伊卡哇
// 造型語彙（純幾何：愛心/星形/矩形/圓角矩形），與 track/retro/sprites.ts 對應功能（drawKmFlag／
// drawTargetIcon）同等地位但改走手繪可愛風（平滑抗鋸齒，非像素風）。

const INK = '#3d2b2b'
const PINK = '#ec6fae'
const PINK_LIGHT = '#ff9fc6'
const CREAM = '#fff8ec'
const GOLD = '#f6b73c'
const MINT = '#bfeede'
const SKY = '#a9dcf5'

// 小愛心（軌跡每 100m 一顆）：改用 components/cute/decor.ts 的共用 drawHeart()（與全站背景層同一份
// 原創美術資料，避免兩處各刻一套、風格不一致），見 CuteMap.tsx 的呼叫端。

// 手刻圓角矩形路徑（不依賴 CanvasRenderingContext2D.roundRect，較新瀏覽器才有此 API——手刻版本
// 相容性最好，也不必處理「有沒有這個方法」的執行期分支）。呼叫端需自行 ctx.fill()/ctx.stroke()。
function roundRectPath(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.arcTo(x + w, y, x + w, y + h, r)
  ctx.arcTo(x + w, y + h, x, y + h, r)
  ctx.arcTo(x, y + h, x, y, r)
  ctx.arcTo(x, y, x + w, y, r)
  ctx.closePath()
}

// 每公里圓角小旗：白色圓角小旗牌＋桃紅描邊，插在細旗杆上，牌內寫「NK」（N＝公里數）。
export function drawKmFlag(ctx: CanvasRenderingContext2D, x: number, y: number, km: number, scale = 1) {
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(scale, scale)
  // 旗杆
  ctx.strokeStyle = INK
  ctx.lineWidth = 1.4
  ctx.lineCap = 'round'
  ctx.beginPath()
  ctx.moveTo(0, 2)
  ctx.lineTo(0, -16)
  ctx.stroke()
  // 圓角旗牌
  const w = 22, h = 13, rx = 6, x0 = 1, y0 = -16
  roundRectPath(ctx, x0, y0, w, h, rx)
  ctx.fillStyle = '#ffffff'
  ctx.fill()
  ctx.lineWidth = 1.6
  ctx.strokeStyle = PINK
  ctx.stroke()
  ctx.fillStyle = INK
  ctx.font = '700 9px "DORCute", "Noto Sans TC", sans-serif'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillText(`${km}K`, x0 + w / 2, y0 + h / 2 + 0.5)
  ctx.restore()
}

// 目標點（未完成）：原創小禮物盒——薄荷色盒身＋天空藍緞帶十字＋盒蓋一個小蝴蝶結，墨線描邊。
export function drawGiftBox(ctx: CanvasRenderingContext2D, x: number, y: number, scale = 1) {
  ctx.save()
  ctx.translate(x, y)
  ctx.scale(scale, scale)
  ctx.lineJoin = 'round'
  // 盒身
  ctx.fillStyle = MINT
  ctx.strokeStyle = INK
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
  ctx.fillStyle = PINK_LIGHT
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
  ctx.strokeStyle = INK
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
  ctx.strokeStyle = INK
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
