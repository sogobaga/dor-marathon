// DORPG 戰鬥音訊單例：BGM（HTMLAudioElement 迴圈＋淡入淡出）＋ SFX（Web Audio 解碼快取，8 聲部上限）。
// 音量持久化在 localStorage，供設定面板／下次進入戰鬥沿用。
//
// 不採用 lib/sfx.ts 的即時合成音（那是舊系統在沒有正式音檔前的佔位音效）；只沿用它「AudioContext
// 必須在使用者手勢內 resume」的解鎖慣例。特效包 runtime（vendor/dorpgCombatFx.js）的內建音效已被
// CombatFxLayer 用 internalAudio:false 關閉，所有戰鬥音效一律由這裡的 playSfx 播放——避免雙重播放。
//
// SSR 安全：模組頂層不觸碰任何瀏覽器 API；每個匯出函式都先檢查 typeof window。

import { bgmUrl, fxAudioUrl } from '@/lib/dorpg/cdn'

type BgmKind = 'master' | 'boss'

const LS_MUSIC = 'dorpg.music'
const LS_SFX = 'dorpg.sfx'
const DEFAULT_VOLUME = 80
const MAX_VOICES = 8 // 照特效包 runtime 規則：全場 8 聲部上限
const VOICE_FADE_S = 0.012 // 超過上限時淡出舊聲部，12ms 淡出 + 2ms 保護間隔再 stop，避免喀嚓聲

function isBrowser(): boolean {
  return typeof window !== 'undefined'
}

function clampVolume(v: number): number {
  return Math.max(0, Math.min(100, Number.isFinite(v) ? v : DEFAULT_VOLUME))
}

function readVolume(key: string): number {
  if (!isBrowser()) return DEFAULT_VOLUME
  try {
    const raw = window.localStorage.getItem(key)
    if (raw == null) return DEFAULT_VOLUME
    const n = Number(raw)
    return Number.isFinite(n) ? clampVolume(n) : DEFAULT_VOLUME
  } catch {
    return DEFAULT_VOLUME // 私密瀏覽模式等情況下 localStorage 可能丟例外，退回預設值
  }
}

function writeVolume(key: string, v: number) {
  if (!isBrowser()) return
  try {
    window.localStorage.setItem(key, String(v))
  } catch {
    /* 略過：不影響本次播放，只是下次不會記得音量 */
  }
}

// 延遲到第一次呼叫任何 API 時才讀 localStorage（模組載入時機可能早於 hydration）。
let musicVolume = DEFAULT_VOLUME
let sfxVolume = DEFAULT_VOLUME
let volumesLoaded = false
function ensureVolumesLoaded() {
  if (volumesLoaded || !isBrowser()) return
  musicVolume = readVolume(LS_MUSIC)
  sfxVolume = readVolume(LS_SFX)
  volumesLoaded = true
}

// ---------------------------------------------------------------------------
// BGM：單一 HTMLAudioElement 迴圈播放（同源／CDN 皆可，不需要 CORS，也不用 Web Audio 解碼）。
// ---------------------------------------------------------------------------
let bgmEl: HTMLAudioElement | null = null
let currentBgmKind: BgmKind | null = null
let bgmFadeTimer: ReturnType<typeof setInterval> | null = null
let bgmWasPlaying = false // 供 suspendOnHidden 判斷回前景要不要恢復播放

function ensureBgmElement(): HTMLAudioElement | null {
  if (!isBrowser()) return null
  if (!bgmEl) {
    bgmEl = new Audio()
    bgmEl.loop = true
    bgmEl.preload = 'auto'
    bgmEl.volume = 0
  }
  return bgmEl
}

function clearBgmFade() {
  if (bgmFadeTimer != null) {
    clearInterval(bgmFadeTimer)
    bgmFadeTimer = null
  }
}

function fadeBgmTo(target: number, ms: number, onDone?: () => void) {
  const el = bgmEl
  if (!el) return
  clearBgmFade()
  const clampedTarget = Math.max(0, Math.min(1, target))
  if (ms <= 0) {
    el.volume = clampedTarget
    onDone?.()
    return
  }
  const start = el.volume
  const startedAt = performance.now()
  bgmFadeTimer = setInterval(() => {
    const t = Math.min(1, (performance.now() - startedAt) / ms)
    el.volume = start + (clampedTarget - start) * t
    if (t >= 1) {
      clearBgmFade()
      onDone?.()
    }
  }, 30)
}

function playBgm(kind: BgmKind) {
  if (!isBrowser()) return
  ensureVolumesLoaded()
  const el = ensureBgmElement()
  if (!el) return
  if (currentBgmKind === kind) {
    // 重複呼叫同一個 kind 不重播：頂多是曾被暫停（背景分頁）就續播，不重設進度、不重新淡入。
    bgmWasPlaying = true
    if (el.paused) el.play().catch(() => {})
    return
  }
  currentBgmKind = kind
  bgmWasPlaying = true
  el.pause()
  el.src = bgmUrl(kind)
  el.currentTime = 0
  el.volume = 0
  el.play().catch((err) => {
    // 最常見原因：尚未在使用者手勢內呼叫 unlock()（瀏覽器自動播放限制）；不拋錯，等下次手勢重試即可。
    console.warn('[dorpg audio] playBgm blocked（可能尚未呼叫 battleAudio.unlock()）', err)
  })
  fadeBgmTo(musicVolume / 100, 600)
}

function stopBgm(fadeMs = 400) {
  if (!isBrowser() || !bgmEl) return
  bgmWasPlaying = false
  const el = bgmEl
  fadeBgmTo(0, fadeMs, () => {
    el.pause()
    el.currentTime = 0
    currentBgmKind = null
  })
}

// ---------------------------------------------------------------------------
// SFX：Web Audio，fetch + decodeAudioData 快取 AudioBuffer；8 聲部上限、超過淡出最舊聲部。
// ---------------------------------------------------------------------------
let audioCtx: AudioContext | null = null
const sfxBuffers = new Map<string, AudioBuffer>()
const sfxPending = new Map<string, Promise<AudioBuffer | null>>()

interface Voice {
  source: AudioBufferSourceNode
  gain: GainNode
}
let voices: Voice[] = []

function ensureAudioContext(): AudioContext | null {
  if (!isBrowser()) return null
  if (!audioCtx) {
    const AC = window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!AC) return null
    try {
      audioCtx = new AC()
    } catch {
      return null
    }
  }
  return audioCtx
}

function stopVoice(v: Voice, ctx: AudioContext) {
  try {
    const t = ctx.currentTime
    v.gain.gain.cancelScheduledValues(t)
    v.gain.gain.setValueAtTime(v.gain.gain.value, t)
    v.gain.gain.linearRampToValueAtTime(0, t + VOICE_FADE_S)
    v.source.stop(t + VOICE_FADE_S + 0.002)
  } catch {
    /* 可能已經自然播放結束或尚未 start，忽略即可 */
  }
  voices = voices.filter((x) => x !== v)
}

function decodeSfx(id: string): Promise<AudioBuffer | null> {
  const cached = sfxBuffers.get(id)
  if (cached) return Promise.resolve(cached)
  const ctx = ensureAudioContext()
  if (!ctx) return Promise.resolve(null)
  let pending = sfxPending.get(id)
  if (!pending) {
    pending = (async () => {
      try {
        const res = await fetch(fxAudioUrl(id))
        if (!res.ok) throw new Error(`sfx fetch failed: ${id} (${res.status})`)
        const arrayBuffer = await res.arrayBuffer()
        const buf = await ctx.decodeAudioData(arrayBuffer)
        sfxBuffers.set(id, buf)
        return buf
      } catch (err) {
        console.error('[dorpg audio] preload sfx failed', id, err)
        return null
      } finally {
        sfxPending.delete(id)
      }
    })()
    sfxPending.set(id, pending)
  }
  return pending
}

async function preloadSfx(ids: string[]): Promise<void> {
  if (!isBrowser()) return
  await Promise.all(ids.map((id) => decodeSfx(id)))
}

function playSfx(id: string, gain = 1) {
  if (!isBrowser()) return
  ensureVolumesLoaded()
  const ctx = audioCtx
  const cached = sfxBuffers.get(id)
  if (!ctx || !cached) {
    // 還沒解碼完成（preloadSfx 尚未跑到這顆，或根本沒 preload 過）：非同步補一次，best-effort，
    // 不阻塞呼叫端（例如攻擊當下才第一次用到某個音效 id）。
    void decodeSfx(id).then((buf) => {
      if (buf) playSfx(id, gain)
    })
    return
  }
  if (ctx.state !== 'running') return // 尚未 unlock()：安靜略過，不噴自動播放例外
  if (voices.length >= MAX_VOICES) {
    const oldest = voices[0]
    if (oldest) stopVoice(oldest, ctx)
  }
  const source = ctx.createBufferSource()
  const g = ctx.createGain()
  source.buffer = cached
  g.gain.value = Math.max(0, gain) * (sfxVolume / 100)
  source.connect(g).connect(ctx.destination)
  const voice: Voice = { source, gain: g }
  voices.push(voice)
  source.onended = () => {
    try {
      source.disconnect()
      g.disconnect()
    } catch {
      /* 已經被 stopVoice 斷開過就忽略 */
    }
    voices = voices.filter((x) => x !== voice)
  }
  source.start()
}

// ---------------------------------------------------------------------------
// 公開 API
// ---------------------------------------------------------------------------
async function unlock(): Promise<void> {
  if (!isBrowser()) return
  ensureVolumesLoaded()
  const ctx = ensureAudioContext()
  if (ctx && ctx.state === 'suspended') {
    try {
      await ctx.resume()
    } catch {
      /* 瀏覽器拒絕（例如根本不在使用者手勢呼叫堆疊內）：之後的手勢再呼叫一次即可 */
    }
  }
  ensureBgmElement()
}

function setMusicVolume(v: number) {
  ensureVolumesLoaded()
  musicVolume = clampVolume(v)
  writeVolume(LS_MUSIC, musicVolume)
  if (bgmEl) {
    clearBgmFade() // 使用者正在拖滑桿：立即生效，取消任何進行中的淡入淡出動畫
    bgmEl.volume = musicVolume / 100
  }
}

function setSfxVolume(v: number) {
  ensureVolumesLoaded()
  sfxVolume = clampVolume(v)
  writeVolume(LS_SFX, sfxVolume)
}

function getVolumes(): { music: number; sfx: number } {
  ensureVolumesLoaded()
  return { music: musicVolume, sfx: sfxVolume }
}

function vibrate(pattern: number | number[]) {
  try {
    if (isBrowser() && typeof navigator.vibrate === 'function') navigator.vibrate(pattern)
  } catch {
    /* iOS Safari 等不支援 Vibration API 的環境，忽略即可 */
  }
}

function suspendOnHidden(): () => void {
  if (!isBrowser()) return () => {}
  const onVisibilityChange = () => {
    if (document.hidden) {
      // 背景：暫停 BGM（保留播放位置，不重置）＋立即取消所有 SFX 尾音，不留殘響。
      if (bgmEl && !bgmEl.paused) bgmEl.pause()
      clearBgmFade()
      const ctx = audioCtx
      if (ctx) for (const v of [...voices]) stopVoice(v, ctx)
    } else if (bgmWasPlaying && bgmEl && bgmEl.paused) {
      // 回前景：只有先前確實在播放（沒被 stopBgm 主動停掉）才恢復。
      bgmEl.play().catch(() => {})
    }
  }
  document.addEventListener('visibilitychange', onVisibilityChange)
  return () => document.removeEventListener('visibilitychange', onVisibilityChange)
}

export interface BattleAudioApi {
  unlock(): Promise<void>
  setMusicVolume(v: number): void
  setSfxVolume(v: number): void
  getVolumes(): { music: number; sfx: number }
  playBgm(kind: BgmKind): void
  stopBgm(fadeMs?: number): void
  preloadSfx(ids: string[]): Promise<void>
  playSfx(id: string, gain?: number): void
  vibrate(pattern: number | number[]): void
  suspendOnHidden(): () => void
}

export const battleAudio: BattleAudioApi = {
  unlock,
  setMusicVolume,
  setSfxVolume,
  getVolumes,
  playBgm,
  stopBgm,
  preloadSfx,
  playSfx,
  vibrate,
  suspendOnHidden,
}
