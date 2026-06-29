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
    img.src = src // 同源 /api/v1/images/* → 不會污染 canvas
  })
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

// 繪製完賽證明為 PNG dataURL（有自訂底圖則疊在底圖上）
export async function renderCertificate(cert: Certificate): Promise<string> {
  const W = 1240
  const H = 877
  const cx = W / 2
  const canvas = document.createElement('canvas')
  canvas.width = W
  canvas.height = H
  const ctx = canvas.getContext('2d')
  if (!ctx) return ''

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

  // 文字疊加區（自訂底圖時加半透明底襯提升可讀性）
  ctx.textAlign = 'center'
  if (custom) {
    ctx.fillStyle = 'rgba(0,0,0,.32)'
    ctx.fillRect(0, 360, W, 430)
  }
  ctx.shadowColor = 'rgba(0,0,0,.45)'
  ctx.shadowBlur = custom ? 8 : 0

  ctx.fillStyle = custom ? 'rgba(255,255,255,.8)' : 'rgba(255,255,255,.65)'
  ctx.font = `400 24px ${CJK}`
  ctx.fillText('茲證明', cx, 410)
  ctx.fillStyle = GOLD
  ctx.font = `800 64px ${CJK}`
  ctx.fillText(cert.name || '跑者', cx, 482)

  ctx.fillStyle = 'rgba(255,255,255,.9)'
  ctx.font = `500 30px ${CJK}`
  ctx.fillText(`完成「${cert.race_title}」`, cx, 550)
  if (cert.group_name) {
    ctx.fillStyle = 'rgba(255,255,255,.65)'
    ctx.font = `400 22px ${CJK}`
    ctx.fillText(cert.group_name, cx, 588)
  }

  const stats: [string, string][] = [
    ['完成里程', `${cert.completed_km.toFixed(1)} K`],
    ['完成時間', fmtDuration(cert.total_time_s)],
    ['完成名次', cert.finish_rank > 0 ? `第 ${cert.finish_rank} 名` : '—'],
  ]
  const colW = (W - 200) / 3
  const baseY = 668
  stats.forEach(([label, value], i) => {
    const x = 100 + colW * i + colW / 2
    ctx.fillStyle = GREEN
    ctx.font = `800 36px ${CJK}`
    ctx.fillText(value, x, baseY)
    ctx.fillStyle = 'rgba(255,255,255,.6)'
    ctx.font = `400 18px ${CJK}`
    ctx.fillText(label, x, baseY + 34)
  })

  ctx.fillStyle = 'rgba(255,255,255,.78)'
  ctx.font = `400 22px ${CJK}`
  ctx.fillText(`完成日期　${fmtDate(cert.completion_at)}`, cx, 770)
  ctx.shadowBlur = 0

  return canvas.toDataURL('image/png')
}

// 觸發下載
export function downloadDataURL(dataURL: string, filename: string) {
  const a = document.createElement('a')
  a.href = dataURL
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
}
