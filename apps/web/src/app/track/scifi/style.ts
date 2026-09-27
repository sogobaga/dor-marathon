// 未來科幻世界（scifi）GPS 地圖 — MapLibre style JSON 程式產生（CONTRACT.md §4.2）。
// 資料源：OpenFreeMap 免金鑰向量圖磚（OpenMapTiles schema，已於 2026-09-27 以 curl 對
// https://tiles.openfreemap.org/planet 的 TileJSON 實際核對 source-layer 名稱與欄位，見回報）。
// glyphs 走官方 fonts 端點；只用 "Noto Sans Regular"／"Noto Sans Bold"（已核對存在）。中文標籤改靠
// MapLibre Map 建構子的 localIdeographFontFamily（見 SciFiMap.tsx），不依賴 CJK glyph 圖磚。
import type { StyleSpecification } from 'maplibre-gl'

// 色盤（對齊 CONTRACT.md §3 全站 token，此處為地圖專用微調）
const BG = '#02040a'
const WATER = '#031428'
const WATER_LINE = 'rgba(53,230,255,.25)'
const PARK = '#04181c'
const LANDUSE = '#040a16'
const FUG = '#35e6ff'
const FUG_BRIGHT = '#7ff3ff'
const LABEL = '#7fdfff'

// z16 目標寬度（CSS px，CONTRACT_R2.md §3）：呼叫端傳「z16 這一級要長怎樣」，這裡把它展開成完整的
// zoom interpolate stops——不再用單一 core*倍率去猜，直接鎖死 z16 那一站的數值，才能保證驗收量測
// （z16 主幹道光暈≥14/亮芯2.5、一般道路光暈8/亮芯1.2）不因為插值誤差而落空。
function widthAtZ16(z16: number): unknown[] {
  return ['interpolate', ['linear'], ['zoom'], 10, z16 * 0.28, 14, z16 * 0.68, 16, z16, 18, z16 * 1.4, 20, z16 * 2.1]
}

// 兩層一組（外層模糊光暈 + 內層細亮芯），組成參考圖 1 的發光路網；filter 依 transportation 的 class 欄位分級。
function roadLayerPair(id: string, classes: string[], coreZ16: number, glowZ16: number, glowOpacity: number, coreColor: string, glowBlur = 6): any[] {
  const filter = ['match', ['get', 'class'], classes, true, false]
  return [
    {
      id: `road-glow-${id}`, type: 'line', source: 'openmaptiles', 'source-layer': 'transportation', filter,
      layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: { 'line-color': FUG, 'line-width': widthAtZ16(glowZ16), 'line-blur': glowBlur, 'line-opacity': glowOpacity },
    },
    {
      id: `road-core-${id}`, type: 'line', source: 'openmaptiles', 'source-layer': 'transportation', filter,
      layout: { 'line-cap': 'round', 'line-join': 'round' },
      paint: { 'line-color': coreColor, 'line-width': widthAtZ16(coreZ16), 'line-opacity': 0.95 },
    },
  ]
}

export function buildScifiStyle(): StyleSpecification {
  return {
    version: 8,
    name: 'dor-scifi',
    glyphs: 'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf',
    sources: {
      // 用 TileJSON url 讓 MapLibre 自動抓 tiles/minzoom/maxzoom（見 planet TileJSON，maxzoom=14，
      // 高於此 zoom 由 MapLibre 自動 overzoom，不會多打不存在的圖磚）。
      openmaptiles: { type: 'vector', url: 'https://tiles.openfreemap.org/planet' },
    },
    // 使用 fill-extrusion 需要 `sky` 才有霧化地平線效果，由 SciFiMap.tsx 在 map 建立後以 map.setSky() 設定
    // （SkySpecification 是 runtime API，不放在靜態 style JSON 內，見該檔）。
    layers: [
      { id: 'background', type: 'background', paint: { 'background-color': BG } },
      { id: 'landcover', type: 'fill', source: 'openmaptiles', 'source-layer': 'landcover', paint: { 'fill-color': LANDUSE, 'fill-opacity': 0.6 } },
      { id: 'landuse', type: 'fill', source: 'openmaptiles', 'source-layer': 'landuse', paint: { 'fill-color': LANDUSE, 'fill-opacity': 0.5 } },
      { id: 'park', type: 'fill', source: 'openmaptiles', 'source-layer': 'park', paint: { 'fill-color': PARK, 'fill-opacity': 0.8 } },
      { id: 'water', type: 'fill', source: 'openmaptiles', 'source-layer': 'water', paint: { 'fill-color': WATER } },
      { id: 'water-outline', type: 'line', source: 'openmaptiles', 'source-layer': 'water', paint: { 'line-color': WATER_LINE, 'line-width': 1 } },
      { id: 'waterway', type: 'line', source: 'openmaptiles', 'source-layer': 'waterway', paint: { 'line-color': WATER, 'line-width': 1.5 } },

      // 道路：由細到粗依序疊（minor→mid→major），交叉口讓主幹道蓋在上面。z16 目標寬度（CSS px，
      // CONTRACT_R2.md §3）：「一般道路」（minor/path/tertiary）光暈8／亮芯1.2；「主幹道」
      // （primary/secondary/motorway/trunk）光暈≥14／亮芯2.5（primary 用 15/2.6、major 用 18/3，
      // 皆已高於門檻留餘裕）。
      ...roadLayerPair('minor', ['minor', 'service', 'track'], 1.2, 8, 0.18, FUG, 4),
      ...roadLayerPair('path', ['path', 'footway', 'cycleway', 'steps', 'pedestrian'], 0.9, 6, 0.12, 'rgba(127,223,255,.7)', 3),
      ...roadLayerPair('mid', ['tertiary'], 1.2, 8, 0.24, FUG, 5),
      ...roadLayerPair('primary', ['primary', 'secondary'], 2.6, 15, 0.3, FUG_BRIGHT, 6),
      ...roadLayerPair('major', ['motorway', 'trunk'], 3, 18, 0.35, FUG_BRIGHT, 8),

      // 3D 建築：fill-extrusion，高度依 render_height，顏色對比拉高（低樓 #0d2350 → 高樓 #2a7fff，
      // 頂面較亮，CONTRACT_R2.md §3），hide_3d 為真的（橋樑等）不擠出。
      {
        id: 'building-3d', type: 'fill-extrusion', source: 'openmaptiles', 'source-layer': 'building', minzoom: 13,
        filter: ['!=', ['get', 'hide_3d'], true],
        paint: {
          'fill-extrusion-height': ['coalesce', ['to-number', ['get', 'render_height']], 8],
          'fill-extrusion-base': ['coalesce', ['to-number', ['get', 'render_min_height']], 0],
          'fill-extrusion-color': [
            'interpolate', ['linear'], ['coalesce', ['to-number', ['get', 'render_height']], 8],
            0, '#0d2350', 30, '#1a4590', 70, '#2a7fff', 130, '#7fc0ff',
          ],
          'fill-extrusion-opacity': 0.92,
          'fill-extrusion-vertical-gradient': true,
        },
      },
      // 建築外框發光線（CONTRACT_R2.md §3）：zoom≥15 才畫，取 building 多邊形外框當 line 圖層來源。
      {
        id: 'building-outline-glow', type: 'line', source: 'openmaptiles', 'source-layer': 'building', minzoom: 15,
        filter: ['!=', ['get', 'hide_3d'], true],
        layout: { 'line-join': 'round' },
        paint: { 'line-color': FUG, 'line-opacity': 0.35, 'line-blur': 1, 'line-width': 1 },
      },

      // 標籤：只留 zoom≥14 的道路名與地標（POI），一律隱藏國家／省市／遠方城市等 place 標籤
      // （CONTRACT_R2.md §3——place-label 圖層整層移除，不再顯示任何 place class）。
      {
        id: 'road-label', type: 'symbol', source: 'openmaptiles', 'source-layer': 'transportation_name', minzoom: 14,
        layout: {
          'symbol-placement': 'line', 'text-field': ['coalesce', ['get', 'name'], ['get', 'name:latin']],
          'text-font': ['Noto Sans Regular'], 'text-size': 11, 'text-letter-spacing': 0.05,
        },
        paint: { 'text-color': LABEL, 'text-halo-color': BG, 'text-halo-width': 1.4, 'text-opacity': 0.85 },
      },
      // 地標（POI）標籤：只在 zoom≥14、只挑有名字且排序較前面（rank 較小＝較重要）的點，避免密密麻麻。
      {
        id: 'poi-label', type: 'symbol', source: 'openmaptiles', 'source-layer': 'poi', minzoom: 14,
        filter: ['all', ['has', 'name'], ['<=', ['coalesce', ['get', 'rank'], 10], 15]],
        layout: {
          'text-field': ['coalesce', ['get', 'name'], ['get', 'name:latin']],
          'text-font': ['Noto Sans Regular'], 'text-size': 10.5, 'text-max-width': 6,
        },
        paint: { 'text-color': LABEL, 'text-halo-color': BG, 'text-halo-width': 1.2, 'text-opacity': 0.8 },
      },
    ],
  } as unknown as StyleSpecification
}
