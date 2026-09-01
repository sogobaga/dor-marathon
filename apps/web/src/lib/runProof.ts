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
