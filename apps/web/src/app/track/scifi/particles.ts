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
    // 拖尾粒子大小同 Soul 一起縮到約 60%（docs/skins/ORBPOS_CONTRACT.md 第三輪 P3「拖尾粒子大小同
    // 比例縮小」）：原 1.4–3.8px → 約 0.84–2.28px。
    p.size = 0.84 + Math.random() * 1.44
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
// 環繞粒子半徑範圍——原本 CONTRACT_R2.md §3 定為 14–42px；docs/skins/ORBPOS_CONTRACT.md 第三輪 P3
// （使用者要求靈魂再小一點、更精緻）整體縮到約 60%＝8–25px，取代 R2 的數字。
function makeOrbiter(): Orbiter {
  return {
    r: 8 + Math.random() * 17,
    speed: (0.4 + Math.random() * 1.2) * (Math.random() < 0.5 ? -1 : 1),
    angle: Math.random() * Math.PI * 2,
    // 粒子大小同一輪縮到約 60%：原 1–3.2px → 約 0.6–1.9px（見上方半徑註解）。
    size: 0.6 + Math.random() * 1.3,
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
  // opts.searching：docs/skins/ORBPOS_CONTRACT.md §B「搜尋中」（GPS acc>65 或定位超過 15 秒沒更新）
  // ——原本（R2 輪）只加一圈青色細圈、本體亮度不變；第三輪 P2 審查發現這跟 cute／retro「本體整體
  // 變淡」的外觀不一致，本輪比照補上本體半透明（見下方 ctx.globalAlpha *= 0.55），青色細圈維持不變；
  // opts.accuracyPx＝依 GPS acc 換算的圈半徑（呼叫端已用 metersPerPixel 算好並 clamp 到 120px 上限）；
  // opts.reducedMotion 為真時圈不脈動（契約逐字「reduced-motion = no pulse」）。原本的 `dim` 參數
  // （尚無真實定位時的暗淡狀態）已隨 SciFiMap.tsx renderFrame 移除「沒有定位時仍退回 initialCenter
  // 畫一顆假靈魂」的呼叫端邏輯一起拿掉——現在完全沒有 p 的情況下 renderFrame 根本不會呼叫這個函式，
  // 不需要這個參數了（與 opts.searching 的半透明是兩件事，不要混用同名）。
  draw(ctx: CanvasRenderingContext2D, x: number, y: number, movingT: number, dirX: number, dirY: number, opts?: { searching?: boolean; accuracyPx?: number; reducedMotion?: boolean }) {
    const searching = !!opts?.searching
    const reducedMotion = !!opts?.reducedMotion
    ctx.save()
    ctx.translate(x, y)
    ctx.globalAlpha = 1
    // docs/skins/ORBPOS_CONTRACT.md §B／第三輪 P2：搜尋中本體半透明，跟 cute（orb.ts 同一行為、
    // 同一係數 0.55）一致。下面的光暈／環繞粒子／核心都沒有各自覆寫 globalAlpha，會直接吃到這裡
    // 乘出來的值；行進拖尾（TrailPool，SciFiMap.tsx 另外呼叫）不算「本體」，不受影響（比照 cute
    // 移動粒子不變淡的理由，見 cute/orb.ts 對應註解）。
    if (searching) ctx.globalAlpha *= 0.55
    if (movingT > 0.05) {
      const ang = Math.atan2(dirY, dirX)
      const stretch = 1 + movingT * 1.8
      ctx.rotate(ang)
      ctx.scale(stretch, 1 / Math.sqrt(stretch))
      ctx.rotate(-ang)
    }
    const breathe = 1 + Math.sin(this.breathT * 2.2) * 0.08
    ctx.globalCompositeOperation = 'lighter'
    // 核心光暈：SCIFI_CONTRACT_R2.md §3 原訂「核心光暈直徑 ≥36px」由
    // docs/skins/ORBPOS_CONTRACT.md 第三輪 P3 取代——使用者要求靈魂再小一點、更精緻，整體縮到約
    // 60%：i=1 層半徑原本 10+10=20px（直徑 40px）縮到 6+6=12px（直徑 24px），核心亮度不變（只改
    // 半徑，漸層色階/透明度公式不動）。
    for (let i = 3; i >= 1; i--) {
      const rad = (6 + 6 * i) * breathe
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
    // 核心半徑同一輪縮到約 60%：原 7px → 約 4.5px（亮度/色階不變，見上方光暈註解）。
    // 審查發現（ORBPOS 第三輪自檢）：縮小後在 DPR 1（實體像素＝CSS px，設備像素比低的手機/桌機瀏覽器）
    // 下，原本第一段色階只到 40% 半徑就從純白轉淡青，換算下來「R>235」的純白範圍半徑不到 0.5 CSS px，
    // 在 DPR 1 幾乎是次像素大小，容易整顆核心都取樣不到任何一顆真正純白的像素（核心「看起來還在」但
    // 嚴格白色偵測會抓到 0 顆）。把第一段色階往外推到 80% 半徑，純白範圍加大約一倍，DPR 1 也能穩定
    // 蓋到完整的實體像素；核心外側整體半徑與亮度公式不變，視覺上只是白→青的轉換點變靠外一點。
    const core = ctx.createRadialGradient(0, 0, 0, 0, 0, 4.5 * breathe)
    core.addColorStop(0, 'rgba(255,255,255,0.95)')
    core.addColorStop(0.8, 'rgba(180,245,255,0.9)')
    core.addColorStop(1, 'rgba(53,230,255,0)')
    ctx.fillStyle = core
    ctx.beginPath(); ctx.arc(0, 0, 4.5 * breathe, 0, Math.PI * 2); ctx.fill()
    // docs/skins/ORBPOS_CONTRACT.md §B「搜尋中」scifi 外觀：青色細圈，半徑依 GPS acc 換算（呼叫端已
    // clamp 到 120px 上限，這裡再保守 clamp 一次防呼叫端漏做），用比本體慢很多的獨立脈動頻率
    // （0.7 rad/s，本體是 2.2）強化「還在搜尋」的觀感；reduced-motion 時不脈動（半徑固定）。
    if (searching) {
      const ringPulse = reducedMotion ? 1 : 1 + Math.sin(this.breathT * 0.7) * 0.12
      const ringR = Math.min(120, Math.max(18, opts?.accuracyPx ?? 40)) * ringPulse
      ctx.save()
      ctx.globalCompositeOperation = 'lighter'
      ctx.strokeStyle = 'rgba(53,230,255,0.55)'
      ctx.lineWidth = 1.5
      ctx.beginPath(); ctx.arc(0, 0, ringR, 0, Math.PI * 2); ctx.stroke()
      ctx.restore()
    }
    ctx.restore()
  }
}
