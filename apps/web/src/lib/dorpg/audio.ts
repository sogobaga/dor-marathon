// DORPG 戰鬥音訊單例：BGM（HTMLAudioElement 迴圈＋淡入淡出）＋ SFX（Web Audio 解碼快取，8 聲部上限）。
// 音量持久化在 localStorage，供設定面板／下次進入戰鬥沿用。
//
// 不採用 lib/sfx.ts 的即時合成音（那是舊系統在沒有正式音檔前的佔位音效）；只沿用它「AudioContext
// 必須在使用者手勢內 resume」的解鎖慣例。特效包 runtime（vendor/dorpgCombatFx.js）的內建音效已被
// CombatFxLayer 用 internalAudio:false 關閉，所有戰鬥音效一律由這裡的 playSfx 播放——避免雙重播放。
//
// SSR 安全：模組頂層不觸碰任何瀏覽器 API；每個匯出函式都先檢查 typeof window。
//
// 2026-09-14 iOS BGM 修復：
// ① 根因是舊版 playBgm() 一律被包在 `unlock().then(() => playBgm(...))` 裡呼叫，unlock() 內部
//   `await ctx.resume()` 之後才輪到 playBgm() 呼叫 el.play()——這時已經脫離使用者手勢的同步呼叫
//   堆疊，iOS Safari 對非手勢堆疊內的 play() 一律拒絕。新增 playBgmFromGesture()：手勢 handler
//   直接同步呼叫、el.play() 之前不可有任何 await，非同步善後（ctx.resume／接 Web Audio 圖）另外排隊。
// ② 次因是 HTMLMediaElement.volume 在 iOS Safari 是唯讀的（指派被忽略，這是 Apple 刻意的設計，逼
//   App 讓使用者用硬體音量鍵），所以音樂滑桿在 iPhone 上點了沒反應。修法是把 <audio> 接進既有 SFX
//   共用的 AudioContext（createMediaElementSource → GainNode → destination），改用 gain 控制音量；
//   跨網域媒體要進 Web Audio 圖必須先有 CORS，R2（img.dor.tw）已於 2026-09-14 設好。
//
// 2026-09-14 BGM 修復第 2 輪（驗證/審查後追加，跟上面①②正交的獨立問題）：
// B 目前網域不在 R2 CORS 白名單（例如 `npm run dev` 的 http://localhost:3000）時，帶
//   crossOrigin='anonymous' 的 <audio> 會被瀏覽器直接判定載入失敗，不是優雅降級、是整段沒聲音。
//   見 handleBgmElementError／degradeBgmElement：改成偵測到這種失敗就换一顆不設 crossOrigin 的
//   元素重試同一首，放棄 Web Audio 路由升級換取「至少聽得到」，每個 kind 只降級一次避免無限重試。
// D AudioContext 除了 'suspended' 還有 iOS 來電等 OS 級中斷會進入的 'interrupted' 狀態，原本只判斷
//   `state === 'suspended'` 才 resume() 會漏掉它，導致「isBgmPlaying() 回 true 卻整段靜音」；
//   兩處判斷改成 `state !== 'running'`，isBgmPlaying() 在 webaudio 路由下也一併檢查 ctx 是否 running。
// F 首次進場的音樂預設音量從 80 降到 45（DEFAULT_MUSIC_VOLUME，SFX 不受影響）：iOS 的
//   audioSession.type='playback' 會蓋過硬體靜音鍵，玩家在安靜場合一開遊戲就會被外放，來不及先找到
//   設定面板調整；使用者仍可在設定面板隨時調高或關掉。
// （手勢事件型別 C、stopBgm 時機 E 兩項屬畫面接線，分別見 BattleScreen.tsx/EncounterPicker.tsx 的
//   handleRootPointerDownCapture 掛點與 PhoneShell.tsx 的 battleScreenRendered。）
import { bgmUrl, fxAudioUrl } from '@/lib/dorpg/cdn'

type BgmKind = 'master' | 'boss'

const LS_MUSIC = 'dorpg.music'
const LS_SFX = 'dorpg.sfx'
// 2026-09-14 修復 F：iOS 的 audioSession.type='playback'（見下方 setupIosAudioSession）會讓 BGM
// 蓋過硬體靜音鍵，若還沿用舊的 80% 預設，玩家在安靜場合（圖書館等）一開遊戲選單就會被外放音樂嚇到，
// 來不及先找到設定面板調整。音樂另立較低的預設值，SFX（音效）維持原樣不受影響；使用者兩者都能在
// 設定面板隨時調整或直接關掉（滑桿拉到 0）。
const DEFAULT_MUSIC_VOLUME = 45
const DEFAULT_SFX_VOLUME = 80
const MAX_VOICES = 8 // 照特效包 runtime 規則：全場 8 聲部上限
const VOICE_FADE_S = 0.012 // 超過上限時淡出舊聲部，12ms 淡出 + 2ms 保護間隔再 stop，避免喀嚓聲

function isBrowser(): boolean {
  return typeof window !== 'undefined'
}

function clampVolume(v: number, fallback: number): number {
  return Math.max(0, Math.min(100, Number.isFinite(v) ? v : fallback))
}

function readVolume(key: string, fallback: number): number {
  if (!isBrowser()) return fallback
  try {
    const raw = window.localStorage.getItem(key)
    if (raw == null) return fallback
    const n = Number(raw)
    return Number.isFinite(n) ? clampVolume(n, fallback) : fallback
  } catch {
    return fallback // 私密瀏覽模式等情況下 localStorage 可能丟例外，退回預設值
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
let musicVolume = DEFAULT_MUSIC_VOLUME
let sfxVolume = DEFAULT_SFX_VOLUME
let volumesLoaded = false
function ensureVolumesLoaded() {
  if (volumesLoaded || !isBrowser()) return
  musicVolume = readVolume(LS_MUSIC, DEFAULT_MUSIC_VOLUME)
  sfxVolume = readVolume(LS_SFX, DEFAULT_SFX_VOLUME)
  volumesLoaded = true
}

// ---------------------------------------------------------------------------
// BGM：HTMLAudioElement 迴圈播放；音量／淡入淡出優先走 Web Audio GainNode（iOS 上 el.volume 唯讀，
// 只有透過 AudioContext 才控制得了），連不上時退回 el.volume 直出（桌機正常、iOS 至少聽得到聲音）。
// ---------------------------------------------------------------------------
let bgmEl: HTMLAudioElement | null = null
let currentBgmKind: BgmKind | null = null
let bgmFadeTimer: ReturnType<typeof setInterval> | null = null
let bgmWasPlaying = false // 供 suspendOnHidden 判斷回前景要不要恢復播放
let bgmBlocked = false // 最近一次 play() 是否被瀏覽器拒絕（診斷用，見 getBgmDiagnostics）
let bgmLastError: string | null = null
let bgmElVolumeControllable = true // 特徵偵測結果：iOS Safari 上 el.volume 指派通常無效，會是 false

// Web Audio 音量路由：只有在這裡是 'webaudio' 時，setMusicVolume 才透過 gain 生效（iOS 需要）。
let bgmSourceNode: MediaElementAudioSourceNode | null = null
let bgmGainNode: GainNode | null = null
let bgmRouting: 'element' | 'webaudio' = 'element'
let bgmRoutingAttempted = false // createMediaElementSource 對同一元素只能呼叫一次，不論成敗都不重試
let bgmVolumeLevel = 0 // 0..1，淡入淡出與路由切換共用的「邏輯目前音量」——不能拿 el.volume 當真實來源，
// 因為 iOS 上讀回來的值不可信（唯讀、可能恆為 1 或維持指派前的舊值）。

// 2026-09-14 修復 B：R2 CORS 白名單不含目前網域（例如本機 dev 的 http://localhost:3000）時，帶
// crossOrigin='anonymous' 的請求會被瀏覽器直接判定載入失敗——不是「聲音小一點/滑桿失效」這種優雅
// 降級，是整段沒聲音。用這個 Set 記錄「哪些 kind 已經降級重試過」，確保 degradeBgmElement 對每個
// kind 最多只觸發一次，不會因為降級後仍載入失敗（例如真的網路異常）而無限重建元素、無限重試。
const bgmCorsDegradedKinds = new Set<BgmKind>()
// 2026-09-14 審查修正（B1）：上面的降級判斷原本「只要元素有 crossOrigin 就當成 CORS 問題」，會把
// 暫時性網路錯誤（MEDIA_ERR_NETWORK，例如邊跑步邊玩時訊號抖一下、CDN 冷邊緣）也誤判成「這個網域
// 不在白名單」而永久放棄 Web Audio 路由——使用者不會發現，只會覺得音量滑桿從某一刻開始失效（等於
// 悄悄吃掉 iOS 音量修復）。改成：同一個 kind 先用原設定重試一次（load()），第二次仍失敗才降級。
const bgmRetriedKinds = new Set<BgmKind>()

function detectElementVolumeControllable(el: HTMLAudioElement): boolean {
  // 用「設一個不同的值再讀回來」偵測 el.volume 是否真的生效，取代瞎猜 UA——iOS Safari 刻意把
  // HTMLMediaElement.volume 設計成唯讀（逼 App 交給硬體音量鍵），純同步操作，不需要播放中也能測。
  try {
    const original = el.volume
    const probe = original > 0.5 ? 0.1 : 0.9
    el.volume = probe
    const applied = Math.abs(el.volume - probe) < 0.01
    el.volume = original
    return applied
  } catch {
    return false
  }
}

// 建立一顆 BGM <audio> 元素；withCors=true 是預設（一般路徑，接 Web Audio 圖需要），withCors=false
// 是修復 B 的降級路徑（放棄 Web Audio 路由，換取「至少載得起來、聽得到」）。error 事件監聽兩種都掛：
// 就算是已降級的非 CORS 元素也可能因為真的網路異常而載入失敗，仍要把診斷寫進 bgmBlocked/bgmLastError。
function createBgmElement(withCors: boolean): HTMLAudioElement {
  const el = new Audio()
  el.loop = true
  el.preload = 'auto'
  el.volume = 0
  if (withCors) {
    // 必須在第一次指派 src 之前設定：之後要把這顆 <audio> 接進 Web Audio 圖
    // （createMediaElementSource）才能在 iOS 上控制音量，跨網域媒體要進 Web Audio 圖必須先有
    // CORS——R2（img.dor.tw）已於 2026-09-14 設好（允許 dor.tw / www.dor.tw / localhost:3123 的
    // GET/HEAD，見 cdn.ts 說明），但白名單目前不含 `npm run dev` 的 http://localhost:3000，見
    // handleBgmElementError 的降級處理。若晚於 src 賦值才設 crossOrigin 不會生效（第一次請求已經
    // 用非匿名模式送出，之後接 Web Audio 圖會判定資源「已污染」而整段靜音，不會報錯，只是聽不到）。
    el.crossOrigin = 'anonymous'
  }
  el.addEventListener('error', handleBgmElementError)
  return el
}

// 2026-09-14 修復 B：目前網域不在 R2 CORS 白名單時，帶 crossOrigin='anonymous' 的請求會被瀏覽器
// 直接判定載入失敗（error 事件，networkState=NETWORK_NO_SOURCE），不是優雅降級、是整段沒聲音。
// 用 evt.currentTarget 而非模組變數 bgmEl 判斷「是誰噴的錯」：降級會整顆汰換元素，若舊元素的 error
// 事件姍姍來遲才觸發（此時 bgmEl 已經指向新元素），不能誤判成新元素出錯而重複降級或誤蓋診斷。
function handleBgmElementError(evt: Event) {
  const el = evt.currentTarget as HTMLAudioElement | null
  if (!el || el !== bgmEl) return // 舊元素被降級流程汰換後才觸發的事件，已經與現況無關
  const kind = currentBgmKind
  if (el.crossOrigin && kind && !bgmCorsDegradedKinds.has(kind)) {
    // 第一次失敗：先用原設定重試一次。CORS 被拒是決定性的（同樣的請求一定再被拒一次），暫時性網路
    // 錯誤則多半一次就好——用「重試後仍失敗」把兩者分開，避免單一次瞬斷就永久降級。
    if (!bgmRetriedKinds.has(kind)) {
      bgmRetriedKinds.add(kind)
      console.warn(`[dorpg audio] BGM(${kind}) 載入失敗，先原樣重試一次再決定是否降級`, el.error)
      try {
        el.load()
        if (bgmWasPlaying) attemptBgmPlay(el)
      } catch {
        /* load() 失敗就讓下一次 error 事件走到降級分支 */
      }
      return
    }
    bgmCorsDegradedKinds.add(kind)
    console.warn(
      `[dorpg audio] BGM(${kind}) 重試後仍失敗，判定為目前網域不在 R2 CORS 白名單，改用不設 ` +
        'crossOrigin 的 <audio> 重試（放棄 Web Audio 路由升級，iOS 音量滑桿可能失效，但至少聽得到）',
      el.error,
    )
    degradeBgmElement(kind)
    return
  }
  // 已經是降級後的非 CORS 元素仍然出錯，或壓根不是 CORS 問題（例如真的網路異常/URL 錯誤）：
  // 不再重試，只記錄診斷，交給 getBgmDiagnostics 讓人查。
  bgmBlocked = true
  bgmLastError = el.error ? `media element error (code=${el.error.code})` : 'bgm element error event'
}

// 換一顆不設 crossOrigin 的新元素重試同一首，接手正在播放/待播放的狀態（音量、是否該播放中）。
// bgmRouting 鎖死在 'element' 且不再嘗試升級：沒有 CORS 的跨網域媒體一旦接上
// createMediaElementSource 音訊圖，聲音會被瀏覽器安全機制整個消音而不拋錯，比「完全沒接上」更難
// 察覺，寧可放棄 iOS 音量滑桿的控制權，換取「至少聽得到」。
function degradeBgmElement(kind: BgmKind) {
  const oldEl = bgmEl
  const prevVolume = bgmVolumeLevel
  const wasPlaying = bgmWasPlaying
  const el = createBgmElement(false)
  bgmEl = el
  bgmElVolumeControllable = detectElementVolumeControllable(el)
  bgmRouting = 'element'
  bgmRoutingAttempted = true
  // 2026-09-14 審查修正（B2）：若降級發生在 Web Audio 路由已升級之後（例如播了一陣子才串流中斷），
  // 舊的 source/gain 仍連在 ctx.destination 上——依規格，連到 destination 的節點即使沒有 JS 參照也
  // 不會被 GC，會靜靜留在音訊圖裡直到分頁關閉。先斷線再丟掉參照。
  try {
    bgmSourceNode?.disconnect()
    bgmGainNode?.disconnect()
  } catch {
    /* 已經斷線或 context 狀態異常：丟掉參照即可，不影響降級流程 */
  }
  bgmSourceNode = null
  bgmGainNode = null
  currentBgmKind = null // 強制下面的 prepareBgmElementForKind 對新元素重新指派 src（新元素 src 是空的）
  prepareBgmElementForKind(kind, el)
  applyBgmVolume(prevVolume) // 沿用降級前的邏輯音量，避免換元素瞬間音量跳動或靜音
  bgmWasPlaying = wasPlaying
  if (wasPlaying) attemptBgmPlay(el)
  try {
    oldEl?.pause()
    if (oldEl) oldEl.src = '' // 中止舊元素還在進行中的請求；就算之後仍觸發 error 也會被上面的身分檢查擋掉
  } catch {
    /* 舊元素即將被捨棄，清理失敗也無妨 */
  }
}

function ensureBgmElement(): HTMLAudioElement | null {
  if (!isBrowser()) return null
  if (!bgmEl) {
    bgmEl = createBgmElement(true)
    bgmElVolumeControllable = detectElementVolumeControllable(bgmEl)
  }
  return bgmEl
}

function clearBgmFade() {
  if (bgmFadeTimer != null) {
    clearInterval(bgmFadeTimer)
    bgmFadeTimer = null
  }
}

// 音量的唯一寫入入口：依目前路由決定寫 gain 還是 el.volume，讓 fadeBgmTo／setMusicVolume／路由
// 切換三處呼叫端都不用關心底層是哪一種，也不會在切換路由的瞬間造成音量跳動（見 maybeUpgradeBgmRouting
// 切換時會拿 bgmVolumeLevel 當 gain 初始值，銜接淡入淡出進度）。
function applyBgmVolume(vol: number) {
  if (bgmRouting === 'webaudio' && bgmGainNode) {
    bgmGainNode.gain.value = vol
    return
  }
  if (!bgmEl) return
  try {
    bgmEl.volume = vol
  } catch {
    /* 部分瀏覽器（含少數 iOS 版本）指派 volume 會拋例外而非單純忽略，忽略即可——見 bgmElVolumeControllable 診斷 */
  }
}

function setBgmVolumeLevel(vol: number) {
  bgmVolumeLevel = Math.max(0, Math.min(1, vol))
  applyBgmVolume(bgmVolumeLevel)
}

function fadeBgmTo(target: number, ms: number, onDone?: () => void) {
  if (!bgmEl) return
  clearBgmFade()
  const clampedTarget = Math.max(0, Math.min(1, target))
  if (ms <= 0) {
    setBgmVolumeLevel(clampedTarget)
    onDone?.()
    return
  }
  const start = bgmVolumeLevel
  const startedAt = performance.now()
  bgmFadeTimer = setInterval(() => {
    const t = Math.min(1, (performance.now() - startedAt) / ms)
    setBgmVolumeLevel(start + (clampedTarget - start) * t)
    if (t >= 1) {
      clearBgmFade()
      onDone?.()
    }
  }, 30)
}

// 切歌／首次播放共用：處理 src／currentTime／currentBgmKind，回傳「是不是換了一首新的」給呼叫端
// 決定要不要重設音量從 0 淡入。同一首重複呼叫必須是 no-op（讓連續多場戰鬥不斷音，見檔頭修復記錄）。
function prepareBgmElementForKind(kind: BgmKind, el: HTMLAudioElement): boolean {
  if (currentBgmKind === kind) return false
  currentBgmKind = kind
  el.pause()
  el.src = bgmUrl(kind)
  el.currentTime = 0
  return true
}

// el.play() 的唯一呼叫入口：同步呼叫、不 throw，把結果寫進 bgmBlocked/bgmLastError 供
// getBgmDiagnostics 觀測。playBgmFromGesture 依賴這裡「不 await 任何東西」才能維持手勢堆疊。
function attemptBgmPlay(el: HTMLAudioElement) {
  bgmBlocked = false
  bgmLastError = null
  let playResult: Promise<void> | undefined
  try {
    playResult = el.play()
  } catch (err) {
    // 極少數瀏覽器會同步拋例外而非回傳被拒的 Promise；一律視為播放被擋，行為與非同步拒絕一致。
    bgmBlocked = true
    bgmLastError = err instanceof Error ? err.message : String(err)
    return
  }
  if (playResult && typeof playResult.catch === 'function') {
    playResult.catch((err: unknown) => {
      bgmBlocked = true
      bgmLastError = err instanceof Error ? err.message : String(err)
      console.warn('[dorpg audio] BGM 播放被瀏覽器拒絕（可能尚未於使用者手勢內呼叫 playBgmFromGesture()）', err)
    })
  }
}

// 只在 AudioContext 已經 running 時才接線——規格上一旦 createMediaElementSource() 過，元素的聲音
// 就「只」從這張音訊圖出來，若圖還沒開始跑（ctx suspended）就會整個沒聲音，因此還沒 running 之前寧可
// 先讓元素直出（桌機正常、iOS 至少聽得到，只是滑桿沒用），等真的 running 了再補接線並無縫接手音量。
function maybeUpgradeBgmRouting() {
  if (!isBrowser() || !bgmEl || bgmRoutingAttempted) return
  const ctx = audioCtx
  if (!ctx || ctx.state !== 'running') return
  bgmRoutingAttempted = true // 不論成敗都只嘗試一次：對同一元素第二次呼叫 createMediaElementSource 必定拋例外
  try {
    bgmSourceNode = ctx.createMediaElementSource(bgmEl)
    bgmGainNode = ctx.createGain()
    bgmGainNode.gain.value = bgmVolumeLevel // 承接目前的邏輯音量，切換路由不造成音量跳動
    bgmSourceNode.connect(bgmGainNode).connect(ctx.destination)
    bgmEl.volume = 1 // 音量之後全交給 gain 控制，避免跟殘留的 el.volume 疊乘造成過小聲
    bgmRouting = 'webaudio'
  } catch (err) {
    bgmLastError = err instanceof Error ? err.message : String(err)
    console.warn('[dorpg audio] 接上 Web Audio 失敗，退回 <audio> 直出（桌機仍聽得到，iOS 音量滑桿可能失效）', err)
  }
}

// navigator.audioSession 是 iOS 16.4+ Safari 才有的新 API，目前主流 TS lib.dom 型別尚未收錄，
// 自訂最小介面做特徵偵測，不用 any。
interface AudioSessionLike {
  type: string
}
let iosAudioSessionConfigured = false
function setupIosAudioSession() {
  if (!isBrowser() || iosAudioSessionConfigured) return
  try {
    const nav = navigator as Navigator & { audioSession?: AudioSessionLike }
    if (nav.audioSession && typeof nav.audioSession === 'object') {
      // 'playback' 讓 BGM 不被硬體靜音鍵切掉（遊戲慣例）；使用者仍可用設定面板的音樂滑桿關掉。
      nav.audioSession.type = 'playback'
      iosAudioSessionConfigured = true
    }
  } catch {
    /* 不支援此 API 的瀏覽器：忽略即可，靜音鍵行為退回系統預設 */
  }
}

// 手勢播放與一般播放共用的非同步善後：resume AudioContext、接上 Web Audio 圖（拿到 iOS 音量控制權）、
// 設定 iOS 靜音鍵行為。刻意跟「同步呼叫 el.play()」分開，讓呼叫端可以在手勢 handler 裡先同步播音再
// 呼叫這裡（void 呼叫、不等待），這些善後即使晚完成或被拒絕也不影響已經同步發出的 play()。
async function settleBgmAudioGraph(): Promise<void> {
  const ctx = ensureAudioContext()
  // 2026-09-14 修復 D：只判斷 'suspended' 漏掉 iOS 來電等 OS 級中斷會進入的獨立 'interrupted' 狀態
  // （AudioContextState 除了 suspended/running/closed 之外還有這個值）——判斷式為 false 就永遠不會
  // 呼叫 resume()。改判斷「不是 running」涵蓋 suspended/interrupted 兩種都要救回來的狀態。
  if (ctx && ctx.state !== 'running') {
    try {
      await ctx.resume()
    } catch {
      /* 不在合法使用者手勢情境下呼叫會被拒絕：忽略即可，下一次手勢／播放呼叫再試一次 */
    }
  }
  maybeUpgradeBgmRouting()
  setupIosAudioSession()
}

/** 非手勢情境的續播/切曲（例如從一般場切到 boss 場、從背景回前景）。已經在播同一首就是 no-op。 */
function playBgm(kind: BgmKind): void {
  if (!isBrowser()) return
  ensureVolumesLoaded()
  const el = ensureBgmElement()
  if (!el) return

  if (currentBgmKind === kind) {
    // 重複呼叫同一個 kind 不重播：頂多是曾被暫停（背景分頁／先前 play() 被拒絕過）就續播，不重設進度、不重新淡入。
    bgmWasPlaying = true
    if (el.paused) attemptBgmPlay(el)
    void settleBgmAudioGraph()
    return
  }

  prepareBgmElementForKind(kind, el)
  bgmWasPlaying = true
  bgmVolumeLevel = 0
  applyBgmVolume(0)
  attemptBgmPlay(el)
  fadeBgmTo(musicVolume / 100, 600)

  void settleBgmAudioGraph()
}

/**
 * 必須在使用者手勢的「同步」呼叫堆疊內呼叫（pointerdown/click handler 第一行，前面不可有 await）。
 * 內部順序固定：①ensureBgmElement（含 crossOrigin）②需要時設 src ③同步呼叫 el.play()
 * ④才去做 ctx.resume() 等非同步工作。回傳 void，不讓呼叫端有機會 await 之後才播——這是修復
 * iOS Safari「BGM 永遠沒聲音」的關鍵：原本的呼叫鏈把 play() 排在 `unlock()` 的 await 之後，
 * 已經脫離手勢的同步呼叫堆疊，iOS 一律拒絕。
 */
function playBgmFromGesture(kind: BgmKind): void {
  if (!isBrowser()) return
  ensureVolumesLoaded()
  const el = ensureBgmElement()
  if (!el) return

  if (currentBgmKind === kind) {
    // 同一首重複呼叫：不重播、不重設進度、不重新淡入，只在意外暫停時（背景分頁／先前被拒絕）續播。
    bgmWasPlaying = true
    if (el.paused) attemptBgmPlay(el) // 同步呼叫，滿足手勢堆疊限制
    void settleBgmAudioGraph()
    return
  }

  prepareBgmElementForKind(kind, el)
  bgmWasPlaying = true
  bgmVolumeLevel = 0
  applyBgmVolume(0)

  // === 關鍵：呼叫堆疊到這裡為止全程同步，el.play() 前沒有任何 await ===
  attemptBgmPlay(el)
  fadeBgmTo(musicVolume / 100, 600)

  // 手勢之後才做的非同步善後，即使晚完成或被拒絕也不影響上面已經同步發出的 play()。
  void settleBgmAudioGraph()
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

/** 觀測用：BGM 是否真的在播（!paused && !ended && readyState>=2）。給 UI 與自動化測試判斷。 */
function isBgmPlaying(): boolean {
  if (!isBrowser() || !bgmEl) return false
  const elementPlaying = !bgmEl.paused && !bgmEl.ended && bgmEl.readyState >= 2
  if (!elementPlaying) return false
  if (bgmRouting === 'webaudio') {
    // 2026-09-14 修復 D：接上 Web Audio 圖之後，聲音只從音訊圖出來；ctx 不是 running（例如 iOS
    // 來電等 OS 級中斷進入 'interrupted' 狀態）代表元素雖然在播、readyState 也正常，實際上整段
    // 靜音——不能只看元素本身的播放狀態，否則會回報「在播」但玩家其實聽不到任何聲音。
    return audioCtx?.state === 'running'
  }
  return true
}

/** 診斷用：{ kind, paused, blocked, lastError, routing, volumeControllable, corsDegraded, corsDegradedKinds }。 */
function getBgmDiagnostics(): Record<string, unknown> {
  return {
    kind: currentBgmKind,
    paused: bgmEl ? bgmEl.paused : true,
    blocked: bgmBlocked,
    lastError: bgmLastError,
    routing: bgmRouting,
    volumeControllable: bgmRouting === 'webaudio' ? true : bgmElVolumeControllable,
    audioCtxState: audioCtx?.state ?? null,
    // 修復 B：目前網域是否曾因 R2 CORS 白名單未涵蓋而降級成非 CORS 元素（見 handleBgmElementError）。
    corsDegraded: bgmCorsDegradedKinds.size > 0,
    corsDegradedKinds: Array.from(bgmCorsDegradedKinds),
  }
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
      // 2026-09-14 修復 D：iOS 從 'interrupted'（來電等 OS 級中斷）恢復 'running' 是瀏覽器自己
      // 觸發的，不會經過 settleBgmAudioGraph 那條「使用者手勢→resume()」的路徑——沒有人會在這個
      // 時間點主動呼叫任何 API。用 statechange 補一次音量套用當保險：萬一某些瀏覽器實作在恢復
      // running 時把 GainNode/el.volume 重置或維持在恢復前的中斷值，這裡重新套用目前的邏輯音量
      // 讓聲音確實對齊使用者設定，而不是不上不下的殘留狀態。
      audioCtx.addEventListener('statechange', () => {
        if (audioCtx?.state === 'running') applyBgmVolume(bgmVolumeLevel)
      })
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
  // 修復 D：同 settleBgmAudioGraph，'suspended' 之外還有 'interrupted'（iOS 來電等 OS 級中斷）
  // 也需要 resume()，只判斷 suspended 會漏掉這個狀態、永遠不 resume。
  if (ctx && ctx.state !== 'running') {
    try {
      await ctx.resume()
    } catch {
      /* 瀏覽器拒絕（例如根本不在使用者手勢呼叫堆疊內）：之後的手勢再呼叫一次即可 */
    }
  }
  ensureBgmElement()
  maybeUpgradeBgmRouting()
  setupIosAudioSession()
}

function setMusicVolume(v: number) {
  ensureVolumesLoaded()
  musicVolume = clampVolume(v, DEFAULT_MUSIC_VOLUME)
  writeVolume(LS_MUSIC, musicVolume)
  if (bgmEl) {
    clearBgmFade() // 使用者正在拖滑桿：立即生效，取消任何進行中的淡入淡出動畫
    setBgmVolumeLevel(musicVolume / 100)
  }
}

function setSfxVolume(v: number) {
  ensureVolumesLoaded()
  sfxVolume = clampVolume(v, DEFAULT_SFX_VOLUME)
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
      // 回前景：只有先前確實在播放（沒被 stopBgm 主動停掉）才恢復。部分瀏覽器會在背景分頁時把
      // AudioContext 自動 suspend，若已經接上 Web Audio 圖（bgmRouting==='webaudio'）沒 resume
      // 就會變成「元素在播、卻整段靜音」，所以連同 settleBgmAudioGraph 一起重試。
      attemptBgmPlay(bgmEl)
      void settleBgmAudioGraph()
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
  playBgmFromGesture(kind: BgmKind): void
  playBgm(kind: BgmKind): void
  stopBgm(fadeMs?: number): void
  preloadSfx(ids: string[]): Promise<void>
  playSfx(id: string, gain?: number): void
  vibrate(pattern: number | number[]): void
  suspendOnHidden(): () => void
  isBgmPlaying(): boolean
  getBgmDiagnostics(): Record<string, unknown>
}

export const battleAudio: BattleAudioApi = {
  unlock,
  setMusicVolume,
  setSfxVolume,
  getVolumes,
  playBgmFromGesture,
  playBgm,
  stopBgm,
  preloadSfx,
  playSfx,
  vibrate,
  suspendOnHidden,
  isBgmPlaying,
  getBgmDiagnostics,
}
