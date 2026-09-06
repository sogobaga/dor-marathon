'use client'

// 運動部「揮汗有禮・全民動起來」500.gov.tw 活動：截圖模式（screenshot mode）畫面。
//
// 2026-09-06 規則變動（owner 定案，取代舊的 canvas 產「證明圖」lib/runProof.ts——已刪除）：
// 500.gov.tw 只收手機系統截圖鍵截出的 App「原始紀錄」畫面，未裁切、看得到日期與達標數值；
// 退件四種情形：①裁切／拼貼過的圖片 ②非 App 畫面的照片（翻拍手錶等） ③文字編輯或另外產生的圖
// ④手動輸入的數據。舊的 canvas 產圖正是③，不管畫得多像原始畫面都必被退件。
// 這個元件本身「就是」App 的畫面——不產生任何圖片，只負責把這趟跑步的日期/時間/距離等數據
// 用一個乾淨、非捲動、塞得進一般手機螢幕（iPhone SE 375×667 ～ 大機型 430×932）的版面呈現出來，
// 由使用者自己按手機的系統截圖鍵擷取整個畫面。
//
// 達標邏輯見 lib/gov500.ts（單次 5 公里或 30 分鐘擇一即可）；呼叫端只在達標時才會給出開啟這個畫面
// 的入口（見 track/page.tsx、track/history/page.tsx 的 gov500 區塊），所以這裡不再重複擋「未達標」。
//
// 全螢幕覆蓋層掛載慣例：overlayMount()+createPortal（見 lib/overlayMount.ts 與
// components/runmeet/RunMeetThreadModal.tsx 同款「整頁」風格，非置中卡片）。刻意不套 data-skin="default"
// ——保留跟隨使用者目前 skin（暗黑/warm）走 var(--bg)/var(--tx)，因為這畫面本身會被截圖，應該長得
// 跟使用者平常看到的 App 介面一致，而不是強制切成另一種固定風格。
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { overlayMount } from '@/lib/overlayMount'
import { loadLeaflet } from '@/lib/leaflet'
import { GOV500_DISTANCE_KM, GOV500_TIME_S, markGov500Shot } from '@/lib/gov500'

export interface RunProofScreenProps {
  startedAt: Date
  endedAt: Date | null // 有值時優先算「運動時間」= ended-started；沒有（例如尚無精確結束時戳）才退回 durationS
  durationS: number
  movingS?: number | null // 移動時間（與總運動時間差在 5 秒內就不重複顯示，避免兩個幾乎相同的數字）
  distanceKm: number
  avgPaceS: number | null
  displayName: string
  recordId?: string | null // 紀錄 id（有的話取前 8 碼當「紀錄編號」，純顯示無驗證意義）
  source?: string // 紀錄來源文案，預設「App GPS 即時追蹤」
  runKey: string // 見 lib/gov500.ts gov500RunKey：本週已截圖標記用的鍵
  // GPS 軌跡（2026-09-06 owner 加碼需求）：多段 [lat,lng] 陣列，段落之間本身不相連
  // （斷訊/跳點期間被排除的段落，見 lib/polyline.ts decodePolylineSegments 註解）。
  // 有值且至少一段非空才畫地圖；沒給或整個是空陣列就不畫（維持舊版純文字版面）。
  track?: [number, number][][]
  onClose: () => void
}

const WEEKDAY_ZH = ['日', '一', '二', '三', '四', '五', '六']
const p2 = (n: number) => String(n).padStart(2, '0')

function fmtDateBig(d: Date): string {
  return `${d.getFullYear()}/${p2(d.getMonth() + 1)}/${p2(d.getDate())}（${WEEKDAY_ZH[d.getDay()]}）`
}
function fmtHm(d: Date): string {
  return `${p2(d.getHours())}:${p2(d.getMinutes())}`
}
function fmtDuration(s: number): string {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  return h > 0 ? `${h}:${p2(m)}:${p2(sec)}` : `${p2(m)}:${p2(sec)}`
}
function fmtPace(s: number): string {
  const m = Math.floor(s / 60), sec = Math.round(s % 60)
  return `${m}:${p2(sec)} /km`
}

export default function RunProofScreen({
  startedAt, endedAt, durationS, movingS, distanceKm, avgPaceS, displayName, recordId, source, runKey, track, onClose,
}: RunProofScreenProps) {
  const om = overlayMount()
  const [marked, setMarked] = useState(false)

  const totalS = endedAt ? Math.max(0, Math.round((endedAt.getTime() - startedAt.getTime()) / 1000)) : Math.max(0, durationS)
  const showMoving = movingS != null && movingS > 0 && Math.abs(movingS - totalS) >= 5
  const hasTrack = !!track && track.some((seg) => seg.length > 0)

  function handleUpload() {
    markGov500Shot(runKey)
    setMarked(true)
    window.open('https://500.gov.tw/registrant/', '_blank', 'noopener')
  }

  // ── 375×667（iPhone SE/8，最窄機型）高度預算，含地圖後最壞情況（平均配速／移動時間都有顯示）──
  // header                          10(padding)+16.8(單行content)+3(padding)             ≈  30.0
  // middle padding（上下各 2px）                                                          ≈   4.0
  // middle gap 10px × 5 個間距（date/map/時間/距離/配速/來源 共 6 塊）                        ≈  50.0
  // 日期區塊：34*1.15(日期)+2(marginTop)+12*1.2(開始HH:mm)                                 ≈  55.5
  // 地圖（<800px 高機型用 120px，border-box 含邊框）                                        ≈ 120.0
  // 運動時間／距離各一列：11*1.2(label)+40*1.15(數字)+3(marginTop)+16.6(pill) ×2 列          ≈ 157.6
  // 配速/移動時間列：12.5*1.2                                                              ≈  15.0
  // 來源區塊：1(borderTop)+8(paddingTop)+13*1.2(姓名)+3(gap)+10.5*1.2(來源文字)              ≈  40.2
  // ── 以上 middle 小計 ≈ 442.3 ──
  // 下方浮動面板（上下 padding 8+12）：20+21.75(check清單)+15(截圖提示，已縮成一行)+33.6(上傳鈕)+36.4(關閉鈕) ≈ 126.8
  //   （SE 無 home indicator，safe-area-inset-bottom=0；"已標記本週截圖" 只在按過上傳鈕後才
  //    出現，屬使用者互動後的暫態，不計入初始版面預算）
  // 合計 ≈ 30 + 442.3 + 126.8 ≈ 599px ＜ 667px（375×667 約 68px 餘裕；審查用 headless Chrome 實測過舊版兩行提示
  //   也仍不溢出。真機 PingFang 字高略不同，仍建議用最矮機型截圖確認一次）
  // 390×844／430×932：地圖改 150px（≥800px 高），但機身高出許多，餘裕更大，不再重複列算式。
  const content = (
    // ⚠️ 目標是「非捲動」（塞得進 375×667～430×932），但仍留 overflow:auto 當安全網——
    // 極端情況（顯示名稱很長／裝置字體放大）寧可讓使用者多滑一下，也不要整段被裁掉截不到。
    <div style={{ position: om.position, inset: 0, zIndex: 3200, background: 'var(--bg)', color: 'var(--tx)', display: 'flex', flexDirection: 'column', overflow: 'auto' }}>
      <div style={{ flexShrink: 0, display: 'flex', alignItems: 'baseline', justifyContent: 'center', gap: 6, padding: '10px 16px 3px' }}>
        <span style={{ fontSize: 14, fontWeight: 900, color: 'var(--tx)' }}>DOR</span>
        <span style={{ fontSize: 11.5, color: 'var(--tx-faint)', fontWeight: 700 }}>城市探索 ・ 跑步紀錄</span>
      </div>

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 10, padding: '2px 22px' }}>
        <div style={{ textAlign: 'center' }}>
          <div style={{ fontSize: 34, fontWeight: 900, letterSpacing: '.01em', fontVariantNumeric: 'tabular-nums' }}>{fmtDateBig(startedAt)}</div>
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 2 }}>開始 {fmtHm(startedAt)}</div>
        </div>

        {hasTrack && <ProofMap track={track as [number, number][][]} />}

        <ProofRow label="運動時間" value={fmtDuration(totalS)} pill={totalS >= GOV500_TIME_S ? '✓ 達標 30 分鐘' : undefined} />
        <ProofRow label="距離" value={`${distanceKm.toFixed(2)} km`} pill={distanceKm >= GOV500_DISTANCE_KM ? '✓ 達標 5 公里' : undefined} />

        {(avgPaceS != null && avgPaceS > 0) || showMoving ? (
          <div style={{ display: 'flex', justifyContent: 'center', gap: 16, fontSize: 12.5, color: 'var(--tx-dim)', flexWrap: 'wrap' }}>
            {avgPaceS != null && avgPaceS > 0 && <span>平均配速 {fmtPace(avgPaceS)}</span>}
            {showMoving && <span>移動時間 {fmtDuration(movingS as number)}</span>}
          </div>
        ) : null}

        <div style={{ borderTop: '1px solid var(--line)', paddingTop: 8, textAlign: 'center', display: 'flex', flexDirection: 'column', gap: 3 }}>
          <div style={{ fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{displayName}</div>
          <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>
            紀錄來源：{source || 'DOR GPS 即時追蹤'}{recordId ? ` · 編號 ${recordId.slice(0, 8)}` : ''}
          </div>
        </div>
      </div>

      <div style={{ flexShrink: 0, background: 'var(--bg-2)', borderTop: '1px solid var(--line)', padding: '8px 16px calc(env(safe-area-inset-bottom,0px) + 12px)' }}>
        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', textAlign: 'center', lineHeight: 1.5, marginBottom: 6 }}>
          ☑ 看得到日期　☑ 看得到達標數值　☑ 整個手機畫面、未裁切
        </div>
        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', textAlign: 'center', lineHeight: 1.5, marginBottom: 8 }}>
          側邊鍵＋音量上鍵截圖整個畫面，不要裁切
        </div>
        <button onClick={handleUpload}
          style={{ width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px', fontSize: 13, cursor: 'pointer' }}>
          我截好了 → 前往 500.gov.tw 上傳
        </button>
        {marked && <div style={{ fontSize: 11.5, color: 'var(--fug)', textAlign: 'center', marginTop: 6 }}>✓ 已標記本週截圖</div>}
        <button onClick={onClose}
          style={{ width: '100%', marginTop: 6, background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px', fontSize: 12, cursor: 'pointer' }}>
          關閉
        </button>
      </div>
    </div>
  )

  return om.node ? createPortal(content, om.node) : content
}

function ProofRow({ label, value, pill }: { label: string; value: string; pill?: string }) {
  return (
    <div style={{ textAlign: 'center' }}>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 600 }}>{label}</div>
      <div style={{ fontSize: 40, fontWeight: 900, lineHeight: 1.15, fontVariantNumeric: 'tabular-nums' }}>{value}</div>
      {pill && (
        <span style={{ display: 'inline-block', marginTop: 3, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, fontSize: 10.5, borderRadius: 999, padding: '2px 9px' }}>
          {pill}
        </span>
      )}
    </div>
  )
}

// 靜態 GPS 軌跡地圖（2026-09-06 owner 加碼需求）：讓截圖看起來像真的運動 App 紀錄，而不只是數字卡片。
// 純展示用途、不可互動——dragging/zoom 全關，使用者截圖時手指誤觸也不會讓地圖跑掉。載入方式與
// track/history/page.tsx 相同（同一份共用 Leaflet CDN 載入器 lib/leaflet.ts、同一個 OSM tile URL），
// 只是這裡改用 ref 掛地圖容器（不用 id 字串）——避免 track 頁與這個全螢幕覆蓋層同時存在時 id 衝突。
function ProofMap({ track }: { track: [number, number][][] }) {
  const elRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<any>(null) // eslint-disable-line @typescript-eslint/no-explicit-any -- Leaflet 無型別套件，全檔慣例用 any
  const [failed, setFailed] = useState(false) // Leaflet 載入失敗（離線／CDN 被擋）：整塊地圖退回不畫，不留空殼佔位
  // 軌跡在覆蓋層開著的期間不會變（顯示的是同一趟），但呼叫端每次 render 都會傳新的陣列參照；若 effect 跟著 track
  // 重跑，父層任何無關的重繪（dashboard SWR、WS data_updated）都會把地圖整個拆掉重建、截圖瞬間閃一下（審查抓到）
  // → 掛載時抓一次存進 ref，effect 只跑一次；關閉再開是重新掛載，自然拿到新軌跡。
  const trackRef = useRef(track)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const L = await loadLeaflet()
        if (cancelled || !elRef.current) return
        const track = trackRef.current
        // 起終點標記與 fitBounds 都只用「畫得出線」的段（≥2 點）：單點段（被斷訊排除夾住的短段）若拿來當起終點，
        // 標記會落在視野外（審查抓到）
        const drawable = track.filter((seg) => seg.length > 1)
        const coords = drawable.flat()
        if (coords.length === 0) return // 沒有可畫的段：不建地圖（呼叫端已檢查 hasTrack，這裡多一層防呆）
        const map = L.map(elRef.current, {
          dragging: false, zoomControl: false, scrollWheelZoom: false, touchZoom: false,
          doubleClickZoom: false, boxZoom: false, keyboard: false, tap: false,
        }).setView(coords[0], 15)
        map.attributionControl.setPrefix(false) // 拿掉「Leaflet」字樣，只留 OSM 授權文字，維持小巧
        L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19, attribution: '© OpenStreetMap' }).addTo(map)
        // 品牌色 var(--fug) 依目前 skin（暗黑/warm）不同，執行期用 getComputedStyle 讀成實際 hex
        // 才能餵給 Leaflet（Leaflet 的 color 選項不支援 CSS 變數字串）
        const brand = getComputedStyle(document.documentElement).getPropertyValue('--fug').trim() || '#2DE59A'
        const lines = drawable.map((seg) => L.polyline(seg, { color: brand, weight: 4 }).addTo(map))
        if (lines.length > 0) {
          L.circleMarker(coords[0], { radius: 5, color: '#fff', weight: 2, fillColor: brand, fillOpacity: 1 }).addTo(map)
          L.circleMarker(coords[coords.length - 1], { radius: 5, color: '#fff', weight: 2, fillColor: '#ff5a5a', fillOpacity: 1 }).addTo(map)
          map.fitBounds(L.featureGroup(lines).getBounds(), { padding: [16, 16] })
        }
        mapRef.current = map
      } catch {
        // 地圖只是錦上添花，安靜放棄、退回無地圖版面，不影響其餘證明數據顯示、也不留空殼佔位
        if (!cancelled) setFailed(true)
      }
    })()
    return () => {
      cancelled = true
      if (mapRef.current) { mapRef.current.remove(); mapRef.current = null }
    }
  }, []) // 刻意只跑一次（見 trackRef 註解）

  if (failed) return null

  return (
    <>
      {/* 高度：≥800px 高機型（390×844、430×932）用 150px，較矮機型（375×667）用 120px，見上方高度預算註解 */}
      <style>{`.dor-proof-map{height:120px}@media (min-height:800px){.dor-proof-map{height:150px}}.dor-proof-map .leaflet-control-attribution{font-size:8px;line-height:1.4;padding:0 4px;background:rgba(255,255,255,.7)}`}</style>
      <div ref={elRef} className="dor-proof-map"
        style={{ width: '100%', boxSizing: 'border-box', borderRadius: 10, overflow: 'hidden', border: '1px solid var(--line)', background: 'var(--bg-2)' }} />
    </>
  )
}
