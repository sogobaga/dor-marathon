// 未來科幻世界（scifi）跑者「粒子靈魂」＋軌跡粒子（CONTRACT.md §4.3）。
// 純 canvas 2D 繪圖類別，不碰 DOM／不 import maplibre-gl，由 SciFiMap.tsx 的單一 requestAnimationFrame
// 迴圈驅動（見該檔）。物件池（TrailPool）重用粒子物件避免每幀配置新物件造成 GC 壓力。

function lerp(a: number, b: number, t: number): number { return a + (b - a) * t }

interface TrailParticle {
  active: boolean
  x: number; y: number
  vx: number; vy: number
  life: number // 剩餘秒數
  maxLife: number
  size: number
}

// 軌跡粒子物件池：spawn() 從池中取一顆閒置（或滿了就隨機頂替最老的一顆），update()/draw() 全池掃過。
export class TrailPool {
  private pool: TrailParticle[]
  private cap: number
  constructor(cap: number) {
    this.cap = Math.max(1, cap)
    this.pool = Array.from({ length: this.cap }, () => ({ active: false, x: 0, y: 0, vx: 0, vy: 0, life: 0, maxLife: 1, size: 2 }))
  }
  setCap(cap: number) {
    // 專注模式減半等情境動態調整上限：縮小時讓多出的粒子自然淘汰（不強制清空，畫面較不突兀）
    this.cap = Math.max(1, cap)
  }
  spawn(x: number, y: number, dirX: number, dirY: number, speedPx: number) {
    let p = this.pool.find((q) => !q.active)
    if (!p) { if (this.pool.length >= this.cap * 1.2) p = this.pool[(Math.random() * this.pool.length) | 0]; else { p = { active: false, x: 0, y: 0, vx: 0, vy: 0, life: 0, maxLife: 1, size: 2 }; this.pool.push(p) } }
    const spread = (Math.random() - 0.5) * 0.7
    const ang = Math.atan2(-dirY, -dirX) + spread // 噴向「行進反方向」
    const spd = speedPx * (0.3 + Math.random() * 0.6)
    p.active = true
    p.x = x; p.y = y
    p.vx = Math.cos(ang) * spd; p.vy = Math.sin(ang) * spd
    p.maxLife = 1.8 + Math.random() * 0.4 // 壽命約 2 秒（CONTRACT_R2.md §3，1.8~2.2 略帶隨機更自然）
    p.life = p.maxLife
    p.size = 1.4 + Math.random() * 2.4
  }
  update(dt: number) {
    for (const p of this.pool) {
      if (!p.active) continue
      p.life -= dt
      if (p.life <= 0) { p.active = false; continue }
      p.x += p.vx * dt; p.y += p.vy * dt
      p.vx *= 0.94; p.vy *= 0.94 // 隨時間減速、擴散
    }
  }
  draw(ctx: CanvasRenderingContext2D) {
    ctx.save()
    ctx.globalCompositeOperation = 'lighter'
    for (const p of this.pool) {
      if (!p.active) continue
      const t = Math.max(0, p.life / p.maxLife) // 1→0
      const r = lerp(53, 255, 1 - t), g = lerp(230, 63, 1 - t), b = lerp(255, 160, 1 - t) // 青→洋紅
      ctx.beginPath()
      ctx.fillStyle = `rgba(${r | 0},${g | 0},${b | 0},${(t * 0.8).toFixed(3)})`
      ctx.arc(p.x, p.y, p.size * (0.5 + t * 0.5), 0, Math.PI * 2)
      ctx.fill()
    }
    ctx.restore()
  }
}

interface Orbiter { r: number; speed: number; angle: number; size: number }

// 跑者「粒子靈魂」：中心白青核心 + 多層加法混合光暈 + 環繞粒子；移動時沿行進反方向拉長成彗星狀。
// 環繞粒子半徑範圍（CONTRACT_R2.md §3：14–42px）
function makeOrbiter(): Orbiter {
  return {
    r: 14 + Math.random() * 28,
    speed: (0.4 + Math.random() * 1.2) * (Math.random() < 0.5 ? -1 : 1),
    angle: Math.random() * Math.PI * 2,
    size: 1 + Math.random() * 2.2,
  }
}

export class Soul {
  private orbiters: Orbiter[]
  private breathT = 0
  constructor(count = 65) {
    this.orbiters = Array.from({ length: count }, makeOrbiter)
  }
  setCount(n: number) {
    n = Math.max(10, n)
    if (n < this.orbiters.length) this.orbiters.length = n
    else while (this.orbiters.length < n) this.orbiters.push(makeOrbiter())
  }
  update(dt: number) {
    this.breathT += dt
    for (const o of this.orbiters) o.angle += o.speed * dt
  }
  // movingT: 0..1（依速度換算的移動強度）；dirX/dirY：螢幕空間行進方向單位向量（靜止時可傳 0,0）；
  // dim：true＝「定位中」暗淡狀態（CONTRACT_R2.md §2，尚無真實 GPS 定位時畫在 initialCenter，整體
  // alpha 降低、不拉彗星尾巴），省略/false＝正常亮度。
  draw(ctx: CanvasRenderingContext2D, x: number, y: number, movingT: number, dirX: number, dirY: number, dim = false) {
    ctx.save()
    ctx.translate(x, y)
    ctx.globalAlpha = dim ? 0.4 : 1
    if (movingT > 0.05) {
      const ang = Math.atan2(dirY, dirX)
      const stretch = 1 + movingT * 1.8
      ctx.rotate(ang)
      ctx.scale(stretch, 1 / Math.sqrt(stretch))
      ctx.rotate(-ang)
    }
    const breathe = 1 + Math.sin(this.breathT * 2.2) * 0.08
    ctx.globalCompositeOperation = 'lighter'
    // 核心光暈：i=1 層即達直徑 ≥36px（CONTRACT_R2.md §3：核心光暈直徑 ≥36px CSS px）
    for (let i = 3; i >= 1; i--) {
      const rad = (10 + 10 * i) * breathe
      const grad = ctx.createRadialGradient(0, 0, 0, 0, 0, rad)
      grad.addColorStop(0, `rgba(53,230,255,${(0.26 / i).toFixed(3)})`)
      grad.addColorStop(1, 'rgba(53,230,255,0)')
      ctx.fillStyle = grad
      ctx.beginPath(); ctx.arc(0, 0, rad, 0, Math.PI * 2); ctx.fill()
    }
    for (const o of this.orbiters) {
      const px = Math.cos(o.angle) * o.r, py = Math.sin(o.angle) * o.r * 0.55 // 略扁，像俯視角光環
      const tone = (Math.sin(o.angle * 1.7 + this.breathT) + 1) / 2
      const r = lerp(53, 255, tone * 0.4), g = lerp(230, 63, tone * 0.4)
      ctx.beginPath()
      ctx.fillStyle = `rgba(${r | 0},${g | 0},255,0.85)`
      ctx.arc(px, py, o.size, 0, Math.PI * 2)
      ctx.fill()
    }
    const core = ctx.createRadialGradient(0, 0, 0, 0, 0, 7 * breathe)
    core.addColorStop(0, 'rgba(255,255,255,0.95)')
    core.addColorStop(0.4, 'rgba(180,245,255,0.9)')
    core.addColorStop(1, 'rgba(53,230,255,0)')
    ctx.fillStyle = core
    ctx.beginPath(); ctx.arc(0, 0, 7 * breathe, 0, Math.PI * 2); ctx.fill()
    ctx.restore()
  }
}
