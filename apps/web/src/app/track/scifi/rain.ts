// 未來科幻世界（scifi）地圖光雨疊層（CONTRACT.md §4.2 尾段）：在地圖之上、UI 之下的細長青色光絲，
// 自上而下飄落，數量 ≤60、速度隨機；prefers-reduced-motion 時由 SciFiMap.tsx 直接不建立/不更新本類別。

interface Streak { x: number; y: number; len: number; speed: number; alpha: number }

function spawnOne(w: number, h: number, randomY: boolean): Streak {
  return {
    x: Math.random() * w,
    y: randomY ? Math.random() * h : -40,
    len: 30 + Math.random() * 70,
    speed: 220 + Math.random() * 260,
    alpha: 0.06 + Math.random() * 0.18,
  }
}

export class LightRain {
  private streaks: Streak[] = []
  private cap: number
  constructor(cap = 60) { this.cap = Math.max(0, cap) }
  setCap(cap: number) { this.cap = Math.max(0, cap); if (this.streaks.length > cap) this.streaks.length = cap }
  update(dt: number, w: number, h: number) {
    while (this.streaks.length < this.cap) this.streaks.push(spawnOne(w, h, true))
    for (const s of this.streaks) {
      s.y += s.speed * dt
      if (s.y - s.len > h) Object.assign(s, spawnOne(w, h, false))
    }
  }
  draw(ctx: CanvasRenderingContext2D) {
    if (!this.streaks.length) return
    ctx.save()
    ctx.globalCompositeOperation = 'lighter'
    ctx.lineWidth = 1
    for (const s of this.streaks) {
      const grad = ctx.createLinearGradient(s.x, s.y - s.len, s.x, s.y)
      grad.addColorStop(0, 'rgba(53,230,255,0)')
      grad.addColorStop(1, `rgba(53,230,255,${s.alpha.toFixed(3)})`)
      ctx.strokeStyle = grad
      ctx.beginPath()
      ctx.moveTo(s.x, s.y - s.len)
      ctx.lineTo(s.x, s.y)
      ctx.stroke()
    }
    ctx.restore()
  }
}
