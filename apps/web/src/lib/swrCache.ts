import type { Cache } from 'swr'

// SWR 快取的「版號隔離持久化」provider：
// - 存到 localStorage → 重新整理/重開後仍在（開頁即時顯示上次資料，不空白 loading）。
// - 以 app 版號當前綴：版號一變（新部署）就清掉舊快取、強制重抓 → 避免拿到舊結構的資料。
// - 只持久化 data（不存 error/isValidating 等暫態），避免把錯誤狀態也快取起來。
// 2026-09-08 第二次稽核修法：再以「目前登入會員 user id」當第二層命名空間（見 currentUid）——
// 持久化的 key 本身就帶使用者身分，同裝置換帳號時天生就是不同的 localStorage entry，不會共用同一份。
const VER = process.env.NEXT_PUBLIC_APP_VERSION || 'dev'
const KEY = 'dor:swr:v1'
const VKEY = 'dor:swr:ver'
const MAX_ENTRY = 100 * 1024 // 單一 entry 上限（bytes，以 JSON.stringify 後長度估算）

type Entry = { data?: unknown }

// USER_TOKEN_KEY 必須跟 userAuth.ts 的 TOKEN_KEY 常數同一個字串——這裡不能 import userAuth.ts
// 來重用常數：userAuth.ts 本身會 import 這個檔案的 clearSwrCache/broadcastAuthChange，形成循環依賴。
// 兩邊各自宣告同一個字面值字串，任一邊改 key 名稱都要記得同步改另一邊。
const USER_TOKEN_KEY = 'dor_user_token'

// currentUid 解出目前登入會員 access token 的 sub（user id）claim，當作這份持久化快取的命名空間。
// 這裡只是「偷看」JWT payload 決定快取要存進哪個 localStorage key，不驗簽也不代表身分驗證
// （真正驗證在後端），偽造的 token 頂多讓攻擊者自己的快取寫到錯的命名空間，不會讀到別人的資料
// （因為別人的資料本來就存在別人 uid 的 localStorage entry 底下）。讀不到/解不出 → 'anon'
// （未登入或格式異常時，公開資料照樣可以快取，只是大家共用同一個 'anon' 命名空間）。
function currentUid(): string {
  if (typeof window === 'undefined') return 'anon'
  try {
    const token = localStorage.getItem(USER_TOKEN_KEY)
    if (!token) return 'anon'
    let b = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    b += '='.repeat((4 - (b.length % 4)) % 4)
    const payload = JSON.parse(atob(b))
    // 後端 Claims 的使用者 id 是 `uid`（見 services/api/internal/auth/service.go Claims json tag），不是標準 `sub`——
    // 讀錯欄位會讓所有登入者都落在 'anon' 命名空間、隔離形同虛設（審查抓到）。保留 sub 當備援。
    const uid = (typeof payload.uid === 'string' && payload.uid) || (typeof payload.sub === 'string' && payload.sub) || ''
    return uid || 'anon'
  } catch {
    return 'anon'
  }
}

// M3 修法：目前分頁「正在使用中」的 SWR 記憶體快取——只保留最後一個建立的 provider Map（AppProviders
// 的根層 SWRConfig 只掛一次，整個 app 生命週期只會有一個 provider 實例）。之所以需要另外抓一份參照，
// 是因為 swr 套件頂層 `import { mutate } from 'swr'` 綁定的是套件內部另一份「預設 cache」（一個獨立
// 建立的 Map），跟這裡透過 SWRConfig 的 `provider` 選項換上的自訂 Map 完全是兩個不同的物件——對
// 預設 cache 呼叫 mutate(() => true, ...) 不會影響這裡的 provider，清不到任何東西（實測驗證過，見
// clearSwrCache 呼叫點的說明）。故改為直接持有 provider 建立的 Map 本體，登出時直接呼叫 .clear()。
let liveCache: Map<string, Entry> | null = null

export function swrLocalStorageProvider(): Cache {
  const map = new Map<string, Entry>()
  liveCache = map
  if (typeof window === 'undefined') return map as unknown as Cache

  // 用「建立當下」的 uid 決定要讀哪個 namespace（等同這次頁面載入時是誰登入）。
  const loadUid = currentUid()
  const dataKey = `${KEY}:${loadUid}`
  const verKey = `${VKEY}:${loadUid}`
  try {
    if (localStorage.getItem(verKey) === VER) {
      const saved = localStorage.getItem(dataKey)
      if (saved) {
        const parsed = JSON.parse(saved) as { uid?: string; entries?: [string, Entry][] }
        // 雙重確認：即使 key 名稱本身已經帶 uid，payload 內也再存一份 uid 校驗——防的是「namespace
        // 算法本身出錯/未來改版忘了同步改」這種意外情境下，仍不會把不屬於這個 uid 的資料誤載進來。
        if (parsed && parsed.uid === loadUid && Array.isArray(parsed.entries)) {
          for (const [k, v] of parsed.entries) map.set(k, v)
        }
      }
    } else {
      // 版號變更 → 清掉這個 uid 的舊快取，記錄新版號
      localStorage.removeItem(dataKey)
      localStorage.setItem(verKey, VER)
    }
  } catch { /* JSON 壞掉 / 無痕模式 → 當空快取 */ }
  // 舊版（無 uid 命名空間）殘留的 key 一律清掉，不遷移——內容可能混著任何人的個人資料，
  // 寧可讓使用者多重抓一次，也不要把舊格式的內容誤判成屬於目前這個 uid。
  try {
    localStorage.removeItem(KEY)
    localStorage.removeItem(VKEY)
  } catch { /* ignore */ }

  const save = () => {
    // 每次存檔都重新讀一次目前 uid（而非用上面 loadUid）：同一分頁如果在沒有整頁重新整理的情況下
    // 換了登入者（登出後又登入），存檔要跟著存進「現在」這個人的 namespace，不能繼續寫回頁面剛載入
    // 時那個人的 key——那樣反而會把新使用者的資料存進舊使用者的 localStorage entry。
    const uid = currentUid()
    const dk = `${KEY}:${uid}`
    const vk = `${VKEY}:${uid}`
    try {
      const entries: [string, Entry][] = []
      map.forEach((v, k) => {
        if (!v || v.data === undefined) return
        const json = JSON.stringify(v.data)
        // 單一 entry 過大（例如某支 API 回傳整包大清單）就不持久化它，避免拖累 setItem 導致整包 all-or-nothing 失敗、
        // 連小而高頻的 key（dashboard/races/site-settings）也跟著沒存成。該 key 只留記憶體快取，本次 session 內仍可用。
        if (json.length > MAX_ENTRY) {
          if (process.env.NODE_ENV !== 'production') {
            console.warn(`[swrCache] skip persisting oversized entry "${k}" (${json.length} bytes > ${MAX_ENTRY})`)
          }
          return
        }
        entries.push([k, { data: v.data }])
      })
      localStorage.setItem(dk, JSON.stringify({ uid, entries }))
      localStorage.setItem(vk, VER)
    } catch { /* 空間滿 / 無痕 → 略過持久化，記憶體快取照常運作 */ }
  }
  // 關頁前存；手機常直接切背景不觸發 beforeunload → visibilitychange 也存一次
  window.addEventListener('beforeunload', save)
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') save() })

  // 跨分頁登入狀態同步（見下方 broadcastAuthChange/listenAuthBroadcast 註解）：只掛一次，
  // 跟這個 provider 的生命週期一致（整個 app 只會呼叫這個函式一次，見上方 M3 修法註解）。
  listenAuthBroadcast()

  return map as unknown as Cache
}

// 供登出時清空（避免同裝置下一位使用者看到上一位的快取資料）。清兩層：
// 1) 持久化那份（localStorage）——原本就有，防的是「重新整理/重開後」看到舊資料。
// 2) 目前分頁「正在使用中」的記憶體 Map（M3 修法新增）——不清這層的話，同一個分頁內使用者 A
//    登出、使用者 B 馬上登入（SPA 內沒有整頁重新整理），像 ['profile-rewards']（未以 user id
//    區分）這種 key 若剛好還沒被重新掛載的元件重新抓過，會短暫吃到記憶體裡 A 的舊資料。
// 直接 map.clear() 而非透過 swr 的 mutate 廣播失效：這裡整批清空後，使用者一律緊接著 emitAuthChange()
// 讓 useUser() 變化觸發相關畫面重新渲染/重新掛載，本來就會用新的 key（或全新 uid）重新抓一次，
// 不需要額外走 SWR 的重新驗證事件。
// 2026-09-08 修法：呼叫端（userAuth.ts clearUserSession）在呼叫這裡之前，storage 裡的
// dor_user_token 通常都已經被清掉了，所以這裡讀到的 currentUid() 多半已經是 'anon'——因此這裡
// 額外多清一輪「所有」`dor:swr:v1:*`／`dor:swr:ver:*` entries（而不是只清當下 uid 那一個），
// 確保剛登出那個使用者殘留的 namespace 一定被清乾淨，不留到下次同一人或別人登入時被誤讀。
export function clearSwrCache() {
  try {
    if (typeof window !== 'undefined') {
      for (const k of Object.keys(localStorage)) {
        if (k === KEY || k === VKEY || k.startsWith(`${KEY}:`) || k.startsWith(`${VKEY}:`)) localStorage.removeItem(k)
      }
    }
  } catch { /* ignore */ }
  liveCache?.clear()
}

// --- 跨分頁登入狀態廣播（2026-09-08 第二次稽核修法）---
// 情境：分頁 A 登出、分頁 B 還開著同一個裝置/瀏覽器（可能停在背景分頁）。B 的 JS 執行環境（記憶體
// 裡的 SWR Map、React 元件狀態）完全不知道 A 剛才登出了，之後只要 B 觸發任何存檔時機
// （beforeunload／切到背景的 visibilitychange），就會把「B 記憶體裡還留著的舊資料」寫回
// localStorage——如果那份舊資料剛好是 A 的個人資料（B 之前也用同一個 uid 登入過，或 B 本來就是
// A 本人的另一分頁），等於在 A 登出後又把 A 的資料重新寫回持久化快取，被下一位使用這台裝置的人
// （甚至下一次 A 自己登入前，若被別人搶先開同一台裝置）讀到。
// 修法：登入/登出時（見 userAuth.ts／adminAuth.ts）呼叫 broadcastAuthChange() 通知其他分頁；
// 其他分頁收到後立刻清掉自己的記憶體快取＋持久化快取，並整頁重新整理——重新整理後
// swrLocalStorageProvider() 會用「重新整理當下」的 uid 重新載入，天然不會有殘留的舊使用者資料。
const AUTH_BROADCAST_CHANNEL = 'dor-auth'
// 沒有 BroadcastChannel 的環境（極舊瀏覽器）退回寫這顆 marker key，靠 'storage' 事件通知其他分頁
// ——'storage' 事件的既定行為就是「只有其他分頁會收到，自己這頁不會」，天然符合「通知其他分頁」的需求。
const AUTH_MARKER_KEY = 'dor:auth:marker'
// 每個分頁一個隨機 ID：BroadcastChannel 的「不會收到自己那個 channel 物件送出的訊息」保證只涵蓋
// 同一個物件實體，這裡 broadcast 端每次都是新建的暫時性 channel 物件，跟長駐監聽用的物件不是同一個，
// 沒有這層 tabId 過濾的話，觸發登出/登入的那一頁自己也會收到廣播、被自己重新整理一次
// （不是錯誤行為，但沒必要——那一頁本來就已經透過既有流程正確更新了自己的狀態）。
const TAB_ID = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`

export function broadcastAuthChange() {
  if (typeof window === 'undefined') return
  try {
    if ('BroadcastChannel' in window) {
      const bc = new BroadcastChannel(AUTH_BROADCAST_CHANNEL)
      bc.postMessage({ type: 'auth-changed', tabId: TAB_ID })
      bc.close()
      return
    }
  } catch { /* ignore */ }
  try { localStorage.setItem(AUTH_MARKER_KEY, `${Date.now()}:${TAB_ID}`) } catch { /* ignore */ }
}

let listening = false
function listenAuthBroadcast() {
  if (typeof window === 'undefined' || listening) return
  listening = true
  const reactToOtherTabAuthChange = () => {
    clearSwrCache()
    // 跑步中（localStorage dor_gps_run 有進行中的軌跡）絕不強制重載——會拆掉 watchPosition、打斷 GPS 追蹤
    // （與 layout.tsx bootJs 的 hasRun() 守則同一口徑，審查抓到）。只清快取，不重載；該分頁下一次導覽自然會拿到新狀態。
    try { if (localStorage.getItem('dor_gps_run')) return } catch { /* ignore */ }
    window.location.reload()
  }
  try {
    if ('BroadcastChannel' in window) {
      const bc = new BroadcastChannel(AUTH_BROADCAST_CHANNEL)
      bc.onmessage = (ev) => {
        if (ev?.data?.tabId === TAB_ID) return // 自己這頁觸發的廣播，忽略
        reactToOtherTabAuthChange()
      }
      return
    }
  } catch { /* ignore */ }
  window.addEventListener('storage', (e) => {
    if (e.key !== AUTH_MARKER_KEY || !e.newValue) return
    if (e.newValue.endsWith(`:${TAB_ID}`)) return // 自己這頁觸發的廣播，忽略
    reactToOtherTabAuthChange()
  })
}
