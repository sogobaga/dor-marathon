'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminTestWhitelistApi } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

export default function AdminSettingsPage() {
  const router = useRouter()
  const [token, setTok] = useState<string | null>(null)
  const [emails, setEmails] = useState<string[] | null>(null)
  const [input, setInput] = useState('')
  const [err, setErr] = useState('')

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t)
    adminTestWhitelistApi.list(t)
      .then((r) => setEmails(r.emails))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
      })
  }, [router])

  async function add() {
    const v = input.trim().toLowerCase()
    if (!token || !v) return
    try {
      await adminTestWhitelistApi.add(token, v)
      setEmails((es) => (es && !es.includes(v) ? [...es, v].sort() : es))
      setInput('')
    } catch (e: any) { setErr(e?.message || '新增失敗') }
  }
  async function remove(email: string) {
    if (!token) return
    try {
      await adminTestWhitelistApi.remove(token, email)
      setEmails((es) => es?.filter((x) => x !== email) ?? es)
    } catch (e: any) { setErr(e?.message || '刪除失敗') }
  }

  return (
    <div style={{ maxWidth: 640 }}>
      <h1 style={{ margin: '0 0 6px', fontSize: 24, fontWeight: 800 }}>全域測試白名單</h1>
      <p style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 0 }}>
        名單內的會員 email，可在前台看到所有「賽事測試中」狀態的賽事（再加上各賽事自己的白名單）。
      </p>

      <div style={{ display: 'flex', gap: 8, margin: '16px 0' }}>
        <input
          value={input} onChange={(e) => setInput(e.target.value)} placeholder="someone@example.com"
          onKeyDown={(e) => { if (e.key === 'Enter') add() }}
          style={{ flex: 1, maxWidth: 340, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 12px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit' }}
        />
        <button onClick={add} style={{ background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14 }}>＋ 加入</button>
      </div>

      {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginBottom: 12 }}>{err}</div>}
      {!emails && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
      {emails && emails.length === 0 && <div style={{ color: 'var(--tx-dim)' }}>尚無預設白名單</div>}
      {emails && emails.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {emails.map((e) => (
            <div key={e} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 14px' }}>
              <span style={{ fontSize: 14 }}>{e}</span>
              <button onClick={() => remove(e)} style={{ background: 'rgba(255,80,80,.08)', color: 'var(--hunt)', border: '1px solid rgba(255,80,80,.25)', borderRadius: 8, padding: '5px 12px', cursor: 'pointer', fontSize: 13 }}>移除</button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
