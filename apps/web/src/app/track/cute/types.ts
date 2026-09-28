// 溫馨可愛（cute）GPS 地圖 — 共用型別。
// 本目錄（track/cute/）只在 `getActiveSkin() === 'cute'` 為真時由 track/page.tsx 以
// next/dynamic(ssr:false) 載入（比照 track/retro/types.ts 的隔離說明），docs/skins/CUTE_CONTRACT.md§4
// 沿用 scifi/retro 同一組資料快照形狀——這裡刻意只做型別別名（純型別、無 runtime 程式碼、不 import
// maplibre-gl），複用同一份 pos/segments/kmMarks/targets 快照，不必為了同一份資料多維護第三套型別。

export type {
  SciFiPos as CutePos,
  SciFiTarget as CuteTarget,
  SciFiKmMark as CuteKmMark,
  SciFiStatus as CuteStatus,
} from '../scifi/types'

import type { SciFiPos, SciFiTarget, SciFiKmMark, SciFiStatus } from '../scifi/types'

export interface CuteMapProps {
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
  // 比照 RetroMapProps.bottomInset：底部可拖曳資訊面板頂端到畫面底的高度（CSS px，隨面板拖曳節流
  // 更新），CuteMap 拿它呼叫 map.setPadding({bottom})，讓 GPS 跟隨鏡頭的「中心」落在面板以上的可見
  // 地圖區正中央。專注模式開啟時改用容器高度的上 45%（見 CuteMap.tsx）。
  bottomInset?: number
}

export interface CuteMapHandle {
  recenter: (pos?: { lat: number; lng: number }) => void
  zoomBy: (delta: number) => void
}
