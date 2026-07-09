'use client'

import { useState } from 'react'
import { profileApi } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'

// 取消訂閱彈窗：顯示 VIP 到期時間 + 「到期後不再持續扣款」；確認後呼叫取消 API。
// 一般會員不會開到此彈窗（入口按鈕已 disabled）。
export default function CancelSubscriptionModal({ vipExpiresAt, onClose }: { vipExpiresAt?: string; onClose: () => void }) {
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const exp = vipExpiresAt ? vipExpiresAt.slice(0, 10) : '—'

  async function confirm() {
    if (!getUserToken()) return
    setBusy(true)
    try { await withUserAuth((t) => profileApi.vipCancel(t)) } catch { /* 無進行中訂閱也視為完成 */ }
    setBusy(false)
    setDone(true)
  }

  return (
    <div data-skin="default" onClick={onClose} style={overlay}>
      <div onClick={(e) => e.stopPropagation()} style={card}>
        <div style={{ fontSize: 18, fontWeight: 900, color: '#fff', textAlign: 'center' }}>取消訂閱</div>
        {done ? (
          <>
            <div style={{ fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.85, marginTop: 12, textAlign: 'center' }}>
              ✓ 已為你取消訂閱，<b>不會再自動扣款</b>。<br />VIP 權益將維持至 <b style={{ color: 'var(--gold)' }}>{exp}</b>，到期後自動降為一般會員。
            </div>
            <button onClick={onClose} style={primary}>我知道了</button>
          </>
        ) : (
          <>
            <div style={{ fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.85, marginTop: 12 }}>
              你的 VIP 到期時間：<b style={{ color: 'var(--gold)' }}>{exp}</b><br />
              取消訂閱後<b>不會再持續扣款</b>，VIP 權益維持至到期日，時間到即自動降為一般會員（VIP 限定功能將重新上鎖）。已扣款期數恕不退費。
            </div>
            <div style={{ display: 'flex', gap: 10, marginTop: 16 }}>
              <button onClick={onClose} style={ghost}>先不要</button>
              <button onClick={confirm} disabled={busy} style={{ ...primary, flex: 1, marginTop: 0, opacity: busy ? 0.6 : 1 }}>{busy ? '處理中…' : '確認取消訂閱'}</button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 3500, background: 'rgba(4,8,6,.88)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18 }
const card: React.CSSProperties = { width: '100%', maxWidth: 360, background: '#0b0e13', border: '1px solid var(--line-2)', borderRadius: 16, padding: '18px 16px', boxShadow: '0 16px 50px rgba(0,0,0,.7)' }
const primary: React.CSSProperties = { marginTop: 16, width: '100%', background: 'var(--gold)', color: '#fff', fontWeight: 900, border: 'none', borderRadius: 11, padding: '12px', fontSize: 14, cursor: 'pointer', fontFamily: 'inherit' }
const ghost: React.CSSProperties = { background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 11, padding: '12px 16px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit', flexShrink: 0 }
