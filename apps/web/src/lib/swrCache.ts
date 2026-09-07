import type { Cache } from 'swr'

// SWR 快取的「版號隔離持久化」provider：
// - 存到 localStorage → 重新整理/重開後仍在（開頁即時顯示上次資料，不空白 loading）。
// - 以 app 版號當前綴：版號一變（新部署）就清掉舊快取、強制重抓 → 避免拿到舊結構的資料。
// - 只持久化 data（不存 error/isValidating 等暫態），避免把錯誤狀態也快取起來。
const VER = process.env.NEXT_PUBLIC_APP_VERSION || 'dev'
const KEY = 'dor:swr:v1'
const VKEY = 'dor:swr:ver'
const MAX_ENTRY = 100 * 1024 // 單一 entry 上限（bytes，以 JSON.stringify 後長度估算）

type Entry = { data?: unknown }

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

  try {
    if (localStorage.getItem(VKEY) === VER) {
      const saved = localStorage.getItem(KEY)
      if (saved) for (const [k, v] of JSON.parse(saved) as [string, Entry][]) map.set(k, v)
    } else {
      // 版號變更 → 清掉舊快取，記錄新版號
      localStorage.removeItem(KEY)
      localStorage.setItem(VKEY, VER)
    }
  } catch { /* JSON 壞掉 / 無痕模式 → 當空快取 */ }

  const save = () => {
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
      localStorage.setItem(KEY, JSON.stringify(entries))
      localStorage.setItem(VKEY, VER)
    } catch { /* 空間滿 / 無痕 → 略過持久化，記憶體快取照常運作 */ }
  }
  // 關頁前存；手機常直接切背景不觸發 beforeunload → visibilitychange 也存一次
  window.addEventListener('beforeunload', save)
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') save() })

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
export function clearSwrCache() {
  try {
    if (typeof window !== 'undefined') localStorage.removeItem(KEY)
  } catch { /* ignore */ }
  liveCache?.clear()
}
