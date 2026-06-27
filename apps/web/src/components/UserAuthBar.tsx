'use client'

import { useEffect, useState } from 'react'
import { GoogleLogin } from '@react-oauth/google'
import { authApi, type User } from '@/lib/api'
import { getUser, setUserSession, clearUserSession } from '@/lib/userAuth'
import { googleConfigured } from './GoogleAuthProvider'

export default function UserAuthBar({ onProfile }: { onProfile?: () => void }) {
  const [user, setUser] = useState<User | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setUser(getUser())
  }, [])

  function logout() {
    clearUserSession()
    setUser(null)
  }

  // 已登入：顯示名稱 + 登出
  if (user) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        {user.avatar_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={user.avatar_url} alt="" width={26} height={26} style={{ borderRadius: 999 }} />
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

  // 未設定 Client ID
  if (!googleConfigured) {
    return <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>Google 登入尚未設定</span>
  }

  // 未登入：Google 登入按鈕
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4 }}>
      <div style={{ opacity: busy ? 0.5 : 1, pointerEvents: busy ? 'none' : 'auto' }}>
        <GoogleLogin
          onSuccess={async (cred) => {
            if (!cred.credential) {
              setErr('未取得 Google 憑證')
              return
            }
            setErr('')
            setBusy(true)
            try {
              const res = await authApi.google(cred.credential)
              setUserSession(res.tokens.access_token, res.tokens.refresh_token, res.user)
              setUser(res.user)
            } catch (e: any) {
              setErr(e?.message || '登入失敗')
            } finally {
              setBusy(false)
            }
          }}
          onError={() => setErr('Google 登入失敗')}
          type="icon"
          shape="circle"
        />
      </div>
      {err && <span style={{ fontSize: 10, color: 'var(--hunt)' }}>{err}</span>}
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
