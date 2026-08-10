'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { authApi } from '@/lib/api'
import { setSession, getToken, ensureValidToken } from '@/lib/adminAuth'

export default function AdminLogin() {
  const router = useRouter()
  const [email, setEmail] = useState('admin')
  const [password, setPassword] = useState('')
  const [keepLogin, setKeepLogin] = useState(false) // 保持登入（預設不勾，安全優先）
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(true) // 進頁先檢查是否已有有效 session，避免表單閃現又跳走

  // 已勾過「保持登入」→ localStorage 還有 refresh token 時，開這頁應直接導去後台，
  // 而不是永遠顯示登入表單（AdminChrome 在 isLogin 分支故意跳過續期，所以要在這裡自己做）。
  useEffect(() => {
    let alive = true
    ;(async () => {
      await ensureValidToken() // 有 refresh 且 access 快過期/已過期 → 自動續期
      if (!alive) return
      if (getToken()) {
        router.replace('/admin/races')
        return // 導開後不切換 checking，避免表單在導頁瞬間閃現
      }
      setChecking(false)
    })()
    return () => { alive = false }
  }, [router])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      const res = await authApi.login({ email, password })
      // 勾「保持登入」→ 存 refresh token（可自動續期、30 天內免登入）；否則只在本次分頁有效
      setSession(res.tokens.access_token, res.tokens.refresh_token, keepLogin)
      router.push('/admin/races')
    } catch (e: any) {
      setErr(e?.message || '登入失敗')
    } finally {
      setBusy(false)
    }
  }

  if (checking) {
    return <div style={{ minHeight: '100vh' }} />
  }

  return (
    <div style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', padding: 20 }}>
      <form
        onSubmit={submit}
        style={{
          width: 360,
          background: 'var(--bg-1)',
          border: '1px solid var(--line)',
          borderRadius: 18,
          padding: 28,
          display: 'flex',
          flexDirection: 'column',
          gap: 16,
        }}
      >
        <div>
          <div style={{ fontSize: 11, letterSpacing: '.18em', color: 'var(--fug)', fontWeight: 600 }}>
            DOR · CONSOLE
          </div>
          <h1 style={{ margin: '6px 0 0', fontSize: 22, fontWeight: 800 }}>營運後台登入</h1>
        </div>

        <Field label="帳號">
          <input value={email} onChange={(e) => setEmail(e.target.value)} style={inputStyle} type="text" autoComplete="username" />
        </Field>
        <Field label="密碼">
          <input value={password} onChange={(e) => setPassword(e.target.value)} style={inputStyle} type="password" autoComplete="current-password" />
        </Field>

        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--tx-dim)', cursor: 'pointer' }}>
          <input type="checkbox" checked={keepLogin} onChange={(e) => setKeepLogin(e.target.checked)} style={{ width: 16, height: 16 }} />
          保持登入（本機 30 天內免再登入）
        </label>

        {err && <div style={{ color: 'var(--hunt)', fontSize: 13 }}>{err}</div>}

        <button type="submit" disabled={busy} style={btnStyle}>
          {busy ? '登入中…' : '登入'}
        </button>
      </form>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
        {label}
      </span>
      {children}
    </label>
  )
}

const inputStyle: React.CSSProperties = {
  background: 'var(--bg-2)',
  border: '1px solid var(--line-2)',
  borderRadius: 10,
  padding: '10px 12px',
  color: 'var(--tx)',
  fontSize: 14,
}

const btnStyle: React.CSSProperties = {
  background: 'var(--fug)',
  color: 'var(--fug-ink)',
  fontWeight: 700,
  border: 'none',
  borderRadius: 10,
  padding: '11px 16px',
  cursor: 'pointer',
  fontSize: 14,
}
