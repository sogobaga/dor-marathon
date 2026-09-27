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
  // 整個容器（含被面板蓋住那一半）的正中央——勇者畫在同一個 map.project() 出來的點，因此
  // 不需要另外調整勇者的畫法，padding 生效後 project() 自然反映新的可見區中心。專注模式開啟
  // 時忽略這個值，改用容器高度的上 45% 當可見區（見 RetroMap.tsx）。
  bottomInset?: number
}

export interface RetroMapHandle {
  recenter: (pos?: { lat: number; lng: number }) => void
  zoomBy: (delta: number) => void
}
