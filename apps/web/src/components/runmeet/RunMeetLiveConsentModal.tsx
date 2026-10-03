'use client'

import { useEffect, useState } from 'react'
import { RunMeetModal, ghostBtn, modalActions, modalTitle, primaryBtn } from './ui'

// 團練同步跑・開跑前的同意視窗（契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §5／§8）。
//
// ⚠️ 殼一律用 ui.tsx 的 RunMeetModal：它 createPortal 到 overlayMount()（桌機被 .phone-shell 框住、
//    手機/獨立路由退回 body+fixed）、zIndex 3500（< 專注模式 3900，> 可拖曳面板 500）、data-skin="default"
//    固定亮字。本檔不自己定位任何東西，也不用 clientX/clientY——不會被桌機手機框的 transform 偏移影響。
// ⚠️ 這裡只負責「呈現三點說明＋勾選＋兩顆鈕」；要不要彈窗（localStorage 旗標）、確認後導頁
//    （router.push('/track?meet=<id>')）都由 RunMeetDetailView 決定，這個元件不碰 storage／router。
// ⚠️ 文案必須與實際行為一致（尤其隱私那一點）：起點隱私區的精確規則見契約 §5——開跑起點若在集合點附近
//    （≤ 300 m）就直接分享，否則離起點 ≥ 200 m 才開始分享、回到起點 200 m 內又停止分享。所以第 3 點寫成
//    「若不是從集合地點附近出發」，不能無條件宣稱「起點 200 公尺內一律不分享」（那會多承諾隱私）。
// ⚠️ 中文一律「團練」，不得寫「跑團」。

/** 金底白字（memory gold-bg-white-text：實心金底上的字一律 #fff；白字在亮金上偏淡，補一層細陰影維持可讀）。
 *  詳情頁的「🏃 開始跑步」鈕與本視窗的「同意並開始」共用同一個樣式。 */
export const liveGoBtn: React.CSSProperties = {
  ...primaryBtn, background: 'var(--gold)', color: '#fff', textShadow: '0 1px 2px rgba(0,0,0,.28)',
}

type Point = { head: string; body: string }

// 一般（會分享位置）的團練
const POINTS_SHARING: Point[] = [
  { head: '分享給誰', body: '只有同一個團練、同時正在跑步且也分享位置的成員，看得到你的名字與即時位置。位置只暫存在伺服器記憶體、不會保存；結束跑步或離開頁面即刪除，連線異常中斷也會在數分鐘內自動清除。' },
  { head: 'GPS 與螢幕', body: '需要用手機 GPS 全程記錄，並保持螢幕常亮（鎖定模式）。按側邊鍵關閉螢幕時同步會暫停，夥伴會看到你「訊號中斷」。' },
  { head: '隱私保護', body: '若你不是從集合地點附近出發（例如從家裡出發），起點 200 公尺內、以及之後回到起點 200 公尺內，都不會分享你的位置。你也可以隨時暫停分享（暫停期間你也看不到夥伴）。' },
]

// 不限地點（presence_only）的團練：伺服器永不收座標，只算在跑人數
const POINTS_PRESENCE: Point[] = [
  { head: '只算人數', body: '不限地點團練只顯示「正在跑步的人數」。你的位置不會被傳送或分享，伺服器也不會收到你的座標。' },
  { head: 'GPS 與螢幕', body: '跑步照常用手機 GPS 記錄，請保持 GPS 開啟與螢幕常亮（鎖定模式）。按側邊鍵關閉螢幕時，人數同步會暫停。' },
  { head: '結束即停', body: '你結束跑步或離開頁面後，就不再被計入在跑人數。' },
]

export default function RunMeetLiveConsentModal({
  meetTitle, presenceOnly, onCancel, onConfirm,
}: {
  meetTitle: string
  presenceOnly: boolean
  onCancel: () => void
  /** dontRemind＝使用者有勾「這個團練不再提醒」；寫 localStorage 與導頁由呼叫端處理。 */
  onConfirm: (dontRemind: boolean) => void
}) {
  const [dontRemind, setDontRemind] = useState(false)
  const [going, setGoing] = useState(false) // 防連點：同意後呼叫端會導頁，這之間不要再觸發一次
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onCancel() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onCancel])

  const points = presenceOnly ? POINTS_PRESENCE : POINTS_SHARING
  return (
    <RunMeetModal onClose={onCancel} maxWidth={360}>
      <div role="dialog" aria-modal="true" aria-labelledby="run-meet-live-consent-title">
        <div id="run-meet-live-consent-title" style={modalTitle}>🏃 開始團練同步跑</div>
        {meetTitle && (
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', textAlign: 'center', marginTop: 6, lineHeight: 1.6, wordBreak: 'break-word' }}>{meetTitle}</div>
        )}
        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.7, marginTop: 10 }}>開跑前請先確認以下三點：</div>

        <ol style={{ listStyle: 'none', margin: '10px 0 0', padding: 0, display: 'flex', flexDirection: 'column', gap: 10 }}>
          {points.map((p, i) => (
            <li key={p.head} style={{ display: 'flex', gap: 10, alignItems: 'flex-start' }}>
              <span aria-hidden="true" style={{ flexShrink: 0, width: 22, height: 22, marginTop: 1, borderRadius: '50%', background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--fug)', fontSize: 12, fontWeight: 900, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>{i + 1}</span>
              <span style={{ flex: 1, minWidth: 0, fontSize: 13, color: 'var(--tx)', lineHeight: 1.7, wordBreak: 'break-word' }}>
                <b style={{ color: '#fff', marginRight: 6 }}>{p.head}</b>{p.body}
              </span>
            </li>
          ))}
        </ol>

        <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 14, padding: '6px 0', fontSize: 13, color: 'var(--tx)', cursor: 'pointer', userSelect: 'none' }}>
          <input
            type="checkbox" checked={dontRemind} onChange={(e) => setDontRemind(e.target.checked)}
            style={{ width: 18, height: 18, flexShrink: 0, accentColor: 'var(--gold)', cursor: 'pointer' }}
          />
          這個團練不再提醒
        </label>

        <div style={modalActions}>
          <button type="button" onClick={onCancel} style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}>取消</button>
          <button
            type="button" disabled={going}
            onClick={() => { if (going) return; setGoing(true); onConfirm(dontRemind) }}
            style={{ ...liveGoBtn, opacity: going ? 0.7 : 1 }}
          >同意並開始</button>
        </div>
      </div>
    </RunMeetModal>
  )
}
