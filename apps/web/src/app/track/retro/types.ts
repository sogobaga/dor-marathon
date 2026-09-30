// 復古 RPG（retro）GPS 地圖 — 共用型別。
// 本目錄（track/retro/）只在 `document.documentElement.dataset.skin === 'retro'` 時由 track/page.tsx
// 以 next/dynamic(ssr:false) 載入（比照 track/scifi/types.ts 的隔離說明），CONTRACT.md §4 要求
// 「沿用 scifi/types.ts 的 props 介面」——這裡刻意只做型別別名（純型別、無 runtime 程式碼、不 import
// maplibre-gl），複用同一組形狀，讓 page.tsx 的既有 SciFiPos/SciFiTarget/SciFiKmMark 資料快照可以
// 原封不動餵給 RetroMap，不必為了同一份資料多維護第二套型別。

export type {
  SciFiPos as RetroPos,
  SciFiTarget as RetroTarget,
  SciFiKmMark as RetroKmMark,
  SciFiStatus as RetroStatus,
} from '../scifi/types'

import type { SciFiPos, SciFiTarget, SciFiKmMark, SciFiStatus } from '../scifi/types'

export interface RetroMapProps {
  pos: SciFiPos | null
  status: SciFiStatus
  segments: [number, number][][]
  kmMarks: SciFiKmMark[]
  targets: SciFiTarget[]
  focusMode: boolean
  initialCenter: [number, number] // [lat, lng]
  initialZoom?: number
  onFallback: (reason: string) => void
  onTargetClick?: (target: SciFiTarget) => void
  // CONTRACT_R2 §2：底部可拖曳資訊面板頂端到畫面底的高度（CSS px，隨面板拖曳節流更新，
  // track/page.tsx 用既有 sheet.H／sheet.curY 算出，非本檔量測）。RetroMap 拿它呼叫
  // map.setPadding({bottom})，讓 GPS 跟隨的「中心」落在面板以上的可見地圖區正中央，而不是
  // 整個容器（含被面板蓋住那一半）的正中央——光點畫在同一個 map.project() 出來的點，因此
  // 不需要另外調整光點的畫法，padding 生效後 project() 自然反映新的可見區中心。專注模式開啟
  // 時忽略這個值，改用容器高度的上 45% 當可見區（見 RetroMap.tsx）。
  bottomInset?: number
  // ORBPOS_CONTRACT.md 第二輪 S2：使用者手勢暫停跟隨（true→false）／8 秒自動恢復或 recenter() 恢復
  // 跟隨（→true）時呼叫，讓 page.tsx 的 followRef/setFollowing 同步，藉此驅動「回到目前位置」按鈕。
  onFollowChange?: (following: boolean) => void
  // ROUTEPLAN_CONTRACT（比照 scifi/types.ts 同名欄位）：「路線規劃」建議路線（[lat,lng] 陣列，從目前
  // 位置接到目標點）；null／空陣列＝目前沒有規劃中的路線。畫法與已跑軌跡 segments 明顯區分（像素風
  // 虛線橘＋黑色描邊），疊在光點／目標點下方、圖磚上方，路線最後一點加終點標記。
  plannedRoute?: [number, number][] | null
}

export interface RetroMapHandle {
  recenter: (pos?: { lat: number; lng: number }) => void
  zoomBy: (delta: number) => void
  // ROUTEPLAN_CONTRACT：把鏡頭縮放到能看見整條建議路線（等同 Leaflet 的 fitBounds），比照使用者
  // 手勢暫停跟隨的規則呼叫 onFollowChange(false)，讓「回到目前位置」按鈕出現。
  fitRoute: (points: [number, number][]) => void
  // ROUTEPLAN_CONTRACT：程式化置中到指定座標（例如「前往打卡」深連結置中目標關主），同樣呼叫
  // onFollowChange(false) 暫停跟隨——避免置中後下一筆 GPS 定位又把鏡頭拉回目前位置，蓋掉這次置中。
  centerOn: (lat: number, lng: number, zoom?: number) => void
}
