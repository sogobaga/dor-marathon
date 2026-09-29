// 溫馨可愛（cute）GPS 地圖 —「靈魂光點」＋移動粒子（docs/skins/CUTE_CONTRACT_R2b.md §B，取代 CONTRACT_R2.md §4.2 第
// 一版結構）。
//
// v857 起不再使用角色造型（原「小井」Q 版跑者已移除，見 CuteMap.tsx 頂端說明與已刪除的
// runner.ts）：避免未來需要角色性別顯示或擴充角色種類造成的維護負擔，改用一顆不具性別/外觀
// 意涵的「靈魂光點」代表跑者目前位置。
//
// FIX round2b（編排者親自放大檢視 4_track_running.png／7_orb_sheet.png 抓到的根因）：R2 第一版光點
// 只有一顆 r≈5px 的白→淡粉核心＋r≈22px candy 55%→0 光暈＋一圈極淡薰衣草描邊，疊在同樣偏粉的地圖上
// 對比不足，「只剩一顆小白點＋一圈極淡薰衣草線」。R2b 改成有明確輪廓的「實心光球」（18px 直徑，2px
// 白色外緣描邊，本體 sakura→#ff6fae 放射漸層，比舊版核心大很多也深很多）＋更大範圍的 #ff6fae 光暈
// （r≈30，取代舊版 r≈22）＋外圈淡薰衣草光環＋球體下方的柔和投影（讓它在淺色地圖上「浮起來」），
// 搭配 docs/skins/CUTE_CONTRACT_R2b.md §A 整體退淡的地圖色票，光點才真的是畫面上最飽和的焦點。
//
// 純 canvas 2D 繪圖類別，不碰 DOM／不 import maplibre-gl，由 CuteMap.tsx 的單一 requestAnimationFrame
// 迴圈驅動（update(dt,...) 再 draw(ctx,...)，兩者都由呼叫端每幀各呼叫一次）。
//
// 色票：本檔逐字複製 docs/skins/CUTE_CONTRACT_R2b.md §B／CONTRACT_R2.md §2「草莓牛奶」色票中光點/粒子會用到的幾個
// hex，刻意不 import components/cute/decor.ts 的 CUTE_PALETTE——THEME 工人正在同步改那份檔案的色票
// 數值，兩邊各自獨立宣告同一組數字，才不會因為對方尚未 commit 完成而讀到寫一半的中途狀態。
const SAKURA = '#ffc4dc' // 光球本體漸層起點（契約 sakura）
const ORB_DEEP = '#ff6fae' // 光球本體漸層終點／外緣描邊底色／光暈主色（docs/skins/CUTE_CONTRACT_R2b.md §B 逐字）
const LAVENDER = '#d9c8ff' // 外圈極淡薰衣草光環
const SHADOW_COLOR = 'rgba(214,69,127,.35)' // 光球下方柔和投影（docs/skins/CUTE_CONTRACT_R2b.md §B 逐字）

// 粒子色票：docs/skins/CUTE_CONTRACT_R2b.md §B 逐字「粒子顏色要比地圖飽和」三色（取代 R2 第一版的
// candy/lavender/butter 三個較淡的 token，數值本身沒有變太多但這裡改用契約明確重申的這組）。
const PARTICLE_CANDY = '#ff6fae'
const PARTICLE_LAVENDER = '#b58cff'
const PARTICLE_BUTTER = '#ffcf5c'

const PARTICLE_CAP = 60 // 契約逐字：粒子上限 60
const PARTICLE_COLORS: readonly string[] = [PARTICLE_CANDY, PARTICLE_LAVENDER, PARTICLE_BUTTER]

interface SparkleStar {
  angle: number
  speed: number // 弧度/秒，正負決定順逆時針，緩慢繞轉
  r: number // 繞轉半徑（CSS px）
  size: number
  twinkleT: number
  twinkleSpeed: number // 忽明忽滅週期速度
}

interface SoulParticle {
  active: boolean
  x: number; y: number
  vx: number; vy: number
  life: number // 剩餘秒數
  maxLife: number
  size: number
  color: string
}

function makeStar(): SparkleStar {
  return {
    angle: Math.random() * Math.PI * 2,
    speed: (0.12 + Math.random() * 0.22) * (Math.random() < 0.5 ? -1 : 1),
    r: 15 + Math.random() * 8,
    size: 1.6 + Math.random() * 1.3,
    twinkleT: Math.random() * Math.PI * 2,
    twinkleSpeed: 0.6 + Math.random() * 0.5,
  }
}

// 小四角閃亮星（8 頂點、內外半徑交錯，讀作可愛感的「✦」亮片符號；與 components/cute/decor.ts
// 的五角 drawStar 刻意區分開，這裡只服務光點旁的小裝飾，不共用那份繪圖函式）。
function drawSparkle(ctx: CanvasRenderingContext2D, x: number, y: number, r: number, alpha: number) {
  const inner = r * 0.35
  ctx.save()
  ctx.globalAlpha *= Math.max(0, alpha)
  ctx.translate(x, y)
  ctx.beginPath()
  for (let i = 0; i < 8; i++) {
    const radius = i % 2 === 0 ? r : inner
    const angle = (Math.PI / 4) * i
    const px = Math.cos(angle) * radius
    const py = Math.sin(angle) * radius
    if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py)
  }
  ctx.closePath()
  ctx.fillStyle = '#ffffff'
  ctx.fill()
  ctx.restore()
}

export interface SoulOrbDrawOpts {
  moving: boolean // 是否在移動（決定呼吸脈動快慢，契約 §4.2「移動中脈動略快」）
  reducedMotion?: boolean // prefers-reduced-motion：無脈動、無粒子、無星星，只畫靜態光點（契約逐字）
  // docs/skins/ORBPOS_CONTRACT.md §B「搜尋中」：GPS acc>65 或定位超過 15 秒沒更新時為 true——本體改半
  // 透明＋多一圈依 accuracyPx 畫的淡色精度圈＋較慢的脈動週期，reducedMotion 時圈不脈動（見 draw()）。
  searching?: boolean
  // 精度圈半徑（CSS px，呼叫端已用 metersPerPixel 把 pos.acc 換算成螢幕距離並 clamp 到 120px 上限，
  // 這裡再保守 clamp 一次防呼叫端漏做）。searching=false 時不使用。
  accuracyPx?: number
}

// SoulOrb：跑者「靈魂光點」＋旁邊 1–2 顆小星星＋移動時冒出的粒子。單一實例對應地圖上的一個光點，
// CuteMap.tsx 只需要建立一次（掛載時）、每幀呼叫 update() 再 draw()。
export class SoulOrb {
  private stars: SparkleStar[]
  private particles: SoulParticle[] = []
  private breathT = 0

  constructor() {
    const starCount = Math.random() < 0.5 ? 1 : 2 // 契約「1–2 顆」
    this.stars = Array.from({ length: starCount }, makeStar)
  }

  /** 目前活躍粒子數（供 window.__cuteDebug.particles 使用，CONTRACT_R2.md §4.6）。 */
  get particleCount(): number {
    let n = 0
    for (const p of this.particles) if (p.active) n++
    return n
  }

  /**
   * 每幀呼叫一次。dt＝秒；moving＝是否視為移動中（決定要不要冒粒子）；reducedMotion 為真時
   * 立刻清空既有粒子並跳過星星/粒子的所有動態更新（契約「只畫靜態光點」）。
   * cx/cy＝光點目前螢幕座標（CSS px），粒子從這裡冒出。
   */
  update(dt: number, moving: boolean, reducedMotion: boolean, cx: number, cy: number): void {
    this.breathT += dt
    if (reducedMotion) {
      if (this.particles.length) this.particles.length = 0
      return
    }
    for (const s of this.stars) { s.angle += s.speed * dt; s.twinkleT += s.twinkleSpeed * dt }
    if (moving) {
      const spawnPerSec = 18 // 粒子池上限（PARTICLE_CAP）已經是實際硬限制，這裡只管冒出速率
      const n = Math.max(1, Math.round(spawnPerSec * dt))
      for (let i = 0; i < n; i++) this.spawn(cx, cy)
    }
    for (const p of this.particles) {
      if (!p.active) continue
      p.life -= dt
      if (p.life <= 0) { p.active = false; continue }
      p.x += p.vx * dt; p.y += p.vy * dt
      p.vx *= 0.94; p.vy *= 0.94 // 隨時間減速、向外擴散淡出
    }
  }

  private spawn(cx: number, cy: number): void {
    let p = this.particles.find((q) => !q.active)
    if (!p) {
      if (this.particles.length >= PARTICLE_CAP) return // 硬上限 60（契約逐字），滿了就不再新增
      p = { active: false, x: 0, y: 0, vx: 0, vy: 0, life: 0, maxLife: 1, size: 2, color: PARTICLE_CANDY }
      this.particles.push(p)
    }
    const ang = Math.random() * Math.PI * 2
    const spd = 6 + Math.random() * 14
    p.active = true
    p.x = cx; p.y = cy
    p.vx = Math.cos(ang) * spd; p.vy = Math.sin(ang) * spd
    p.maxLife = 1 + Math.random() * 0.5 // 1–1.5 秒飄散淡出（契約逐字）
    p.life = p.maxLife
    p.size = 1.4 + Math.random() * 1.8
    p.color = PARTICLE_COLORS[(Math.random() * PARTICLE_COLORS.length) | 0]
  }

  /** 畫在 (x, y)（螢幕 CSS px，通常是 map.project() 出來的座標）。尺寸固定，不隨 zoom 縮放。 */
  draw(ctx: CanvasRenderingContext2D, x: number, y: number, opts: SoulOrbDrawOpts): void {
    const reducedMotion = !!opts.reducedMotion
    const searching = !!opts.searching
    ctx.save()
    ctx.translate(x, y)

    // 呼吸脈動：一般週期約 1.6 秒、±10% 大小，移動中略快（契約逐字，×0.6 週期）；「搜尋中」改用更慢
    // （3.2 秒）、略大（±14%）的脈動，跟本體透明度一起強化「還在定位、不確定」的觀感。reduced-motion
    // 恆為 1（不脈動，畫靜止幀）——下方精度圈共用同一個 breathe 縮放半徑，因此也一併不脈動。
    const period = searching ? 3.2 : (opts.moving ? 1.6 * 0.6 : 1.6)
    const amp = searching ? 0.14 : 0.1
    const breathe = reducedMotion ? 1 : 1 + Math.sin((this.breathT / period) * Math.PI * 2) * amp
    // docs/skins/ORBPOS_CONTRACT.md §B「本體半透明」：之後所有圖層（陰影/粒子以外/光環/光暈/本體/
    // 高光/星星）都用 rgba 顏色字串搭配預設 alpha 混合，沒有另外設定 globalAlpha 的區塊會直接吃到
    // 這裡的值；有各自 ctx.save()/restore() 的區塊（陰影、光環、光暈）restore 後才回到這個值，粒子
    // 區塊自己覆寫 globalAlpha 不受影響（移動粒子不屬於「本體」，契約沒有要求粒子也變透明）。
    if (searching) ctx.globalAlpha *= 0.55

    // 柔和投影（docs/skins/CUTE_CONTRACT_R2b.md §B 逐字：rgba(214,69,127,.35)、模糊 6px，畫在光球「下方」讓它浮起
    // 來）：不用 ctx.filter/shadowBlur（相容性/效能顧慮，且要精準控制只在球體下方偏移），改用扁平化
    // 的放射漸層橢圓模擬模糊邊緣，畫在所有其他圖層之前（最底層）。
    ctx.save()
    ctx.translate(0, 11 * breathe)
    ctx.scale(1, 0.42)
    const shadowR = 11 * breathe
    const shadow = ctx.createRadialGradient(0, 0, 0, 0, 0, shadowR)
    shadow.addColorStop(0, SHADOW_COLOR)
    shadow.addColorStop(1, 'rgba(214,69,127,0)')
    ctx.fillStyle = shadow
    ctx.beginPath(); ctx.arc(0, 0, shadowR, 0, Math.PI * 2); ctx.fill()
    ctx.restore()

    // 粒子畫在光球本體之前（飄散開才看得見邊緣，不會整團被光球蓋住）。注意：不用
    // globalCompositeOperation='lighter'（scifi/retro 深色底圖適合疊加變亮，但 cute 地圖底色接近
    // 白，加法混合遇到白色像素會直接 clamp 成純白、粒子等於隱形），一律用預設的一般 alpha 混合，
    // 飽和色粒子疊在淺色地圖上才看得見。
    if (!reducedMotion && this.particles.length) {
      ctx.save()
      for (const p of this.particles) {
        if (!p.active) continue
        const t = Math.max(0, p.life / p.maxLife) // 1→0
        ctx.beginPath()
        ctx.fillStyle = p.color
        ctx.globalAlpha = t * 0.9
        ctx.arc(p.x - x, p.y - y, p.size * (0.5 + t * 0.5), 0, Math.PI * 2)
        ctx.fill()
      }
      ctx.restore()
    }

    // 外圈極淡薰衣草光環：畫在 #ff6fae 光暈「外面」一圈（半徑比光暈大），做出「光暈→光環」兩層
    // 由濃到淡的層次感（docs/skins/CUTE_CONTRACT_R2b.md §B「再外一圈淡薰衣草光環」）。一般 alpha 混合（理由同上）。
    ctx.save()
    ctx.strokeStyle = LAVENDER
    ctx.globalAlpha = 0.4
    ctx.lineWidth = 2.4
    ctx.beginPath(); ctx.arc(0, 0, 36 * breathe, 0, Math.PI * 2); ctx.stroke()
    ctx.restore()

    // docs/skins/ORBPOS_CONTRACT.md §B「搜尋中」：一圈依 GPS acc 換算半徑的淡色精度圈（上限 120px，
    // 由呼叫端 CuteMap.tsx 用 metersPerPixel 換算好再傳進來，這裡只負責畫＋跟本體共用同一個 breathe
    // 做緩慢脈動）；虛線圈風格與其餘實心光暈區分開，讓人一眼看出這是「精度範圍」而非光點本體。
    if (searching) {
      const ringR = Math.min(120, Math.max(18, opts.accuracyPx ?? 40)) * breathe
      ctx.save()
      ctx.strokeStyle = 'rgba(255,111,174,0.5)'
      ctx.lineWidth = 2
      ctx.setLineDash([4, 5])
      ctx.beginPath(); ctx.arc(0, 0, ringR, 0, Math.PI * 2); ctx.stroke()
      ctx.restore()
    }

    // #ff6fae 光暈：docs/skins/CUTE_CONTRACT_R2b.md §B 逐字「半徑約 30px，55%→0」（取代 R2 第一版 r≈22 的 candy
    // 光暈，範圍更大、顏色更深，才蓋得過退淡後的地圖底色）。
    const haloR = 30 * breathe
    const halo = ctx.createRadialGradient(0, 0, 0, 0, 0, haloR)
    halo.addColorStop(0, 'rgba(255,111,174,0.55)')
    halo.addColorStop(1, 'rgba(255,111,174,0)')
    ctx.save()
    ctx.fillStyle = halo
    ctx.beginPath(); ctx.arc(0, 0, haloR, 0, Math.PI * 2); ctx.fill()
    ctx.restore()

    // 實心光球本體：docs/skins/CUTE_CONTRACT_R2b.md §B 逐字「直徑約 18px（半徑 9px）、主體 sakura→#ff6fae 放射漸層、
    // 2px 白色外緣」——取代 R2 第一版只有 r≈5px 核心＋沒有明確輪廓的畫法，這裡先畫漸層球體本體，
    // 再疊一圈白色描邊當「外緣」，最後在左上方疊一小塊白色高光模擬光澤（契約「中心白色高光」）。
    const ballR = 9 * breathe
    const body = ctx.createRadialGradient(0, 0, 0, 0, 0, ballR)
    body.addColorStop(0, SAKURA)
    body.addColorStop(1, ORB_DEEP)
    ctx.beginPath(); ctx.arc(0, 0, ballR, 0, Math.PI * 2)
    ctx.fillStyle = body
    ctx.fill()
    ctx.lineWidth = 2
    ctx.strokeStyle = '#ffffff'
    ctx.stroke()
    // 中心白色高光（左上偏移一點點，模擬球體光澤，不需要另外裁切——半徑夠小，畫在球體範圍內即可）。
    const hlR = ballR * 0.55
    const hlX = -ballR * 0.32, hlY = -ballR * 0.32
    const highlight = ctx.createRadialGradient(hlX, hlY, 0, hlX, hlY, hlR)
    highlight.addColorStop(0, 'rgba(255,255,255,0.95)')
    highlight.addColorStop(1, 'rgba(255,255,255,0)')
    ctx.beginPath(); ctx.arc(hlX, hlY, hlR, 0, Math.PI * 2)
    ctx.fillStyle = highlight
    ctx.fill()

    // 1–2 顆小四角閃亮星：緩慢繞轉＋忽明忽滅；reduced-motion 時完全不畫（契約「只畫靜態光點」）。
    if (!reducedMotion) {
      for (const s of this.stars) {
        const sx = Math.cos(s.angle) * (s.r + 14) // 光球本體變大了，星星繞轉半徑往外挪一點避免疊在球上
        const sy = Math.sin(s.angle) * (s.r + 14) * 0.6 // 略扁，像俯視角的小光環
        const twinkle = (Math.sin(s.twinkleT) + 1) / 2 // 0..1 忽明忽滅
        drawSparkle(ctx, sx, sy, s.size, 0.3 + twinkle * 0.6)
      }
    }

    ctx.restore()
  }
}
