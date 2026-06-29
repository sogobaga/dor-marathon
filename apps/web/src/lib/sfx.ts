// 用 Web Audio API 合成音效（免音檔）。之後要換成真實音檔，替換這幾個函式即可。

let ctx: AudioContext | null = null
let muted = false

function ac(): AudioContext | null {
  if (typeof window === 'undefined') return null
  if (!ctx) {
    const AC = window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!AC) return null
    try { ctx = new AC() } catch { return null }
  }
  if (ctx.state === 'suspended') ctx.resume().catch(() => {})
  return ctx
}

// 任何使用者互動後嘗試解鎖（iOS 需在手勢內 resume）
export function unlockAudio() {
  ac()
}

export function setMuted(m: boolean) {
  muted = m
  if (m) stopFill()
}
export function isMuted() {
  return muted
}

let fillOsc: OscillatorNode | null = null
let fillGain: GainNode | null = null

// EXP bar 開始增加 → 持續上升的「增加中」音效
export function startFill() {
  if (muted) return
  const c = ac()
  if (!c) return
  stopFill()
  const osc = c.createOscillator()
  const gain = c.createGain()
  osc.type = 'sawtooth'
  osc.frequency.setValueAtTime(300, c.currentTime)
  gain.gain.setValueAtTime(0.0001, c.currentTime)
  gain.gain.exponentialRampToValueAtTime(0.05, c.currentTime + 0.03)
  osc.connect(gain).connect(c.destination)
  osc.start()
  fillOsc = osc
  fillGain = gain
}

// 依 bar 百分比更新音高（越滿越高）
export function updateFill(pct: number) {
  const c = ac()
  if (!c || !fillOsc) return
  const f = 300 + Math.max(0, Math.min(1, pct)) * 640 // 300→940Hz
  fillOsc.frequency.setTargetAtTime(f, c.currentTime, 0.02)
}

export function stopFill() {
  const c = ctx
  if (fillOsc && fillGain && c) {
    const osc = fillOsc
    const gain = fillGain
    try {
      gain.gain.cancelScheduledValues(c.currentTime)
      gain.gain.setTargetAtTime(0.0001, c.currentTime, 0.03)
    } catch { /* ignore */ }
    setTimeout(() => { try { osc.stop() } catch { /* ignore */ } }, 120)
  }
  fillOsc = null
  fillGain = null
}

// 升級到 100% 的「噹」——鐘聲（多諧波 + 快速衰減）
export function playDing() {
  if (muted) return
  const c = ac()
  if (!c) return
  const now = c.currentTime
  const harmonics: [number, number][] = [[880, 0.2], [1320, 0.13], [1760, 0.08], [2640, 0.04]]
  for (const [freq, vol] of harmonics) {
    const o = c.createOscillator()
    const g = c.createGain()
    o.type = 'sine'
    o.frequency.value = freq
    g.gain.setValueAtTime(0.0001, now)
    g.gain.exponentialRampToValueAtTime(vol, now + 0.008)
    g.gain.exponentialRampToValueAtTime(0.0001, now + 0.95)
    o.connect(g).connect(c.destination)
    o.start(now)
    o.stop(now + 1.0)
  }
}
