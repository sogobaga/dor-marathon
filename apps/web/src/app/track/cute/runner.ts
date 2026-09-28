// 溫馨可愛（cute）GPS 地圖 — Q 版小井（原創角色，DOR 自有 IP）。docs/skins/CUTE_CONTRACT.md§4：
// 「SVG 在載入時繪製成離屏 canvas 圖（2 倍解析度）後每幀 drawImage」——本檔把角色畫成一份 SVG 字串，
// 用 <img> 載入後一次性 drawImage 進離屏 canvas（快取），之後每個動畫幀只需對這張快取圖做
// translate/scale/drawImage，不必每幀重新算路徑，效能與可讀性都比逐幀重繪向量路徑好。
//
// 版權硬性規定（契約 §0）：不得使用、描摹、仿製任何吉伊卡哇角色（白色圓滾倉鼠、兔兔、小八貓等造型、
// 臉部五官組合、招牌嘴型）——本檔造型改走「粉紅鮑伯短髮＋圓角黑框眼鏡＋DOR 球衣」的小井既有 IP
// 外觀（沿用 docs/skins/RETRO_CONTRACT_R2.md §7 對「小井」髮型/眼鏡/球衣的既有描述，Q 版化：
// 大頭短身 2.5 頭身），嘴型改用單一道簡單上揚弧線（非吉伊卡哇任何角色的招牌嘴型組合）。
//
// 造型要點逐項對照 CONTRACT.md §4：
//   - 粉紅鮑伯髮（#fc7aa4／陰影 #e0608c）
//   - 圓角黑框眼鏡，內圓點眼＋白色高光
//   - 腮紅橢圓＋三條斜線
//   - 小小微笑（簡單弧線）
//   - DOR 白色球衣配粉紅袖、深藍小裙、粉紅跑鞋
//   - 2px 墨線＋3px 白色貼紙外框（見下方 SVG filter：feMorphology 兩層 dilate 模擬雙層描邊，
//     比逐形狀疊 stroke 更準確地做出「整個角色輪廓」的單一連續外框，不會在形狀交界處露出縫隙）

const INK = '#3d2b2b'
const HAIR = '#fc7aa4'
const HAIR_SHADOW = '#e0608c'
const SKIN = '#ffe0c2'
const BLUSH = '#ffb3c7'
const JERSEY = '#ffffff'
const SLEEVE = '#ff9fc6'
const SKIRT = '#33418f'
const SHOE = '#ec6fae'

// 角色核心尺寸（不含外框）約 40×46（契約「尺寸約 40×46 CSS px」），外加 6 單位邊界供 2px 墨線
// ＋3px 白邊的 feMorphology dilate 有空間可畫、不被裁切，故 SVG 實際輸出尺寸為 52×58。
const CORE_W = 40, CORE_H = 46
const MARGIN = 6
export const RUNNER_W = CORE_W + MARGIN * 2 // 52
export const RUNNER_H = CORE_H + MARGIN * 2 // 58

function buildRunnerSvg(): string {
  return `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="-${MARGIN} -${MARGIN} ${RUNNER_W} ${RUNNER_H}" width="${RUNNER_W}" height="${RUNNER_H}">
  <defs>
    <filter id="stk" x="-150%" y="-150%" width="400%" height="400%">
      <feMorphology in="SourceAlpha" operator="dilate" radius="5" result="wbase"/>
      <feFlood flood-color="#ffffff" result="wcolor"/>
      <feComposite in="wcolor" in2="wbase" operator="in" result="white"/>
      <feMorphology in="SourceAlpha" operator="dilate" radius="2" result="ibase"/>
      <feFlood flood-color="${INK}" result="icolor"/>
      <feComposite in="icolor" in2="ibase" operator="in" result="ink"/>
      <feMerge>
        <feMergeNode in="white"/>
        <feMergeNode in="ink"/>
        <feMergeNode in="SourceGraphic"/>
      </feMerge>
    </filter>
  </defs>
  <g filter="url(#stk)">
    <ellipse cx="14.5" cy="42.5" rx="3.6" ry="2.6" fill="${SHOE}"/>
    <ellipse cx="25.5" cy="42.5" rx="3.6" ry="2.6" fill="${SHOE}"/>
    <rect x="12" y="40.7" width="5" height="1.4" rx="0.7" fill="#ffffff"/>
    <rect x="23" y="40.7" width="5" height="1.4" rx="0.7" fill="#ffffff"/>
    <path d="M12 29 Q20 27 28 29 L32.5 39 Q20 42 7.5 39 Z" fill="${SKIRT}"/>
    <ellipse cx="8.6" cy="30.5" rx="2.5" ry="3" fill="${SKIN}"/>
    <ellipse cx="31.4" cy="30.5" rx="2.5" ry="3" fill="${SKIN}"/>
    <path d="M11 21 Q20 18.5 29 21 L30.5 30 Q20 33 9.5 30 Z" fill="${JERSEY}"/>
    <ellipse cx="9.5" cy="24" rx="4" ry="4.4" fill="${SLEEVE}"/>
    <ellipse cx="30.5" cy="24" rx="4" ry="4.4" fill="${SLEEVE}"/>
    <circle cx="20" cy="15.5" r="11.2" fill="${SKIN}"/>
    <path d="M8 15 Q7.2 4 20 3.2 Q32.8 4 32 15 Q32.6 21 28.5 23.5 Q29.6 15.5 27 10 Q20 6.5 13 10 Q10.4 15.5 11.5 23.5 Q7.4 21 8 15 Z" fill="${HAIR}"/>
    <path d="M9.6 16.5 Q9.2 21 11.5 23.5 Q10.6 18.6 11.6 14.3 Z" fill="${HAIR_SHADOW}"/>
    <path d="M30.4 16.5 Q30.8 21 28.5 23.5 Q29.4 18.6 28.4 14.3 Z" fill="${HAIR_SHADOW}"/>
    <path d="M9.4 11.5 Q10 5 20 4.3 Q30 5 30.6 11.5 Q28 9 25.6 11 Q23 8.6 20 10.6 Q17 8.6 14.4 11 Q12 9 9.4 11.5 Z" fill="${HAIR}"/>
    <rect x="10.6" y="13.6" width="7.4" height="6.2" rx="3" fill="none" stroke="${INK}" stroke-width="1.5"/>
    <rect x="22" y="13.6" width="7.4" height="6.2" rx="3" fill="none" stroke="${INK}" stroke-width="1.5"/>
    <line x1="18" y1="16.4" x2="22" y2="16.4" stroke="${INK}" stroke-width="1.5"/>
    <circle cx="14.3" cy="16.9" r="1.55" fill="${INK}"/>
    <circle cx="25.7" cy="16.9" r="1.55" fill="${INK}"/>
    <circle cx="13.7" cy="16.3" r="0.55" fill="#ffffff"/>
    <circle cx="25.1" cy="16.3" r="0.55" fill="#ffffff"/>
    <ellipse cx="11.6" cy="20.4" rx="2.3" ry="1.4" fill="${BLUSH}" opacity="0.85"/>
    <ellipse cx="28.4" cy="20.4" rx="2.3" ry="1.4" fill="${BLUSH}" opacity="0.85"/>
    <g stroke="${INK}" stroke-width="0.45" opacity="0.55" stroke-linecap="round">
      <line x1="10.2" y1="19.6" x2="11" y2="21.2"/>
      <line x1="11.4" y1="19.4" x2="12.2" y2="21"/>
      <line x1="12.6" y1="19.6" x2="13.2" y2="21.1"/>
      <line x1="27" y1="19.6" x2="27.8" y2="21.1"/>
      <line x1="28.2" y1="19.4" x2="29" y2="21"/>
      <line x1="29.4" y1="19.6" x2="30" y2="21.2"/>
    </g>
    <path d="M17.6 22.3 Q20 24 22.4 22.3" fill="none" stroke="${INK}" stroke-width="1" stroke-linecap="round"/>
  </g>
</svg>`.trim()
}

const RES_SCALE = 2 // 契約「2 倍解析度」

let cachedCanvas: HTMLCanvasElement | null = null
let loadingPromise: Promise<HTMLCanvasElement> | null = null

// loadRunnerSprite：非同步載入一次、快取結果；供 CuteMap.tsx 在建圖時呼叫一次（不阻塞地圖初始化，
// 尚未載入完成前 drawRunner() 安靜略過那幾幀，載入完成後自動接上）。失敗（理論上不會，data URI 沒有
// 網路請求）也只是讓角色晚一點出現，不影響地圖其餘圖層或跑步邏輯。
export function loadRunnerSprite(): Promise<HTMLCanvasElement> {
  if (cachedCanvas) return Promise.resolve(cachedCanvas)
  if (loadingPromise) return loadingPromise
  loadingPromise = new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => {
      const canvas = document.createElement('canvas')
      canvas.width = RUNNER_W * RES_SCALE
      canvas.height = RUNNER_H * RES_SCALE
      const ctx = canvas.getContext('2d')
      if (!ctx) { reject(new Error('runner-canvas-2d-unavailable')); return }
      ctx.imageSmoothingEnabled = true
      ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
      cachedCanvas = canvas
      resolve(canvas)
    }
    img.onerror = () => reject(new Error('runner-svg-load-failed'))
    img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(buildRunnerSvg())
  })
  return loadingPromise
}

// 供冒煙測試／sprite sheet 截圖直接取用（不經過 loadRunnerSprite 的快取，單純曝露 SVG 字串本身，
// 讓驗證腳本可以另外用 Playwright 開一張離屏頁面把它渲染出來另存 PNG，比對「一眼看出是小井」）。
export function getRunnerSvgSource(): string {
  return buildRunnerSvg()
}

export interface RunnerDrawOpts {
  flipX: boolean // 往西移動時水平翻轉（CONTRACT.md §4）
  moving: boolean // 是否在移動（決定彈跳 vs 呼吸兩種動畫）
  t: number // 高解析度時間戳（performance.now()），純用來算動畫相位，不影響其他狀態
  reducedMotion?: boolean // prefers-reduced-motion：關閉彈跳/呼吸，畫靜止幀
}

// drawRunner：每幀呼叫，把快取的離屏 canvas 疊上 squash&stretch／呼吸動畫後畫到地圖 canvas 上。
// cx/cy＝畫面上的中心點（CSS px，通常是 map.project() 出來的螢幕座標）。
export function drawRunner(ctx: CanvasRenderingContext2D, cx: number, cy: number, opts: RunnerDrawOpts) {
  const sprite = cachedCanvas
  if (!sprite) return // 尚未載入完成：安靜略過這一幀（見 loadRunnerSprite 註解）
  const { flipX, moving, t, reducedMotion } = opts
  let sx = 1, sy = 1, bob = 0
  if (!reducedMotion) {
    if (moving) {
      // 移動時上下彈跳（squash & stretch，週期 500ms，契約 §4）：sin 波驅動，壓扁時稍微變寬、
      // 拉長時稍微變窄，模擬跑步彈跳的重量感；bob 讓角色整體隨波峰略為上移，加強跳動感。
      const phase = ((t % 500) / 500) * Math.PI * 2
      const s = Math.sin(phase)
      sy = 1 + s * 0.09
      sx = 1 - s * 0.06
      bob = -Math.abs(s) * 2.2
    } else {
      // 靜止時輕微呼吸（週期 2200ms，避免完全靜止的死板感，非契約硬性數字，純錦上添花的細節）。
      const phase = ((t % 2200) / 2200) * Math.PI * 2
      const s = (Math.sin(phase) + 1) / 2
      sy = 1 + s * 0.03
      sx = 1 - s * 0.015
    }
  }
  ctx.save()
  ctx.translate(cx, cy + bob)
  ctx.scale((flipX ? -1 : 1) * sx, sy)
  ctx.imageSmoothingEnabled = true
  ctx.drawImage(sprite, -RUNNER_W / 2, -RUNNER_H / 2, RUNNER_W, RUNNER_H)
  ctx.restore()
}
