'use client'

import { useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminPushApi } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

export default function AdminPushPage() {
  const router = useRouter()
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [url, setUrl] = useState('')
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<{ sent: number; failed: number } | null>(null)
  const [err, setErr] = useState('')

  async function send() {
    const token = getToken()
    if (!token) { router.replace('/admin/login'); return }
    if (!title.trim() || !body.trim()) { setErr('請填寫標題與內容'); return }
    setSending(true); setErr(''); setResult(null)
    try {
      const r = await adminPushApi.broadcast(token, { title: title.trim(), body: body.trim(), url: url.trim() || undefined })
      setResult(r)
    } catch (e: any) {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '發送失敗')
    } finally {
      setSending(false)
    }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 8px', fontSize: 24, fontWeight: 800 }}>推播通知</h1>
      <p style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7, margin: '0 0 16px', maxWidth: 640 }}>
        發送 Web Push 通知給所有已訂閱的會員。<b>點擊網址</b> 留空預設導向首頁 <code>/</code>。
      </p>

      <div style={card}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <F label="標題"><input style={inp} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="例：新賽事開放報名！" /></F>
          <F label="內容"><textarea style={{ ...inp, minHeight: 80, resize: 'vertical' }} value={body} onChange={(e) => setBody(e.target.value)} placeholder="通知內文" /></F>
          <F label="點擊網址（可留空，預設 /）"><input style={inp} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="/races/xxx" /></F>
        </div>
        <div style={{ display: 'flex', gap: 10, marginTop: 14, alignItems: 'center' }}>
          <button onClick={send} disabled={sending} style={primaryBtn}>{sending ? '發送中…' : '發送給全部訂閱者'}</button>
          {result && (
            <span style={{ fontSize: 13, color: 'var(--tx-dim)' }}>
              已發送 <b style={{ color: 'var(--fug)' }}>{result.sent}</b>、失敗 <b style={{ color: result.failed > 0 ? 'var(--hunt)' : 'var(--tx-dim)' }}>{result.failed}</b>
            </span>
          )}
        </div>
        {err && <div style={{ color: 'var(--hunt)', marginTop: 10, fontSize: 13 }}>{err}</div>}
      </div>
    </div>
  )
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
      <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit', boxSizing: 'border-box' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 18px', cursor: 'pointer', fontSize: 14, fontFamily: 'inherit' }
const card: React.CSSProperties = { border: '1px solid var(--line)', borderRadius: 14, padding: 18, marginTop: 14, marginBottom: 4, maxWidth: 560 }
