'use client'

import { useState } from 'react'
import { GoogleLogin } from '@react-oauth/google'
import { authApi } from '@/lib/api'
import { setUserSession, clearUserSession, useUser } from '@/lib/userAuth'
import { googleConfigured } from './GoogleAuthProvider'

export default function UserAuthBar({ onProfile }: { onProfile?: () => void }) {
  const user = useUser()
  const [showLogin, setShowLogin] = useState(false)

  function logout() {
    clearUserSession() // 觸發 useUser 更新
  }

  // 已登入：顯示名稱 + 登出
  if (user) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        {user.avatar_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={user.avatar_url} alt="" style={{ width: 26, height: 26, borderRadius: '50%', objectFit: 'cover', display: 'block', flexShrink: 0 }} />
        ) : (
          <div style={avatar}>{(user.name || 'U').slice(0, 1)}</div>
        )}
        <button onClick={onProfile} style={{ ...logoutBtn, color: 'var(--tx)', maxWidth: 110, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title="個人資訊">
          {user.name}
        </button>
        <button onClick={logout} style={logoutBtn}>登出</button>
      </div>
    )
  }

  // 未登入：顯示「登入」按鈕，點擊跳出登入視窗
  return (
    <>
      <button onClick={() => setShowLogin(true)} style={loginBtn}>登入</button>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
    </>
  )
}

// 登入彈窗：預設提供 Google 登入，未來可在此擴充其他第三方
export function LoginModal({ onClose }: { onClose: () => void }) {
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  return (
    <div style={overlay} onClick={onClose}>
      <div style={panel} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
          <strong style={{ fontSize: 18 }}>登入 DOR</strong>
          <button onClick={onClose} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 18 }}>✕</button>
        </div>
        <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '0 0 18px' }}>選擇登入方式以報名賽事</p>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, opacity: busy ? 0.5 : 1, pointerEvents: busy ? 'none' : 'auto' }}>
          {googleConfigured ? (
            <GoogleLogin
              onSuccess={async (cred) => {
                if (!cred.credential) { setErr('未取得 Google 憑證'); return }
                setErr(''); setBusy(true)
                try {
                  const res = await authApi.google(cred.credential)
                  setUserSession(res.tokens.access_token, res.tokens.refresh_token, res.user) // 觸發 useUser 更新
                  onClose()
                } catch (e: any) {
                  setErr(e?.message || '登入失敗')
                } finally {
                  setBusy(false)
                }
              }}
              onError={() => setErr('Google 登入失敗')}
              width="280"
            />
          ) : (
            <div style={{ fontSize: 13, color: 'var(--tx-faint)', padding: '10px 0' }}>Google 登入尚未設定</div>
          )}
          {/* 未來其他第三方登入按鈕可加在這裡 */}
        </div>

        {err && <div style={{ fontSize: 12, color: 'var(--hunt)', marginTop: 12 }}>{err}</div>}
      </div>
    </div>
  )
}

const avatar: React.CSSProperties = {
  width: 26, height: 26, borderRadius: 999, background: 'var(--bg-2)', border: '1px solid var(--line-2)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, color: 'var(--tx)',
}
const logoutBtn: React.CSSProperties = {
  background: 'rgba(255,255,255,.05)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 8, padding: '4px 10px', cursor: 'pointer', fontSize: 12,
}
const loginBtn: React.CSSProperties = {
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 9, padding: '7px 16px', cursor: 'pointer', fontSize: 13,
}
const overlay: React.CSSProperties = {
  // zIndex 需高於首頁/追蹤頁的可拖曳資訊面板(500)與地圖橫幅(1001)，否則登入視窗會被面板蓋住而點不到；
  // 仍低於事件演出(2000+)與系統級提示(InAppBrowser 3000 / Dedup 3200)。
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1200, padding: 20,
}
const panel: React.CSSProperties = {
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16,
  padding: 22, width: '100%', maxWidth: 340,
}
