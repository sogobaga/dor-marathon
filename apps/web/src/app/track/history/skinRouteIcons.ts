'use client'

// 歷史軌跡風格地圖（SkinRouteMap.tsx，HISTORY_MAP_CONTRACT.md）專用：把 retro／cute 既有的像素/手繪
// 繪圖函式（原樣重用，不修改那些函式的邏輯）畫到我們自己配置的離屏 canvas 上，轉成
// map.addImage() 需要的 ImageData——本檔只負責「幫既有繪圖函式配一塊剛好夠大、且錨點對齊的畫布」，
// 不重新設計圖案本身（契約：retro 公里旗「沿用 R4 放大後的像素公里旗樣式」、cute 公里徽章「沿用
// icons.ts 的繪法產生 image」）。retro 起終點旗是既有檔案沒有的圖示，屬於契約要求的「原創繪製」，
// 寫在本檔、風格比照 sprites.ts 的旗杆/外框語彙。scifi 不需要本檔任何函式（公里號碼／起終點改用
// MapLibre 原生 circle+symbol 文字圖層，見 SkinRouteMap.tsx，不需要點陣圖示）。
//
// 錨點設計：每個 icon 的離屏 canvas 都刻意讓「繪圖函式的座標原點」落在 canvas 正中央（symbol
// layer 用 icon-anchor:'center'）或正下方中央（icon-anchor:'bottom'，用於 retro 旗——旗杆底部＝
// GPS 座標點，比照「旗子插在地上」的視覺語彙），呼叫端不需要另外算 icon-offset。

import { RETRO_PALETTE } from '@/components/retro/tiles'
import { drawKmFlag } from '../retro/sprites'
import { drawKmHeartBadge, drawStartDot, heartPath } from '../cute/icons'

export interface RouteIcon {
  id: string
  data: ImageData
  pixelRatio: number
}

// 畫布尺寸一律先 ceil 成整數再建立 canvas（浮點運算如 20*4.8 在 JS 裡可能是 96.00000000000001，
// 直接拿來設 canvas.width 會多裁掉一段），並把「ceil 後的整數」一併回傳給呼叫端——呼叫端後續的
// 置中運算（w/2, h）與 getImageData(0,0,w,h) 都必須改用這個整數，不能再用 ceil 前的原始值：
// getImageData 的 sw/sh 參數是 WebIDL `long`（ToInt32 對正數等同無條件捨去），若傳入 canvas 實際
// 寬高的浮點原值，一旦有小數部分就會比 canvas.width（ceil 後）少讀到最後一欄/列，讓截出的
// ImageData 比實際畫布還窄/矮，置中錨點也會偏移半個實體像素。
function offscreenCtx(w: number, h: number): { ctx: CanvasRenderingContext2D; w: number; h: number } {
  const cw = Math.max(1, Math.ceil(w))
  const ch = Math.max(1, Math.ceil(h))
  const canvas = document.createElement('canvas')
  canvas.width = cw
  canvas.height = ch
  return { ctx: canvas.getContext('2d')!, w: cw, h: ch }
}

// ── retro ────────────────────────────────────────────────────────────────

// 2026-09-30 owner 檢視 C:\Users\paris\Downloads\history_preview\ 截圖後回饋定案：R4 版旗子
// （scale=3×dpr，邏輯 CSS 高度約 60px）在歷史回放地圖的比例下「快蓋住整條路線」，這裡把旗子縮到約
// 53%（scale=1.6×dpr，公里數字字級 10×1.6=16 CSS px，仍遠高於 owner 要求的 ≥11 CSS px 門檻）——
// 只影響歷史回放地圖用的這三顆圖示（retroKmIcon／retroStartIcon／retroEndIcon），不動
// RetroMap.tsx 即時追蹤地圖那份 orbScale（走完全不同的呼叫路徑，見上方檔頭說明），也不改
// drawKmFlag() 本體任何邏輯，純粹改呼叫端傳入的 scale 係數。SkinRouteMap.tsx 的 RETRO_FIT_PADDING
// 已跟著等比縮小，兩處要一起調整。
const HISTORY_RETRO_ICON_SCALE = 1.6 // 原 3（見上方沿革），約 53%，落在 owner 要求的 50–55% 區間

// 公里旗：沿用 track/retro/sprites.ts 的 drawKmFlag()（R4 放大後的黑框金黃像素旗＋白字描邊）。
// dpr 只影響輸出解析度，addImage 的 pixelRatio 選項讓 MapLibre 換算回同一個邏輯（CSS px）尺寸，
// 不會因為裝置 DPR 不同而在畫面上顯示得忽大忽小。
// 畫布寬度刻意取「旗面延伸到最遠那一側」的兩倍當半寬，讓 drawKmFlag() 的座標原點（旗杆底部）落在
// canvas 正中央——旗子本身視覺上因此偏右，但錨點（GPS 座標）對齊是正確的，等同其餘 icon 的設計原則。
export function retroKmIcon(km: number, dpr: number): RouteIcon {
  const scale = HISTORY_RETRO_ICON_SCALE * dpr
  const flagW = (String(km).length >= 2 ? 20 : 14) * scale
  const halfW = flagW + 4 * scale // 旗面右緣（flagX+flagW+scale=flagW+2scale）留 2scale 安全邊
  const { ctx, w, h } = offscreenCtx(halfW * 2, 20 * scale) // 20*scale=旗杆+旗面頂端(-19scale)+1scale 安全邊
  ctx.imageSmoothingEnabled = false
  drawKmFlag(ctx, w / 2, h, km, scale)
  return { id: `retro-km-${km}`, data: ctx.getImageData(0, 0, w, h), pixelRatio: dpr }
}

function drawRetroPole(ctx: CanvasRenderingContext2D, s: number) {
  ctx.fillStyle = RETRO_PALETTE[0] // 黑：外框
  ctx.fillRect(-2 * s, -18 * s, 4 * s, 18 * s)
  ctx.fillStyle = RETRO_PALETTE[15] // 灰：旗杆主體
  ctx.fillRect(-1 * s, -17 * s, 2 * s, 16 * s)
}

// 起點旗（原創繪製，既有 sprites.ts 沒有這個圖示）：方形亮綠旗＋黑框，比照公里旗同一套旗杆/外框
// 語彙，只是旗面改單色（無數字）。畫布寬度取兩側最大延伸的兩倍，讓旗杆底部＝canvas 正中央。
export function retroStartIcon(dpr: number): RouteIcon {
  const scale = HISTORY_RETRO_ICON_SCALE * dpr
  const halfW = 17 * scale // 旗面右緣 flagX+flagW+scale = 1s+14s+s = 16s，留 1scale 安全邊
  const { ctx, w, h } = offscreenCtx(halfW * 2, 20 * scale)
  ctx.imageSmoothingEnabled = false
  ctx.save()
  ctx.translate(w / 2, h)
  drawRetroPole(ctx, scale)
  const flagX = 1 * scale, flagY = -18 * scale, flagW = 14 * scale, flagH = 12 * scale
  ctx.fillStyle = RETRO_PALETTE[0]
  ctx.fillRect(flagX - scale, flagY - scale, flagW + 2 * scale, flagH + 2 * scale)
  ctx.fillStyle = RETRO_PALETTE[2] // 亮綠
  ctx.fillRect(flagX, flagY, flagW, flagH)
  ctx.restore()
  return { id: 'retro-start', data: ctx.getImageData(0, 0, w, h), pixelRatio: dpr }
}

// 終點旗（原創繪製）：格子旗——旗面內畫 4×3 黑白棋盤格，純幾何色塊語彙（非任何商業素材），畫布規格
// 與起點旗相同。
export function retroEndIcon(dpr: number): RouteIcon {
  const scale = HISTORY_RETRO_ICON_SCALE * dpr
  const halfW = 17 * scale
  const { ctx, w, h } = offscreenCtx(halfW * 2, 20 * scale)
  ctx.imageSmoothingEnabled = false
  ctx.save()
  ctx.translate(w / 2, h)
  drawRetroPole(ctx, scale)
  const flagX = 1 * scale, flagY = -18 * scale, flagW = 14 * scale, flagH = 12 * scale
  ctx.fillStyle = RETRO_PALETTE[0]
  ctx.fillRect(flagX - scale, flagY - scale, flagW + 2 * scale, flagH + 2 * scale)
  const cols = 4, rows = 3
  const cw = flagW / cols, ch = flagH / rows
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      ctx.fillStyle = (r + c) % 2 === 0 ? RETRO_PALETTE[1] : RETRO_PALETTE[0]
      ctx.fillRect(flagX + c * cw, flagY + r * ch, cw, ch)
    }
  }
  ctx.restore()
  return { id: 'retro-end', data: ctx.getImageData(0, 0, w, h), pixelRatio: dpr }
}

// ── cute ─────────────────────────────────────────────────────────────────

// 公里徽章：沿用 track/cute/icons.ts 的 drawKmHeartBadge()（粉紅愛心＋白色貼紙邊＋berry 粗體數字），
// 該函式內部 SIZE 固定 34（不隨呼叫端 scale 改變比例），這裡只用 scale 參數(=dpr)提高輸出解析度。
// heartPath 幾何中心在原點，愛心＋貼紙邊＋陰影的總延伸半徑約 30（SIZE 局部單位），pad 留 14 保守值。
export function cuteKmIcon(km: number, dpr: number): RouteIcon {
  const SIZE = 34, pad = 14
  const { ctx, w, h } = offscreenCtx((SIZE + pad * 2) * dpr, (SIZE + pad * 2) * dpr)
  drawKmHeartBadge(ctx, w / 2, h / 2, km, dpr)
  return { id: `cute-km-${km}`, data: ctx.getImageData(0, 0, w, h), pixelRatio: dpr }
}

// 起點：沿用 drawStartDot()（白底＋粉框小圓點），幾何中心在原點。
export function cuteStartIcon(dpr: number): RouteIcon {
  const { ctx, w, h } = offscreenCtx(20 * dpr, 20 * dpr)
  drawStartDot(ctx, w / 2, h / 2, dpr)
  return { id: 'cute-start', data: ctx.getImageData(0, 0, w, h), pixelRatio: dpr }
}

// 終點：小愛心（契約「終點小愛心」）——沿用 icons.ts 匯出的 heartPath() 幾何（唯一為本檔新匯出的
// helper，向後相容、不影響原檔任何既有行為），換一組較小的實心填色＋白色描邊，不疊數字。
export function cuteEndIcon(dpr: number): RouteIcon {
  const SIZE = 18, pad = 6
  const { ctx, w, h } = offscreenCtx((SIZE + pad * 2) * dpr, (SIZE + pad * 2) * dpr)
  ctx.save()
  ctx.translate(w / 2, h / 2)
  ctx.scale(dpr, dpr)
  ctx.lineJoin = 'round'
  ctx.fillStyle = '#ff6fae'
  heartPath(ctx, SIZE)
  ctx.fill()
  ctx.strokeStyle = '#ffffff'
  ctx.lineWidth = 2
  heartPath(ctx, SIZE)
  ctx.stroke()
  ctx.restore()
  return { id: 'cute-end', data: ctx.getImageData(0, 0, w, h), pixelRatio: dpr }
}
