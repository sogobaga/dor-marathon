// 未來科幻世界（scifi）GPS 地圖 — 共用型別。
// 本目錄（track/scifi/）只在 `document.documentElement.dataset.skin === 'scifi'` 時由 track/page.tsx
// 以 next/dynamic(ssr:false) 載入，見該檔案掛載處註解與 CONTRACT.md §4.1。
// 這裡刻意只放「純型別」（無 runtime 程式碼、不 import maplibre-gl），讓 page.tsx 平時（非 scifi）
// 也能安全 import type 而不拖進任何 bundle 內容。

export interface SciFiPos {
  lat: number
  lng: number
  acc?: number
  heading?: number | null
  // ORBPOS_CONTRACT.md 修正 B：這筆定位「被 App 收到」的時間戳（epoch ms，非 GPS 裝置自己回報的
  // pos.timestamp——快取定位可能回報較舊的內部時間，會誤判成早就過期）。由 track/page.tsx 的 onPos
  // 在 setCurPos 當下同步記錄、透過 mapSnapshot 帶入；地圖元件用它判斷「超過 15 秒沒收到新定位」→
  // 光點改為「定位中」外觀。選填：舊快照／尚未有任何定位時可能是 undefined，地圖端應 fallback 成
  // 「pos 本身最後一次變化的時間」而非直接當作永遠新鮮。
  ts?: number
}

// 與 page.tsx 既有 checkpoints/exploreCps/focusBoss 統一映射後的目標點形狀（見 CONTRACT.md §4.1）。
export interface SciFiTarget {
  id: string
  lat: number
  lng: number
  radius: number // 公尺
  label: string
  kind: 'checkpoint' | 'boss' | 'focus'
  done: boolean
}

export interface SciFiKmMark {
  km: number
  lat: number
  lng: number
}

export type SciFiStatus = 'idle' | 'tracking' | 'paused' | 'done'

export interface SciFiMapProps {
  pos: SciFiPos | null
  status: SciFiStatus
  // 依既有 Leaflet 邏輯的軌跡分段（排除訊號中斷/跳點的壞段後，剩下的每一段各自是一條連續路線）；
  // 即時追蹤時通常只有一段（見 track/page.tsx pointsRef），歷史/接續等情境可能不只一段。
  segments: [number, number][][]
  kmMarks: SciFiKmMark[]
  targets: SciFiTarget[]
  // 專注模式（RaceFocusMode 開啟）：地圖降到 10fps、關閉光雨、粒子數減半，見 CONTRACT.md §4.3。
  focusMode: boolean
  initialCenter: [number, number] // [lat, lng]
  initialZoom?: number
  onFallback: (reason: string) => void // WebGL 不支援／逾時／context lost／例外 → 父層卸載本元件、恢復 Leaflet
  onTargetClick?: (target: SciFiTarget) => void
  // ORBPOS_CONTRACT.md 第二輪 S1：比照 CuteMapProps／RetroMapProps 的 bottomInset——底部可拖曳資訊面板
  // 頂端到畫面底的高度（CSS px），SciFiMap 拿它呼叫 map.setPadding({bottom}) 讓跟隨鏡頭把靈魂置中在
  // 面板以上的可見地圖區，車頭朝上時身後軌跡才不會整段被面板蓋住。專注模式開啟時改用容器高度上 45%。
  bottomInset?: number
  // ORBPOS_CONTRACT.md 第二輪 S2：使用者手勢暫停跟隨（true→false）／8 秒自動恢復或 recenter() 恢復
  // 跟隨（→true）時呼叫，讓 page.tsx 的 followRef/setFollowing 同步，藉此驅動「回到目前位置」按鈕。
  onFollowChange?: (following: boolean) => void
}

export interface SciFiMapHandle {
  recenter: (pos?: { lat: number; lng: number }) => void
  zoomBy: (delta: number) => void
}
