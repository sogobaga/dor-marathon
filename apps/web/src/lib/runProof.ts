// 運動證明圖產生工具（見台灣運動部「揮汗有禮」活動 500.gov.tw，2026-09）：跑完步後一鍵產生一張含
// 「日期、運動時間、距離」的乾淨證明圖卡片，供使用者上傳 500.gov.tw 完成當週任務。純 canvas 繪製、
// 不載入任何外部圖片/字型（全部系統字型），避免行動裝置上 CORS/字型載入失敗導致產圖卡住或空白。
// 入口可見性見 DashboardInfo.gov500_entry（後端 profile/membership.go resolveEntry），本檔本身無 gate。

export interface RunProofInput {
  startedAt: Date
  durationS: number
  distanceKm: number
  avgPaceS: number | null
  displayName: string
}

const CARD_W = 1080
const CARD_H = 1350
// 系統字型堆疊（含中/英文常見字型，不額外載入任何 webfont）
const FONT = `-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang TC", "Microsoft JhengHei", sans-serif`

function fmtDateTime(d: Date): string {
  const y = d.getFullYear(), mo = d.getMonth() + 1, day = d.getDate()
  const hh = String(d.getHours()).padStart(2, '0'), mm = String(d.getMinutes()).padStart(2, '0')
  return `${y}/${mo}/${day} ${hh}:${mm}`
}

// 運動時間：h:mm:ss（無小時則 mm:ss，比照 track 頁 fmtTime 慣例）
function fmtDuration(s: number): string {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(sec)}` : `${m}:${p(sec)}`
}

// 平均配速：m:ss /km
function fmtPace(s: number): string {
  const m = Math.floor(s / 60), sec = Math.round(s % 60)
  return `${m}:${String(sec).padStart(2, '0')} /km`
}

interface ProofRow { label: string; value: string; big?: boolean }

function drawRow(ctx: CanvasRenderingContext2D, x: number, slotTop: number, slotH: number, row: ProofRow) {
  ctx.textAlign = 'left'
  ctx.font = `600 28px ${FONT}`
  ctx.fillStyle = '#8b93a1'
  ctx.fillText(row.label, x, slotTop + 34)
  const valueSize = row.big ? 140 : 78
  ctx.font = `800 ${valueSize}px ${FONT}`
  ctx.fillStyle = '#ffffff'
  ctx.fillText(row.value, x, slotTop + slotH - (row.big ? 30 : 26))
}

// generateRunProofImage：畫一張 1080×1350 直式證明卡（接近手機截圖比例），回傳 PNG Blob。
export async function generateRunProofImage(input: RunProofInput): Promise<Blob> {
  const canvas = document.createElement('canvas')
  canvas.width = CARD_W
  canvas.height = CARD_H
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('無法建立畫布（canvas 不支援）')

  // 深色底
  ctx.fillStyle = '#0d0f14'
  ctx.fillRect(0, 0, CARD_W, CARD_H)

  // 品牌列：DOR（白）｜城市探索（綠色點綴）＋副標
  const brandY = 100
  ctx.textAlign = 'left'
  ctx.font = `800 44px ${FONT}`
  ctx.fillStyle = '#ffffff'
  ctx.fillText('DOR', 60, brandY)
  const dorW = ctx.measureText('DOR').width
  ctx.fillStyle = '#46E3A0'
  ctx.fillText('｜城市探索', 60 + dorW, brandY)

  ctx.font = `500 26px ${FONT}`
  ctx.fillStyle = '#8b93a1'
  ctx.fillText('GPS 跑步紀錄證明', 60, brandY + 40)

  ctx.strokeStyle = 'rgba(255,255,255,.08)'
  ctx.lineWidth = 2
  ctx.beginPath()
  ctx.moveTo(60, brandY + 70)
  ctx.lineTo(CARD_W - 60, brandY + 70)
  ctx.stroke()

  // 主體大字：日期時間／距離（特大）／運動時間／平均配速（無資料則略過），各配小灰標籤
  const rows: ProofRow[] = [
    { label: '日期', value: fmtDateTime(input.startedAt) },
    { label: '距離', value: `${input.distanceKm.toFixed(2)} km`, big: true },
    { label: '運動時間', value: fmtDuration(input.durationS) },
  ]
  if (input.avgPaceS != null && input.avgPaceS > 0) {
    rows.push({ label: '平均配速', value: fmtPace(input.avgPaceS) })
  }

  const areaTop = brandY + 100
  const areaBottom = CARD_H - 170 // 留給底部品牌列
  const slotH = (r: ProofRow) => (r.big ? 300 : 210)
  const totalH = rows.reduce((s, r) => s + slotH(r), 0)
  let y = areaTop + Math.max(0, (areaBottom - areaTop - totalH) / 2)
  for (const row of rows) {
    const h = slotH(row)
    drawRow(ctx, 60, y, h, row)
    y += h
  }

  // 底部：跑者顯示名稱 + 網址
  ctx.strokeStyle = 'rgba(255,255,255,.08)'
  ctx.beginPath()
  ctx.moveTo(60, CARD_H - 130)
  ctx.lineTo(CARD_W - 60, CARD_H - 130)
  ctx.stroke()

  ctx.textAlign = 'left'
  ctx.font = `700 32px ${FONT}`
  ctx.fillStyle = '#ffffff'
  ctx.fillText(input.displayName || 'DOR 跑者', 60, CARD_H - 78)

  ctx.textAlign = 'right'
  ctx.font = `500 26px ${FONT}`
  ctx.fillStyle = '#46E3A0'
  ctx.fillText('www.dor.tw', CARD_W - 60, CARD_H - 78)

  const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
  if (!blob) throw new Error('證明圖產生失敗')
  return blob
}

// shareRunProof：優先叫出系統分享面板（可直接存到相簿/傳送）；不支援、或分享失敗（非使用者取消）
// 則退回開新分頁顯示圖片，讓使用者長按儲存。使用者主動取消分享（AbortError）視為流程已正常結束，
// 不再退回開分頁（避免使用者按了取消卻又跳出一個新分頁造成困惑）。
export async function shareRunProof(blob: Blob): Promise<'shared' | 'downloaded'> {
  const file = new File([blob], 'dor-run-proof.png', { type: 'image/png' })
  const shareable =
    typeof navigator !== 'undefined' &&
    typeof navigator.share === 'function' &&
    !!navigator.canShare?.({ files: [file] })
  if (shareable) {
    try {
      await navigator.share({ files: [file] })
      return 'shared'
    } catch (e: unknown) {
      if (e instanceof Error && e.name === 'AbortError') return 'shared' // 使用者取消＝成功結束，不退回
      // 其餘失敗（權限被拒/裝置臨時不支援等）→ 往下退回另存
    }
  }
  const url = URL.createObjectURL(blob)
  window.open(url, '_blank')
  return 'downloaded'
}

// 依 DOM 實況擷取（html2canvas 動態載入，按下才抓 ~200KB chunk）：把歷史詳情區塊
//（日期/GPS 軌跡地圖/距離·時間·配速統計/每公里分段計量表）**原樣**轉成圖片——與畫面一致，
// 避免「另外合成的卡片看起來像作假」的疑慮；只在底部追加 DOR 署名列，不改動任何數據呈現。
// 地圖磚是跨網域圖片：useCORS 讓 html2canvas 以 crossOrigin=anonymous 重抓（OSM 磚有 ACAO:*）。
// 標了 data-proof-ignore="1" 的節點（關閉鈕、活動卡片本身）不入鏡。
export async function captureRunProofFromDom(
  el: HTMLElement,
  displayName: string,
  opts?: { mapOverlay?: { el: HTMLElement; canvas: HTMLCanvasElement; selector: string } },
): Promise<Blob> {
  const html2canvas = (await import('html2canvas')).default
  const bg = getComputedStyle(document.body).backgroundColor || '#0d0f14'
  const mapSel = opts?.mapOverlay?.selector
  const shot = await html2canvas(el, {
    scale: 2, useCORS: true, backgroundColor: bg, logging: false,
    // 活地圖「內容」全部不拍（容器本身保留當底框）：v724 實測 html2canvas 會把帶 CORS 的磚
    // 依算錯的縮放 transform 放大、且未遵守容器 overflow 裁切，直接噴到頁面下半部。
    // 地圖畫面一律只由下方的快照疊圖提供。
    ignoreElements: (node) => {
      const h = node as HTMLElement
      if (h.dataset?.proofIgnore === '1') return true
      if (mapSel && typeof h.closest === 'function' && h.closest(mapSel) && !(typeof h.matches === 'function' && h.matches(mapSel))) return true
      return false
    },
  })
  // Leaflet 地圖用「成品後製疊圖」：把預繪快照（snapshotMap，與畫面同座標系）直接 drawImage
  // 蓋到成品上地圖所在的位置。演進備忘——v719 讓 html2canvas 自己拍活地圖：多層 translate3d
  // 疊層算不準 → 軌跡偏移；v720-723 在 clone 注入 dataURL <img> 替換：html2canvas 圖片管線
  // 沒把它畫出來 → 整塊空白。直接畫在成品 canvas 上沒有任何中間層，不再受它的管線影響；
  // 活地圖在底下拍成什麼樣都無所謂，會被快照完整蓋掉。
  if (opts?.mapOverlay) {
    const rootR = el.getBoundingClientRect()
    const mapR = opts.mapOverlay.el.getBoundingClientRect()
    const sx = shot.width / rootR.width // 實際輸出縮放（≈scale 2，以量到的為準）
    const x = (mapR.left - rootR.left) * sx
    const y = (mapR.top - rootR.top) * sx
    const w = mapR.width * sx
    const h = mapR.height * sx
    const sctx = shot.getContext('2d')
    if (sctx) {
      sctx.save()
      // 沿用容器圓角裁切（roundRect iOS16+；不支援就方角，僅四角些微出界、可接受）
      const rr = (sctx as CanvasRenderingContext2D & { roundRect?: (x: number, y: number, w: number, h: number, r: number) => void }).roundRect
      if (typeof rr === 'function') { sctx.beginPath(); rr.call(sctx, x, y, w, h, 10 * sx); sctx.clip() }
      sctx.drawImage(opts.mapOverlay.canvas, x, y, w, h)
      sctx.restore()
    }
  }
  const footerH = 84
  const out = document.createElement('canvas')
  out.width = shot.width
  out.height = shot.height + footerH
  const ctx = out.getContext('2d')
  if (!ctx) throw new Error('canvas unsupported')
  ctx.fillStyle = bg
  ctx.fillRect(0, 0, out.width, out.height)
  ctx.drawImage(shot, 0, 0)
  ctx.fillStyle = '#0d0f14' // 署名列固定深色（與 skin 無關），與內容區以色塊自然分隔
  ctx.fillRect(0, shot.height, out.width, footerH)
  ctx.textBaseline = 'middle'
  ctx.fillStyle = '#46E3A0'
  ctx.font = `bold ${Math.round(footerH * 0.34)}px -apple-system, "PingFang TC", "Microsoft JhengHei", sans-serif`
  ctx.fillText('DOR｜城市探索', 28, shot.height + footerH / 2)
  ctx.fillStyle = 'rgba(255,255,255,.78)'
  ctx.font = `${Math.round(footerH * 0.26)}px -apple-system, "PingFang TC", "Microsoft JhengHei", sans-serif`
  const right = `${displayName ? displayName + ' · ' : ''}www.dor.tw`
  ctx.fillText(right, out.width - ctx.measureText(right).width - 28, shot.height + footerH / 2)
  return await new Promise<Blob>((resolve, reject) => out.toBlob((b) => (b ? resolve(b) : reject(new Error('toBlob failed'))), 'image/png'))
}
