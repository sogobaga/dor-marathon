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
import { useState } from 'react'
import { createPortal } from 'react-dom'
import { overlayMount } from '@/lib/overlayMount'
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
  startedAt, endedAt, durationS, movingS, distanceKm, avgPaceS, displayName, recordId, source, runKey, onClose,
}: RunProofScreenProps) {
  const om = overlayMount()
  const [marked, setMarked] = useState(false)

  const totalS = endedAt ? Math.max(0, Math.round((endedAt.getTime() - startedAt.getTime()) / 1000)) : Math.max(0, durationS)
  const showMoving = movingS != null && movingS > 0 && Math.abs(movingS - totalS) >= 5

  function handleUpload() {
    markGov500Shot(runKey)
    setMarked(true)
    window.open('https://500.gov.tw/registrant/', '_blank', 'noopener')
  }

  const content = (
    // ⚠️ 目標是「非捲動」（塞得進 375×667～430×932），但仍留 overflow:auto 當安全網——
    // 極端情況（顯示名稱很長／裝置字體放大）寧可讓使用者多滑一下，也不要整段被裁掉截不到。
    <div style={{ position: om.position, inset: 0, zIndex: 3200, background: 'var(--bg)', color: 'var(--tx)', display: 'flex', flexDirection: 'column', overflow: 'auto' }}>
      <div style={{ flexShrink: 0, display: 'flex', alignItems: 'baseline', justifyContent: 'center', gap: 6, padding: '16px 16px 4px' }}>
        <span style={{ fontSize: 14, fontWeight: 900, color: 'var(--tx)' }}>DOR</span>
        <span style={{ fontSize: 11.5, color: 'var(--tx-faint)', fontWeight: 700 }}>城市探索 ・ 跑步紀錄</span>
      </div>

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 14, padding: '4px 22px' }}>
        <div style={{ textAlign: 'center' }}>
          <div style={{ fontSize: 30, fontWeight: 900, letterSpacing: '.01em', fontVariantNumeric: 'tabular-nums' }}>{fmtDateBig(startedAt)}</div>
          <div style={{ fontSize: 13, color: 'var(--tx-faint)', marginTop: 4 }}>開始 {fmtHm(startedAt)}</div>
        </div>

        <ProofRow label="運動時間" value={fmtDuration(totalS)} pill={totalS >= GOV500_TIME_S ? '✓ 達標 30 分鐘' : undefined} />
        <ProofRow label="距離" value={`${distanceKm.toFixed(2)} km`} pill={distanceKm >= GOV500_DISTANCE_KM ? '✓ 達標 5 公里' : undefined} />

        {(avgPaceS != null && avgPaceS > 0) || showMoving ? (
          <div style={{ display: 'flex', justifyContent: 'center', gap: 16, fontSize: 12.5, color: 'var(--tx-dim)', flexWrap: 'wrap' }}>
            {avgPaceS != null && avgPaceS > 0 && <span>平均配速 {fmtPace(avgPaceS)}</span>}
            {showMoving && <span>移動時間 {fmtDuration(movingS as number)}</span>}
          </div>
        ) : null}

        <div style={{ borderTop: '1px solid var(--line)', paddingTop: 10, textAlign: 'center', display: 'flex', flexDirection: 'column', gap: 3 }}>
          <div style={{ fontSize: 13.5, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{displayName}</div>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
            紀錄來源：{source || 'App GPS 即時追蹤'}{recordId ? ` · 編號 ${recordId.slice(0, 8)}` : ''}
          </div>
        </div>
      </div>

      <div style={{ flexShrink: 0, background: 'var(--bg-2)', borderTop: '1px solid var(--line)', padding: '10px 18px calc(env(safe-area-inset-bottom,0px) + 16px)' }}>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', textAlign: 'center', lineHeight: 1.7, marginBottom: 8 }}>
          ☑ 看得到日期　☑ 看得到達標數值　☑ 整個手機畫面、未裁切
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', textAlign: 'center', lineHeight: 1.6, marginBottom: 10 }}>
          用手機截圖鍵擷取整個畫面（iPhone：側邊鍵＋音量上；Android：電源＋音量下），不要裁切
        </div>
        <button onClick={handleUpload}
          style={{ width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '11px', fontSize: 13.5, cursor: 'pointer' }}>
          我截好了 → 前往 500.gov.tw 上傳
        </button>
        {marked && <div style={{ fontSize: 11.5, color: 'var(--fug)', textAlign: 'center', marginTop: 8 }}>✓ 已標記本週截圖</div>}
        <button onClick={onClose}
          style={{ width: '100%', marginTop: 8, background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '9px', fontSize: 13, cursor: 'pointer' }}>
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
      <div style={{ fontSize: 12, color: 'var(--tx-faint)', fontWeight: 600 }}>{label}</div>
      <div style={{ fontSize: 38, fontWeight: 900, lineHeight: 1.15, fontVariantNumeric: 'tabular-nums' }}>{value}</div>
      {pill && (
        <span style={{ display: 'inline-block', marginTop: 3, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, fontSize: 11, borderRadius: 999, padding: '3px 10px' }}>
          {pill}
        </span>
      )}
    </div>
  )
}
