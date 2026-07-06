import type { Cache } from 'swr'

// SWR 快取的「版號隔離持久化」provider：
// - 存到 localStorage → 重新整理/重開後仍在（開頁即時顯示上次資料，不空白 loading）。
// - 以 app 版號當前綴：版號一變（新部署）就清掉舊快取、強制重抓 → 避免拿到舊結構的資料。
// - 只持久化 data（不存 error/isValidating 等暫態），避免把錯誤狀態也快取起來。
const VER = process.env.NEXT_PUBLIC_APP_VERSION || 'dev'
const KEY = 'dor:swr:v1'
const VKEY = 'dor:swr:ver'

type Entry = { data?: unknown }

export function swrLocalStorageProvider(): Cache {
  const map = new Map<string, Entry>()
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
      map.forEach((v, k) => { if (v && v.data !== undefined) entries.push([k, { data: v.data }]) })
      localStorage.setItem(KEY, JSON.stringify(entries))
      localStorage.setItem(VKEY, VER)
    } catch { /* 空間滿 / 無痕 → 略過持久化，記憶體快取照常運作 */ }
  }
  // 關頁前存；手機常直接切背景不觸發 beforeunload → visibilitychange 也存一次
  window.addEventListener('beforeunload', save)
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') save() })

  return map as unknown as Cache
}

// 供登出時清空（避免同裝置下一位使用者看到上一位的快取資料）。
export function clearSwrCache() {
  try {
    if (typeof window !== 'undefined') localStorage.removeItem(KEY)
  } catch { /* ignore */ }
}
