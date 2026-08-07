import type { Certificate } from './api'

const CJK = "'Noto Sans TC','PingFang TC','Microsoft JhengHei',sans-serif"
const GOLD = '#E5C46B'
const GREEN = '#46E3A0'

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

  // 文字疊加區（自訂底圖時加柔和垂直漸層底襯提升可讀性，不完全壓暗底圖）
  ctx.textAlign = 'center'
  const maskY = 345
  const maskH = 490
  if (custom) {
    const grad = ctx.createLinearGradient(0, maskY, 0, maskY + maskH)
    grad.addColorStop(0, 'rgba(0,0,0,0)')
    grad.addColorStop(0.15, 'rgba(0,0,0,.14)')
    grad.addColorStop(0.85, 'rgba(0,0,0,.14)')
    grad.addColorStop(1, 'rgba(0,0,0,0)')
    ctx.fillStyle = grad
    ctx.fillRect(0, maskY, W, maskH)
  }
  ctx.shadowColor = 'rgba(0,0,0,.45)'
  ctx.shadowBlur = custom ? 8 : 0

  const maxTextW = W * 0.86 // shrink-to-fit 可用寬度上限

  ctx.fillStyle = custom ? 'rgba(255,255,255,.8)' : 'rgba(255,255,255,.65)'
  ctx.font = `400 36px ${CJK}`
  ctx.fillText('茲證明', cx, 395)

  ctx.fillStyle = GOLD
  const nameText = cert.name || '跑者'
  fitFontSize(ctx, nameText, 800, 100, 40, maxTextW)
  ctx.fillText(nameText, cx, 505)

  ctx.fillStyle = 'rgba(255,255,255,.9)'
  const raceText = `完成「${cert.race_title}」`
  fitFontSize(ctx, raceText, 500, 46, 24, maxTextW)
  ctx.fillText(raceText, cx, 585)

  if (cert.group_name) {
    ctx.fillStyle = 'rgba(255,255,255,.65)'
    fitFontSize(ctx, cert.group_name, 400, 32, 20, maxTextW)
    ctx.fillText(cert.group_name, cx, 635)
  }

  const stats: [string, string][] = [
    ['完成里程', `${cert.completed_km.toFixed(1)} K`],
    ['完成時間', fmtDuration(cert.total_time_s)],
    ['完成名次', cert.finish_rank > 0 ? `第 ${cert.finish_rank} 名` : '—'],
  ]
  const colW = (W - 200) / 3
  const baseY = 705
  stats.forEach(([label, value], i) => {
    const x = 100 + colW * i + colW / 2
    ctx.fillStyle = GREEN
    fitFontSize(ctx, value, 800, 56, 28, colW * 0.92)
    ctx.fillText(value, x, baseY)
    ctx.fillStyle = 'rgba(255,255,255,.6)'
    ctx.font = `400 26px ${CJK}`
    ctx.fillText(label, x, baseY + 45)
  })

  ctx.fillStyle = 'rgba(255,255,255,.78)'
  ctx.font = `400 32px ${CJK}`
  ctx.fillText(`完成日期　${fmtDate(cert.completion_at)}`, cx, 805)
  ctx.shadowBlur = 0

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
