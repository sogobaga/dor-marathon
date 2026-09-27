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
}

export interface SciFiMapHandle {
  recenter: (pos?: { lat: number; lng: number }) => void
  zoomBy: (delta: number) => void
}
