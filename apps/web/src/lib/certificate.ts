import type { Certificate } from './api'

const CJK = "'Noto Sans TC','PingFang TC','Microsoft JhengHei',sans-serif"
const GOLD = '#E5C46B'
const GREEN = '#46E3A0'

// 完賽證明文字層字型堆疊／色票（依設計規格）
const FONT = '"Noto Sans TC", "Microsoft JhengHei", "PingFang TC", sans-serif'
const NAVY = '#112C57'
const MUTED = '#596579'
const GOLD_ACCENT = '#AC7E1A'
const RED = '#B92B31'
const BLUE = '#174B8B'

function fmtDuration(s: number): string {
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  return h > 0 ? `${h} 時 ${m} 分 ${sec} 秒` : `${m} 分 ${sec} 秒`
}

function fmtDate(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`
}

function loadImage(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = reject
    // 目前底圖同源 /api/v1/images/* 加這行無害；預留將來底圖搬到 img.dor.tw(R2)
    // → R2 需回 Access-Control-Allow-Origin，否則跨來源底圖會載入失敗且污染 canvas
    img.crossOrigin = 'anonymous'
    img.src = src
  })
}

// 依可用寬度動態縮小字級，避免長姓名/賽事名/分組溢出畫布（會就地設定 ctx.font）
function fitFontSize(
  ctx: CanvasRenderingContext2D,
  text: string,
  weight: number,
  maxSize: number,
  minSize: number,
  maxWidth: number,
): number {
  let size = maxSize
  for (; size > minSize; size -= 2) {
    ctx.font = `${weight} ${size}px ${CJK}`
    if (ctx.measureText(text).width <= maxWidth) break
  }
  ctx.font = `${weight} ${size}px ${CJK}`
  return size
}

// 圓角矩形路徑 helper：優先用原生 roundRect，不支援時退回手繪路徑
function roundRectPath(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  const rr = Math.min(r, w / 2, h / 2)
  ctx.beginPath()
  if (typeof ctx.roundRect === 'function') {
    ctx.roundRect(x, y, w, h, rr)
  } else {
    ctx.moveTo(x + rr, y)
    ctx.arcTo(x + w, y, x + w, y + h, rr)
    ctx.arcTo(x + w, y + h, x, y + h, rr)
    ctx.arcTo(x, y + h, x, y, rr)
    ctx.arcTo(x, y, x + w, y, rr)
    ctx.closePath()
  }
}

// 預設底圖設計（無自訂底圖時用）：深色金框 + 品牌 + 標題
function drawDefaultBackground(ctx: CanvasRenderingContext2D, W: number, H: number, cx: number) {
  ctx.fillStyle = '#0b0f0d'
  ctx.fillRect(0, 0, W, H)
  const grad = ctx.createLinearGradient(0, 0, W, H)
  grad.addColorStop(0, '#101a16')
  grad.addColorStop(1, '#0a0d0c')
  ctx.fillStyle = grad
  ctx.fillRect(0, 0, W, H)

  ctx.strokeStyle = GOLD
  ctx.lineWidth = 4
  ctx.strokeRect(36, 36, W - 72, H - 72)
  ctx.lineWidth = 1
  ctx.strokeRect(50, 50, W - 100, H - 100)

  ctx.textAlign = 'center'
  ctx.fillStyle = GREEN
  ctx.font = `600 22px ${CJK}`
  ctx.fillText('D O R　·　雲 端 馬 拉 松', cx, 130)
  ctx.fillStyle = '#ffffff'
  ctx.font = `800 76px ${CJK}`
  ctx.fillText('完 賽 證 明', cx, 240)
  ctx.fillStyle = GOLD
  ctx.font = `500 20px ${CJK}`
  ctx.fillText('C E R T I F I C A T E   O F   C O M P L E T I O N', cx, 285)
  ctx.strokeStyle = 'rgba(229,196,107,.5)'
  ctx.lineWidth = 2
  ctx.beginPath()
  ctx.moveTo(cx - 120, 320)
  ctx.lineTo(cx + 120, 320)
  ctx.stroke()
}

export interface CertificateRender {
  dataUrl: string // 給 <img> 預覽用
  blob: Blob | null // 給下載用；toBlob 失敗時為 null（下載會退回 dataUrl）
}

// 繪製完賽證明（有自訂底圖則疊在底圖上）；同時回傳預覽用 dataURL 與下載用 Blob
export async function renderCertificate(cert: Certificate): Promise<CertificateRender> {
  const W = 1240
  const H = 877
  const cx = W / 2
  const canvas = document.createElement('canvas')
  canvas.width = W
  canvas.height = H
  const ctx = canvas.getContext('2d')
  if (!ctx) return { dataUrl: '', blob: null }

  const custom = !!cert.bg_url
  if (custom) {
    try {
      const img = await loadImage(cert.bg_url!)
      // cover 填滿
      const scale = Math.max(W / img.width, H / img.height)
      const dw = img.width * scale
      const dh = img.height * scale
      ctx.drawImage(img, (W - dw) / 2, (H - dh) / 2, dw, dh)
    } catch {
      drawDefaultBackground(ctx, W, H, cx)
    }
  } else {
    drawDefaultBackground(ctx, W, H, cx)
  }

  // ---- 文字與容器層（背景已畫完）----
  // 規格明訂不可用深色遮罩／粗重陰影：容器與文字一律直接畫在底圖上
  ctx.textAlign = 'center'
  ctx.shadowColor = 'transparent'
  ctx.shadowBlur = 0

  // ① 成績資訊卡（白色半透明圓角卡）+ 兩條垂直分隔線
  roundRectPath(ctx, 198, 573, 844, 128, 20)
  ctx.fillStyle = 'rgba(255,255,255,0.72)'
  ctx.fill()
  ctx.strokeStyle = 'rgba(17,44,87,0.08)'
  ctx.lineWidth = 1.5
  ctx.stroke()

  ctx.strokeStyle = 'rgba(17,44,87,0.12)'
  ctx.lineWidth = 1.5
  for (const dividerX of [478, 762]) {
    ctx.beginPath()
    ctx.moveTo(dividerX, 599)
    ctx.lineTo(dividerX, 677)
    ctx.stroke()
  }

  // ② 日期膠囊底（先用日期字串量寬決定膠囊寬度）
  const dateText = `完成日期｜${fmtDate(cert.completion_at)}`
  ctx.font = `700 26px ${FONT}`
  const dateTextW = ctx.measureText(dateText).width
  const pillW = Math.max(300, dateTextW + 70)
  const pillH = 54
  const pillX = 617 - pillW / 2
  const pillY = 719
  roundRectPath(ctx, pillX, pillY, pillW, pillH, 27)
  ctx.fillStyle = 'rgba(255,255,255,0.72)'
  ctx.fill()
  ctx.strokeStyle = 'rgba(17,44,87,0.08)'
  ctx.lineWidth = 1.5
  ctx.stroke()

  // ③ 所有文字欄位（含姓名金線）
  // 標題
  ctx.fillStyle = MUTED
  ctx.font = `400 28px ${FONT}`
  ctx.letterSpacing = '6px'
  ctx.fillText('完賽證明', 620, 307)
  ctx.letterSpacing = '0px'

  // 姓名 + 金色底線
  const nameText = cert.name || '跑者'
  let size = fitFontSize(ctx, nameText, 700, 76, 50, 620)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = NAVY
  ctx.fillText(nameText, 620, 400)
  const nameW = ctx.measureText(nameText).width
  const barW = Math.min(280, Math.max(100, nameW * 0.72))
  roundRectPath(ctx, 620 - barW / 2, 425, barW, 6, 3)
  ctx.fillStyle = GOLD_ACCENT
  ctx.fill()

  // 完成賽事
  const raceText = `完成「${cert.race_title}」`
  size = fitFontSize(ctx, raceText, 700, 34, 27, 850)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = NAVY
  ctx.fillText(raceText, 620, 490)

  // 賽事細項（分組行；無分組資料則略過此欄）
  if (cert.group_name) {
    size = fitFontSize(ctx, cert.group_name, 400, 27, 22, 760)
    ctx.font = `400 ${size}px ${FONT}`
    ctx.fillStyle = MUTED
    ctx.fillText(cert.group_name, 620, 533)
  }

  // 完成里程
  ctx.fillStyle = MUTED
  ctx.font = `400 20px ${FONT}`
  ctx.fillText('完成里程', 337, 601)
  const kmText = `${cert.completed_km.toFixed(1)} K`
  size = fitFontSize(ctx, kmText, 700, 45, 36, 230)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = RED
  ctx.fillText(kmText, 337, 655)

  // 完成時間
  ctx.fillStyle = MUTED
  ctx.font = `400 20px ${FONT}`
  ctx.fillText('完成時間', 620, 601)
  const timeText = fmtDuration(cert.total_time_s)
  size = fitFontSize(ctx, timeText, 700, 37, 30, 250)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = BLUE
  ctx.fillText(timeText, 620, 655)

  // 完成名次（無名次資料時顯示 —，不整欄消失）
  ctx.fillStyle = MUTED
  ctx.font = `400 20px ${FONT}`
  ctx.fillText('完成名次', 903, 601)
  const rankText = cert.finish_rank > 0 ? `第 ${cert.finish_rank} 名` : '—'
  size = fitFontSize(ctx, rankText, 700, 45, 36, 230)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = GOLD_ACCENT
  ctx.fillText(rankText, 903, 655)

  // 完成日期（文字疊在②的膠囊底上）
  size = fitFontSize(ctx, dateText, 700, 26, 21, 390)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = NAVY
  ctx.fillText(dateText, 617, 744)

  const dataUrl = canvas.toDataURL('image/png')
  const blob = await new Promise<Blob | null>((resolve) => {
    try {
      canvas.toBlob((b) => resolve(b), 'image/png')
    } catch {
      resolve(null)
    }
  })
  return { dataUrl, blob }
}

function sanitizeFilename(name: string): string {
  // 過濾檔名非法字元，避免部分系統存檔失敗
  return name.replace(/[\\/:*?"<>|]/g, '_')
}

// 觸發下載：優先用 Blob URL（data: URL 的 <a download> 在手機/WebView 常失效）
// 若瀏覽器不支援 <a>.download（常見於 iOS Safari/WebView）→ 改開新分頁，讓使用者長按存檔
export function downloadCertificate(render: CertificateRender, filename: string) {
  const safeName = sanitizeFilename(filename)
  if (render.blob) {
    const url = URL.createObjectURL(render.blob)
    const a = document.createElement('a')
    if ('download' in a) {
      a.href = url
      a.download = safeName
      document.body.appendChild(a)
      a.click()
      a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 4000)
    } else {
      window.open(url, '_blank')
      setTimeout(() => URL.revokeObjectURL(url), 60000)
    }
    return
  }
  // 後備：toBlob 失敗（極少見）時退回 data URL 舊方式
  const a = document.createElement('a')
  a.href = render.dataUrl
  a.download = safeName
  document.body.appendChild(a)
  a.click()
  a.remove()
}
