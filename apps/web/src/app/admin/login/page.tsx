'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { authApi } from '@/lib/api'
import { setToken } from '@/lib/adminAuth'
import { loadCreds, saveCreds, clearCreds } from '@/lib/adminRemember'

export default function AdminLogin() {
  const router = useRouter()
  const [email, setEmail] = useState('admin')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  // 若之前勾過「記住密碼」，載入時解密還原帳密並自動勾選
  useEffect(() => {
    loadCreds().then((c) => { if (c) { setEmail(c.email || 'admin'); setPassword(c.password); setRemember(true) } })
  }, [])

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      const res = await authApi.login({ email, password })
      if (remember) await saveCreds(email, password); else clearCreds() // 只有勾選才加密暫存；取消勾選則清除
      setToken(res.tokens.access_token)
      router.push('/admin/races')
    } catch (e: any) {
      setErr(e?.message || '登入失敗')
    } finally {
      setBusy(false)
    }
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
          <input type="checkbox" checked={remember} onChange={(e) => { setRemember(e.target.checked); if (!e.target.checked) clearCreds() }} style={{ width: 16, height: 16 }} />
          記住密碼（僅存於本機、加密保存）
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
  color: '#05140e',
  fontWeight: 700,
  border: 'none',
  borderRadius: 10,
  padding: '11px 16px',
  cursor: 'pointer',
  fontSize: 14,
}
