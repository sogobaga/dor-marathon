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

// 純函數：依可用寬度把文字折行（拉丁字母/數字/半形符號連續片段整段折行、CJK 逐字折行）
// 例如 "2026" 不會被拆成 "20"／"26" 兩截。measureFn 由呼叫端注入：
// canvas 內用 ctx.measureText；離線單元測試可傳入寬度 stub，不依賴瀏覽器 canvas 環境。
export function wrapTextByWidth(text: string, maxWidth: number, measureFn: (s: string) => number): string[] {
  const tokens: string[] = []
  let asciiBuf = ''
  for (const ch of text) {
    // U+2E80（CJK 部首補充區起點）之前視為拉丁/數字/半形符號，同一連續片段合併成一個詞一起量寬；
    // 之後（CJK 表意文字／全形標點）逐字獨立成詞，符合中文折行習慣。
    if (ch.charCodeAt(0) < 0x2e80) {
      asciiBuf += ch
    } else {
      if (asciiBuf) { tokens.push(asciiBuf); asciiBuf = '' }
      tokens.push(ch)
    }
  }
  if (asciiBuf) tokens.push(asciiBuf)

  const lines: string[] = []
  let current = ''
  for (const token of tokens) {
    const candidate = current + token
    if (current && measureFn(candidate) > maxWidth) {
      lines.push(current)
      current = token
    } else {
      current = candidate
    }
  }
  if (current) lines.push(current)
  return lines.length ? lines : ['']
}

// 找出能讓文字排進安全寬度的最大字級：優先單行塞入（字級只小幅縮小，避免為了塞單行縮到難以辨讀）；
// 塞不下才改採折行（字級可縮更多，換取維持較大可讀字級但改用多行）。
// 用於賽事名稱這類長度不定、需避免壓到底圖裝飾（人物立繪／四角圖案）的欄位。
function layoutWrappedText(
  ctx: CanvasRenderingContext2D,
  text: string,
  weight: number,
  maxSize: number,
  minSizeSingle: number,
  minSizeWrap: number,
  maxWidth: number,
  maxLines: number,
): { lines: string[]; size: number } {
  for (let size = maxSize; size >= minSizeSingle; size -= 1) {
    ctx.font = `${weight} ${size}px ${CJK}`
    if (ctx.measureText(text).width <= maxWidth) return { lines: [text], size }
  }
  for (let size = maxSize; size >= minSizeWrap; size -= 1) {
    ctx.font = `${weight} ${size}px ${CJK}`
    const lines = wrapTextByWidth(text, maxWidth, (s) => ctx.measureText(s).width)
    if (lines.length <= maxLines && lines.every((l) => ctx.measureText(l).width <= maxWidth)) {
      return { lines, size }
    }
  }
  // 極端情況（字級已到下限仍放不進 maxLines 行）：用最小字級硬折，行數可能超過 maxLines，
  // 但至少每行寬度都受控（不會壓到左右裝飾）
  ctx.font = `${weight} ${minSizeWrap}px ${CJK}`
  return { lines: wrapTextByWidth(text, maxWidth, (s) => ctx.measureText(s).width), size: minSizeWrap }
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

  // ---- 文字層（背景已畫完）----
  // 標準底圖模板規格：底圖本身已畫好文字容器（中央成績大框＋兩條分隔線＋下方日期膠囊框），
  // 此處只負責把文字對到模板的固定座標（以 W/H 比例量測），不再疊加任何方塊底色／邊框。
  ctx.textAlign = 'center'
  ctx.shadowColor = 'transparent'
  ctx.shadowBlur = 0

  // 標題
  ctx.fillStyle = MUTED
  ctx.font = `400 28px ${FONT}`
  ctx.letterSpacing = '6px'
  ctx.fillText('完賽證明', cx, H * 0.3)
  ctx.letterSpacing = '0px'

  // 姓名 + 金色底線
  const nameText = cert.name || '跑者'
  let size = fitFontSize(ctx, nameText, 700, 76, 50, W * 0.5)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = NAVY
  const nameY = H * 0.38
  ctx.fillText(nameText, cx, nameY)
  const nameW = ctx.measureText(nameText).width
  const barW = Math.min(280, Math.max(100, nameW * 0.72))
  roundRectPath(ctx, cx - barW / 2, nameY + 25, barW, 6, 3)
  ctx.fillStyle = GOLD_ACCENT
  ctx.fill()

  // 完成賽事：安全寬 ≈46%W、置中；過長自動折成兩行（單行 y=48%H；兩行往 45.5%/50.5%H 分配
  // ——行距 5%H，2026-08-24 使用者實測回饋原 44%/52% 間距過大；下方組別固定於 56%H 仍留有間距）
  const raceText = `完成「${cert.race_title}」`
  const raceSafeW = W * 0.46
  const raceLayout = layoutWrappedText(ctx, raceText, 700, 34, 30, 24, raceSafeW, 2)
  ctx.font = `700 ${raceLayout.size}px ${FONT}`
  ctx.fillStyle = NAVY
  if (raceLayout.lines.length > 1) {
    ctx.fillText(raceLayout.lines[0], cx, H * 0.455)
    ctx.fillText(raceLayout.lines[1], cx, H * 0.505)
  } else {
    ctx.fillText(raceLayout.lines[0], cx, H * 0.48)
  }

  // 賽事細項（分組行；無分組資料則略過此欄）
  if (cert.group_name) {
    size = fitFontSize(ctx, cert.group_name, 400, 27, 22, raceSafeW)
    ctx.font = `400 ${size}px ${FONT}`
    ctx.fillStyle = MUTED
    ctx.fillText(cert.group_name, cx, H * 0.56)
  }

  // 成績三欄：對齊底圖中央大框（x 26%~73.3%W、y 63.6%~79.3%H，分隔線在 x 40.6%/56.5%W）
  // 三欄文字中心 x 33.3%/48.5%/65%W；欄內文字若超出欄寬，字級自動微降
  const col1X = W * 0.333
  const col2X = W * 0.485
  const col3X = W * 0.65
  const labelY = H * 0.68
  const valueY = H * 0.74
  const colGutter = 24 // 每欄左右各留的內距，避免數值貼到分隔線
  const col1MaxW = W * 0.406 - W * 0.26 - colGutter
  const col2MaxW = W * 0.565 - W * 0.406 - colGutter
  const col3MaxW = W * 0.733 - W * 0.565 - colGutter

  // 完成里程
  ctx.fillStyle = MUTED
  ctx.font = `400 20px ${FONT}`
  ctx.fillText('完成里程', col1X, labelY)
  const kmText = `${cert.completed_km.toFixed(1)} K`
  size = fitFontSize(ctx, kmText, 700, 38, 26, col1MaxW)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = RED
  ctx.fillText(kmText, col1X, valueY)

  // 完成時間
  ctx.fillStyle = MUTED
  ctx.font = `400 20px ${FONT}`
  ctx.fillText('完成時間', col2X, labelY)
  const timeText = fmtDuration(cert.total_time_s)
  size = fitFontSize(ctx, timeText, 700, 30, 22, col2MaxW)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = BLUE
  ctx.fillText(timeText, col2X, valueY)

  // 完成名次（無名次資料時顯示 —，不整欄消失）
  ctx.fillStyle = MUTED
  ctx.font = `400 20px ${FONT}`
  ctx.fillText('完成名次', col3X, labelY)
  const rankText = cert.finish_rank > 0 ? `第 ${cert.finish_rank} 名` : '—'
  size = fitFontSize(ctx, rankText, 700, 38, 26, col3MaxW)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = GOLD_ACCENT
  ctx.fillText(rankText, col3X, valueY)

  // 完成日期：對齊底圖下方日期膠囊框（x 33.3%~67%W、y 83%~90.3%H），整行置中
  const dateText = `完成日期｜${fmtDate(cert.completion_at)}`
  const dateMaxW = W * (0.67 - 0.333) - 40
  size = fitFontSize(ctx, dateText, 700, 26, 20, dateMaxW)
  ctx.font = `700 ${size}px ${FONT}`
  ctx.fillStyle = NAVY
  ctx.fillText(dateText, cx, H * 0.882) // 2026-08-24 實測回饋：0.866 於膠囊框內視覺偏高，下移至 88.2%H 置中

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
